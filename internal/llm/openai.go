// Package llm provides a small OpenAI-compatible client for structured output.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Request is a single structured-output completion request.
type Request struct {
	SystemPrompt string
	UserPrompt   string
	// SchemaName identifies the JSON schema in the provider request.
	SchemaName string
	// Schema is the JSON Schema the response must satisfy.
	Schema json.RawMessage
}

// Response is a validated JSON completion.
type Response struct {
	JSON  []byte
	Model string
}

// Options configures an OpenAI-compatible client.
type Options struct {
	// BaseURL is the API root, e.g. https://api.openai.com/v1.
	BaseURL string
	// APIKey is sent as a bearer token when non-empty.
	APIKey string
	// Model is the model identifier.
	Model string
	// HTTPClient overrides the default client.
	HTTPClient *http.Client
}

// Client calls an OpenAI-compatible /chat/completions endpoint.
type Client struct {
	baseURL string
	apiKey  string
	model   string
	http    *http.Client
}

// NewOpenAI returns a client for an OpenAI-compatible API.
func NewOpenAI(opts Options) (*Client, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(opts.BaseURL), "/")
	if baseURL == "" {
		return nil, errors.New("llm: base URL is required")
	}
	model := strings.TrimSpace(opts.Model)
	if model == "" {
		return nil, errors.New("llm: model is required")
	}
	httpClient := opts.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 2 * time.Minute}
	}
	return &Client{
		baseURL: baseURL,
		apiKey:  opts.APIKey,
		model:   model,
		http:    httpClient,
	}, nil
}

// Model returns the configured model identifier.
func (c *Client) Model() string { return c.model }

// CompleteJSON asks the model for a JSON document matching req.Schema.
func (c *Client) CompleteJSON(ctx context.Context, req Request) (*Response, error) {
	if len(req.Schema) == 0 || !json.Valid(req.Schema) {
		return nil, errors.New("llm: a valid JSON schema is required")
	}
	if strings.TrimSpace(req.SchemaName) == "" {
		req.SchemaName = "response"
	}

	body, err := json.Marshal(chatRequest{
		Model: c.model,
		Messages: []message{
			{Role: "system", Content: req.SystemPrompt},
			{Role: "user", Content: req.UserPrompt},
		},
		Temperature: 0,
		ResponseFormat: &responseFormat{
			Type: "json_schema",
			JSONSchema: jsonSchema{
				Name:   req.SchemaName,
				Strict: true,
				Schema: req.Schema,
			},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("llm: encode request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("llm: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("llm: request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, fmt.Errorf("llm: read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("llm: %s: %s", resp.Status, truncate(data, 400))
	}

	var decoded chatResponse
	if err := json.Unmarshal(data, &decoded); err != nil {
		return nil, fmt.Errorf("llm: decode response: %w", err)
	}
	if len(decoded.Choices) == 0 || decoded.Choices[0].Message.Content == "" {
		return nil, errors.New("llm: response has no message content")
	}

	content := []byte(decoded.Choices[0].Message.Content)
	if !json.Valid(content) {
		return nil, fmt.Errorf("llm: response is not valid JSON: %s", truncate(content, 400))
	}

	model := decoded.Model
	if model == "" {
		model = c.model
	}
	return &Response{JSON: content, Model: model}, nil
}

type chatRequest struct {
	Model          string          `json:"model"`
	Messages       []message       `json:"messages"`
	Temperature    float64         `json:"temperature"`
	ResponseFormat *responseFormat `json:"response_format,omitempty"`
}

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type responseFormat struct {
	Type       string     `json:"type"`
	JSONSchema jsonSchema `json:"json_schema"`
}

type jsonSchema struct {
	Name   string          `json:"name"`
	Strict bool            `json:"strict"`
	Schema json.RawMessage `json:"schema"`
}

type chatResponse struct {
	Model   string `json:"model"`
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

func truncate(data []byte, max int) string {
	s := strings.TrimSpace(string(data))
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
