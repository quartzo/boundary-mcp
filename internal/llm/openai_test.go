package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCompleteJSONRequestShape(t *testing.T) {
	t.Parallel()

	var got chatRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if gotAuth := r.Header.Get("Authorization"); gotAuth != "Bearer secret" {
			t.Errorf("Authorization = %q", gotAuth)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_, _ = w.Write([]byte(`{"model":"test-model","choices":[{"message":{"content":"{\"ok\":true}"}}]}`))
	}))
	t.Cleanup(server.Close)

	client, err := NewOpenAI(Options{
		BaseURL: server.URL + "/v1/",
		APIKey:  "secret",
		Model:   "test-model",
	})
	if err != nil {
		t.Fatal(err)
	}

	resp, err := client.CompleteJSON(context.Background(), Request{
		SystemPrompt: "system",
		UserPrompt:   "user",
		SchemaName:   "thing",
		Schema:       json.RawMessage(`{"type":"object","properties":{"ok":{"type":"boolean"}}}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(resp.JSON) != `{"ok":true}` {
		t.Fatalf("JSON = %s", resp.JSON)
	}
	if resp.Model != "test-model" {
		t.Fatalf("Model = %q", resp.Model)
	}

	if got.Model != "test-model" {
		t.Errorf("request model = %q", got.Model)
	}
	if got.Temperature != 0 {
		t.Errorf("temperature = %v", got.Temperature)
	}
	if got.ResponseFormat == nil || got.ResponseFormat.Type != "json_schema" {
		t.Fatalf("response_format = %+v", got.ResponseFormat)
	}
	if !got.ResponseFormat.JSONSchema.Strict {
		t.Error("json_schema.strict = false")
	}
	if got.ResponseFormat.JSONSchema.Name != "thing" {
		t.Errorf("schema name = %q", got.ResponseFormat.JSONSchema.Name)
	}
	if len(got.Messages) != 2 || got.Messages[0].Role != "system" || got.Messages[1].Role != "user" {
		t.Errorf("messages = %+v", got.Messages)
	}
}

func TestCompleteJSONHTTPError(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "provider unavailable", http.StatusServiceUnavailable)
	}))
	t.Cleanup(server.Close)

	client, err := NewOpenAI(Options{BaseURL: server.URL, Model: "m"})
	if err != nil {
		t.Fatal(err)
	}

	_, err = client.CompleteJSON(context.Background(), Request{
		Schema: json.RawMessage(`{"type":"object"}`),
	})
	if err == nil || !strings.Contains(err.Error(), "503") || !strings.Contains(err.Error(), "provider unavailable") {
		t.Fatalf("err = %v", err)
	}
}

func TestCompleteJSONRejectsInvalidContent(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"not json"}}]}`))
	}))
	t.Cleanup(server.Close)

	client, err := NewOpenAI(Options{BaseURL: server.URL, Model: "m"})
	if err != nil {
		t.Fatal(err)
	}

	_, err = client.CompleteJSON(context.Background(), Request{
		Schema: json.RawMessage(`{"type":"object"}`),
	})
	if err == nil || !strings.Contains(err.Error(), "not valid JSON") {
		t.Fatalf("err = %v", err)
	}
}

func TestCompleteJSONRequiresSchema(t *testing.T) {
	t.Parallel()

	client, err := NewOpenAI(Options{BaseURL: "http://127.0.0.1:1", Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.CompleteJSON(context.Background(), Request{}); err == nil {
		t.Fatal("CompleteJSON without a schema returned nil error")
	}
}

func TestNewOpenAIValidation(t *testing.T) {
	t.Parallel()

	if _, err := NewOpenAI(Options{Model: "m"}); err == nil {
		t.Fatal("missing base URL accepted")
	}
	if _, err := NewOpenAI(Options{BaseURL: "http://x"}); err == nil {
		t.Fatal("missing model accepted")
	}
}
