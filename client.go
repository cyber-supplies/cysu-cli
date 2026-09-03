package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// The Base URL path the CLI talks to
const baseURL = "https://blueprint-backend-670100724771.europe-west1.run.app/"

// apiRoot is the common prefix for every backend route (see commands.md).
const apiRoot = "/api"

// Returns the configured base URL
func resolveBaseURL() string {
	return strings.TrimRight(baseURL, "/")
}

// Performs an HTTP request against the backend and pretty-prints the
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

	printBody(raw)

	return nil
}

// Writes the response, pretty-printing it when it is valid JSON.
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
