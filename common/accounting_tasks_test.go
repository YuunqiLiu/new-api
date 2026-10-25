package common

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAccountingDrainWaitsForQueuedRefund(t *testing.T) {
	var drain accountingTaskDrain
	release := make(chan struct{})
	drain.start(func() error {
		<-release
		return nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, drain.wait(ctx), context.Canceled)
	close(release)
	require.NoError(t, drain.wait(context.Background()))
}

func TestAccountingDrainRetainsFailureWithoutReplayingRefund(t *testing.T) {
	var drain accountingTaskDrain
	calls := 0
	drain.start(func() error {
		calls++
		return errors.New("database acknowledgement unavailable")
	})
	require.ErrorContains(t, drain.wait(context.Background()), "reconciliation required")
	require.ErrorContains(t, drain.wait(context.Background()), "reconciliation required")
	assert.Equal(t, 1, calls)
}
