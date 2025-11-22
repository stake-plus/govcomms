package haiku45

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/stake-plus/govcomms/src/ai/core"
	"github.com/stake-plus/govcomms/src/webclient"
)

const (
	defaultModel        = "claude-haiku-4-5"
	anthropicEndpoint   = "https://api.anthropic.com/v1/messages"
	defaultMaxTokens    = 8192
	defaultTemperature  = 0.2
	defaultRequestDelay = 240 * time.Second
)

func init() {
	core.RegisterProvider("haiku45", newClient)
}

type client struct {
	apiKey     string
	httpClient *http.Client
	defaults   core.Options
}

func newClient(cfg core.FactoryConfig) (core.Client, error) {
	if cfg.ClaudeKey == "" {
		return nil, fmt.Errorf("haiku-4.5: Claude API key not configured")
	}

	return &client{
		apiKey:     cfg.ClaudeKey,
		httpClient: webclient.NewDefault(defaultRequestDelay),
		defaults: core.Options{
			Model:               valueOrDefault(cfg.Model, defaultModel),
			Temperature:         orFloat(cfg.Temperature, defaultTemperature),
			MaxCompletionTokens: orInt(cfg.MaxCompletionTokens, defaultMaxTokens),
			SystemPrompt:        cfg.SystemPrompt,
			EnableWebSearch:     cfg.Extra["enable_web_search"] == "1",
		},
	}, nil
}

func (c *client) AnswerQuestion(ctx context.Context, content string, question string, opts core.Options) (string, error) {
	merged := c.merge(opts)
	userPrompt := fmt.Sprintf("Proposal Content:\n%s\n\nQuestion: %s\n\nProvide a direct, concise answer grounded only in the provided material unless instructed otherwise.", content, question)
	return c.invoke(ctx, merged, userPrompt, nil)
}

func (c *client) Respond(ctx context.Context, input string, tools []core.Tool, opts core.Options) (string, error) {
	merged := c.merge(opts)
	if shouldEnableWebSearch(merged, tools) {
		input = "If you require newer information, you may use web search or browsing before responding.\n\n" + input
	}
	return c.respondWithTools(ctx, input, tools, merged)
}

func (c *client) invoke(ctx context.Context, opts core.Options, input string, tools []core.Tool) (string, error) {
	maxTokens := opts.MaxCompletionTokens
	if maxTokens <= 0 {
		maxTokens = defaultMaxTokens
	}

	body := map[string]interface{}{
		"model":       opts.Model,
		"system":      opts.SystemPrompt,
		"max_tokens":  maxTokens,
		"temperature": opts.Temperature,
		"messages": []map[string]interface{}{
			{
				"role": "user",
				"content": []map[string]string{
					{"type": "text", "text": input},
				},
			},
		},
	}

	bodyBytes, _ := json.Marshal(body)
	_, responseBody, err := webclient.DoWithRetry(ctx, 3, 2*time.Second, func() (int, []byte, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, anthropicEndpoint, bytes.NewBuffer(bodyBytes))
		if err != nil {
			return 0, nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("x-api-key", c.apiKey)
		req.Header.Set("anthropic-version", "2023-06-01")
		resp, err := c.httpClient.Do(req)
		if err != nil {
			return 0, nil, err
		}
		defer resp.Body.Close()
		b, err := io.ReadAll(resp.Body)
		if err != nil {
			return resp.StatusCode, nil, err
		}
		if resp.StatusCode != http.StatusOK {
			return resp.StatusCode, b, fmt.Errorf("haiku-4.5: status %d", resp.StatusCode)
		}
		return resp.StatusCode, b, nil
	})
	if err != nil {
		return "", err
	}

	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(responseBody, &result); err != nil {
		return "", fmt.Errorf("haiku-4.5: parse error: %w", err)
	}

	text := extractText(result.Content)
	if text == "" {
		return "", fmt.Errorf("haiku-4.5: empty response")
	}
	return text, nil
}

// respondWithTools mirrors the GPT providers' MCP workflow using Anthropic's tool APIs.
func (c *client) respondWithTools(ctx context.Context, input string, tools []core.Tool, opts core.Options) (string, error) {
	maxTokens := opts.MaxCompletionTokens
	if maxTokens <= 0 {
		maxTokens = defaultMaxTokens
	}

	messages := []anthropicMessage{
		{
			Role: "user",
			Content: []anthropicContentBlock{
				{Type: "text", Text: input},
			},
		},
	}

	toolDefs, toolMap, forced := buildAnthropicToolsPayload(tools)
	toolCache := make(map[string]string)
	stallCount := 0
	metadataFetched := false
	contentFetched := false
	attachmentNames := []string{}
	attachmentsRetrieved := map[string]bool{}
	finalReminderSent := false
	base64ReminderSent := false
	toolsDisabled := false

	hasPendingAttachments := func() bool {
		for _, name := range attachmentNames {
			if !attachmentsRetrieved[name] {
				return true
			}
		}
		return false
	}

	nextPendingAttachment := func() string {
		for _, name := range attachmentNames {
			if !attachmentsRetrieved[name] {
				return name
			}
		}
		return ""
	}

	for iteration := 0; iteration < 20; iteration++ {
		reqBody := map[string]any{
			"model":       opts.Model,
			"messages":    messages,
			"max_tokens":  maxTokens,
			"temperature": opts.Temperature,
		}
		if strings.TrimSpace(opts.SystemPrompt) != "" {
			reqBody["system"] = opts.SystemPrompt
		}
		if !toolsDisabled && len(toolDefs) > 0 {
			reqBody["tools"] = toolDefs
			if !(metadataFetched && contentFetched && !hasPendingAttachments()) && strings.TrimSpace(forced) != "" {
				reqBody["tool_choice"] = buildAnthropicToolChoice(forced)
			} else {
				reqBody["tool_choice"] = map[string]any{"type": "auto"}
			}
		}

		bodyBytes, _ := json.Marshal(reqBody)
		_, payload, err := webclient.DoWithRetry(ctx, 3, 2*time.Second, func() (int, []byte, error) {
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, anthropicEndpoint, bytes.NewBuffer(bodyBytes))
			if err != nil {
				return 0, nil, err
			}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("x-api-key", c.apiKey)
			req.Header.Set("anthropic-version", "2023-06-01")
			resp, err := c.httpClient.Do(req)
			if err != nil {
				return 0, nil, err
			}
			defer resp.Body.Close()
			b, err := io.ReadAll(resp.Body)
			if err != nil {
				return resp.StatusCode, nil, err
			}
			if resp.StatusCode != http.StatusOK {
				return resp.StatusCode, b, fmt.Errorf("haiku-4.5: status %d: %s", resp.StatusCode, truncatePayload(b, 512))
			}
			return resp.StatusCode, b, nil
		})
		if err != nil {
			return "", err
		}

		var resp anthropicMessageResponse
		if err := json.Unmarshal(payload, &resp); err != nil {
			return "", fmt.Errorf("haiku-4.5: parse error: %w", err)
		}
		if len(resp.Content) == 0 {
			return "", fmt.Errorf("haiku-4.5: empty response")
		}

		textOutput := anthropicTextFromBlocks(resp.Content)
		toolUses := extractAnthropicToolUses(resp.Content)

		messages = append(messages, anthropicMessage{
			Role:    "assistant",
			Content: resp.Content,
		})

		if len(toolUses) == 0 {
			if strings.TrimSpace(textOutput) == "" {
				continue
			}
			return textOutput, nil
		}

		convertedCalls := convertAnthropicToolUses(toolUses)
		if len(convertedCalls) == 0 {
			continue
		}

		callOutputs := make(map[string]string, len(convertedCalls))
		pendingCalls := make([]openAIToolCall, 0, len(convertedCalls))
		pendingKeys := make(map[string]string, len(convertedCalls))

		for _, call := range convertedCalls {
			key := toolCacheKey(call)
			if val, ok := toolCache[key]; ok {
				callOutputs[call.ID] = val
				continue
			}
			pendingCalls = append(pendingCalls, call)
			pendingKeys[call.ID] = key
		}

		if len(pendingCalls) > 0 {
			outputs, execErr := c.executeToolCalls(ctx, pendingCalls, toolMap)
			if execErr != nil {
				return "", execErr
			}
			for _, out := range outputs {
				callOutputs[out.ToolCallID] = out.Output
				if key := pendingKeys[out.ToolCallID]; key != "" {
					toolCache[key] = out.Output
				}
			}
		}

		pendingCallExecuted := false
		metadataAnnouncedAttachments := false
		toolResultBlocks := make([]anthropicContentBlock, 0, len(convertedCalls))

		for _, call := range convertedCalls {
			content := callOutputs[call.ID]
			toolResultBlocks = append(toolResultBlocks, anthropicContentBlock{
				Type:      "tool_result",
				ToolUseID: call.ID,
				Content:   content,
			})
			if _, ok := pendingKeys[call.ID]; ok {
				pendingCallExecuted = true
			}
			resType := normalizeResource(resourceFromToolCall(call))
			switch resType {
			case "content":
				contentFetched = true
			case "metadata":
				metadataFetched = true
				names := metadataAttachmentNames(content)
				if len(names) > 0 {
					if len(attachmentNames) == 0 {
						attachmentNames = names
					}
					if hasPendingAttachments() {
						metadataAnnouncedAttachments = true
					}
				}
			case "attachments":
				fileArg := attachmentFileFromCall(call)
				if fileArg != "" {
					attachmentsRetrieved[fileArg] = true
				}
			}
		}

		messages = append(messages, anthropicMessage{
			Role:    "user",
			Content: toolResultBlocks,
		})

		if !pendingCallExecuted {
			stallCount++
			if stallCount >= 2 {
				messages = append(messages, anthropicMessage{
					Role: "user",
					Content: []anthropicContentBlock{
						textBlock("You already retrieved the referendum metadata and content. Use the information you have and provide the final answer without calling the tool again."),
					},
				})
				stallCount = 0
			}
		} else {
			stallCount = 0
		}

		if metadataAnnouncedAttachments && hasPendingAttachments() {
			example := ""
			if next := nextPendingAttachment(); next != "" {
				example = fmt.Sprintf(" For example: {\"resource\":\"attachments\",\"file\":\"%s\"}.", next)
			}
			messages = append(messages, anthropicMessage{
				Role: "user",
				Content: []anthropicContentBlock{
					textBlock("Metadata references attachments." + example + " Retrieve each file before answering."),
				},
			})
			continue
		}

		if !hasPendingAttachments() && len(attachmentNames) > 0 && !base64ReminderSent {
			messages = append(messages, anthropicMessage{
				Role: "user",
				Content: []anthropicContentBlock{
					textBlock("Attachment content is provided as base64 text in the tool response. Decode the base64 string to inspect the file before answering."),
				},
			})
			base64ReminderSent = true
		}

		if metadataFetched && contentFetched && !hasPendingAttachments() && !finalReminderSent {
			messages = append(messages, anthropicMessage{
				Role: "user",
				Content: []anthropicContentBlock{
					textBlock("You now have metadata, the full proposal content, and attachments. Provide the final answer without calling the tool again."),
				},
			})
			finalReminderSent = true
			toolsDisabled = true
		}
	}

	return "", fmt.Errorf("haiku-4.5: tool loop exceeded")
}

func (c *client) merge(opts core.Options) core.Options {
	out := c.defaults
	if strings.TrimSpace(opts.Model) != "" {
		out.Model = opts.Model
	}
	if opts.Temperature != 0 {
		out.Temperature = opts.Temperature
	}
	if opts.MaxCompletionTokens != 0 {
		out.MaxCompletionTokens = opts.MaxCompletionTokens
	}
	if strings.TrimSpace(opts.SystemPrompt) != "" {
		out.SystemPrompt = opts.SystemPrompt
	}
	if opts.EnableWebSearch {
		out.EnableWebSearch = true
	}
	if opts.EnableDeepSearch {
		out.EnableDeepSearch = true
	}
	return out
}

func shouldEnableWebSearch(opts core.Options, tools []core.Tool) bool {
	if opts.EnableWebSearch {
		return true
	}
	for _, tool := range tools {
		if strings.EqualFold(tool.Type, "web_search") {
			return true
		}
	}
	return false
}

func extractText(chunks []struct {
	Type string `json:"type"`
	Text string `json:"text"`
}) string {
	var builder strings.Builder
	for _, chunk := range chunks {
		if strings.TrimSpace(chunk.Text) == "" {
			continue
		}
		if builder.Len() > 0 {
			builder.WriteString("\n")
		}
		builder.WriteString(chunk.Text)
	}
	return strings.TrimSpace(builder.String())
}

func valueOrDefault(val, def string) string {
	if strings.TrimSpace(val) != "" {
		return val
	}
	return def
}

func orInt(v, def int) int {
	if v > 0 {
		return v
	}
	return def
}

func orFloat(v, def float64) float64 {
	if v != 0 {
		return v
	}
	return def
}

func buildAnthropicToolsPayload(tools []core.Tool) ([]map[string]any, map[string]core.Tool, string) {
	out := []map[string]any{}
	toolMap := map[string]core.Tool{}
	var forced string
	for idx, t := range tools {
		if strings.ToLower(t.Type) != "mcp_referenda" {
			continue
		}
		name := t.Name
		if strings.TrimSpace(name) == "" {
			name = fmt.Sprintf("mcp_referendum_%d", idx+1)
		}
		definition := map[string]any{
			"name":        name,
			"description": t.Description,
		}
		if t.Parameters != nil {
			definition["input_schema"] = t.Parameters
		}
		out = append(out, definition)
		toolCopy := t
		toolCopy.Name = name
		toolMap[name] = toolCopy
		forced = name
	}
	return out, toolMap, forced
}

func buildAnthropicToolChoice(forced string) map[string]any {
	if strings.TrimSpace(forced) == "" {
		return map[string]any{"type": "auto"}
	}
	return map[string]any{
		"type": "tool",
		"name": forced,
	}
}

func anthropicTextFromBlocks(blocks []anthropicContentBlock) string {
	var builder strings.Builder
	for _, block := range blocks {
		if block.Type != "text" {
			continue
		}
		if strings.TrimSpace(block.Text) == "" {
			continue
		}
		if builder.Len() > 0 {
			builder.WriteString("\n")
		}
		builder.WriteString(block.Text)
	}
	return strings.TrimSpace(builder.String())
}

func extractAnthropicToolUses(blocks []anthropicContentBlock) []anthropicContentBlock {
	out := []anthropicContentBlock{}
	for _, block := range blocks {
		if block.Type == "tool_use" {
			out = append(out, block)
		}
	}
	return out
}

func convertAnthropicToolUses(blocks []anthropicContentBlock) []openAIToolCall {
	out := make([]openAIToolCall, 0, len(blocks))
	for _, block := range blocks {
		args := "null"
		if block.Input != nil {
			data, err := json.Marshal(block.Input)
			if err == nil {
				args = string(data)
			}
		} else {
			args = "{}"
		}
		out = append(out, openAIToolCall{
			ID:   block.ID,
			Type: "function",
			Function: struct {
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			}{
				Name:      block.Name,
				Arguments: args,
			},
		})
	}
	return out
}

func textBlock(msg string) anthropicContentBlock {
	return anthropicContentBlock{
		Type: "text",
		Text: msg,
	}
}

type anthropicMessage struct {
	Role    string                  `json:"role"`
	Content []anthropicContentBlock `json:"content"`
}

type anthropicContentBlock struct {
	Type      string         `json:"type"`
	Text      string         `json:"text,omitempty"`
	ID        string         `json:"id,omitempty"`
	Name      string         `json:"name,omitempty"`
	Input     map[string]any `json:"input,omitempty"`
	ToolUseID string         `json:"tool_use_id,omitempty"`
	Content   any            `json:"content,omitempty"`
	IsError   bool           `json:"is_error,omitempty"`
}

type anthropicMessageResponse struct {
	Content []anthropicContentBlock `json:"content"`
}

type openAIToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type toolOutput struct {
	ToolCallID string `json:"tool_call_id"`
	Output     string `json:"output"`
}

func (c *client) executeToolCalls(ctx context.Context, calls []openAIToolCall, toolMap map[string]core.Tool) ([]toolOutput, error) {
	outputs := make([]toolOutput, 0, len(calls))
	for _, call := range calls {
		toolDef, ok := toolMap[call.Function.Name]
		if !ok {
			return nil, fmt.Errorf("haiku-4.5: unknown tool %s", call.Function.Name)
		}
		args, err := decodeToolArguments(call.Function.Arguments)
		if err != nil {
			log.Printf("haiku45: tool %s arg parse error: %v", call.Function.Name, err)
			outputs = append(outputs, toolOutput{
				ToolCallID: call.ID,
				Output:     fmt.Sprintf(`{"error":"invalid arguments: %s"}`, sanitizeToolError(err)),
			})
			continue
		}
		rawArgs := copyArgs(args)
		args = mergeArgs(args, toolDef.Defaults)
		log.Printf("haiku45: tool call %s raw=%v merged=%v", call.Function.Name, rawArgs, args)
		result, execErr := c.dispatchTool(ctx, toolDef, args)
		if execErr != nil {
			log.Printf("haiku45: tool %s error: %v", call.Function.Name, execErr)
			result = fmt.Sprintf(`{"error":"%s"}`, sanitizeToolError(execErr))
		}
		log.Printf("haiku45: tool %s output=%s", call.Function.Name, truncatePayload([]byte(result), 256))
		outputs = append(outputs, toolOutput{
			ToolCallID: call.ID,
			Output:     result,
		})
	}
	return outputs, nil
}

func (c *client) dispatchTool(ctx context.Context, toolDef core.Tool, args map[string]any) (string, error) {
	switch strings.ToLower(toolDef.Type) {
	case "mcp_referenda":
		return c.invokeMCP(ctx, toolDef.MCP, args)
	default:
		return "", fmt.Errorf("haiku-4.5: unsupported tool %s", toolDef.Type)
	}
}

func (c *client) invokeMCP(ctx context.Context, desc *core.MCPDescriptor, args map[string]any) (string, error) {
	if desc == nil || strings.TrimSpace(desc.BaseURL) == "" {
		return "", fmt.Errorf("mcp descriptor missing")
	}
	network := strings.TrimSpace(fmt.Sprint(args["network"]))
	if network == "" {
		return "", fmt.Errorf("network argument required")
	}
	refIDRaw, ok := args["refId"]
	if !ok {
		return "", fmt.Errorf("refId argument required")
	}
	refID, err := parseUint(refIDRaw)
	if err != nil {
		return "", fmt.Errorf("invalid refId: %w", err)
	}
	resource := strings.TrimSpace(fmt.Sprint(args["resource"]))
	resource = strings.ToLower(resource)
	fileParam := strings.TrimSpace(fmt.Sprint(args["file"]))
	base := strings.TrimRight(desc.BaseURL, "/")
	endpoint := fmt.Sprintf("%s/v1/referenda/%s/%d", base, url.PathEscape(network), refID)
	if resource != "" && resource != "metadata" {
		endpoint += "/" + url.PathEscape(resource)
	}
	if resource == "attachments" && fileParam != "" {
		query := url.Values{}
		query.Set("file", fileParam)
		endpoint += "?" + query.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/json")
	if desc.AuthToken != "" {
		req.Header.Set("Authorization", "Bearer "+desc.AuthToken)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("mcp: status %d: %s", resp.StatusCode, string(body))
	}
	return string(body), nil
}

func parseUint(value any) (uint64, error) {
	switch v := value.(type) {
	case float64:
		return uint64(v), nil
	case uint64:
		return v, nil
	case uint32:
		return uint64(v), nil
	case uint:
		return uint64(v), nil
	case int:
		return uint64(v), nil
	case int64:
		return uint64(v), nil
	case string:
		return strconv.ParseUint(v, 10, 64)
	default:
		return 0, fmt.Errorf("unsupported numeric type %T", value)
	}
}

func truncatePayload(b []byte, limit int) string {
	if len(b) <= limit {
		return string(b)
	}
	return string(b[:limit]) + "... (truncated)"
}

func sanitizeToolError(err error) string {
	msg := strings.TrimSpace(err.Error())
	if len(msg) > 200 {
		msg = msg[:200]
	}
	return msg
}

func decodeToolArguments(raw string) (map[string]any, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || strings.EqualFold(trimmed, "null") {
		return map[string]any{}, nil
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(trimmed), &args); err != nil {
		return nil, err
	}
	return args, nil
}

func mergeArgs(args map[string]any, defaults map[string]any) map[string]any {
	if args == nil {
		args = map[string]any{}
	}
	for k, v := range defaults {
		if _, exists := args[k]; !exists {
			args[k] = v
		}
	}
	return args
}

func copyArgs(src map[string]any) map[string]any {
	if src == nil {
		return nil
	}
	dst := make(map[string]any, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func toolCacheKey(call openAIToolCall) string {
	name := strings.ToLower(strings.TrimSpace(call.Function.Name))
	args := strings.TrimSpace(call.Function.Arguments)
	return name + "::" + args
}

func resourceFromToolCall(call openAIToolCall) string {
	args := strings.TrimSpace(call.Function.Arguments)
	if args == "" {
		return "metadata"
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(args), &obj); err != nil {
		return "metadata"
	}
	res := strings.TrimSpace(strings.ToLower(fmt.Sprint(obj["resource"])))
	if res == "" {
		return "metadata"
	}
	return res
}

func normalizeResource(res string) string {
	r := strings.TrimSpace(strings.ToLower(res))
	if r == "" {
		return "metadata"
	}
	if strings.HasPrefix(r, "attachment") {
		return "attachments"
	}
	return r
}

func metadataAttachmentNames(content string) []string {
	var payload struct {
		Attachments []struct {
			File string `json:"file"`
		} `json:"attachments"`
	}
	if err := json.Unmarshal([]byte(content), &payload); err != nil {
		return nil
	}
	names := make([]string, 0, len(payload.Attachments))
	for _, att := range payload.Attachments {
		file := strings.TrimSpace(att.File)
		if file == "" {
			continue
		}
		if !strings.HasPrefix(strings.ToLower(file), "files/") {
			continue
		}
		names = append(names, file)
	}
	return names
}

func attachmentFileFromCall(call openAIToolCall) string {
	args := strings.TrimSpace(call.Function.Arguments)
	if args == "" {
		return ""
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(args), &obj); err != nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(obj["file"]))
}
