package model

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"

	"github.com/bytedance/gopkg/util/gopool"
	"gorm.io/gorm"
)

const (
	BatchUpdateTypeUserQuota = iota
	BatchUpdateTypeTokenQuota
	BatchUpdateTypeUsedQuota
	BatchUpdateTypeChannelUsedQuota
	BatchUpdateTypeRequestCount
	BatchUpdateTypeCount // if you add a new type, you need to add a new map and a new lock
)

var batchUpdateStores []map[int]int
var batchUpdateLocks []sync.Mutex

// A swapped batch is still pending until all its database writes finish. The
// single writer also prevents a shutdown flush racing the periodic updater.
var batchUpdateWriter = make(chan struct{}, 1)

type uncertainBatchWrite struct {
	Kind   string
	ID     int
	Deltas []int
	Err    error
}

// An error can mean the server committed an increment but its acknowledgement
// was lost. Keep these records for reconciliation; never replay them blindly.
// Access is serialized by batchUpdateWriter.
var uncertainBatchWrites []uncertainBatchWrite
var batchFailureJournalName = fmt.Sprintf("batch-%d-%d.jsonl", os.Getpid(), time.Now().UnixNano())

func init() {
	for i := 0; i < BatchUpdateTypeCount; i++ {
		batchUpdateStores = append(batchUpdateStores, make(map[int]int))
		batchUpdateLocks = append(batchUpdateLocks, sync.Mutex{})
	}
}

func InitBatchUpdater() {
	gopool.Go(func() {
		for {
			time.Sleep(time.Duration(common.BatchUpdateInterval) * time.Second)
			batchUpdate()
		}
	})
}

func addNewRecord(type_ int, id int, value int) {
	batchUpdateLocks[type_].Lock()
	defer batchUpdateLocks[type_].Unlock()
	if _, ok := batchUpdateStores[type_][id]; !ok {
		batchUpdateStores[type_][id] = value
	} else {
		batchUpdateStores[type_][id] += value
	}
}

func batchUpdate() {
	if err := FlushBatchUpdates(context.Background()); err != nil {
		common.SysError("batch accounting requires reconciliation: " + err.Error())
	}
}

// FlushBatchUpdates waits for any in-progress batch and writes records queued
// since that batch started. Call after requests and asynchronous refunds drain.
// An uncertain write prevents a successful drain even on later empty flushes.
func FlushBatchUpdates(ctx context.Context) error {
	select {
	case batchUpdateWriter <- struct{}{}:
		defer func() { <-batchUpdateWriter }()
	case <-ctx.Done():
		return ctx.Err()
	}

	// check if there's any data to update
	hasData := false
	for i := 0; i < BatchUpdateTypeCount; i++ {
		batchUpdateLocks[i].Lock()
		if len(batchUpdateStores[i]) > 0 {
			hasData = true
			batchUpdateLocks[i].Unlock()
			break
		}
		batchUpdateLocks[i].Unlock()
	}

	if !hasData {
		return batchAccountingError()
	}

	common.SysLog("batch update started")
	stores := make([]map[int]int, BatchUpdateTypeCount)
	for i := 0; i < BatchUpdateTypeCount; i++ {
		batchUpdateLocks[i].Lock()
		stores[i] = batchUpdateStores[i]
		batchUpdateStores[i] = make(map[int]int)
		batchUpdateLocks[i].Unlock()
	}

	for i, store := range stores {
		if i == BatchUpdateTypeUserQuota || i == BatchUpdateTypeUsedQuota || i == BatchUpdateTypeRequestCount {
			continue
		}
		for key, value := range store {
			switch i {
			case BatchUpdateTypeTokenQuota:
				err := DB.WithContext(ctx).Model(&Token{}).Where("id = ?", key).Updates(map[string]interface{}{
					"remain_quota":  gorm.Expr("remain_quota + ?", value),
					"used_quota":    gorm.Expr("used_quota - ?", value),
					"accessed_time": common.GetTimestamp(),
				}).Error
				if err != nil {
					recordUncertainBatchWrite("token", key, []int{value}, err)
				}
			case BatchUpdateTypeChannelUsedQuota:
				err := DB.WithContext(ctx).Model(&Channel{}).Where("id = ?", key).
					Update("used_quota", gorm.Expr("used_quota + ?", value)).Error
				if err != nil {
					recordUncertainBatchWrite("channel", key, []int{value}, err)
				}
			}
		}
	}

	userQuotaStore := stores[BatchUpdateTypeUserQuota]
	usedQuotaStore := stores[BatchUpdateTypeUsedQuota]
	requestCountStore := stores[BatchUpdateTypeRequestCount]

	userIDs := make(map[int]struct{}, len(userQuotaStore)+len(usedQuotaStore)+len(requestCountStore))
	for key := range userQuotaStore {
		userIDs[key] = struct{}{}
	}
	for key := range usedQuotaStore {
		userIDs[key] = struct{}{}
	}
	for key := range requestCountStore {
		userIDs[key] = struct{}{}
	}
	for key := range userIDs {
		quota, used, count := userQuotaStore[key], usedQuotaStore[key], requestCountStore[key]
		if quota == 0 && used == 0 && count == 0 {
			continue
		}
		err := DB.WithContext(ctx).Model(&User{}).Where("id = ?", key).Updates(map[string]interface{}{
			"quota":         gorm.Expr("quota + ?", quota),
			"used_quota":    gorm.Expr("used_quota + ?", used),
			"request_count": gorm.Expr("request_count + ?", count),
		}).Error
		if err != nil {
			recordUncertainBatchWrite("user", key, []int{quota, used, count}, err)
		}
	}
	if err := batchAccountingError(); err != nil {
		return err
	}
	common.SysLog("batch update finished")
	return nil
}

func batchAccountingError() error {
	if len(uncertainBatchWrites) == 0 {
		return nil
	}
	return fmt.Errorf("%d unconfirmed database writes retained in memory; do not stop this instance or replay increments", len(uncertainBatchWrites))
}

func recordUncertainBatchWrite(kind string, id int, deltas []int, writeErr error) {
	uncertainBatchWrites = append(uncertainBatchWrites, uncertainBatchWrite{kind, id, deltas, writeErr})
	common.SysError(fmt.Sprintf("unconfirmed batch accounting write: kind=%s id=%d; retained for reconciliation, not replayed", kind, id))
	dir := os.Getenv("ACCOUNTING_FAILURE_DIR")
	if dir == "" {
		return
	}
	// Only numeric accounting identifiers and increments are persisted. Never
	// include raw database errors, credentials, request content or token keys.
	record := struct {
		Time   int64  `json:"time"`
		Kind   string `json:"kind"`
		ID     int    `json:"id"`
		Deltas []int  `json:"deltas"`
	}{time.Now().Unix(), kind, id, deltas}
	data, err := common.Marshal(record)
	if err == nil {
		err = os.MkdirAll(dir, 0700)
	}
	if err != nil {
		common.SysError("unable to persist accounting reconciliation record; retained in memory")
		return
	}
	file, err := os.OpenFile(filepath.Join(dir, batchFailureJournalName), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		common.SysError("unable to open accounting reconciliation journal; retained in memory")
		return
	}
	defer file.Close()
	if _, err = file.Write(append(data, '\n')); err == nil {
		err = file.Sync()
	}
	if err != nil {
		common.SysError("unable to sync accounting reconciliation journal; retained in memory")
	}
}

func RecordExist(err error) (bool, error) {
	if err == nil {
		return true, nil
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	return false, err
}

func shouldUpdateRedis(fromDB bool, err error) bool {
	return common.RedisEnabled && fromDB && err == nil
}
