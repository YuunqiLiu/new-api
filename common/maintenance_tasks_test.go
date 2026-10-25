package common

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMaintenanceDrainStopsNewWorkAndWaitsForExistingWork(t *testing.T) {
	t.Cleanup(func() {
		maintenanceTasks.mu.Lock()
		maintenanceTasks.stopping = false
		maintenanceTasks.mu.Unlock()
	})
	done, accepted := BeginMaintenanceTask()
	require.True(t, accepted)
	defer done()
	StopMaintenanceTaskAdmission()
	_, accepted = BeginMaintenanceTask()
	assert.False(t, accepted)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	assert.ErrorIs(t, WaitForMaintenanceTasks(ctx), context.Canceled)
	done()
	require.NoError(t, WaitForMaintenanceTasks(context.Background()))
}
