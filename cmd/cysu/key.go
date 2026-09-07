package main

// key.go is the `cysu key` command: managing the encryption key file
// described in encryption.go.

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"time"
)

var keyResource = resource{
	name:  "key",
	title: "Key (encryption)",
	commands: []command{
		{
			name: "init",
			flags: []flagSpec{
				{name: "force", boolean: true, usage: "replace an existing key (the old key is kept as a .bak file)"},
			},
			run: func(v flagValues) error {
				return initKey(v.enabled("force"))
			},
		},
		{
			name: "path",
			run: func(flagValues) error {
				return printKeyPath()
			},
		},
		{
			name: "verify",
			run: func(flagValues) error {
				return verifyKey()
			},
		},
	},
}

// initKey creates the key file. With force, an existing key is moved aside
// to a timestamped .bak file first.
func initKey(force bool) error {
	path, err := keyPath()
	if err != nil {
		return err
	}
	if _, err := os.Stat(path); err == nil {
		if !force {
			return failf("key file %s already exists; re-run with --force to replace it (data encrypted with the old key can then no longer be decrypted)", path)
		}
		bak := fmt.Sprintf("%s.bak-%s", path, time.Now().UTC().Format("20060102T150405Z"))
		if err := os.Rename(path, bak); err != nil {
			return fmt.Errorf("backing up existing key: %w", err)
		}
		fmt.Fprintf(os.Stderr, "Existing key moved to %s. Data encrypted with it needs that file to decrypt.\n", bak)
	}
	kf, err := createKeyFile(path)
	if err != nil {
		return err
	}
	cachedKey = kf.Key
	return nil
}

func printKeyPath() error {
	path, err := keyPath()
	if err != nil {
		return err
	}
	fmt.Println(path)
	return nil
}

// verifyKey prompts for the passphrase and checks it against the stored key
// by re-running the KDF with the stored salt and parameters.
func verifyKey() error {
	path, err := keyPath()
	if err != nil {
		return err
	}
	kf, err := readKeyFile(path)
	if err != nil {
		return err
	}
	pass, err := promptPassphrase()
	if err != nil {
		return err
	}
	defer zero(pass)

	derived := deriveKey(pass, kf.Salt, kf.Time, kf.Memory, kf.Threads)
	defer zero(derived)
	if !bytes.Equal(derived, kf.Key) {
		return errors.New("passphrase does not match the stored key")
	}
	fmt.Println("OK: passphrase matches", path)
	return nil
}
