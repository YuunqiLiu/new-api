package service

import (
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChannelRoutingAutoGroupsPreserveExclusions(t *testing.T) {
	for _, cached := range []bool{false, true} {
		t.Run(map[bool]string{false: "database", true: "cache"}[cached], func(t *testing.T) {
			db := setupChannelSelectAutoGroupsTest(t)
			t.Setenv("NEXEVIC_GLM_PEAK_CHANNEL_IDS", "")
			createChannelSelectAutoGroupsChannel(t, db, 2101, "vip", "routing-test")
			createChannelSelectAutoGroupsChannel(t, db, 2102, "vip", "routing-test")
			createChannelSelectAutoGroupsChannel(t, db, 2103, "default", "routing-test")
			model.InitChannelCache()
			common.MemoryCacheEnabled = cached
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			common.SetContextKey(ctx, constant.ContextKeyUserGroup, "default")
			common.SetContextKey(ctx, constant.ContextKeyTokenAutoGroups, []string{"vip", "default"})
			channel, group, err := CacheGetRandomSatisfiedChannel(&RetryParam{
				Ctx: ctx, TokenGroup: "auto", ModelName: "routing-test", RequestPath: "/v1/chat/completions",
				ExcludedChannelIds: map[int]bool{2101: true, 2102: true},
			})
			require.NoError(t, err)
			require.NotNil(t, channel)
			assert.Equal(t, 2103, channel.Id)
			assert.Equal(t, "default", group)
		})
	}
}

func TestChannelRoutingAffinityCannotStickToLowerPriority(t *testing.T) {
	db := setupChannelSelectAutoGroupsTest(t)
	t.Setenv("NEXEVIC_GLM_PEAK_CHANNEL_IDS", "")
	createChannelSelectAutoGroupsChannel(t, db, 2101, "default", "routing-test")
	createChannelSelectAutoGroupsChannel(t, db, 2102, "default", "routing-test")
	require.NoError(t, db.Model(&model.Channel{}).Where("id = ?", 2101).Update("priority", 100).Error)
	require.NoError(t, db.Model(&model.Ability{}).Where("channel_id = ?", 2101).Update("priority", 100).Error)
	model.InitChannelCache()
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	for _, cached := range []bool{false, true} {
		common.MemoryCacheEnabled = cached
		channel, _, err := GetUsableAffinityChannel(ctx, "routing-test", "default", "/v1/chat/completions", 2102)
		require.NoError(t, err)
		assert.Nil(t, channel, "a prior pay-as-you-go fallback cannot override a recovered higher tier")
		channel, group, err := GetUsableAffinityChannel(ctx, "routing-test", "default", "/v1/chat/completions", 2101)
		require.NoError(t, err)
		require.NotNil(t, channel)
		assert.Equal(t, 2101, channel.Id)
		assert.Equal(t, "default", group)
	}
}

func TestChannelRoutingAffinityRespectsAutoGroupOrder(t *testing.T) {
	db := setupChannelSelectAutoGroupsTest(t)
	t.Setenv("NEXEVIC_GLM_PEAK_CHANNEL_IDS", "")
	createChannelSelectAutoGroupsChannel(t, db, 2101, "vip", "routing-test")
	createChannelSelectAutoGroupsChannel(t, db, 2102, "default", "routing-test")
	model.InitChannelCache()
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	common.SetContextKey(ctx, constant.ContextKeyUserGroup, "default")
	common.SetContextKey(ctx, constant.ContextKeyTokenAutoGroups, []string{"vip", "default"})
	channel, group, err := GetUsableAffinityChannel(ctx, "routing-test", "auto", "/v1/chat/completions", 2102)
	require.NoError(t, err)
	assert.Nil(t, channel)
	assert.Equal(t, "vip", group)
}
