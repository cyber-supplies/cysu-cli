package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// baseURL is the base URL path the CLI talks to. Override with the
// CYSU_API_URL environment variable.
const baseURL = "https://blueprint-backend-670100724771.europe-west1.run.app/"

// apiRoot is the common prefix for every backend route (see commands.md).
const apiRoot = "/api"

// resolveBaseURL returns the configured base URL, honouring CYSU_API_URL.
func resolveBaseURL() string {
	if v := strings.TrimSpace(os.Getenv("CYSU_API_URL")); v != "" {
		return strings.TrimRight(v, "/")
	}
	return strings.TrimRight(baseURL, "/")
}

// request performs an HTTP request against the backend and pretty-prints the
// JSON response body to stdout. A non-nil body is JSON-encoded automatically.
func request(method, path string, body any) error {
	var reader io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encoding request body: %w", err)
		}
		reader = bytes.NewReader(buf)
	}

	url := resolveBaseURL() + apiRoot + path
	req, err := http.NewRequest(method, url, reader)
	if err != nil {
		return fmt.Errorf("building request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, url, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%s %s: %s\n%s", method, url, resp.Status, strings.TrimSpace(string(raw)))
	}

	printBody(raw)
	return nil
}

// printBody writes the response, pretty-printing it when it is valid JSON.
func printBody(raw []byte) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return
	}
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, raw, "", "  "); err != nil {
		fmt.Println(string(raw))
		return
	}
	fmt.Println(pretty.String())
}
