package model

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func prepareBatchDrainTest(t *testing.T) {
	t.Helper()
	truncateTables(t)
	resetBatchUpdateTestState(t)
	uncertainBatchWrites = nil
	t.Cleanup(func() { uncertainBatchWrites = nil })
}

func TestBatchDrainPersistsQuotaAndUsageExactlyOnce(t *testing.T) {
	prepareBatchDrainTest(t)
	user := createReserveTestUser(t, 100)
	token := createReserveTestToken(t, 100)
	channel := Channel{Name: "batch-drain", Status: 1}
	require.NoError(t, DB.Create(&channel).Error)
	addNewRecord(BatchUpdateTypeUserQuota, user.Id, -20)
	addNewRecord(BatchUpdateTypeUsedQuota, user.Id, 20)
	addNewRecord(BatchUpdateTypeRequestCount, user.Id, 1)
	addNewRecord(BatchUpdateTypeTokenQuota, token.Id, -20)
	addNewRecord(BatchUpdateTypeChannelUsedQuota, channel.Id, 20)
	require.NoError(t, FlushBatchUpdates(context.Background()))
	require.NoError(t, FlushBatchUpdates(context.Background()))
	require.NoError(t, DB.First(&user, user.Id).Error)
	require.NoError(t, DB.First(&channel, channel.Id).Error)
	token = getTokenFromDB(t, token.Id)
	assert.Equal(t, 80, user.Quota)
	assert.Equal(t, 20, user.UsedQuota)
	assert.Equal(t, 1, user.RequestCount)
	assert.Equal(t, 80, token.RemainQuota)
	assert.Equal(t, 20, token.UsedQuota)
	assert.Equal(t, int64(20), channel.UsedQuota)
}

func TestBatchDrainWaitsForSwappedBatchAndFlushesLaterRefund(t *testing.T) {
	prepareBatchDrainTest(t)
	user := createReserveTestUser(t, 100)
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	callback := "test:hold_batch_drain"
	require.NoError(t, DB.Callback().Update().Before("gorm:update").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table == "users" {
			once.Do(func() {
				close(started)
				<-release
			})
		}
	}))
	t.Cleanup(func() { DB.Callback().Update().Remove(callback) })
	addNewRecord(BatchUpdateTypeUserQuota, user.Id, -20)
	finished := make(chan error, 1)
	go func() { finished <- FlushBatchUpdates(context.Background()) }()
	<-started
	// The first batch has left the queue, but its write is not complete.
	addNewRecord(BatchUpdateTypeUserQuota, user.Id, 5)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	assert.ErrorIs(t, FlushBatchUpdates(ctx), context.Canceled)
	close(release)
	require.NoError(t, <-finished)
	require.NoError(t, FlushBatchUpdates(context.Background()))
	assert.Equal(t, 85, getUserQuotaFromDB(t, user.Id))
}

func TestBatchDrainDoesNotReplayAnUncertainCommittedIncrement(t *testing.T) {
	prepareBatchDrainTest(t)
	token := createReserveTestToken(t, 100)
	callback := "test:lost_batch_acknowledgement"
	require.NoError(t, DB.Callback().Update().After("gorm:commit_or_rollback_transaction").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table == "tokens" {
			tx.AddError(errors.New("simulated lost commit acknowledgement"))
		}
	}))
	t.Cleanup(func() { DB.Callback().Update().Remove(callback) })
	addNewRecord(BatchUpdateTypeTokenQuota, token.Id, -20)
	require.ErrorContains(t, FlushBatchUpdates(context.Background()), "unconfirmed database writes")
	require.NoError(t, DB.Callback().Update().Remove(callback))
	require.ErrorContains(t, FlushBatchUpdates(context.Background()), "unconfirmed database writes")
	token = getTokenFromDB(t, token.Id)
	assert.Equal(t, 80, token.RemainQuota)
	assert.Equal(t, 20, token.UsedQuota)
}
