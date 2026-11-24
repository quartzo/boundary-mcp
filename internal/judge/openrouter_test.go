package judge

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestOpenRouterEvaluate(t *testing.T) {
	t.Parallel()

	var got struct {
		Model     string         `json:"model"`
		State     map[string]any `json:"state"`
		Questions map[string]struct {
			Type         string `json:"type"`
			Instructions string `json:"instructions"`
			Criteria     struct {
				True  string `json:"true"`
				False string `json:"false"`
			} `json:"criteria"`
		} `json:"questions"`
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s", r.Method)
		}
		if r.URL.Path != "/api/v1/systemone" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if auth := r.Header.Get("Authorization"); auth != "Bearer secret" {
			t.Errorf("Authorization = %q", auth)
		}
		if title := r.Header.Get("X-Title"); title != "boundary-mcp" {
			t.Errorf("X-Title = %q", title)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}

		scores := fullScores()
		scores["dropped_invariant"] = 0.74
		_, _ = io.WriteString(w, responseJSON(scores))
	}))
	t.Cleanup(server.Close)

	client, err := NewOpenRouter(OpenRouterOptions{
		BaseURL: server.URL + "/api/v1",
		APIKey:  "secret",
		Model:   "typesafe/jev-1.14",
	})
	if err != nil {
		t.Fatal(err)
	}

	assessment, err := client.Evaluate(context.Background(), Input{
		Actor:          "actor_ingest",
		Intent:         "add requested_at",
		PurposeBefore:  "INV-01\n",
		PurposeAfter:   "INV-01\nINV-02\n",
		ProtocolBefore: "table A {}\n",
		ProtocolAfter:  "table A { x:int; }\n",
		FlatcStatus:    "pass",
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}

	if got.Model != "typesafe/jev-1.14" {
		t.Errorf("request model = %q", got.Model)
	}
	if got.State["actor"] != "actor_ingest" || got.State["flatc"] != "pass" {
		t.Errorf("state = %+v", got.State)
	}
	if len(got.Questions) != len(Rules) {
		t.Fatalf("questions = %d, want %d", len(got.Questions), len(Rules))
	}
	for _, rule := range Rules {
		question, ok := got.Questions[rule.ID]
		if !ok {
			t.Fatalf("missing question %q", rule.ID)
		}
		if question.Type != "noul" || question.Instructions == "" || question.Criteria.True == "" || question.Criteria.False == "" {
			t.Fatalf("question %q = %+v", rule.ID, question)
		}
	}

	if assessment.Rules["dropped_invariant"] != 0.74 {
		t.Errorf("dropped_invariant = %v", assessment.Rules["dropped_invariant"])
	}
	if assessment.Rules["intent_mismatch"] != 0.01 {
		t.Errorf("intent_mismatch = %v", assessment.Rules["intent_mismatch"])
	}
	if assessment.Model != "typesafe/jev-1.14" {
		t.Errorf("model = %q", assessment.Model)
	}
	if assessment.InputTokens != 120 || assessment.OutputTokens != 7 || assessment.CostUSD != 0.00004 {
		t.Errorf("usage = %+v", assessment)
	}
	if assessment.Latency <= 0 || assessment.Latency > 5*time.Second {
		t.Errorf("latency = %v", assessment.Latency)
	}
}

func TestOpenRouterHTTPError(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "rate limited", http.StatusTooManyRequests)
	}))
	t.Cleanup(server.Close)

	client, err := NewOpenRouter(OpenRouterOptions{BaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Evaluate(context.Background(), Input{Actor: "a"})
	if err == nil || !strings.Contains(err.Error(), "429") || !strings.Contains(err.Error(), "rate limited") {
		t.Fatalf("err = %v", err)
	}
}

func TestOpenRouterMissingRule(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, responseJSON(map[string]float64{Rules[0].ID: 0.1}))
	}))
	t.Cleanup(server.Close)

	client, err := NewOpenRouter(OpenRouterOptions{BaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Evaluate(context.Background(), Input{Actor: "a"}); err == nil || !strings.Contains(err.Error(), "missing rule") {
		t.Fatalf("err = %v", err)
	}
}

func TestOpenRouterDefaults(t *testing.T) {
	t.Parallel()

	client, err := NewOpenRouter(OpenRouterOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if client.Model() != defaultModel {
		t.Fatalf("model = %q", client.Model())
	}
}

func fullScores() map[string]float64 {
	scores := make(map[string]float64, len(Rules))
	for _, rule := range Rules {
		scores[rule.ID] = 0.01
	}
	return scores
}

func responseJSON(scores map[string]float64) string {
	answers := make(map[string]any, len(scores))
	for rule, probability := range scores {
		answers[rule] = map[string]any{"type": "noul", "noul": probability}
	}
	body, _ := json.Marshal(map[string]any{
		"id":      "gen-test",
		"model":   "typesafe/jev-1.14",
		"answers": answers,
		"usage": map[string]any{
			"input_tokens":  120,
			"output_tokens": 7,
			"cost":          0.00004,
		},
	})
	return string(body)
}
