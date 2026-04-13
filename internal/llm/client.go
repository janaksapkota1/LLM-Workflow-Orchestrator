package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const anthropicBaseURL = "https://api.anthropic.com/v1/messages"

// iLLM is the internal interface both the real and mock backends satisfy.
type iLLM interface {
	Complete(ctx context.Context, systemPrompt, userPrompt string) (*CompletionResult, error)
}

// Client wraps the Anthropic Messages API.
type Client struct {
	apiKey     string
	model      string
	maxTokens  int
	httpClient *http.Client
	mock       *MockClient // non-nil when running in mock mode
}

// New creates a real Anthropic-backed Client.
func New(apiKey, model string, maxTokens int) *Client {
	return &Client{
		apiKey:    apiKey,
		model:     model,
		maxTokens: maxTokens,
		httpClient: &http.Client{
			Timeout: 120 * time.Second,
		},
	}
}

// NewWithMock creates a Client that returns fake responses without any API calls.
func NewWithMock() *Client {
	return &Client{mock: NewMock()}
}

// ── Request / Response types ──────────────────────────────────────────────────

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type completionRequest struct {
	Model     string    `json:"model"`
	MaxTokens int       `json:"max_tokens"`
	System    string    `json:"system,omitempty"`
	Messages  []message `json:"messages"`
}

type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type completionResponse struct {
	ID      string         `json:"id"`
	Content []contentBlock `json:"content"`
	Usage   struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// CompletionResult is returned from Complete.
type CompletionResult struct {
	Text         string
	InputTokens  int
	OutputTokens int
}

// Complete sends a prompt to the LLM and returns the text response.
// systemPrompt may be empty; if so it is omitted from the request.
func (c *Client) Complete(ctx context.Context, systemPrompt, userPrompt string) (*CompletionResult, error) {
	if c.mock != nil {
		return c.mock.Complete(ctx, systemPrompt, userPrompt)
	}
	reqBody := completionRequest{
		Model:     c.model,
		MaxTokens: c.maxTokens,
		System:    systemPrompt,
		Messages: []message{
			{Role: "user", Content: userPrompt},
		},
	}

	payload, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, anthropicBaseURL, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("new request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", c.apiKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("http do: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	var cr completionResponse
	if err := json.Unmarshal(body, &cr); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}

	if cr.Error != nil {
		return nil, fmt.Errorf("api error [%s]: %s", cr.Error.Type, cr.Error.Message)
	}

	if len(cr.Content) == 0 {
		return nil, fmt.Errorf("empty content in response")
	}

	result := &CompletionResult{
		Text:         cr.Content[0].Text,
		InputTokens:  cr.Usage.InputTokens,
		OutputTokens: cr.Usage.OutputTokens,
	}
	return result, nil
}

// CompleteWithHistory sends a multi-turn conversation to the LLM.
// Each pair of (role, content) in history is included as prior context.
func (c *Client) CompleteWithHistory(ctx context.Context, systemPrompt string, history []message, userPrompt string) (*CompletionResult, error) {
	messages := make([]message, len(history)+1)
	copy(messages, history)
	messages[len(history)] = message{Role: "user", Content: userPrompt}

	reqBody := completionRequest{
		Model:     c.model,
		MaxTokens: c.maxTokens,
		System:    systemPrompt,
		Messages:  messages,
	}

	payload, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, anthropicBaseURL, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", c.apiKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var cr completionResponse
	if err := json.Unmarshal(body, &cr); err != nil {
		return nil, err
	}

	if cr.Error != nil {
		return nil, fmt.Errorf("api error [%s]: %s", cr.Error.Type, cr.Error.Message)
	}

	if len(cr.Content) == 0 {
		return nil, fmt.Errorf("empty response")
	}

	return &CompletionResult{
		Text:         cr.Content[0].Text,
		InputTokens:  cr.Usage.InputTokens,
		OutputTokens: cr.Usage.OutputTokens,
	}, nil
}