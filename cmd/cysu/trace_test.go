package main

// Tests for the redaction rules in trace.go. Tracing is the one place the CLI
// renders credentials for a person to read, so most of what follows checks
// what does not come out.

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

// captureTrace turns tracing on and collects the records it emits, restoring
// the previous state when the test ends.
func captureTrace(t *testing.T) *bytes.Buffer {
	t.Helper()

	buf := &bytes.Buffer{}
	prevOut, prevOn, prevSeq := traceOut, tracingEnabled, traceSeq
	traceOut, tracingEnabled, traceSeq = buf, true, 0

	t.Cleanup(func() {
		traceOut, tracingEnabled, traceSeq = prevOut, prevOn, prevSeq
	})
	return buf
}

// useAPIToken sets the token of the invocation for the duration of the test.
func useAPIToken(t *testing.T, token string) {
	t.Helper()

	prev := apiToken
	apiToken = token
	t.Cleanup(func() { apiToken = prev })
}

func TestIsSensitive(t *testing.T) {
	hide := []string{
		"password", "Password", "passphrase",
		"token", "access_token", "apiToken", "CYSU_TOKEN",
		"cookie", "cookies", "Set-Cookie", "sessionCookie",
		"secret", "client-secret", "credentials",
		"auth", "Authorization", "authHeader",
		"key", "apiKey", "private_key",
	}
	for _, name := range hide {
		if !isSensitive(name) {
			t.Errorf("isSensitive(%q) = false, want true", name)
		}
	}

	// Fields the backend actually returns, which must stay readable.
	keep := []string{
		"id", "name", "path", "scope", "severity", "title", "description",
		"username", "location", "url", "method", "parameters", "format",
		"optional", "success", "files", "projects", "findings", "endpoints",
		"uploadUrl", "downloadUrl", "size", "project",
	}
	for _, name := range keep {
		if isSensitive(name) {
			t.Errorf("isSensitive(%q) = true, want false", name)
		}
	}
}

func TestRedactJSON(t *testing.T) {
	// Shaped like an `accounts get-auth` reply, which carries live credentials.
	body := []byte(`{
		"id": "acc_01m1r85wgef0x88sa4p8qrkjtb",
		"username": "pentester1",
		"password": "hunter2",
		"cookies": "session=8f14e45fceea167a; token=5d41402abc4b2a76;",
		"nested": {"apiKey": "sk-nested-secret"},
		"accounts": [{"secret": "array-secret", "location": "https://example.com/login"}]
	}`)

	redacted, ok := redactJSON(body)
	if !ok {
		t.Fatal("redactJSON reported the body was not JSON")
	}
	out := string(redacted)

	for _, secret := range []string{"hunter2", "8f14e45fceea167a", "5d41402abc4b2a76", "sk-nested-secret", "array-secret"} {
		if strings.Contains(out, secret) {
			t.Errorf("redacted body still contains %q: %s", secret, out)
		}
	}

	// Anything that is not a credential has to survive, or the trace is no use
	// for working out what happened.
	for _, want := range []string{"acc_01m1r85wgef0x88sa4p8qrkjtb", "pentester1", "https://example.com/login"} {
		if !strings.Contains(out, want) {
			t.Errorf("redacted body dropped %q: %s", want, out)
		}
	}

	if _, ok := redactJSON([]byte("<html><body>Bad Gateway</body></html>")); ok {
		t.Error("redactJSON accepted an HTML error page as JSON")
	}
}

// Ids are strings in the spec but integers elsewhere in the backend. Decoding
// a large one through float64 would reformat it, leaving the trace disagreeing
// with what was actually sent.
func TestRedactJSONNumbers(t *testing.T) {
	redacted, ok := redactJSON([]byte(`{"id":12345678901234567890,"size":1024}`))
	if !ok {
		t.Fatal("redactJSON reported the body was not JSON")
	}
	if out := string(redacted); !strings.Contains(out, "12345678901234567890") {
		t.Errorf("redactJSON reformatted a large id: %s", out)
	}
}

func TestTraceBody(t *testing.T) {
	t.Run("json", func(t *testing.T) {
		body := []byte(`{"name":"Example App","password":"hunter2"}`)

		value, size, truncated := traceBody(body)
		if truncated {
			t.Error("a short body was reported as truncated")
		}
		if size != len(body) {
			t.Errorf("size = %d, want %d", size, len(body))
		}

		// A JSON body is embedded as JSON, not as a quoted string.
		var obj map[string]any
		if err := json.Unmarshal(value, &obj); err != nil {
			t.Fatalf("traced body is not embedded as JSON: %s", value)
		}
		if obj["password"] != redactedMarker {
			t.Errorf("password = %v, want %q", obj["password"], redactedMarker)
		}
		if obj["name"] != "Example App" {
			t.Errorf("name = %v, want %q", obj["name"], "Example App")
		}
	})

	t.Run("not json", func(t *testing.T) {
		value, size, truncated := traceBody([]byte("Bad Gateway"))
		if truncated {
			t.Error("a short body was reported as truncated")
		}
		if size != len("Bad Gateway") {
			t.Errorf("size = %d, want %d", size, len("Bad Gateway"))
		}

		var text string
		if err := json.Unmarshal(value, &text); err != nil {
			t.Fatalf("non-JSON body was not carried as a JSON string: %s", value)
		}
		if text != "Bad Gateway" {
			t.Errorf("text = %q, want %q", text, "Bad Gateway")
		}
	})

	t.Run("empty", func(t *testing.T) {
		value, size, truncated := traceBody(nil)
		if value != nil || size != 0 || truncated {
			t.Errorf("traceBody(nil) = %s, %d, %v; want nil, 0, false", value, size, truncated)
		}
	})
}

// The key below sorts first, so the secret would land inside the part that
// survives the cut if a long body were truncated before it was redacted.
func TestTraceBodyTruncation(t *testing.T) {
	const secret = "hunter2-DO-NOT-LEAK"

	body, err := json.Marshal(map[string]any{
		"aaa_password": secret,
		"notes":        strings.Repeat("x", maxTraceBody*2),
	})
	if err != nil {
		t.Fatal(err)
	}

	value, size, truncated := traceBody(body)
	if !truncated {
		t.Fatal("an oversized body was not reported as truncated")
	}
	if size != len(body) {
		t.Errorf("size = %d, want the true body size %d", size, len(body))
	}
	if strings.Contains(string(value), secret) {
		t.Error("an oversized body was cut before it was redacted")
	}
	if !strings.Contains(string(value), redactedMarker) {
		t.Errorf("cut body lost its redaction marker: %s", value)
	}
	if len(value) > maxTraceBody+64 { // room for quoting and escapes
		t.Errorf("cut body is %d bytes, want about %d", len(value), maxTraceBody)
	}
}

func TestRedactHeaders(t *testing.T) {
	header := http.Header{}
	header.Set("Authorization", "Bearer sk-live-abcd1234")
	header.Set("Cookie", "session=8f14e45fceea167a")
	header.Set("Accept", "application/json")
	header.Set("Content-Type", "application/json")

	out := redactHeaders(header)

	want := map[string]string{
		"Authorization": "Bearer ****1234",
		"Cookie":        redactedMarker,
		"Accept":        "application/json",
		"Content-Type":  "application/json",
	}
	for name, value := range want {
		if out[name] != value {
			t.Errorf("%s = %q, want %q", name, out[name], value)
		}
	}

	if out := redactHeaders(nil); out != nil {
		t.Errorf("redactHeaders(nil) = %v, want nil", out)
	}
}

func TestRedactAuthorization(t *testing.T) {
	cases := map[string]string{
		"Bearer sk-live-abcd1234": "Bearer ****1234",
		"Bearer abc":              "Bearer ****", // too short to hint at
		"sk-live-abcd1234":        "****1234",    // no scheme
		"":                        redactedMarker,
	}
	for in, want := range cases {
		if got := redactAuthorization(in); got != want {
			t.Errorf("redactAuthorization(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestScrubToken(t *testing.T) {
	useAPIToken(t, "sk-live-abcd1234")

	in := "GET https://example.com/api?token=sk-live-abcd1234 failed"
	if out := scrubToken(in); strings.Contains(out, "sk-live-abcd1234") {
		t.Errorf("scrubToken left the token in place: %s", out)
	}

	// A short token is not a real one, and replacing it would corrupt
	// unrelated text that happens to contain the same characters.
	useAPIToken(t, "abc")
	if out := scrubToken("abcdef"); out != "abcdef" {
		t.Errorf("scrubToken(%q) = %q, want it unchanged", "abcdef", out)
	}
}

func TestTraceRequest(t *testing.T) {
	const token = "sk-live-abcd1234"
	useAPIToken(t, token)
	buf := captureTrace(t)

	header := http.Header{}
	header.Set("Authorization", "Bearer "+token)
	header.Set("Accept", "application/json")

	n := traceRequest("POST", "https://api.example.com/api/projects/p1/accounts", header,
		[]byte(`{"username":"pentester1","password":"hunter2"}`))
	if n != 1 {
		t.Errorf("traceRequest returned %d, want the first sequence number 1", n)
	}

	line := buf.String()
	if strings.Contains(line, token) {
		t.Errorf("traced request leaked the API token: %s", line)
	}
	if strings.Contains(line, "hunter2") {
		t.Errorf("traced request leaked a password: %s", line)
	}

	var rec map[string]any
	if err := json.Unmarshal([]byte(line), &rec); err != nil {
		t.Fatalf("traced request is not valid JSON: %s", line)
	}
	if rec["debug"] != "request" {
		t.Errorf("debug = %v, want %q", rec["debug"], "request")
	}
	if rec["method"] != "POST" {
		t.Errorf("method = %v, want %q", rec["method"], "POST")
	}
}

// A URL is not covered by the field name rules, so it is the scrub in emit
// that has to catch a token appearing in one.
func TestEmitScrubsToken(t *testing.T) {
	const token = "sk-live-abcd1234"
	useAPIToken(t, token)
	buf := captureTrace(t)

	traceRequest("GET", "https://api.example.com/api/projects?access="+token, nil, nil)

	if line := buf.String(); strings.Contains(line, token) {
		t.Errorf("emit left a token in a field the redaction rules do not cover: %s", line)
	}
}

func TestTraceTransfer(t *testing.T) {
	buf := captureTrace(t)

	traceTransfer("PUT", "https://storage.googleapis.com/bucket/file?X-Goog-Signature=deadbeefsecret",
		2048, "application/octet-stream")

	line := buf.String()
	if strings.Contains(line, "deadbeefsecret") || strings.Contains(line, "X-Goog-Signature") {
		t.Errorf("traced transfer leaked the presigned URL signature: %s", line)
	}
	if !strings.Contains(line, "https://storage.googleapis.com/bucket/file") {
		t.Errorf("traced transfer lost the URL itself: %s", line)
	}
}

// A trace record must never look like a failure: an agent tests for the
// "error" key to decide whether a command succeeded.
func TestNoErrorKey(t *testing.T) {
	buf := captureTrace(t)

	header := http.Header{}
	header.Set("Authorization", "Bearer sk-live-abcd1234")

	n := traceRequest("GET", "https://api.example.com/api/projects", header, nil)
	traceResponse(n, 500, 120*time.Millisecond, []byte(`{"error":"Project Not Found"}`))
	traceFailure(n, 30*time.Millisecond, errors.New("connection refused"))
	traceTransfer("GET", "https://storage.example.com/f?sig=x", 0, "")
	traceTransferResponse(n, 200, 90*time.Millisecond, 2048)
	traceEvent("token", map[string]any{"source": "CYSU_TOKEN", "token": "sk-live-abcd1234"})

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 6 {
		t.Fatalf("emitted %d records, want 6: %s", len(lines), buf.String())
	}

	for _, line := range lines {
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("record is not valid JSON: %s", line)
		}
		if _, found := rec["error"]; found {
			t.Errorf("record carries an \"error\" key: %s", line)
		}
		if _, found := rec["debug"]; !found {
			t.Errorf("record has no \"debug\" key: %s", line)
		}
	}

	// The response body above is itself an error reply. Its "error" belongs to
	// the traced body, not to the record.
	if !strings.Contains(lines[1], "Project Not Found") {
		t.Errorf("traced response lost the backend's error body: %s", lines[1])
	}
	if strings.Contains(lines[5], "sk-live-abcd1234") {
		t.Errorf("traced event leaked a token: %s", lines[5])
	}
}

func TestTracingOff(t *testing.T) {
	buf := captureTrace(t)
	setTracing(false)

	header := http.Header{}
	header.Set("Authorization", "Bearer sk-live-abcd1234")

	if n := traceRequest("GET", "https://api.example.com/api/projects", header, nil); n != 0 {
		t.Errorf("traceRequest returned %d with tracing off, want 0", n)
	}
	traceResponse(0, 200, time.Millisecond, []byte(`{"success":true}`))
	traceFailure(0, time.Millisecond, errors.New("connection refused"))
	traceTransfer("GET", "https://storage.example.com/f?sig=x", 0, "")
	traceTransferResponse(0, 200, time.Millisecond, 2048)
	traceEvent("token", map[string]any{"source": "CYSU_TOKEN"})

	if buf.Len() != 0 {
		t.Errorf("tracing off still emitted: %s", buf.String())
	}
}

func TestTruthy(t *testing.T) {
	for _, v := range []string{"1", "true", "TRUE", "yes", "on", "debug"} {
		if !truthy(v) {
			t.Errorf("truthy(%q) = false, want true", v)
		}
	}
	for _, v := range []string{"", "  ", "0", "false", "FALSE", "no", "off"} {
		if truthy(v) {
			t.Errorf("truthy(%q) = true, want false", v)
		}
	}

	t.Setenv("CYSU_DEBUG", "1")
	if !tracingFromEnv() {
		t.Error("CYSU_DEBUG=1 did not turn tracing on")
	}

	t.Setenv("CYSU_DEBUG", "0")
	if tracingFromEnv() {
		t.Error("CYSU_DEBUG=0 turned tracing on")
	}
}
