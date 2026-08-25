package controller

import (
	"errors"
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRelayRetryPolicySeparatesSameChannelRetriesAndSwitches(t *testing.T) {
	originalSame := common.SameChannelRetryTimes
	originalCross := common.CrossChannelRetryTimes
	originalBudget := common.RetryTimeBudgetSeconds
	t.Cleanup(func() {
		common.SameChannelRetryTimes = originalSame
		common.CrossChannelRetryTimes = originalCross
		common.RetryTimeBudgetSeconds = originalBudget
	})
	common.SameChannelRetryTimes = 5
	common.CrossChannelRetryTimes = 2
	common.RetryTimeBudgetSeconds = 90

	policy := newRelayRetryPolicy()
	serverError := &types.NewAPIError{Err: errors.New("upstream unavailable"), StatusCode: http.StatusServiceUnavailable}
	quotaError := &types.NewAPIError{Err: errors.New("quota exhausted"), StatusCode: http.StatusTooManyRequests}

	for i := 0; i < 5; i++ {
		require.True(t, policy.retrySameChannel(serverError))
		policy.markSameChannelRetry()
	}
	assert.False(t, policy.retrySameChannel(serverError))
	assert.False(t, policy.retrySameChannel(quotaError))

	require.True(t, policy.canSwitchChannel())
	policy.switchChannel(6)
	assert.True(t, policy.excludedChannels[6])
	assert.Zero(t, policy.sameChannelRetries)

	require.True(t, policy.canSwitchChannel())
	policy.switchChannel(7)
	assert.True(t, policy.excludedChannels[7])
	assert.False(t, policy.canSwitchChannel())
}
