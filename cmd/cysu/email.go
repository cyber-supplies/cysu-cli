package main

// email.go loads mail accounts from the email API. Listing and reading use
// IMAP; sending uses SMTP directly from this process.

import (
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

var emailAccountFlag = flagSpec{
	name: "account", placeholder: "accountId", required: true,
	usage: "Email account ID",
}

var emailPortFlag = flagSpec{
	name: "port", placeholder: "int", usage: "IMAP TLS port (1-65535; 0 or omitted uses 993)",
}

var emailResource = resource{
	name: "email", title: "Email", api: true, keyless: true,
	commands: []command{
		{name: "accounts", run: listEmailAccounts},
		{
			name: "create",
			flags: []flagSpec{emailAccountFlag,
				{name: "server", placeholder: "server", usage: "IMAP server host or address", required: true},
				{name: "username", placeholder: "username", usage: "IMAP login username", required: true},
				{name: "password", placeholder: "password", usage: "IMAP login password", required: true},
				emailPortFlag,
			},
			validate: validateEmailPortFlag,
			run:      createEmailAccount,
		},
		{
			name: "modify",
			flags: []flagSpec{emailAccountFlag,
				{name: "server", placeholder: "server", usage: "replacement IMAP server"},
				{name: "username", placeholder: "username", usage: "replacement IMAP username"},
				{name: "password", placeholder: "password", usage: "replacement IMAP password"},
				emailPortFlag,
			},
			validate: validateModifyEmailAccount,
			run:      modifyEmailAccount,
		},
		{name: "list", flags: []flagSpec{emailAccountFlag}, run: listEmail},
		{name: "send", flags: []flagSpec{emailAccountFlag,
			{name: "recipient", placeholder: "email", required: true, usage: "Recipient email address"},
			{name: "subject", placeholder: "subject", required: true, usage: "Message subject"},
			{name: "body", placeholder: "body", required: true, usage: "Plain text message body"},
		}, run: sendEmail},
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

// emailAccount is the full API record. Storage decrypts the password before
// returning it, and this process uses it to log in to IMAP or SMTP.
type emailAccount struct {
	ID       string `json:"id"`
	Server   string `json:"server"`
	Username string `json:"username"`
	Password string `json:"password"`
	Port     int    `json:"port,omitempty"`
}

type emailAccounts map[string]emailAccount

// parseEmailPort rejects values the email API cannot store.
func parseEmailPort(value string) (int, error) {
	port, err := strconv.Atoi(value)
	if err != nil || port < 0 || port > 65535 {
		return 0, cliError{message: "email port must be an integer from 0 to 65535", exit: exitUsage}
	}
	return port, nil
}

// validateEmailPortFlag checks an optional port before authentication.
func validateEmailPortFlag(v flagValues) error {
	if v.provided("port") {
		_, err := parseEmailPort(v["port"])
		return err
	}
	return nil
}

// validateModifyEmailAccount requires at least one nonempty replacement.
func validateModifyEmailAccount(v flagValues) error {
	changed := false
	for _, field := range []string{"server", "username", "password"} {
		if v.provided(field) {
			changed = true
			if strings.TrimSpace(v[field]) == "" {
				return cliError{message: "email modify: --" + field + " cannot be empty", exit: exitUsage}
			}
		}
	}
	if v.provided("port") {
		changed = true
		if err := validateEmailPortFlag(v); err != nil {
			return err
		}
	}
	if !changed {
		return cliError{message: "email modify: provide --server, --username, --password, or --port", exit: exitUsage}
	}
	return nil
}

// loadEmailAccounts lists the caller's API accounts and indexes them by ID.
// Malformed account data is rejected before any mail connection is attempted.
func loadEmailAccounts() (emailAccounts, error) {
	raw, err := send("GET", apiPath("emails"), nil)
	if err != nil {
		return nil, err
	}
	var rows []emailAccount
	if err := json.Unmarshal(raw, &rows); err != nil || rows == nil {
		return nil, failf("emails: unexpected response from the API")
	}
	accounts := make(emailAccounts, len(rows))
	for _, acct := range rows {
		if strings.TrimSpace(acct.ID) == "" || strings.TrimSpace(acct.Server) == "" ||
			strings.TrimSpace(acct.Username) == "" || strings.TrimSpace(acct.Password) == "" {
			return nil, failf("emails: incomplete account from the API")
		}
		if _, exists := accounts[acct.ID]; exists {
			return nil, failf("emails: duplicate account ID from the API")
		}
		if _, err := emailServerAddress(acct); err != nil {
			return nil, fmt.Errorf("email account %q: %w", acct.ID, err)
		}
		accounts[acct.ID] = acct
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

// listEmailAccounts prints account IDs and usernames without exposing passwords.
func listEmailAccounts(_ flagValues) error {
	accounts, err := loadEmailAccounts()
	if err != nil {
		return err
	}
	type accountSummary struct {
		ID    string `json:"id"`
		Email string `json:"email"`
	}
	result := make([]accountSummary, 0, len(accounts))
	for _, id := range accounts.names() {
		result = append(result, accountSummary{ID: id, Email: accounts[id].Username})
	}
	return printJSON(result)
}

// createEmailAccount stores a new full account in the API and prints it.
func createEmailAccount(v flagValues) error {
	acct := emailAccount{
		ID: v["account"], Server: v["server"],
		Username: v["username"], Password: v["password"],
	}
	if v.provided("port") {
		port, err := parseEmailPort(v["port"])
		if err != nil {
			return err
		}
		acct.Port = port
	}
	return request("POST", apiPath("emails"), acct)
}

// modifyEmailAccount preserves omitted fields because PUT replaces the entire
// account. The API's exact ID is kept even for a case-insensitive match.
func modifyEmailAccount(v flagValues) error {
	accounts, err := loadEmailAccounts()
	if err != nil {
		return err
	}
	_, acct, err := accounts.lookup(v["account"])
	if err != nil {
		return err
	}
	if v.provided("server") {
		acct.Server = v["server"]
	}
	if v.provided("username") {
		acct.Username = v["username"]
	}
	if v.provided("password") {
		acct.Password = v["password"]
	}
	if v.provided("port") {
		port, err := parseEmailPort(v["port"])
		if err != nil {
			return err
		}
		acct.Port = port
	}
	return request("PUT", apiPath("emails"), acct)
}

// sendEmail loads the selected account through the API, then sends directly
// from this machine to its configured SMTP server.
func sendEmail(v flagValues) error {
	accounts, err := loadEmailAccounts()
	if err != nil {
		return err
	}
	id, acct, err := accounts.lookup(v["account"])
	if err != nil {
		return err
	}
	if err := smtpDeliver(smtpDeliveryAccount(acct), v["recipient"], v["subject"], v["body"]); err != nil {
		return err
	}
	return printJSON(map[string]any{"success": true, "account": id, "recipient": v["recipient"]})
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
