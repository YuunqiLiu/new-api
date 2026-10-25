package model

import (
	"errors"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

// ChannelRecoverySnapshot identifies one particular system-disable generation.
// Every manual status operation, including an idempotent one, invalidates it.
type ChannelRecoverySnapshot struct {
	ChannelID int   `json:"channel_id"`
	Version   int64 `json:"status_version"`
}

func SnapshotChannelRecovery(channel *Channel) (ChannelRecoverySnapshot, bool) {
	if channel == nil || channel.Status != common.ChannelStatusAutoDisabled {
		return ChannelRecoverySnapshot{}, false
	}
	return ChannelRecoverySnapshot{ChannelID: channel.Id, Version: channel.StatusVersion}, true
}

// RecoverChannelStatus atomically checks the disable generation. The status and
// ability changes commit together; a stale probe never overwrites manual work.
func RecoverChannelStatus(snapshot ChannelRecoverySnapshot, usingKey string) (bool, error) {
	if common.IsChannelTimeBlocked(snapshot.ChannelID) {
		return false, nil
	}
	return changeChannelStatus(snapshot.ChannelID, usingKey, common.ChannelStatusEnabled, "", &snapshot.Version, true)
}

// QuarantineChannelStatus is used only after an explicit account quota error.
// expectedVersion protects siblings and scheduler observations from manual edits.
func QuarantineChannelStatus(channelID int, expectedVersion int64, reason string) (bool, error) {
	return changeChannelStatus(channelID, "", common.ChannelStatusAutoDisabled, reason, &expectedVersion, false)
}

func changeChannelStatus(channelID int, usingKey string, status int, reason string, expectedVersion *int64, recovery bool) (bool, error) {
	if common.MemoryCacheEnabled {
		channelStatusLock.Lock()
		defer channelStatusLock.Unlock()
	}
	pollingLock := GetChannelPollingLock(channelID)
	pollingLock.Lock()
	defer pollingLock.Unlock()

	changed := false
	err := DB.Transaction(func(tx *gorm.DB) error {
		var channel Channel
		if err := lockForUpdate(tx).First(&channel, channelID).Error; err != nil {
			return err
		}
		if expectedVersion != nil && channel.StatusVersion != *expectedVersion {
			return nil
		}
		if recovery && (channel.Status != common.ChannelStatusAutoDisabled || common.IsChannelTimeBlocked(channelID)) {
			return nil
		}
		// A late real-request failure must not convert an administrator's stop
		// into an automatically recoverable stop.
		if status == common.ChannelStatusAutoDisabled && channel.Status == common.ChannelStatusManuallyDisabled {
			return nil
		}
		if channel.StatusVersion == int64(^uint64(0)>>1) {
			return errors.New("channel status version exhausted")
		}
		previousStatus, previousVersion := channel.Status, channel.StatusVersion
		if channel.ChannelInfo.IsMultiKey {
			if usingKey != "" {
				found := false
				for _, key := range channel.GetKeys() {
					if key == usingKey {
						found = true
						break
					}
				}
				if !found {
					return nil
				}
			}
			handlerMultiKeyUpdate(&channel, usingKey, status, reason)
		} else {
			channel.Status = status
		}
		info := channel.GetOtherInfo()
		info["status_reason"] = reason
		info["status_time"] = common.GetTimestamp()
		channel.SetOtherInfo(info)
		updates := map[string]any{
			"status":         channel.Status,
			"status_version": previousVersion + 1,
			"other_info":     channel.OtherInfo,
		}
		if channel.ChannelInfo.IsMultiKey {
			updates["channel_info"] = channel.ChannelInfo
		}
		result := tx.Model(&Channel{}).Where("id = ? AND status = ? AND status_version = ?", channelID, previousStatus, previousVersion).Updates(updates)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return nil
		}
		if err := tx.Model(&Ability{}).Where("channel_id = ?", channelID).Update("enabled", channel.Status == common.ChannelStatusEnabled).Error; err != nil {
			return err
		}
		changed = true
		return nil
	})
	if err != nil {
		return false, err
	}
	if changed {
		InitChannelCache()
	}
	return changed, nil
}

// AdoptLegacySchedulerDisable only migrates an explicitly verified v1 scheduler
// record. It never treats the mere presence of a local JSON entry as ownership.
func AdoptLegacySchedulerDisable(channelID int, expectedVersion int64, disabledAt int64, expectedReason string, recoveredReason string) (bool, error) {
	if disabledAt <= 0 || expectedReason == "" {
		return false, nil
	}
	changed := false
	err := DB.Transaction(func(tx *gorm.DB) error {
		var channel Channel
		if err := lockForUpdate(tx).First(&channel, channelID).Error; err != nil {
			return err
		}
		if channel.Status != common.ChannelStatusManuallyDisabled || channel.StatusVersion != expectedVersion {
			return nil
		}
		originalInfo := channel.OtherInfo
		info := channel.GetOtherInfo()
		// Legacy status APIs stored the fixed reason and a second-resolution
		// timestamp, while the scheduler stored the upstream reason separately.
		statusTime, validTime := info["status_time"].(float64)
		if info["status_reason"] != expectedReason || !validTime || int64(statusTime) != disabledAt {
			return nil
		}
		info["status_reason"] = recoveredReason
		channel.SetOtherInfo(info)
		result := tx.Model(&Channel{}).Where("id = ? AND status = ? AND status_version = ? AND other_info = ?", channelID, common.ChannelStatusManuallyDisabled, expectedVersion, originalInfo).Updates(map[string]any{
			"status":         common.ChannelStatusAutoDisabled,
			"status_version": expectedVersion + 1,
			"other_info":     channel.OtherInfo,
		})
		if result.Error != nil {
			return result.Error
		}
		changed = result.RowsAffected == 1
		return nil
	})
	if err == nil && changed {
		InitChannelCache()
	}
	return changed, err
}
