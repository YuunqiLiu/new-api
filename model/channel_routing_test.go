package model

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestChannelRoutingExclusionsAndPeakWithSQLite(t *testing.T) {
	oldDB, oldMemory := DB, common.MemoryCacheEnabled
	oldGroups, oldChannels := group2model2channels, channelsIDM
	t.Cleanup(func() {
		DB, common.MemoryCacheEnabled = oldDB, oldMemory
		group2model2channels, channelsIDM = oldGroups, oldChannels
	})
	t.Setenv("NEXEVIC_GLM_PEAK_CHANNEL_IDS", "6,7,14,18")
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "routing.db")), &gorm.Config{})
	require.NoError(t, err)
	DB = db
	require.NoError(t, DB.AutoMigrate(&Channel{}, &Ability{}))
	channels := []*Channel{
		{Id: 6, Status: common.ChannelStatusEnabled, Group: "default", Models: "glm-5.3", Priority: common.GetPointer(int64(100)), Weight: common.GetPointer(uint(30))},
		{Id: 3, Status: common.ChannelStatusEnabled, Group: "default", Models: "glm-5.3", Priority: common.GetPointer(int64(50)), Weight: common.GetPointer(uint(30))},
		{Id: 15, Status: common.ChannelStatusEnabled, Group: "default", Models: "glm-5.3", Priority: common.GetPointer(int64(50)), Weight: common.GetPointer(uint(40))},
		{Id: 20, Status: common.ChannelStatusEnabled, Group: "default", Models: "glm-5.3", Priority: common.GetPointer(int64(10)), Weight: common.GetPointer(uint(1))},
		{Id: 21, Status: common.ChannelStatusManuallyDisabled, Group: "default", Models: "glm-5.3", Priority: common.GetPointer(int64(200))},
	}
	for _, channel := range channels {
		require.NoError(t, DB.Create(channel).Error)
		// Keep the disabled channel's ability deliberately stale to verify that
		// both branches check current channel status as well as the ability.
		require.NoError(t, DB.Create(&Ability{Group: "default", Model: "glm-5.3", ChannelId: channel.Id, Enabled: true, Priority: channel.Priority}).Error)
	}
	channelsIDM = make(map[int]*Channel)
	for _, channel := range channels {
		channelsIDM[channel.Id] = channel
	}
	group2model2channels = map[string]map[string][]int{"default": {"glm-5.3": {6, 3, 15, 20, 21}}}
	for _, cached := range []bool{false, true} {
		common.MemoryCacheEnabled = cached
		for _, tc := range []struct {
			name     string
			now      time.Time
			excluded map[int]bool
			ids      []int
		}{
			{"off_peak", time.Date(2026, 10, 18, 5, 29, 0, 0, time.UTC), nil, []int{6, 3, 15, 20}},
			{"peak", time.Date(2026, 10, 18, 5, 30, 0, 0, time.UTC), nil, []int{3, 15, 20}},
			{"exclude_tier", time.Date(2026, 10, 18, 5, 30, 0, 0, time.UTC), map[int]bool{3: true, 15: true}, []int{20}},
			{"exhausted", time.Date(2026, 10, 18, 5, 30, 0, 0, time.UTC), map[int]bool{3: true, 15: true, 20: true}, []int{}},
		} {
			candidates, err := getChannelRoutingCandidatesAt("default", "glm-5.3", "/v1/chat/completions", tc.excluded, tc.now)
			require.NoError(t, err, "cache=%v case=%s", cached, tc.name)
			ids := make([]int, 0, len(candidates))
			for _, channel := range candidates {
				ids = append(ids, channel.Id)
			}
			assert.Equal(t, tc.ids, ids, "cache=%v case=%s", cached, tc.name)
			selected := selectRoutingChannel(candidates, 0)
			if len(candidates) == 0 {
				assert.Nil(t, selected)
			} else {
				require.NotNil(t, selected)
				assert.Equal(t, candidates[0].GetPriority(), selected.GetPriority())
			}
		}
		selected, err := GetRandomSatisfiedChannelExcluding("default", "glm-5.3", 0, "/v1/chat/completions", map[int]bool{6: true, 3: true, 15: true})
		require.NoError(t, err)
		require.NotNil(t, selected)
		assert.Equal(t, 20, selected.Id, "cache=%v must propagate exclusions through the public selector", cached)
	}
}

func TestChannelRoutingZeroWeightDoesNotOverridePositiveWeight(t *testing.T) {
	channels := []*Channel{
		{Id: 1, Priority: common.GetPointer(int64(10)), Weight: common.GetPointer(uint(0))},
		{Id: 2, Priority: common.GetPointer(int64(10)), Weight: common.GetPointer(uint(40))},
		{Id: 3, Priority: common.GetPointer(int64(1)), Weight: common.GetPointer(uint(100))},
	}
	assert.Equal(t, 2, selectRoutingChannel(channels, 0).Id)
	assert.Equal(t, 3, selectRoutingChannel(channels, 1).Id)
	assert.Equal(t, 3, selectRoutingChannel(channels, 99).Id)
	assert.Nil(t, selectRoutingChannel(nil, 0))
}
