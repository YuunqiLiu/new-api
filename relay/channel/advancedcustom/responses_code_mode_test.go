package advancedcustom

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/relayconvert"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func responsesChatBridgeFixture(t *testing.T, stream bool) (*Adaptor, *relaycommon.RelayInfo, *httptest.ResponseRecorder) {
	t.Helper()
	info := advancedCustomRelayInfo(&dto.AdvancedCustomConfig{
		Routes: []dto.AdvancedCustomRoute{{
			IncomingPath: "/v1/responses",
			UpstreamPath: "/v2/tokenplan/personal/chat/completions",
			Converter:    relayconvert.ConverterOpenAIResponsesToOpenAIChat,
		}},
	})
	info.RelayFormat = types.RelayFormatOpenAIResponses
	info.RelayMode = relayconstant.RelayModeResponses
	info.RequestURLPath = "/v1/responses"
	info.IsStream = stream
	info.DisablePing = true
	return &Adaptor{}, info, httptest.NewRecorder()
}

func TestResponsesChatBridgeNormalizesCodeModeHistory(t *testing.T) {
	for _, reasoning := range []string{"", "Inspect the result."} {
		t.Run("reasoning="+reasoning, func(t *testing.T) {
			adaptor, info, _ := responsesChatBridgeFixture(t, false)
			request := dto.OpenAIResponsesRequest{
				Model: "deepseek-v4.1-flash",
				Tools: mustAdvancedCustomRawMessage(t, []any{
					map[string]any{"type": "custom", "name": "exec", "format": map[string]any{"type": "text"}},
					map[string]any{"type": "function", "name": "echo", "parameters": map[string]any{"type": "object"}},
				}),
				Input: mustAdvancedCustomRawMessage(t, []any{
					map[string]any{"role": "user", "content": "Run the two tools."},
					map[string]any{"type": "reasoning", "reasoning_content": reasoning},
					map[string]any{"type": "custom_tool_call", "call_id": "call_exec", "name": "exec", "input": "notify('OK')"},
					map[string]any{"type": "function_call", "call_id": "call_echo", "name": "echo", "arguments": "{}"},
					map[string]any{"type": "function_call_output", "call_id": "call_echo", "output": "ECHO"},
					map[string]any{"type": "custom_tool_call_output", "id": "out_1", "call_id": "call_exec", "output": "Script completed"},
					map[string]any{"type": "custom_tool_call_output", "id": "out_2", "call_id": "call_exec", "output": "OK"},
				}),
			}
			originalInput := string(request.Input)
			converted, err := adaptor.ConvertOpenAIResponsesRequest(advancedCustomGinContext("/v1/responses"), info, request)
			require.NoError(t, err)
			chat, ok := converted.(*dto.GeneralOpenAIRequest)
			require.True(t, ok)
			assert.Equal(t, types.RelayFormatOpenAI, info.GetFinalRequestRelayFormat())
			require.Len(t, chat.Tools, 2)
			assert.Equal(t, "function", chat.Tools[0].Type)
			assert.Equal(t, "exec", chat.Tools[0].Function.Name)
			var callIDs, outputIDs, outputs []string
			for _, message := range chat.Messages {
				for _, call := range message.ParseToolCalls() {
					callIDs = append(callIDs, call.ID)
					if call.ID == "call_exec" {
						require.NotNil(t, message.ReasoningContent)
						assert.Equal(t, reasoning, message.GetReasoningContent())
						assert.JSONEq(t, `{"input":"notify('OK')"}`, call.Function.Arguments)
					}
				}
				if message.Role == "tool" {
					outputIDs = append(outputIDs, message.ToolCallId)
					outputs = append(outputs, message.StringContent())
				}
			}
			assert.Equal(t, []string{"call_exec", "call_echo"}, callIDs)
			assert.Equal(t, callIDs, outputIDs)
			assert.Equal(t, []string{"Script completed\nOK", "ECHO"}, outputs)
			assert.Equal(t, originalInput, string(request.Input), "do not mutate caller history on retry")
		})
	}
}

func TestResponsesChatBridgeRestoresCustomAndFunctionTools(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(map[bool]string{false: "json", true: "sse"}[stream], func(t *testing.T) {
			adaptor, info, recorder := responsesChatBridgeFixture(t, stream)
			oldTimeout := constant.StreamingTimeout
			constant.StreamingTimeout = 30
			t.Cleanup(func() { constant.StreamingTimeout = oldTimeout })
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			c.Request.Header.Set("Content-Type", "application/json")
			c.Set(common.RequestIdKey, "advanced-custom-code-mode-test")
			_, err := adaptor.ConvertOpenAIResponsesRequest(c, info, dto.OpenAIResponsesRequest{
				Model: "deepseek-v4.1-flash",
				Input: mustAdvancedCustomRawMessage(t, "Use the tools."),
				Tools: mustAdvancedCustomRawMessage(t, []any{
					map[string]any{"type": "custom", "name": "exec"},
					map[string]any{"type": "function", "name": "echo", "parameters": map[string]any{"type": "object"}},
				}),
			})
			require.NoError(t, err)
			body := `{"id":"chat_test","object":"chat.completion","model":"deepseek-v4.1-flash","choices":[{"index":0,"message":{"role":"assistant","tool_calls":[{"id":"call_exec","type":"function","function":{"name":"exec","arguments":"{\"input\":\"notify('OK')\"}"}},{"id":"call_echo","type":"function","function":{"name":"echo","arguments":"{}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":2,"completion_tokens":3,"total_tokens":5}}`
			if stream {
				body = strings.Join([]string{
					`data: {"id":"chat_test","object":"chat.completion.chunk","model":"deepseek-v4.1-flash","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_exec","type":"function","function":{"name":"exec","arguments":"{\"input\":\"notify('OK')\"}"}},{"index":1,"id":"call_echo","type":"function","function":{"name":"echo","arguments":"{}"}}]},"finish_reason":null}]}`,
					`data: {"id":"chat_test","object":"chat.completion.chunk","model":"deepseek-v4.1-flash","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
					`data: [DONE]`, "",
				}, "\n")
			}
			_, apiErr := adaptor.DoResponse(c, &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(body)),
			}, info)
			require.Nil(t, apiErr)
			got := recorder.Body.String()
			assert.Contains(t, got, `"type":"custom_tool_call"`)
			assert.Contains(t, got, `"input":"notify('OK')"`)
			assert.Contains(t, got, `"call_id":"call_exec"`)
			assert.Contains(t, got, `"type":"function_call"`)
			assert.Contains(t, got, `"call_id":"call_echo"`)
			assert.Contains(t, got, `"arguments":"{}"`)
			if stream {
				assert.Contains(t, got, "event: response.custom_tool_call_input.delta")
				assert.Contains(t, got, "event: response.custom_tool_call_input.done")
				assert.Contains(t, got, "event: response.function_call_arguments.done")
				assert.Contains(t, got, "event: response.completed")
			}
		})
	}
}

func TestResponsesPassthroughKeepsOriginalCodeModeHistory(t *testing.T) {
	adaptor := &Adaptor{}
	info := advancedCustomRelayInfo(&dto.AdvancedCustomConfig{
		Routes: []dto.AdvancedCustomRoute{{
			IncomingPath: "/v1/responses", UpstreamPath: "/v1/responses", Converter: relayconvert.ConverterNone,
		}},
	})
	info.RequestURLPath = "/v1/responses"
	info.RelayFormat = types.RelayFormatOpenAIResponses
	info.RelayMode = relayconstant.RelayModeResponses
	var request dto.OpenAIResponsesRequest
	require.NoError(t, common.Unmarshal([]byte(`{
		"model":"native-test",
		"tools":[{"type":"custom","name":"exec"}],
		"input":[
			{"type":"custom_tool_call","call_id":"call_1","name":"exec","input":"notify('OK')"},
			{"type":"custom_tool_call_output","id":"out_1","call_id":"call_1","output":"first"},
			{"type":"custom_tool_call_output","id":"out_2","call_id":"call_1","output":"second"}
		]
	}`), &request))
	converted, err := adaptor.ConvertOpenAIResponsesRequest(advancedCustomGinContext("/v1/responses"), info, request)
	require.NoError(t, err)
	body, err := common.Marshal(converted)
	require.NoError(t, err)
	var got dto.OpenAIResponsesRequest
	require.NoError(t, common.Unmarshal(body, &got))
	assert.JSONEq(t, string(request.Input), string(got.Input))
	assert.JSONEq(t, string(request.Tools), string(got.Tools))
}
