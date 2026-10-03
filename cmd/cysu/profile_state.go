package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gofrs/flock"
)

type profileTarget struct {
	Path string `json:"path"`
	Kind string `json:"kind"`
}

type profileLayout struct {
	Provider string
	Scope    string
	Project  string
	Backup   string
	Targets  []profileTarget
}

type profileSnapshot struct {
	profileTarget
	Existed bool        `json:"existed"`
	Mode    os.FileMode `json:"mode"`
}

type profileState struct {
	Version   int               `json:"version"`
	Provider  string            `json:"provider"`
	Scope     string            `json:"scope,omitempty"`
	Project   string            `json:"project_directory,omitempty"`
	Profile   string            `json:"profile"`
	Snapshots []profileSnapshot `json:"snapshots"`
}

// A process lock covers profiles and the older skills installer, including
// providers that share the same skills directory. It releases on process exit.
func lockProfileChanges() (func(), error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(home, "cysu", "backup")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	lock := flock.New(filepath.Join(dir, ".profile.lock"), flock.SetPermissions(0o600))
	ok, err := lock.TryLock()
	if err != nil {
		return nil, err
	}
	if !ok {
		_ = lock.Close()
		return nil, failf("another skills or profile installation is in progress")
	}
	return func() { _ = lock.Close() }, nil
}

func profileLayoutFor(provider string) (profileLayout, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return profileLayout{}, err
	}
	home, err = filepath.Abs(home)
	if err != nil {
		return profileLayout{}, err
	}
	root := filepath.Join(home, ".agents")
	skills := filepath.Join(root, "skills")
	config := filepath.Join(root, "mcp.json")
	switch provider {
	case skillProviderCodex:
		root = strings.TrimSpace(os.Getenv("CODEX_HOME"))
		if root == "" {
			root = filepath.Join(home, ".codex")
		}
		config = filepath.Join(root, "config.toml")
	case skillProviderClaude:
		root = strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR"))
		if root == "" {
			root = filepath.Join(home, ".claude")
			config = filepath.Join(home, ".claude.json")
		} else {
			config = filepath.Join(root, ".claude.json")
		}
		skills = filepath.Join(root, "skills")
	case skillProviderAgents:
	default:
		return profileLayout{}, failf("unsupported profile provider %q", provider)
	}
	layout := profileLayout{
		Provider: provider,
		Scope:    profileScopeGlobal,
		Targets: []profileTarget{
			{Path: skills, Kind: "skills"},
			{Path: filepath.Join(root, "agents"), Kind: "agents"},
			{Path: config, Kind: "config"},
		},
	}
	layout.Backup, err = profileBackupDirectory(provider)
	if err != nil {
		return layout, err
	}
	if provider == skillProviderCodex {
		// Older Codex versions load this directory as well. Back it up and
		// leave it empty while the profile owns the current shared skill path.
		layout.Targets = append(layout.Targets, profileTarget{Path: filepath.Join(root, "skills"), Kind: "legacy-skills"})
	}
	return validateProfileLayout(layout)
}

func validateProfileLayout(layout profileLayout) (profileLayout, error) {
	globalBackup, err := profileBackupDirectory(layout.Provider)
	if err != nil {
		return layout, err
	}
	backupRoot := filepath.Dir(filepath.Dir(globalBackup))
	for i := range layout.Targets {
		path, err := filepath.Abs(layout.Targets[i].Path)
		if err != nil {
			return layout, err
		}
		layout.Targets[i].Path = path
		if layout.Scope == profileScopeProject && !profilePathWithin(layout.Project, resolveProfilePath(path)) {
			return layout, failf("project target %s resolves outside the project directory; remove the escaping symlink before retrying", path)
		}
		if pathsOverlap(path, backupRoot) {
			return layout, failf("profile target %s overlaps the backup directory %s", path, backupRoot)
		}
		for j := 0; j < i; j++ {
			if pathsOverlap(path, layout.Targets[j].Path) {
				return layout, failf("profile targets %s and %s overlap", path, layout.Targets[j].Path)
			}
		}
	}
	return layout, nil
}

func resolveProfilePath(path string) string {
	// Resolve existing ancestors so symlinked provider roots also participate
	// in conflict detection. Missing leaf paths remain relative to those roots.
	var tail []string
	for {
		if resolved, err := filepath.EvalSymlinks(path); err == nil {
			for i := len(tail) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, tail[i])
			}
			return resolved
		}
		parent := filepath.Dir(path)
		if parent == path {
			return path
		}
		tail = append(tail, filepath.Base(path))
		path = parent
	}
}

func profilePathWithin(parent, child string) bool {
	if os.PathSeparator == '\\' {
		parent, child = strings.ToLower(parent), strings.ToLower(child)
	}
	relative, err := filepath.Rel(parent, child)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator))
}

func pathsOverlap(a, b string) bool {
	a, b = resolveProfilePath(a), resolveProfilePath(b)
	return profilePathWithin(a, b) || profilePathWithin(b, a)
}

func loadProfileState(layout profileLayout) (*profileState, error) {
	state, err := readProfileState(layout.Provider, layout.Backup)
	if err != nil || state == nil {
		return state, err
	}
	if len(state.Snapshots) != len(layout.Targets) {
		return nil, failf("profile backup does not match the provider layout")
	}
	if state.Scope != layout.Scope || state.Project != layout.Project {
		return nil, failf("profile backup does not match the selected scope and project directory")
	}
	for i, snapshot := range state.Snapshots {
		if snapshot.profileTarget != layout.Targets[i] {
			return nil, failf("provider paths changed since installation; restore the original CODEX_HOME/CLAUDE_CONFIG_DIR before uninstalling")
		}
	}
	return state, nil
}

// Locate ownership records without validating a provider's current layout.
// An inactive provider may have overlapping symlinks, while an active provider
// still owns its recorded paths even when environment overrides have changed.
func profileBackupDirectory(provider string) (string, error) {
	switch provider {
	case skillProviderAgents, skillProviderClaude, skillProviderCodex:
	default:
		return "", failf("unsupported profile provider %q", provider)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Abs(filepath.Join(home, "cysu", "backup", "profiles", strings.ToLower(provider)))
}

func loadStoredProfileState(provider string) (*profileState, error) {
	backup, err := profileBackupDirectory(provider)
	if err != nil {
		return nil, err
	}
	return readProfileState(provider, backup)
}

func requiredStateFields(raw []byte, names ...string) bool {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return false
	}
	for _, name := range names {
		value, exists := fields[name]
		if !exists || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return false
		}
	}
	return true
}

// Missing/damaged manifests and incomplete snapshot payloads fail closed.
// Never infer that a backup is unowned simply because state.json is absent.
func readProfileState(provider, backup string) (*profileState, error) {
	info, err := os.Lstat(backup)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	invalid := func() (*profileState, error) {
		return nil, failf("invalid or incomplete profile backup at %s; preserve it and repair its state before retrying", backup)
	}
	if !info.IsDir() {
		return invalid()
	}
	statePath := filepath.Join(backup, "state.json")
	info, err = os.Lstat(statePath)
	if os.IsNotExist(err) {
		return nil, failf("profile backup %s exists without a valid state file; preserve it before retrying", backup)
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return invalid()
	}
	raw, err := os.ReadFile(statePath)
	if err != nil {
		return nil, err
	}
	var state profileState
	kinds := []string{"skills", "agents", "config"}
	if provider == skillProviderCodex {
		kinds = append(kinds, "legacy-skills")
	}
	if !requiredStateFields(raw, "version", "provider", "profile", "snapshots") ||
		json.Unmarshal(raw, &state) != nil || state.Provider != provider ||
		strings.TrimSpace(state.Profile) == "" || state.Profile != strings.TrimSpace(state.Profile) || len(state.Snapshots) != len(kinds) {
		return invalid()
	}
	if !validProfileStateScope(&state, backup, raw) {
		return invalid()
	}
	var wire struct {
		Snapshots []json.RawMessage `json:"snapshots"`
	}
	if json.Unmarshal(raw, &wire) != nil {
		return invalid()
	}
	for i, snapshot := range state.Snapshots {
		if !requiredStateFields(wire.Snapshots[i], "path", "kind", "existed", "mode") ||
			snapshot.Kind != kinds[i] || !filepath.IsAbs(snapshot.Path) || snapshot.Path != filepath.Clean(snapshot.Path) ||
			snapshot.Mode&^os.FileMode(0o777) != 0 || pathsOverlap(snapshot.Path, backup) {
			return invalid()
		}
		for j := 0; j < i; j++ {
			if pathsOverlap(snapshot.Path, state.Snapshots[j].Path) {
				return invalid()
			}
		}
		payload, err := os.Lstat(filepath.Join(backup, fmt.Sprint(i)))
		if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		if snapshot.Existed != (err == nil) {
			return invalid()
		}
		if snapshot.Existed {
			if snapshot.Kind == "config" {
				if !payload.Mode().IsRegular() {
					return invalid()
				}
			} else if !payload.IsDir() && payload.Mode()&os.ModeSymlink == 0 {
				return invalid()
			}
		}
	}
	return &state, nil
}

func saveProfileState(layout profileLayout, state *profileState) error {
	raw, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return writePrivateFile(filepath.Join(layout.Backup, "state.json"), append(raw, '\n'))
}

// Existing backups belong to the old skills commands and must be restored by
// those commands before a profile can take ownership of the same files.
func checkProfileOwnership(layout profileLayout) error {
	if err := checkStoredProfileOwnership(layout); err != nil {
		return err
	}
	for _, provider := range []string{skillProviderAgents, skillProviderClaude, skillProviderCodex} {
		skills, err := providerSkillsDirectory(provider)
		if err != nil {
			return err
		}
		backup, err := providerSkillsBackupDirectory(provider)
		if err != nil {
			return err
		}
		if exists, err := pathExists(backup); err != nil {
			return err
		} else if exists {
			for _, target := range layout.Targets {
				if pathsOverlap(target.Path, skills) {
					return failf("an earlier skills backup exists at %s; run cysu skills uninstall --provider %s first", backup, provider)
				}
			}
		}
	}
	return nil
}

func checkStoredProfileOwnership(layout profileLayout) error {
	owners, err := storedProfileOwners(layout.Backup)
	if err != nil {
		return err
	}
	for _, state := range owners {
		for _, target := range layout.Targets {
			for _, snapshot := range state.Snapshots {
				if pathsOverlap(target.Path, snapshot.Path) {
					return failf("%s %s profile %q owns %s; %s before changing %s", state.Scope, state.Provider, state.Profile, snapshot.Path, profileOwnerRestoreHint(state), layout.Provider)
				}
			}
		}
	}
	return nil
}

func rejectActiveProfile(provider string) error {
	skills, err := providerSkillsDirectory(provider)
	if err != nil {
		return err
	}
	globalBackup, err := profileBackupDirectory(provider)
	if err != nil {
		return err
	}
	if pathsOverlap(skills, filepath.Dir(filepath.Dir(globalBackup))) {
		return failf("skills directory %s overlaps the backup directory; restore the provider directory override before retrying", skills)
	}
	owners, err := storedProfileOwners("")
	if err != nil {
		return err
	}
	for _, state := range owners {
		for _, target := range state.Snapshots {
			if pathsOverlap(skills, target.Path) {
				return failf("%s %s profile %q owns these skills; %s first", state.Scope, state.Provider, state.Profile, profileOwnerRestoreHint(state))
			}
		}
	}
	return nil
}

func backupProfile(layout profileLayout, profile string) (*profileState, error) {
	if err := os.MkdirAll(filepath.Dir(layout.Backup), 0o700); err != nil {
		return nil, err
	}
	stage, err := os.MkdirTemp(filepath.Dir(layout.Backup), ".profile-backup-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(stage)
	state := &profileState{Version: 1, Provider: layout.Provider, Profile: profile}
	if layout.Scope == profileScopeProject {
		state.Version, state.Scope, state.Project = 2, layout.Scope, layout.Project
	}
	for i, target := range layout.Targets {
		info, err := os.Lstat(target.Path)
		snapshot := profileSnapshot{profileTarget: target}
		if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		if err == nil {
			snapshot.Existed = true
			snapshot.Mode = info.Mode().Perm()
			if err := copyProfilePath(target.Path, filepath.Join(stage, fmt.Sprint(i))); err != nil {
				return nil, err
			}
		}
		state.Snapshots = append(state.Snapshots, snapshot)
	}
	stagedLayout := layout
	stagedLayout.Backup = stage
	if err := saveProfileState(stagedLayout, state); err != nil {
		return nil, err
	}
	if err := os.Rename(stage, layout.Backup); err != nil {
		return nil, err
	}
	return state, nil
}

func copyProfilePath(source, target string) error {
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if info.Mode().IsRegular() {
		return copyRegularFile(source, target, info.Mode().Perm())
	}
	return copyPath(source, target)
}

func writePrivateFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".cysu-write-")
	if err != nil {
		return err
	}
	tmp := file.Name()
	defer os.Remove(tmp)
	_, writeErr := file.Write(data)
	if writeErr == nil {
		writeErr = file.Sync()
	}
	if err := errors.Join(writeErr, file.Close()); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

type profileChange struct {
	Target profileTarget
	Files  map[string][]byte // directory contents
	Data   []byte            // config file
	Source string            // restore from a snapshot
	Remove bool              // originally absent
	Mode   os.FileMode
}

// Each new target and its rollback path are on the same filesystem as that
// target. Preparation finishes before any live paths are renamed. Successful
// activation consumes the temporary copies; failure restores every old target.
func applyProfile(plan []profileChange) error {
	return applyProfileWithRename(plan, os.Rename)
}

func applyProfileWithRename(plan []profileChange, rename func(string, string) error) error {
	type swap struct {
		change profileChange
		dir    string
		old    bool
		new    bool
	}
	var swaps []*swap
	keep := false
	defer func() {
		if !keep {
			for _, item := range swaps {
				_ = os.RemoveAll(item.dir)
			}
		}
	}()
	for _, change := range plan {
		if err := validateProfileTarget(change.Target); err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(change.Target.Path), 0o700); err != nil {
			return err
		}
		dir, err := os.MkdirTemp(filepath.Dir(change.Target.Path), ".cysu-profile-")
		if err != nil {
			return err
		}
		item := &swap{change: change, dir: dir}
		swaps = append(swaps, item)
		stage := filepath.Join(dir, "new")
		switch {
		case change.Remove:
		case change.Source != "":
			if err := copyProfilePath(change.Source, stage); err != nil {
				return err
			}
		case change.Target.Kind == "config":
			if err := os.WriteFile(stage, change.Data, 0o600); err != nil {
				return err
			}
			if change.Mode != 0 {
				if err := os.Chmod(stage, change.Mode); err != nil {
					return err
				}
			}
		default:
			if err := os.Mkdir(stage, 0o700); err != nil {
				return err
			}
			for name, data := range change.Files {
				if filepath.IsAbs(name) || strings.HasPrefix(filepath.Clean(name), "..") {
					return failf("invalid staged profile path")
				}
				if err := writePrivateFile(filepath.Join(stage, name), data); err != nil {
					return err
				}
			}
		}
	}
	rollback := func(cause error) error {
		var failures []error
		for i := len(swaps) - 1; i >= 0; i-- {
			item := swaps[i]
			if item.new {
				if err := rename(item.change.Target.Path, filepath.Join(item.dir, "new")); err != nil {
					failures = append(failures, fmt.Errorf("%s: %w", item.change.Target.Path, err))
					continue
				}
			}
			if item.old {
				if err := rename(filepath.Join(item.dir, "old"), item.change.Target.Path); err != nil {
					failures = append(failures, fmt.Errorf("%s: %w", item.change.Target.Path, err))
				}
			}
		}
		if len(failures) != 0 {
			keep = true
			var recovery []string
			for _, item := range swaps {
				recovery = append(recovery, item.dir)
			}
			return failf("%v; rollback failed: %v; recovery files retained in %s", cause, errors.Join(failures...), strings.Join(recovery, ", "))
		}
		return cause
	}
	for _, item := range swaps {
		exists, err := pathExists(item.change.Target.Path)
		if err != nil {
			return rollback(err)
		}
		if exists {
			if err := rename(item.change.Target.Path, filepath.Join(item.dir, "old")); err != nil {
				return rollback(err)
			}
			item.old = true
		}
		if !item.change.Remove {
			if err := rename(filepath.Join(item.dir, "new"), item.change.Target.Path); err != nil {
				return rollback(err)
			}
			item.new = true
		}
	}
	return nil
}

func validateProfileTarget(target profileTarget) error {
	info, err := os.Lstat(target.Path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if target.Kind == "config" {
		if !info.Mode().IsRegular() {
			return failf("configuration %s must be a regular file", target.Path)
		}
		return nil
	}
	return requireDirectory(target.Path, "profile directory")
}

// Read optional configuration without exposing credentials in an error.
func readProfileConfig(path string) ([]byte, error) {
	if err := validateProfileTarget(profileTarget{Path: path, Kind: "config"}); err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	return raw, err
}

func prepareProfileRestore(layout profileLayout, state *profileState) ([]profileChange, error) {
	var plan []profileChange
	for i, snapshot := range state.Snapshots {
		change := profileChange{Target: snapshot.profileTarget, Remove: !snapshot.Existed}
		source := filepath.Join(layout.Backup, fmt.Sprint(i))
		if snapshot.Kind == "config" {
			var original []byte
			if snapshot.Existed {
				var err error
				original, err = readProfileConfig(source)
				if err != nil {
					return nil, err
				}
				if original == nil {
					return nil, failf("missing configuration backup at %s", source)
				}
			}
			current, err := readProfileConfig(snapshot.Path)
			if err != nil {
				return nil, err
			}
			change.Data, err = restoreProfileConfig(layout.Provider, current, original)
			if err != nil {
				return nil, err
			}
			change.Remove = !snapshot.Existed && len(bytes.TrimSpace(change.Data)) == 0
			change.Mode = snapshot.Mode
		} else if snapshot.Existed {
			if exists, err := pathExists(source); err != nil {
				return nil, err
			} else if !exists {
				return nil, failf("missing profile backup at %s", source)
			}
			change.Source = source
		}
		plan = append(plan, change)
	}
	return plan, nil
}
