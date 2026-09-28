package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestJavascriptListRequestBody(t *testing.T) {
	list, ok := javascriptResource.lookup("list")
	if !ok {
		t.Fatal("javascript list command not found")
	}

	for _, tc := range []struct {
		name string
		args []string
		body string
	}{
		{name: "all domains", body: `{}`},
		{name: "one domain", args: []string{"--domain", "example.com"}, body: `{"domain":"example.com"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost {
					t.Errorf("method = %s, want POST", r.Method)
				}
				if r.URL.Path != "/api/javascript/list" {
					t.Errorf("path = %s, want /api/javascript/list", r.URL.Path)
				}
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Errorf("reading body: %v", err)
				}
				if string(body) != tc.body {
					t.Errorf("body = %q, want %q", body, tc.body)
				}
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(`[]`))
			}))
			t.Cleanup(server.Close)
			t.Setenv("CYSU_API_URL", server.URL)
			setTestAPIToken(t)

			values, err := list.parse(javascriptResource, tc.args)
			if err != nil {
				t.Fatalf("parsing args: %v", err)
			}
			if err := listJavascript(values["domain"]); err != nil {
				t.Fatalf("listJavascript() error = %v", err)
			}
		})
	}
}
