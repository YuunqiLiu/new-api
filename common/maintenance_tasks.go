package common

import (
	"context"
	"sync"
)

var maintenanceTasks struct {
	mu       sync.Mutex
	stopping bool
	pending  int
	idle     chan struct{}
}

// BeginMaintenanceTask prevents new process-wide maintenance work once drain
// begins, while allowing already-running work to finish normally.
func BeginMaintenanceTask() (done func(), accepted bool) {
	maintenanceTasks.mu.Lock()
	defer maintenanceTasks.mu.Unlock()
	if maintenanceTasks.stopping {
		return nil, false
	}
	if maintenanceTasks.pending == 0 {
		maintenanceTasks.idle = make(chan struct{})
	}
	maintenanceTasks.pending++
	var once sync.Once
	return func() {
		once.Do(func() {
			maintenanceTasks.mu.Lock()
			defer maintenanceTasks.mu.Unlock()
			maintenanceTasks.pending--
			if maintenanceTasks.pending == 0 {
				close(maintenanceTasks.idle)
			}
		})
	}, true
}

func StopMaintenanceTaskAdmission() {
	maintenanceTasks.mu.Lock()
	maintenanceTasks.stopping = true
	maintenanceTasks.mu.Unlock()
}

func WaitForMaintenanceTasks(ctx context.Context) error {
	maintenanceTasks.mu.Lock()
	pending, idle := maintenanceTasks.pending, maintenanceTasks.idle
	maintenanceTasks.mu.Unlock()
	if pending == 0 {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-idle:
		return nil
	}
}
