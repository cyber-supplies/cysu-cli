package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEndpointsListForwardsFullWithAFilter(t *testing.T) {
	oldToken := apiToken
	apiToken = "test-token"
	t.Cleanup(func() { apiToken = oldToken })

	queries := make(chan map[string]string, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/api/endpoints" {
			http.NotFound(w, r)
			return
		}
		values := map[string]string{}
		for name, items := range r.URL.Query() {
			values[name] = items[0]
		}
		queries <- values
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"endpoints": []any{}})
	}))
	defer server.Close()
	t.Setenv("CYSU_API_URL", server.URL)

	captureStdout(t, func() error {
		return listEndpoints(flagValues{"domain": "example.com", "method": "get", "full": "true"})
	})
	if got := <-queries; got["domain"] != "example.com" || got["method"] != "get" || got["full"] != "true" {
		t.Fatalf("full query = %#v", got)
	}

	captureStdout(t, func() error { return listEndpoints(flagValues{}) })
	if got := <-queries; len(got) != 0 {
		t.Fatalf("compact query = %#v", got)
	}
}

func TestEndpointsListFullRequiresDomainOrPath(t *testing.T) {
	cmd, ok := endpointsResource.lookup("list")
	if !ok {
		t.Fatal("endpoints list command is missing")
	}
	_, err := cmd.parse(endpointsResource, []string{"--full", "--method", "GET", "--token", "test"})
	if err == nil || !strings.Contains(err.Error(), "--full requires --domain or --path") {
		t.Fatalf("full validation error = %v", err)
	}
	if _, err := cmd.parse(endpointsResource,
		[]string{"--full", "--path", "/api", "--token", "test"}); err != nil {
		t.Fatalf("full path validation failed: %v", err)
	}
}
