package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

var profileNameFlag = flagSpec{
	name: "profile", placeholder: "profile", usage: "profile name", required: true,
}

var profileSaveNameFlag = flagSpec{
	name: "profile", placeholder: "profile", usage: "profile name (overrides CYSU_PROFILE; default: default)",
	fallback: profileFromEnvironment,
}

// profileFromEnvironment supplies the default profile for profile commands.
func profileFromEnvironment() (string, error) {
	profile := strings.TrimSpace(os.Getenv("CYSU_PROFILE"))
	if profile == "" {
		profile = "default"
	}
	return profile, nil
}

var profileScopeFlag = flagSpec{
	name: "scope", placeholder: "scope", usage: "installation scope: global or project", required: true,
}

var profileResource = resource{
	name: "profiles", title: "Profiles", api: true,
	commands: []command{
		{name: "list", run: func(v flagValues) error { return request("GET", apiPath("profiles"), nil) }},
		{name: "read", flags: []flagSpec{profileNameFlag}, run: func(v flagValues) error {
			return request("GET", apiPath("profiles", v["profile"]), nil)
		}},
		{name: "remove", flags: []flagSpec{profileNameFlag}, run: func(v flagValues) error {
			return request("DELETE", apiPath("profiles", v["profile"]), nil)
		}},
		{name: "create", local: true, flags: []flagSpec{profileNameFlag, {name: "example", boolean: true, usage: "populate one sample skill, agent, MCP server, and prompt"}}, run: func(v flagValues) error {
			return createProfile(v["profile"], v.enabled("example"))
		}},
		{
			name: "edit", flags: []flagSpec{
				{name: "profile", placeholder: "profile", usage: "profile name"},
				{name: "all", boolean: true, usage: "pull all remote profiles into ~/cysu/profiles (overrides --profile)"},
			},
			validate: func(v flagValues) error {
				if !v.enabled("all") && strings.TrimSpace(v["profile"]) == "" {
					return cliError{message: "profiles edit: provide --profile or --all", exit: exitUsage}
				}
				return nil
			},
			run: func(v flagValues) error {
				if v.enabled("all") {
					return editAllProfiles()
				}
				return editProfile(v["profile"])
			},
		},
		{
			name: "save", flags: []flagSpec{
				profileSaveNameFlag,
				{name: "all", boolean: true, usage: "save all local profiles in ~/cysu/profiles (overrides --profile and CYSU_PROFILE)"},
			},
			run: func(v flagValues) error {
				if v.enabled("all") {
					return saveAllEditedProfiles()
				}
				return saveEditedProfile(v["profile"])
			},
		},
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
	Name    string            `json:"name"`
	ID      string            `json:"id"`
	Type    string            `json:"type"`
	URL     string            `json:"url"`
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Headers map[string]string `json:"headers"`
	Profile string            `json:"profile"`
}

type profileBundle struct {
	ID      string `json:"id"`
	Skills  []storedSkill
	Agents  []storedAgent
	MCP     []storedMCP
	Prompts []storedPrompt
}

// fetchProfile reads all resources from one consistent server-side snapshot.
func fetchProfile(profile string) (profileBundle, error) {
	var bundle profileBundle
	raw, err := send("GET", apiPath("profiles", profile), nil)
	if err != nil {
		return bundle, err
	}
	if json.Unmarshal(raw, &bundle) != nil || bundle.ID != profile || bundle.Skills == nil || bundle.Agents == nil || bundle.MCP == nil || bundle.Prompts == nil {
		return bundle, failf("invalid profile response for %q", profile)
	}
	for i := range bundle.Skills {
		bundle.Skills[i].ID = bundle.Skills[i].Name
	}
	for i := range bundle.Agents {
		bundle.Agents[i].ID = bundle.Agents[i].Name
	}
	for i := range bundle.MCP {
		bundle.MCP[i].ID = bundle.MCP[i].Name
	}
	for i := range bundle.Prompts {
		bundle.Prompts[i].ID = bundle.Prompts[i].Name
	}
	if err := validateStoredPrompts(bundle.Prompts); err != nil {
		return bundle, err
	}
	return bundle, nil
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
