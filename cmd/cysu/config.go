package main

// config.go holds the CLI's current OOB correlation ID. The file lives next
// to the master key (see keyfile.go) and is written atomically so a crash
// cannot leave it half-written.

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
	// OOB is the correlation id of the caller's current out-of-bounds
	// endpoint, saved by `cysu oob create` and read by `cysu oob pull-hits`
	// (see oob.go). A user has at most one at a time, so one field is enough.
	OOB string `json:"oob,omitempty"`
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
// Out-of-bounds endpoint
// ---------------------------------------------------------------------------

// setOOB records id as the correlation id of the caller's current OOB
// endpoint, so `cysu oob pull-hits` knows which endpoint to read.
func setOOB(id string) error {
	cfg, err := readConfig()
	if err != nil {
		return err
	}
	cfg.OOB = id
	_, err = writeConfig(cfg)
	return err
}

// currentOOB returns the saved OOB correlation id, or "" when none has been
// created yet.
func currentOOB() (string, error) {
	cfg, err := readConfig()
	if err != nil {
		return "", err
	}
	return cfg.OOB, nil
}
