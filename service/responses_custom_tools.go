package service

import (
	"strings"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/gin-gonic/gin"
)

const responsesCustomToolsContextKey = "responses_custom_tools"

// TrackResponsesCustomTools records which Responses tools were declared as
// custom so Chat Completions tool calls can be restored on the way back.
func TrackResponsesCustomTools(c *gin.Context, request *dto.OpenAIResponsesRequest) {
	if c == nil || request == nil {
		return
	}
	tools := make(map[string]struct{})
	for _, tool := range request.GetToolsMap() {
		if strings.TrimSpace(dtoValueString(tool["type"])) != dto.CustomType {
			continue
		}
		name := strings.TrimSpace(dtoValueString(tool["name"]))
		if name != "" {
			tools[name] = struct{}{}
		}
	}
	if len(tools) > 0 {
		c.Set(responsesCustomToolsContextKey, tools)
	}
}

func IsResponsesCustomTool(c *gin.Context, name string) bool {
	if c == nil || strings.TrimSpace(name) == "" {
		return false
	}
	value, ok := c.Get(responsesCustomToolsContextKey)
	if !ok {
		return false
	}
	tools, ok := value.(map[string]struct{})
	if !ok {
		return false
	}
	_, ok = tools[strings.TrimSpace(name)]
	return ok
}

func dtoValueString(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	return ""
}
