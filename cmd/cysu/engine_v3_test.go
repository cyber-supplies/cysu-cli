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

func TestEngineV3CommandsReachMainDispatch(t *testing.T) {
	r, found := findResource("engine")
	if !found {
		t.Fatal("engine missing from main dispatch")
	}
	v := engineFixture(t, func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/api/status" && req.URL.Path != "/api/settings" {
			t.Error(req.URL)
		}
		io.WriteString(w, `{"cysu":true,"llm":{"ready":true,"provider":"claude"}}`)
	})
	captureStdout(t, func() error { return r.run([]string{"settings", "--engine", v["engine"], "--token", v["token"]}) })
	for _, cmd := range engineManagementCommands() {
		if _, ok := r.lookup(cmd.name); !ok {
			t.Errorf("%s is unreachable from main", cmd.name)
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

func TestEngineSecretAndPassphraseAreIndependent(t *testing.T) {
	for _, command := range []string{"set-passphrase", "set-secret"} {
		t.Run(command, func(t *testing.T) {
			engineInput(t, "  private value  \n")
			var trace bytes.Buffer
			oldOut, oldTracing := traceOut, tracingEnabled
			traceOut = &trace
			tracingEnabled = true
			t.Cleanup(func() { traceOut = oldOut; tracingEnabled = oldTracing })
			v := engineFixture(t, func(w http.ResponseWriter, r *http.Request) {
				var body map[string]string
				mustDecodeRequest(t, r, &body)
				field, path := "passkey", "/api/settings"
				if command == "set-secret" {
					field, path = "value", "/api/secrets/TEST_SECRET"
				}
				if r.Method != "PUT" || r.URL.Path != path || body[field] != "  private value  " || len(body) != 1 {
					t.Error("unexpected write")
				}
				io.WriteString(w, `{"saved":true}`)
			})
			v["stdin"], v["name"] = "true", "TEST_SECRET"
			cmd, _ := engineResource.lookup(command)
			captureStdout(t, func() error { return cmd.run(v) })
			if strings.Contains(trace.String(), "private value") {
				t.Fatal("secret leaked to trace")
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

func TestEngineProviderValidationAndAutoPreservesRequest(t *testing.T) {
	for _, value := range []any{nil, "", "auto", "typo", 1, map[string]any{"x": 1}} {
		if validateWorkflowSettings(&engineWorkflowSettings{Name: "test", Request: map[string]any{"provider": value}}) == nil {
			t.Fatalf("accepted provider %#v", value)
		}
	}
	if validateWorkflowSettings(&engineWorkflowSettings{Name: "test", Request: map[string]any{"stages": []any{map[string]any{"provider": "claude"}}}}) == nil {
		t.Fatal("accepted stage provider")
	}
	for _, choice := range []string{"codex", "claude", "openrouter", "auto"} {
		t.Run(choice, func(t *testing.T) {
			v := engineFixture(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "PATCH" {
					var body struct{ Request map[string]any }
					mustDecodeRequest(t, r, &body)
					if body.Request["model"] != "existing-model" {
						t.Error("lost model")
					}
					if choice == "auto" {
						if _, exists := body.Request["provider"]; exists {
							t.Error("auto must remove provider")
						}
					} else if body.Request["provider"] != choice {
						t.Error("provider not saved")
					}
				}
				io.WriteString(w, `{"id":"12345678-1234-1234-1234-123456789abc","name":"test","request":{"provider":"claude","model":"existing-model","stages":[]}}`)
			})
			v["workflow"], v["provider"] = "12345678-1234-1234-1234-123456789abc", choice
			captureStdout(t, func() error { return setEngineWorkflowProvider(v) })
		})
	}
}

func TestEngineClaudeTranscriptAcrossPages(t *testing.T) {
	events := []string{
		`{"seq":1,"type":"assistant","session_id":"s","message":{"content":[{"type":"text","text":"Inspecting"},{"type":"tool_use","id":"tool-1","name":"Bash","input":{"command":"echo hello"}}]}}`,
		`{"seq":2,"type":"user","session_id":"s","message":{"content":[{"type":"tool_result","tool_use_id":"tool-1","content":"hello"}]}}`,
		`{"seq":3,"type":"assistant","session_id":"s","parent_tool_use_id":"agent-1","message":{"content":[{"type":"text","text":"Nested answer"}]}}`,
		`{"seq":4,"type":"assistant","session_id":"s","message":{"content":[{"type":"text","text":"Final answer"}]}}`,
		`{"seq":5,"type":"result","session_id":"s","subtype":"success","result":"Final answer"}`,
		`{"seq":6,"type":"result","session_id":"s","is_error":true,"subtype":"error_during_execution","errors":["permission denied"]}`,
	}
	v := engineFixture(t, func(w http.ResponseWriter, r *http.Request) {
		var n int
		fmt.Sscan(r.URL.Query().Get("after"), &n)
		if n >= len(events) {
			t.Error("snapshot overrun")
			io.WriteString(w, `{"events":[],"next":6}`)
			return
		}
		fmt.Fprintf(w, `{"events":[%s],"next":%d}`, events[n], n+1)
	})
	var out bytes.Buffer
	index, count := 0, int64(len(events))
	n, err := writeStageEvents(v, "run", engineRunStage{Index: &index, EventCount: &count}, &out)
	if err != nil || n != len(events) {
		t.Fatalf("%d %v", n, err)
	}
	for _, want := range []string{"Inspecting", "Tool Bash (tool-1) result", "hello", "Nested agent (agent-1)", "permission denied"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %s in %s", want, out.String())
		}
	}
	if strings.Count(out.String(), "Final answer") != 1 {
		t.Error("duplicated final answer")
	}
}

func TestEngineCodexTranscriptStillCoalesces(t *testing.T) {
	var out bytes.Buffer
	tr := engineTranscript{dst: &out}
	for _, event := range []string{
		`{"type":"item.started","item":{"id":"1","type":"command_execution","command":"echo test"}}`,
		`{"type":"item.completed","item":{"id":"1","type":"command_execution","command":"echo test","aggregated_output":"test","exit_code":0}}`,
		`{"type":"item.completed","item":{"id":"2","type":"agent_message","text":"Done"}}`,
	} {
		if err := tr.write(json.RawMessage(event)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tr.flush(); err != nil {
		t.Fatal(err)
	}
	if strings.Count(out.String(), "echo test") != 1 || !strings.Contains(out.String(), "Done") {
		t.Fatal(out.String())
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

func TestEngineArchivesLogsAndSchedulePatch(t *testing.T) {
	archive := []byte{0x1f, 0x8b, 0x08, 0x00}
	v := engineFixture(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/download"):
			w.Write(archive)
		case strings.HasSuffix(r.URL.Path, "/log"):
			io.WriteString(w, "container log\n")
		case r.Method == "PATCH":
			var body map[string]any
			mustDecodeRequest(t, r, &body)
			if len(body) != 1 || body["enabled"] != false {
				t.Error("changed unintended schedule fields")
			}
			io.WriteString(w, `{"enabled":false}`)
		default:
			t.Error(r.Method, r.URL)
			w.WriteHeader(404)
		}
	})
	v["run"], v["stage"], v["output"] = "run", "0", filepath.Join(t.TempDir(), "run.tar.gz")
	captureStdout(t, func() error { return downloadEngineRun(v) })
	raw, err := os.ReadFile(v["output"])
	if err != nil || !bytes.Equal(raw, archive) {
		t.Fatal("archive corrupted", err)
	}
	if downloadEngineRun(v) == nil {
		t.Fatal("overwrote existing archive")
	}
	out := captureStdout(t, func() error { return getEngineLog(v) })
	if !bytes.Contains(out, []byte("container log")) {
		t.Fatal(string(out))
	}
	v["file"], v["schedule"] = filepath.Join(t.TempDir(), "patch.yaml"), "schedule"
	if err := os.WriteFile(v["file"], []byte("enabled: false\n"), 0600); err != nil {
		t.Fatal(err)
	}
	captureStdout(t, func() error { return updateEngineSchedule(v) })
	if err := os.WriteFile(v["file"], []byte("token: usr_never-send\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if updateEngineSettings(v) == nil {
		t.Fatal("accepted installer-only token")
	}
}

func TestEngineWatchDrainsFinalPageAndPreservesCursor(t *testing.T) {
	calls := 0
	v := engineFixture(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if !strings.HasSuffix(r.URL.Path, "/feed") || r.URL.Query().Get("wait") != "25000" {
			t.Error(r.URL)
		}
		if calls == 1 {
			if r.URL.Query().Get("cursor") != "original+/=" {
				t.Error("initial cursor changed")
			}
			io.WriteString(w, `{"cursor":"next+/=","done":true,"items":[{"text":"final message"}]}`)
		} else {
			if r.URL.Query().Get("cursor") != "next+/=" {
				t.Error("returned cursor changed")
			}
			io.WriteString(w, `{"cursor":"next+/=","done":true,"items":[]}`)
		}
	})
	v["run"], v["cursor"], v["follow"] = "run", "original+/=", "true"
	out := captureStdout(t, func() error { return watchEngineRun(v) })
	if calls != 2 || len(bytes.Split(bytes.TrimSpace(out), []byte("\n"))) != 2 {
		t.Fatalf("watch did not drain its final page: %d %s", calls, out)
	}
}

func TestEngineFailedDownloadLeavesNoArtifact(t *testing.T) {
	v := engineFixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(409)
		io.WriteString(w, `{"error":"run is active"}`)
	})
	dir := t.TempDir()
	v["output"], v["run"] = filepath.Join(dir, "run.tar.gz"), "run"
	if err := downloadEngineRun(v); err == nil || !strings.Contains(err.Error(), "run is active") {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatal("failed download left files", err)
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
