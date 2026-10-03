package main

// skills_install.go installs every skill stored by the gateway into the
// personal skills directory of Codex, Claude Code, or an Agent Skills host.
// Existing user skills are moved to a provider-specific directory under
// ~/cysu/backup first, allowing `cysu skills uninstall` to restore the original
// directory exactly.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"gopkg.in/yaml.v3"
)

const (
	skillProviderOpenAI = "OPENAI"
	skillProviderCodex  = "CODEX"
	skillProviderClaude = "CLAUDE"
	skillProviderAgents = "AGENTS"
)

// providerFlag selects where CYSU skills are installed. An explicit
// flag takes precedence; the fallback reads CYSU_PROVIDER.
var providerFlag = flagSpec{
	name:        "provider",
	placeholder: "provider",
	usage:       "provider: AGENTS, CLAUDE, or CODEX (overrides CYSU_PROVIDER)",
	fallback:    providerFromEnvironment,
}

// storedSkill is one entry returned by GET /skills.
type storedSkill struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Content     string `json:"content"`
	Profile     string `json:"profile"`
}

// installedSkill is one installed file in the command's JSON result.
type installedSkill struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Output string `json:"output"`
	Size   int64  `json:"size"`
}

// preparedSkill holds a validated file until every gateway entry has been
// checked. Validation therefore cannot leave a partially installed set.
type preparedSkill struct {
	storedSkill
	path string
	data []byte
}

// providerFromEnvironment supplies --provider from CYSU_PROVIDER.
func providerFromEnvironment() (string, error) {
	provider := strings.TrimSpace(os.Getenv("CYSU_PROVIDER"))
	if provider == "" {
		return "", cliError{
			message: "missing provider: set CYSU_PROVIDER to AGENTS, CLAUDE, or CODEX, or pass --provider <provider>",
			exit:    exitUsage,
		}
	}
	return normalizeSkillProvider(provider)
}

// normalizeSkillProvider validates and canonicalises a provider value.
func normalizeSkillProvider(provider string) (string, error) {
	provider = strings.ToUpper(strings.TrimSpace(provider))
	// Keep the previous CLI spelling as an alias.
	if provider == skillProviderOpenAI {
		provider = skillProviderCodex
	}
	switch provider {
	case skillProviderCodex, skillProviderClaude, skillProviderAgents:
		return provider, nil
	default:
		return "", cliError{
			message: fmt.Sprintf("unsupported provider %q: use AGENTS, CLAUDE, or CODEX", provider),
			exit:    exitUsage,
		}
	}
}

// installSkills fetches all saved skills, validates and stages the complete
// set, backs up the existing provider directory, and installs only CYSU skills.
func installSkills(providerValue string) error {
	provider, err := normalizeSkillProvider(providerValue)
	if err != nil {
		return err
	}
	unlock, err := lockProfileChanges()
	if err != nil {
		return err
	}
	defer unlock()
	if err := rejectActiveProfile(provider); err != nil {
		return err
	}

	raw, err := send("GET", apiPath("skills"), nil)
	if err != nil {
		return err
	}
	skills, err := decodeStoredSkills(raw)
	if err != nil {
		return err
	}

	dir, err := providerSkillsDirectory(provider)
	if err != nil {
		return err
	}
	backup, err := providerSkillsBackupDirectory(provider)
	if err != nil {
		return err
	}
	prepared, err := prepareSkills(skills, dir)
	if err != nil {
		return err
	}

	stage, err := stageSkills(dir, prepared)
	if err != nil {
		return err
	}
	defer func() {
		if stage != "" {
			_ = os.RemoveAll(stage)
		}
	}()

	backupCreated, err := ensureSkillsBackup(dir, backup)
	if err != nil {
		return err
	}
	if err := replaceSkillsDirectory(dir, stage); err != nil {
		if backupCreated {
			if rollbackErr := restoreSkillsBackup(dir, backup); rollbackErr != nil {
				return failf("installing CYSU skills: %v; restoring original skills: %v", err, rollbackErr)
			}
		}
		return err
	}
	stage = ""

	installed := make([]installedSkill, 0, len(prepared))
	for _, skill := range prepared {
		installed = append(installed, installedSkill{
			ID:     skill.ID,
			Name:   skill.Name,
			Output: skill.path,
			Size:   int64(len(skill.data)),
		})
	}

	return printJSON(struct {
		Success   bool             `json:"success"`
		Provider  string           `json:"provider"`
		Directory string           `json:"directory"`
		Backup    string           `json:"backup"`
		Installed int              `json:"installed"`
		Skills    []installedSkill `json:"skills"`
	}{true, provider, dir, backup, len(installed), installed})
}

// uninstallSkills removes the CYSU-only directory and restores the provider's
// original directory from its backup. It is intentionally a local command.
func uninstallSkills(providerValue string) error {
	provider, err := normalizeSkillProvider(providerValue)
	if err != nil {
		return err
	}
	unlock, err := lockProfileChanges()
	if err != nil {
		return err
	}
	defer unlock()
	if err := rejectActiveProfile(provider); err != nil {
		return err
	}
	dir, err := providerSkillsDirectory(provider)
	if err != nil {
		return err
	}
	backup, err := providerSkillsBackupDirectory(provider)
	if err != nil {
		return err
	}
	if exists, err := pathExists(backup); err != nil {
		return failf("checking skills backup %s: %v", backup, err)
	} else if !exists {
		return failf("no %s skills backup exists at %s", provider, backup)
	}
	if err := restoreSkillsBackup(dir, backup); err != nil {
		return err
	}

	return printJSON(struct {
		Success   bool   `json:"success"`
		Provider  string `json:"provider"`
		Directory string `json:"directory"`
		Backup    string `json:"backup"`
		Restored  bool   `json:"restored"`
	}{true, provider, dir, backup, true})
}

// decodeStoredSkills reads the bare skill array described by the gateway API.
func decodeStoredSkills(raw []byte) ([]storedSkill, error) {
	var skills []storedSkill
	if err := json.Unmarshal(raw, &skills); err != nil {
		return nil, failf("unexpected skills response: %s", summarize(raw))
	}
	if skills == nil {
		skills = []storedSkill{}
	}
	for i, skill := range skills {
		if strings.TrimSpace(skill.ID) == "" {
			return nil, failf("unexpected skills response: entry %d has no id", i)
		}
	}
	return skills, nil
}

// providerSkillsDirectory returns the personal skills directory for provider.
// Codex uses CODEX_HOME when set, Claude Code uses CLAUDE_CONFIG_DIR, and the
// provider-neutral Agent Skills location is ~/.agents/skills.
func providerSkillsDirectory(provider string) (string, error) {
	var root string
	switch provider {
	case skillProviderCodex:
		root = strings.TrimSpace(os.Getenv("CODEX_HOME"))
		if root == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return "", failf("finding the home directory: %v", err)
			}
			root = filepath.Join(home, ".codex")
		}
	case skillProviderClaude:
		root = strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR"))
		if root == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return "", failf("finding the home directory: %v", err)
			}
			root = filepath.Join(home, ".claude")
		}
	case skillProviderAgents:
		home, err := os.UserHomeDir()
		if err != nil {
			return "", failf("finding the home directory: %v", err)
		}
		root = filepath.Join(home, ".agents")
	default:
		return "", failf("unsupported skill provider %q", provider)
	}

	abs, err := filepath.Abs(root)
	if err != nil {
		return "", failf("resolving the %s skills directory: %v", provider, err)
	}
	return filepath.Join(abs, "skills"), nil
}

// providerSkillsBackupDirectory returns the provider-specific backup location
// beneath ~/cysu/backup, preventing provider backups from colliding.
func providerSkillsBackupDirectory(provider string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", failf("finding the home directory: %v", err)
	}
	// Preserve the old OPENAI backup location for existing installations.
	if provider == skillProviderCodex {
		provider = skillProviderOpenAI
	}
	abs, err := filepath.Abs(filepath.Join(home, "cysu", "backup", strings.ToLower(provider)))
	if err != nil {
		return "", failf("resolving the %s skills backup directory: %v", provider, err)
	}
	return abs, nil
}

// prepareSkills validates and renders the entire skill list before any file is
// written. Duplicate names are rejected because they target the same folder.
func prepareSkills(skills []storedSkill, dir string) ([]preparedSkill, error) {
	prepared := make([]preparedSkill, 0, len(skills))
	seen := make(map[string]string, len(skills))

	for _, skill := range skills {
		name, err := safeSkillDirectoryName(skill.Name)
		if err != nil {
			return nil, failf("skill %s: %v", skill.ID, err)
		}
		data, err := renderSkillDocument(skillDocument{
			Name:        name,
			Description: skill.Description,
			Content:     skill.Content,
		})
		if err != nil {
			return nil, failf("skill %s: %v", skill.ID, err)
		}
		skill.Name = name

		path := filepath.Join(dir, name, "SKILL.md")
		key := strings.ToLower(filepath.Clean(path))
		if previous, exists := seen[key]; exists {
			return nil, failf("skills %s and %s have the same name %q", previous, skill.ID, name)
		}
		seen[key] = skill.ID
		prepared = append(prepared, preparedSkill{storedSkill: skill, path: path, data: data})
	}

	return prepared, nil
}

// stageSkills writes the validated CYSU skills into a temporary directory next
// to the live skills directory, so the final installation is a directory swap.
func stageSkills(dir string, skills []preparedSkill) (string, error) {
	parent := filepath.Dir(dir)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return "", failf("creating provider directory %s: %v", parent, err)
	}
	stage, err := os.MkdirTemp(parent, ".cysu-skills-install-")
	if err != nil {
		return "", failf("creating skill staging directory: %v", err)
	}
	for _, skill := range skills {
		path := filepath.Join(stage, skill.Name, "SKILL.md")
		if _, err := writeAtomically(path, bytes.NewReader(skill.data)); err != nil {
			_ = os.RemoveAll(stage)
			return "", err
		}
	}
	return stage, nil
}

// ensureSkillsBackup moves the original skills directory into the backup on
// first install. An existing backup is retained across repeat installations.
func ensureSkillsBackup(dir, backup string) (bool, error) {
	backupExists, err := pathExists(backup)
	if err != nil {
		return false, failf("checking skills backup %s: %v", backup, err)
	}
	if backupExists {
		if err := requireDirectory(backup, "skills backup"); err != nil {
			return false, err
		}
		return false, nil
	}

	if err := os.MkdirAll(filepath.Dir(backup), 0o700); err != nil {
		return false, failf("creating skills backup directory: %v", err)
	}
	dirExists, err := pathExists(dir)
	if err != nil {
		return false, failf("checking skills directory %s: %v", dir, err)
	}
	if !dirExists {
		if err := os.Mkdir(backup, 0o700); err != nil {
			return false, failf("creating empty skills backup %s: %v", backup, err)
		}
		return true, nil
	}
	if err := requireDirectory(dir, "skills directory"); err != nil {
		return false, err
	}
	if err := movePath(dir, backup); err != nil {
		return false, failf("backing up skills from %s to %s: %v", dir, backup, err)
	}
	return true, nil
}

// replaceSkillsDirectory swaps a staged CYSU directory into place. When a
// repeat install replaces an earlier CYSU set, the earlier set is recoverable
// until the new directory has been installed successfully.
func replaceSkillsDirectory(dir, stage string) error {
	exists, err := pathExists(dir)
	if err != nil {
		return failf("checking skills directory %s: %v", dir, err)
	}
	var displaced string
	if exists {
		if err := requireDirectory(dir, "skills directory"); err != nil {
			return err
		}
		displaced, err = unusedSiblingPath(dir, ".cysu-skills-previous-")
		if err != nil {
			return err
		}
		if err := movePath(dir, displaced); err != nil {
			return failf("moving previous CYSU skills aside: %v", err)
		}
	}

	if err := movePath(stage, dir); err != nil {
		if displaced != "" {
			if rollbackErr := movePath(displaced, dir); rollbackErr != nil {
				return failf("installing CYSU skills: %v; restoring previous CYSU skills: %v", err, rollbackErr)
			}
		}
		return failf("installing CYSU skills: %v", err)
	}
	if displaced != "" {
		if err := os.RemoveAll(displaced); err != nil {
			return failf("removing replaced CYSU skills %s: %v", displaced, err)
		}
	}
	return nil
}

// restoreSkillsBackup removes the active CYSU set and moves the original
// provider directory back. A failed restore puts the CYSU set back in place.
func restoreSkillsBackup(dir, backup string) error {
	backupExists, err := pathExists(backup)
	if err != nil {
		return failf("checking skills backup %s: %v", backup, err)
	}
	if !backupExists {
		return failf("skills backup does not exist at %s", backup)
	}
	if err := requireDirectory(backup, "skills backup"); err != nil {
		return err
	}

	activeExists, err := pathExists(dir)
	if err != nil {
		return failf("checking skills directory %s: %v", dir, err)
	}
	var displaced string
	if activeExists {
		if err := requireDirectory(dir, "skills directory"); err != nil {
			return err
		}
		displaced, err = unusedSiblingPath(dir, ".cysu-skills-uninstall-")
		if err != nil {
			return err
		}
		if err := movePath(dir, displaced); err != nil {
			return failf("moving installed CYSU skills aside: %v", err)
		}
	}

	if err := movePath(backup, dir); err != nil {
		if displaced != "" {
			if rollbackErr := movePath(displaced, dir); rollbackErr != nil {
				return failf("restoring skills backup: %v; restoring CYSU skills: %v", err, rollbackErr)
			}
		}
		return failf("restoring skills backup: %v", err)
	}
	if displaced != "" {
		if err := os.RemoveAll(displaced); err != nil {
			return failf("removing uninstalled CYSU skills %s: %v", displaced, err)
		}
	}
	return nil
}

// requireDirectory rejects regular files at paths that must represent a skill
// tree. Symlinks are accepted when they resolve to directories and are moved as
// links by the normal same-filesystem rename path.
func requireDirectory(path, label string) error {
	info, err := os.Stat(path)
	if err != nil {
		return failf("checking %s %s: %v", label, path, err)
	}
	if !info.IsDir() {
		return failf("%s %s is not a directory", label, path)
	}
	return nil
}

// pathExists uses Lstat so a broken symlink is still treated as an occupied
// path and never overwritten by a backup or installation.
func pathExists(path string) (bool, error) {
	_, err := os.Lstat(path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

// unusedSiblingPath reserves and releases a unique pathname beside path.
func unusedSiblingPath(path, pattern string) (string, error) {
	tmp, err := os.MkdirTemp(filepath.Dir(path), pattern)
	if err != nil {
		return "", failf("creating temporary skills path: %v", err)
	}
	if err := os.Remove(tmp); err != nil {
		return "", failf("preparing temporary skills path: %v", err)
	}
	return tmp, nil
}

// movePath prefers an atomic rename and falls back to copy-then-remove when
// the skills directory and ~/cysu/backup live on different filesystems.
func movePath(source, target string) error {
	if exists, err := pathExists(target); err != nil {
		return err
	} else if exists {
		return fmt.Errorf("destination already exists: %s", target)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return err
	}
	renameErr := os.Rename(source, target)
	if renameErr == nil {
		return nil
	}
	if err := copyPath(source, target); err != nil {
		return fmt.Errorf("renaming failed: %v; copying failed: %w", renameErr, err)
	}
	trash, err := unusedSiblingPath(source, ".cysu-skills-move-")
	if err != nil {
		_ = os.RemoveAll(target)
		return err
	}
	if err := os.Rename(source, trash); err != nil {
		_ = os.RemoveAll(target)
		return fmt.Errorf("moving copied source aside: %w", err)
	}
	// The move is complete once source has disappeared and target contains the
	// full copy. A cleanup failure only leaves a hidden duplicate beside source;
	// it must not roll back by deleting the sole destination.
	_ = os.RemoveAll(trash)
	return nil
}

// copyPath copies a directory tree without following symlinks. It exists only
// as the cross-filesystem fallback for movePath.
func copyPath(source, target string) error {
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		link, err := os.Readlink(source)
		if err != nil {
			return err
		}
		return os.Symlink(link, target)
	}
	if !info.IsDir() {
		return fmt.Errorf("source %s is not a directory", source)
	}

	if err := os.Mkdir(target, 0o700); err != nil {
		return err
	}
	failed := true
	defer func() {
		if failed {
			_ = os.RemoveAll(target)
		}
	}()

	var directories []struct {
		path string
		mode os.FileMode
	}
	err = filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(source, path)
		if err != nil || rel == "." {
			return err
		}
		destination := filepath.Join(target, rel)
		entryInfo, err := os.Lstat(path)
		if err != nil {
			return err
		}
		switch {
		case entryInfo.Mode()&os.ModeSymlink != 0:
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(link, destination)
		case entryInfo.IsDir():
			if err := os.Mkdir(destination, 0o700); err != nil {
				return err
			}
			directories = append(directories, struct {
				path string
				mode os.FileMode
			}{destination, entryInfo.Mode().Perm()})
			return nil
		case entryInfo.Mode().IsRegular():
			return copyRegularFile(path, destination, entryInfo.Mode().Perm())
		default:
			return fmt.Errorf("unsupported file type at %s", path)
		}
	})
	if err != nil {
		return err
	}
	for i := len(directories) - 1; i >= 0; i-- {
		if err := os.Chmod(directories[i].path, directories[i].mode); err != nil {
			return err
		}
	}
	if err := os.Chmod(target, info.Mode().Perm()); err != nil {
		return err
	}
	failed = false
	return nil
}

// copyRegularFile copies one file and applies its original permission bits.
func copyRegularFile(source, target string, mode os.FileMode) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Chmod(target, mode)
}

// safeSkillDirectoryName accepts a skill name only when it is one portable
// path component, preventing a gateway value from escaping the skills folder.
func safeSkillDirectoryName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("name is empty")
	}
	if name == "." || name == ".." || filepath.IsAbs(name) || filepath.VolumeName(name) != "" ||
		strings.ContainsAny(name, `<>:"/\|?*`) {
		return "", fmt.Errorf("name %q is not a safe directory name", name)
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return "", fmt.Errorf("name %q contains a control character", name)
		}
	}
	if strings.HasSuffix(name, ".") || strings.HasSuffix(name, " ") || windowsReservedName(name) {
		return "", fmt.Errorf("name %q is not portable across operating systems", name)
	}
	return name, nil
}

// windowsReservedName reports names Windows reserves even with an extension.
func windowsReservedName(name string) bool {
	base := strings.ToUpper(strings.SplitN(name, ".", 2)[0])
	switch base {
	case "CON", "PRN", "AUX", "NUL", "COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9",
		"LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9":
		return true
	}
	return false
}

// renderSkillDocument reconstructs the SKILL.md front matter and body stored
// separately by the gateway.
func renderSkillDocument(doc skillDocument) ([]byte, error) {
	doc.Name = strings.TrimSpace(doc.Name)
	doc.Description = strings.TrimSpace(doc.Description)
	doc.Content = strings.TrimSpace(doc.Content)
	if doc.Name == "" {
		return nil, fmt.Errorf("name is empty")
	}
	if doc.Description == "" {
		return nil, fmt.Errorf("description is empty")
	}
	if doc.Content == "" {
		return nil, fmt.Errorf("content is empty")
	}

	frontMatter, err := yaml.Marshal(skillMetadata{Name: doc.Name, Description: doc.Description})
	if err != nil {
		return nil, fmt.Errorf("encoding YAML front matter: %w", err)
	}

	var out bytes.Buffer
	out.WriteString("---\n")
	out.Write(frontMatter)
	out.WriteString("---\n\n")
	out.WriteString(doc.Content)
	out.WriteByte('\n')
	return out.Bytes(), nil
}
