package main

// encryption.go implements passphrase-based encryption for the CLI.
//
// Design:
//
//   - On first run the user is prompted for a passphrase. The passphrase is
//     stretched with Argon2id (64 MiB, 3 passes, 4 lanes) and a random
//     16-byte salt into a 256-bit master key.
//   - The derived key, salt and KDF parameters are written to a key file with
//     mode 0600 inside a 0700 directory in the user's config directory
//     (Linux: $XDG_CONFIG_HOME/cysu/master.key, default ~/.config/cysu/;
//     macOS: ~/Library/Application Support/cysu/; Windows: %AppData%\cysu\).
//     CYSU_KEY_FILE overrides the location.
//   - encrypt/decrypt use AES-256-GCM with a fresh random 96-bit nonce per
//     message. Wire format: [version:1][nonce:12][ciphertext||tag:16].
//     The version byte is bound as additional authenticated data.
//
// Anyone without the passphrase (or read access to the key file) cannot
// decrypt. Protecting the key file is therefore the same job as protecting
// ~/.ssh/id_ed25519: the CLI refuses to use a key file that is readable by
// group or others.
//
// What is encrypted: finding titles and descriptions (findings.go) and the
// contents of stored files, zstd-compressed first (storage.go). The backend only
// ever sees ciphertext for those; everything else (ids, severity, paths)
// stays in the clear so the backend can list and look things up.
//
// This file holds the cipher itself. keyfile.go derives, stores and loads the
// master key; key.go is the `cysu key` command that manages it.

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"
)

const (
	keyLen = 32 // AES-256

	// Ciphertext wire format.
	ctVersion byte = 1
	nonceLen       = 12
	tagLen         = 16
)

// errNotCiphertext marks data that is not in the wire format at all (too
// short, unknown version, not base64). It is distinct from ciphertext that
// fails to authenticate: the former may simply be plaintext stored by other
// means, the latter is encrypted data this key cannot open.
var errNotCiphertext = errors.New("not ciphertext")

// encrypt seals plaintext with the master key and returns a self-describing
// binary blob suitable for decrypt. It triggers first-run key setup if no key
// file exists yet.
func encrypt(plaintext []byte) ([]byte, error) {
	key, err := masterKey()
	if err != nil {
		return nil, err
	}
	aead, err := newAEAD(key)
	if err != nil {
		return nil, err
	}

	nonce := make([]byte, nonceLen)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("generating nonce: %w", err)
	}

	out := make([]byte, 0, 1+nonceLen+len(plaintext)+tagLen)
	out = append(out, ctVersion)
	out = append(out, nonce...)
	out = aead.Seal(out, nonce, plaintext, []byte{ctVersion})
	return out, nil
}

// decrypt opens a blob produced by encrypt. It fails if the data was
// produced with a different key, was tampered with, or is malformed.
func decrypt(ciphertext []byte) ([]byte, error) {
	if len(ciphertext) < 1+nonceLen+tagLen {
		return nil, fmt.Errorf("decrypt: ciphertext too short: %w", errNotCiphertext)
	}
	if ciphertext[0] != ctVersion {
		return nil, fmt.Errorf("decrypt: unsupported ciphertext version %d: %w", ciphertext[0], errNotCiphertext)
	}

	key, err := masterKey()
	if err != nil {
		return nil, err
	}
	aead, err := newAEAD(key)
	if err != nil {
		return nil, err
	}

	nonce := ciphertext[1 : 1+nonceLen]
	body := ciphertext[1+nonceLen:]
	plaintext, err := aead.Open(nil, nonce, body, []byte{ctVersion})
	if err != nil {
		return nil, errors.New("decrypt: authentication failed (wrong key or corrupted data)")
	}
	return plaintext, nil
}

// encryptString is a convenience wrapper that returns the ciphertext as
// standard base64, handy for embedding in JSON or config files.
func encryptString(plaintext string) (string, error) {
	ct, err := encrypt([]byte(plaintext))
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(ct), nil
}

// decryptString reverses encryptString.
func decryptString(encoded string) (string, error) {
	ct, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil {
		return "", fmt.Errorf("decrypt: invalid base64: %w", errNotCiphertext)
	}
	pt, err := decrypt(ct)
	if err != nil {
		return "", err
	}
	return string(pt), nil
}

// decryptIfEncrypted decrypts s when it is ciphertext from encryptString
// and returns it unchanged when it is not, so values stored in plaintext by
// other means (the backend's own UI) still read back. Ciphertext that fails
// to authenticate is an error: the data is encrypted, but not for this key.
func decryptIfEncrypted(s string) (string, error) {
	pt, err := decryptString(s)
	if errors.Is(err, errNotCiphertext) {
		return s, nil
	}
	return pt, err
}

// decryptBytesIfEncrypted is decryptIfEncrypted for raw data such as a
// stored file. The boolean reports whether data was ciphertext.
func decryptBytesIfEncrypted(data []byte) ([]byte, bool, error) {
	pt, err := decrypt(data)
	if errors.Is(err, errNotCiphertext) {
		return data, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return pt, true, nil
}

// newAEAD builds an AES-256-GCM cipher for key.
func newAEAD(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("initialising cipher: %w", err)
	}
	return cipher.NewGCM(block)
}

// zero overwrites b in place. Best effort: Go may have copied it elsewhere.
func zero(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
