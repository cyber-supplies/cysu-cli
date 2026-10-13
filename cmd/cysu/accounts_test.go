package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestAccountsEncryptBeforeSendingAndDecryptResponses(t *testing.T) {
	oldKey := cachedKey
	cachedKey = bytes.Repeat([]byte{0x42}, keyLen)
	t.Cleanup(func() { cachedKey = oldKey })
	oldToken := apiToken
	apiToken = "test-token"
	t.Cleanup(func() { apiToken = oldToken })

	id := "acc_00000000000000000000000000"
	updated := "2026-10-13T12:00:00.000Z"
	var stored account
	var removed bool

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("Authorization = %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")

		switch {
		case r.Method == "POST" && r.URL.Path == "/api/accounts":
			var body struct{ Username, Password, Location string }
			mustDecodeRequest(t, r, &body)
			if body.Password == "initial-secret" {
				t.Error("create sent a plaintext password")
			}
			plain, err := decryptString(body.Password)
			if err != nil || plain != "initial-secret" {
				t.Errorf("create password decrypt = %q, %v", plain, err)
			}
			stored = account{ID: id, Username: body.Username, Password: body.Password, Location: body.Location}
			json.NewEncoder(w).Encode(stored)

		case r.Method == "GET" && r.URL.Path == "/api/accounts":
			if got := r.URL.Query().Get("location"); got != `example\.com` {
				t.Errorf("location query = %q", got)
			}
			json.NewEncoder(w).Encode([]map[string]string{{
				"id": id, "username": stored.Username, "location": stored.Location,
			}})

		case r.Method == "GET" && r.URL.Path == "/api/accounts/"+id:
			json.NewEncoder(w).Encode(stored)

		case r.Method == "PUT" && r.URL.Path == "/api/accounts/"+id:
			var body struct{ Username, Password, Location string }
			mustDecodeRequest(t, r, &body)
			plain, err := decryptString(body.Password)
			if err != nil || plain != "initial-secret" {
				t.Errorf("modified password decrypt = %q, %v", plain, err)
			}
			stored.Username, stored.Password, stored.Location = body.Username, body.Password, body.Location
			json.NewEncoder(w).Encode(stored)

		case r.Method == "POST" && r.URL.Path == "/api/account/"+id+"/setCookie":
			var body struct{ Cookie string }
			mustDecodeRequest(t, r, &body)
			if body.Cookie == "session=abc" {
				t.Error("set-cookie sent a plaintext cookie")
			}
			plain, err := decryptString(body.Cookie)
			if err != nil || plain != "session=abc" {
				t.Errorf("cookie decrypt = %q, %v", plain, err)
			}
			stored.Cookie, stored.UpdatedAt = &body.Cookie, &updated
			json.NewEncoder(w).Encode(stored)

		case r.Method == "DELETE" && r.URL.Path == "/api/accounts/"+id:
			removed = true
			json.NewEncoder(w).Encode(map[string]any{"id": id, "removed": true})

		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	t.Setenv("CYSU_API_URL", server.URL)

	created := decodePrintedAccount(t, captureStdout(t, func() error {
		return createAccount("alice", "initial-secret", "https://example.com/login")
	}))
	if created.Password != "initial-secret" || created.Cookie != nil {
		t.Fatalf("created output = %#v", created)
	}

	var listed []map[string]string
	decodePrintedJSON(t, captureStdout(t, func() error {
		return listAccounts(`example\.com`)
	}), &listed)
	if len(listed) != 1 || listed[0]["id"] != id {
		t.Fatalf("listed output = %#v", listed)
	}

	got := decodePrintedAccount(t, captureStdout(t, func() error { return getAccount(id) }))
	if got.Password != "initial-secret" {
		t.Fatalf("get password = %q", got.Password)
	}

	modified := decodePrintedAccount(t, captureStdout(t, func() error {
		return modifyAccount(id, "alice@example.com", "", "https://example.com/new-login")
	}))
	if modified.Username != "alice@example.com" || modified.Password != "initial-secret" {
		t.Fatalf("modified output = %#v", modified)
	}

	withCookie := decodePrintedAccount(t, captureStdout(t, func() error {
		return setAccountCookie(id, "session=abc")
	}))
	if withCookie.Cookie == nil || *withCookie.Cookie != "session=abc" || withCookie.UpdatedAt == nil {
		t.Fatalf("set-cookie output = %#v", withCookie)
	}

	var result struct {
		ID      string `json:"id"`
		Removed bool   `json:"removed"`
	}
	decodePrintedJSON(t, captureStdout(t, func() error { return removeAccount(id) }), &result)
	if !removed || !result.Removed || result.ID != id {
		t.Fatalf("remove output = %#v, request received = %v", result, removed)
	}
}

func TestAccountsModifyRequiresAChange(t *testing.T) {
	cmd, ok := accountsResource.lookup("modify")
	if !ok {
		t.Fatal("modify command is missing")
	}
	_, err := cmd.parse(accountsResource, []string{"--account", "acc_00000000000000000000000000", "--token", "test"})
	if err == nil || !strings.Contains(err.Error(), "provide --username, --password, or --location") {
		t.Fatalf("modify validation error = %v", err)
	}
}

func mustDecodeRequest(t *testing.T, r *http.Request, target any) {
	t.Helper()
	defer r.Body.Close()
	if err := json.NewDecoder(r.Body).Decode(target); err != nil {
		t.Fatalf("decoding request: %v", err)
	}
}

func captureStdout(t *testing.T, fn func() error) []byte {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = w
	runErr := fn()
	w.Close()
	os.Stdout = old
	out, readErr := io.ReadAll(r)
	r.Close()
	if runErr != nil {
		t.Fatalf("command failed: %v", runErr)
	}
	if readErr != nil {
		t.Fatal(readErr)
	}
	return out
}

func decodePrintedAccount(t *testing.T, raw []byte) account {
	t.Helper()
	var acct account
	decodePrintedJSON(t, raw, &acct)
	return acct
}

func decodePrintedJSON(t *testing.T, raw []byte, target any) {
	t.Helper()
	if err := json.Unmarshal(raw, target); err != nil {
		t.Fatalf("decoding command output %q: %v", raw, err)
	}
}
