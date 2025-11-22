package grok

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/stake-plus/govcomms/src/ai/core"
	"github.com/stake-plus/govcomms/src/webclient"
)

const (
	providerKey             = "grok4"
	apiURL                  = "https://api.x.ai/v1/chat/completions"
	defaultModel            = "grok-4-fast-reasoning"
	defaultMaxTokens        = 8192
	defaultTemperature      = 1.0
	defaultTopP             = 0.9
	defaultFrequencyPenalty = 0.0
	defaultRequestTimeout   = 240 * time.Second
	defaultRetryAttempts    = 3
	defaultRetryBackoff     = 2 * time.Second
	maxToolIterations       = 20
	minTopP                 = 0.01
	maxTopP                 = 1.0
	penaltyMin              = -2.0
	penaltyMax              = 2.0
)

const (
	extraTopPKey             = "grok.top_p"
	extraFrequencyPenaltyKey = "grok.frequency_penalty"
)

func init() {
	core.RegisterProvider(providerKey, newClient)
}

type client struct {
	apiKey           string
	httpClient       *http.Client
	defaults         core.Options
	topP             float64
	frequencyPenalty float64
}

func newClient(cfg core.FactoryConfig) (core.Client, error) {
	if cfg.GrokKey == "" {
		return nil, fmt.Errorf("grok: API key not configured")
	}

	topP := core.ClampFloat(core.ExtraFloat(cfg.Extra, extraTopPKey, defaultTopP), minTopP, maxTopP)
	frequencyPenalty := core.ClampFloat(core.ExtraFloat(cfg.Extra, extraFrequencyPenaltyKey, defaultFrequencyPenalty), penaltyMin, penaltyMax)

	return &client{
		apiKey:     cfg.GrokKey,
		httpClient: webclient.NewDefault(defaultRequestTimeout),
		defaults: core.Options{
			Model:               valueOrDefault(cfg.Model, defaultModel),
			Temperature:         orFloat(cfg.Temperature, defaultTemperature),
			MaxCompletionTokens: orInt(cfg.MaxCompletionTokens, defaultMaxTokens),
			SystemPrompt:        cfg.SystemPrompt,
		},
		topP:             topP,
		frequencyPenalty: frequencyPenalty,
	}, nil
}

func (c *client) AnswerQuestion(ctx context.Context, content string, question string, opts core.Options) (string, error) {
	merged := c.merge(opts)
	userPrompt := fmt.Sprintf("Proposal Content:\n%s\n\nQuestion: %s\n\nProvide a concise answer. Use the Grok internet tool if you need real-time information.", content, question)
	body := c.buildRequest(merged, userPrompt, false)
	return c.send(ctx, body)
}

func (c *client) Respond(ctx context.Context, input string, tools []core.Tool, opts core.Options) (string, error) {
	merged := c.merge(opts)
	return c.respondWithChatTools(ctx, input, tools, merged)
}

func (c *client) buildRequest(opts core.Options, userPrompt string, enableWeb bool) map[string]interface{} {
	messages := []map[string]string{}
	if strings.TrimSpace(opts.SystemPrompt) != "" {
		messages = append(messages, map[string]string{
			"role":    "system",
			"content": opts.SystemPrompt,
		})
	}
	messages = append(messages, map[string]string{
		"role":    "user",
		"content": userPrompt,
	})

	body := map[string]interface{}{
		"model":             opts.Model,
		"messages":          messages,
		"temperature":       opts.Temperature,
		"max_output_tokens": maxTokens(opts.MaxCompletionTokens),
		"stream":            false,
		"n":                 1,
		"top_p":             c.topP,
		"frequency_penalty": c.frequencyPenalty,
	}

	if enableWeb {
		body["tools"] = []map[string]string{
			{"type": "internet"},
		}
		body["tool_choice"] = "auto"
	}

	return body
}

func (c *client) send(ctx context.Context, payload map[string]interface{}) (string, error) {
	bodyBytes, _ := json.Marshal(payload)
	_, body, err := webclient.DoWithRetry(ctx, defaultRetryAttempts, defaultRetryBackoff, func() (int, []byte, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewBuffer(bodyBytes))
		if err != nil {
			return 0, nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
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
			return resp.StatusCode, b, fmt.Errorf("status %d: %s", resp.StatusCode, truncateErrorBody(b))
		}
		return resp.StatusCode, b, nil
	})
	if err != nil {
		return "", fmt.Errorf("grok API error: %w", err)
	}

	var result completionResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return "", err
	}
	text := result.FirstMessage()
	if text == "" {
		return "", fmt.Errorf("grok: empty response")
	}
	return text, nil
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
	return out
}

func maxTokens(requested int) int {
	if requested <= 0 {
		return defaultMaxTokens
	}
	return requested
}

func truncateErrorBody(body []byte) string {
	text := strings.TrimSpace(string(body))
	if text == "" {
		return "no response body"
	}
	const limit = 300
	if len(text) > limit {
		return text[:limit] + "..."
	}
	return text
}

type completionResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

func (r completionResponse) FirstMessage() string {
	for _, choice := range r.Choices {
		content := strings.TrimSpace(choice.Message.Content)
		if content != "" {
			return content
		}
	}
	return ""
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

func (c *client) executeToolCalls(ctx context.Context, calls []openAIToolCall, toolMap map[string]core.Tool) ([]toolOutput, error) {
	outputs := make([]toolOutput, 0, len(calls))
	for _, call := range calls {
		toolDef, ok := toolMap[call.Function.Name]
		if !ok {
			return nil, fmt.Errorf("grok4: unknown tool %s", call.Function.Name)
		}
		args, err := decodeToolArguments(call.Function.Arguments)
		if err != nil {
			log.Printf("grok4: tool %s arg parse error: %v", call.Function.Name, err)
			outputs = append(outputs, toolOutput{
				ToolCallID: call.ID,
				Output:     fmt.Sprintf(`{"error":"invalid arguments: %s"}`, sanitizeToolError(err)),
			})
			continue
		}
		origArgs := copyArgs(args)
		args = normalizeToolArguments(args)
		normArgs := copyArgs(args)
		args = mergeArgs(args, toolDef.Defaults)
		log.Printf("grok4: tool call %s raw=%v normalized=%v merged=%v", call.Function.Name, origArgs, normArgs, args)
		result, execErr := c.dispatchTool(ctx, toolDef, args)
		if execErr != nil {
			log.Printf("grok4: tool %s error: %v", call.Function.Name, execErr)
			result = fmt.Sprintf(`{"error":"%s"}`, sanitizeToolError(execErr))
		}
		log.Printf("grok4: tool %s output=%s", call.Function.Name, truncatePayload([]byte(result), 256))
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
		return "", fmt.Errorf("unsupported tool %s", toolDef.Type)
	}
}

func (c *client) invokeMCP(ctx context.Context, desc *core.MCPDescriptor, args map[string]any) (string, error) {
	if desc == nil || strings.TrimSpace(desc.BaseURL) == "" {
		return "", fmt.Errorf("mcp descriptor missing")
	}
	network := normalizedArgValue(args["network"])
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
	resource := strings.ToLower(normalizedArgValue(args["resource"]))
	fileParam := normalizedArgValue(args["file"])
	if resource == "attachments" && fileParam == "" {
		return "", fmt.Errorf("file argument required for attachments")
	}
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

func decodeToolArgumentsNormalized(raw string) (map[string]any, error) {
	args, err := decodeToolArguments(raw)
	if err != nil {
		return nil, err
	}
	return normalizeToolArguments(args), nil
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

func (c *client) respondWithChatTools(ctx context.Context, input string, tools []core.Tool, opts core.Options) (string, error) {
	messages := make([]chatMessagePayload, 0, 4)
	if strings.TrimSpace(opts.SystemPrompt) != "" {
		messages = append(messages, chatMessagePayload{Role: "system", Content: opts.SystemPrompt})
	}
	messages = append(messages, chatMessagePayload{Role: "user", Content: input})

	toolDefs, toolMap, forced := buildChatToolsPayload(tools, opts.EnableWebSearch)
	toolCache := make(map[string]string)
	stallCount := 0
	metadataFetched := false
	contentFetched := false
	historyFetched := false
	attachmentNames := []string{}
	attachmentsRetrieved := map[string]bool{}
	missingAttachmentFileCount := 0
	finalReminderSent := false
	base64ReminderSent := false
	historyReminderSent := false
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

	for iteration := 0; iteration < maxToolIterations; iteration++ {
		reqBody := map[string]any{
			"model":             opts.Model,
			"messages":          messages,
			"temperature":       opts.Temperature,
			"stream":            false,
			"n":                 1,
			"top_p":             c.topP,
			"frequency_penalty": c.frequencyPenalty,
		}
		if opts.MaxCompletionTokens > 0 {
			reqBody["max_output_tokens"] = opts.MaxCompletionTokens
		}

		if !toolsDisabled && len(toolDefs) > 0 {
			reqBody["tools"] = toolDefs
			if !(metadataFetched && contentFetched && historyFetched && !hasPendingAttachments()) && strings.TrimSpace(forced) != "" {
				reqBody["tool_choice"] = buildChatToolChoice(forced)
			} else {
				reqBody["tool_choice"] = "auto"
			}
		}

		bodyBytes, _ := json.Marshal(reqBody)
		_, body, err := webclient.DoWithRetry(ctx, defaultRetryAttempts, defaultRetryBackoff, func() (int, []byte, error) {
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewBuffer(bodyBytes))
			if err != nil {
				return 0, nil, err
			}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+c.apiKey)
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
				return resp.StatusCode, b, fmt.Errorf("status %d: %s", resp.StatusCode, truncatePayload(b, 512))
			}
			return resp.StatusCode, b, nil
		})
		if err != nil {
			return "", fmt.Errorf("grok4 chat error: %w", err)
		}

		var resp chatCompletionResponse
		if err := json.Unmarshal(body, &resp); err != nil {
			return "", err
		}
		if len(resp.Choices) == 0 {
			return "", fmt.Errorf("grok4: chat completion returned no choices")
		}
		msg := resp.Choices[0].Message

		assistantPayload := chatMessagePayload{
			Role:    "assistant",
			Content: msg.Content,
		}
		if len(msg.ToolCalls) > 0 {
			assistantPayload.ToolCalls = msg.ToolCalls
		}
		messages = append(messages, assistantPayload)

		if len(msg.ToolCalls) == 0 {
			if strings.TrimSpace(msg.Content) == "" {
				continue
			}
			return msg.Content, nil
		}

		convertedCalls := convertChatToolCalls(msg.ToolCalls)
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
			outputs, err := c.executeToolCalls(ctx, pendingCalls, toolMap)
			if err != nil {
				return "", err
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
		missingAttachmentFile := false
		for _, call := range convertedCalls {
			resType := normalizeResource(resourceFromToolCall(call))
			content := callOutputs[call.ID]
			messages = append(messages, chatMessagePayload{
				Role:       "tool",
				ToolCallID: call.ID,
				Name:       call.Function.Name,
				Content:    content,
			})
			if _, ok := pendingKeys[call.ID]; ok {
				pendingCallExecuted = true
			}
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
				} else {
					missingAttachmentFile = true
				}
			case "history":
				historyFetched = true
			}
		}

		if missingAttachmentFile {
			missingAttachmentFileCount++
			example := ""
			if next := nextPendingAttachment(); next != "" {
				example = fmt.Sprintf(" Example: {\"resource\":\"attachments\",\"file\":\"%s\"}.", next)
			}
			messages = append(messages, chatMessagePayload{
				Role: "user",
				Content: "When calling fetch_referendum_data for attachments you must include the \"file\" argument set to one of the attachment paths from metadata." +
					example + " Retry the attachment request with the proper file.",
			})
			if missingAttachmentFileCount >= 3 {
				if len(attachmentNames) > 0 {
					for _, name := range attachmentNames {
						attachmentsRetrieved[name] = true
					}
				}
				attachmentNames = nil
				messages = append(messages, chatMessagePayload{
					Role:    "user",
					Content: "You have attempted to fetch attachments without a file parameter multiple times. Proceed using the metadata and content you already retrieved and provide the final answer without calling attachments again.",
				})
				toolsDisabled = true
				finalReminderSent = true
			}
			continue
		}

		if !pendingCallExecuted {
			stallCount++
			if stallCount >= 2 {
				messages = append(messages, chatMessagePayload{
					Role:    "user",
					Content: "You already retrieved the required referendum context. Use the information you have and provide the final answer without calling the tool again.",
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
			messages = append(messages, chatMessagePayload{
				Role:    "user",
				Content: "Metadata references attachments." + example + " Retrieve each file before answering.",
			})
			continue
		}

		if !hasPendingAttachments() && len(attachmentNames) > 0 && !base64ReminderSent {
			messages = append(messages, chatMessagePayload{
				Role:    "user",
				Content: "Attachment content is provided as base64 text in the tool response. Decode the base64 string to inspect the file before answering.",
			})
			base64ReminderSent = true
		}

		if metadataFetched && contentFetched && !historyFetched && !historyReminderSent {
			messages = append(messages, chatMessagePayload{
				Role:    "user",
				Content: "Retrieve the previous Q&A history by calling the tool with {\"resource\":\"history\"} before answering.",
			})
			historyReminderSent = true
			continue
		}

		if metadataFetched && contentFetched && historyFetched && !hasPendingAttachments() && !finalReminderSent {
			messages = append(messages, chatMessagePayload{
				Role:    "user",
				Content: "You now have metadata, the full proposal content, prior Q&A history, and any attachments you needed. Provide the final answer without calling the tool again.",
			})
			finalReminderSent = true
			toolsDisabled = true
		}
	}

	return "", fmt.Errorf("grok4: chat tool loop exceeded")
}

func buildChatToolsPayload(tools []core.Tool, enableWeb bool) ([]map[string]any, map[string]core.Tool, string) {
	out := []map[string]any{}
	toolMap := map[string]core.Tool{}
	var forced string
	webAdded := false

	addInternet := func() {
		if webAdded {
			return
		}
		out = append(out, map[string]any{
			"type": "live_search",
			"sources": []map[string]any{
				{"type": "web"},
			},
		})
		webAdded = true
	}

	if enableWeb {
		addInternet()
	}

	for idx, t := range tools {
		switch strings.ToLower(t.Type) {
		case "web_search":
			addInternet()
		case "mcp_referenda":
			name := t.Name
			if strings.TrimSpace(name) == "" {
				name = fmt.Sprintf("mcp_referendum_%d", idx+1)
			}
			funcDef := map[string]any{
				"name":        name,
				"description": t.Description,
			}
			if t.Parameters != nil {
				funcDef["parameters"] = t.Parameters
			}
			out = append(out, map[string]any{
				"type":     "function",
				"function": funcDef,
			})
			toolCopy := t
			toolCopy.Name = name
			toolMap[name] = toolCopy
			forced = name
		}
	}
	return out, toolMap, forced
}

func buildChatToolChoice(forced string) any {
	if strings.TrimSpace(forced) == "" {
		return "auto"
	}
	return map[string]any{
		"type": "function",
		"function": map[string]any{
			"name": forced,
		},
	}
}

type chatCompletionResponse struct {
	Choices []struct {
		Message chatMessage `json:"message"`
	} `json:"choices"`
}

type chatMessage struct {
	Role      string         `json:"role"`
	Content   string         `json:"content"`
	ToolCalls []chatToolCall `json:"tool_calls"`
}

type chatToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type chatMessagePayload struct {
	Role       string         `json:"role"`
	Content    string         `json:"content,omitempty"`
	ToolCalls  []chatToolCall `json:"tool_calls,omitempty"`
	Name       string         `json:"name,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
}

func convertChatToolCalls(calls []chatToolCall) []openAIToolCall {
	out := make([]openAIToolCall, len(calls))
	for i, call := range calls {
		out[i] = openAIToolCall{
			ID:   call.ID,
			Type: call.Type,
			Function: struct {
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			}{
				Name:      call.Function.Name,
				Arguments: call.Function.Arguments,
			},
		}
	}
	return out
}

func toolCacheKey(call openAIToolCall) string {
	name := strings.ToLower(strings.TrimSpace(call.Function.Name))
	args := strings.TrimSpace(call.Function.Arguments)
	return name + "::" + args
}

func resourceFromToolCall(call openAIToolCall) string {
	args, err := decodeToolArgumentsNormalized(call.Function.Arguments)
	if err != nil || len(args) == 0 {
		return "metadata"
	}
	res := strings.TrimSpace(strings.ToLower(fmt.Sprint(args["resource"])))
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
	args, err := decodeToolArgumentsNormalized(call.Function.Arguments)
	if err != nil || len(args) == 0 {
		return ""
	}
	return normalizedArgValue(args["file"])
}

var weirdParamPattern = regexp.MustCompile(`parameter name="([^"]+)">([^<]+)`)

func normalizeToolArguments(args map[string]any) map[string]any {
	if len(args) == 0 {
		return args
	}
	for key, val := range args {
		strVal, ok := val.(string)
		if !ok {
			args[key] = normalizedArgValue(val)
			continue
		}
		if !strings.Contains(strVal, "parameter name=") {
			clean := normalizedString(strVal)
			if clean == "" {
				delete(args, key)
			} else {
				args[key] = clean
			}
			continue
		}
		base := normalizedString(strBefore(strVal, "<"))
		if base != "" {
			args[key] = base
		} else {
			delete(args, key)
		}
		extracted := extractWeirdParameters(strVal)
		for name, value := range extracted {
			if strings.TrimSpace(name) == "" {
				continue
			}
			if value == "" {
				delete(args, name)
				continue
			}
			args[name] = value
		}
	}
	return args
}

func extractWeirdParameters(input string) map[string]string {
	matches := weirdParamPattern.FindAllStringSubmatch(input, -1)
	if len(matches) == 0 {
		return nil
	}
	out := make(map[string]string, len(matches))
	for _, match := range matches {
		if len(match) < 3 {
			continue
		}
		name := strings.TrimSpace(match[1])
		value := normalizedString(html.UnescapeString(match[2]))
		if name == "" {
			continue
		}
		out[name] = value
	}
	return out
}

func normalizedString(value string) string {
	trimmed := strings.TrimSpace(value)
	lowered := strings.ToLower(trimmed)
	if trimmed == "" || trimmed == "<nil>" || lowered == "null" || lowered == "nil" {
		return ""
	}
	return trimmed
}

func normalizedArgValue(val any) string {
	if val == nil {
		return ""
	}
	switch v := val.(type) {
	case string:
		return normalizedString(v)
	default:
		return normalizedString(fmt.Sprint(v))
	}
}

func strBefore(input, sep string) string {
	if idx := strings.Index(input, sep); idx >= 0 {
		return input[:idx]
	}
	return input
}
