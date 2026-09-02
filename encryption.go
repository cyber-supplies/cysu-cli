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

import (
	"bufio"
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
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
	keyLen         = 32 // AES-256
	saltLen        = 16

	// Argon2id parameters (RFC 9106 second recommended option).
	argonTime    uint32 = 3
	argonMemory  uint32 = 64 * 1024 // KiB, i.e. 64 MiB
	argonThreads uint8  = 4

	// Ciphertext wire format.
	ctVersion byte = 1
	nonceLen       = 12
	tagLen         = 16
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
// Public API
// ---------------------------------------------------------------------------

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
		return nil, errors.New("decrypt: ciphertext too short")
	}
	if ciphertext[0] != ctVersion {
		return nil, fmt.Errorf("decrypt: unsupported ciphertext version %d", ciphertext[0])
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
		return "", fmt.Errorf("decrypt: invalid base64: %w", err)
	}
	pt, err := decrypt(ct)
	if err != nil {
		return "", err
	}
	return string(pt), nil
}

// ensureKey makes sure a master key exists, running the interactive
// first-run setup if necessary. main calls this before every command.
func ensureKey() error {
	_, err := masterKey()
	return err
}

// ---------------------------------------------------------------------------
// Key management
// ---------------------------------------------------------------------------

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
		fmt.Fprintf(os.Stderr, "No encryption key found at %s. Setting one up now.\n", path)
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

// ---------------------------------------------------------------------------
// Passphrase prompting
// ---------------------------------------------------------------------------

// promptNewPassphrase collects a fresh passphrase. Input is echoed so the
// user can see exactly what they type. For non-interactive use the
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

// promptPassphrase reads a passphrase once, without confirmation.
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
			return nil, errors.New("no terminal available to read the passphrase; set CYSU_PASSPHRASE for non-interactive use")
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

// ---------------------------------------------------------------------------
// `cysu key` command
// ---------------------------------------------------------------------------

func runKey(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("key: missing action (init|path|verify)")
	}
	action, rest := args[0], args[1:]

	switch action {

	case "init":
		fs := flag.NewFlagSet("key init", flag.ContinueOnError)
		force := fs.Bool("force", false, "replace an existing key (old key is kept as a .bak file)")
		if err := fs.Parse(rest); err != nil {
			return err
		}
		return initKey(*force)

	default:
		return fmt.Errorf("key: unknown action %q (init|path|verify)", action)
	}
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
			return fmt.Errorf("key file %s already exists; use --force to replace it", path)
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
