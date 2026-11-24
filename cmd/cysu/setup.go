package main

// setup.go collects installation settings and stores them for later commands.

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"

	"golang.org/x/term"
)

var setupResource = resource{
	name: "setup", title: "Setup", standalone: true,
	commands: []command{{
		flags: []flagSpec{
			{name: "headless", boolean: true, usage: "skip prompts (also automatic when any flag is passed)"},
			{name: "license", placeholder: "license", usage: "optional JXScout licence, uploaded but never saved locally"},
			{name: "passphrase", placeholder: "passphrase", usage: "encryption passphrase (or CYSU_PASSPHRASE)"},
			{name: "key", placeholder: "cysu-key", usage: "Cyber Supplies API token (or CYSU_TOKEN)"},
			{name: "engine-url", placeholder: "url", usage: "optional engine URL (or CYSU_ENGINE_URL)"},
			{name: "provider", placeholder: "AGENTS|CLAUDE|CODEX", usage: "optional inference provider: AGENTS, CLAUDE, or CODEX"},
		},
		run: setup,
	}},
}

// setup merges explicit values with the existing config and environment,
// prompts for missing values when interactive, and saves the result.
func setup(v flagValues) error {
	headless := setupIsHeadless(v)
	cfg, err := readConfig()
	if err != nil {
		return err
	}
	if v.provided("key") {
		cfg.Token = strings.TrimSpace(v["key"])
	} else if value := strings.TrimSpace(os.Getenv("CYSU_TOKEN")); value != "" {
		cfg.Token = value
	}
	if v.provided("passphrase") {
		cfg.Passphrase = v["passphrase"]
	} else if value := os.Getenv("CYSU_PASSPHRASE"); value != "" {
		cfg.Passphrase = value
	}
	license := strings.TrimSpace(v["license"])
	if v.provided("engine-url") {
		cfg.EngineURL = strings.TrimSpace(v["engine-url"])
	} else if value := strings.TrimSpace(os.Getenv("CYSU_ENGINE_URL")); value != "" {
		cfg.EngineURL = value
	}
	if v.provided("provider") {
		cfg.Provider = strings.TrimSpace(v["provider"])
	} else if cfg.Provider == "" {
		cfg.Provider = strings.TrimSpace(os.Getenv("CYSU_PROVIDER"))
	}

	if !headless {
		if err := promptSetup(&cfg, &license, v); err != nil {
			return err
		}
	}
	if cfg.Token == "" {
		return cliError{message: "missing Cyber Supplies key: pass --key or set CYSU_TOKEN", exit: exitUsage}
	}
	if headless {
		if err := validateSetupToken(cfg.Token); err != nil {
			return err
		}
	}
	if cfg.Passphrase == "" {
		return cliError{message: "missing passphrase: pass --passphrase or set CYSU_PASSPHRASE", exit: exitUsage}
	}
	if license != "" && !strings.HasPrefix(cfg.Token, "usr_") {
		return cliError{message: "uploading a JXScout licence requires a Cyber Supplies USER token (usr_)", exit: exitUsage}
	}
	if err := validateSetupEngineURL(cfg.EngineURL); err != nil {
		return err
	}
	if cfg.Provider, err = normalizeSetupProvider(cfg.Provider); err != nil {
		return err
	}
	pass := []byte(cfg.Passphrase)
	cfg.MasterKey = deriveMasterKey(pass)
	zero(pass)
	path, err := writeConfig(cfg)
	if err != nil {
		zero(cfg.MasterKey)
		return err
	}
	zero(cachedKey)
	cachedKey = nil
	if license != "" {
		raw, err := send(http.MethodPost, "/integrations/jxscout", map[string]string{"licenseKey": license})
		if err != nil {
			return err
		}
		var result struct {
			Connected bool `json:"connected"`
		}
		if json.Unmarshal(raw, &result) != nil || !result.Connected {
			return failf("JXScout licence upload did not confirm a connection")
		}
	}
	fmt.Println(path)
	return nil
}

// setupIsHeadless treats any explicit setup flag as a non-interactive run.
func setupIsHeadless(v flagValues) bool {
	if v.enabled("headless") {
		return true
	}
	for name := range v {
		if strings.HasPrefix(name, "provided:") {
			return true
		}
	}
	return false
}

// validateSetupToken confirms that the supplied USER or LLM token is accepted
// by the gateway before setup saves local settings.
func validateSetupToken(token string) error {
	if !strings.HasPrefix(token, "usr_") && !strings.HasPrefix(token, "llm_") {
		return cliError{message: "setup requires a Cyber Supplies USER (usr_) or LLM (llm_) token", exit: exitUsage}
	}
	apiToken = token
	raw, err := send(http.MethodGet, "/integrations/jxscout", nil)
	if err != nil {
		return err
	}
	var result struct {
		Connected *bool `json:"connected"`
	}
	if json.Unmarshal(raw, &result) != nil || result.Connected == nil {
		return failf("gateway returned an invalid token validation response")
	}
	return nil
}

// promptSetup asks for missing values, skipping engine and licence questions
// for LLM tokens. Terminal echo lets users see and edit values as they type.
func promptSetup(cfg *config, license *string, v flagValues) error {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return failf("interactive setup requires a terminal; pass a setup flag or use --headless with CYSU_TOKEN and CYSU_PASSPHRASE")
	}
	reader := bufio.NewReader(os.Stdin)
	fields := []struct {
		name, label string
		value       *string
	}{
		{"key", "Cyber Supplies key", &cfg.Token},
		{"passphrase", "Encryption passphrase", &cfg.Passphrase},
		{"engine-url", "Engine URL (optional)", &cfg.EngineURL},
		{"license", "JXScout licence (optional)", license},
		{"provider", "Inference provider (AGENTS|CLAUDE|CODEX; optional)", &cfg.Provider},
	}
	for _, field := range fields {
		if strings.HasPrefix(cfg.Token, "llm_") && (field.name == "engine-url" || field.name == "license") {
			continue
		}
		changePassphrase := field.name == "passphrase" && !v.provided("passphrase") && os.Getenv("CYSU_PASSPHRASE") == "" && *field.value != ""
		if changePassphrase || (!v.provided(field.name) && *field.value == "") {
			label := field.label
			if changePassphrase {
				label = "New passphrase (leave blank to keep current)"
			}
			fmt.Fprint(os.Stderr, label+": ")
			raw, err := reader.ReadString('\n')
			if err != nil {
				return fmt.Errorf("reading %s: %w", field.label, err)
			}
			value := strings.TrimRight(raw, "\r\n")
			if field.name != "passphrase" {
				value = strings.TrimSpace(value)
			}
			if !changePassphrase || value != "" {
				*field.value = value
			}
		}
		if field.name == "key" {
			if err := validateSetupToken(cfg.Token); err != nil {
				return err
			}
		}
	}
	return nil
}

// normalizeSetupProvider accepts only the three displayed provider choices.
func normalizeSetupProvider(value string) (string, error) {
	provider := strings.ToUpper(strings.TrimSpace(value))
	switch provider {
	case "", skillProviderAgents, skillProviderClaude, skillProviderCodex:
		return provider, nil
	default:
		return "", cliError{message: "invalid provider: use AGENTS, CLAUDE, or CODEX", exit: exitUsage}
	}
}

// validateSetupEngineURL applies the existing engine URL rules when an
// optional address is supplied.
func validateSetupEngineURL(value string) error {
	if value == "" {
		return nil
	}
	values := flagValues{"engine": value}
	return validateEngineURL(values)
}
