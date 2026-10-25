package common

import (
	"errors"
	"os"
	"strconv"
	"strings"
	"time"
)

// Shanghai has used UTC+8 without daylight saving since 1991. A fixed zone keeps
// this deployment policy independent of the host timezone and tzdata packaging.
var channelRoutingShanghai = time.FixedZone("Asia/Shanghai", 8*60*60)

var ErrChannelTimeBlocked = errors.New("channel is unavailable during its scheduled exclusion window")

// IsChannelTimeBlocked is shared by request routing and administrative probes.
// An unset NEXEVIC_GLM_PEAK_CHANNEL_IDS preserves the previous routing behavior.
func IsChannelTimeBlocked(channelID int) bool {
	return IsChannelTimeBlockedAt(channelID, time.Now())
}

// IsChannelTimeBlockedAt makes the daily [13:30, 18:00) boundary testable without
// changing the system clock. Configure the deployment with IDs 6,7,14,18.
func IsChannelTimeBlockedAt(channelID int, now time.Time) bool {
	if channelID <= 0 {
		return false
	}
	local := now.In(channelRoutingShanghai)
	minute := local.Hour()*60 + local.Minute()
	if minute < 13*60+30 || minute >= 18*60 {
		return false
	}
	for _, value := range strings.Split(os.Getenv("NEXEVIC_GLM_PEAK_CHANNEL_IDS"), ",") {
		id, err := strconv.Atoi(strings.TrimSpace(value))
		if err == nil && id == channelID {
			return true
		}
	}
	return false
}
