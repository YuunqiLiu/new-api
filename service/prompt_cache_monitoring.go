package service

import (
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

const promptCacheMonitoringMaxLogs = 100000

type PromptCacheMonitoringRequest struct {
	Window  string
	Model   string
	Channel int
}

type PromptCacheMonitoringBucket struct {
	Key          string `json:"key"`
	PromptTokens int64  `json:"prompt_tokens"`
	CachedTokens int64  `json:"cached_tokens"`
	Requests     int64  `json:"requests"`
	ColdStarts   int64  `json:"cold_starts"`
	AffinityHits int64  `json:"affinity_hits"`
	CrossChannel int64  `json:"cross_channel"`
}

type PromptCacheMonitoringReport struct {
	Window       string                        `json:"window"`
	StartAt      int64                         `json:"start_at"`
	EndAt        int64                         `json:"end_at"`
	Truncated    bool                          `json:"truncated"`
	Overall      PromptCacheMonitoringBucket   `json:"overall"`
	Trend        []PromptCacheMonitoringBucket `json:"trend"`
	ByModel      []PromptCacheMonitoringBucket `json:"by_model"`
	ByChannel    []PromptCacheMonitoringBucket `json:"by_channel"`
	SessionCount int64                         `json:"session_count"`
}

func GetPromptCacheMonitoringReport(req PromptCacheMonitoringRequest) (PromptCacheMonitoringReport, error) {
	windowHours := map[string]int{"1h": 1, "6h": 6, "24h": 24, "7d": 168, "30d": 720}[req.Window]
	if windowHours == 0 {
		windowHours = 24
		req.Window = "24h"
	}
	now := time.Now().Unix()
	start := now - int64(windowHours)*3600
	logs, total, err := model.GetAllLogs(model.LogTypeConsume, start, now, strings.TrimSpace(req.Model), "", "", 0, promptCacheMonitoringMaxLogs, req.Channel, "", "", "")
	if err != nil {
		return PromptCacheMonitoringReport{}, err
	}
	report := PromptCacheMonitoringReport{Window: req.Window, StartAt: start, EndAt: now, Truncated: total > promptCacheMonitoringMaxLogs}
	byModel, byChannel, trend := map[string]*PromptCacheMonitoringBucket{}, map[string]*PromptCacheMonitoringBucket{}, map[string]*PromptCacheMonitoringBucket{}
	sessions := map[string]map[int]struct{}{}
	for _, log := range logs {
		var other map[string]interface{}
		if common.UnmarshalJsonStr(log.Other, &other) != nil {
			continue
		}
		cacheTokens, _ := numberAsInt64(other["cache_tokens"])
		admin, _ := other["admin_info"].(map[string]interface{})
		affinity, _ := admin["channel_affinity"].(map[string]interface{})
		fp, _ := affinity["key_fp"].(string)
		routing, _ := affinity["routing"].(string)
		bucketKey := time.Unix(log.CreatedAt, 0).UTC().Format("2006-01-02 15:00")
		if windowHours >= 168 {
			bucketKey = time.Unix(log.CreatedAt, 0).UTC().Format("2006-01-02")
		}
		addPromptCacheBucket(&report.Overall, log.PromptTokens, cacheTokens, routing, false)
		modelKey := log.ModelName
		if modelKey == "" {
			modelKey = "unknown"
		}
		channelKey := "#" + strconv.Itoa(log.ChannelId)
		if byModel[modelKey] == nil {
			byModel[modelKey] = &PromptCacheMonitoringBucket{Key: modelKey}
		}
		if byChannel[channelKey] == nil {
			byChannel[channelKey] = &PromptCacheMonitoringBucket{Key: channelKey}
		}
		if trend[bucketKey] == nil {
			trend[bucketKey] = &PromptCacheMonitoringBucket{Key: bucketKey}
		}
		addPromptCacheBucket(byModel[modelKey], log.PromptTokens, cacheTokens, routing, false)
		addPromptCacheBucket(byChannel[channelKey], log.PromptTokens, cacheTokens, routing, false)
		addPromptCacheBucket(trend[bucketKey], log.PromptTokens, cacheTokens, routing, false)
		if fp != "" {
			if sessions[fp] == nil {
				sessions[fp] = map[int]struct{}{}
			}
			sessions[fp][log.ChannelId] = struct{}{}
		}
	}
	for _, channels := range sessions {
		if len(channels) > 1 {
			report.Overall.CrossChannel++
		}
	}
	report.SessionCount = int64(len(sessions))
	report.Trend = promptCacheBuckets(trend)
	report.ByModel = promptCacheBuckets(byModel)
	report.ByChannel = promptCacheBuckets(byChannel)
	return report, nil
}

func numberAsInt64(v interface{}) (int64, bool) {
	switch n := v.(type) {
	case float64:
		return int64(n), true
	case int64:
		return n, true
	case int:
		return int64(n), true
	case string:
		value, err := strconv.ParseInt(n, 10, 64)
		return value, err == nil
	default:
		return 0, false
	}
}

func addPromptCacheBucket(bucket *PromptCacheMonitoringBucket, promptTokens int, cachedTokens int64, routing string, crossChannel bool) {
	if bucket == nil {
		return
	}
	bucket.PromptTokens += int64(promptTokens)
	bucket.CachedTokens += cachedTokens
	bucket.Requests++
	if routing == "cold_start" {
		bucket.ColdStarts++
	}
	if routing == "affinity_hit" {
		bucket.AffinityHits++
	}
	if crossChannel {
		bucket.CrossChannel++
	}
}

func promptCacheBuckets(source map[string]*PromptCacheMonitoringBucket) []PromptCacheMonitoringBucket {
	result := make([]PromptCacheMonitoringBucket, 0, len(source))
	for _, bucket := range source {
		result = append(result, *bucket)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Key < result[j].Key })
	return result
}
