package gemini

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
	baseURL          = "https://generativelanguage.googleapis.com/v1beta"
	defaultModelName = "gemini-2.5-flash"
	defaultMaxTokens = 16000
)

func init() {
	core.RegisterProvider("gemini25", newClient)
}

type client struct {
	apiKey     string
	httpClient *http.Client
	defaults   core.Options
}

func newClient(cfg core.FactoryConfig) (core.Client, error) {
	if cfg.GeminiKey == "" {
		return nil, fmt.Errorf("gemini: API key not configured")
	}

	model := cfg.Model
	if strings.TrimSpace(model) == "" {
		model = defaultModelName
	}

	return &client{
		apiKey:     cfg.GeminiKey,
		httpClient: webclient.NewDefault(240 * time.Second),
		defaults: core.Options{
			Model:               model,
			Temperature:         orFloat(cfg.Temperature, 0.2),
			MaxCompletionTokens: orInt(cfg.MaxCompletionTokens, defaultMaxTokens),
			SystemPrompt:        cfg.SystemPrompt,
		},
	}, nil
}

func (c *client) AnswerQuestion(ctx context.Context, content string, question string, opts core.Options) (string, error) {
	merged := c.merge(opts)
	userText := fmt.Sprintf("Proposal Content:\n%s\n\nQuestion: %s\n\nProvide a direct, concise answer grounded in the provided context. Use web search only if the answer requires up-to-date information.", content, question)
	body := c.buildRequestBody(merged, userText, false)
	return c.send(ctx, merged.Model, body)
}

func (c *client) Respond(ctx context.Context, input string, tools []core.Tool, opts core.Options) (string, error) {
	merged := c.merge(opts)
	return c.respondWithChatTools(ctx, input, tools, merged)
}

func (c *client) respondWithChatTools(ctx context.Context, input string, tools []core.Tool, opts core.Options) (string, error) {
	contents := make([]geminiContent, 0, 4)
	contents = append(contents, geminiContent{
		Role: "user",
		Parts: []geminiPart{
			{Text: input},
		},
	})

	enableSearch := hasWebSearch(opts, tools)
	toolDefs, toolMap, functionNames := buildGeminiToolsPayload(tools, enableSearch)

	var systemInstruction map[string]any
	if strings.TrimSpace(opts.SystemPrompt) != "" {
		systemInstruction = map[string]any{
			"parts": []map[string]string{
				{"text": opts.SystemPrompt},
			},
		}
	}

	generationConfig := map[string]any{
		"temperature":     opts.Temperature,
		"maxOutputTokens": maxTokens(opts.MaxCompletionTokens),
	}

	toolCache := make(map[string]string)
	stallCount := 0
	metadataFetched := false
	contentFetched := false
	attachmentNames := []string{}
	attachmentsRetrieved := map[string]bool{}
	finalReminderSent := false
	base64ReminderSent := false
	toolsDisabled := false
	forceFunctions := true
	callSeq := 0
	metadataHintSent := false
	contentHintSent := false

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
		body := map[string]any{
			"contents":          contents,
			"generationConfig":  generationConfig,
			"tools":             []map[string]any{},
			"toolConfig":        map[string]any{},
			"systemInstruction": systemInstruction,
		}

		if systemInstruction == nil {
			delete(body, "systemInstruction")
		}

		if !toolsDisabled && len(toolDefs) > 0 {
			body["tools"] = toolDefs
			if len(functionNames) > 0 {
				mode := "AUTO"
				if forceFunctions {
					mode = "ANY"
				}
				fConfig := map[string]any{
					"mode": mode,
				}
				if strings.EqualFold(mode, "ANY") {
					fConfig["allowedFunctionNames"] = functionNames
				}
				body["toolConfig"] = map[string]any{
					"functionCallingConfig": fConfig,
				}
			} else {
				delete(body, "toolConfig")
			}
		} else {
			delete(body, "tools")
			delete(body, "toolConfig")
		}

		raw, err := c.callGenerateContent(ctx, opts.Model, body)
		if err != nil {
			return "", fmt.Errorf("gemini chat error: %w", err)
		}

		var resp generateContentResponse
		if err := json.Unmarshal(raw, &resp); err != nil {
			return "", err
		}
		if len(resp.Candidates) == 0 {
			return "", fmt.Errorf("gemini: generateContent returned no candidates")
		}

		modelContent := resp.Candidates[0].Content
		contents = append(contents, modelContent)

		calls := geminiFunctionCallsFromContent(modelContent)
		if len(calls) == 0 {
			text := strings.TrimSpace(modelContent.FirstText())
			if text == "" {
				continue
			}
			return text, nil
		}

		convertedCalls := convertGeminiToolCalls(calls, &callSeq)
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
		for _, call := range convertedCalls {
			content := callOutputs[call.ID]
			contents = append(contents, geminiToolResponseContent(call.Function.Name, content))
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

		if !pendingCallExecuted {
			stallCount++
			if stallCount >= 2 {
				contents = append(contents, geminiContent{
					Role: "user",
					Parts: []geminiPart{
						{Text: "You already retrieved the referendum metadata and content. Use the information you have and provide the final answer without calling the tool again."},
					},
				})
				stallCount = 0
				forceFunctions = false
			}
		} else {
			stallCount = 0
		}

		if metadataAnnouncedAttachments && hasPendingAttachments() {
			example := ""
			if next := nextPendingAttachment(); next != "" {
				example = fmt.Sprintf(" For example: {\"resource\":\"attachments\",\"file\":\"%s\"}.", next)
			}
			contents = append(contents, geminiContent{
				Role: "user",
				Parts: []geminiPart{
					{Text: "Metadata references attachments." + example + " Retrieve each file before answering."},
				},
			})
			continue
		}

		if !hasPendingAttachments() && len(attachmentNames) > 0 && !base64ReminderSent {
			contents = append(contents, geminiContent{
				Role: "user",
				Parts: []geminiPart{
					{Text: "Attachment content is provided as base64 text in the tool response. Decode the base64 string to inspect the file before answering."},
				},
			})
			base64ReminderSent = true
		}

		if metadataFetched && contentFetched && !hasPendingAttachments() && !finalReminderSent {
			contents = append(contents, geminiContent{
				Role: "user",
				Parts: []geminiPart{
					{Text: "You now have metadata, the full proposal content, and attachments. Provide the final answer without calling the tool again."},
				},
			})
			finalReminderSent = true
			toolsDisabled = true
			forceFunctions = false
		}

		if contentFetched && !metadataFetched && !metadataHintSent {
			contents = append(contents, geminiContent{
				Role: "user",
				Parts: []geminiPart{
					{Text: "Retrieve the referendum metadata (resource:\"metadata\") so you know the title, proposer, and attachments before answering."},
				},
			})
			metadataHintSent = true
		} else if metadataFetched && !contentFetched && !contentHintSent {
			contents = append(contents, geminiContent{
				Role: "user",
				Parts: []geminiPart{
					{Text: "Retrieve the full referendum content (resource:\"content\") before answering."},
				},
			})
			contentHintSent = true
		}
	}

	return "", fmt.Errorf("gemini: chat tool loop exceeded")
}

func (c *client) buildRequestBody(opts core.Options, userText string, enableSearch bool) map[string]any {
	content := map[string]any{
		"role": "user",
		"parts": []map[string]string{
			{"text": userText},
		},
	}

	body := map[string]any{
		"contents": []map[string]any{content},
		"generationConfig": map[string]any{
			"temperature":     opts.Temperature,
			"maxOutputTokens": maxTokens(opts.MaxCompletionTokens),
		},
	}

	if strings.TrimSpace(opts.SystemPrompt) != "" {
		body["systemInstruction"] = map[string]any{
			"parts": []map[string]string{
				{"text": opts.SystemPrompt},
			},
		}
	}

	if enableSearch {
		body["tools"] = []map[string]any{
			{
				"google_search": map[string]any{},
			},
		}
	}

	return body
}

func (c *client) send(ctx context.Context, model string, payload map[string]any) (string, error) {
	body, err := c.callGenerateContent(ctx, model, payload)
	if err != nil {
		return "", err
	}

	var result generateContentResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return "", err
	}
	text := result.FirstText()
	if text == "" {
		return "", fmt.Errorf("gemini: empty response")
	}
	return text, nil
}

func (c *client) callGenerateContent(ctx context.Context, model string, payload map[string]any) ([]byte, error) {
	modelPath := normalizeModel(model)
	url := fmt.Sprintf("%s/%s:generateContent?key=%s", baseURL, modelPath, c.apiKey)
	bodyBytes, _ := json.Marshal(payload)

	_, body, err := webclient.DoWithRetry(ctx, 3, 2*time.Second, func() (int, []byte, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewBuffer(bodyBytes))
		if err != nil {
			return 0, nil, err
		}
		req.Header.Set("Content-Type", "application/json")
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
		return nil, fmt.Errorf("gemini API error: %w", err)
	}
	return body, nil
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

func hasWebSearch(opts core.Options, tools []core.Tool) bool {
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

func maxTokens(requested int) int {
	if requested <= 0 {
		return defaultMaxTokens
	}
	return requested
}

func normalizeModel(model string) string {
	model = strings.TrimSpace(model)
	if model == "" {
		return defaultModelName
	}
	if strings.HasPrefix(model, "models/") {
		return model
	}
	return "models/" + model
}

type generateContentResponse struct {
	Candidates []struct {
		Content geminiContent `json:"content"`
	} `json:"candidates"`
}

type geminiContent struct {
	Role  string       `json:"role"`
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	Text             string                  `json:"text,omitempty"`
	FunctionCall     *geminiFunctionCall     `json:"functionCall,omitempty"`
	FunctionResponse *geminiFunctionResponse `json:"functionResponse,omitempty"`
}

type geminiFunctionCall struct {
	Name string         `json:"name"`
	Args map[string]any `json:"args"`
}

type geminiFunctionResponse struct {
	Name     string         `json:"name"`
	Response map[string]any `json:"response"`
}

func (r generateContentResponse) FirstText() string {
	for _, candidate := range r.Candidates {
		if text := candidate.Content.FirstText(); text != "" {
			return text
		}
	}
	return ""
}

func (c geminiContent) FirstText() string {
	sb := strings.Builder{}
	for idx, part := range c.Parts {
		if strings.TrimSpace(part.Text) == "" {
			continue
		}
		if sb.Len() > 0 && idx > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString(part.Text)
	}
	return strings.TrimSpace(sb.String())
}

func buildGeminiToolsPayload(tools []core.Tool, enableSearch bool) ([]map[string]any, map[string]core.Tool, []string) {
	declarations := []map[string]any{}
	toolMap := map[string]core.Tool{}
	functionNames := []string{}
	for idx, t := range tools {
		if strings.ToLower(t.Type) != "mcp_referenda" {
			continue
		}
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
		declarations = append(declarations, funcDef)
		toolCopy := t
		toolCopy.Name = name
		toolMap[name] = toolCopy
		functionNames = append(functionNames, name)
	}

	toolsPayload := []map[string]any{}
	if len(declarations) > 0 {
		toolsPayload = append(toolsPayload, map[string]any{
			"functionDeclarations": declarations,
		})
	}
	if enableSearch {
		if len(declarations) == 0 {
			toolsPayload = append(toolsPayload, map[string]any{
				"google_search": map[string]any{},
			})
		} else {
			log.Printf("gemini: skipping google_search tool because function calling is enabled")
		}
	}
	return toolsPayload, toolMap, functionNames
}

func geminiFunctionCallsFromContent(content geminiContent) []geminiFunctionCall {
	calls := []geminiFunctionCall{}
	for _, part := range content.Parts {
		if part.FunctionCall != nil {
			calls = append(calls, *part.FunctionCall)
		}
	}
	return calls
}

func convertGeminiToolCalls(calls []geminiFunctionCall, seq *int) []openAIToolCall {
	out := make([]openAIToolCall, 0, len(calls))
	for _, call := range calls {
		argsBytes, err := json.Marshal(call.Args)
		if err != nil {
			log.Printf("gemini: failed to marshal tool args for %s: %v", call.Name, err)
			continue
		}
		current := len(out) + 1
		if seq != nil {
			*seq++
			current = *seq
		}
		id := fmt.Sprintf("gemini_call_%d", current)
		out = append(out, openAIToolCall{
			ID:   id,
			Type: "function",
			Function: struct {
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			}{
				Name:      call.Name,
				Arguments: string(argsBytes),
			},
		})
	}
	return out
}

func geminiToolResponseContent(name, output string) geminiContent {
	resultValue := any(output)
	if json.Valid([]byte(output)) {
		var data any
		if err := json.Unmarshal([]byte(output), &data); err == nil {
			resultValue = data
		}
	}
	return geminiContent{
		Role: "function",
		Parts: []geminiPart{
			{
				FunctionResponse: &geminiFunctionResponse{
					Name: name,
					Response: map[string]any{
						"result": resultValue,
					},
				},
			},
		},
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

func (c *client) executeToolCalls(ctx context.Context, calls []openAIToolCall, toolMap map[string]core.Tool) ([]toolOutput, error) {
	outputs := make([]toolOutput, 0, len(calls))
	for _, call := range calls {
		toolDef, ok := toolMap[call.Function.Name]
		if !ok {
			return nil, fmt.Errorf("gemini: unknown tool %s", call.Function.Name)
		}
		args, err := decodeToolArguments(call.Function.Arguments)
		if err != nil {
			log.Printf("gemini: tool %s arg parse error: %v", call.Function.Name, err)
			outputs = append(outputs, toolOutput{
				ToolCallID: call.ID,
				Output:     fmt.Sprintf(`{"error":"invalid arguments: %s"}`, sanitizeToolError(err)),
			})
			continue
		}
		rawArgs := copyArgs(args)
		args = mergeArgs(args, toolDef.Defaults)
		log.Printf("gemini: tool call %s raw=%v merged=%v", call.Function.Name, rawArgs, args)
		result, execErr := c.dispatchTool(ctx, toolDef, args)
		if execErr != nil {
			log.Printf("gemini: tool %s error: %v", call.Function.Name, execErr)
			result = fmt.Sprintf(`{"error":"%s"}`, sanitizeToolError(execErr))
		}
		log.Printf("gemini: tool %s output=%s", call.Function.Name, truncatePayload([]byte(result), 256))
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
