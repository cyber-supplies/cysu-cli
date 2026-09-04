package main

// client.go is the HTTP layer: it authenticates and builds a request against
// the backend, sends it, prints the JSON that comes back and turns failures
// into errors in the backend's own shape (see errors.go).

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// defaultBaseURL is the backend the CLI talks to unless CYSU_API_URL is set.
const defaultBaseURL = "https://blueprint-backend-670100724771.europe-west1.run.app/"

// apiRoot is the common prefix of every backend route.
const apiRoot = "/api"

var httpClient = &http.Client{Timeout: 30 * time.Second}

// ---------------------------------------------------------------------------
// Authentication
// ---------------------------------------------------------------------------

// tokenFlag lets a single invocation pass its API token on the command line
// instead of through CYSU_TOKEN. Every API command accepts it.
var tokenFlag = flagSpec{name: "token", placeholder: "token", usage: "API token (overrides CYSU_TOKEN)"}

// apiToken authenticates every request of this invocation. loadAPIToken
// resolves it once before an API command runs.
var apiToken string

// loadAPIToken picks the token for this invocation: --token when given,
// otherwise CYSU_TOKEN. Having neither is a usage error, reported before any
// request is made.
func loadAPIToken(v flagValues) error {
	token := strings.TrimSpace(v["token"])
	if token == "" {
		token = strings.TrimSpace(os.Getenv("CYSU_TOKEN"))
	}
	if token == "" {
		return cliError{message: "missing API token: set CYSU_TOKEN or pass --token <token>", exit: exitUsage}
	}
	apiToken = token
	return nil
}

// ---------------------------------------------------------------------------
// Requests
// ---------------------------------------------------------------------------

// resolveBaseURL returns the backend base URL without a trailing slash,
// honouring the CYSU_API_URL override.
func resolveBaseURL() string {
	base := os.Getenv("CYSU_API_URL")
	if strings.TrimSpace(base) == "" {
		base = defaultBaseURL
	}
	return strings.TrimRight(base, "/")
}

// apiPath joins segments into a route below apiRoot, escaping each one so a
// user-supplied id cannot alter the path. For example
// apiPath("projects", id, "findings") gives "/projects/<id>/findings".
func apiPath(segments ...string) string {
	escaped := make([]string, len(segments))
	for i, s := range segments {
		escaped[i] = url.PathEscape(s)
	}
	return "/" + strings.Join(escaped, "/")
}

// request sends one HTTP request to the backend and prints the JSON response
// to stdout. See send for the error handling.
func request(method, path string, body any) error {
	raw, err := send(method, path, body)
	if err != nil {
		return err
	}
	printResponse(raw)
	return nil
}

// send sends one HTTP request to the backend and returns the response body
// for the caller to interpret. A non-nil body is sent as JSON. An error
// status becomes a cliError that echoes the backend's own error body (see
// backendError).
func send(method, path string, body any) ([]byte, error) {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("encoding request body: %w", err)
		}
		reader = bytes.NewReader(encoded)
	}

	endpoint := resolveBaseURL() + apiRoot + path
	req, err := http.NewRequest(method, endpoint, reader)
	if err != nil {
		return nil, fmt.Errorf("building request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiToken)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, failf("%s %s: %v", method, endpoint, transportError(err))
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}
	raw = bytes.TrimSpace(raw)

	if resp.StatusCode >= 400 {
		return nil, backendError(resp.StatusCode, raw)
	}
	return raw, nil
}

// backendError turns an error response into a cliError. A JSON body is the
// backend's own error ({"error": "<message>"}) and is echoed untouched. When
// the body is not JSON (an HTML page from a proxy, an empty reply) the
// status line and a condensed body stand in for it.
func backendError(status int, raw []byte) error {
	if json.Valid(raw) {
		return echoError(raw)
	}
	return failf("HTTP %d %s: %s", status, http.StatusText(status), summarize(raw))
}

// errorMessage extracts the "error" field from a JSON error body, for hosts
// other than the backend (the storage host). Empty when there is none.
func errorMessage(raw []byte) string {
	var body struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(raw, &body) != nil {
		return ""
	}
	return body.Error
}

// printResponse writes a backend response to stdout: pretty-printed when it
// is JSON, verbatim otherwise, nothing when it is empty.
func printResponse(raw []byte) {
	if pretty, isJSON := indentJSON(raw); isJSON {
		fmt.Println(pretty)
	} else if len(raw) > 0 {
		fmt.Println(string(raw))
	}
}

// printJSON writes a CLI-generated result to stdout in the same
// pretty-printed form as a backend response.
func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// transportError strips the repeated method and URL that net/http wraps
// around a connection failure.
func transportError(err error) error {
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return urlErr.Err
	}
	return err
}

// ---------------------------------------------------------------------------
// Transfers
// ---------------------------------------------------------------------------

// transferClient moves file contents to and from the presigned storage URLs
// the backend hands out. Those can take much longer than an API call, so it
// has its own, generous timeout.
var transferClient = &http.Client{Timeout: 10 * time.Minute}

// transfer performs a request against a presigned URL. The API token is
// deliberately not sent: the URL is the credential, and the host is not the
// backend. A non-2xx status is an error carrying the storage host's message
// (JSON "error" field, else the condensed body, e.g. the XML GCS returns).
func transfer(method, rawURL string, body io.Reader, size int64, contentType string) (*http.Response, error) {
	req, err := http.NewRequest(method, rawURL, body)
	if err != nil {
		return nil, fmt.Errorf("building %s request: %w", method, err)
	}
	if body != nil {
		req.ContentLength = size
		req.Header.Set("Content-Type", contentType)
	}

	resp, err := transferClient.Do(req)
	if err != nil {
		return nil, failf("%s %s: %v", method, redactURL(rawURL), transportError(err))
	}
	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		detail := errorMessage(raw)
		if detail == "" {
			detail = summarize(raw)
		}
		return nil, failf("%s %s: HTTP %d %s: %s", method, redactURL(rawURL),
			resp.StatusCode, http.StatusText(resp.StatusCode), detail)
	}
	return resp, nil
}

// redactURL drops the query string of a presigned URL, which is where its
// signature lives, so an error message never leaks a usable credential.
func redactURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "<invalid url>"
	}
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}

// indentJSON pretty-prints raw if it is valid JSON.
func indentJSON(raw []byte) (string, bool) {
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, raw, "", "  "); err != nil {
		return "", false
	}
	return pretty.String(), true
}

// summarize reduces a non-JSON body to one short line so an HTML or XML
// error page costs a few words instead of a screenful: the text of a <pre>
// block when there is one (Express error pages), else the visible text,
// truncated.
func summarize(raw []byte) string {
	text := string(raw)
	if start := strings.Index(text, "<pre>"); start >= 0 {
		text = text[start+len("<pre>"):]
		if end := strings.Index(text, "</pre>"); end >= 0 {
			text = text[:end]
		}
	}
	text = strings.Join(strings.Fields(stripTags(text)), " ")
	if text == "" {
		return "empty response"
	}
	if len(text) > 120 {
		text = text[:120] + "..."
	}
	return text
}

// stripTags removes anything between < and >.
func stripTags(s string) string {
	var out strings.Builder
	inTag := false
	for _, r := range s {
		switch {
		case r == '<':
			inTag = true
		case r == '>':
			inTag = false
			out.WriteByte(' ')
		case !inTag:
			out.WriteRune(r)
		}
	}
	return out.String()
}
