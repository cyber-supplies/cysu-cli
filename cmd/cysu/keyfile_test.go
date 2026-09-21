package main

import (
	"bytes"
	"encoding/hex"
	"path/filepath"
	"testing"
)

func TestCreateKeyFileIsDeterministicFromPassphrase(t *testing.T) {
	t.Setenv("CYSU_PASSPHRASE", "correct horse battery staple")

	first, err := createKeyFile(filepath.Join(t.TempDir(), "master.key"))
	if err != nil {
		t.Fatalf("create first key file: %v", err)
	}
	second, err := createKeyFile(filepath.Join(t.TempDir(), "master.key"))
	if err != nil {
		t.Fatalf("create second key file: %v", err)
	}

	if !bytes.Equal(first.Key, second.Key) {
		t.Fatal("the same passphrase produced different master keys")
	}
	if !bytes.Equal(first.Salt, []byte(masterKeySalt)) || !bytes.Equal(second.Salt, []byte(masterKeySalt)) {
		t.Fatal("new key files do not use the deterministic CYSU domain salt")
	}
}

func TestDifferentPassphrasesProduceDifferentMasterKeys(t *testing.T) {
	first := deriveMasterKey([]byte("first passphrase"))
	second := deriveMasterKey([]byte("second passphrase"))
	defer zero(first)
	defer zero(second)

	if bytes.Equal(first, second) {
		t.Fatal("different passphrases produced the same master key")
	}
}

func TestDeriveMasterKeyCompatibilityVector(t *testing.T) {
	key := deriveMasterKey([]byte("cysu deterministic test vector"))
	defer zero(key)

	const expected = "ecbdd8a6a5e755f120da99a0dc490f46734ff25e3c3b1efa0f5ba96cf4fc41c8"
	if actual := hex.EncodeToString(key); actual != expected {
		t.Fatalf("master key derivation changed: got %s, want %s", actual, expected)
	}
}
