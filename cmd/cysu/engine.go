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
	"strconv"
	"strings"
	"time"
)

// enginePreflight checks engine authentication and fills a missing user token.
// Only auth bypasses the readiness gate; passphrases are never synchronized here.
func enginePreflight(v flagValues, requireAuth bool) error {
	token := strings.TrimSpace(v["token"])
	if token == "" {
		token = strings.TrimSpace(os.Getenv("CYSU_TOKEN"))
	}
	if token == "" {
		return cliError{message: "engine commands require a Cyber Supplies user token: set CYSU_TOKEN or pass --token <token>", exit: exitUsage}
	}
	if !strings.HasPrefix(token, "usr_") {
		return cliError{message: "engine commands require a Cyber Supplies user token starting with usr_; LLM tokens cannot access engine commands", exit: exitUsage}
	}
	raw, err := engineSend(v, http.MethodGet, "/api/settings", nil)
	if err != nil {
		return err
	}
	var settings struct {
		Token   *string `json:"token"`
		Passkey string  `json:"passkey"`
		LLM     struct {
			Mode  string `json:"mode"`
			Codex struct {
				Saved bool `json:"saved"`
			} `json:"codex"`
		} `json:"llm"`
	}
	if err := json.Unmarshal(raw, &settings); err != nil || settings.Token == nil {
		return failf("engine returned invalid credential settings")
	}
	if requireAuth && (settings.Passkey == "" || settings.LLM.Mode != "codex" || !settings.LLM.Codex.Saved) {
		return cliError{message: "engine authentication is incomplete: run cysu engine auth --passphrase <passphrase> and complete the Codex sign-in before using engine commands", exit: exitUsage}
	}
	credentials := map[string]string{}
	if *settings.Token == "" {
		credentials["token"] = token
	}
	if len(credentials) == 0 {
		return nil
	}
	_, err = engineSend(v, http.MethodPut, "/api/settings", credentials)
	return err
}

// authEngine explicitly saves the supplied passphrase and initiates Codex
// device login. Return the URL/code as JSON, leaving the engine to finish
// login in the background after the user signs in through the browser.
func authEngine(v flagValues) error {
	if _, err := engineSend(v, http.MethodPut, "/api/settings", map[string]string{"passkey": v["passphrase"]}); err != nil {
		return err
	}
	raw, err := engineSend(v, http.MethodPost, "/api/llm/codex-login", nil)
	if err != nil {
		return err
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		var login struct {
			Status string `json:"status"`
			URL    string `json:"url"`
			Code   string `json:"code"`
			Error  string `json:"error"`
		}
		if err := json.Unmarshal(raw, &login); err != nil {
			return failf("engine returned invalid Codex login state")
		}
		switch login.Status {
		case "failed", "cancelled":
			if login.Error != "" {
				return failf("Codex login %s: %s", login.Status, login.Error)
			}
			return failf("Codex login %s", login.Status)
		case "succeeded":
			printResponse(raw)
			return nil
		case "waiting":
			if login.URL != "" && login.Code != "" {
				printResponse(raw)
				return nil
			}
		case "starting":
		default:
			return failf("engine returned invalid Codex login state")
		}
		if !time.Now().Before(deadline) {
			return failf("Codex login has not provided a sign-in code yet; retry cysu engine auth --passphrase <passphrase>")
		}
		time.Sleep(500 * time.Millisecond)
		raw, err = engineSend(v, http.MethodGet, "/api/llm/codex-login", nil)
		if err != nil {
			return err
		}
	}
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

// runWorkflow starts a workspace and returns the queued run without waiting
// for its stages to finish.
func runWorkflow(v flagValues) error {
	return engineRequest(v, http.MethodPost, workflowPath(v)+"/run", nil)
}

// validateStageEvents checks and normalizes zero-based stage and paging values.
func validateStageEvents(v flagValues) error {
	for _, name := range []string{"stage", "after", "limit"} {
		if name != "stage" && !v.provided(name) {
			continue
		}
		n, err := strconv.ParseUint(v[name], 10, 53)
		if err != nil {
			return cliError{message: "--" + name + " must be a nonnegative integer", exit: exitUsage}
		}
		if name == "limit" && (n < 1 || n > 5000) {
			return cliError{message: "--limit must be between 1 and 5000", exit: exitUsage}
		}
		v[name] = strconv.FormatUint(n, 10)
	}
	return nil
}

// getStage returns a page of raw stage events and the next sequence pointer.
func getStage(v flagValues) error {
	query := url.Values{}
	for _, name := range []string{"after", "limit"} {
		if v.provided(name) {
			query.Set(name, v[name])
		}
	}
	path := "/api/runs/" + url.PathEscape(v["run"]) + "/stages/" + v["stage"] + "/events"
	if len(query) > 0 {
		path += "?" + query.Encode()
	}
	return engineRequest(v, http.MethodGet, path, nil)
}

// deleteWorkflow prints a JSON acknowledgment for the engine's empty 204 reply.
func deleteWorkflow(v flagValues) error {
	if _, err := engineSend(v, http.MethodDelete, workflowPath(v), nil); err != nil {
		return err
	}
	return printJSON(map[string]string{"deleted": v["workflow"]})
}

// validateWorkflowEdit requires at least one workflow setting to change.
func validateWorkflowEdit(v flagValues) error {
	if err := validateWorkflow(v); err != nil {
		return err
	}
	for _, f := range workflowFlags(false) {
		if v.provided(f.name) {
			return nil
		}
	}
	return cliError{message: "edit-workflow: provide at least one workflow setting to change", exit: exitUsage}
}

// editWorkflow merges explicitly supplied settings into the existing request.
// The API replaces the whole request, so unspecified and advanced fields must
// survive the update. The resolved ID pins both operations to the same workspace.
func editWorkflow(v flagValues) error {
	body := map[string]any{}
	if v.provided("name") {
		body["name"] = v["name"]
	}
	changeRequest := false
	for _, f := range workflowFlags(false) {
		if f.name != "name" && v.provided(f.name) {
			changeRequest = true
		}
	}
	if !changeRequest {
		return engineRequest(v, http.MethodPatch, workflowPath(v), body)
	}
	raw, err := engineSend(v, http.MethodGet, workflowPath(v), nil)
	if err != nil {
		return err
	}
	var current struct {
		ID      string                     `json:"id"`
		Request map[string]json.RawMessage `json:"request"`
	}
	if err := json.Unmarshal(raw, &current); err != nil || current.Request == nil || current.ID == "" {
		return failf("engine returned an invalid workflow")
	}
	set := func(key string, value any) {
		encoded, _ := json.Marshal(value)
		current.Request[key] = encoded
	}
	for flag, field := range map[string]string{
		"prompt": "prompt", "global-profile": "globalProfile", "model": "model",
		"fallback-model": "fallbackModel", "reasoning-effort": "reasoningEffort",
	} {
		if v.provided(flag) {
			set(field, v[flag])
		}
	}
	settings := workflowBody(v)["request"].(map[string]any)
	if v.provided("profile") {
		delete(current.Request, "fromTags")
		set("stages", settings["stages"])
	}
	if v.provided("stop-on-failure") {
		set("continueOnFailure", !v.enabled("stop-on-failure"))
	}
	body["request"] = current.Request
	return engineRequest(v, http.MethodPatch, "/api/workspaces/"+url.PathEscape(current.ID), body)
}

// engineResource exposes saved engine workspaces as workflows in the CLI.
// A preflight supplies missing credentials; commands need no local master key.
var engineResource = resource{
	name: "engine", title: "Engine",
	commands: []command{
		{
			name: "auth",
			flags: []flagSpec{
				{name: "passphrase", placeholder: "passphrase", usage: "passphrase to save in the engine before starting Codex sign-in", required: true},
			},
			run: authEngine,
		},
		{
			name: "get-status",
			run: func(v flagValues) error {
				return engineRequest(v, http.MethodGet, "/api/status", nil)
			},
		},
		{
			name: "get-workflows",
			run:  func(v flagValues) error { return engineRequest(v, http.MethodGet, "/api/workspaces", nil) },
		},
		{
			name:  "get-workflow",
			flags: []flagSpec{workflowRefFlag},
			run:   func(v flagValues) error { return engineRequest(v, http.MethodGet, workflowPath(v), nil) },
		},
		{
			name:     "create-workflow",
			flags:    workflowFlags(true),
			validate: validateWorkflow,
			run: func(v flagValues) error {
				return engineRequest(v, http.MethodPost, "/api/workspaces", workflowBody(v))
			},
		},
		{
			name:  "run",
			flags: []flagSpec{workflowRefFlag},
			run:   runWorkflow,
		},
		{
			name:  "run-workflow",
			flags: []flagSpec{workflowRefFlag},
			run:   runWorkflow,
		},
		{
			name: "list-runs",
			run: func(v flagValues) error {
				return engineRequest(v, http.MethodGet, "/api/runs", nil)
			},
		},
		{
			name:  "get-run-status",
			flags: []flagSpec{runRefFlag},
			run: func(v flagValues) error {
				return engineRequest(v, http.MethodGet, "/api/runs/"+url.PathEscape(v["run"]), nil)
			},
		},
		{
			name: "get-run",
			flags: []flagSpec{runRefFlag,
				{name: "cursor", placeholder: "cursor", usage: "cursor from the previous response; omit to read from the beginning"},
			},
			run: func(v flagValues) error {
				query := url.Values{"cursor": {v["cursor"]}}
				return engineRequest(v, http.MethodGet, "/api/runs/"+url.PathEscape(v["run"])+"/feed?"+query.Encode(), nil)
			},
		},
		{
			name: "get-stage",
			flags: []flagSpec{runRefFlag,
				{name: "stage", placeholder: "index", usage: "zero-based stage index (first stage is 0)", required: true},
				{name: "after", placeholder: "sequence", usage: "return events after this sequence; use next from the previous response (default 0)"},
				{name: "limit", placeholder: "count", usage: "maximum events to return, 1–5000 (default 500)"},
			},
			validate: validateStageEvents,
			run:      getStage,
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
		{
			name:     "edit-workflow",
			flags:    append([]flagSpec{workflowRefFlag}, workflowFlags(false)...),
			validate: validateWorkflowEdit,
			run:      editWorkflow,
		},
	},
}

// workflowFlags shares run settings between workflow creation and editing.
// During editing an explicitly empty value clears that setting.
func workflowFlags(create bool) []flagSpec {
	flags := []flagSpec{
		{name: "name", placeholder: "name", usage: "unique workflow name; creates and selects the workflow", required: true},
		{name: "prompt", placeholder: "text", usage: "objective given to every stage; omit for a draft"},
		{name: "profile", placeholder: "id", usage: "stage profile, in execution order", multi: true},
		{name: "global-profile", placeholder: "id", usage: "global profile installed for every stage"},
		{name: "model", placeholder: "model", usage: "default model for every stage"},
		{name: "fallback-model", placeholder: "model", usage: "retry a failed stage with this model"},
		{name: "reasoning-effort", placeholder: "effort", usage: "none, minimal, low, medium, high, xhigh, max, or ultra"},
		{name: "stop-on-failure", usage: "skip remaining stages after a stage fails", boolean: true},
	}
	flags[0].required = create
	if !create {
		flags[0].usage = "replacement workflow name"
		flags[1].usage = "replacement objective; an empty value clears it"
		flags[2].usage = "replace stage profiles, in execution order"
		for i := range flags {
			if flags[i].name == "stop-on-failure" {
				flags[i].usage = "stop after failure; use --stop-on-failure=false to continue"
			}
		}
	}
	return flags
}

// validateWorkflow checks flag combinations before creating a saved setup.
// A draft may omit both its objective and its stage profiles.
func validateWorkflow(v flagValues) error {
	invalid := func(message string) error { return cliError{message: message, exit: exitUsage} }
	if effort := v["reasoning-effort"]; effort != "" {
		switch effort {
		case "none", "minimal", "low", "medium", "high", "xhigh", "max", "ultra":
		default:
			return invalid("--reasoning-effort must be none, minimal, low, medium, high, xhigh, max, or ultra")
		}
	}
	if len(v.list("profile")) > 20 {
		return invalid("at most 20 --profile stages are supported")
	}
	return nil
}

// workflowBody translates CLI flags into the engine's workspace schema.
func workflowBody(v flagValues) map[string]any {
	request := map[string]any{"prompt": v["prompt"]}
	stages := make([]map[string]string, 0, len(v.list("profile")))
	for _, profile := range v.list("profile") {
		stages = append(stages, map[string]string{"profile": profile})
	}
	request["stages"] = stages
	for flag, field := range map[string]string{
		"global-profile": "globalProfile", "model": "model",
		"fallback-model": "fallbackModel", "reasoning-effort": "reasoningEffort",
	} {
		if v[flag] != "" {
			request[field] = v[flag]
		}
	}
	if v.enabled("stop-on-failure") {
		request["continueOnFailure"] = false
	}
	return map[string]any{"name": v["name"], "request": request}
}

// engineRequest sends JSON to the selected engine without gateway credentials
// or an Origin header, then prints its JSON response unchanged in structure.
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
	var encoded []byte
	var reader io.Reader
	if body != nil {
		var err error
		encoded, err = json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("encoding engine request: %w", err)
		}
		reader = bytes.NewReader(encoded)
	}
	endpoint := v["engine"] + path
	req, err := http.NewRequest(method, endpoint, reader)
	if err != nil {
		return nil, fmt.Errorf("building engine request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	n := traceRequest(method, endpoint, req.Header, encoded)
	start := time.Now()
	// Never forward engine settings (including credentials) through redirects.
	client := *httpClient
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Do(req)
	if err != nil {
		cause := transportError(err)
		traceFailure(n, time.Since(start), cause)
		return nil, failf("%s %s: %v", method, endpoint, cause)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		traceFailure(n, time.Since(start), err)
		return nil, fmt.Errorf("reading engine response: %w", err)
	}
	traceResponse(n, resp.StatusCode, time.Since(start), raw)
	if resp.StatusCode >= 300 {
		return nil, backendError(resp.StatusCode, raw)
	}
	if resp.StatusCode != http.StatusNoContent && !json.Valid(raw) {
		return nil, failf("engine returned an invalid JSON response (HTTP %d)", resp.StatusCode)
	}
	return raw, nil
}

// engineFlag supplies the engine address for each engine command. There is
// deliberately no default: callers must choose an engine explicitly.
var engineFlag = flagSpec{
	name: "engine", placeholder: "url",
	usage: "Engine URL (overrides CYSU_ENGINE_URL; one is required)",
	fallback: func() (string, error) {
		return os.Getenv("CYSU_ENGINE_URL"), nil
	},
}

// validateEngineURL resolves and normalizes the address before an engine
// command runs, keeping invalid or missing configuration off the network.
func validateEngineURL(v flagValues) error {
	base := strings.TrimSpace(v["engine"])
	if base == "" {
		return cliError{message: "missing engine URL: set CYSU_ENGINE_URL or pass --engine <url>", exit: exitUsage}
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
