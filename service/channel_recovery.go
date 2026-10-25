package service

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/types"
)

const (
	ChannelFailureAccountQuota = "account_quota"
	ChannelFailureRateLimit    = "rate_limit"
	ChannelFailureModel        = "model"
	ChannelFailureAuth         = "auth"
	ChannelFailureOther        = "other"
)

// ClassifyChannelFailure never equates HTTP 429 (or numeric GLM limit codes)
// with account exhaustion. Ambiguous errors retain the existing policy.
func ClassifyChannelFailure(err *types.NewAPIError) string {
	if err == nil {
		return ChannelFailureOther
	}
	if err.GetErrorCode() == types.ErrorCodeInsufficientUserQuota || err.GetErrorCode() == types.ErrorCodePreConsumeTokenQuotaFailed {
		return ChannelFailureOther
	}
	upstream := err.ToOpenAIError()
	return ClassifyChannelFailureText(fmt.Sprint(upstream.Code), upstream.Message, err.StatusCode)
}

func ClassifyChannelFailureText(code, message string, status int) string {
	code = strings.ToLower(strings.TrimSpace(code))
	message = strings.ToLower(message)
	if code == "1302" || code == "rate_limit_exceeded" || strings.Contains(message, "requests per minute") || strings.Contains(message, "tokens per minute") || strings.Contains(message, "并发") || strings.Contains(message, "频率") {
		return ChannelFailureRateLimit
	}
	if code == "model_not_found" || code == "unsupported_model" || strings.Contains(message, "[model]") || strings.Contains(message, "model quota") || strings.Contains(message, "model-specific") || strings.Contains(message, "model not found") || strings.Contains(message, "模型不存在") || strings.Contains(message, "不支持该模型") {
		return ChannelFailureModel
	}
	if code == "invalid_api_key" || code == "authentication_error" || code == "invalid_token" || status == 401 || strings.Contains(message, "api key is invalid") || strings.Contains(message, "invalid api key") || strings.Contains(message, "密钥无效") || strings.Contains(message, "[auth]") {
		return ChannelFailureAuth
	}
	switch code {
	case "insufficient_quota", "insufficient_balance", "credit_limit", "tokens_limit", "token_quota_exceeded", "quota_exhausted", "monthly_quota_exceeded", "weekly_quota_exceeded":
		return ChannelFailureAccountQuota
	}
	for _, marker := range []string{"[account_quota", "credit balance is too low", "insufficient balance", "insufficient quota", "exceeded your current quota", "usage limit for this billing cycle", "monthly quota exhausted", "weekly quota exhausted", "额度已用尽", "额度不足", "余额不足", "套餐已用尽", "周期用量限制", "月度限额", "周限额", "您已达到每周/每月使用上限", "您已达到每周使用上限", "您已达到每月使用上限"} {
		if strings.Contains(message, marker) {
			return ChannelFailureAccountQuota
		}
	}
	if status == 429 {
		return ChannelFailureRateLimit
	}
	return ChannelFailureOther
}

var channelQuotaResetPattern = regexp.MustCompile(`\breset_at=([0-9]{10})\b`)
var channelChineseQuotaResetPattern = regexp.MustCompile(`您的限额将在\s*([0-9]{4}-[0-9]{2}-[0-9]{2} [0-9]{2}:[0-9]{2}:[0-9]{2})\s*重置`)

func ChannelQuotaResetAt(reason string) int64 {
	match := channelQuotaResetPattern.FindStringSubmatch(reason)
	if len(match) == 2 {
		value, _ := strconv.ParseInt(match[1], 10, 64)
		return value
	}
	// GLM's verified weekly/monthly quota message uses a China local timestamp.
	// Do not interpret an arbitrary date elsewhere in an upstream response.
	match = channelChineseQuotaResetPattern.FindStringSubmatch(reason)
	if len(match) != 2 {
		return 0
	}
	parsed, err := time.ParseInLocation("2006-01-02 15:04:05", match[1], time.FixedZone("Asia/Shanghai", 8*60*60))
	if err != nil {
		return 0
	}
	return parsed.Unix()
}

func channelErrorResetAt(err *types.NewAPIError) int64 {
	upstream := err.ToOpenAIError()
	metadata := upstream.Metadata
	if len(metadata) == 0 {
		metadata = err.Metadata
	}
	var values map[string]any
	if len(metadata) == 0 || common.Unmarshal(metadata, &values) != nil {
		return ChannelQuotaResetAt(upstream.Message)
	}
	for _, key := range []string{"reset_at", "reset_time", "reset_timestamp"} {
		var value int64
		switch raw := values[key].(type) {
		case float64:
			if raw > 0 && raw < 100000000000000 {
				value = int64(raw)
			}
		case string:
			value, _ = strconv.ParseInt(raw, 10, 64)
			if value == 0 {
				if parsed, e := time.Parse(time.RFC3339, raw); e == nil {
					value = parsed.Unix()
				}
			}
		}
		if value > 100000000000 {
			value /= 1000
		}
		if value > time.Now().Unix() {
			return value
		}
	}
	return ChannelQuotaResetAt(upstream.Message)
}

func CanAutomaticallyRecoverChannel(channel *model.Channel) bool {
	if channel == nil || channel.Status != common.ChannelStatusAutoDisabled || common.IsChannelTimeBlocked(channel.Id) {
		return false
	}
	reason, _ := channel.GetOtherInfo()["status_reason"].(string)
	classification := ClassifyChannelFailureText("", reason, 0)
	if classification == ChannelFailureAuth || classification == ChannelFailureModel {
		return false
	}
	return ChannelQuotaResetAt(reason) <= time.Now().Unix()
}

func ChannelFailureReason(err *types.NewAPIError) string {
	classification := ClassifyChannelFailure(err)
	reason := "[" + classification + "] " + err.ToOpenAIError().Message
	if resetAt := channelErrorResetAt(err); classification == ChannelFailureAccountQuota && resetAt > 0 {
		reason = fmt.Sprintf("[account_quota reset_at=%d] %s", resetAt, err.ToOpenAIError().Message)
	}
	if len(reason) > 2000 {
		reason = reason[:2000]
	}
	return reason
}

// A failed recovery may reveal expired credentials or a future quota reset.
// Record that result only if it still belongs to the probe's disable generation.
func RecordChannelRecoveryFailure(snapshot model.ChannelRecoverySnapshot, err *types.NewAPIError) (bool, error) {
	classification := ClassifyChannelFailure(err)
	if classification != ChannelFailureAccountQuota && classification != ChannelFailureAuth && classification != ChannelFailureModel {
		return false, nil
	}
	return model.QuarantineChannelStatus(snapshot.ChannelID, snapshot.Version, ChannelFailureReason(err))
}

// DisableChannelForError preserves structured error classification before
// recording a sanitized reason. Only explicit account quota affects siblings.
func DisableChannelForError(channelError types.ChannelError, err *types.NewAPIError) {
	if err == nil || !channelError.AutoBan {
		return
	}
	classification := ClassifyChannelFailure(err)
	if classification == ChannelFailureRateLimit || classification == ChannelFailureModel {
		return
	}
	reason := ChannelFailureReason(err)
	if classification != ChannelFailureAccountQuota {
		DisableChannel(channelError, reason)
		return
	}
	// Account groups are explicit configuration, not inferred from names or
	// model overlap. Separate subscriptions must never share a circuit.
	ids := []int{channelError.ChannelId}
	var groups [][]int
	if raw := os.Getenv("NEXEVIC_QUOTA_ACCOUNT_GROUPS"); raw != "" && common.UnmarshalJsonStr(raw, &groups) == nil {
		for _, group := range groups {
			for _, id := range group {
				if id == channelError.ChannelId {
					ids = group
					break
				}
			}
		}
	}
	channels, getErr := model.GetChannelsByIds(ids)
	if getErr != nil {
		common.SysError("failed to read quota account channels")
		return
	}
	for _, channel := range channels {
		if !channel.GetAutoBan() || channel.Status == common.ChannelStatusManuallyDisabled {
			continue
		}
		if _, updateErr := model.QuarantineChannelStatus(channel.Id, channel.StatusVersion, reason); updateErr != nil {
			common.SysError(fmt.Sprintf("failed to quarantine channel #%d", channel.Id))
		}
	}
}

func RecoverChannel(snapshot model.ChannelRecoverySnapshot, usingKey, channelName string) (bool, error) {
	if !common.AutomaticEnableChannelEnabled {
		return false, nil
	}
	changed, err := model.RecoverChannelStatus(snapshot, usingKey)
	if err == nil && changed {
		NotifyRootUser(formatNotifyType(snapshot.ChannelID, common.ChannelStatusEnabled), fmt.Sprintf("通道「%s」（#%d）已恢复", channelName, snapshot.ChannelID), "检测成功且系统禁用版本未发生变化，渠道已恢复。")
	}
	return changed, err
}
