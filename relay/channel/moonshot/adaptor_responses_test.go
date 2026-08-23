package moonshot

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func newKimiResponsesRelayInfo(model string) *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: model},
	}
}

func TestConvertOpenAIResponsesRequestToChatCompletions(t *testing.T) {
	var request dto.OpenAIResponsesRequest
	require.NoError(t, common.Unmarshal([]byte(`{
		"model":"k3",
		"input":"Reply exactly hello",
		"max_output_tokens":32,
		"stream":false
	}`), &request))

	converted, err := (&Adaptor{}).ConvertOpenAIResponsesRequest(
		&gin.Context{}, newKimiResponsesRelayInfo("k3"), request,
	)
	require.NoError(t, err)
	chatRequest, ok := converted.(*dto.GeneralOpenAIRequest)
	require.True(t, ok)
	require.Equal(t, "k3", chatRequest.Model)
	require.NotNil(t, chatRequest.MaxCompletionTokens)
	require.Equal(t, uint(32), *chatRequest.MaxCompletionTokens)
	require.Len(t, chatRequest.Messages, 1)
}

func TestConvertOpenAIResponsesRequestPreservesTools(t *testing.T) {
	var request dto.OpenAIResponsesRequest
	require.NoError(t, common.Unmarshal([]byte(`{
		"model":"k3",
		"input":[{"role":"user","content":"get the weather"}],
		"tools":[{"type":"function","name":"get_weather","description":"Get weather","parameters":{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]}}]
	}`), &request))

	converted, err := (&Adaptor{}).ConvertOpenAIResponsesRequest(
		&gin.Context{}, newKimiResponsesRelayInfo("k3"), request,
	)
	require.NoError(t, err)
	chatRequest, ok := converted.(*dto.GeneralOpenAIRequest)
	require.True(t, ok)
	require.Len(t, chatRequest.Tools, 1)
}
