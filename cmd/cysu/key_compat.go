package main

// key_compat.go reads and writes the former master.key format while worker
// images still expect that file. The config remains the primary key store.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const legacyKeyFileName = "master.key"

// legacyKeyFile matches the format written by older cysu key init releases.
type legacyKeyFile struct {
	Version int       `json:"version"`
	KDF     string    `json:"kdf"`
	Salt    []byte    `json:"salt"`
	Time    uint32    `json:"time"`
	Memory  uint32    `json:"memory"`
	Threads uint8     `json:"threads"`
	Key     []byte    `json:"key"`
	Created time.Time `json:"created"`
}

// legacyKeyPath locates the old key file, preserving CYSU_KEY_FILE overrides.
func legacyKeyPath() (string, error) {
	if path := strings.TrimSpace(os.Getenv("CYSU_KEY_FILE")); path != "" {
		return path, nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("locating user config dir: %w", err)
	}
	return filepath.Join(dir, "cysu", legacyKeyFileName), nil
}

// readLegacyKeyFile loads a key when config has no key or passphrase.
func readLegacyKeyFile() ([]byte, error) {
	path, err := legacyKeyPath()
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("legacy key file %s is not a regular file", path)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("legacy key file %s has insecure permissions %04o", path, info.Mode().Perm())
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading legacy key file: %w", err)
	}
	var file legacyKeyFile
	if err := json.Unmarshal(raw, &file); err != nil {
		return nil, fmt.Errorf("parsing legacy key file %s: %w", path, err)
	}
	if file.Version != 1 || file.KDF != "argon2id" || len(file.Salt) == 0 || len(file.Key) != keyLen {
		return nil, fmt.Errorf("legacy key file %s has an unsupported format", path)
	}
	return file.Key, nil
}

// writeLegacyKeyFile mirrors an initialized key for published worker images.
func writeLegacyKeyFile(key []byte) error {
	path, err := legacyKeyPath()
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating legacy key directory: %w", err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(dir, 0o700); err != nil {
			return fmt.Errorf("securing legacy key directory: %w", err)
		}
	}
	data, err := json.MarshalIndent(legacyKeyFile{
		Version: 1, KDF: "argon2id", Salt: []byte(masterKeySalt),
		Time: argonTime, Memory: argonMemory, Threads: argonThreads,
		Key: key, Created: time.Now().UTC(),
	}, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding legacy key file: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".master-key-*.tmp")
	if err != nil {
		return fmt.Errorf("creating legacy key file: %w", err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return fmt.Errorf("writing legacy key file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("syncing legacy key file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing legacy key file: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("installing legacy key file: %w", err)
	}
	return nil
}
