package service

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

// GetUsableAffinityChannel only honors affinity within the first eligible
// group's highest priority pool. A fallback must not pin subsequent requests
// after the subscription channels have recovered.
func GetUsableAffinityChannel(c *gin.Context, modelName, group, requestPath string, preferredID int) (*model.Channel, string, error) {
	groups := []string{group}
	if group == "auto" {
		groups = GetRequestAutoGroups(c, common.GetContextKeyString(c, constant.ContextKeyUserGroup))
	}
	for index, candidateGroup := range groups {
		candidates, err := model.GetChannelRoutingCandidates(candidateGroup, modelName, requestPath, nil)
		if err != nil {
			return nil, candidateGroup, err
		}
		if len(candidates) == 0 {
			continue
		}
		priority := candidates[0].GetPriority()
		var preferred *model.Channel
		totalWeight := 0
		for _, channel := range candidates {
			if channel.GetPriority() != priority {
				break
			}
			totalWeight += channel.GetWeight()
			if channel.Id == preferredID {
				preferred = channel
			}
		}
		if preferred != nil && (totalWeight == 0 || preferred.GetWeight() > 0) {
			if group == "auto" {
				common.SetContextKey(c, constant.ContextKeyAutoGroup, candidateGroup)
				common.SetContextKey(c, constant.ContextKeyAutoGroupIndex, index)
			}
			return preferred, candidateGroup, nil
		}
		// Do not bypass a healthy earlier Auto group to follow a later affinity.
		return nil, candidateGroup, nil
	}
	return nil, group, nil
}
