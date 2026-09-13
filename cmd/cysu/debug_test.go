package main

// Tests for `cysu debug`. The command exists for when nothing else works, so
// most of these check that it still reports something useful with the token,
// the key or the backend missing.

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// isolate points the key file, the config directory and the token at a fresh
// temporary directory, so a test neither reads nor writes the real ones.
func isolate(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	t.Setenv("CYSU_KEY_FILE", filepath.Join(dir, "master.key"))
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("CYSU_TOKEN", "")
	t.Setenv("CYSU_PASSPHRASE", "test-passphrase")

	// The master key is cached for the lifetime of the process, so without
	// this a key loaded by an earlier test stands in for the one under test.
	prev := cachedKey
	cachedKey = nil
	t.Cleanup(func() { cachedKey = prev })

	return dir
}

// writeTestKey creates a usable key file at the isolated location.
func writeTestKey(t *testing.T, dir string) {
	t.Helper()

	if _, err := createKeyFile(filepath.Join(dir, "master.key")); err != nil {
		t.Fatalf("createKeyFile: %v", err)
	}
}

// asMap renders a report the way the command prints it, so a test can check
// the JSON rather than the struct.
func asMap(t *testing.T, report debugReport) map[string]any {
	t.Helper()

	raw, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return out
}

func TestDebugReportsProblems(t *testing.T) {
	isolate(t)

	report := buildReport(flagValues{}, false)

	if report.OK {
		t.Error("ok is true with no token and no key")
	}
	if len(report.Problems) != 2 {
		t.Errorf("problems = %v, want one for the token and one for the key", report.Problems)
	}
	if report.Token.Present {
		t.Error("a token was reported with none set")
	}
	if report.Key.Exists {
		t.Error("a key file was reported with none present")
	}
	if report.Version == "" || report.Platform == "" || report.APIURL == "" {
		t.Errorf("report is missing basic fields: %+v", report)
	}

	// Finding problems is still a successful run, so the contract's "error"
	// key must not appear.
	if _, found := asMap(t, report)["error"]; found {
		t.Error("a report carries an \"error\" key")
	}
}

// A diagnostic describes what is there. It must not prompt for a passphrase
// or write a key, which is what going through masterKey would do.
func TestDebugCreatesNoKey(t *testing.T) {
	dir := isolate(t)
	path := filepath.Join(dir, "master.key")

	buildReport(flagValues{}, false)

	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("debug created a key file at %s", path)
	}
}

func TestDebugReportsTheKey(t *testing.T) {
	dir := isolate(t)
	writeTestKey(t, dir)

	report := buildReport(flagValues{}, false)

	if !report.Key.Exists || !report.Key.OK {
		t.Errorf("key = %+v, want it present and usable", report.Key)
	}
	if report.Key.Permissions != "0600" {
		t.Errorf("permissions = %q, want 0600", report.Key.Permissions)
	}
	if report.Key.Detail != "" {
		t.Errorf("detail = %q, want none for a usable key", report.Key.Detail)
	}
}

// readKeyFile refuses a key others can read, so debug has to report that key
// as unusable rather than fine.
func TestDebugReportsInsecureKey(t *testing.T) {
	dir := isolate(t)
	writeTestKey(t, dir)

	path := filepath.Join(dir, "master.key")
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}

	report := buildReport(flagValues{}, false)

	if report.Key.OK {
		t.Error("an insecure key file was reported as usable")
	}
	if report.Key.Permissions != "0644" {
		t.Errorf("permissions = %q, want 0644", report.Key.Permissions)
	}
	if !strings.Contains(strings.Join(report.Problems, " "), "encryption key unusable") {
		t.Errorf("problems = %v, want the key listed", report.Problems)
	}
}

func TestDebugReportsTheToken(t *testing.T) {
	isolate(t)
	t.Setenv("CYSU_TOKEN", "sk-live-abcd1234")

	report := buildReport(flagValues{}, false)

	if !report.Token.Present {
		t.Fatal("no token reported with CYSU_TOKEN set")
	}
	if report.Token.Source != "CYSU_TOKEN" {
		t.Errorf("source = %q, want CYSU_TOKEN", report.Token.Source)
	}
	if report.Token.Hint != "****1234" {
		t.Errorf("hint = %q, want ****1234", report.Token.Hint)
	}

	// --token takes precedence, the same order loadAPIToken follows.
	report = buildReport(flagValues{"token": "sk-flag-wxyz9876"}, false)
	if report.Token.Source != "--token" {
		t.Errorf("source = %q, want --token", report.Token.Source)
	}
	if report.Token.Hint != "****9876" {
		t.Errorf("hint = %q, want ****9876", report.Token.Hint)
	}

	// The full token is never in the output.
	if raw, _ := json.Marshal(report); strings.Contains(string(raw), "sk-flag-wxyz9876") {
		t.Errorf("report leaked the token: %s", raw)
	}
}

func TestDebugCheckReachable(t *testing.T) {
	dir := isolate(t)
	writeTestKey(t, dir)
	t.Setenv("CYSU_TOKEN", "sk-live-abcd1234")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"success":true,"projects":[]}`)
	}))
	defer srv.Close()
	t.Setenv("CYSU_API_URL", srv.URL)

	report := buildReport(flagValues{}, true)

	if report.Backend == nil {
		t.Fatal("check did not probe the backend")
	}
	if !report.Backend.Reachable || !report.Backend.Authorised {
		t.Errorf("backend = %+v, want reachable and authorised", report.Backend)
	}
	if !report.OK {
		t.Errorf("ok is false with everything working: %v", report.Problems)
	}
}

// A refused request still proves the backend answered, which is the
// distinction worth drawing when something is wrong.
func TestDebugCheckRefused(t *testing.T) {
	dir := isolate(t)
	writeTestKey(t, dir)
	t.Setenv("CYSU_TOKEN", "sk-live-abcd1234")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error":"Invalid API token"}`)
	}))
	defer srv.Close()
	t.Setenv("CYSU_API_URL", srv.URL)

	report := buildReport(flagValues{}, true)

	if !report.Backend.Reachable {
		t.Error("a backend that answered was reported unreachable")
	}
	if report.Backend.Authorised {
		t.Error("a refused token was reported as authorised")
	}
	if !strings.Contains(string(report.Backend.Detail), "Invalid API token") {
		t.Errorf("detail = %s, want the backend's message", report.Backend.Detail)
	}
	if !strings.Contains(strings.Join(report.Problems, " "), "refused the token") {
		t.Errorf("problems = %v, want the refusal listed", report.Problems)
	}
}

func TestDebugCheckUnreachable(t *testing.T) {
	dir := isolate(t)
	writeTestKey(t, dir)
	t.Setenv("CYSU_TOKEN", "sk-live-abcd1234")
	t.Setenv("CYSU_API_URL", "http://127.0.0.1:1")

	report := buildReport(flagValues{}, true)

	if report.Backend.Reachable {
		t.Error("a closed port was reported reachable")
	}
	if !strings.Contains(strings.Join(report.Problems, " "), "unreachable") {
		t.Errorf("problems = %v, want the backend listed", report.Problems)
	}
}

// Without a token there is nothing to probe with, so check reports the
// missing token rather than a misleading connection failure.
func TestDebugCheckSkipsProbeWithoutToken(t *testing.T) {
	dir := isolate(t)
	writeTestKey(t, dir)

	report := buildReport(flagValues{}, true)

	if report.Backend != nil {
		t.Errorf("backend = %+v, want no probe without a token", report.Backend)
	}
	if !strings.Contains(strings.Join(report.Problems, " "), "no API token") {
		t.Errorf("problems = %v, want the token listed", report.Problems)
	}
}
