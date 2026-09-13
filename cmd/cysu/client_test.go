package main

// Tests for the tracing hooks in client.go. They run against a local server
// so a record is produced by a real exchange rather than by calling the trace
// functions directly.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// records splits captured trace output into one decoded record per line.
func records(t *testing.T, out string) []map[string]any {
	t.Helper()

	lines := strings.Split(strings.TrimSpace(out), "\n")
	decoded := make([]map[string]any, 0, len(lines))

	for _, line := range lines {
		if line == "" {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("record is not valid JSON: %s", line)
		}
		decoded = append(decoded, rec)
	}
	return decoded
}

func TestSendTraces(t *testing.T) {
	const token = "sk-live-abcd1234"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"error":"Project Not Found"}`)
	}))
	defer srv.Close()

	t.Setenv("CYSU_API_URL", srv.URL)
	useAPIToken(t, token)
	buf := captureTrace(t)

	_, err := send("POST", "/projects/p1/accounts", map[string]any{
		"username": "pentester1",
		"password": "hunter2",
	})
	if err == nil {
		t.Fatal("send did not report the 404")
	}

	// The backend's own error body is still what the caller sees.
	if !strings.Contains(err.Error(), "Project Not Found") {
		t.Errorf("error = %q, want the backend's body", err.Error())
	}

	out := buf.String()
	if strings.Contains(out, token) {
		t.Errorf("trace leaked the API token: %s", out)
	}
	if strings.Contains(out, "hunter2") {
		t.Errorf("trace leaked a password: %s", out)
	}

	recs := records(t, out)
	if len(recs) != 2 {
		t.Fatalf("emitted %d records, want a request and a response: %s", len(recs), out)
	}

	req, resp := recs[0], recs[1]
	if req["debug"] != "request" || resp["debug"] != "response" {
		t.Fatalf("unexpected record kinds: %s", out)
	}
	if req["method"] != "POST" {
		t.Errorf("method = %v, want POST", req["method"])
	}
	if req["n"] != resp["n"] {
		t.Errorf("response n = %v, does not match request n = %v", resp["n"], req["n"])
	}
	if resp["status"] != float64(http.StatusNotFound) {
		t.Errorf("status = %v, want 404", resp["status"])
	}

	// A local exchange takes under a millisecond, so this is 0; it still has
	// to be reported rather than dropped.
	if _, found := resp["duration_ms"]; !found {
		t.Errorf("response has no duration_ms: %v", resp)
	}

	// The failing reply is traced, which is the whole point of tracing it
	// before the status is judged.
	body, _ := resp["body"].(map[string]any)
	if body["error"] != "Project Not Found" {
		t.Errorf("traced response body = %v, want the backend's error", resp["body"])
	}

	// The Authorization header is present but carries no usable credential.
	headers, _ := req["headers"].(map[string]any)
	if got, want := headers["Authorization"], "Bearer ****1234"; got != want {
		t.Errorf("Authorization = %v, want %q", got, want)
	}
}

func TestSendTracingOff(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"success":true}`)
	}))
	defer srv.Close()

	t.Setenv("CYSU_API_URL", srv.URL)
	useAPIToken(t, "sk-live-abcd1234")
	buf := captureTrace(t)
	setTracing(false)

	raw, err := send("GET", "/projects", nil)
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if string(raw) != `{"success":true}` {
		t.Errorf("body = %s, want the response unchanged", raw)
	}
	if buf.Len() != 0 {
		t.Errorf("tracing off still emitted: %s", buf.String())
	}
}

func TestTransferTraces(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("stored-file-contents"))
	}))
	defer srv.Close()

	useAPIToken(t, "sk-live-abcd1234")
	buf := captureTrace(t)

	// A presigned URL carries its signature in the query string.
	resp, err := transfer("GET", srv.URL+"/bucket/notes.md?X-Goog-Signature=deadbeefsecret", nil, 0, "")
	if err != nil {
		t.Fatalf("transfer: %v", err)
	}
	resp.Body.Close()

	out := buf.String()
	if strings.Contains(out, "deadbeefsecret") || strings.Contains(out, "Signature") {
		t.Errorf("trace leaked the presigned URL signature: %s", out)
	}
	if !strings.Contains(out, "/bucket/notes.md") {
		t.Errorf("trace lost the URL itself: %s", out)
	}

	recs := records(t, out)
	if len(recs) != 2 {
		t.Fatalf("emitted %d records, want a transfer and a response: %s", len(recs), out)
	}
	if recs[0]["debug"] != "transfer" || recs[1]["debug"] != "response" {
		t.Fatalf("unexpected record kinds: %s", out)
	}

	// The contents are never traced, so the size comes from the response.
	if got, want := recs[1]["bytes"], float64(len("stored-file-contents")); got != want {
		t.Errorf("bytes = %v, want %v", got, want)
	}
}

// A storage host reports its errors as XML rather than JSON, and that body is
// worth tracing: it is the only explanation of why an upload was rejected.
func TestTransferTracesErrorBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `<?xml version="1.0"?><Error><Code>SignatureDoesNotMatch</Code></Error>`)
	}))
	defer srv.Close()

	useAPIToken(t, "sk-live-abcd1234")
	buf := captureTrace(t)

	body := strings.NewReader("ciphertext")
	if _, err := transfer("PUT", srv.URL+"/bucket/notes.md?sig=x", body, 10, "application/octet-stream"); err == nil {
		t.Fatal("transfer did not report the 403")
	}

	recs := records(t, buf.String())
	if len(recs) != 2 {
		t.Fatalf("emitted %d records, want a transfer and a response: %s", len(recs), buf.String())
	}
	if recs[1]["status"] != float64(http.StatusForbidden) {
		t.Errorf("status = %v, want 403", recs[1]["status"])
	}
	if text, _ := recs[1]["body"].(string); !strings.Contains(text, "SignatureDoesNotMatch") {
		t.Errorf("traced response lost the storage host's error: %v", recs[1]["body"])
	}
	if recs[0]["content_type"] != "application/octet-stream" {
		t.Errorf("content_type = %v, want application/octet-stream", recs[0]["content_type"])
	}
}

// Placeholders such as <token> appear in what the CLI prints, and an agent
// reads them. errors.go and findings.go both turn HTML escaping off for this
// reason; printJSON has to match, or the same text reads differently
// depending on which of them printed it.
func TestPrintJSONKeepsAngleBrackets(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}

	prev := os.Stdout
	os.Stdout = w
	printErr := printJSON(map[string]string{"hint": "pass --token <token>"})
	w.Close()
	os.Stdout = prev

	if printErr != nil {
		t.Fatalf("printJSON: %v", printErr)
	}

	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "<token>") {
		t.Errorf("printJSON escaped a placeholder: %s", out)
	}
}
