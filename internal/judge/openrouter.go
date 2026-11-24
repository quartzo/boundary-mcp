package judge

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	defaultBaseURL = "https://openrouter.ai/api/v1"
	defaultModel   = "typesafe/jev-1.14"
)

// OpenRouterOptions configures the JEV client.
type OpenRouterOptions struct {
	// BaseURL defaults to https://openrouter.ai/api/v1.
	BaseURL string
	// APIKey is sent as a bearer token.
	APIKey string
	// Model defaults to typesafe/jev-1.14.
	Model string
	// HTTPClient overrides the default 10s client.
	HTTPClient *http.Client
}

// OpenRouter calls the typed System One endpoint for JEV.
type OpenRouter struct {
	baseURL string
	apiKey  string
	model   string
	http    *http.Client
}

// NewOpenRouter builds a JEV client.
func NewOpenRouter(opts OpenRouterOptions) (*OpenRouter, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(opts.BaseURL), "/")
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	model := strings.TrimSpace(opts.Model)
	if model == "" {
		model = defaultModel
	}
	httpClient := opts.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	return &OpenRouter{
		baseURL: baseURL,
		apiKey:  opts.APIKey,
		model:   model,
		http:    httpClient,
	}, nil
}

// Model returns the configured model ID.
func (o *OpenRouter) Model() string { return o.model }

// Evaluate sends all rules as Noul questions in one System One request.
func (o *OpenRouter) Evaluate(ctx context.Context, in Input) (*Assessment, error) {
	payload, err := json.Marshal(request{
		Model:     o.model,
		State:     buildState(in),
		Questions: questions(),
	})
	if err != nil {
		return nil, fmt.Errorf("judge: encode request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.baseURL+"/systemone", bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("judge: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Title", "boundary-mcp")
	if o.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+o.apiKey)
	}

	start := time.Now()
	resp, err := o.http.Do(req)
	latency := time.Since(start)
	if err != nil {
		return nil, fmt.Errorf("judge: request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, fmt.Errorf("judge: read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("judge: %s: %s", resp.Status, truncate(body, 500))
	}

	var decoded response
	if err := json.Unmarshal(body, &decoded); err != nil {
		return nil, fmt.Errorf("judge: decode response: %w", err)
	}

	rules := make(map[string]float64, len(Rules))
	for _, rule := range Rules {
		answer, ok := decoded.Answers[rule.ID]
		if !ok {
			return nil, fmt.Errorf("judge: response is missing rule %q", rule.ID)
		}
		if answer.Type != "" && answer.Type != "noul" {
			return nil, fmt.Errorf("judge: rule %q returned type %q", rule.ID, answer.Type)
		}
		rules[rule.ID] = answer.Noul
	}

	model := decoded.Model
	if model == "" {
		model = o.model
	}
	return &Assessment{
		Rules:        rules,
		Model:        model,
		RequestID:    decoded.ID,
		InputTokens:  decoded.Usage.InputTokens,
		OutputTokens: decoded.Usage.OutputTokens,
		CostUSD:      decoded.Usage.Cost,
		Latency:      latency,
	}, nil
}

type request struct {
	Model     string              `json:"model"`
	State     state               `json:"state"`
	Questions map[string]question `json:"questions"`
}

type response struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Answers map[string]struct {
		Type string  `json:"type"`
		Noul float64 `json:"noul"`
	} `json:"answers"`
	Usage struct {
		InputTokens  int     `json:"input_tokens"`
		OutputTokens int     `json:"output_tokens"`
		Cost         float64 `json:"cost"`
	} `json:"usage"`
}

func truncate(data []byte, max int) string {
	text := strings.TrimSpace(string(data))
	if len(text) <= max {
		return text
	}
	return text[:max] + "..."
}
