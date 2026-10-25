package common

import (
	"context"
	"fmt"
	"sync"

	"github.com/bytedance/gopkg/util/gopool"
)

type accountingTaskDrain struct {
	mu      sync.Mutex
	pending int
	failed  int
	idle    chan struct{}
}

var accountingTasks accountingTaskDrain

// GoAccountingTask registers before dispatch, so a request may finish without
// hiding its still-queued refund from graceful shutdown.
func GoAccountingTask(fn func() error) {
	accountingTasks.start(fn)
}

func (d *accountingTaskDrain) start(fn func() error) {
	d.mu.Lock()
	if d.pending == 0 {
		d.idle = make(chan struct{})
	}
	d.pending++
	d.mu.Unlock()
	gopool.Go(func() {
		failed := true
		defer func() {
			d.mu.Lock()
			defer d.mu.Unlock()
			if failed {
				d.failed++
			}
			d.pending--
			if d.pending == 0 {
				close(d.idle)
			}
		}()
		failed = fn() != nil
	})
}

// WaitForAccountingTasks must follow HTTP and job-worker drain. A failed refund
// is not replayed, and prevents shutdown being reported as successful.
func WaitForAccountingTasks(ctx context.Context) error {
	return accountingTasks.wait(ctx)
}

func (d *accountingTaskDrain) wait(ctx context.Context) error {
	for {
		d.mu.Lock()
		pending, failed, idle := d.pending, d.failed, d.idle
		d.mu.Unlock()
		if pending == 0 {
			if failed != 0 {
				return fmt.Errorf("%d accounting tasks failed; reconciliation required before stopping this instance", failed)
			}
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-idle:
		}
	}
}
