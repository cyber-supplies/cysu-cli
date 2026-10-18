package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestProxyStatusDistinguishesInstalledFromActive(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "environment")
	secret := "test-secret-not-for-output"
	proxyURL := (&url.URL{Scheme: "https", Host: "proxy.example.test:443", User: url.UserPassword("cysu", secret)}).String()
	if err := os.WriteFile(path, []byte(renderEnvironment("", proxyURL)), 0o600); err != nil {
		t.Fatal(err)
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.CreateCertificate(rand.Reader, &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Test proxy CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}, &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Test proxy CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certPath := filepath.Join(dir, "ca.crt")
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert}), 0o600); err != nil {
		t.Fatal(err)
	}
	stores := []trustStore{{dir: dir, file: "ca.crt"}}
	status := inspectProxyStatus(path, stores, func(string) string { return "" })
	if !status.Installed || status.Active || status.Proxy != "https://proxy.example.test:443" {
		t.Fatalf("unexpected inactive status: %+v", status)
	}
	if strings.Contains(status.Proxy+status.Detail, secret) {
		t.Fatal("status exposed the proxy password")
	}
	status = inspectProxyStatus(path, stores, func(key string) string {
		if key == "HTTPS_PROXY" {
			return proxyURL
		}
		return ""
	})
	if !status.Installed || !status.Active || status.Detail != "" {
		t.Fatalf("unexpected active status: %+v", status)
	}
	if err := os.Remove(certPath); err != nil {
		t.Fatal(err)
	}
	status = inspectProxyStatus(path, stores, func(string) string { return proxyURL })
	if status.Installed || status.Active {
		t.Fatalf("missing CA was reported as installed: %+v", status)
	}
}

func TestProxyStatusRejectsIncompleteManagedBlock(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "environment")
	content := envBlockBegin + "\nhttps_proxy=\"https://cysu:secret@proxy.example.test:443\"\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	status := inspectProxyStatus(path, nil, func(string) string { return "" })
	if status.Installed || status.Proxy != "" || strings.Contains(status.Detail, "secret") {
		t.Fatalf("invalid block was accepted or exposed credentials: %+v", status)
	}
}
