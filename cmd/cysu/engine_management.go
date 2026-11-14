package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

var engineFileFlag = flagSpec{name: "file", placeholder: "path", usage: "JSON or YAML object containing only fields to change", required: true}
var engineScheduleRef = flagSpec{name: "schedule", placeholder: "id", usage: "schedule UUID", required: true}
var engineSecretName = flagSpec{name: "name", placeholder: "NAME", usage: "secret environment variable name", required: true}

func engineManagementCommands() []command {
	return []command{
		{name: "set-passphrase", flags: []flagSpec{secretStdinFlag}, run: setEnginePassphrase},
		{name: "auth-code", flags: []flagSpec{{name: "id", placeholder: "id", usage: "Claude login attempt ID", required: true}, secretStdinFlag}, run: submitClaudeCode},
		{name: "auth-status", flags: []flagSpec{authProviderFlag}, validate: validateAuthProvider, run: engineAuthAction(http.MethodGet, "auth")},
		{name: "login-status", flags: []flagSpec{authProviderFlag}, validate: validateAuthProvider, run: engineAuthAction(http.MethodGet, "login")},
		{name: "cancel-auth", flags: []flagSpec{authProviderFlag}, validate: validateAuthProvider, run: engineAuthAction(http.MethodDelete, "login")},
		{name: "forget-auth", flags: []flagSpec{authProviderFlag}, validate: validateAuthProvider, run: engineAuthAction(http.MethodDelete, "auth")},
		{name: "settings", run: engineRoute(http.MethodGet, "/api/settings")},
		{name: "set-settings", flags: []flagSpec{engineFileFlag}, run: updateEngineSettings},
		{name: "list-secrets", run: engineRoute(http.MethodGet, "/api/secrets")},
		{name: "set-secret", flags: []flagSpec{engineSecretName, secretStdinFlag}, validate: validateEngineSecretName, run: setEngineSecret},
		{name: "delete-secret", flags: []flagSpec{engineSecretName}, validate: validateEngineSecretName, run: func(v flagValues) error {
			return engineRequest(v, http.MethodDelete, "/api/secrets/"+url.PathEscape(v["name"]), nil)
		}},
		{name: "daybreak-status", run: engineRoute(http.MethodGet, "/api/daybreak/check")},
		{name: "check-daybreak", flags: []flagSpec{{name: "model", placeholder: "model", usage: "optional model to check; makes small billable model requests"}}, run: checkEngineDaybreak},
		{name: "plan", flags: []flagSpec{workflowRefFlag, {name: "objective", placeholder: "text", usage: "objective to preview without starting a run", required: true}}, validate: validateRunObjective, run: planEngineWorkflow},
		{name: "set-provider", flags: []flagSpec{workflowRefFlag, {name: "provider", placeholder: "provider", usage: "codex, claude, openrouter, or auto (removes the field)", required: true}}, validate: func(v flagValues) error {
			if v["provider"] == "auto" {
				return nil
			}
			return validateAuthProvider(v)
		}, run: setEngineWorkflowProvider},
		{name: "run-status", flags: []flagSpec{runRefFlag}, run: func(v flagValues) error { return engineRequest(v, http.MethodGet, engineRunPath(v), nil) }},
		{name: "rerun", flags: []flagSpec{runRefFlag}, run: func(v flagValues) error { return engineRequest(v, http.MethodPost, engineRunPath(v)+"/rerun", nil) }},
		{name: "delete-run", flags: []flagSpec{runRefFlag}, run: deleteEngineRun},
		{name: "download-run", flags: []flagSpec{runRefFlag, {name: "output", placeholder: "path.tar.gz", usage: "new archive file (never overwrites an existing file)", required: true}}, run: downloadEngineRun},
		{name: "get-log", flags: []flagSpec{runRefFlag, {name: "stage", placeholder: "index", usage: "zero-based stage index", required: true}}, validate: func(v flagValues) error {
			n, err := strconv.Atoi(v["stage"])
			if err != nil || n < 0 {
				return failf("--stage must be a nonnegative integer")
			}
			v["stage"] = strconv.Itoa(n)
			return nil
		}, run: getEngineLog},
		{name: "watch", flags: []flagSpec{runRefFlag, {name: "cursor", placeholder: "cursor", usage: "cursor from an earlier watch response"}, {name: "follow", boolean: true, usage: "stream JSON feed responses until finished; Ctrl-C stops watching"}}, run: watchEngineRun},
		{name: "list-schedules", run: engineRoute(http.MethodGet, "/api/schedules")},
		{name: "get-schedule", flags: []flagSpec{engineScheduleRef}, validate: validateScheduleID, run: engineScheduleAction(http.MethodGet, "")},
		{name: "update-schedule", flags: []flagSpec{engineScheduleRef, engineFileFlag}, validate: validateScheduleID, run: updateEngineSchedule},
		{name: "pause-schedule", flags: []flagSpec{engineScheduleRef}, validate: validateScheduleID, run: engineScheduleAction(http.MethodPost, "/pause")},
		{name: "resume-schedule", flags: []flagSpec{engineScheduleRef}, validate: validateScheduleID, run: engineScheduleAction(http.MethodPost, "/resume")},
	}
}

func engineRoute(method, path string) func(flagValues) error {
	return func(v flagValues) error { return engineRequest(v, method, path, nil) }
}

func engineRunPath(v flagValues) string { return "/api/runs/" + url.PathEscape(v["run"]) }

func engineScheduleAction(method, suffix string) func(flagValues) error {
	return func(v flagValues) error {
		return engineRequest(v, method, "/api/schedules/"+url.PathEscape(v["schedule"])+suffix, nil)
	}
}

// Explicit allowlists prevent accidental resubmission of masked settings,
// server-managed metadata, and installer-only tokens. Do not echo file input.
func readEnginePatch(path string, allowed ...string) (map[string]any, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var body map[string]any
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	if decoder.Decode(&body) != nil || len(body) == 0 {
		return nil, failf("file must contain a nonempty JSON or YAML object")
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return nil, failf("file must contain exactly one document")
	}
	for key := range body {
		found := false
		for _, field := range allowed {
			if key == field {
				found = true
				break
			}
		}
		if !found {
			return nil, failf("file contains an unsupported field; allowed fields: %s", strings.Join(allowed, ", "))
		}
	}
	return body, nil
}

func updateEngineSettings(v flagValues) error {
	body, err := readEnginePatch(v["file"], "passkey", "llm", "globalProfile", "agentToken", "engineLinked")
	if err != nil {
		return err
	}
	return engineRequest(v, http.MethodPut, "/api/settings", body)
}

func updateEngineSchedule(v flagValues) error {
	body, err := readEnginePatch(v["file"], "name", "workspaceId", "objective", "when", "tz", "enabled")
	if err != nil {
		return err
	}
	return engineRequest(v, http.MethodPatch, "/api/schedules/"+url.PathEscape(v["schedule"]), body)
}

func validateEngineSecretName(v flagValues) error {
	if !regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,63}$`).MatchString(v["name"]) {
		return failf("secret name must match [A-Z][A-Z0-9_]{0,63}")
	}
	return nil
}

func setEngineSecret(v flagValues) error {
	value, err := readEngineSecret(v, "Secret value")
	if err != nil {
		return err
	}
	return engineRequest(v, http.MethodPut, "/api/secrets/"+url.PathEscape(v["name"]), map[string]string{"value": value})
}

func checkEngineDaybreak(v flagValues) error {
	body := map[string]string{}
	if v.provided("model") {
		body["model"] = v["model"]
	}
	raw, _, err := engineExchange(v, http.MethodPost, "/api/daybreak/check", body, 10*time.Minute, "application/json", nil)
	if err != nil {
		return err
	}
	printResponse(raw)
	return nil
}

func loadEngineWorkflow(v flagValues) (engineWorkflowSettings, error) {
	var ws engineWorkflowSettings
	raw, err := engineSend(v, http.MethodGet, workflowPath(v), nil)
	if err != nil {
		return ws, err
	}
	if json.Unmarshal(raw, &ws) != nil || ws.Request == nil {
		return ws, failf("engine returned an invalid workflow")
	}
	return ws, nil
}

func planEngineWorkflow(v flagValues) error {
	ws, err := loadEngineWorkflow(v)
	if err != nil {
		return err
	}
	ws.Request["objective"] = v["objective"]
	return engineRequest(v, http.MethodPost, "/api/runs/plan", ws.Request)
}

func setEngineWorkflowProvider(v flagValues) error {
	ws, err := loadEngineWorkflow(v)
	if err != nil {
		return err
	}
	if v["provider"] == "auto" {
		delete(ws.Request, "provider")
	} else {
		ws.Request["provider"] = v["provider"]
	}
	return engineRequest(v, http.MethodPatch, workflowPath(v), map[string]any{"request": ws.Request})
}

func deleteEngineRun(v flagValues) error {
	if _, err := engineSend(v, http.MethodDelete, engineRunPath(v), nil); err != nil {
		return err
	}
	return printJSON(map[string]string{"deleted": v["run"]})
}

func getEngineLog(v flagValues) error {
	raw, _, err := engineExchange(v, http.MethodGet, engineRunPath(v)+"/stages/"+v["stage"]+"/log", nil, 30*time.Second, "text/plain", nil)
	if err != nil {
		return err
	}
	return printJSON(map[string]string{"run": v["run"], "stage": v["stage"], "log": string(raw)})
}

// Stream to a private temporary file, then publish without replacing any
// existing destination, including symlinks. Failed downloads leave no artifact.
func downloadEngineRun(v flagValues) error {
	path, err := filepath.Abs(v["output"])
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".cysu-run-*.tar.gz")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, _, sendErr := engineExchange(v, http.MethodGet, engineRunPath(v)+"/download", nil, 10*time.Minute, "application/gzip", f)
	closeErr := f.Close()
	if sendErr != nil {
		return sendErr
	}
	if closeErr != nil {
		return closeErr
	}
	if err := os.Link(f.Name(), path); err != nil {
		return err
	}
	return printJSON(map[string]string{"run": v["run"], "path": path})
}

func watchEngineRun(v flagValues) error {
	cursor := v["cursor"]
	for {
		query := url.Values{"cursor": {cursor}}
		if v.enabled("follow") {
			query.Set("wait", "25000")
		}
		raw, err := engineSend(v, http.MethodGet, engineRunPath(v)+"/feed?"+query.Encode(), nil)
		if err != nil {
			return err
		}
		var feed struct {
			Cursor string            `json:"cursor"`
			Done   bool              `json:"done"`
			Items  []json.RawMessage `json:"items"`
		}
		if json.Unmarshal(raw, &feed) != nil {
			return failf("engine returned invalid run feed")
		}
		// Compact JSON lines make --follow consumable by streaming tools.
		var compact bytes.Buffer
		if err := json.Compact(&compact, raw); err != nil {
			return err
		}
		if _, err := os.Stdout.Write(append(compact.Bytes(), '\n')); err != nil {
			return err
		}
		if !v.enabled("follow") || (feed.Done && len(feed.Items) == 0) {
			return nil
		}
		if len(feed.Items) > 0 && feed.Cursor == cursor {
			return failf("engine feed cursor did not advance")
		}
		cursor = feed.Cursor
	}
}
