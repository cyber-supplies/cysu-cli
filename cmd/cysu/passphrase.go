package main

// passphrase.go loads the saved encryption key or derives it from the
// configured passphrase when a key has not been initialized.

import (
	"errors"
	"os"

	"golang.org/x/crypto/argon2"
)

const (
	// These parameters and the salt are a compatibility contract: changing
	// them would change the key for every existing ciphertext.
	masterKeySalt = "cysu-cli/master-key/v1"
	argonTime     = 3
	argonMemory   = 64 * 1024 // KiB
	argonThreads  = 4
)

// cachedKey avoids repeating Argon2id for each value in one CLI process.
var cachedKey []byte

// ensureEncryptionKey loads or derives a key before an encrypted API command.
func ensureEncryptionKey() error {
	_, err := masterKey()
	return err
}

// masterKey loads the config key, derives from a saved passphrase, then reads
// a legacy key file before using CYSU_PASSPHRASE as the last fallback.
func masterKey() ([]byte, error) {
	if cachedKey != nil {
		return cachedKey, nil
	}
	cfg, err := readConfig()
	if err != nil {
		return nil, err
	}
	if len(cfg.MasterKey) != 0 {
		if len(cfg.MasterKey) != keyLen {
			return nil, cliError{message: "invalid master key in config: run cysu key init", exit: exitUsage}
		}
		cachedKey = cfg.MasterKey
		return cachedKey, nil
	}
	passphrase := cfg.Passphrase
	if passphrase == "" {
		legacy, err := readLegacyKeyFile()
		if err == nil {
			cachedKey = legacy
			return cachedKey, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		passphrase = os.Getenv("CYSU_PASSPHRASE")
	}
	if passphrase == "" {
		return nil, cliError{message: "missing encryption passphrase: run cysu setup or set CYSU_PASSPHRASE", exit: exitUsage}
	}
	pass := []byte(passphrase)
	defer zero(pass)
	cachedKey = deriveMasterKey(pass)
	return cachedKey, nil
}

// deriveMasterKey stretches the passphrase with the unchanged Argon2id
// parameters, preserving compatibility with existing encrypted data.
func deriveMasterKey(pass []byte) []byte {
	return argon2.IDKey(pass, []byte(masterKeySalt), argonTime, argonMemory, argonThreads, keyLen)
}
