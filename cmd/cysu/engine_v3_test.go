package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func engineFixture(t *testing.T, handler http.HandlerFunc) flagValues {
	t.Helper()
	s := httptest.NewServer(handler)
	t.Cleanup(s.Close)
	return flagValues{"engine": s.URL, "token": "usr_fixture-token"}
}

func engineInput(t *testing.T, value string) {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "input")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.WriteString(value); err != nil {
		t.Fatal(err)
	}
	if _, err = f.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	old := os.Stdin
	os.Stdin = f
	t.Cleanup(func() { os.Stdin = old; f.Close() })
}

func TestEnginePreflightNeverProvisions(t *testing.T) {
	for _, tc := range []struct {
		name       string
		status     int
		body, want string
	}{
		{"setup", 401, `{"setupRequired":true,"error":"setup"}`, "install.sh --token"},
		{"wrong-token", 401, `{"error":"Wrong engine token"}`, "Wrong engine token"},
		{"limited", 429, `{"error":"Retry in a minute"}`, "Retry in a minute"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			v := engineFixture(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != "GET" || r.URL.Path != "/api/status" {
					t.Errorf("unexpected request %s %s", r.Method, r.URL)
				}
				if r.Header.Get("Authorization") != "Bearer usr_fixture-token" {
					t.Error("missing bearer")
				}
				w.WriteHeader(tc.status)
				io.WriteString(w, tc.body)
			})
			err := enginePreflight(v, false)
			if err == nil || !strings.Contains(err.Error(), tc.want) || calls != 1 {
				t.Fatalf("calls=%d err=%v", calls, err)
			}
		})
	}
}

func TestEngineAuthCommandsReachMainDispatch(t *testing.T) {
	r, found := findResource("engine")
	if !found {
		t.Fatal("engine missing from main dispatch")
	}
	v := engineFixture(t, func(w http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case "/api/status":
			io.WriteString(w, `{"cysu":true,"llm":{"ready":true}}`)
		case "/api/llm/claude-auth":
			io.WriteString(w, `{"saved":true}`)
		default:
			t.Error(req.URL)
			w.WriteHeader(404)
		}
	})
	captureStdout(t, func() error {
		return r.run([]string{"auth-status", "--provider", "claude", "--engine", v["engine"], "--token", v["token"]})
	})
	for _, cmd := range engineAuthCommands() {
		if _, ok := r.lookup(cmd.name); !ok {
			t.Errorf("%s is unreachable from main", cmd.name)
		}
	}
	for _, name := range []string{"auth", "status", "list-workflows", "get-workflow", "create-workflow", "save-workflow", "clear-workflows", "run", "schedule", "unschedule", "list-runs", "get-run", "clear-runs", "cancel-run", "delete-workflow"} {
		if _, ok := r.lookup(name); !ok {
			t.Errorf("pre-existing command %s was removed", name)
		}
	}
	for _, name := range []string{"set-passphrase", "settings", "set-settings", "list-secrets", "set-secret", "delete-secret", "daybreak-status", "check-daybreak", "plan", "set-provider", "run-status", "rerun", "delete-run", "download-run", "get-log", "watch", "list-schedules", "get-schedule", "update-schedule", "pause-schedule", "resume-schedule"} {
		if _, ok := r.lookup(name); ok {
			t.Errorf("removed command %s remains registered", name)
		}
	}
}

func TestEngineAuthenticationFlows(t *testing.T) {
	for _, provider := range []string{"codex", "claude", "openrouter"} {
		t.Run(provider, func(t *testing.T) {
			engineInput(t, "private-key\n")
			calls := 0
			v := engineFixture(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				if provider == "openrouter" {
					if r.Method != "PUT" || r.URL.Path != "/api/llm/openrouter-auth" {
						t.Error(r.Method, r.URL)
					}
					var body map[string]string
					mustDecodeRequest(t, r, &body)
					if body["apiKey"] != "private-key" || body["model"] != "vendor/model" {
						t.Error("incorrect credentials body")
					}
					io.WriteString(w, `{"configured":true,"apiKey":"****","model":"vendor/model"}`)
				} else {
					if r.Method != "POST" || r.URL.Path != "/api/llm/"+provider+"-login" {
						t.Error(r.Method, r.URL)
					}
					state := map[string]string{"status": "waiting", "url": "https://login.example/", "id": "attempt"}
					if provider == "codex" {
						state["code"] = "ABCD"
					}
					json.NewEncoder(w).Encode(state)
				}
			})
			v["provider"] = provider
			if provider == "openrouter" {
				v["stdin"] = "true"
				v["model"] = "vendor/model"
				v["provided:model"] = "true"
			}
			out := captureStdout(t, func() error { return authEngine(v) })
			if calls != 1 || !json.Valid(out) || bytes.Contains(out, []byte("private-key")) {
				t.Fatalf("unexpected auth output/calls: %s %d", out, calls)
			}
		})
	}
}

func TestEngineAuthPreservesLegacyPassphraseOption(t *testing.T) {
	var trace bytes.Buffer
	oldOut, oldTracing := traceOut, tracingEnabled
	traceOut, tracingEnabled = &trace, true
	t.Cleanup(func() { traceOut, tracingEnabled = oldOut, oldTracing })
	calls := 0
	v := engineFixture(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			if r.Method != "PUT" || r.URL.Path != "/api/settings" {
				t.Error(r.Method, r.URL)
			}
			var body map[string]string
			mustDecodeRequest(t, r, &body)
			if len(body) != 1 || body["passkey"] != "private-passphrase" {
				t.Error("unexpected settings write")
			}
			io.WriteString(w, `{"passkey":"set"}`)
			return
		}
		if r.Method != "POST" || r.URL.Path != "/api/llm/codex-login" {
			t.Error(r.Method, r.URL)
		}
		io.WriteString(w, `{"status":"waiting","url":"https://login.example/","code":"ABCD"}`)
	})
	v["provider"], v["passphrase"], v["provided:passphrase"] = "codex", "private-passphrase", "true"
	captureStdout(t, func() error { return authEngine(v) })
	if calls != 2 || strings.Contains(trace.String(), "private-passphrase") {
		t.Fatal("legacy auth failed or leaked its passphrase")
	}
}

func TestEngineClaudeCodePreservesStateAndRedactsErrors(t *testing.T) {
	for _, status := range []int{200, 400} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			const code = "private-code#state-with-quote\""
			engineInput(t, code+"\n")
			var trace bytes.Buffer
			oldOut, oldTracing := traceOut, tracingEnabled
			traceOut = &trace
			tracingEnabled = true
			t.Cleanup(func() { traceOut = oldOut; tracingEnabled = oldTracing })
			v := engineFixture(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/llm/claude-login/code" {
					t.Error(r.URL)
				}
				var body map[string]string
				mustDecodeRequest(t, r, &body)
				if body["code"] != code || body["id"] != "attempt" {
					t.Error("code/state changed")
				}
				w.WriteHeader(status)
				json.NewEncoder(w).Encode(map[string]string{"status": "failed", "error": "rejected " + code})
			})
			v["stdin"], v["id"] = "true", "attempt"
			err := submitClaudeCode(v)
			if err == nil || strings.Contains(err.Error(), "private-code") || strings.Contains(trace.String(), "private-code") {
				t.Fatalf("secret leaked or error missing: %v %s", err, trace.String())
			}
		})
	}
}

func TestEngineOpenRouterModelUpdateAndAuthActions(t *testing.T) {
	v := engineFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/settings" && r.Method == "GET" {
			io.WriteString(w, `{"llm":{"mode":"none","codex":{"saved":true,"savedAt":"2026-11-14"}}}`)
			return
		}
		if r.Method == "PUT" {
			var body map[string]string
			mustDecodeRequest(t, r, &body)
			if len(body) != 1 || body["model"] != "vendor/new" {
				t.Error("model-only update sent a key")
			}
		}
		io.WriteString(w, `{"saved":true}`)
	})
	v["provider"], v["model"], v["provided:model"], v["model-only"] = "openrouter", "vendor/new", "true", "true"
	captureStdout(t, func() error { return authEngine(v) })
	for _, provider := range []string{"codex", "claude", "openrouter"} {
		v["provider"] = provider
		for _, name := range []string{"auth-status", "forget-auth", "login-status", "cancel-auth"} {
			cmd, _ := engineResource.lookup(name)
			if provider == "openrouter" && (name == "login-status" || name == "cancel-auth") {
				if cmd.run(v) == nil {
					t.Error("accepted browser flow for OpenRouter")
				}
				continue
			}
			out := captureStdout(t, func() error { return cmd.run(v) })
			if name == "auth-status" && provider == "codex" && !bytes.Contains(out, []byte(`"savedAt"`)) {
				t.Fatal("Codex saved status confused with active LLM mode")
			}
		}
	}
}

func TestEngineProviderIsSavedInWorkflowYAML(t *testing.T) {
	for _, value := range []any{nil, "", "auto", "typo", 1, map[string]any{"x": 1}} {
		if validateWorkflowSettings(&engineWorkflowSettings{Name: "test", Request: map[string]any{"provider": value}}) == nil {
			t.Fatalf("accepted provider %#v", value)
		}
	}
	if validateWorkflowSettings(&engineWorkflowSettings{Name: "test", Request: map[string]any{"stages": []any{map[string]any{"provider": "claude"}}}}) == nil {
		t.Fatal("accepted stage provider")
	}
	for _, provider := range []string{"codex", "claude", "openrouter", ""} {
		t.Run("provider="+provider, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			const id = "12345678-1234-1234-1234-123456789abc"
			request := map[string]any{"model": "existing-model", "stages": []any{}}
			if provider != "" {
				request["provider"] = provider
			}
			path, err := workflowSettingsPath("test")
			if err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(engineWorkflowSettings{ID: id, Name: "test", Request: request})
			if err := os.WriteFile(path, raw, 0600); err != nil {
				t.Fatal(err)
			}
			v := engineFixture(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "PATCH" || r.URL.Path != "/api/workspaces/"+id {
					t.Error(r.Method, r.URL)
				}
				var body engineWorkflowSettings
				mustDecodeRequest(t, r, &body)
				if body.Request["model"] != "existing-model" {
					t.Error("lost model")
				}
				if provider == "" {
					if _, exists := body.Request["provider"]; exists {
						t.Error("automatic selection must omit provider")
					}
				} else if body.Request["provider"] != provider {
					t.Error("provider changed during save")
				}
				json.NewEncoder(w).Encode(map[string]string{"id": id})
			})
			v["name"] = "test"
			captureStdout(t, func() error { return saveWorkflow(v) })
			saved, _, err := readWorkflowSettings(path)
			if err != nil {
				t.Fatal(err)
			}
			if saved.Request["provider"] != request["provider"] {
				t.Error("local workflow provider changed")
			}
		})
	}
}

func TestEngineTLSAndRedirects(t *testing.T) {
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, `{"ok":true}`) }))
	defer s.Close()
	v := flagValues{"engine": s.URL, "token": "usr_private"}
	if _, err := engineSend(v, "GET", "/api/status", nil); err == nil {
		t.Fatal("accepted untrusted IP certificate")
	}
	old := httpClient
	httpClient = s.Client()
	t.Cleanup(func() { httpClient = old })
	if _, err := engineSend(v, "GET", "/api/status", nil); err != nil {
		t.Fatal("trusted IP SAN failed:", err)
	}
	targetCalls := 0
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { targetCalls++ }))
	defer target.Close()
	v = engineFixture(t, func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 307) })
	if _, err := engineSend(v, "PUT", "/api/settings", map[string]string{"passkey": "private"}); err == nil || targetCalls != 0 {
		t.Fatal("followed credential redirect")
	}
}

func TestEngineRedactsMalformedCredentialError(t *testing.T) {
	const secret = "private-code\"with-quote"
	body, _ := json.Marshal(map[string]string{"code": secret})
	clean := engineRedactor(body, "usr_fixture")
	for _, raw := range []string{`{"error":"private-code\"with-quote`, `bad input: private-code"with-quote`} {
		if bytes.Contains(clean([]byte(raw)), []byte("private-code")) {
			t.Fatal("credential leaked in malformed error")
		}
	}
}
