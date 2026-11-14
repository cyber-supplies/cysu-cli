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

	"golang.org/x/term"
)

var authProviderFlag = flagSpec{name: "provider", placeholder: "provider", usage: "codex (default), claude, or openrouter", fallback: func() (string, error) { return "codex", nil }}
var secretStdinFlag = flagSpec{name: "stdin", boolean: true, usage: "read the secret from stdin instead of a hidden terminal prompt"}

func validateAuthProvider(v flagValues) error {
	switch v["provider"] {
	case "codex", "claude", "openrouter":
		return nil
	}
	return failf("--provider must be codex, claude, or openrouter")
}

func validateAuthEngine(v flagValues) error {
	if err := validateAuthProvider(v); err != nil {
		return err
	}
	if v["provider"] != "openrouter" && (v.enabled("stdin") || v.enabled("model-only") || v.provided("model")) {
		return failf("--stdin, --model and --model-only apply to OpenRouter authentication")
	}
	if v.enabled("model-only") && (v.enabled("stdin") || strings.TrimSpace(v["model"]) == "") {
		return failf("--model-only requires --model and cannot be combined with --stdin")
	}
	return nil
}

// Secret input is never echoed or included in parsing errors. Remove one line
// ending, preserving intentional whitespace and embedded newlines in passkeys.
func readEngineSecret(v flagValues, label string) (string, error) {
	var raw []byte
	var err error
	if v.enabled("stdin") {
		raw, err = io.ReadAll(io.LimitReader(os.Stdin, 16387))
	} else if term.IsTerminal(int(os.Stdin.Fd())) {
		fmt.Fprint(os.Stderr, label+": ")
		raw, err = term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
	} else {
		return "", failf("%s requires a terminal or --stdin", label)
	}
	if err != nil {
		return "", failf("could not read %s", label)
	}
	value := strings.TrimSuffix(strings.TrimSuffix(string(raw), "\n"), "\r")
	if strings.TrimSpace(value) == "" || len(value) > 16384 || strings.ContainsRune(value, 0) {
		return "", failf("%s must be nonempty, at most 16 KiB and contain no NUL", label)
	}
	return value, nil
}

func setEnginePassphrase(v flagValues) error {
	value, err := readEngineSecret(v, "Passphrase")
	if err != nil {
		return err
	}
	return engineRequest(v, http.MethodPut, "/api/settings", map[string]string{"passkey": value})
}

func authEngine(v flagValues) error {
	if v.provided("passphrase") {
		if _, err := engineSend(v, http.MethodPut, "/api/settings", map[string]string{"passkey": v["passphrase"]}); err != nil {
			return err
		}
	}
	if v["provider"] == "openrouter" {
		body := map[string]string{}
		if v.provided("model") {
			body["model"] = v["model"]
		}
		if !v.enabled("model-only") {
			key, err := readEngineSecret(v, "OpenRouter API key")
			if err != nil {
				return err
			}
			body["apiKey"] = strings.TrimSpace(key)
		}
		return engineRequest(v, http.MethodPut, "/api/llm/openrouter-auth", body)
	}
	path := "/api/llm/" + v["provider"] + "-login"
	raw, err := engineSend(v, http.MethodPost, path, nil)
	if err != nil {
		return err
	}
	return waitEngineLogin(v, path, raw)
}

// A waiting Claude login needs only a URL. Codex also supplies a device code.
// A slow startup/exchange returns its current state so it can be polled later;
// it never starts a second login just because the local wait timed out.
func waitEngineLogin(v flagValues, path string, raw []byte) error {
	deadline := time.Now().Add(30 * time.Second)
	for {
		var state struct{ Status, URL, Code, Error string }
		if json.Unmarshal(raw, &state) != nil {
			return failf("engine returned invalid login state")
		}
		switch state.Status {
		case "failed", "cancelled", "expired":
			return failf("%s login %s: %s", v["provider"], state.Status, state.Error)
		case "succeeded":
			printResponse(raw)
			return nil
		case "waiting":
			if state.URL != "" && (v["provider"] == "claude" || state.Code != "") {
				printResponse(raw)
				return nil
			}
		case "starting", "exchanging":
		default:
			return failf("engine returned invalid login state")
		}
		if !time.Now().Before(deadline) {
			printResponse(raw)
			return nil
		}
		time.Sleep(500 * time.Millisecond)
		var err error
		raw, err = engineSend(v, http.MethodGet, path, nil)
		if err != nil {
			return err
		}
	}
}

func submitClaudeCode(v flagValues) error {
	code, err := readEngineSecret(v, "Claude one-time code (including #state)")
	if err != nil {
		return err
	}
	// Do not split on '#': Claude Code needs the complete browser result.
	raw, err := engineSend(v, http.MethodPost, "/api/llm/claude-login/code", map[string]string{"id": v["id"], "code": strings.TrimSpace(code)})
	if err != nil {
		return err
	}
	v["provider"] = "claude"
	return waitEngineLogin(v, "/api/llm/claude-login", raw)
}

func engineAuthAction(method, suffix string) func(flagValues) error {
	return func(v flagValues) error {
		if suffix == "login" && v["provider"] == "openrouter" {
			return failf("OpenRouter uses a saved API key; use auth-status or forget-auth")
		}
		// Saved Codex credentials are independent of the currently selected mode.
		if method == http.MethodGet && suffix == "auth" && v["provider"] == "codex" {
			raw, err := engineSend(v, method, "/api/settings", nil)
			if err != nil {
				return err
			}
			var settings struct {
				LLM struct {
					Codex json.RawMessage `json:"codex"`
				} `json:"llm"`
			}
			if json.Unmarshal(raw, &settings) != nil || len(settings.LLM.Codex) == 0 || string(settings.LLM.Codex) == "null" {
				return failf("engine returned invalid Codex credential status")
			}
			printResponse(settings.LLM.Codex)
			return nil
		}
		return engineRequest(v, method, "/api/llm/"+v["provider"]+"-"+suffix, nil)
	}
}

// Scrub submitted secrets even if an upstream error echoes them under an
// unrelated field. Include JSON-escaped forms and the engine bearer token.
func engineRedactor(encoded []byte, token string) func([]byte) []byte {
	secrets := []string{token}
	var collect func(any)
	collect = func(value any) {
		switch value := value.(type) {
		case map[string]any:
			for key, item := range value {
				if isSensitive(key) || key == "code" || key == "value" {
					if s, ok := item.(string); ok && s != "" {
						secrets = append(secrets, s)
					}
				} else {
					collect(item)
				}
			}
		case []any:
			for _, item := range value {
				collect(item)
			}
		}
	}
	var body any
	_ = json.Unmarshal(encoded, &body)
	collect(body)
	return func(raw []byte) []byte {
		scrub := func(s string) string {
			for _, secret := range secrets {
				if secret != "" {
					s = strings.ReplaceAll(s, secret, redactedMarker)
				}
			}
			return s
		}
		var walk func(any) any
		walk = func(value any) any {
			switch value := value.(type) {
			case string:
				return scrub(value)
			case map[string]any:
				for key, item := range value {
					value[key] = walk(item)
				}
			case []any:
				for i, item := range value {
					value[i] = walk(item)
				}
			}
			return value
		}
		var value any
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		if decoder.Decode(&value) == nil {
			cleaned, err := json.Marshal(walk(value))
			if err == nil {
				return cleaned
			}
		}
		// A malformed JSON or plain-text error can still contain escaped input.
		text := string(raw)
		for _, secret := range secrets {
			if secret != "" {
				escaped, _ := json.Marshal(secret)
				text = strings.ReplaceAll(text, string(escaped[1:len(escaped)-1]), redactedMarker)
			}
		}
		return []byte(scrub(text))
	}
}
