package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"
)

var profileNameFlag = flagSpec{
	name: "profile", placeholder: "profile", usage: "profile to install (default: default)",
	fallback: func() (string, error) { return "default", nil },
}

var profileScopeFlag = flagSpec{
	name: "scope", placeholder: "scope", usage: "installation scope: global or project", required: true,
}

var profileResource = resource{
	name: "profile", title: "Profiles", api: true,
	commands: []command{
		{
			name: "install", flags: []flagSpec{profileNameFlag, providerFlag, profileScopeFlag},
			run: func(v flagValues) error { return installProfile(v["profile"], v["provider"], v["scope"]) },
		},
		{
			name: "uninstall", flags: []flagSpec{providerFlag, profileScopeFlag}, local: true,
			run: func(v flagValues) error { return uninstallProfile(v["provider"], v["scope"]) },
		},
	},
}

type storedAgent struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Model       string `json:"model"`
	Description string `json:"description"`
	Content     string `json:"content"`
	Profile     string `json:"profile"`
}

type storedMCP struct {
	ID      string            `json:"id"`
	Type    string            `json:"type"`
	URL     string            `json:"url"`
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Headers map[string]string `json:"headers"`
	Profile string            `json:"profile"`
}

type profileBundle struct {
	Skills  []storedSkill
	Agents  []storedAgent
	MCP     []storedMCP
	Prompts []storedPrompt
}

// Fetch every resource through the profile-filtered API before changing local
// configuration. Checking the returned profile also detects older servers that
// silently ignore the query parameter.
func fetchProfile(profile string) (profileBundle, error) {
	var bundle profileBundle
	for _, resource := range []struct {
		name string
		out  any
	}{{"skills", &bundle.Skills}, {"agents", &bundle.Agents}, {"mcp", &bundle.MCP}} {
		raw, err := send("GET", apiPath(resource.name)+"?profile="+url.QueryEscape(profile), nil)
		if err != nil {
			return bundle, err
		}
		if len(raw) == 0 || raw[0] != '[' || json.Unmarshal(raw, resource.out) != nil {
			return bundle, failf("unexpected %s response: expected a JSON array", resource.name)
		}
	}
	var err error
	bundle.Prompts, err = fetchPrompts(profile)
	if err != nil {
		return bundle, err
	}
	if len(bundle.Skills)+len(bundle.Agents)+len(bundle.MCP)+len(bundle.Prompts) == 0 {
		return bundle, failf("profile %q contains no skills, agents, MCP servers, or prompts", profile)
	}
	for _, skill := range bundle.Skills {
		if err := checkProfileEntry(skill.ID, skill.Profile, profile); err != nil {
			return bundle, err
		}
	}
	for _, agent := range bundle.Agents {
		if err := checkProfileEntry(agent.ID, agent.Profile, profile); err != nil {
			return bundle, err
		}
	}
	for _, server := range bundle.MCP {
		if err := checkProfileEntry(server.ID, server.Profile, profile); err != nil {
			return bundle, err
		}
	}
	return bundle, nil
}

func checkProfileEntry(id, actual, expected string) error {
	if strings.TrimSpace(id) == "" {
		return failf("profile response contains an entry without an id")
	}
	if actual != expected {
		return failf("resource %s has profile %q, expected %q; ensure the storage API supports profile filtering", id, actual, expected)
	}
	return nil
}

func installProfile(profile, providerValue, scopeValue string) error {
	profile = strings.TrimSpace(profile)
	if profile == "" {
		return failf("--profile must not be empty")
	}
	provider, err := normalizeSkillProvider(providerValue)
	if err != nil {
		return err
	}
	scope, err := normalizeProfileScope(scopeValue)
	if err != nil {
		return err
	}
	unlock, err := lockProfileChanges()
	if err != nil {
		return err
	}
	defer unlock()
	layout, err := profileLayoutForScope(provider, scope)
	if err != nil {
		return err
	}
	state, err := loadProfileState(layout)
	if err != nil {
		return err
	}
	if err := checkProfileOwnership(layout); err != nil {
		return err
	}
	bundle, err := fetchProfile(profile)
	if err != nil {
		return err
	}
	plan, err := prepareProfile(layout, bundle)
	if err != nil {
		return err
	}
	if state == nil {
		state, err = backupProfile(layout, profile)
		if err != nil {
			return err
		}
	} else if state.Version < 3 {
		if err := upgradeProfilePromptBackup(layout, state); err != nil {
			return err
		}
	}
	// applyProfile stages every target and rolls all changes back on an error.
	// The durable backup remains available after process termination as well.
	if err := applyProfile(plan); err != nil {
		return err
	}
	state.Profile = profile
	if err := saveProfileState(layout, state); err != nil {
		return failf("profile installed, but updating its state failed: %v; backup remains at %s", err, layout.Backup)
	}
	return printJSON(map[string]any{
		"success": true, "provider": provider, "profile": profile, "backup": layout.Backup,
		"scope": layout.Scope, "project_directory": layout.Project,
		"skills": len(bundle.Skills), "agents": len(bundle.Agents), "mcp": len(bundle.MCP), "prompts": len(bundle.Prompts),
		"skills_directory": layout.Targets[0].Path, "restart_required": true,
	})
}

func uninstallProfile(providerValue, scopeValue string) error {
	provider, err := normalizeSkillProvider(providerValue)
	if err != nil {
		return err
	}
	scope, err := normalizeProfileScope(scopeValue)
	if err != nil {
		return err
	}
	unlock, err := lockProfileChanges()
	if err != nil {
		return err
	}
	defer unlock()
	layout, err := profileLayoutForScope(provider, scope)
	if err != nil {
		return err
	}
	state, err := loadProfileState(layout)
	if err != nil {
		return err
	}
	if state == nil {
		return failf("no %s %s profile is installed", scope, provider)
	}
	if err := checkStoredProfileOwnership(layout); err != nil {
		return err
	}
	plan, err := prepareProfileRestore(layout, state)
	if err != nil {
		return err
	}
	if err := applyProfile(plan); err != nil {
		return err
	}
	if err := os.RemoveAll(layout.Backup); err != nil {
		return fmt.Errorf("profile restored but removing backup %s failed: %w", layout.Backup, err)
	}
	return printJSON(map[string]any{
		"success": true, "provider": provider, "profile": state.Profile,
		"scope": layout.Scope, "project_directory": layout.Project,
		"restored": true, "restart_required": true,
	})
}
