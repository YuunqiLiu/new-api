package zhipu_4v

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestConvertOpenAIResponsesRequestToChatCompletions(t *testing.T) {
	var request dto.OpenAIResponsesRequest
	require.NoError(t, common.Unmarshal([]byte(`{
		"model":"glm-5.2",
		"input":"Reply exactly hello",
		"max_output_tokens":32,
		"stream":false
	}`), &request))

	converted, err := (&Adaptor{}).ConvertOpenAIResponsesRequest(
		&gin.Context{},
		&relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "glm-5.2"}},
		request,
	)
	require.NoError(t, err)
	chatRequest, ok := converted.(*dto.GeneralOpenAIRequest)
	require.True(t, ok)
	require.Equal(t, "glm-5.2", chatRequest.Model)
	require.NotNil(t, chatRequest.MaxTokens)
	require.Equal(t, uint(32), *chatRequest.MaxTokens)
	require.Len(t, chatRequest.Messages, 1)
}

func TestConvertOpenAIResponsesRequestPreservesTools(t *testing.T) {
	var request dto.OpenAIResponsesRequest
	require.NoError(t, common.Unmarshal([]byte(`{
		"model":"glm-5.3",
		"input":[{"role":"user","content":"get the weather"}],
		"tools":[{"type":"function","name":"get_weather","description":"Get weather","parameters":{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]}}]
	}`), &request))

	converted, err := (&Adaptor{}).ConvertOpenAIResponsesRequest(
		&gin.Context{},
		&relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "glm-5.3"}},
		request,
	)
	require.NoError(t, err)
	chatRequest, ok := converted.(*dto.GeneralOpenAIRequest)
	require.True(t, ok)
	require.Len(t, chatRequest.Tools, 1)
}

func TestConvertOpenAIResponsesRequestPreservesGLM53ReasoningEffort(t *testing.T) {
	for _, model := range []string{"glm-5.3", "glm-5.3-flash"} {
		t.Run(model, func(t *testing.T) {
			var request dto.OpenAIResponsesRequest
			require.NoError(t, common.Unmarshal([]byte(`{
				"model":"`+model+`",
				"input":"Reply exactly OK",
				"reasoning":{"effort":"high"}
			}`), &request))

			info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: model}}
			converted, err := (&Adaptor{}).ConvertOpenAIResponsesRequest(&gin.Context{}, info, request)
			require.NoError(t, err)
			chatRequest, ok := converted.(*dto.GeneralOpenAIRequest)
			require.True(t, ok)
			require.Equal(t, "high", chatRequest.ReasoningEffort)
			require.Equal(t, "high", info.GetReasoningEffort())
		})
	}
}

func TestConvertOpenAIResponsesRequestRejectsUnsupportedGLM53ReasoningEffort(t *testing.T) {
	var request dto.OpenAIResponsesRequest
	require.NoError(t, common.Unmarshal([]byte(`{
		"model":"glm-5.3",
		"input":"Reply exactly OK",
		"reasoning":{"effort":"medium"}
	}`), &request))

	_, err := (&Adaptor{}).ConvertOpenAIResponsesRequest(
		&gin.Context{},
		&relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "glm-5.3"}},
		request,
	)
	require.ErrorContains(t, err, "use low, high, or max")
}

func TestConvertOpenAIResponsesRequestNormalizesDeveloperForGLM53Flash(t *testing.T) {
	var request dto.OpenAIResponsesRequest
	require.NoError(t, common.Unmarshal([]byte(`{
		"model":"glm-5.3-flash",
		"instructions":"Original system content.",
		"input":[
			{"role":"developer","content":[{"type":"input_text","text":"Codex developer content."}]},
			{"role":"user","content":[{"type":"input_text","text":"Reply with exactly OK"}]}
		]
	}`), &request))

	converted, err := (&Adaptor{}).ConvertOpenAIResponsesRequest(
		&gin.Context{},
		&relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "glm-5.3-flash"}},
		request,
	)
	require.NoError(t, err)
	chatRequest, ok := converted.(*dto.GeneralOpenAIRequest)
	require.True(t, ok)
	require.Len(t, chatRequest.Messages, 2)
	require.Equal(t, "system", chatRequest.Messages[0].Role)
	require.Equal(t, "Original system content.\n\nCodex developer content.", chatRequest.Messages[0].StringContent())
	require.Equal(t, "user", chatRequest.Messages[1].Role)
	require.Equal(t, "Reply with exactly OK", chatRequest.Messages[1].StringContent())
}

func TestConvertOpenAIResponsesRequestKeepsDeveloperForOtherGLMModels(t *testing.T) {
	for _, model := range []string{"glm-5.2", "glm-5.3"} {
		t.Run(model, func(t *testing.T) {
			var request dto.OpenAIResponsesRequest
			require.NoError(t, common.Unmarshal([]byte(`{
				"model":"`+model+`",
				"input":[{"role":"developer","content":[{"type":"input_text","text":"Developer content."}]}]
			}`), &request))

			converted, err := (&Adaptor{}).ConvertOpenAIResponsesRequest(
				&gin.Context{},
				&relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: model}},
				request,
			)
			require.NoError(t, err)
			chatRequest, ok := converted.(*dto.GeneralOpenAIRequest)
			require.True(t, ok)
			require.Len(t, chatRequest.Messages, 1)
			require.Equal(t, "developer", chatRequest.Messages[0].Role)
		})
	}
}

func TestConvertOpenAIResponsesRequestPreservesToolContinuationForGLM53Flash(t *testing.T) {
	var request dto.OpenAIResponsesRequest
	require.NoError(t, common.Unmarshal([]byte(`{
		"model":"glm-5.3-flash",
		"input":[
			{"role":"developer","content":[{"type":"input_text","text":"Use the tool."}]},
			{"type":"function_call","call_id":"call_123","name":"get_weather","arguments":"{\"city\":\"Hangzhou\"}"},
			{"type":"function_call_output","call_id":"call_123","output":"sunny"}
		]
	}`), &request))

	converted, err := (&Adaptor{}).ConvertOpenAIResponsesRequest(
		&gin.Context{},
		&relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "glm-5.3-flash"}},
		request,
	)
	require.NoError(t, err)
	chatRequest, ok := converted.(*dto.GeneralOpenAIRequest)
	require.True(t, ok)
	require.Len(t, chatRequest.Messages, 3)
	require.Equal(t, "system", chatRequest.Messages[0].Role)
	require.Equal(t, "assistant", chatRequest.Messages[1].Role)
	require.Equal(t, "tool", chatRequest.Messages[2].Role)
	require.Equal(t, "call_123", chatRequest.Messages[2].ToolCallId)
	toolCalls := chatRequest.Messages[1].ParseToolCalls()
	require.Len(t, toolCalls, 1)
	require.Equal(t, "call_123", toolCalls[0].ID)
}

func TestConvertOpenAIResponsesRequestMergesIncrementalToolOutputsForGLM(t *testing.T) {
	var request dto.OpenAIResponsesRequest
	require.NoError(t, common.Unmarshal([]byte(`{
		"model":"glm-5.3",
		"input":[
			{"role":"user","content":"run the tool"},
			{"type":"reasoning","summary":[{"type":"summary_text","text":"select tool"}]},
			{"type":"function_call","call_id":"call_123","name":"exec","arguments":"{}"},
			{"type":"function_call_output","call_id":"call_123","output":"part one"},
			{"type":"function_call_output","call_id":"call_123","output":"part two"}
		],
		"tools":[{"type":"function","name":"exec","parameters":{"type":"object"}}]
	}`), &request))

	converted, err := (&Adaptor{}).ConvertOpenAIResponsesRequest(
		&gin.Context{},
		&relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "glm-5.3"}},
		request,
	)
	require.NoError(t, err)
	chatRequest, ok := converted.(*dto.GeneralOpenAIRequest)
	require.True(t, ok)
	require.Len(t, chatRequest.Messages, 3)
	require.Equal(t, "select tool", chatRequest.Messages[1].GetReasoningContent())
	require.Equal(t, "tool", chatRequest.Messages[2].Role)
	require.Equal(t, "call_123", chatRequest.Messages[2].ToolCallId)
	require.Equal(t, "part one\npart two", chatRequest.Messages[2].StringContent())
}
