package mcp

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHTTPHealth(t *testing.T) {
	t.Parallel()

	s := newTestServer(t, Options{})
	handler := s.HTTPHandler("secret")

	rec := doRequest(handler, http.MethodGet, HealthPath, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"status":"ok"`) {
		t.Fatalf("body = %q", rec.Body.String())
	}
}

func TestHTTPRequiresBearer(t *testing.T) {
	t.Parallel()

	s := newTestServer(t, Options{})
	handler := s.HTTPHandler("secret")

	for _, tc := range []struct {
		name   string
		header string
	}{
		{name: "missing"},
		{name: "wrong", header: "Bearer nope"},
	} {
		rec := doRequest(handler, http.MethodPost, MCPPath, tc.header)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s: status = %d, want 401", tc.name, rec.Code)
		}
	}

	rec := doRequest(handler, http.MethodPost, MCPPath, "Bearer secret")
	if rec.Code == http.StatusUnauthorized {
		t.Fatalf("authorized request was rejected: %s", rec.Body.String())
	}
}

func TestHTTPWithoutTokenSkipsAuth(t *testing.T) {
	t.Parallel()

	s := newTestServer(t, Options{})
	handler := s.HTTPHandler("")

	rec := doRequest(handler, http.MethodPost, MCPPath, "")
	if rec.Code == http.StatusUnauthorized {
		t.Fatal("request without a configured token was rejected")
	}
}

func doRequest(handler http.Handler, method, path, authorization string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader("{}"))
	req.Host = "127.0.0.1"
	req.RemoteAddr = "127.0.0.1:12345"
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}
