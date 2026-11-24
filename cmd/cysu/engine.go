package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// enginePreflight checks authentication; provisioning belongs to the installer.
// Only starting a run requires ready credentials; passphrases are not synchronized here.
func enginePreflight(v flagValues, requireAuth bool) error {
	token, err := configuredToken(v)
	if err != nil {
		return err
	}
	if token == "" {
		return cliError{message: "engine commands require a Cyber Supplies user token: run cysu setup, set CYSU_TOKEN, or pass --token <token>", exit: exitUsage}
	}
	if !strings.HasPrefix(token, "usr_") {
		return cliError{message: "engine commands require a Cyber Supplies user token starting with usr_; LLM tokens cannot access engine commands", exit: exitUsage}
	}
	v["token"] = token
	raw, status, err := engineSendWithStatus(v, http.MethodGet, "/api/status", nil)
	if status == http.StatusUnauthorized {
		var rejected struct {
			SetupRequired bool `json:"setupRequired"`
		}
		if json.Unmarshal(raw, &rejected) != nil || !rejected.SetupRequired {
			return err
		}
		return failf("engine is not provisioned: on the engine host run install.sh --token <user-token> (optionally --ip <public-ip>); setup is not available over HTTP")
	}
	if err != nil {
		return err
	}
	var state struct {
		Cysu bool `json:"cysu"`
		LLM  struct {
			Ready bool `json:"ready"`
		} `json:"llm"`
	}
	if json.Unmarshal(raw, &state) != nil {
		return failf("engine returned invalid status")
	}
	if requireAuth && (!state.Cysu || !state.LLM.Ready) {
		return cliError{message: "engine authentication is incomplete: use cysu engine auth --provider codex|claude|openrouter (with --passphrase if the engine passphrase is not set); select the execution provider in your workflow", exit: exitUsage}
	}
	return nil
}

// workflowRefFlag identifies a saved workspace strictly by its UUID.
var workflowRefFlag = flagSpec{
	name: "workflow", placeholder: "id",
	usage: "workflow UUID; names and current are not supported", required: true,
}

var workflowIDPattern = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// validateWorkflowID rejects names, aliases, and partial IDs before preflight.
func validateWorkflowID(v flagValues) error {
	if !workflowIDPattern.MatchString(v["workflow"]) {
		return cliError{message: "--workflow must be a full workflow UUID; names and current are not supported", exit: exitUsage}
	}
	v["workflow"] = strings.ToLower(v["workflow"])
	return nil
}

// runRefFlag accepts the engine's full run IDs and unique short prefixes.
var runRefFlag = flagSpec{
	name: "run", placeholder: "id",
	usage: "run ID or unique prefix of at least 4 characters", required: true,
}

// workflowPath builds the route for a validated workflow ID.
func workflowPath(v flagValues) string {
	return "/api/workspaces/" + url.PathEscape(v["workflow"])
}

// validateRunObjective rejects empty or oversized objectives before
// authentication or any network requests.
func validateRunObjective(v flagValues) error {
	return validateEngineObjective(v["objective"], "run")
}

// validateEngineObjective shares objective validation between runs and schedules.
func validateEngineObjective(objective, action string) error {
	invalid := func(message string) error { return cliError{message: message, exit: exitUsage} }
	if !utf8.ValidString(objective) {
		return invalid(action + ": objective must be valid UTF-8")
	}
	if strings.TrimSpace(objective) == "" {
		return invalid(action + ": objective must not be empty")
	}
	// JavaScript measures the engine's limit in UTF-16 code units.
	length := 0
	for _, r := range objective {
		length++
		if r > 0xffff {
			length++
		}
	}
	if length > 100000 {
		return invalid(action + ": objective must be at most 100000 characters (UTF-16 code units)")
	}
	return nil
}

// runWorkflow starts a workspace with this run's objective and returns the
// queued run without waiting for its stages to finish.
func runWorkflow(v flagValues) error {
	return engineRequest(v, http.MethodPost, workflowPath(v)+"/run", map[string]string{"objective": v["objective"]})
}

// deleteWorkflow deletes the remote workflow, then removes matching local
// checkouts by ID so renamed workflows and folders are handled correctly.
func deleteWorkflow(v flagValues) error {
	if _, err := engineSend(v, http.MethodDelete, workflowPath(v), nil); err != nil {
		return err
	}
	if err := removeLocalWorkflow(v["workflow"]); err != nil {
		return failf("workflow %s deleted from the engine, but local cleanup failed: %v", v["workflow"], err)
	}
	return printJSON(map[string]string{"deleted": v["workflow"]})
}

// engineResource exposes saved engine workspaces as workflows in the CLI.
// Preflight verifies the token; commands need no local encryption passphrase.
var engineResource = resource{
	name: "engine", title: "Engine",
	commands: append([]command{
		{
			name: "auth",
			flags: []flagSpec{
				authProviderFlag, secretStdinFlag,
				{name: "passphrase", placeholder: "passphrase", usage: "optional Cyber Supplies encryption passphrase to save during authentication"},
				{name: "model", placeholder: "provider/model", usage: "OpenRouter default model; omit to retain the saved model"},
				{name: "model-only", boolean: true, usage: "update only the OpenRouter model, keeping its saved key"},
			},
			validate: validateAuthEngine,
			run:      authEngine,
		},
		{
			name: "status",
			run: func(v flagValues) error {
				return engineRequest(v, http.MethodGet, "/api/status", nil)
			},
		},
		{
			name: "list-workflows",
			run:  func(v flagValues) error { return engineRequest(v, http.MethodGet, "/api/workspaces", nil) },
		},
		{
			name:  "get-workflow",
			flags: []flagSpec{workflowRefFlag},
			run:   getWorkflow,
		},
		{
			name: "create-workflow",
			flags: []flagSpec{
				{name: "name", placeholder: "name", usage: "new workflow name and local folder", required: true},
			},
			validate: validateWorkflowCreate,
			run:      createWorkflow,
		},
		{
			name: "save-workflow",
			flags: []flagSpec{
				{name: "name", placeholder: "name", usage: "local workflow folder containing settings.yaml", required: true},
			},
			validate: validateWorkflowSave,
			run:      saveWorkflow,
		},
		{
			name:  "clear-workflows",
			local: true,
			run:   func(flagValues) error { return clearEngineDirectory("workflows") },
		},
		{
			name: "run",
			flags: []flagSpec{workflowRefFlag,
				{name: "objective", placeholder: "text", usage: "objective for this run", required: true},
			},
			validate: validateRunObjective,
			run:      runWorkflow,
		},
		{
			name:     "schedule",
			flags:    engineScheduleFlags(),
			validate: validateEngineSchedule,
			run:      scheduleWorkflow,
		},
		{
			name: "unschedule",
			flags: []flagSpec{
				{name: "schedule", placeholder: "id", usage: "full schedule UUID returned by schedule", required: true},
			},
			validate: validateScheduleID,
			run:      unscheduleWorkflow,
		},
		{
			name: "list-runs",
			run: func(v flagValues) error {
				return engineRequest(v, http.MethodGet, "/api/runs", nil)
			},
		},
		{
			name:  "get-run",
			flags: []flagSpec{runRefFlag},
			run:   getRun,
		},
		{
			name:  "clear-runs",
			local: true,
			run:   func(flagValues) error { return clearRuns() },
		},
		{
			name:  "cancel-run",
			flags: []flagSpec{runRefFlag},
			run: func(v flagValues) error {
				return engineRequest(v, http.MethodPost, "/api/runs/"+url.PathEscape(v["run"])+"/cancel", nil)
			},
		},
		{
			name:  "delete-workflow",
			flags: []flagSpec{workflowRefFlag},
			run:   deleteWorkflow,
		},
	}, engineAuthCommands()...),
}

// engineRequest sends JSON to the selected engine and prints its response.
func engineRequest(v flagValues, method, path string, body any) error {
	raw, err := engineSend(v, method, path, body)
	if err != nil {
		return err
	}
	printResponse(raw)
	return nil
}

// engineSend returns a response for commands that need to inspect it first.
func engineSend(v flagValues, method, path string, body any) ([]byte, error) {
	raw, _, err := engineSendWithStatus(v, method, path, body)
	return raw, err
}

// engineSendWithStatus also returns the HTTP status so preflight can recognize
// the engine's first-setup challenge without parsing a masked settings token.
func engineSendWithStatus(v flagValues, method, path string, body any) ([]byte, int, error) {
	var encoded []byte
	var reader io.Reader
	if body != nil {
		var err error
		encoded, err = json.Marshal(body)
		if err != nil {
			return nil, 0, fmt.Errorf("encoding engine request: %w", err)
		}
		reader = bytes.NewReader(encoded)
	}
	endpoint := v["engine"] + path
	req, err := http.NewRequest(method, endpoint, reader)
	if err != nil {
		return nil, 0, fmt.Errorf("building engine request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+v["token"])
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	safe := engineRedactor(encoded, v["token"])
	n := traceRequest(method, endpoint, req.Header, safe(encoded))
	start := time.Now()
	// Never forward engine settings (including credentials) through redirects.
	client := *httpClient
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Do(req)
	if err != nil {
		cause := transportError(err)
		traceFailure(n, time.Since(start), cause)
		return nil, 0, failf("%s %s: %v", method, endpoint, cause)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		traceFailure(n, time.Since(start), err)
		return nil, 0, fmt.Errorf("reading engine response: %w", err)
	}
	traceResponse(n, resp.StatusCode, time.Since(start), safe(raw))
	if resp.StatusCode >= 300 {
		return raw, resp.StatusCode, backendError(resp.StatusCode, safe(raw))
	}
	if resp.StatusCode != http.StatusNoContent && !json.Valid(raw) {
		return nil, resp.StatusCode, failf("engine returned an invalid JSON response (HTTP %d)", resp.StatusCode)
	}
	if body != nil {
		raw = safe(raw)
	}
	return raw, resp.StatusCode, nil
}

// engineFlag supplies the engine address from an explicit flag, saved setup,
// or CYSU_ENGINE_URL when no address was saved.
var engineFlag = flagSpec{
	name: "engine", placeholder: "url",
	usage: "Engine URL (overrides saved setup and CYSU_ENGINE_URL)",
	fallback: func() (string, error) {
		cfg, err := readConfig()
		if err != nil {
			return "", err
		}
		if value := strings.TrimSpace(cfg.EngineURL); value != "" {
			return value, nil
		}
		return strings.TrimSpace(os.Getenv("CYSU_ENGINE_URL")), nil
	},
}

// validateEngineURL resolves and normalizes the address before an engine
// command runs, keeping invalid or missing configuration off the network.
func validateEngineURL(v flagValues) error {
	base := strings.TrimSpace(v["engine"])
	if base == "" {
		return cliError{message: "missing engine URL: run cysu setup, set CYSU_ENGINE_URL, or pass --engine <url>", exit: exitUsage}
	}
	u, err := url.Parse(base)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.Opaque != "" {
		return cliError{message: "invalid engine URL: use an absolute http:// or https:// URL", exit: exitUsage}
	}
	if u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(base, "#") {
		return cliError{message: "invalid engine URL: credentials, query parameters and fragments are not supported", exit: exitUsage}
	}
	v["engine"] = strings.TrimRight(base, "/")
	return nil
}
