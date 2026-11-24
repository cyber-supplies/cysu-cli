package main

// debug.go is the `cysu debug` command: what the CLI can say about its own
// setup when something is not working.
//
// `debug info` is offline and answers "what is this binary configured to
// do": version, platform, gateway URL, whether a token and passphrase are
// present, and whether the config is readable. `debug check` adds a
// request to the gateway, so it also answers "does any of it work".
//
// Both are built to run when nothing else does. Neither needs a token or
// passphrase, and neither derives a key. A diagnostic must not change setup.
//
// A run that finds problems is still a successful run. It reports them under
// "problems" with "ok": false and exits 0. The "error" key is kept for the
// command itself failing, so an agent testing for it is not told that asking
// the question went wrong.

import (
	"encoding/json"
	"errors"
	"os"
	"runtime"
	"strings"
	"time"
)

var debugResource = resource{
	name:  "debug",
	title: "Debug",
	commands: []command{
		{
			name:  "info",
			flags: []flagSpec{tokenFlag},
			run: func(v flagValues) error {
				return printJSON(buildReport(v, false))
			},
		},
		{
			name:  "check",
			flags: []flagSpec{tokenFlag},
			run: func(v flagValues) error {
				return printJSON(buildReport(v, true))
			},
		},
	},
}

// ---------------------------------------------------------------------------
// The report
// ---------------------------------------------------------------------------

// A debugReport is what `cysu debug` prints.
type debugReport struct {
	OK         bool             `json:"ok"`
	Version    string           `json:"version"`
	Platform   string           `json:"platform"`
	Go         string           `json:"go"`
	APIURL     string           `json:"api_url"`
	Token      tokenStatus      `json:"token"`
	Passphrase passphraseStatus `json:"passphrase"`
	Config     configStatus     `json:"config"`
	Gateway    *gatewayStatus   `json:"gateway,omitempty"`
	Problems   []string         `json:"problems"`
}

// A tokenStatus says whether a token was found and where, never what it is.
type tokenStatus struct {
	Present bool   `json:"present"`
	Source  string `json:"source,omitempty"`
	Hint    string `json:"hint,omitempty"`
}

// passphraseStatus reports presence without exposing the passphrase.
type passphraseStatus struct {
	Present bool   `json:"present"`
	Source  string `json:"source,omitempty"`
}

// A configStatus describes the settings file.
type configStatus struct {
	Path   string `json:"path"`
	Exists bool   `json:"exists"`
	Detail string `json:"detail,omitempty"`
}

// A gatewayStatus is the outcome of the request `debug check` makes.
type gatewayStatus struct {
	Reachable  bool            `json:"reachable"`
	Authorised bool            `json:"authorised"`
	DurationMS int64           `json:"duration_ms"`
	Detail     json.RawMessage `json:"detail,omitempty"`
}

// buildReport gathers everything the CLI can say about itself. With probe set
// it also asks the gateway, which needs a token; without one that step is
// skipped rather than failing, since a missing token is one of the things
// worth reporting.
func buildReport(v flagValues, probe bool) debugReport {
	report := debugReport{
		Version:    version,
		Platform:   runtime.GOOS + "/" + runtime.GOARCH,
		Go:         runtime.Version(),
		APIURL:     resolveBaseURL(),
		Token:      inspectToken(v),
		Passphrase: inspectPassphrase(),
		Config:     inspectConfig(),
		Problems:   []string{}, // never null in the output
	}

	if !report.Token.Present {
		report.Problems = append(report.Problems,
			"no API token: run cysu setup, set CYSU_TOKEN, or pass --token <token>")
	}
	if !report.Passphrase.Present {
		report.Problems = append(report.Problems, "no encryption passphrase: run cysu setup or set CYSU_PASSPHRASE")
	}
	if report.Config.Detail != "" {
		report.Problems = append(report.Problems, "config unreadable: "+report.Config.Detail)
	}

	if probe && report.Token.Present {
		loadAPIToken(v) // cannot fail: a token was found above
		gateway := probeGateway()
		report.Gateway = &gateway

		switch {
		case !gateway.Reachable:
			report.Problems = append(report.Problems, "gateway unreachable at "+report.APIURL)
		case !gateway.Authorised:
			report.Problems = append(report.Problems, "gateway refused the token")
		}
	}

	report.OK = len(report.Problems) == 0
	return report
}

// ---------------------------------------------------------------------------
// Gathering
// ---------------------------------------------------------------------------

// inspectToken reports where the token for this invocation would come from.
// It follows the same order as loadAPIToken, and keeps only enough of the
// value to tell two tokens apart.
func inspectToken(v flagValues) tokenStatus {
	if token := strings.TrimSpace(v["token"]); token != "" {
		return tokenStatus{Present: true, Source: "--token", Hint: redactTail(token)}
	}
	cfg, err := readConfig()
	if err != nil {
		return tokenStatus{}
	}
	if strings.TrimSpace(cfg.Token) != "" {
		return tokenStatus{Present: true, Source: "setup", Hint: redactTail(cfg.Token)}
	}
	if token := strings.TrimSpace(os.Getenv("CYSU_TOKEN")); token != "" {
		return tokenStatus{Present: true, Source: "CYSU_TOKEN", Hint: redactTail(token)}
	}
	return tokenStatus{}
}

// inspectPassphrase follows the same saved-config-first precedence as masterKey.
func inspectPassphrase() passphraseStatus {
	cfg, err := readConfig()
	if err != nil {
		return passphraseStatus{}
	}
	if cfg.Passphrase != "" {
		return passphraseStatus{Present: true, Source: "setup"}
	}
	if os.Getenv("CYSU_PASSPHRASE") != "" {
		return passphraseStatus{Present: true, Source: "CYSU_PASSPHRASE"}
	}
	return passphraseStatus{}
}

// inspectConfig describes the settings file.
func inspectConfig() configStatus {
	path, err := configPath()
	if err != nil {
		return configStatus{Detail: err.Error()}
	}
	status := configStatus{Path: path}

	if _, err := os.Stat(path); err == nil {
		status.Exists = true
	}

	_, err = readConfig()
	if err != nil {
		status.Detail = err.Error()
		return status
	}
	return status
}

// probeGateway asks the gateway for the user's storage list, a read-only route
// that needs a token, and reports what came back. A reply the gateway refused
// still proves it is reachable, which is the distinction worth drawing when
// something is wrong.
func probeGateway() gatewayStatus {
	start := time.Now()
	_, err := send("GET", apiPath("storage", "files"), nil)
	status := gatewayStatus{DurationMS: time.Since(start).Milliseconds()}

	if err == nil {
		status.Reachable = true
		status.Authorised = true
		return status
	}

	// A cliError carrying a body is one the gateway sent, so the host
	// answered and it is the request it turned down.
	var e cliError
	if errors.As(err, &e) && len(e.raw) > 0 {
		status.Reachable = true
		status.Detail = e.raw
		return status
	}
	status.Detail = jsonString(err.Error())
	return status
}
