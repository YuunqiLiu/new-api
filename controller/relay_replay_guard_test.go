package controller

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	taskdto "github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRelayRetryStopsAfterResponseStarts(t *testing.T) {
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	require.True(t, canReplayRelayRequest(ctx))
	_, err := ctx.Writer.WriteString("data: {\"choices\":[{\"delta\":{\"content\":\"first\"}}]}\n\n")
	require.NoError(t, err)
	assert.False(t, canReplayRelayRequest(ctx))
	upstreamErr := types.NewErrorWithStatusCode(errors.New("upstream stream failed"), types.ErrorCodeGetChannelFailed, http.StatusServiceUnavailable)
	assert.False(t, shouldRetry(ctx, upstreamErr, 2))
	assert.False(t, shouldRetryTaskRelay(ctx, 1, &taskdto.TaskError{StatusCode: http.StatusServiceUnavailable}, 2))
}

func TestRelayRetryStopsAfterClientCancellation(t *testing.T) {
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	requestContext, cancel := context.WithCancel(context.Background())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil).WithContext(requestContext)
	require.True(t, canReplayRelayRequest(ctx))
	cancel()
	assert.False(t, canReplayRelayRequest(ctx))
}

func TestRelayRetryTimeBudgetUsesControlledClock(t *testing.T) {
	previous := common.RetryTimeBudgetSeconds
	t.Cleanup(func() { common.RetryTimeBudgetSeconds = previous })
	common.RetryTimeBudgetSeconds = 90
	start := time.Date(2026, 10, 18, 0, 0, 0, 0, time.UTC)
	now := start.Add(89 * time.Second)
	policy := newRelayRetryPolicy()
	policy.startedAt = start
	policy.now = func() time.Time { return now }
	assert.True(t, policy.withinTimeBudget())
	now = start.Add(90 * time.Second)
	assert.False(t, policy.withinTimeBudget())
}
