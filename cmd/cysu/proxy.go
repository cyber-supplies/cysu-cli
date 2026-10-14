package main

// proxy.go is the `cysu proxy` command: setting a machine up to route its
// traffic through the CyberSupplies intercepting proxy.
//
// `cysu proxy install` does two privileged, system-wide things, so it must be
// run as root:
//
//  1. It fetches the proxy's CA certificate and installs it into the OS trust
//     store, so the TLS the proxy re-signs on the fly is trusted by clients.
//  2. It writes the proxy into /etc/environment, with the CYSU_TOKEN as the
//     proxy password, so ordinary tools (curl, package managers, ...) send
//     their traffic through it and authenticate automatically.
//
// This is Linux-only for now; other platforms report an error rather than
// silently doing nothing. The token lives in the proxy URL, which on Linux
// means it is written to /etc/environment (world-readable): a local user can
// read it. Rotate the token if that machine is shared.

import (
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// defaultProxyHost is the host that terminates TLS for the proxy and serves
// its CA certificate. CYSU_PROXY_HOST overrides it (host or host:port).
const defaultProxyHost = "proxy.cybersupplies.app"

// proxyPort is the port nginx listens on for proxy clients (TLS). It is made
// explicit in the proxy URL because tools such as curl default an https proxy
// to a different port when none is given.
const proxyPort = "443"

// caCertPath is the unauthenticated path on the proxy host that serves its CA
// certificate as PEM.
const caCertPath = "/ca.pem"

// The block of /etc/environment that this command owns. Everything between
// the markers is rewritten on each run; the rest of the file is left alone.
const (
	envFile        = "/etc/environment"
	envBlockBegin  = "# BEGIN cysu-proxy (managed by `cysu proxy install`)"
	envBlockEnd    = "# END cysu-proxy"
	noProxyDefault = "localhost,127.0.0.1,::1"
)

var proxyResource = resource{
	name:  "proxy",
	title: "Proxy",
	// Not an API resource: it talks to the proxy host, not the backend, and
	// handles CYSU_TOKEN itself (as the proxy password, not a Bearer token).
	commands: []command{
		{
			name: "install",
			run: func(flagValues) error {
				return installProxy()
			},
		},
		{
			name: "status",
			run: func(flagValues) error {
				return printProxyStatus()
			},
		},
	},
}

// installProxy performs the whole `cysu proxy install` flow: it checks its
// preconditions, fetches and trusts the CA certificate, and writes the proxy
// into /etc/environment. It prints one JSON summary on success.
func installProxy() error {
	if runtime.GOOS != "linux" {
		return failf("cysu proxy install is only supported on Linux (this is %s)", runtime.GOOS)
	}
	if os.Geteuid() != 0 {
		return failf("cysu proxy install must run as root: re-run with `sudo -E cysu proxy install` (so CYSU_TOKEN is preserved)")
	}

	token := strings.TrimSpace(os.Getenv("CYSU_TOKEN"))
	if token == "" {
		return failf("missing CYSU_TOKEN: set it before running (with sudo, use `sudo -E` or `sudo CYSU_TOKEN=... cysu proxy install`)")
	}

	host := proxyHost()

	// Fetch and validate the certificate before touching the system, so a
	// bad download changes nothing.
	pemBytes, err := fetchCACertificate(host)
	if err != nil {
		return err
	}

	// Install it into whichever trust store this distribution uses.
	certPath, trustCmd, err := installCACertificate(pemBytes)
	if err != nil {
		return err
	}

	// Point the system proxy at the proxy host, with the token as the
	// password. Kept out of the output so it is never printed.
	proxyURL := (&url.URL{
		Scheme: "https",
		User:   url.UserPassword("cysu", token),
		Host:   host,
	}).String()

	if err := writeProxyEnvironment(proxyURL); err != nil {
		return err
	}
	if err := setProxyInstalled(true); err != nil {
		return failf("saving proxy installation status: %v", err)
	}

	// A URL with the credentials stripped, safe to print.
	safeProxyURL := (&url.URL{Scheme: "https", Host: host}).String()

	return printJSON(struct {
		Installed       bool   `json:"installed"`
		Certificate     string `json:"certificate"`
		TrustCommand    string `json:"trust_command"`
		Proxy           string `json:"proxy"`
		EnvironmentFile string `json:"environment_file"`
		Note            string `json:"note"`
	}{
		Installed:       true,
		Certificate:     certPath,
		TrustCommand:    trustCmd,
		Proxy:           safeProxyURL,
		EnvironmentFile: envFile,
		Note:            "open a new login session for the proxy environment to take effect",
	})
}

// printProxyStatus reports the installation state recorded by the most recent
// successful `cysu proxy install`. It deliberately returns no configuration or
// credentials, so callers can safely inspect or pipe the result.
func printProxyStatus() error {
	installed, err := proxyInstalled()
	if err != nil {
		return err
	}
	return printJSON(struct {
		Installed bool `json:"installed"`
	}{Installed: installed})
}

// proxyHost returns the proxy host (optionally host:port), honouring the
// CYSU_PROXY_HOST override and defaulting the port to proxyPort when the
// override gives none.
func proxyHost() string {
	host := strings.TrimSpace(os.Getenv("CYSU_PROXY_HOST"))
	if host == "" {
		host = defaultProxyHost
	}
	host = strings.TrimRight(host, "/")
	if !strings.Contains(host, ":") {
		host += ":" + proxyPort
	}
	return host
}

// fetchCACertificate downloads the proxy's CA certificate over HTTPS and
// confirms it parses as an actual CA certificate before it is trusted.
func fetchCACertificate(host string) ([]byte, error) {
	endpoint := (&url.URL{Scheme: "https", Host: host, Path: caCertPath}).String()

	req, err := http.NewRequest("GET", endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("building request: %w", err)
	}
	req.Header.Set("Accept", "application/x-pem-file")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, failf("fetching CA certificate from %s: %v", endpoint, transportError(err))
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("reading CA certificate: %w", err)
	}
	if resp.StatusCode >= 400 {
		return nil, failf("fetching CA certificate from %s: HTTP %d %s: %s",
			endpoint, resp.StatusCode, http.StatusText(resp.StatusCode), summarize(raw))
	}

	block, _ := pem.Decode(raw)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, failf("CA certificate from %s is not a PEM certificate", endpoint)
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, failf("parsing CA certificate from %s: %v", endpoint, err)
	}
	if !cert.IsCA {
		return nil, failf("certificate from %s is not a CA certificate", endpoint)
	}
	return raw, nil
}

// trustStore describes where a distribution keeps additional trusted CA
// certificates and the command that rebuilds the trust bundle afterwards.
type trustStore struct {
	dir     string   // anchor directory certificates are dropped into
	file    string   // basename to write our certificate as (extension matters)
	command []string // command that rebuilds the system trust bundle
}

// trustStores lists the supported Linux trust-store layouts, tried in order.
// The first whose anchor directory exists is used.
var trustStores = []trustStore{
	// Debian / Ubuntu
	{dir: "/usr/local/share/ca-certificates", file: "cysu-proxy-ca.crt", command: []string{"update-ca-certificates"}},
	// RHEL / Fedora / CentOS
	{dir: "/etc/pki/ca-trust/source/anchors", file: "cysu-proxy-ca.pem", command: []string{"update-ca-trust", "extract"}},
}

// installCACertificate writes the certificate into the detected trust store
// and rebuilds the trust bundle. It returns the path written and the command
// that was run.
func installCACertificate(pemBytes []byte) (certPath, trustCmd string, err error) {
	store, ok := detectTrustStore()
	if !ok {
		return "", "", failf("no supported CA trust store found (looked for %s)", trustStoreDirs())
	}

	certPath = filepath.Join(store.dir, store.file)
	if err := os.WriteFile(certPath, pemBytes, 0o644); err != nil {
		return "", "", failf("writing CA certificate to %s: %v", certPath, err)
	}

	cmd := exec.Command(store.command[0], store.command[1:]...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", "", failf("running %s: %v: %s",
			strings.Join(store.command, " "), err, strings.TrimSpace(string(out)))
	}
	return certPath, strings.Join(store.command, " "), nil
}

// detectTrustStore returns the first configured trust store whose anchor
// directory exists on this machine.
func detectTrustStore() (trustStore, bool) {
	for _, s := range trustStores {
		if info, err := os.Stat(s.dir); err == nil && info.IsDir() {
			return s, true
		}
	}
	return trustStore{}, false
}

// trustStoreDirs lists the anchor directories we look for, for the error
// shown when none is present.
func trustStoreDirs() string {
	dirs := make([]string, len(trustStores))
	for i, s := range trustStores {
		dirs[i] = s.dir
	}
	return strings.Join(dirs, ", ")
}

// writeProxyEnvironment rewrites the cysu-proxy block of /etc/environment so
// the proxy variables point at proxyURL. Lines outside the block, and any
// pre-existing settings of the variables we manage, are removed so the block
// is the single source of truth; the rest of the file is preserved.
func writeProxyEnvironment(proxyURL string) error {
	existing, err := os.ReadFile(envFile)
	if err != nil && !os.IsNotExist(err) {
		return failf("reading %s: %v", envFile, err)
	}

	content := renderEnvironment(string(existing), proxyURL)

	// Write atomically so a crash cannot leave /etc/environment half-written.
	tmp := envFile + ".cysu.tmp"
	if err := os.WriteFile(tmp, []byte(content), 0o644); err != nil {
		return failf("writing %s: %v", envFile, err)
	}
	if err := os.Rename(tmp, envFile); err != nil {
		os.Remove(tmp)
		return failf("installing %s: %v", envFile, err)
	}
	return nil
}

// renderEnvironment builds the new /etc/environment contents: the existing
// file with our managed block and any managed assignments stripped, followed
// by a single freshly written cysu-proxy block.
func renderEnvironment(existing, proxyURL string) string {
	kept := filterEnvironment(existing)

	var b strings.Builder
	b.WriteString(kept)
	if kept != "" && !strings.HasSuffix(kept, "\n") {
		b.WriteByte('\n')
	}
	b.WriteString(envBlockBegin + "\n")
	for _, key := range []string{"http_proxy", "https_proxy", "HTTP_PROXY", "HTTPS_PROXY"} {
		fmt.Fprintf(&b, "%s=%q\n", key, proxyURL)
	}
	for _, key := range []string{"no_proxy", "NO_PROXY"} {
		fmt.Fprintf(&b, "%s=%q\n", key, noProxyDefault)
	}
	b.WriteString(envBlockEnd + "\n")
	return b.String()
}

// managedEnvKeys are the variables writeProxyEnvironment owns; any prior
// assignment of them (inside or outside our block) is dropped on rewrite.
var managedEnvKeys = map[string]bool{
	"http_proxy": true, "https_proxy": true, "HTTP_PROXY": true, "HTTPS_PROXY": true,
	"no_proxy": true, "NO_PROXY": true,
}

// filterEnvironment returns content with our managed block and any managed
// variable assignments removed, so writeProxyEnvironment can re-add a single
// clean block. The remaining lines are returned unchanged.
func filterEnvironment(content string) string {
	if content == "" {
		return ""
	}

	var out []string
	inBlock := false
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == envBlockBegin:
			inBlock = true
			continue
		case trimmed == envBlockEnd:
			inBlock = false
			continue
		case inBlock:
			continue
		case isManagedAssignment(trimmed):
			continue
		default:
			out = append(out, line)
		}
	}

	// Drop trailing blank lines left behind by the removals.
	for len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
		out = out[:len(out)-1]
	}
	if len(out) == 0 {
		return ""
	}
	return strings.Join(out, "\n") + "\n"
}

// isManagedAssignment reports whether line assigns one of the variables this
// command manages, e.g. `https_proxy=...` or `export HTTP_PROXY=...`.
func isManagedAssignment(line string) bool {
	line = strings.TrimPrefix(line, "export ")
	eq := strings.IndexByte(line, '=')
	if eq < 0 {
		return false
	}
	return managedEnvKeys[strings.TrimSpace(line[:eq])]
}
