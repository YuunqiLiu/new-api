package minimax

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestResponsesUsesChatCompletionsURL(t *testing.T) {
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelBaseUrl: "https://api.minimax.chat",
		},
		RelayMode: relayconstant.RelayModeResponses,
	}

	requestURL, err := (&Adaptor{}).GetRequestURL(info)
	require.NoError(t, err)
	require.Equal(t, "https://api.minimax.chat/v1/text/chatcompletion_v2", requestURL)
}

func TestConvertOpenAIResponsesRequestToChatCompletions(t *testing.T) {
	var request dto.OpenAIResponsesRequest
	require.NoError(t, common.Unmarshal([]byte(`{
		"model":"MiniMax-M2.5",
		"input":"Reply exactly hello",
		"max_output_tokens":32,
		"tools":[{"type":"function","name":"get_weather","parameters":{"type":"object","properties":{"city":{"type":"string"}}}}]
	}`), &request))

	converted, err := (&Adaptor{}).ConvertOpenAIResponsesRequest(
		&gin.Context{}, &relaycommon.RelayInfo{}, request,
	)
	require.NoError(t, err)
	chatRequest, ok := converted.(*dto.GeneralOpenAIRequest)
	require.True(t, ok)
	require.Equal(t, "MiniMax-M2.5", chatRequest.Model)
	require.NotNil(t, chatRequest.MaxCompletionTokens)
	require.Equal(t, uint(32), *chatRequest.MaxCompletionTokens)
	require.Len(t, chatRequest.Messages, 1)
	require.Len(t, chatRequest.Tools, 1)
}
