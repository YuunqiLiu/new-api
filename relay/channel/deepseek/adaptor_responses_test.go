package deepseek

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestResponsesCustomExecUsesChatBridge(t *testing.T) {
	var request dto.OpenAIResponsesRequest
	require.NoError(t, common.Unmarshal([]byte(`{
		"model":"deepseek-v4-flash",
		"input":"Run echo OK",
		"tools":[
			{"type":"custom","name":"apply_patch","description":"Apply a patch"},
			{"type":"custom","name":"exec","description":"Run a command"}
		]
	}`), &request))
	info := &relaycommon.RelayInfo{
		RelayFormat: types.RelayFormatOpenAIResponses,
		RelayMode:   relayconstant.RelayModeResponses,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelBaseUrl:    "https://api.deepseek.com",
			UpstreamModelName: "deepseek-v4-flash",
		},
	}

	converted, err := (&Adaptor{}).ConvertOpenAIResponsesRequest(&gin.Context{}, info, request)
	require.NoError(t, err)
	chatRequest, ok := converted.(*dto.GeneralOpenAIRequest)
	require.True(t, ok)
	require.Equal(t, relayconstant.RelayModeChatCompletions, info.RelayMode)
	require.Len(t, chatRequest.Tools, 2)
	require.Equal(t, "apply_patch", chatRequest.Tools[0].Function.Name)
	require.Equal(t, "exec", chatRequest.Tools[1].Function.Name)

	requestURL, err := (&Adaptor{}).GetRequestURL(info)
	require.NoError(t, err)
	require.Equal(t, "https://api.deepseek.com/v1/chat/completions", requestURL)
}

func TestResponsesCustomExecNormalizesDeveloperRoleForChatBridge(t *testing.T) {
	var request dto.OpenAIResponsesRequest
	require.NoError(t, common.Unmarshal([]byte(`{
		"model":"deepseek-v4-flash",
		"input":[
			{"role":"developer","content":[{"type":"input_text","text":"Follow the user instruction."}]},
			{"role":"user","content":[{"type":"input_text","text":"Use exec."}]}
		],
		"tools":[{"type":"custom","name":"exec","description":"Run a command"}]
	}`), &request))
	info := &relaycommon.RelayInfo{
		RelayFormat: types.RelayFormatOpenAIResponses,
		RelayMode:   relayconstant.RelayModeResponses,
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "deepseek-v4-flash"},
	}

	converted, err := (&Adaptor{}).ConvertOpenAIResponsesRequest(&gin.Context{}, info, request)
	require.NoError(t, err)
	chatRequest, ok := converted.(*dto.GeneralOpenAIRequest)
	require.True(t, ok)
	require.Len(t, chatRequest.Messages, 2)
	require.Equal(t, "system", chatRequest.Messages[0].Role)
	require.Equal(t, "user", chatRequest.Messages[1].Role)
}

func TestResponsesCustomExecRestoresReasoningForToolContinuation(t *testing.T) {
	var request dto.OpenAIResponsesRequest
	require.NoError(t, common.Unmarshal([]byte(`{
		"model":"deepseek-v4-flash",
		"input":[
			{"role":"developer","content":[{"type":"input_text","text":"Use exec."}]},
			{"type":"reasoning","summary":[{"type":"summary_text","text":"I should call exec."}]},
			{"type":"custom_tool_call","call_id":"call_1","name":"exec","input":"echo OK"},
			{"type":"custom_tool_call_output","call_id":"call_1","output":"OK"}
		],
		"tools":[{"type":"custom","name":"exec","description":"Run a command"}]
	}`), &request))
	info := &relaycommon.RelayInfo{
		RelayFormat: types.RelayFormatOpenAIResponses,
		RelayMode:   relayconstant.RelayModeResponses,
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "deepseek-v4-flash"},
	}

	converted, err := (&Adaptor{}).ConvertOpenAIResponsesRequest(&gin.Context{}, info, request)
	require.NoError(t, err)
	chatRequest, ok := converted.(*dto.GeneralOpenAIRequest)
	require.True(t, ok)
	require.Len(t, chatRequest.Messages, 3)
	require.Equal(t, "system", chatRequest.Messages[0].Role)
	require.Equal(t, "assistant", chatRequest.Messages[1].Role)
	require.Equal(t, "I should call exec.", chatRequest.Messages[1].GetReasoningContent())
	require.Equal(t, "tool", chatRequest.Messages[2].Role)
	require.Equal(t, "call_1", chatRequest.Messages[2].ToolCallId)
}

func TestResponsesCustomExecMergesMultipleOutputsForOneCallID(t *testing.T) {
	var request dto.OpenAIResponsesRequest
	require.NoError(t, common.Unmarshal([]byte(`{
		"model":"deepseek-v4-flash",
		"input":[
			{"role":"user","content":"run exec"},
			{"type":"reasoning","summary":[]},
			{"type":"custom_tool_call","call_id":"call_exec","name":"exec","input":"echo OK"},
			{"type":"custom_tool_call_output","call_id":"call_exec","output":"part one"},
			{"type":"custom_tool_call_output","call_id":"call_exec","output":"part two"}
		],
		"tools":[{"type":"custom","name":"exec","description":"Run a command"}]
	}`), &request))
	info := &relaycommon.RelayInfo{
		RelayFormat: types.RelayFormatOpenAIResponses,
		RelayMode:   relayconstant.RelayModeResponses,
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "deepseek-v4-flash"},
	}

	converted, err := (&Adaptor{}).ConvertOpenAIResponsesRequest(&gin.Context{}, info, request)
	require.NoError(t, err)
	chatRequest, ok := converted.(*dto.GeneralOpenAIRequest)
	require.True(t, ok)
	require.Len(t, chatRequest.Messages, 3)
	require.Equal(t, "assistant", chatRequest.Messages[1].Role)
	require.Equal(t, "tool", chatRequest.Messages[2].Role)
	require.Equal(t, "call_exec", chatRequest.Messages[2].ToolCallId)
	require.Equal(t, "part one\npart two", chatRequest.Messages[2].StringContent())
	require.NotNil(t, chatRequest.Messages[1].ReasoningContent)
}

func TestResponsesCustomExecHistorySelectsChatBridgeWithoutToolDeclaration(t *testing.T) {
	var request dto.OpenAIResponsesRequest
	require.NoError(t, common.Unmarshal([]byte(`{
		"model":"deepseek-v4-flash",
		"input":[
			{"type":"custom_tool_call","call_id":"call_exec","name":"exec","input":"echo OK"},
			{"type":"custom_tool_call_output","call_id":"call_exec","output":"OK"}
		]
	}`), &request))
	info := &relaycommon.RelayInfo{
		RelayFormat: types.RelayFormatOpenAIResponses,
		RelayMode:   relayconstant.RelayModeResponses,
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "deepseek-v4-flash"},
	}

	converted, err := (&Adaptor{}).ConvertOpenAIResponsesRequest(&gin.Context{}, info, request)
	require.NoError(t, err)
	chatRequest, ok := converted.(*dto.GeneralOpenAIRequest)
	require.True(t, ok)
	require.Len(t, chatRequest.Messages, 2)
	require.Equal(t, relayconstant.RelayModeChatCompletions, info.RelayMode)
}

func TestResponsesApplyPatchDuplicateOutputsUseChatBridge(t *testing.T) {
	var request dto.OpenAIResponsesRequest
	require.NoError(t, common.Unmarshal([]byte(`{
		"model":"deepseek-v4-flash",
		"input":[
			{"type":"custom_tool_call","call_id":"call_patch","name":"apply_patch","input":"patch"},
			{"type":"custom_tool_call_output","id":"output_1","call_id":"call_patch","output":"part one"},
			{"type":"custom_tool_call_output","id":"output_2","call_id":"call_patch","output":"part two"}
		],
		"tools":[{"type":"custom","name":"apply_patch","description":"Apply a patch"}]
	}`), &request))
	info := &relaycommon.RelayInfo{
		RelayFormat: types.RelayFormatOpenAIResponses,
		RelayMode:   relayconstant.RelayModeResponses,
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "deepseek-v4-flash"},
	}

	converted, err := (&Adaptor{}).ConvertOpenAIResponsesRequest(&gin.Context{}, info, request)
	require.NoError(t, err)
	chatRequest, ok := converted.(*dto.GeneralOpenAIRequest)
	require.True(t, ok)
	require.Len(t, chatRequest.Messages, 2)
	require.Equal(t, relayconstant.RelayModeChatCompletions, info.RelayMode)
	require.Equal(t, "part one\npart two", chatRequest.Messages[1].StringContent())
}

func TestResponsesApplyPatchOnlyKeepsNativeEndpoint(t *testing.T) {
	var request dto.OpenAIResponsesRequest
	require.NoError(t, common.Unmarshal([]byte(`{
		"model":"deepseek-v4-flash",
		"input":"Apply the patch",
		"tools":[{"type":"custom","name":"apply_patch","description":"Apply a patch"}]
	}`), &request))
	info := &relaycommon.RelayInfo{
		RelayFormat: types.RelayFormatOpenAIResponses,
		RelayMode:   relayconstant.RelayModeResponses,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelBaseUrl:    "https://api.deepseek.com",
			UpstreamModelName: "deepseek-v4-flash",
		},
	}

	converted, err := (&Adaptor{}).ConvertOpenAIResponsesRequest(&gin.Context{}, info, request)
	require.NoError(t, err)
	_, ok := converted.(dto.OpenAIResponsesRequest)
	require.True(t, ok)
	require.Equal(t, relayconstant.RelayModeResponses, info.RelayMode)

	requestURL, err := (&Adaptor{}).GetRequestURL(info)
	require.NoError(t, err)
	require.Equal(t, "https://api.deepseek.com/responses", requestURL)
}

func TestResponsesFunctionToolsKeepNativeEndpoint(t *testing.T) {
	var request dto.OpenAIResponsesRequest
	require.NoError(t, common.Unmarshal([]byte(`{
		"model":"deepseek-v4-flash",
		"input":"Run echo OK",
		"tools":[{"type":"function","name":"exec","parameters":{"type":"object"}}]
	}`), &request))
	info := &relaycommon.RelayInfo{
		RelayFormat: types.RelayFormatOpenAIResponses,
		RelayMode:   relayconstant.RelayModeResponses,
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "deepseek-v4-flash"},
	}

	converted, err := (&Adaptor{}).ConvertOpenAIResponsesRequest(&gin.Context{}, info, request)
	require.NoError(t, err)
	_, ok := converted.(dto.OpenAIResponsesRequest)
	require.True(t, ok)
	require.Equal(t, relayconstant.RelayModeResponses, info.RelayMode)
}
