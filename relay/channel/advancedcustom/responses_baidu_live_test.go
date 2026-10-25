package advancedcustom

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This is opt-in and uses a separate in-process gateway: no production database,
// Redis, routing configuration, user history or running requests are touched.
// Tool results are mocked; generated tool input is never executed.
func TestBaiduResponsesLive(t *testing.T) {
	key := os.Getenv("NEWAPI_BAIDU_LIVE_KEY")
	if key == "" {
		t.Skip("set NEWAPI_BAIDU_LIVE_KEY to run the isolated upstream integration test")
	}
	oldMode := gin.Mode()
	gin.SetMode(gin.TestMode)
	t.Cleanup(func() { gin.SetMode(oldMode) })
	oldTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 90
	t.Cleanup(func() { constant.StreamingTimeout = oldTimeout })
	client := &http.Client{Timeout: 90 * time.Second}
	router := gin.New()
	router.POST("/v1/responses", func(c *gin.Context) {
		var request dto.OpenAIResponsesRequest
		if err := common.DecodeJson(c.Request.Body, &request); err != nil {
			c.AbortWithStatus(http.StatusBadRequest)
			return
		}
		adaptor, info, _ := responsesChatBridgeFixture(t, request.Stream != nil && *request.Stream)
		info.ChannelBaseUrl = "https://qianfan.baidubce.com"
		info.ApiKey = key
		info.OriginModelName = request.Model
		info.UpstreamModelName = request.Model
		c.Set(common.RequestIdKey, "isolated-baidu-responses-test")
		converted, err := adaptor.ConvertOpenAIResponsesRequest(c, info, request)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		body, err := common.Marshal(converted)
		if err != nil {
			c.AbortWithStatus(http.StatusInternalServerError)
			return
		}
		upstreamURL, err := adaptor.GetRequestURL(info)
		if err != nil {
			c.AbortWithStatus(http.StatusInternalServerError)
			return
		}
		upstreamRequest, err := http.NewRequestWithContext(c.Request.Context(), http.MethodPost, upstreamURL, bytes.NewReader(body))
		if err != nil {
			c.AbortWithStatus(http.StatusInternalServerError)
			return
		}
		upstreamRequest.Header.Set("Content-Type", "application/json")
		if err := adaptor.SetupRequestHeader(c, &upstreamRequest.Header, info); err != nil {
			c.AbortWithStatus(http.StatusInternalServerError)
			return
		}
		response, err := client.Do(upstreamRequest)
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			data, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
			c.Data(response.StatusCode, "application/json", data)
			return
		}
		if _, apiErr := adaptor.DoResponse(c, response, info); apiErr != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": apiErr.Error()})
		}
	})
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)

	for _, model := range []string{"deepseek-v4.1-flash", "deepseek-v4-pro"} {
		t.Run(model, func(t *testing.T) {
			for _, stream := range []bool{false, true} {
				transport := map[bool]string{false: "json", true: "sse"}[stream]
				t.Run("plain/"+transport, func(t *testing.T) {
					response := baiduLiveResponse(t, client, server.URL, map[string]any{
						"model": model, "stream": stream, "max_output_tokens": 1024,
						"input": []any{
							map[string]any{"role": "developer", "content": "Follow the user instruction."},
							map[string]any{"role": "user", "content": "Reply with exactly OK"},
						},
					})
					assert.Equal(t, "OK", baiduLiveOutputText(response))
				})
				for _, custom := range []bool{false, true} {
					toolType := map[bool]string{false: "function", true: "custom"}[custom]
					t.Run(toolType+"/"+transport, func(t *testing.T) {
						name := "diagnostic_echo"
						definition := map[string]any{
							"type": "function", "name": name, "description": "Return the supplied value.",
							"parameters": map[string]any{
								"type": "object", "properties": map[string]any{"value": map[string]any{"type": "string"}},
								"required": []string{"value"}, "additionalProperties": false,
							},
						}
						if custom {
							name = "exec"
							definition = map[string]any{
								"type": "custom", "name": name,
								"description": "A diagnostic tool: supply raw text OK. The tool returns OK.",
								"format":      map[string]any{"type": "text"},
							}
						}
						input := []any{
							map[string]any{"role": "developer", "content": "Call the requested tool once. After its output is provided, reply with exactly OK and do not call tools again."},
							map[string]any{"role": "user", "content": "Call " + name + " with OK, then reply exactly OK after receiving the result."},
						}
						request := map[string]any{
							"model": model, "stream": stream, "max_output_tokens": 2048,
							"input": input, "tools": []any{definition}, "tool_choice": "auto",
						}
						first := baiduLiveResponse(t, client, server.URL, request)
						output, ok := first["output"].([]any)
						require.True(t, ok)
						var callID string
						for _, value := range output {
							item, ok := value.(map[string]any)
							require.True(t, ok)
							if item["name"] == name {
								assert.Equal(t, map[bool]string{false: "function_call", true: "custom_tool_call"}[custom], item["type"])
								callID, _ = item["call_id"].(string)
							}
						}
						require.NotEmpty(t, callID, "upstream must actually call the requested tool")
						input = append(input, output...)
						outputType := map[bool]string{false: "function_call_output", true: "custom_tool_call_output"}[custom]
						input = append(input, map[string]any{"type": outputType, "id": "out_1", "call_id": callID, "output": "OK"})
						if custom {
							input = append(input, map[string]any{"type": outputType, "id": "out_2", "call_id": callID, "output": "Diagnostic notification completed. The requested result is OK."})
						}
						request["input"] = input
						second := baiduLiveResponse(t, client, server.URL, request)
						assert.Equal(t, "OK", baiduLiveOutputText(second))
					})
				}
			}
		})
	}
}

func baiduLiveResponse(t *testing.T, client *http.Client, serverURL string, request map[string]any) map[string]any {
	t.Helper()
	body, err := common.Marshal(request)
	require.NoError(t, err)
	started := time.Now()
	response, err := client.Post(serverURL+"/v1/responses", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, response.StatusCode, string(data))
	var result map[string]any
	if request["stream"] == true {
		for _, line := range strings.Split(string(data), "\n") {
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			var event map[string]any
			require.NoError(t, common.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event))
			if event["type"] == "response.completed" {
				result, _ = event["response"].(map[string]any)
			}
			assert.NotEqual(t, "response.failed", event["type"])
		}
		require.NotNil(t, result, "Responses SSE must have a response.completed event")
	} else {
		require.NoError(t, common.Unmarshal(data, &result))
	}
	assert.Equal(t, "completed", result["status"])
	t.Logf("HTTP %d model=%s stream=%v completed in %s", response.StatusCode, request["model"], request["stream"], time.Since(started).Round(time.Millisecond))
	return result
}

func baiduLiveOutputText(response map[string]any) string {
	var text strings.Builder
	output, _ := response["output"].([]any)
	for _, value := range output {
		item, _ := value.(map[string]any)
		if item["type"] != "message" {
			continue
		}
		content, _ := item["content"].([]any)
		for _, part := range content {
			fields, _ := part.(map[string]any)
			value, _ := fields["text"].(string)
			text.WriteString(value)
		}
	}
	return strings.TrimSpace(text.String())
}
