package main

import (
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// The install record is machine state, not a setting owned by the root user.
// It contains no token or proxy URL. Existing installations without this file
// can still be recognized from the managed environment block and CA anchor.
const proxyInstallRecordFile = "/var/lib/cysu/proxy-install.json"

type proxyStatus struct {
	Installed bool
	Active    bool
	Proxy     string
	Detail    string
}

func recordProxyInstall(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".proxy-install-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.WriteString("{\"installed\":true}\n"); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func proxyInstallRecorded(path string) (bool, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("reading proxy install record: %w", err)
	}
	var record struct {
		Installed bool `json:"installed"`
	}
	if err := json.Unmarshal(raw, &record); err != nil {
		return false, fmt.Errorf("parsing proxy install record: %w", err)
	}
	return record.Installed, nil
}

// inspectProxyStatus checks the artifacts that actually route traffic. The
// environment block also identifies installations made by older CLI versions.
func inspectProxyStatus(environmentPath string, stores []trustStore, getenv func(string) string) proxyStatus {
	raw, err := os.ReadFile(environmentPath)
	if err != nil {
		return proxyStatus{Detail: "proxy environment is not installed"}
	}
	vars, ok := managedProxyEnvironment(string(raw))
	if !ok {
		return proxyStatus{Detail: "managed proxy environment is missing or incomplete"}
	}
	proxyURL := vars["https_proxy"]
	parsed, err := url.Parse(proxyURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User == nil {
		return proxyStatus{Detail: "managed proxy URL is invalid"}
	}
	if user := parsed.User.Username(); user != "cysu" {
		return proxyStatus{Detail: "managed proxy URL is invalid"}
	}
	if password, ok := parsed.User.Password(); !ok || password == "" {
		return proxyStatus{Detail: "managed proxy URL is invalid"}
	}
	for _, key := range []string{"http_proxy", "HTTP_PROXY", "HTTPS_PROXY"} {
		if vars[key] != proxyURL {
			return proxyStatus{Detail: "managed proxy environment is inconsistent"}
		}
	}
	for _, key := range []string{"no_proxy", "NO_PROXY"} {
		if vars[key] == "" {
			return proxyStatus{Detail: "managed proxy environment is incomplete"}
		}
	}
	if !installedProxyCA(stores) {
		return proxyStatus{Detail: "proxy CA certificate is missing or invalid"}
	}
	active := getenv("https_proxy") == proxyURL || getenv("HTTPS_PROXY") == proxyURL
	status := proxyStatus{
		Installed: true,
		Active:    active,
		Proxy:     (&url.URL{Scheme: parsed.Scheme, Host: parsed.Host}).String(),
	}
	if !active {
		status.Detail = "proxy is installed but this process has not loaded its environment"
	}
	return status
}

func managedProxyEnvironment(content string) (map[string]string, bool) {
	vars := make(map[string]string)
	inBlock, found, complete := false, false, false
	for _, line := range strings.Split(content, "\n") {
		switch strings.TrimSpace(line) {
		case envBlockBegin:
			if inBlock || found {
				return nil, false
			}
			inBlock, found = true, true
			continue
		case envBlockEnd:
			if !inBlock {
				return nil, false
			}
			inBlock, complete = false, true
			continue
		}
		if !inBlock {
			continue
		}
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok || !managedEnvKeys[key] || vars[key] != "" {
			return nil, false
		}
		decoded, err := strconv.Unquote(value)
		if err != nil {
			return nil, false
		}
		vars[key] = decoded
	}
	return vars, found && complete && !inBlock
}

func installedProxyCA(stores []trustStore) bool {
	for _, store := range stores {
		raw, err := os.ReadFile(filepath.Join(store.dir, store.file))
		if err != nil {
			continue
		}
		block, _ := pem.Decode(raw)
		if block == nil || block.Type != "CERTIFICATE" {
			continue
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err == nil && cert.IsCA {
			return true
		}
	}
	return false
}
