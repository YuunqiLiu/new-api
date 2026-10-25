package common

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChannelTimeGateBoundaries(t *testing.T) {
	t.Setenv("NEXEVIC_GLM_PEAK_CHANNEL_IDS", "6,7,14,18")
	for _, tc := range []struct {
		instant string
		blocked bool
	}{
		{"2026-10-18T05:29:59Z", false},
		{"2026-10-18T05:30:00Z", true},
		{"2026-10-18T09:59:59Z", true},
		{"2026-10-18T10:00:00Z", false},
		{"2026-10-19T13:30:00+08:00", true},
	} {
		t.Run(tc.instant, func(t *testing.T) {
			now, err := time.Parse(time.RFC3339, tc.instant)
			require.NoError(t, err)
			for _, id := range []int{6, 7, 14, 18} {
				assert.Equal(t, tc.blocked, IsChannelTimeBlockedAt(id, now), "channel %d", id)
			}
			assert.False(t, IsChannelTimeBlockedAt(3, now))
			assert.False(t, IsChannelTimeBlockedAt(15, now))
		})
	}
}

func TestChannelTimeGateUnconfiguredPreservesRouting(t *testing.T) {
	t.Setenv("NEXEVIC_GLM_PEAK_CHANNEL_IDS", "")
	assert.False(t, IsChannelTimeBlockedAt(6, time.Date(2026, 10, 18, 6, 0, 0, 0, time.UTC)))
}
