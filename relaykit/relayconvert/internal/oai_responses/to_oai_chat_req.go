package oairesponses

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/relaykit/dto"
	kitutil "github.com/QuantumNous/new-api/relaykit/relayconvert/kitutil"
)

const (
	responsesInputTypeFunctionCall       = "function_call"
	responsesInputTypeFunctionCallOutput = "function_call_output"
	responsesInputTypeCustomToolCall     = "custom_tool_call"
	responsesInputTypeCustomToolOutput   = "custom_tool_call_output"
	responsesInputTypeReasoning          = "reasoning"
)

const (
	ResponsesInputTypeFunctionCall       = responsesInputTypeFunctionCall
	ResponsesInputTypeFunctionCallOutput = responsesInputTypeFunctionCallOutput
	ResponsesInputTypeCustomToolCall     = responsesInputTypeCustomToolCall
	ResponsesInputTypeCustomToolOutput   = responsesInputTypeCustomToolOutput
)

// ResponsesToChatOptions controls compatibility normalization when a
// Responses request is sent to a Chat Completions-only provider.  The
// normalization is opt-in because providers that natively understand the
// Responses item stream should receive the original history unchanged.
type ResponsesToChatOptions struct {
	// NormalizeToolHistory coalesces multiple output items for one call_id and
	// puts each logical tool call immediately before its merged output.
	NormalizeToolHistory bool
	// PreserveToolReasoning carries a reasoning item associated with a tool call
	// into the assistant message as reasoning_content.  A present-but-empty
	// reasoning item is preserved as a non-nil empty string.
	PreserveToolReasoning bool
}

func ResponsesRequestToChatCompletionsRequest(req *dto.OpenAIResponsesRequest) (*dto.GeneralOpenAIRequest, error) {
	return ResponsesRequestToChatCompletionsRequestWithOptions(req, ResponsesToChatOptions{})
}

func ResponsesRequestToChatCompletionsRequestWithOptions(req *dto.OpenAIResponsesRequest, options ResponsesToChatOptions) (*dto.GeneralOpenAIRequest, error) {
	if req == nil {
		return nil, errors.New("request is nil")
	}
	if req.Model == "" {
		return nil, errors.New("model is required")
	}
	if err := validateResponsesRequestChatUnsupportedFields(req); err != nil {
		return nil, err
	}

	workingRequest := req
	if options.NormalizeToolHistory {
		normalizedInput, err := normalizeResponsesToolHistory(req.Input)
		if err != nil {
			return nil, err
		}
		copyRequest := *req
		copyRequest.Input = normalizedInput
		workingRequest = &copyRequest
	}

	messages, err := responsesRequestMessagesToChat(workingRequest)
	if err != nil {
		return nil, err
	}
	if options.PreserveToolReasoning {
		if err := applyResponsesToolReasoning(req.Input, messages); err != nil {
			return nil, err
		}
	}

	tools, err := responsesRequestToolsToChat(workingRequest.Tools)
	if err != nil {
		return nil, err
	}

	toolChoice, err := responsesRequestToolChoiceToChat(workingRequest.ToolChoice)
	if err != nil {
		return nil, err
	}

	responseFormat, err := responsesRequestTextToChatResponseFormat(workingRequest.Text)
	if err != nil {
		return nil, err
	}

	out := &dto.GeneralOpenAIRequest{
		Model:                workingRequest.Model,
		Messages:             messages,
		Stream:               workingRequest.Stream,
		StreamOptions:        workingRequest.StreamOptions,
		MaxCompletionTokens:  workingRequest.MaxOutputTokens,
		Temperature:          workingRequest.Temperature,
		TopP:                 workingRequest.TopP,
		TopLogProbs:          workingRequest.TopLogProbs,
		ResponseFormat:       responseFormat,
		Tools:                tools,
		ToolChoice:           toolChoice,
		User:                 workingRequest.User,
		Store:                workingRequest.Store,
		Metadata:             workingRequest.Metadata,
		SafetyIdentifier:     workingRequest.SafetyIdentifier,
		PromptCacheRetention: workingRequest.PromptCacheRetention,
		EnableThinking:       workingRequest.EnableThinking,
		ThinkingBudget:       workingRequest.ThinkingBudget,
	}

	out.FrequencyPenalty, err = responsesRawFloat(workingRequest.FrequencyPenalty)
	if err != nil {
		return nil, fmt.Errorf("invalid frequency_penalty: %w", err)
	}
	out.PresencePenalty, err = responsesRawFloat(workingRequest.PresencePenalty)
	if err != nil {
		return nil, fmt.Errorf("invalid presence_penalty: %w", err)
	}

	if workingRequest.Reasoning != nil {
		out.ReasoningEffort = workingRequest.Reasoning.Effort
	}
	if workingRequest.ServiceTier != "" {
		out.ServiceTier, _ = kitutil.Marshal(workingRequest.ServiceTier)
	}
	if len(workingRequest.ParallelToolCalls) > 0 && kitutil.GetJsonType(workingRequest.ParallelToolCalls) == "boolean" {
		var parallelToolCalls bool
		if err := kitutil.Unmarshal(workingRequest.ParallelToolCalls, &parallelToolCalls); err == nil {
			out.ParallelTooCalls = &parallelToolCalls
		}
	}
	if len(workingRequest.PromptCacheKey) > 0 && kitutil.GetJsonType(workingRequest.PromptCacheKey) == "string" {
		var promptCacheKey string
		if err := kitutil.Unmarshal(workingRequest.PromptCacheKey, &promptCacheKey); err == nil {
			out.PromptCacheKey = promptCacheKey
		}
	}

	return out, nil
}

func validateResponsesRequestChatUnsupportedFields(req *dto.OpenAIResponsesRequest) error {
	unsupported := make([]string, 0, 4)
	if rawJSONPresent(req.Conversation) {
		unsupported = append(unsupported, "conversation")
	}
	if strings.TrimSpace(req.PreviousResponseID) != "" {
		unsupported = append(unsupported, "previous_response_id")
	}
	if rawJSONPresent(req.Prompt) {
		unsupported = append(unsupported, "prompt")
	}
	if rawJSONPresent(req.ContextManagement) {
		unsupported = append(unsupported, "context_management")
	}
	if len(unsupported) > 0 {
		return fmt.Errorf("responses to chat conversion does not support stateful fields: %s", strings.Join(unsupported, ", "))
	}
	return nil
}

func ValidateRequestChatUnsupportedFields(req *dto.OpenAIResponsesRequest) error {
	return validateResponsesRequestChatUnsupportedFields(req)
}

type responsesToolHistoryCall struct {
	outputItems  []map[string]any
	outputValues []any
}

type responsesToolReasoning struct {
	present bool
	text    string
}

// normalizeResponsesToolHistory turns the incremental tool-output history
// emitted by Code Mode into the single-result shape expected by Chat
// Completions providers.  It deliberately keys by call_id (the logical tool
// invocation) rather than item id (the individual history item).
func normalizeResponsesToolHistory(raw json.RawMessage) (json.RawMessage, error) {
	if !rawJSONPresent(raw) || kitutil.GetJsonType(raw) != "array" {
		return raw, nil
	}

	var items []map[string]any
	if err := kitutil.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("invalid responses input array: %w", err)
	}

	calls := make(map[string]*responsesToolHistoryCall)
	callOrder := make([]string, 0)
	callIDsSeen := make(map[string]struct{})
	for _, item := range items {
		itemType := strings.TrimSpace(kitutil.Interface2String(item["type"]))
		if itemType != responsesInputTypeFunctionCall && itemType != responsesInputTypeCustomToolCall {
			continue
		}
		callID := responsesCallID(item)
		if callID == "" {
			return nil, errors.New("tool call item is missing call_id")
		}
		if _, exists := callIDsSeen[callID]; exists {
			return nil, fmt.Errorf("duplicate tool call for call_id %s", callID)
		}
		callIDsSeen[callID] = struct{}{}
		calls[callID] = &responsesToolHistoryCall{}
		callOrder = append(callOrder, callID)
	}

	for _, item := range items {
		itemType := strings.TrimSpace(kitutil.Interface2String(item["type"]))
		if itemType != responsesInputTypeFunctionCallOutput && itemType != responsesInputTypeCustomToolOutput {
			continue
		}
		callID := strings.TrimSpace(kitutil.Interface2String(item["call_id"]))
		if callID == "" {
			return nil, errors.New("tool output item is missing call_id")
		}
		call, ok := calls[callID]
		if !ok {
			return nil, fmt.Errorf("tool output has no matching call_id %s", callID)
		}
		call.outputItems = append(call.outputItems, item)
		call.outputValues = append(call.outputValues, item["output"])
	}

	mergedOutput := make(map[string]map[string]any, len(calls))
	for _, callID := range callOrder {
		call := calls[callID]
		if len(call.outputItems) == 0 {
			continue
		}
		first := cloneResponsesItem(call.outputItems[0])
		first["output"] = mergeResponsesToolOutputValues(call.outputValues)
		// Preserve the terminal status when Code Mode supplied one on a later
		// incremental item.  Chat providers do not use it, but keeping it makes
		// the normalized history deterministic for callers that inspect it.
		for i := len(call.outputItems) - 1; i >= 0; i-- {
			if status := strings.TrimSpace(kitutil.Interface2String(call.outputItems[i]["status"])); status != "" {
				first["status"] = status
				break
			}
		}
		mergedOutput[callID] = first
	}

	// Rebuild the stream while keeping non-tool items in order.  Once a tool
	// round starts, interleaved context is held until all known outputs for that
	// round have been emitted, guaranteeing call/output adjacency.  Outputs
	// are emitted in first-call order even if their fragments arrived
	// interleaved.
	normalized := make([]map[string]any, 0, len(items))
	pendingCalls := make([]string, 0)
	deferred := make([]map[string]any, 0)
	emittedOutputs := make(map[string]struct{})
	flush := func() {
		if len(pendingCalls) == 0 {
			return
		}
		for _, callID := range pendingCalls {
			if output, ok := mergedOutput[callID]; ok {
				normalized = append(normalized, output)
				emittedOutputs[callID] = struct{}{}
			}
		}
		pendingCalls = pendingCalls[:0]
		if len(deferred) > 0 {
			normalized = append(normalized, deferred...)
			deferred = deferred[:0]
		}
	}

	for _, item := range items {
		itemType := strings.TrimSpace(kitutil.Interface2String(item["type"]))
		switch itemType {
		case responsesInputTypeFunctionCall, responsesInputTypeCustomToolCall:
			callID := responsesCallID(item)
			normalized = append(normalized, item)
			pendingCalls = append(pendingCalls, callID)
		case responsesInputTypeFunctionCallOutput, responsesInputTypeCustomToolOutput:
			callID := strings.TrimSpace(kitutil.Interface2String(item["call_id"]))
			if _, emitted := emittedOutputs[callID]; !emitted {
				flush()
			}
		default:
			if len(pendingCalls) > 0 {
				deferred = append(deferred, item)
			} else {
				normalized = append(normalized, item)
			}
		}
	}
	flush()

	result, err := kitutil.Marshal(normalized)
	if err != nil {
		return nil, fmt.Errorf("marshal normalized responses input: %w", err)
	}
	return result, nil
}

func cloneResponsesItem(item map[string]any) map[string]any {
	copy := make(map[string]any, len(item))
	for key, value := range item {
		copy[key] = value
	}
	return copy
}

func mergeResponsesToolOutputValues(values []any) any {
	if len(values) == 1 {
		return values[0]
	}
	allText := true
	textParts := make([]string, 0, len(values))
	for _, value := range values {
		switch typed := value.(type) {
		case nil:
			textParts = append(textParts, "")
		case string:
			textParts = append(textParts, typed)
		default:
			allText = false
		}
	}
	if allText {
		return strings.Join(textParts, "\n")
	}

	parts := make([]any, 0)
	for index, value := range values {
		if index > 0 {
			parts = append(parts, map[string]any{"type": "input_text", "text": "\n"})
		}
		switch typed := value.(type) {
		case []any:
			parts = append(parts, typed...)
		case []map[string]any:
			for _, part := range typed {
				parts = append(parts, part)
			}
		case string:
			parts = append(parts, map[string]any{"type": "input_text", "text": typed})
		case nil:
			parts = append(parts, map[string]any{"type": "input_text", "text": ""})
		default:
			if raw, err := kitutil.Marshal(typed); err == nil {
				parts = append(parts, map[string]any{"type": "input_text", "text": string(raw)})
			} else {
				parts = append(parts, map[string]any{"type": "input_text", "text": fmt.Sprint(typed)})
			}
		}
	}
	return parts
}

// applyResponsesToolReasoning keeps the reasoning metadata that belongs to a
// tool-call round.  The normal Responses-to-Chat conversion intentionally
// ignores reasoning items because Chat Completions has no standalone
// reasoning item.  Providers that validate or use reasoning_content for tool
// continuations can opt in through ResponsesToChatOptions instead.
func applyResponsesToolReasoning(raw json.RawMessage, messages []dto.Message) error {
	if !rawJSONPresent(raw) || kitutil.GetJsonType(raw) != "array" {
		return nil
	}
	var items []map[string]any
	if err := kitutil.Unmarshal(raw, &items); err != nil {
		return fmt.Errorf("invalid responses reasoning history: %w", err)
	}

	reasoningByCallID := make(map[string]responsesToolReasoning)
	pending := responsesToolReasoning{}
	for _, item := range items {
		itemType := strings.TrimSpace(kitutil.Interface2String(item["type"]))
		switch itemType {
		case responsesInputTypeReasoning:
			// A reasoning item may carry summary (the public Responses shape),
			// content, or an empty array.  Presence is meaningful even when no
			// plaintext is available (for example encrypted reasoning).
			pending.present = true
			texts := make([]string, 0)
			for _, key := range []string{"summary", "content", "text", "reasoning_content", "reasoning_text"} {
				if value, ok := item[key]; ok {
					texts = append(texts, responsesReasoningTexts(value)...)
				}
			}
			if len(texts) > 0 {
				text := strings.Join(texts, "\n")
				if pending.text == "" {
					pending.text = text
				} else {
					pending.text += "\n" + text
				}
			}
		case responsesInputTypeFunctionCall, responsesInputTypeCustomToolCall:
			if callID := responsesCallID(item); callID != "" && pending.present {
				reasoningByCallID[callID] = pending
			}
		case responsesInputTypeFunctionCallOutput, responsesInputTypeCustomToolOutput:
			// Once a tool result starts, a following reasoning item belongs to a
			// later round rather than the call that just completed.
			pending = responsesToolReasoning{}
		}
	}

	for i := range messages {
		if messages[i].Role != "assistant" {
			continue
		}
		for _, toolCall := range messages[i].ParseToolCalls() {
			if reasoning, ok := reasoningByCallID[toolCall.ID]; ok && reasoning.present {
				text := reasoning.text
				messages[i].ReasoningContent = &text
				break
			}
		}
	}
	return nil
}

func responsesReasoningTexts(value any) []string {
	switch typed := value.(type) {
	case nil:
		return nil
	case string:
		return []string{typed}
	case []any:
		texts := make([]string, 0)
		for _, part := range typed {
			texts = append(texts, responsesReasoningTexts(part)...)
		}
		return texts
	case []map[string]any:
		texts := make([]string, 0)
		for _, part := range typed {
			texts = append(texts, responsesReasoningTexts(part)...)
		}
		return texts
	case map[string]any:
		if text, ok := typed["text"].(string); ok {
			return []string{text}
		}
		texts := make([]string, 0)
		for _, key := range []string{"summary_text", "reasoning_text", "content"} {
			if nested, ok := typed[key]; ok {
				texts = append(texts, responsesReasoningTexts(nested)...)
			}
		}
		return texts
	default:
		return nil
	}
}

func responsesRequestMessagesToChat(req *dto.OpenAIResponsesRequest) ([]dto.Message, error) {
	messages := make([]dto.Message, 0)
	if rawJSONPresent(req.Instructions) {
		instructions, err := responsesJSONString(req.Instructions)
		if err != nil {
			return nil, fmt.Errorf("invalid instructions: %w", err)
		}
		if strings.TrimSpace(instructions) != "" {
			messages = append(messages, dto.Message{Role: "system", Content: instructions})
		}
	}

	if !rawJSONPresent(req.Input) {
		return messages, nil
	}

	switch kitutil.GetJsonType(req.Input) {
	case "string":
		input, err := responsesJSONString(req.Input)
		if err != nil {
			return nil, fmt.Errorf("invalid input string: %w", err)
		}
		messages = append(messages, dto.Message{Role: "user", Content: input})
		return messages, nil
	case "array":
		var items []map[string]any
		if err := kitutil.Unmarshal(req.Input, &items); err != nil {
			return nil, fmt.Errorf("invalid input array: %w", err)
		}
		for _, item := range items {
			nextMessages, err := responsesInputItemToChatMessages(item, messages)
			if err != nil {
				return nil, err
			}
			messages = nextMessages
		}
		return messages, nil
	default:
		return nil, fmt.Errorf("unsupported responses input type %q", kitutil.GetJsonType(req.Input))
	}
}

func responsesInputItemToChatMessages(item map[string]any, messages []dto.Message) ([]dto.Message, error) {
	itemType := strings.TrimSpace(kitutil.Interface2String(item["type"]))
	switch itemType {
	case responsesInputTypeReasoning:
		// Reasoning items are output-side metadata carried forward by stateless
		// Responses clients. Chat Completions has no equivalent input message.
		// Treating one as a default user item creates an empty user prompt and
		// breaks tool-result continuations on providers that validate prompts.
		return messages, nil
	case responsesInputTypeFunctionCall:
		toolCall, err := responsesFunctionCallItemToChatToolCall(item)
		if err != nil {
			return nil, err
		}
		return appendToolCallToLastAssistant(messages, toolCall), nil
	case responsesInputTypeCustomToolCall:
		toolCall, err := responsesCustomToolCallItemToChatToolCall(item)
		if err != nil {
			return nil, err
		}
		return appendToolCallToLastAssistant(messages, toolCall), nil
	case responsesInputTypeFunctionCallOutput, responsesInputTypeCustomToolOutput:
		callID := strings.TrimSpace(kitutil.Interface2String(item["call_id"]))
		content := responseToolOutputToChatContent(item["output"])
		return append(messages, dto.Message{Role: "tool", ToolCallId: callID, Content: content}), nil
	}

	role := strings.TrimSpace(kitutil.Interface2String(item["role"]))
	if role == "" {
		role = "user"
	}
	content, err := responsesInputContentToChatContent(item["content"])
	if err != nil {
		return nil, err
	}
	return append(messages, dto.Message{Role: role, Content: content}), nil
}

func responsesInputContentToChatContent(content any) (any, error) {
	if content == nil {
		return "", nil
	}

	switch value := content.(type) {
	case string:
		return value, nil
	case []any:
		return responsesContentPartsToChatContent(value)
	case []map[string]any:
		parts := make([]any, 0, len(value))
		for _, part := range value {
			parts = append(parts, part)
		}
		return responsesContentPartsToChatContent(parts)
	default:
		return content, nil
	}
}

func responsesContentPartsToChatContent(parts []any) (any, error) {
	chatParts := make([]any, 0, len(parts))
	var textOnly strings.Builder
	onlyText := true

	for _, rawPart := range parts {
		part, ok := rawPart.(map[string]any)
		if !ok {
			onlyText = false
			chatParts = append(chatParts, rawPart)
			continue
		}

		partType := strings.TrimSpace(kitutil.Interface2String(part["type"]))
		switch partType {
		case "input_text", "output_text", "text":
			text := kitutil.Interface2String(part["text"])
			textOnly.WriteString(text)
			chatParts = append(chatParts, map[string]any{
				"type": dto.ContentTypeText,
				"text": text,
			})
		case "input_image":
			onlyText = false
			chatParts = append(chatParts, map[string]any{
				"type":      dto.ContentTypeImageURL,
				"image_url": responsesImagePartToChatImageURL(part),
			})
		case "input_file":
			onlyText = false
			chatParts = append(chatParts, map[string]any{
				"type": dto.ContentTypeFile,
				"file": responsesFilePartToChatFile(part),
			})
		case "input_audio":
			onlyText = false
			chatParts = append(chatParts, map[string]any{
				"type":        dto.ContentTypeInputAudio,
				"input_audio": responsesPartPayload(part, "input_audio"),
			})
		case "input_video":
			onlyText = false
			chatParts = append(chatParts, map[string]any{
				"type":      dto.ContentTypeVideoUrl,
				"video_url": responsesVideoPartToChatVideoURL(part),
			})
		default:
			onlyText = false
			chatParts = append(chatParts, part)
		}
	}

	if onlyText {
		return textOnly.String(), nil
	}
	return chatParts, nil
}

func responsesFunctionCallItemToChatToolCall(item map[string]any) (dto.ToolCallRequest, error) {
	name := strings.TrimSpace(kitutil.Interface2String(item["name"]))
	if name == "" {
		return dto.ToolCallRequest{}, errors.New("function_call item is missing name")
	}
	return dto.ToolCallRequest{
		ID:   responsesCallID(item),
		Type: "function",
		Function: dto.FunctionRequest{
			Name:      name,
			Arguments: responsesArgumentsString(item["arguments"]),
		},
	}, nil
}

func responsesCustomToolCallItemToChatToolCall(item map[string]any) (dto.ToolCallRequest, error) {
	arguments, err := kitutil.Marshal(map[string]any{
		"input": item["input"],
	})
	if err != nil {
		return dto.ToolCallRequest{}, err
	}
	return dto.ToolCallRequest{
		ID:   responsesCallID(item),
		Type: "function",
		Function: dto.FunctionRequest{
			Name:      strings.TrimSpace(kitutil.Interface2String(item["name"])),
			Arguments: string(arguments),
		},
	}, nil
}

func appendToolCallToLastAssistant(messages []dto.Message, toolCall dto.ToolCallRequest) []dto.Message {
	if len(messages) == 0 || messages[len(messages)-1].Role != "assistant" {
		messages = append(messages, dto.Message{Role: "assistant"})
	}

	idx := len(messages) - 1
	toolCalls := messages[idx].ParseToolCalls()
	toolCalls = append(toolCalls, toolCall)
	toolCallsRaw, _ := kitutil.Marshal(toolCalls)
	messages[idx].ToolCalls = toolCallsRaw
	return messages
}

func responsesRequestToolsToChat(raw json.RawMessage) ([]dto.ToolCallRequest, error) {
	if !rawJSONPresent(raw) {
		return nil, nil
	}

	var tools []map[string]any
	if err := kitutil.Unmarshal(raw, &tools); err != nil {
		return nil, fmt.Errorf("invalid tools: %w", err)
	}

	out := make([]dto.ToolCallRequest, 0, len(tools))
	for _, tool := range tools {
		toolType := strings.TrimSpace(kitutil.Interface2String(tool["type"]))
		if toolType == "function" {
			out = append(out, dto.ToolCallRequest{
				Type: "function",
				Function: dto.FunctionRequest{
					Name:        strings.TrimSpace(kitutil.Interface2String(tool["name"])),
					Description: kitutil.Interface2String(tool["description"]),
					Parameters:  tool["parameters"],
				},
			})
			continue
		}
		if toolType == dto.CustomType {
			name := strings.TrimSpace(kitutil.Interface2String(tool["name"]))
			if name == "" {
				continue
			}
			description := kitutil.Interface2String(tool["description"])
			if description != "" {
				description += "\n\n"
			}
			description += "Provide the custom tool input verbatim in the input field."
			out = append(out, dto.ToolCallRequest{
				Type: "function",
				Function: dto.FunctionRequest{
					Name:        name,
					Description: description,
					Parameters: map[string]any{
						"type": "object",
						"properties": map[string]any{
							"input": map[string]any{"type": "string"},
						},
						"required":             []string{"input"},
						"additionalProperties": false,
					},
				},
			})
		}
		// Chat Completions has no lossless equivalent for hosted, MCP,
		// namespace, or tool-search tools. Do not forward an illegal type.
	}
	return out, nil
}

func responsesRequestToolChoiceToChat(raw json.RawMessage) (any, error) {
	if !rawJSONPresent(raw) {
		return nil, nil
	}
	if kitutil.GetJsonType(raw) == "string" {
		var choice string
		if err := kitutil.Unmarshal(raw, &choice); err != nil {
			return nil, fmt.Errorf("invalid tool_choice: %w", err)
		}
		return choice, nil
	}

	var choice map[string]any
	if err := kitutil.Unmarshal(raw, &choice); err != nil {
		return nil, fmt.Errorf("invalid tool_choice: %w", err)
	}
	if kitutil.Interface2String(choice["type"]) == "function" {
		name := strings.TrimSpace(kitutil.Interface2String(choice["name"]))
		if name != "" {
			return map[string]any{
				"type": "function",
				"function": map[string]any{
					"name": name,
				},
			}, nil
		}
	}
	return choice, nil
}

func RequestToolChoiceToChat(raw json.RawMessage) (any, error) {
	return responsesRequestToolChoiceToChat(raw)
}

func responsesRequestTextToChatResponseFormat(raw json.RawMessage) (*dto.ResponseFormat, error) {
	if !rawJSONPresent(raw) {
		return nil, nil
	}

	var textConfig map[string]any
	if err := kitutil.Unmarshal(raw, &textConfig); err != nil {
		return nil, fmt.Errorf("invalid text config: %w", err)
	}
	format, ok := textConfig["format"].(map[string]any)
	if !ok {
		return nil, nil
	}

	formatType := strings.TrimSpace(kitutil.Interface2String(format["type"]))
	if formatType == "" {
		return nil, nil
	}

	out := &dto.ResponseFormat{Type: formatType}
	if formatType == "json_schema" {
		schemaRaw, err := kitutil.Marshal(format)
		if err != nil {
			return nil, err
		}
		out.JsonSchema = schemaRaw
	}
	return out, nil
}

func RequestTextToChatResponseFormat(raw json.RawMessage) (*dto.ResponseFormat, error) {
	return responsesRequestTextToChatResponseFormat(raw)
}

func responsesImagePartToChatImageURL(part map[string]any) any {
	if imageURL, ok := part["image_url"]; ok {
		return imageURL
	}
	imageURL := map[string]any{}
	for _, key := range []string{"url", "file_id", "detail"} {
		if value, ok := part[key]; ok {
			imageURL[key] = value
		}
	}
	if len(imageURL) == 0 {
		return part
	}
	return imageURL
}

func responsesFilePartToChatFile(part map[string]any) any {
	if file, ok := part["file"]; ok {
		return file
	}
	file := map[string]any{}
	for _, key := range []string{"file_id", "file_data", "filename", "file_url"} {
		if value, ok := part[key]; ok {
			file[key] = value
		}
	}
	if len(file) == 0 {
		return part
	}
	return file
}

func responsesVideoPartToChatVideoURL(part map[string]any) any {
	if videoURL, ok := part["video_url"]; ok {
		if videoURLMap, ok := videoURL.(map[string]any); ok {
			if url := kitutil.Interface2String(videoURLMap["url"]); url != "" {
				return url
			}
		}
		return videoURL
	}
	if url := kitutil.Interface2String(part["url"]); url != "" {
		return url
	}
	return responsesPartPayload(part, "video_url")
}

func responsesPartPayload(part map[string]any, key string) any {
	if value, ok := part[key]; ok {
		return value
	}
	payload := make(map[string]any, len(part))
	for k, value := range part {
		if k == "type" {
			continue
		}
		payload[k] = value
	}
	return payload
}

func responsesCallID(item map[string]any) string {
	callID := strings.TrimSpace(kitutil.Interface2String(item["call_id"]))
	if callID != "" {
		return callID
	}
	return strings.TrimSpace(kitutil.Interface2String(item["id"]))
}

func CallID(item map[string]any) string {
	return responsesCallID(item)
}

func responsesArgumentsString(value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return v
	default:
		raw, err := kitutil.Marshal(v)
		if err != nil {
			return kitutil.Interface2String(v)
		}
		return string(raw)
	}
}

func responseToolOutputToChatContent(value any) any {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return v
	case []any:
		if content, err := responsesContentPartsToChatContent(v); err == nil {
			return content
		}
	case []map[string]any:
		parts := make([]any, 0, len(v))
		for _, part := range v {
			parts = append(parts, part)
		}
		if content, err := responsesContentPartsToChatContent(parts); err == nil {
			return content
		}
	default:
		raw, err := kitutil.Marshal(v)
		if err != nil {
			return fmt.Sprintf("%v", v)
		}
		return string(raw)
	}

	raw, err := kitutil.Marshal(value)
	if err != nil {
		return fmt.Sprintf("%v", value)
	}
	return string(raw)
}

func responsesRawFloat(raw json.RawMessage) (*float64, error) {
	if !rawJSONPresent(raw) {
		return nil, nil
	}
	var value float64
	if err := kitutil.Unmarshal(raw, &value); err != nil {
		return nil, err
	}
	return &value, nil
}

func responsesJSONString(raw json.RawMessage) (string, error) {
	if kitutil.GetJsonType(raw) != "string" {
		return string(raw), nil
	}
	var value string
	if err := kitutil.Unmarshal(raw, &value); err != nil {
		return "", err
	}
	return value, nil
}

func rawJSONPresent(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	return kitutil.GetJsonType(raw) != "null"
}

func JSONString(raw json.RawMessage) (string, error) {
	return responsesJSONString(raw)
}

func RawJSONPresent(raw json.RawMessage) bool {
	return rawJSONPresent(raw)
}
