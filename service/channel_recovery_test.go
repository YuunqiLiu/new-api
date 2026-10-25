package service

import (
	"errors"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/stretchr/testify/assert"
)

func TestChannelQuotaClassificationDoesNotBanTransientOrModelLimits(t *testing.T) {
	tests := []struct {
		code, message string
		status        int
		want          string
	}{
		{"1302", "too many requests", 429, ChannelFailureRateLimit},
		{"1308", "request limit reached", 429, ChannelFailureRateLimit},
		{"1310", "limit reached", 429, ChannelFailureRateLimit},
		{"1310", "本周套餐额度不足", 429, ChannelFailureAccountQuota},
		{"1310", "您已达到每周/每月使用上限，您的限额将在 2026-10-20 12:33:54 重置。", 429, ChannelFailureAccountQuota},
		{"1310", "已达到使用上限", 429, ChannelFailureRateLimit},
		{"tokens_limit", "tokens per minute exceeded", 429, ChannelFailureRateLimit},
		{"token_quota_exceeded", "Token Plan Person monthly quota limit exceeded", 429, ChannelFailureAccountQuota},
		{"token_plan_person_rate_limit_exceeded", "Token Plan Person rate limit exceeded", 429, ChannelFailureRateLimit},
		{"insufficient_quota", "model quota exhausted", 429, ChannelFailureModel},
		{"insufficient_quota", "monthly account quota exhausted", 429, ChannelFailureAccountQuota},
		{"", "insufficient balance", 402, ChannelFailureAccountQuota},
		{"invalid_api_key", "invalid credentials", 401, ChannelFailureAuth},
		{"", "provider overloaded", 503, ChannelFailureOther},
	}
	for _, tt := range tests {
		t.Run(tt.code+tt.message, func(t *testing.T) {
			assert.Equal(t, tt.want, ClassifyChannelFailureText(tt.code, tt.message, tt.status))
		})
	}
	err := types.NewErrorWithStatusCode(errors.New("local user quota insufficient"), types.ErrorCodeInsufficientUserQuota, 403)
	assert.Equal(t, ChannelFailureOther, ClassifyChannelFailure(err), "local user quota must never quarantine an upstream")
}

func TestChannelQuotaResetParsesVerifiedChinaLocalMessage(t *testing.T) {
	message := "您已达到每周/每月使用上限，您的限额将在 2026-10-20 12:33:54 重置。"
	want := time.Date(2026, time.October, 20, 12, 33, 54, 0, time.FixedZone("Asia/Shanghai", 8*60*60)).Unix()
	assert.Equal(t, want, ChannelQuotaResetAt(message))
	assert.Zero(t, ChannelQuotaResetAt("Request failed at 2026-10-20 12:33:54"))
	assert.Zero(t, ChannelQuotaResetAt("您的限额将在 2026-99-20 12:33:54 重置"))
}

func TestChannelRecoverySkipsAuthManualAndFutureReset(t *testing.T) {
	t.Setenv("NEXEVIC_GLM_PEAK_CHANNEL_IDS", "")
	channel := &model.Channel{Id: 123, Status: common.ChannelStatusAutoDisabled}
	channel.SetOtherInfo(map[string]interface{}{"status_reason": "[auth] invalid API key"})
	assert.False(t, CanAutomaticallyRecoverChannel(channel))
	channel.SetOtherInfo(map[string]interface{}{"status_reason": "[model] unsupported model"})
	assert.False(t, CanAutomaticallyRecoverChannel(channel))
	channel.SetOtherInfo(map[string]interface{}{"status_reason": "[account_quota reset_at=9999999999] exhausted"})
	assert.False(t, CanAutomaticallyRecoverChannel(channel))
	channel.SetOtherInfo(map[string]interface{}{"status_reason": "[account_quota] exhausted"})
	assert.True(t, CanAutomaticallyRecoverChannel(channel))
	channel.Status = common.ChannelStatusManuallyDisabled
	assert.False(t, CanAutomaticallyRecoverChannel(channel))
	assert.Greater(t, ChannelQuotaResetAt("[account_quota reset_at=9999999999]"), time.Now().Unix())
}
