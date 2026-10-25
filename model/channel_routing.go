package model

import (
	"fmt"
	"math/rand"
	"sort"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
)

// GetChannelRoutingCandidates applies the same eligibility rules to random
// selection and affinity. Candidates are ordered by descending priority.
func GetChannelRoutingCandidates(group, modelName, requestPath string, excluded map[int]bool) ([]*Channel, error) {
	return getChannelRoutingCandidatesAt(group, modelName, requestPath, excluded, time.Now())
}

func getChannelRoutingCandidatesAt(group, modelName, requestPath string, excluded map[int]bool, now time.Time) ([]*Channel, error) {
	var candidates []*Channel
	if common.MemoryCacheEnabled {
		channelSyncLock.RLock()
		defer channelSyncLock.RUnlock()
		ids := filterChannelsByRequestPathAndModel(group2model2channels[group][modelName], requestPath, modelName)
		if len(ids) == 0 {
			ids = filterChannelsByRequestPathAndModel(group2model2channels[group][ratio_setting.FormatMatchingModelName(modelName)], requestPath, modelName)
		}
		for _, id := range ids {
			channel, ok := channelsIDM[id]
			if !ok {
				return nil, fmt.Errorf("routing channel #%d does not exist", id)
			}
			if channel.Status == common.ChannelStatusEnabled && !excluded[id] && !common.IsChannelTimeBlockedAt(id, now) {
				candidates = append(candidates, channel)
			}
		}
	} else {
		// Filter before selecting a priority. Otherwise excluding every channel in
		// the highest tier would incorrectly hide a usable lower-priority tier.
		modelNames := []string{modelName}
		if normalized := ratio_setting.FormatMatchingModelName(modelName); normalized != "" && normalized != modelName {
			modelNames = append(modelNames, normalized)
		}
		for _, name := range modelNames {
			var abilities []Ability
			if err := DB.Where(map[string]interface{}{"group": group, "model": name, "enabled": true}).Find(&abilities).Error; err != nil {
				return nil, err
			}
			ids := make([]int, 0, len(abilities))
			for _, ability := range abilities {
				if !excluded[ability.ChannelId] && !common.IsChannelTimeBlockedAt(ability.ChannelId, now) {
					ids = append(ids, ability.ChannelId)
				}
			}
			if len(ids) == 0 {
				continue
			}
			var channels []*Channel
			if err := DB.Where("id IN ? AND status = ?", ids, common.ChannelStatusEnabled).Find(&channels).Error; err != nil {
				return nil, err
			}
			for _, channel := range channels {
				if channel.Type == constant.ChannelTypeAdvancedCustom && requestPath != "" {
					config := channel.GetOtherSettings().AdvancedCustom
					if config == nil || !config.SupportsPathForModel(requestPath, modelName) {
						continue
					}
				}
				candidates = append(candidates, channel)
			}
			if len(candidates) > 0 {
				break
			}
		}
	}
	// Sort only our own slice, never a shared cache entry.
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].GetPriority() == candidates[j].GetPriority() {
			return candidates[i].Id < candidates[j].Id
		}
		return candidates[i].GetPriority() > candidates[j].GetPriority()
	})
	return candidates, nil
}

func selectRoutingChannel(candidates []*Channel, retry int) *Channel {
	if len(candidates) == 0 {
		return nil
	}
	if retry < 0 {
		retry = 0
	}
	start := 0
	for retry > 0 {
		next := start + 1
		for next < len(candidates) && candidates[next].GetPriority() == candidates[start].GetPriority() {
			next++
		}
		if next == len(candidates) {
			break
		}
		start = next
		retry--
	}
	end := start + 1
	for end < len(candidates) && candidates[end].GetPriority() == candidates[start].GetPriority() {
		end++
	}
	pool := candidates[start:end]
	totalWeight := 0
	for _, channel := range pool {
		totalWeight += channel.GetWeight()
	}
	if totalWeight == 0 {
		return pool[rand.Intn(len(pool))]
	}
	weight := rand.Intn(totalWeight)
	for _, channel := range pool {
		weight -= channel.GetWeight()
		if weight < 0 {
			return channel
		}
	}
	return nil
}
