package ai

import (
    "bytes"
    "context"
    "encoding/json"
    "fmt"
    "io"
    "net/http"
    "time"
)

type claudeClient struct {
    apiKey     string
    httpClient *http.Client
    defaults   Options
}

func newClaudeClient(cfg FactoryConfig) *claudeClient {
    return &claudeClient{
        apiKey: cfg.ClaudeKey,
        httpClient: &http.Client{Timeout: 60 * time.Second},
        defaults: Options{
            Model:        valueOrDefault(cfg.Model, "claude-3-haiku-20240307"),
            Temperature:  orFloat(cfg.Temperature, 0.2),
            SystemPrompt: cfg.SystemPrompt,
        },
    }
}

func (c *claudeClient) AnswerQuestion(ctx context.Context, content string, question string, opts Options) (string, error) {
    merged := c.merge(opts)
    reqBody := map[string]interface{}{
        "model": merged.Model,
        "messages": []map[string]string{
            {"role": "user", "content": fmt.Sprintf("Proposal Content:\n%s\n\nQuestion: %s\n\nProvide a direct, concise answer.", content, question)},
        },
        "system":      merged.SystemPrompt,
        "max_tokens":  500,
        "temperature": merged.Temperature,
    }
    b, _ := json.Marshal(reqBody)
    req, err := http.NewRequestWithContext(ctx, "POST", "https://api.anthropic.com/v1/messages", bytes.NewBuffer(b))
    if err != nil { return "", err }
    req.Header.Set("Content-Type", "application/json")
    req.Header.Set("x-api-key", c.apiKey)
    req.Header.Set("anthropic-version", "2023-06-01")
    resp, err := c.httpClient.Do(req)
    if err != nil { return "", err }
    defer resp.Body.Close()
    body, err := io.ReadAll(resp.Body)
    if err != nil { return "", err }
    if resp.StatusCode != http.StatusOK { return "", fmt.Errorf("claude API error: %s", string(body)) }
    var result struct { Content []struct { Text string `json:"text"` } `json:"content"` }
    if err := json.Unmarshal(body, &result); err != nil { return "", err }
    if len(result.Content) == 0 { return "", fmt.Errorf("no response from Claude") }
    return result.Content[0].Text, nil
}

func (c *claudeClient) Respond(ctx context.Context, input string, tools []Tool, opts Options) (string, error) {
    // Claude Messages API has limited tool support compared to OpenAI; ignore tools for now
    merged := c.merge(opts)
    reqBody := map[string]interface{}{
        "model": merged.Model,
        "messages": []map[string]string{
            {"role": "user", "content": input},
        },
        "system":      merged.SystemPrompt,
        "max_tokens":  1000,
        "temperature": merged.Temperature,
    }
    b, _ := json.Marshal(reqBody)
    req, err := http.NewRequestWithContext(ctx, "POST", "https://api.anthropic.com/v1/messages", bytes.NewBuffer(b))
    if err != nil { return "", err }
    req.Header.Set("Content-Type", "application/json")
    req.Header.Set("x-api-key", c.apiKey)
    req.Header.Set("anthropic-version", "2023-06-01")
    resp, err := c.httpClient.Do(req)
    if err != nil { return "", err }
    defer resp.Body.Close()
    body, err := io.ReadAll(resp.Body)
    if err != nil { return "", err }
    if resp.StatusCode != http.StatusOK { return "", fmt.Errorf("claude API error: %s", string(body)) }
    var result struct { Content []struct { Text string `json:"text"` } `json:"content"` }
    if err := json.Unmarshal(body, &result); err != nil { return "", err }
    if len(result.Content) == 0 { return "", fmt.Errorf("no response from Claude") }
    return result.Content[0].Text, nil
}

func (c *claudeClient) merge(opts Options) Options {
    out := c.defaults
    if opts.Model != "" { out.Model = opts.Model }
    if opts.Temperature != 0 { out.Temperature = opts.Temperature }
    if opts.SystemPrompt != "" { out.SystemPrompt = opts.SystemPrompt }
    return out
}


