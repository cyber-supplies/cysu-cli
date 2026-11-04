package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestProxyInstallRecordIsMachineScoped(t *testing.T) {
	path := filepath.Join(t.TempDir(), "var", "lib", "cysu", "proxy-install.json")
	if recorded, err := proxyInstallRecorded(path); err != nil || recorded {
		t.Fatalf("missing record: recorded=%t, err=%v", recorded, err)
	}
	if err := recordProxyInstall(path); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", t.TempDir())
	if recorded, err := proxyInstallRecorded(path); err != nil || !recorded {
		t.Fatalf("record after changing HOME: recorded=%t, err=%v", recorded, err)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o644 {
		t.Fatalf("record permissions: info=%v, err=%v", info, err)
	}
}
