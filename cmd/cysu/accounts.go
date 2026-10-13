package main

// accounts.go manages credentials shared through the accounts service.
// Passwords and cookies are encrypted before they leave this process and
// decrypted only for CLI output; the gateway and service see opaque base64.

import (
	"encoding/json"
	"net/url"
	"strings"
)

var accountIDFlag = flagSpec{
	name: "account", placeholder: "account", usage: "account TypeID", required: true,
}

var accountsResource = resource{
	name: "accounts", title: "Accounts", api: true,
	commands: []command{
		{
			name: "list",
			flags: []flagSpec{
				{name: "location", placeholder: "location", usage: "PostgreSQL regex matched against account locations"},
			},
			run: func(v flagValues) error { return listAccounts(v["location"]) },
		},
		{
			name:  "get",
			flags: []flagSpec{accountIDFlag},
			run:   func(v flagValues) error { return getAccount(v["account"]) },
		},
		{
			name: "set-cookie",
			flags: []flagSpec{accountIDFlag, {
				name: "cookie", placeholder: "cookie", usage: "cookie value to encrypt", required: true,
			}},
			run: func(v flagValues) error { return setAccountCookie(v["account"], v["cookie"]) },
		},
		{
			name: "create",
			flags: []flagSpec{
				{name: "username", placeholder: "user", usage: "account username", required: true},
				{name: "password", placeholder: "pass", usage: "account password to encrypt", required: true},
				{name: "location", placeholder: "location", usage: "absolute HTTP(S) login URL", required: true},
			},
			run: func(v flagValues) error {
				return createAccount(v["username"], v["password"], v["location"])
			},
		},
		{
			name: "modify",
			flags: []flagSpec{accountIDFlag,
				{name: "username", placeholder: "user", usage: "replacement username"},
				{name: "password", placeholder: "pass", usage: "replacement password to encrypt"},
				{name: "location", placeholder: "location", usage: "replacement absolute HTTP(S) login URL"},
			},
			validate: func(v flagValues) error {
				if strings.TrimSpace(v["username"]) == "" && strings.TrimSpace(v["password"]) == "" && strings.TrimSpace(v["location"]) == "" {
					return cliError{message: "accounts modify: provide --username, --password, or --location", exit: exitUsage}
				}
				return nil
			},
			run: func(v flagValues) error {
				return modifyAccount(v["account"], v["username"], v["password"], v["location"])
			},
		},
		{
			name:  "remove",
			flags: []flagSpec{accountIDFlag},
			run:   func(v flagValues) error { return removeAccount(v["account"]) },
		},
	},
}

type account struct {
	ID        string  `json:"id"`
	Username  string  `json:"username"`
	Password  string  `json:"password"`
	Location  string  `json:"location"`
	Cookie    *string `json:"cookie"`
	UpdatedAt *string `json:"updatedAt"`
}

func listAccounts(location string) error {
	path := apiPath("accounts")
	if location != "" {
		query := url.Values{}
		query.Set("location", location)
		path += "?" + query.Encode()
	}
	return request("GET", path, nil)
}

func getAccount(id string) error {
	acct, err := fetchAccount(id)
	if err != nil {
		return err
	}
	return printJSON(acct)
}

func createAccount(username, password, location string) error {
	encrypted, err := encryptString(password)
	if err != nil {
		return err
	}
	raw, err := send("POST", apiPath("accounts"), map[string]string{
		"username": username, "password": encrypted, "location": location,
	})
	if err != nil {
		return err
	}
	return printDecryptedAccount(raw)
}

func modifyAccount(id, username, password, location string) error {
	acct, err := fetchAccount(id)
	if err != nil {
		return err
	}
	if username != "" {
		acct.Username = username
	}
	if password != "" {
		acct.Password = password
	}
	if location != "" {
		acct.Location = location
	}
	encrypted, err := encryptString(acct.Password)
	if err != nil {
		return err
	}
	raw, err := send("PUT", apiPath("accounts", id), map[string]string{
		"username": acct.Username, "password": encrypted, "location": acct.Location,
	})
	if err != nil {
		return err
	}
	return printDecryptedAccount(raw)
}

func setAccountCookie(id, cookie string) error {
	encrypted, err := encryptString(cookie)
	if err != nil {
		return err
	}
	raw, err := send("POST", apiPath("account", id, "setCookie"), map[string]string{
		"cookie": encrypted,
	})
	if err != nil {
		return err
	}
	return printDecryptedAccount(raw)
}

func removeAccount(id string) error {
	return request("DELETE", apiPath("accounts", id), nil)
}

func fetchAccount(id string) (account, error) {
	raw, err := send("GET", apiPath("accounts", id), nil)
	if err != nil {
		return account{}, err
	}
	acct, err := decodeAccount(raw)
	if err != nil {
		return account{}, err
	}
	if err := decryptAccount(&acct); err != nil {
		return account{}, err
	}
	return acct, nil
}

func printDecryptedAccount(raw []byte) error {
	acct, err := decodeAccount(raw)
	if err != nil {
		return err
	}
	if err := decryptAccount(&acct); err != nil {
		return err
	}
	return printJSON(acct)
}

func decodeAccount(raw []byte) (account, error) {
	var acct account
	if json.Unmarshal(raw, &acct) != nil || acct.ID == "" || acct.Username == "" || acct.Password == "" || acct.Location == "" {
		return account{}, failf("invalid account response")
	}
	return acct, nil
}

func decryptAccount(acct *account) error {
	password, err := decryptIfEncrypted(acct.Password)
	if err != nil {
		return err
	}
	acct.Password = password
	if acct.Cookie != nil {
		cookie, err := decryptIfEncrypted(*acct.Cookie)
		if err != nil {
			return err
		}
		acct.Cookie = &cookie
	}
	return nil
}
