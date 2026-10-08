package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/gofrs/flock"
	"gopkg.in/yaml.v3"
)

var profileEditFiles = map[string]string{"skills": "SKILL.md", "agents": "AGENT.md", "mcp": "settings.json", "prompts": "AGENTS.md"}
var profileEditKinds = []string{"skills", "agents", "mcp", "prompts"}

type profileEditItem struct {
	Kind string
	Name string
	ID   string
	Doc  map[string]any
}

type profileEditState struct {
	Profile   string            `json:"profile"`
	Endpoint  string            `json:"endpoint"`
	Directory string            `json:"directory"`
	IDs       map[string]string `json:"ids"`
}

// profileEditPaths confines portable profile names to the user's editing directory.
func profileEditPaths(profile string) (string, string, error) {
	name, err := safeSkillDirectoryName(profile)
	if err != nil || name != profile {
		return "", "", failf("invalid profile directory name %q", profile)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", "", err
	}
	config, err := configPath()
	if err != nil {
		return "", "", err
	}
	root := filepath.Join(home, "cysu", "profiles", profile)
	key := sha256.Sum256([]byte(resolveBaseURL() + "\n" + root))
	state := filepath.Join(filepath.Dir(config), "profile-edits", fmt.Sprintf("%x.json", key))
	for _, path := range []string{root, state} {
		if err := profileEditNoLinks(path); err != nil {
			return "", "", err
		}
	}
	return root, state, nil
}

// profileEditNoLinks prevents reading or replacing a symlinked checkout or file.
func profileEditNoLinks(path string) error {
	for {
		info, err := os.Lstat(path)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		if err == nil && info.Mode()&os.ModeSymlink != 0 {
			return failf("profile editing does not follow symlinks: %s", path)
		}
		parent := filepath.Dir(path)
		if parent == path {
			return nil
		}
		path = parent
	}
}

// lockProfileEdit serializes edits and saves of this checkout using its config directory.
func lockProfileEdit(statePath string) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(statePath), 0700); err != nil {
		return nil, err
	}
	lockPath := statePath + ".lock"
	if err := profileEditNoLinks(lockPath); err != nil {
		return nil, err
	}
	lock := flock.New(lockPath, flock.SetPermissions(0600))
	ok, err := lock.TryLock()
	if err != nil || !ok {
		_ = lock.Close()
		if err != nil {
			return nil, err
		}
		return nil, failf("another edit or save is using this profile checkout")
	}
	return func() { _ = lock.Close() }, nil
}

// fetchProfileEditItems exports the same whole-profile snapshot used for installation.
func fetchProfileEditItems(profile string) ([]profileEditItem, error) {
	raw, err := send("GET", apiPath("profiles", profile), nil)
	if err != nil {
		return nil, err
	}
	var snapshot map[string]json.RawMessage
	if json.Unmarshal(raw, &snapshot) != nil {
		return nil, failf("invalid profile response")
	}
	var id string
	if json.Unmarshal(snapshot["id"], &id) != nil || id != profile {
		return nil, failf("profile response does not match %q", profile)
	}
	var items []profileEditItem
	for _, kind := range profileEditKinds {
		var rows []map[string]any
		if json.Unmarshal(snapshot[kind], &rows) != nil || rows == nil {
			return nil, failf("invalid %s response", kind)
		}
		seen := map[string]bool{}
		for _, doc := range rows {
			name, _ := doc["name"].(string)
			if !slugNameValid(name) || seen[name] {
				return nil, failf("invalid or duplicate %s name %q", kind, name)
			}
			seen[name] = true
			items = append(items, profileEditItem{Kind: kind, Name: name, ID: name, Doc: doc})
		}
	}
	return items, nil
}

// renderProfileEditItem uses YAML metadata and an unmodified Markdown body.
func renderProfileEditItem(item profileEditItem) ([]byte, error) {
	doc := map[string]any{}
	for k, v := range item.Doc {
		if k != "profile" {
			doc[k] = v
		}
	}
	if item.Kind == "mcp" {
		return json.MarshalIndent(doc, "", "  ")
	}
	content, ok := doc["content"].(string)
	if !ok {
		return nil, failf("%s/%s has no content", item.Kind, item.Name)
	}
	delete(doc, "content")
	header, err := yaml.Marshal(doc)
	if err != nil {
		return nil, err
	}
	return []byte("---\n" + string(header) + "---\n" + content), nil
}

// writeProfileEditState records IDs atomically, outside the user-editable files.
func writeProfileEditState(path string, state profileEditState) error {
	if err := profileEditNoLinks(path); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".profile-edit-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(raw); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

// editProfile downloads one existing remote profile and prints its local path.
func editProfile(profile string) error {
	result, err := checkoutProfile(profile)
	if err != nil {
		return err
	}
	return printJSON(result)
}

// editAllProfiles pulls only profiles returned by the API, leaving local-only
// directories alone. Each checkout follows the same rules as single-profile edit.
func editAllProfiles() error {
	raw, err := send("GET", apiPath("profiles"), nil)
	if err != nil {
		return err
	}
	var profiles []struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(raw, &profiles) != nil || profiles == nil {
		return failf("invalid profiles response: expected a JSON array")
	}
	seen := map[string]bool{}
	// Reject unsafe or colliding names before replacing any local checkout.
	for _, profile := range profiles {
		if _, _, err := profileEditPaths(profile.ID); err != nil {
			return err
		}
		key := strings.ToLower(profile.ID)
		if seen[key] {
			return failf("duplicate profile directory name %q", profile.ID)
		}
		seen[key] = true
	}
	results := make([]map[string]any, 0, len(profiles))
	for _, profile := range profiles {
		result, err := checkoutProfile(profile.ID)
		if err != nil {
			return failf("pulling profile %q failed after %d completed profiles; earlier local checkouts may have been replaced: %v", profile.ID, len(results), err)
		}
		results = append(results, result)
	}
	return printJSON(map[string]any{"success": true, "profiles": results})
}

// checkoutProfile validates a fresh checkout before discarding the previous local edits.
func checkoutProfile(profile string) (map[string]any, error) {
	root, statePath, err := profileEditPaths(profile)
	if err != nil {
		return nil, err
	}
	unlock, err := lockProfileEdit(statePath)
	if err != nil {
		return nil, err
	}
	defer unlock()
	items, err := fetchProfileEditItems(profile)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(filepath.Dir(root), 0700); err != nil {
		return nil, err
	}
	stage, err := os.MkdirTemp(filepath.Dir(root), ".profile-edit-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(stage)
	state := profileEditState{profile, resolveBaseURL(), root, map[string]string{}}
	for _, kind := range profileEditKinds {
		if err = os.Mkdir(filepath.Join(stage, kind), 0700); err != nil {
			return nil, err
		}
	}
	for _, item := range items {
		raw, err := renderProfileEditItem(item)
		if err != nil {
			return nil, err
		}
		dir := filepath.Join(stage, item.Kind, item.Name)
		if err = os.Mkdir(dir, 0700); err != nil {
			return nil, err
		}
		if err = os.WriteFile(filepath.Join(dir, profileEditFiles[item.Kind]), raw, 0600); err != nil {
			return nil, err
		}
		state.IDs[item.Kind+"/"+item.Name] = item.ID
	}
	// Check remote documents with the same validation used before saving.
	if _, err = readProfileEditItems(stage, profile); err != nil {
		return nil, err
	}
	if info, statErr := os.Lstat(root); statErr == nil {
		if !info.IsDir() {
			return nil, failf("profile checkout is not a directory: %s", root)
		}
		if err = os.RemoveAll(root); err != nil {
			return nil, err
		}
	} else if !os.IsNotExist(statErr) {
		return nil, statErr
	}
	if err = os.Rename(stage, root); err != nil {
		return nil, err
	}
	if err = writeProfileEditState(statePath, state); err != nil {
		return nil, failf("profile exported to %s but saving checkout state failed: %v", root, err)
	}
	return map[string]any{"success": true, "profile": profile, "directory": root, "items": len(items)}, nil
}

// readProfileEditItems validates the entire local source before any remote mutation.
func readProfileEditItems(root, profile string) ([]profileEditItem, error) {
	if err := profileEditNoLinks(root); err != nil {
		return nil, err
	}
	info, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, failf("profile checkout is not a directory")
	}
	var items []profileEditItem
	for _, kind := range profileEditKinds {
		folder := filepath.Join(root, kind)
		if err = profileEditNoLinks(folder); err != nil {
			return nil, err
		}
		dirs, err := os.ReadDir(folder)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		seen := map[string]bool{}
		for _, dir := range dirs {
			if strings.HasPrefix(dir.Name(), ".") {
				continue
			}
			if !dir.IsDir() {
				return nil, failf("expected an item directory: %s", filepath.Join(folder, dir.Name()))
			}
			key := strings.ToLower(dir.Name())
			if seen[key] {
				return nil, failf("duplicate %s directory: %s", kind, dir.Name())
			}
			seen[key] = true
			safe, err := safeSkillDirectoryName(dir.Name())
			if err != nil || safe != dir.Name() {
				return nil, failf("invalid item directory: %s", dir.Name())
			}
			path := filepath.Join(folder, dir.Name(), profileEditFiles[kind])
			if err = profileEditNoLinks(path); err != nil {
				return nil, err
			}
			info, err := os.Stat(path)
			if err != nil {
				return nil, failf("reading %s: %v; remove the item directory to delete it", path, err)
			}
			if !info.Mode().IsRegular() {
				return nil, failf("not a regular file: %s", path)
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				return nil, err
			}
			if !utf8.Valid(raw) {
				return nil, failf("invalid UTF-8: %s", path)
			}
			doc := map[string]any{}
			if kind == "mcp" {
				if json.Unmarshal(raw, &doc) != nil || doc == nil {
					return nil, failf("expected a JSON object: %s", path)
				}
				// The directory supplies the name when settings omit it.
				if _, ok := doc["name"]; !ok {
					doc["name"] = dir.Name()
				}
			} else {
				text := strings.ReplaceAll(string(bytes.TrimPrefix(raw, []byte{0xef, 0xbb, 0xbf})), "\r\n", "\n")
				if !strings.HasPrefix(text, "---\n") {
					return nil, failf("missing YAML front matter: %s", path)
				}
				end := strings.Index(text[4:], "\n---\n")
				if end < 0 {
					return nil, failf("missing closing YAML front matter: %s", path)
				}
				if yaml.Unmarshal([]byte(text[4:4+end]), &doc) != nil || doc == nil {
					return nil, failf("invalid YAML metadata: %s", path)
				}
				doc["content"] = text[4+end+5:]
			}
			if v, ok := doc["profile"]; ok && v != profile {
				return nil, failf("profile in %s must match --profile %q", path, profile)
			}
			doc["profile"] = profile
			if err = validateProfileEditDocument(kind, dir.Name(), doc, path); err != nil {
				return nil, err
			}
			items = append(items, profileEditItem{Kind: kind, Name: dir.Name(), Doc: doc})
		}
	}
	return items, nil
}

// validateProfileEditDocument mirrors the API's required fields and MCP variants.
func validateProfileEditDocument(kind, name string, doc map[string]any, path string) error {
	if kind == "agents" {
		_, err := validateAgentDocument(doc, path)
		if err != nil {
			return err
		}
	}
	if kind == "skills" {
		description, ok := doc["description"].(string)
		if !ok || strings.TrimSpace(description) == "" {
			return failf("skill %s requires description", name)
		}
	}
	if kind == "prompts" || kind == "skills" {
		_, err := validatePromptDocument(doc, path, true)
		if err != nil {
			return err
		}
	}
	if doc["name"] != name {
		return failf("name in %s must match directory %q", path, name)
	}
	if !slugNameValid(name) {
		return failf("name in %s must be a lowercase hyphenated slug", path)
	}
	if kind != "mcp" {
		return nil
	}
	allowed := map[string]bool{"name": true, "type": true, "url": true, "profile": true}
	for _, field := range []string{"type", "url", "profile"} {
		v, ok := doc[field].(string)
		if !ok || strings.TrimSpace(v) == "" {
			return failf("MCP settings %s need non-empty %s", path, field)
		}
	}
	switch doc["type"] {
	case "stdio":
		allowed["command"] = true
		allowed["args"] = true
		command, ok := doc["command"].(string)
		if !ok || strings.TrimSpace(command) == "" {
			return failf("MCP settings %s need a command", path)
		}
		args, ok := doc["args"].([]any)
		if !ok {
			return failf("MCP args must be a string array: %s", path)
		}
		for _, arg := range args {
			if _, ok := arg.(string); !ok {
				return failf("MCP args must be strings: %s", path)
			}
		}
	case "http":
		allowed["headers"] = true
		u, err := url.Parse(doc["url"].(string))
		if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
			return failf("MCP URL must be HTTP(S): %s", path)
		}
		headers, ok := doc["headers"].(map[string]any)
		if !ok {
			return failf("MCP headers must be an object: %s", path)
		}
		for key, v := range headers {
			value, ok := v.(string)
			if !ok || !profileHeaderName.MatchString(key) || strings.ContainsAny(value, "\r\n\x00") {
				return failf("invalid MCP header in %s", path)
			}
		}
	default:
		return failf("MCP type must be http or stdio: %s", path)
	}
	for key := range doc {
		if !allowed[key] {
			return failf("unsupported MCP field %q in %s", key, path)
		}
	}
	return nil
}

// saveEditedProfile prints the result of saving a single local profile.
func saveEditedProfile(profile string) error {
	result, err := reconcileEditedProfile(profile)
	if err != nil {
		return err
	}
	return printJSON(result)
}

// saveAllEditedProfiles saves immediate profile directories in name order.
func saveAllEditedProfiles() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	root := filepath.Join(home, "cysu", "profiles")
	if err := profileEditNoLinks(root); err != nil {
		return err
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	profiles := []string{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		// Validate every directory name before starting remote changes.
		if _, _, err := profileEditPaths(entry.Name()); err != nil {
			return err
		}
		profiles = append(profiles, entry.Name())
	}
	results := make([]map[string]any, 0, len(profiles))
	for _, profile := range profiles {
		result, err := reconcileEditedProfile(profile)
		if err != nil {
			return failf("saving profile %q failed after %d completed profiles; earlier changes may have applied: %v", profile, len(results), err)
		}
		results = append(results, result)
	}
	return printJSON(map[string]any{"success": true, "profiles": results})
}

// reconcileEditedProfile sends one complete replacement; the server commits it atomically.
func reconcileEditedProfile(profile string) (map[string]any, error) {
	root, statePath, err := profileEditPaths(profile)
	if err != nil {
		return nil, err
	}
	unlock, err := lockProfileEdit(statePath)
	if err != nil {
		return nil, err
	}
	defer unlock()
	local, err := readProfileEditItems(root, profile)
	if err != nil {
		return nil, err
	}
	body := map[string]any{"id": profile}
	for _, kind := range profileEditKinds {
		body[kind] = []map[string]any{}
	}
	for _, item := range local {
		delete(item.Doc, "profile")
		body[item.Kind] = append(body[item.Kind].([]map[string]any), item.Doc)
	}
	raw, err := send("PUT", apiPath("profiles", profile), body)
	if err != nil {
		return nil, err
	}
	var result struct {
		ID      string         `json:"id"`
		Changes map[string]int `json:"changes"`
	}
	if json.Unmarshal(raw, &result) != nil || result.ID != profile || result.Changes == nil {
		return nil, failf("invalid profile save response; remote changes may have applied")
	}
	return map[string]any{"success": true, "profile": profile, "directory": root, "changes": result.Changes}, nil
}
