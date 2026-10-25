package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestChannelRecoveryRejectsManualAndNewDisableGeneration(t *testing.T) {
	setupChannelStatusTest(t)
	t.Setenv("NEXEVIC_GLM_PEAK_CHANNEL_IDS", "")
	channel := Channel{Name: "recover", Key: "test-key", Status: common.ChannelStatusAutoDisabled, Models: "test-model", Group: "default"}
	require.NoError(t, DB.Create(&channel).Error)
	stale, ok := SnapshotChannelRecovery(&channel)
	require.True(t, ok)
	require.True(t, UpdateChannelStatus(channel.Id, "", common.ChannelStatusManuallyDisabled, "manual operation"))
	changed, err := RecoverChannelStatus(stale, "")
	require.NoError(t, err)
	assert.False(t, changed)
	require.True(t, UpdateChannelStatus(channel.Id, "", common.ChannelStatusEnabled, "manual operation"))
	require.True(t, UpdateChannelStatus(channel.Id, "", common.ChannelStatusAutoDisabled, "new quota error"))
	changed, err = RecoverChannelStatus(stale, "")
	require.NoError(t, err)
	assert.False(t, changed, "a new system disable cannot be recovered by an earlier probe")
	stored, err := GetChannelById(channel.Id, true)
	require.NoError(t, err)
	assert.Equal(t, common.ChannelStatusAutoDisabled, stored.Status)
	assert.Greater(t, stored.StatusVersion, stale.Version)
}

func TestChannelRecoveryIsSingleUseAndPreservesConfiguration(t *testing.T) {
	setupChannelStatusTest(t)
	t.Setenv("NEXEVIC_GLM_PEAK_CHANNEL_IDS", "")
	channel := Channel{Name: "recover-once", Key: "test-key", Status: common.ChannelStatusAutoDisabled, Models: "model-a", Group: "default", UsedQuota: 100}
	require.NoError(t, DB.Create(&channel).Error)
	require.NoError(t, DB.Create(&Ability{ChannelId: channel.Id, Model: "model-a", Group: "default", Enabled: false}).Error)
	snapshot, ok := SnapshotChannelRecovery(&channel)
	require.True(t, ok)
	require.NoError(t, DB.Model(&Channel{}).Where("id = ?", channel.Id).Updates(map[string]any{"key": "rotated-test-key", "used_quota": gorm.Expr("used_quota + 200"), "models": "model-b"}).Error)
	changed, err := RecoverChannelStatus(snapshot, "")
	require.NoError(t, err)
	assert.True(t, changed)
	changed, err = RecoverChannelStatus(snapshot, "")
	require.NoError(t, err)
	assert.False(t, changed, "another worker cannot apply the same recovery twice")
	stored, err := GetChannelById(channel.Id, true)
	require.NoError(t, err)
	assert.Equal(t, "rotated-test-key", stored.Key)
	assert.Equal(t, int64(300), stored.UsedQuota)
	assert.Equal(t, "model-b", stored.Models)
	var ability Ability
	require.NoError(t, DB.Where("channel_id = ?", channel.Id).First(&ability).Error)
	assert.True(t, ability.Enabled)
}

func TestManualStopSurvivesLateQuotaFailure(t *testing.T) {
	setupChannelStatusTest(t)
	channel := Channel{Name: "manual", Key: "test-key", Status: common.ChannelStatusManuallyDisabled}
	require.NoError(t, DB.Create(&channel).Error)
	assert.False(t, UpdateChannelStatus(channel.Id, "", common.ChannelStatusAutoDisabled, "late upstream error"))
	require.True(t, UpdateChannelStatus(channel.Id, "", common.ChannelStatusManuallyDisabled, "manual operation"))
	stored, err := GetChannelById(channel.Id, true)
	require.NoError(t, err)
	assert.Equal(t, common.ChannelStatusManuallyDisabled, stored.Status)
	assert.Equal(t, int64(1), stored.StatusVersion, "idempotent manual actions invalidate previous observations")
}

func TestLegacyRecoveryAdoptionRequiresUnchangedOwnership(t *testing.T) {
	setupChannelStatusTest(t)
	channel := Channel{Name: "legacy", Key: "test-key", Status: common.ChannelStatusManuallyDisabled}
	channel.SetOtherInfo(map[string]interface{}{"status_reason": "manual operation", "status_time": int64(1792300000)})
	require.NoError(t, DB.Create(&channel).Error)
	const adoptedReason = "[account_quota reset_at=1792470834] adopted legacy quota scheduler"
	changed, err := AdoptLegacySchedulerDisable(channel.Id, 0, 1792300001, "manual operation", adoptedReason)
	require.NoError(t, err)
	assert.False(t, changed)
	changed, err = AdoptLegacySchedulerDisable(channel.Id, 0, 1792300000, "manual operation", adoptedReason)
	require.NoError(t, err)
	assert.True(t, changed)
	stored, err := GetChannelById(channel.Id, true)
	require.NoError(t, err)
	assert.Equal(t, adoptedReason, stored.GetOtherInfo()["status_reason"])
	changed, err = AdoptLegacySchedulerDisable(channel.Id, 0, 1792300000, "manual operation", adoptedReason)
	require.NoError(t, err)
	assert.False(t, changed)
}

func TestChannelRecoveryGenericUpdatesPreserveNewManualStatus(t *testing.T) {
	setupChannelStatusTest(t)
	for _, method := range []string{"update", "save"} {
		t.Run(method, func(t *testing.T) {
			channel := Channel{Name: "stale-" + method, Key: "test-key", Status: common.ChannelStatusEnabled, Models: "test-model", Group: "default"}
			require.NoError(t, DB.Create(&channel).Error)
			require.True(t, UpdateChannelStatus(channel.Id, "", common.ChannelStatusManuallyDisabled, "manual operation"))
			channel.Name = "edited"
			if method == "update" {
				require.NoError(t, channel.Update())
			} else {
				require.NoError(t, channel.Save())
			}
			stored, err := GetChannelById(channel.Id, true)
			require.NoError(t, err)
			assert.Equal(t, common.ChannelStatusManuallyDisabled, stored.Status)
			assert.Equal(t, "manual operation", stored.GetOtherInfo()["status_reason"])
		})
	}
}

func TestChannelRecoveryTagEditInvalidatesProbe(t *testing.T) {
	setupChannelStatusTest(t)
	t.Setenv("NEXEVIC_GLM_PEAK_CHANNEL_IDS", "")
	tag, newTag, newModels := "before", "after", "changed-model"
	channel := Channel{Name: "tag-edit", Key: "test-key", Status: common.ChannelStatusAutoDisabled, Models: "original-model", Group: "default", Tag: &tag}
	require.NoError(t, DB.Create(&channel).Error)
	snapshot, ok := SnapshotChannelRecovery(&channel)
	require.True(t, ok)
	require.NoError(t, EditChannelByTag(tag, &newTag, nil, &newModels, nil, nil, nil, nil, nil))
	changed, err := RecoverChannelStatus(snapshot, "")
	require.NoError(t, err)
	assert.False(t, changed)
	stored, err := GetChannelById(channel.Id, true)
	require.NoError(t, err)
	assert.Equal(t, newModels, stored.Models)
	assert.Equal(t, int64(1), stored.StatusVersion)
}

func TestChannelRecoveryMalformedSettingsRepairPreservesState(t *testing.T) {
	setupChannelStatusTest(t)
	invalid := "{invalid"
	channel := Channel{Name: "invalid-setting", Key: "old-key", Status: common.ChannelStatusEnabled, Setting: &invalid, OtherSettings: invalid, UsedQuota: 10}
	require.NoError(t, DB.Create(&channel).Error)
	require.True(t, UpdateChannelStatus(channel.Id, "", common.ChannelStatusManuallyDisabled, "manual operation"))
	require.NoError(t, DB.Model(&Channel{}).Where("id = ?", channel.Id).Updates(map[string]any{"key": "rotated-key", "used_quota": 50}).Error)
	channel.GetSetting()
	channel.GetOtherSettings()
	stored, err := GetChannelById(channel.Id, true)
	require.NoError(t, err)
	assert.Equal(t, common.ChannelStatusManuallyDisabled, stored.Status)
	assert.Equal(t, "rotated-key", stored.Key)
	assert.Equal(t, int64(50), stored.UsedQuota)
}
