package main

// keyfile.go is where the master key comes from: deriving it from a
// passphrase, storing it in the key file and loading it back. See
// encryption.go for the overall design.

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"golang.org/x/crypto/argon2"
	"golang.org/x/term"
)

const (
	keyFileName    = "master.key"
	keyFileVersion = 1
	saltLen        = 16

	// Argon2id parameters (RFC 9106 second recommended option).
	argonTime    uint32 = 3
	argonMemory  uint32 = 64 * 1024 // KiB, i.e. 64 MiB
	argonThreads uint8  = 4
)

// keyFile is the on-disk representation of the master key. []byte fields are
// base64-encoded by encoding/json.
type keyFile struct {
	Version int       `json:"version"`
	KDF     string    `json:"kdf"`
	Salt    []byte    `json:"salt"`
	Time    uint32    `json:"time"`
	Memory  uint32    `json:"memory"`
	Threads uint8     `json:"threads"`
	Key     []byte    `json:"key"`
	Created time.Time `json:"created"`
}

// cachedKey holds the master key for the lifetime of the process so the key
// file is read (and the first-run prompt shown) at most once.
var cachedKey []byte

// ---------------------------------------------------------------------------
// Loading
// ---------------------------------------------------------------------------

// ensureKey makes sure a master key exists, running the interactive
// first-run setup if necessary. main calls this before every API command.
func ensureKey() error {
	_, err := masterKey()
	return err
}

// keyPath returns the location of the key file, honouring CYSU_KEY_FILE.
func keyPath() (string, error) {
	if v := strings.TrimSpace(os.Getenv("CYSU_KEY_FILE")); v != "" {
		return v, nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("locating user config dir: %w", err)
	}
	return filepath.Join(dir, "cysu", keyFileName), nil
}

// masterKey returns the cached master key, loading or creating it on first
// use.
func masterKey() ([]byte, error) {
	if cachedKey != nil {
		return cachedKey, nil
	}
	path, err := keyPath()
	if err != nil {
		return nil, err
	}
	kf, err := readKeyFile(path)
	switch {
	case err == nil:
		cachedKey = kf.Key
		return cachedKey, nil
	case errors.Is(err, os.ErrNotExist):
		fmt.Fprintf(os.Stderr, "No encryption key found at %s.\n", path)
		kf, err = createKeyFile(path)
		if err != nil {
			return nil, err
		}
		cachedKey = kf.Key
		return cachedKey, nil
	default:
		return nil, err
	}
}

// readKeyFile loads and validates the key file at path.
func readKeyFile(path string) (*keyFile, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("key file %s is not a regular file", path)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("key file %s has insecure permissions %04o; run: chmod 600 %s",
			path, info.Mode().Perm(), path)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading key file: %w", err)
	}
	var kf keyFile
	if err := json.Unmarshal(raw, &kf); err != nil {
		return nil, fmt.Errorf("parsing key file %s: %w", path, err)
	}
	if kf.Version != keyFileVersion {
		return nil, fmt.Errorf("key file %s: unsupported version %d", path, kf.Version)
	}
	if kf.KDF != "argon2id" {
		return nil, fmt.Errorf("key file %s: unsupported kdf %q", path, kf.KDF)
	}
	if len(kf.Key) != keyLen {
		return nil, fmt.Errorf("key file %s: key must be %d bytes, got %d", path, keyLen, len(kf.Key))
	}
	if len(kf.Salt) == 0 {
		return nil, fmt.Errorf("key file %s: missing salt", path)
	}
	return &kf, nil
}

// ---------------------------------------------------------------------------
// Creating
// ---------------------------------------------------------------------------

// createKeyFile prompts for a new passphrase, derives the master key and
// writes it to path with restrictive permissions.
func createKeyFile(path string) (*keyFile, error) {
	pass, err := promptNewPassphrase()
	if err != nil {
		return nil, err
	}
	defer zero(pass)

	salt := make([]byte, saltLen)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return nil, fmt.Errorf("generating salt: %w", err)
	}

	kf := &keyFile{
		Version: keyFileVersion,
		KDF:     "argon2id",
		Salt:    salt,
		Time:    argonTime,
		Memory:  argonMemory,
		Threads: argonThreads,
		Key:     deriveKey(pass, salt, argonTime, argonMemory, argonThreads),
		Created: time.Now().UTC(),
	}

	if err := writeKeyFile(path, kf); err != nil {
		return nil, err
	}
	fmt.Fprintf(os.Stderr, "Encryption key saved to %s (mode 0600). Keep this file private.\n", path)
	return kf, nil
}

// writeKeyFile atomically writes kf to path, creating the parent directory
// with mode 0700 and the file with mode 0600. It refuses to overwrite.
func writeKeyFile(path string, kf *keyFile) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating key dir %s: %w", dir, err)
	}
	// MkdirAll does not tighten an existing directory; do it explicitly.
	if runtime.GOOS != "windows" {
		if err := os.Chmod(dir, 0o700); err != nil {
			return fmt.Errorf("securing key dir %s: %w", dir, err)
		}
	}

	data, err := json.MarshalIndent(kf, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding key file: %w", err)
	}

	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("creating key file: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp)
		return fmt.Errorf("writing key file: %w", err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return fmt.Errorf("syncing key file: %w", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("closing key file: %w", err)
	}

	// Never clobber an existing key: that would silently orphan every
	// ciphertext made with it.
	if _, err := os.Stat(path); err == nil {
		os.Remove(tmp)
		return fmt.Errorf("key file %s already exists (use `cysu key init --force` to replace it)", path)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("installing key file: %w", err)
	}
	return nil
}

// deriveKey stretches a passphrase into a keyLen-byte key with Argon2id.
func deriveKey(pass, salt []byte, t, m uint32, p uint8) []byte {
	return argon2.IDKey(pass, salt, t, m, p, keyLen)
}

// ---------------------------------------------------------------------------
// Passphrase prompting
// ---------------------------------------------------------------------------

// promptNewPassphrase collects a fresh, non-empty passphrase. Input is echoed
// so the user can see exactly what they type. For non-interactive use the
// CYSU_PASSPHRASE environment variable is honoured.
func promptNewPassphrase() ([]byte, error) {
	if v := os.Getenv("CYSU_PASSPHRASE"); v != "" {
		return []byte(v), nil
	}
	p, err := readLine("Enter a passphrase: ")
	if err != nil {
		return nil, err
	}
	if len(p) == 0 {
		return nil, errors.New("passphrase must not be empty")
	}
	return p, nil
}

// promptPassphrase reads an existing passphrase for verification. Like
// promptNewPassphrase it honours CYSU_PASSPHRASE.
func promptPassphrase() ([]byte, error) {
	if v := os.Getenv("CYSU_PASSPHRASE"); v != "" {
		return []byte(v), nil
	}
	return readLine("Passphrase: ")
}

// readLine prints prompt and reads one line with terminal echo left on, so
// the user sees what they type. It prefers the controlling terminal so it
// works even when stdin/stdout are redirected. The trailing newline is
// stripped; nothing else is trimmed.
func readLine(prompt string) ([]byte, error) {
	in, out := os.Stdin, os.Stderr
	if !term.IsTerminal(int(in.Fd())) {
		tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
		if err != nil {
			return nil, failf("no terminal available to read the passphrase: run `cysu key init` once, or set CYSU_PASSPHRASE")
		}
		defer tty.Close()
		in, out = tty, tty
	}
	fmt.Fprint(out, prompt)
	line, err := bufio.NewReader(in).ReadBytes('\n')
	if err != nil && (err != io.EOF || len(line) == 0) {
		return nil, fmt.Errorf("reading passphrase: %w", err)
	}
	line = bytes.TrimRight(line, "\r\n")
	return line, nil
}
