package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	profileScopeGlobal  = "global"
	profileScopeProject = "project"
)

func normalizeProfileScope(value string) (string, error) {
	scope := strings.ToLower(strings.TrimSpace(value))
	if scope != profileScopeGlobal && scope != profileScopeProject {
		return "", cliError{message: fmt.Sprintf("unsupported scope %q: use global or project", value), exit: exitUsage}
	}
	return scope, nil
}

func profileLayoutForScope(provider, value string) (profileLayout, error) {
	scope, err := normalizeProfileScope(value)
	if err != nil {
		return profileLayout{}, err
	}
	if scope == profileScopeGlobal {
		return profileLayoutFor(provider)
	}
	project, err := os.Getwd()
	if err != nil {
		return profileLayout{}, err
	}
	project, err = filepath.EvalSymlinks(project)
	if err != nil {
		return profileLayout{}, err
	}
	if os.PathSeparator == '\\' {
		project = strings.ToLower(project)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return profileLayout{}, err
	}
	if profilePathWithin(project, resolveProfilePath(home)) {
		return profileLayout{}, failf("project scope cannot use your home directory or one of its ancestors; run from a project folder")
	}
	backup, err := projectProfileBackupDirectory(provider, project)
	if err != nil {
		return profileLayout{}, err
	}
	layout := profileLayout{Provider: provider, Scope: scope, Project: project, Backup: backup, Targets: projectProfileTargets(provider, project)}
	return validateProfileLayout(layout)
}

// Native project paths intentionally do not consult global directory overrides.
func projectProfileTargets(provider, project string) []profileTarget {
	root := filepath.Join(project, ".agents")
	skills, config := filepath.Join(root, "skills"), filepath.Join(root, "mcp.json")
	switch provider {
	case skillProviderClaude:
		root = filepath.Join(project, ".claude")
		skills, config = filepath.Join(root, "skills"), filepath.Join(project, ".mcp.json")
	case skillProviderCodex:
		root = filepath.Join(project, ".codex")
		config = filepath.Join(root, "config.toml")
	}
	targets := []profileTarget{{Path: skills, Kind: "skills"}, {Path: filepath.Join(root, "agents"), Kind: "agents"}, {Path: config, Kind: "config"}}
	if provider == skillProviderCodex {
		targets = append(targets, profileTarget{Path: filepath.Join(root, "skills"), Kind: "legacy-skills"})
	}
	targets = append(targets, profileTarget{Path: profilePromptPath(provider, project), Kind: "prompts"})
	return targets
}

func projectProfileBackupDirectory(provider, project string) (string, error) {
	global, err := profileBackupDirectory(provider)
	if err != nil {
		return "", err
	}
	identity := project
	if os.PathSeparator == '\\' {
		identity = strings.ToLower(identity)
	}
	hash := sha256.Sum256([]byte(identity))
	return filepath.Join(filepath.Dir(global), "projects", hex.EncodeToString(hash[:]), strings.ToLower(provider)), nil
}

// Version 1 global and version 2 project backups remain readable. Version 3
// includes the prompt instruction file in both scopes.
func validProfileStateScope(state *profileState, backup string, raw []byte) bool {
	var expected string
	var err error
	switch state.Version {
	case 1:
		if (state.Scope != "" && state.Scope != profileScopeGlobal) || state.Project != "" {
			return false
		}
		state.Scope = profileScopeGlobal
		expected, err = profileBackupDirectory(state.Provider)
	case 2:
		if state.Scope != profileScopeProject || !requiredStateFields(raw, "scope", "project_directory") ||
			!filepath.IsAbs(state.Project) || state.Project != filepath.Clean(state.Project) {
			return false
		}
		targets := projectProfileTargets(state.Provider, state.Project)
		targets = targets[:len(targets)-1]
		if len(state.Snapshots) != len(targets) {
			return false
		}
		for i, snapshot := range state.Snapshots {
			if snapshot.profileTarget != targets[i] {
				return false
			}
		}
		expected, err = projectProfileBackupDirectory(state.Provider, state.Project)
	case 3:
		if !requiredStateFields(raw, "scope") {
			return false
		}
		switch state.Scope {
		case profileScopeGlobal:
			if state.Project != "" {
				return false
			}
			expected, err = profileBackupDirectory(state.Provider)
		case profileScopeProject:
			if !requiredStateFields(raw, "project_directory") || !filepath.IsAbs(state.Project) || state.Project != filepath.Clean(state.Project) {
				return false
			}
			targets := projectProfileTargets(state.Provider, state.Project)
			if len(state.Snapshots) != len(targets) {
				return false
			}
			for i, snapshot := range state.Snapshots {
				if snapshot.profileTarget != targets[i] {
					return false
				}
			}
			expected, err = projectProfileBackupDirectory(state.Provider, state.Project)
		default:
			return false
		}
	default:
		return false
	}
	return err == nil && backup == expected
}

// Enumerate saved ownership independently of current provider roots and cwd.
// This also protects project files when a global override aliases their paths.
func storedProfileOwners(excludeBackup string) ([]*profileState, error) {
	var owners []*profileState
	load := func(provider, backup string) error {
		if backup == excludeBackup {
			return nil
		}
		state, err := readProfileState(provider, backup)
		if err != nil {
			return err
		}
		if state != nil {
			owners = append(owners, state)
		}
		return nil
	}
	providers := []string{skillProviderAgents, skillProviderClaude, skillProviderCodex}
	for _, provider := range providers {
		backup, err := profileBackupDirectory(provider)
		if err != nil {
			return nil, err
		}
		if err := load(provider, backup); err != nil {
			return nil, err
		}
	}
	global, err := profileBackupDirectory(skillProviderAgents)
	if err != nil {
		return nil, err
	}
	projects := filepath.Join(filepath.Dir(global), "projects")
	info, err := os.Lstat(projects)
	if os.IsNotExist(err) {
		return owners, nil
	}
	if err != nil {
		return nil, err
	}
	invalid := func(path string) error {
		return failf("invalid project profile backup registry at %s; preserve it and repair it before retrying", path)
	}
	if !info.IsDir() {
		return nil, invalid(projects)
	}
	entries, err := os.ReadDir(projects)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		bucket := filepath.Join(projects, entry.Name())
		key, decodeErr := hex.DecodeString(entry.Name())
		if !entry.IsDir() || decodeErr != nil || len(key) != sha256.Size {
			return nil, invalid(bucket)
		}
		backups, err := os.ReadDir(bucket)
		if err != nil {
			return nil, err
		}
		for _, entry := range backups {
			if strings.HasPrefix(entry.Name(), ".profile-backup-") {
				continue // Uncommitted temporary snapshots left after a crash.
			}
			provider := strings.ToUpper(entry.Name())
			if entry.Name() != strings.ToLower(provider) || (provider != skillProviderAgents && provider != skillProviderClaude && provider != skillProviderCodex) {
				return nil, invalid(filepath.Join(bucket, entry.Name()))
			}
			if err := load(provider, filepath.Join(bucket, entry.Name())); err != nil {
				return nil, err
			}
		}
	}
	return owners, nil
}

func profileOwnerRestoreHint(state *profileState) string {
	hint := fmt.Sprintf("run cysu profiles uninstall --provider %s --scope %s", state.Provider, state.Scope)
	if state.Scope == profileScopeProject {
		hint += fmt.Sprintf(" from %q", state.Project)
	}
	return hint
}
