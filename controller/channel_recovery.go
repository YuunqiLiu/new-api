package controller

import (
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

type channelRecoveryRequest struct {
	ExpectedVersion    *int64 `json:"expected_version"`
	LegacyDisabledAt   int64  `json:"legacy_disabled_at"`
	LegacyStatusReason string `json:"legacy_status_reason"`
	LegacyQuotaReason  string `json:"legacy_quota_reason"`
}

// RecoverDisabledChannel keeps the probe and guarded state change on the server.
// API clients cannot claim a successful probe by posting a boolean.
func RecoverDisabledChannel(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	request := channelRecoveryRequest{}
	if err != nil || c.ShouldBindJSON(&request) != nil || request.ExpectedVersion == nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "invalid recovery request"})
		return
	}
	channel, err := model.GetChannelById(id, true)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	snapshot, recoverable := model.SnapshotChannelRecovery(channel)
	data := gin.H{"outcome": "stale", "status_version": channel.StatusVersion}
	if !recoverable || snapshot.Version != *request.ExpectedVersion {
		c.JSON(http.StatusOK, gin.H{"success": true, "data": data})
		return
	}
	if !common.AutomaticEnableChannelEnabled || !service.CanAutomaticallyRecoverChannel(channel) {
		data["outcome"] = "blocked"
		c.JSON(http.StatusOK, gin.H{"success": true, "data": data})
		return
	}
	testUserID, err := resolveChannelTestUserID(c)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	result := testChannel(c.Request.Context(), channel, testUserID, "", "", false)
	if result.localErr != nil || result.newAPIError != nil {
		data["outcome"] = "unavailable"
		data["classification"] = service.ClassifyChannelFailure(result.newAPIError)
		data["next_probe_at"] = time.Now().Unix() + 15*60
		if result.newAPIError != nil {
			if changed, updateErr := service.RecordChannelRecoveryFailure(snapshot, result.newAPIError); updateErr != nil {
				common.ApiError(c, updateErr)
				return
			} else if changed {
				data["status_version"] = snapshot.Version + 1
				if resetAt := service.ChannelQuotaResetAt(service.ChannelFailureReason(result.newAPIError)); resetAt > time.Now().Unix()+15*60 {
					data["next_probe_at"] = resetAt
				}
			}
		}
		// Do not return upstream bodies or credentials through this endpoint.
		if common.IsChannelTimeBlocked(id) {
			data["outcome"] = "blocked"
		}
		c.JSON(http.StatusOK, gin.H{"success": true, "data": data})
		return
	}
	changed, err := service.RecoverChannel(snapshot, common.GetContextKeyString(result.context, constant.ContextKeyChannelKey), channel.Name)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if changed {
		data["outcome"] = "recovered"
	}
	recordManageAudit(c, "channel.auto_recovery", map[string]interface{}{"id": id, "changed": changed, "expected_version": snapshot.Version})
	c.JSON(http.StatusOK, gin.H{"success": true, "data": data})
}

func AdoptLegacyChannelRecovery(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	request := channelRecoveryRequest{}
	if err != nil || c.ShouldBindJSON(&request) != nil || request.ExpectedVersion == nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "invalid recovery request"})
		return
	}
	allowed := false
	for _, entry := range strings.Split(os.Getenv("NEXEVIC_LEGACY_SCHEDULER_CHANNEL_IDS"), ",") {
		if strings.TrimSpace(entry) == strconv.Itoa(id) {
			allowed = true
		}
	}
	if !allowed || request.LegacyStatusReason != "manual operation" || service.ClassifyChannelFailureText("", request.LegacyQuotaReason, 0) != service.ChannelFailureAccountQuota {
		c.JSON(http.StatusForbidden, gin.H{"success": false, "message": "legacy recovery ownership not verified"})
		return
	}
	reason := "[account_quota] adopted legacy quota scheduler"
	if resetAt := service.ChannelQuotaResetAt(request.LegacyQuotaReason); resetAt > 0 {
		reason = fmt.Sprintf("[account_quota reset_at=%d] adopted legacy quota scheduler", resetAt)
	}
	changed, err := model.AdoptLegacySchedulerDisable(id, *request.ExpectedVersion, request.LegacyDisabledAt, request.LegacyStatusReason, reason)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "channel.recovery_adopt", map[string]interface{}{"id": id, "changed": changed})
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"adopted": changed}})
}
