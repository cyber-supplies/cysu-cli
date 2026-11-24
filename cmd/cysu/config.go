package main

// config.go holds local CLI settings. The file is written atomically so a
// crash cannot leave it half-written.

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

const configFileName = "config.json"

// config is the on-disk shape of the settings file.
type config struct {
	// ProxyInstalled records that `cysu proxy install` completed successfully.
	// The other fields can contain credentials, so writes use mode 0600.
	ProxyInstalled bool   `json:"proxyInstalled,omitempty"`
	Token          string `json:"token,omitempty"`
	Passphrase     string `json:"passphrase,omitempty"`
	MasterKey      []byte `json:"masterKey,omitempty"` // JSON encodes the 32-byte key as base64.
	EngineURL      string `json:"engineUrl,omitempty"`
	Provider       string `json:"provider,omitempty"`
}

// configPath returns the location of the settings file in the user's config
// directory.
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
// parent directory with mode 0700 if needed.
func writeConfig(cfg config) (string, error) {
	path, err := configPath()
	if err != nil {
		return "", err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("creating config dir %s: %w", dir, err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(dir, 0o700); err != nil {
			return "", fmt.Errorf("securing config dir %s: %w", dir, err)
		}
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encoding config: %w", err)
	}
	data = append(data, '\n')

	tmp, err := os.CreateTemp(dir, ".config-*.tmp")
	if err != nil {
		return "", fmt.Errorf("creating config file: %w", err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return "", fmt.Errorf("writing config file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return "", fmt.Errorf("syncing config file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("closing config file: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return "", fmt.Errorf("installing config file: %w", err)
	}
	return path, nil
}

// ---------------------------------------------------------------------------
// Proxy installation
// ---------------------------------------------------------------------------

// setProxyInstalled records whether the proxy installation completed.
func setProxyInstalled(installed bool) error {
	cfg, err := readConfig()
	if err != nil {
		return err
	}
	cfg.ProxyInstalled = installed
	_, err = writeConfig(cfg)
	return err
}

// proxyInstalled returns the proxy installation state saved in the config.
// A missing config or field is treated as not installed by readConfig.
func proxyInstalled() (bool, error) {
	cfg, err := readConfig()
	if err != nil {
		return false, err
	}
	return cfg.ProxyInstalled, nil
}
