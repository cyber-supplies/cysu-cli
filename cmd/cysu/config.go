package main

// config.go holds the CLI's persistent settings: today just the selected
// project. The file lives next to the master key (see keyfile.go) and is
// written atomically so a crash can never leave it half-written.
//
// The project a command acts on is --project <id> when given, otherwise the
// project saved by `cysu projects select`. Every command scoped to a project
// uses projectFlag (projects.go), whose fallback reads the selection.

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const configFileName = "config.json"

// config is the on-disk shape of the settings file.
type config struct {
	Project string `json:"project,omitempty"`
}

// configPath returns the location of the settings file: config.json in the
// same directory as the key file.
func configPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("locating user config dir: %w", err)
	}
	return filepath.Join(dir, "cysu", configFileName), nil
}

// readConfig loads the settings file. A missing file is an empty config.
func readConfig() (config, error) {
	var cfg config
	path, err := configPath()
	if err != nil {
		return cfg, err
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return cfg, fmt.Errorf("reading config file: %w", err)
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return cfg, fmt.Errorf("parsing config file %s: %w", path, err)
	}
	return cfg, nil
}

// writeConfig atomically replaces the settings file with cfg, creating the
// parent directory (mode 0700, shared with the key file) if needed.
func writeConfig(cfg config) (string, error) {
	path, err := configPath()
	if err != nil {
		return "", err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("creating config dir %s: %w", dir, err)
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encoding config: %w", err)
	}
	data = append(data, '\n')

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return "", fmt.Errorf("writing config file: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return "", fmt.Errorf("installing config file: %w", err)
	}
	return path, nil
}

// ---------------------------------------------------------------------------
// Project selection
// ---------------------------------------------------------------------------

// selectProject records id as the project every project-scoped command acts
// on until it is changed. It returns the path of the file that holds it.
func selectProject(id string) (string, error) {
	cfg, err := readConfig()
	if err != nil {
		return "", err
	}
	cfg.Project = id
	return writeConfig(cfg)
}

// currentProject returns the saved selection, or "" when there is none.
func currentProject() (string, error) {
	cfg, err := readConfig()
	if err != nil {
		return "", err
	}
	return cfg.Project, nil
}

// defaultProject is projectFlag's fallback: the selected project, or a usage
// error telling the caller how to pick one.
func defaultProject() (string, error) {
	id, err := currentProject()
	if err != nil {
		return "", err
	}
	if id == "" {
		return "", cliError{
			message: "no project selected: pick one from `cysu projects list`, then pass --project <id> or run `cysu projects select --project <id>`",
			exit:    exitUsage,
		}
	}
	return id, nil
}
