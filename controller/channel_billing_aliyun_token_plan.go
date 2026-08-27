package controller

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	openapiutil "github.com/alibabacloud-go/darabonba-openapi/v2/utils"
	modelstudio "github.com/alibabacloud-go/modelstudio-20260210/v2/client"
	"github.com/alibabacloud-go/tea/dara"
	aliyuncredential "github.com/aliyun/credentials-go/credentials"
)

const (
	aliyunTokenPlanBaseHost         = "token-plan.cn-beijing.maas.aliyuncs.com"
	aliyunTokenPlanPersonalUsageAPI = "zeldaHttp.apikeyMgr./tokenplan/personal/api/v2/usage"
	aliyunTokenPlanConsoleURL       = "https://bailian.console.aliyun.com/cn-beijing?tab=plan"
)

var aliyunConsoleSecTokenPattern = regexp.MustCompile(`\bSEC_TOKEN\s*:\s*"([^"]+)"`)

func isAliyunTokenPlan(channel *model.Channel) bool {
	return strings.Contains(strings.ToLower(channel.GetBaseURL()), aliyunTokenPlanBaseHost)
}

func isAliyunPersonalTokenPlan(channel *model.Channel) bool {
	return isAliyunTokenPlan(channel) && strings.Contains(channel.Name, "个人版")
}

func isAliyunTeamTokenPlan(channel *model.Channel) bool {
	return isAliyunTokenPlan(channel) && strings.Contains(channel.Name, "团队版")
}

func persistAliyunTokenPlanQuota(channel *model.Channel, quota *ChannelPlanQuota) (*ChannelPlanQuota, error) {
	raw, err := common.Marshal(quota)
	if err != nil {
		return nil, err
	}
	channel.UpdatePlanQuota(string(raw))
	return quota, nil
}

func updateAliyunTokenPlanQuota(channel *model.Channel) (*ChannelPlanQuota, error) {
	switch {
	case isAliyunTeamTokenPlan(channel):
		quota, err := fetchAliyunTeamTokenPlanQuota()
		if err != nil {
			return nil, err
		}
		return persistAliyunTokenPlanQuota(channel, quota)
	case isAliyunPersonalTokenPlan(channel):
		quota, err := fetchAliyunPersonalTokenPlanQuota(channel.Id)
		if err != nil {
			return nil, err
		}
		return persistAliyunTokenPlanQuota(channel, quota)
	default:
		return nil, errors.New("无法识别千问 Token Plan 版本")
	}
}

func fetchAliyunTeamTokenPlanQuota() (*ChannelPlanQuota, error) {
	accessKeyID := strings.TrimSpace(os.Getenv("ALIYUN_TOKEN_PLAN_ACCESS_KEY_ID"))
	accessKeySecret := strings.TrimSpace(os.Getenv("ALIYUN_TOKEN_PLAN_ACCESS_KEY_SECRET"))
	config := &openapiutil.Config{RegionId: dara.String("cn-beijing")}
	if accessKeyID != "" && accessKeySecret != "" {
		config.AccessKeyId = dara.String(accessKeyID)
		config.AccessKeySecret = dara.String(accessKeySecret)
	} else {
		roleName := strings.TrimSpace(os.Getenv("ALIYUN_TOKEN_PLAN_ECS_RAM_ROLE"))
		if roleName == "" {
			return nil, errors.New("未配置千问 Token Plan 只读管理凭据")
		}
		credential, err := aliyuncredential.NewCredential(&aliyuncredential.Config{
			Type: dara.String("ecs_ram_role"), RoleName: dara.String(roleName),
		})
		if err != nil {
			return nil, fmt.Errorf("初始化 ECS RAM 角色凭据失败: %w", err)
		}
		config.Credential = credential
	}
	client, err := modelstudio.NewClient(config)
	if err != nil {
		return nil, fmt.Errorf("初始化 ModelStudio 客户端失败: %w", err)
	}
	response, err := client.GetSubscriptionStats(&modelstudio.GetSubscriptionStatsRequest{})
	if err != nil {
		return nil, fmt.Errorf("查询团队版 Token Plan 额度失败: %w", err)
	}
	if response == nil || response.Body == nil || response.Body.Success == nil || !*response.Body.Success || response.Body.Data == nil {
		return nil, errors.New("团队版 Token Plan 额度响应无效")
	}
	quota := &ChannelPlanQuota{
		PlanType: "阿里百炼 Token Plan 团队版",
		Status:   "active",
		Items:    make([]ChannelPlanQuotaItem, 0, len(response.Body.Data.Items)),
	}
	for _, item := range response.Body.Data.Items {
		if item == nil || item.SeatCredits == nil {
			continue
		}
		limit := *item.SeatCredits
		remaining := float64(0)
		if item.SeatRemainingCredits != nil {
			remaining = *item.SeatRemainingCredits
		}
		used := limit - remaining
		if used < 0 {
			used = 0
		}
		quota.Items = append(quota.Items, ChannelPlanQuotaItem{
			Type: "CREDIT_LIMIT", Unit: 5, Number: 1,
			Limit: limit, Used: used, Remaining: remaining,
			Percent: quotaPercent(used, limit), ResetAt: item.SeatRefreshTime,
		})
	}
	if len(quota.Items) == 0 {
		return nil, errors.New("团队版 Token Plan 未返回席位额度")
	}
	return quota, nil
}

func aliyunPersonalCookies() (map[int]string, error) {
	path := strings.TrimSpace(os.Getenv("ALIYUN_TOKEN_PLAN_PERSONAL_COOKIES_FILE"))
	if path == "" {
		return nil, errors.New("未配置个人版 Token Plan 控制台只读会话")
	}
	rawBytes, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取个人版 Token Plan 会话配置失败: %w", err)
	}
	var encoded map[string]string
	if err := json.Unmarshal(rawBytes, &encoded); err != nil {
		return nil, fmt.Errorf("个人版 Token Plan 会话配置格式错误: %w", err)
	}
	result := make(map[int]string, len(encoded))
	for key, cookie := range encoded {
		channelID, err := strconv.Atoi(key)
		if err != nil || strings.TrimSpace(cookie) == "" {
			continue
		}
		result[channelID] = strings.TrimSpace(cookie)
	}
	return result, nil
}

func fetchAliyunPersonalTokenPlanQuota(channelID int) (*ChannelPlanQuota, error) {
	cookies, err := aliyunPersonalCookies()
	if err != nil {
		return nil, err
	}
	cookie := cookies[channelID]
	if cookie == "" {
		return nil, fmt.Errorf("渠道 %d 未配置个人版 Token Plan 只读会话", channelID)
	}
	client := &http.Client{Timeout: 15 * time.Second}
	request, err := http.NewRequest(http.MethodGet, aliyunTokenPlanConsoleURL, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Cookie", cookie)
	request.Header.Set("User-Agent", "Mozilla/5.0")
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("读取阿里云控制台会话失败: %w", err)
	}
	defer response.Body.Close()
	html, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	match := aliyunConsoleSecTokenPattern.FindSubmatch(html)
	if response.StatusCode != http.StatusOK || len(match) != 2 {
		return nil, errors.New("个人版 Token Plan 控制台会话已失效")
	}
	params, err := json.Marshal(map[string]interface{}{
		"Api": aliyunTokenPlanPersonalUsageAPI,
		"V":   "1.0",
		"Data": map[string]interface{}{"cornerstoneParam": map[string]interface{}{
			"feTraceId": strconv.FormatInt(time.Now().UnixNano(), 10),
			"feURL":     "https://bailian.console.aliyun.com/cn-beijing?tab=plan#/efm/subscription/token-plan/personal",
			"protocol":  "V2", "console": "ONE_CONSOLE", "productCode": "p_efm",
			"switchAgent": 12608464, "switchUserType": 3,
			"domain": "bailian.console.aliyun.com", "consoleSite": "BAILIAN_ALIYUN",
			"userNickName": "", "userPrincipalName": "", "xsp_lang": "zh-CN",
		}},
	})
	if err != nil {
		return nil, err
	}
	form := url.Values{
		"product": {"sfm_bailian"}, "action": {"BroadScopeAspnGateway"},
		"region": {"cn-beijing"}, "sec_token": {string(match[1])}, "params": {string(params)},
	}
	usageURL := "https://bailian-cs.console.aliyun.com/data/api.json?action=BroadScopeAspnGateway&product=sfm_bailian&api=" + url.QueryEscape(aliyunTokenPlanPersonalUsageAPI)
	request, err = http.NewRequest(http.MethodPost, usageURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Cookie", cookie)
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Origin", "https://bailian.console.aliyun.com")
	request.Header.Set("Referer", aliyunTokenPlanConsoleURL)
	request.Header.Set("User-Agent", "Mozilla/5.0")
	response, err = client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("查询个人版 Token Plan 额度失败: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	var payload struct {
		SuccessResponse bool `json:"successResponse"`
		Data            struct {
			DataV2 struct {
				Data struct {
					Data struct {
						WeekPercentage float64 `json:"per1WeekPercentage"`
						WeekResetTime  int64   `json:"per1WeekResetTime"`
					} `json:"data"`
				} `json:"data"`
			} `json:"DataV2"`
		} `json:"data"`
	}
	if response.StatusCode != http.StatusOK || json.Unmarshal(body, &payload) != nil || !payload.SuccessResponse {
		return nil, errors.New("个人版 Token Plan 额度响应无效")
	}
	usedFraction := payload.Data.DataV2.Data.Data.WeekPercentage
	if usedFraction > 1 {
		usedFraction /= 100
	}
	if usedFraction < 0 || usedFraction > 1 {
		return nil, errors.New("个人版 Token Plan 返回了无效的使用比例")
	}
	resetAt := payload.Data.DataV2.Data.Data.WeekResetTime
	quota := &ChannelPlanQuota{
		PlanType: "阿里百炼 Token Plan 个人版", Status: "active",
		Items: []ChannelPlanQuotaItem{{
			Type: "CREDIT_LIMIT", Unit: 6, Number: 1,
			Percent: int(usedFraction*100 + 0.5), ResetAt: &resetAt,
		}},
	}
	return quota, nil
}
