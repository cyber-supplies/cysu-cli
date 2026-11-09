package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"cysu/examples"
)

// ensureExamples installs bundled reference data for all distribution methods.
// It fills missing files only, preserving user edits across runs and upgrades.
// Filesystem operations below the examples directory cannot follow links out.
func ensureExamples() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("locating home directory for examples: %w", err)
	}
	path := filepath.Join(home, "cysu", "examples")
	if err := os.MkdirAll(path, 0700); err != nil {
		return fmt.Errorf("creating examples directory: %w", err)
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return err
	}
	defer root.Close()
	return fs.WalkDir(examples.Files, ".", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return root.MkdirAll(filepath.FromSlash(name), 0700)
		}
		raw, err := examples.Files.ReadFile(name)
		if err != nil {
			return err
		}
		local := filepath.FromSlash(name)
		file, err := root.OpenFile(local, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if os.IsExist(err) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("creating example %s: %w", name, err)
		}
		if _, err := file.Write(raw); err != nil {
			file.Close()
			_ = root.Remove(local)
			return err
		}
		return file.Close()
	})
}
