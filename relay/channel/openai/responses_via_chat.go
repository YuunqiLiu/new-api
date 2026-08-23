package openai

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/relayconvert"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

func OaiChatToResponsesHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	if resp == nil || resp.Body == nil {
		return nil, types.NewOpenAIError(fmt.Errorf("invalid response"), types.ErrorCodeBadResponse, http.StatusInternalServerError)
	}
	defer service.CloseResponseBodyGracefully(resp)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeReadResponseBodyFailed, http.StatusInternalServerError)
	}

	var chatResp dto.OpenAITextResponse
	if err := common.Unmarshal(body, &chatResp); err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}
	if oaiError := chatResp.GetOpenAIError(); oaiError != nil && oaiError.Type != "" {
		return nil, types.WithOpenAIError(*oaiError, resp.StatusCode)
	}

	if responseID := helper.GetResponseID(c); responseID != "" {
		chatResp.Id = responseID
	}
	convertResult, err := relayconvert.ConvertResponse(c, info, types.RelayFormatOpenAIResponses, &chatResp)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}
	responsesResp, ok := convertResult.Value.(*dto.OpenAIResponsesResponse)
	if !ok {
		return nil, types.NewOpenAIError(fmt.Errorf("expected OpenAI responses response, got %T", convertResult.Value), types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}
	restoreCustomToolsInResponse(c, responsesResp)
	usage := convertResult.Usage
	if usage == nil || usage.TotalTokens == 0 {
		text := service.ExtractOutputTextFromResponses(responsesResp)
		usage = service.ResponseText2Usage(c, text, info.UpstreamModelName, info.GetEstimatePromptTokens())
		responsesResp.Usage = relayconvert.UsageFromChatUsage(usage)
	}

	responseBody, err := common.Marshal(responsesResp)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeJsonMarshalFailed, http.StatusInternalServerError)
	}

	service.IOCopyBytesGracefully(c, resp, responseBody)
	return usage, nil
}

func OaiChatToResponsesStreamHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	if resp == nil || resp.Body == nil {
		return nil, types.NewOpenAIError(fmt.Errorf("invalid response"), types.ErrorCodeBadResponse, http.StatusInternalServerError)
	}
	defer service.CloseResponseBodyGracefully(resp)

	responseID := helper.GetResponseID(c)
	state, err := relayconvert.NewResponseStreamState(types.RelayFormatOpenAI, types.RelayFormatOpenAIResponses, relayconvert.ResponseStreamOptions{
		ID:    responseID,
		Model: info.UpstreamModelName,
	})
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponse, http.StatusInternalServerError)
	}
	streamErr := (*types.NewAPIError)(nil)

	rawSendEvent := func(event relayconvert.ChatToResponsesStreamEvent) bool {
		data, err := common.Marshal(event.Payload)
		if err != nil {
			streamErr = types.NewOpenAIError(err, types.ErrorCodeJsonMarshalFailed, http.StatusInternalServerError)
			return false
		}
		helper.ResponseChunkData(c, dto.ResponsesStreamResponse{Type: event.Type}, string(data))
		return true
	}
	customItems := make(map[string]struct{})
	sendEvent := func(event relayconvert.ChatToResponsesStreamEvent) bool {
		itemID := event.Payload.ItemID
		if itemID == "" && event.Payload.Item != nil {
			itemID = event.Payload.Item.ID
		}

		if event.Payload.Item != nil && service.IsResponsesCustomTool(c, event.Payload.Item.Name) {
			customItems[itemID] = struct{}{}
		}
		_, isCustom := customItems[itemID]

		switch event.Type {
		case "response.function_call_arguments.delta", "response.function_call_arguments.done":
			if isCustom {
				return true
			}
		case "response.output_item.added":
			if isCustom {
				restoreCustomToolOutput(event.Payload.Item)
			}
		case "response.output_item.done":
			if isCustom {
				callID := ""
				if event.Payload.Item != nil {
					callID = event.Payload.Item.CallId
				}
				input := restoreCustomToolOutput(event.Payload.Item)
				if input != "" && !rawSendEvent(relayconvert.ChatToResponsesStreamEvent{
					Type: "response.custom_tool_call_input.delta",
					Payload: dto.ResponsesStreamResponse{
						Type:        "response.custom_tool_call_input.delta",
						OutputIndex: event.Payload.OutputIndex,
						ItemID:      itemID,
						CallID:      callID,
						Delta:       input,
					},
				}) {
					return false
				}
				if !rawSendEvent(relayconvert.ChatToResponsesStreamEvent{
					Type: "response.custom_tool_call_input.done",
					Payload: dto.ResponsesStreamResponse{
						Type:        "response.custom_tool_call_input.done",
						OutputIndex: event.Payload.OutputIndex,
						ItemID:      itemID,
						CallID:      callID,
						Input:       input,
					},
				}) {
					return false
				}
			}
		case "response.completed", "response.incomplete":
			restoreCustomToolsInResponse(c, event.Payload.Response)
		}
		return rawSendEvent(event)
	}

	helper.StreamScannerHandler(c, resp, info, func(data string, sr *helper.StreamResult) {
		if streamErr != nil {
			sr.Stop(streamErr)
			return
		}

		var errorResp dto.OpenAITextResponse
		if err := common.UnmarshalJsonStr(data, &errorResp); err == nil {
			if oaiError := errorResp.GetOpenAIError(); oaiError != nil && oaiError.Type != "" {
				streamErr = types.WithOpenAIError(*oaiError, resp.StatusCode)
				sr.Stop(streamErr)
				return
			}
		}

		var chunk dto.ChatCompletionsStreamResponse
		if err := common.UnmarshalJsonStr(data, &chunk); err != nil {
			logger.LogError(c, "failed to unmarshal chat stream response: "+err.Error())
			sr.Error(err)
			return
		}

		results, err := relayconvert.ConvertStreamResponseChunk(c, info, state, &chunk)
		if err != nil {
			streamErr = types.NewOpenAIError(err, types.ErrorCodeBadResponse, http.StatusInternalServerError)
			sr.Stop(streamErr)
			return
		}
		for _, result := range results {
			event, ok := result.Value.(relayconvert.ChatToResponsesStreamEvent)
			if !ok {
				streamErr = types.NewOpenAIError(fmt.Errorf("expected OAI responses stream event, got %T", result.Value), types.ErrorCodeBadResponse, http.StatusInternalServerError)
				sr.Stop(streamErr)
				return
			}
			if !sendEvent(event) {
				sr.Stop(streamErr)
				return
			}
		}
	})

	if streamErr != nil {
		return nil, streamErr
	}

	usage := state.Usage()
	if usage == nil || usage.TotalTokens == 0 {
		usage = service.ResponseText2Usage(c, state.UsageText(), info.UpstreamModelName, info.GetEstimatePromptTokens())
		state.SetUsage(usage)
	}

	finalResults, err := relayconvert.FinalizeStreamResponse(c, info, state)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponse, http.StatusInternalServerError)
	}
	for _, result := range finalResults {
		event, ok := result.Value.(relayconvert.ChatToResponsesStreamEvent)
		if !ok {
			return nil, types.NewOpenAIError(fmt.Errorf("expected OAI responses stream event, got %T", result.Value), types.ErrorCodeBadResponse, http.StatusInternalServerError)
		}
		if !sendEvent(event) {
			return nil, streamErr
		}
	}

	return usage, nil
}

func restoreCustomToolsInResponse(c *gin.Context, response *dto.OpenAIResponsesResponse) {
	if response == nil {
		return
	}
	for i := range response.Output {
		if service.IsResponsesCustomTool(c, response.Output[i].Name) {
			restoreCustomToolOutput(&response.Output[i])
		}
	}
}

func restoreCustomToolOutput(output *dto.ResponsesOutput) string {
	if output == nil {
		return ""
	}
	input := customToolInput(output.Arguments)
	output.Type = "custom_tool_call"
	output.Arguments = nil
	if input != "" {
		if raw, err := common.Marshal(input); err == nil {
			output.Input = raw
		}
	}
	return input
}

func customToolInput(arguments json.RawMessage) string {
	text := dto.ResponsesArgumentsString(arguments)
	if text == "" {
		return ""
	}
	var object map[string]any
	if err := common.Unmarshal([]byte(text), &object); err == nil {
		if input, ok := object["input"].(string); ok {
			return input
		}
	}
	return text
}
