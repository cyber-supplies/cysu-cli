package main

// email.go exposes local IMAP accounts through the CLI's JSON command tables.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

var emailAccountFlag = flagSpec{
	name: "account", placeholder: "accountId", required: true,
	usage: "Account name from ~/.config/cysu/accounts.json",
}

var emailResource = resource{
	name: "email", title: "Email",
	commands: []command{
		{name: "accounts", run: listEmailAccounts},
		{name: "list", flags: []flagSpec{emailAccountFlag}, run: listEmail},
		{
			name: "read",
			flags: []flagSpec{emailAccountFlag, {
				name: "email", placeholder: "emailId", required: true,
				usage: "Message ID from list (folder:uid, or an INBOX uid); marks it read",
			}},
			run: readEmail,
		},
	},
}

// emailAccount matches checkemail's account configuration; extra fields such as
// archive are harmless because this resource only lists and reads messages.
type emailAccount struct {
	Server   string `json:"server"`
	Username string `json:"username"`
	Password string `json:"password"`
	Port     int    `json:"port,omitempty"`
}

type emailAccounts map[string]emailAccount

// emailAccountsPath resolves the requested accounts file without reading it.
func emailAccountsPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locating email accounts: %w", err)
	}
	return filepath.Join(home, ".config", "cysu", "accounts.json"), nil
}

// loadEmailAccounts validates credentials without ever printing their values.
func loadEmailAccounts() (emailAccounts, error) {
	path, err := emailAccountsPath()
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading email accounts %s: %w", path, err)
	}
	var accounts emailAccounts
	if err := json.Unmarshal(raw, &accounts); err != nil {
		return nil, fmt.Errorf("parsing email accounts %s: %w", path, err)
	}
	if accounts == nil {
		return nil, fmt.Errorf("email accounts %s must be a JSON object keyed by account ID", path)
	}
	for _, name := range accounts.names() {
		if strings.TrimSpace(name) == "" {
			return nil, fmt.Errorf("email accounts %s contains an empty account ID", path)
		}
		acct := accounts[name]
		for _, field := range []struct{ name, value string }{
			{"server", acct.Server}, {"username", acct.Username}, {"password", acct.Password},
		} {
			if strings.TrimSpace(field.value) == "" {
				return nil, fmt.Errorf("email account %q has no %q", name, field.name)
			}
		}
		if _, err := emailServerAddress(acct); err != nil {
			return nil, fmt.Errorf("email account %q: %w", name, err)
		}
	}
	return accounts, nil
}

// names returns stable account IDs, including an empty JSON array for no accounts.
func (a emailAccounts) names() []string {
	names := make([]string, 0, len(a))
	for name := range a {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// lookup prefers an exact ID, then an unambiguous case-insensitive match.
func (a emailAccounts) lookup(name string) (string, emailAccount, error) {
	if acct, ok := a[name]; ok {
		return name, acct, nil
	}
	match := ""
	for _, key := range a.names() {
		if strings.EqualFold(key, name) {
			if match != "" {
				return "", emailAccount{}, fmt.Errorf("ambiguous email account %q; use an exact account ID", name)
			}
			match = key
		}
	}
	if match != "" {
		return match, a[match], nil
	}
	return "", emailAccount{}, fmt.Errorf("unknown email account %q; known accounts: %s", name, strings.Join(a.names(), ", "))
}

// listEmailAccounts prints only IDs; it never connects to IMAP.
func listEmailAccounts(_ flagValues) error {
	accounts, err := loadEmailAccounts()
	if err != nil {
		return err
	}
	return printJSON(accounts.names())
}

// openEmailAccount loads the selected credentials and opens a TLS IMAP session.
func openEmailAccount(name string) (string, *emailConnection, error) {
	accounts, err := loadEmailAccounts()
	if err != nil {
		return "", nil, err
	}
	id, acct, err := accounts.lookup(name)
	if err != nil {
		return "", nil, err
	}
	conn, err := dialEmailAccount(acct)
	return id, conn, err
}

// listEmail prints the newest twenty inbox messages without changing read flags.
func listEmail(v flagValues) error {
	id, conn, err := openEmailAccount(v["account"])
	if err != nil {
		return err
	}
	defer conn.close()
	result, err := conn.list(id)
	if err != nil {
		return err
	}
	return printJSON(result)
}

// readEmail decodes one message and marks it read using a non-PEEK body fetch.
func readEmail(v flagValues) error {
	ref, err := parseEmailRef(v["email"])
	if err != nil {
		return usageErrorf("cysu email read --account <accountId> --email <emailId>", "%v", err)
	}
	id, conn, err := openEmailAccount(v["account"])
	if err != nil {
		return err
	}
	defer conn.close()
	result, err := conn.read(id, ref)
	if err != nil {
		return err
	}
	return printJSON(result)
}
