package main

// findings.go is the `cysu findings` command.
//
// A finding's title and description are encrypted with the master key
// before they leave this machine (see encryption.go), so the backend stores
// and returns ciphertext for them. Every response that carries findings is
// decrypted here before it is printed. Severity and ids stay in the clear.

import (
	"bytes"
	"encoding/json"
	"fmt"
)

var findingFlag = flagSpec{name: "finding", placeholder: "id", usage: "finding id", required: true}

// encryptedFindingFields are the finding fields that hold ciphertext.
var encryptedFindingFields = []string{"title", "description"}

var findingsResource = resource{
	name:  "findings",
	title: "Findings",
	api:   true,
	commands: []command{
		{
			name:  "list",
			flags: []flagSpec{projectFlag},
			run: func(v flagValues) error {
				return findingsRequest("GET", apiPath("projects", v["project"], "findings"), nil)
			},
		},
		{
			name:  "get",
			flags: []flagSpec{projectFlag, findingFlag},
			run: func(v flagValues) error {
				return findingsRequest("GET", apiPath("projects", v["project"], "findings", v["finding"]), nil)
			},
		},
		{
			name: "create",
			flags: []flagSpec{
				projectFlag,
				{name: "title", placeholder: "title", usage: "finding title", required: true},
				{name: "desc", placeholder: "description", usage: "finding description", required: true},
				{name: "severity", placeholder: "sev", usage: "finding severity"},
			},
			run: func(v flagValues) error {
				title, err := encryptString(v["title"])
				if err != nil {
					return err
				}
				description, err := encryptString(v["desc"])
				if err != nil {
					return err
				}
				body := map[string]any{"title": title, "description": description}
				if severity := v["severity"]; severity != "" {
					body["severity"] = severity
				}
				return findingsRequest("POST", apiPath("projects", v["project"], "findings"), body)
			},
		},
		{
			name:  "delete",
			flags: []flagSpec{projectFlag, findingFlag},
			run: func(v flagValues) error {
				return findingsRequest("DELETE", apiPath("projects", v["project"], "findings", v["finding"]), nil)
			},
		},
	},
}

// findingsRequest is request for responses that carry findings: the
// encrypted fields are decrypted before printing.
func findingsRequest(method, path string, body any) error {
	raw, err := send(method, path, body)
	if err != nil {
		return err
	}
	decrypted, err := decryptFindings(raw)
	if err != nil {
		return err
	}
	printResponse(decrypted)
	return nil
}

// decryptFindings returns raw with the encrypted fields of every finding in
// it decrypted. It understands the shapes findings arrive in: a single
// finding (bare, or under "finding") and a list (bare, or under
// "findings"). Anything else is returned unchanged. Untouched fields keep
// their exact bytes; the keys of a rewritten object come out sorted.
func decryptFindings(raw []byte) ([]byte, error) {
	var list []json.RawMessage
	if json.Unmarshal(raw, &list) == nil {
		return decryptFindingList(list)
	}

	var obj map[string]json.RawMessage
	if json.Unmarshal(raw, &obj) != nil {
		return raw, nil
	}
	switch {
	case obj["finding"] != nil:
		finding, err := decryptFinding(obj["finding"])
		if err != nil {
			return nil, err
		}
		obj["finding"] = finding
	case obj["findings"] != nil:
		if err := json.Unmarshal(obj["findings"], &list); err != nil {
			return raw, nil
		}
		findings, err := decryptFindingList(list)
		if err != nil {
			return nil, err
		}
		obj["findings"] = findings
	default:
		return decryptFinding(raw)
	}
	return marshalJSON(obj)
}

func decryptFindingList(list []json.RawMessage) ([]byte, error) {
	out := make([]json.RawMessage, len(list))
	for i, item := range list {
		finding, err := decryptFinding(item)
		if err != nil {
			return nil, err
		}
		out[i] = finding
	}
	return marshalJSON(out)
}

// decryptFinding decrypts the string fields of one finding object that hold
// ciphertext. A non-object, or one without those fields, is returned as is.
func decryptFinding(raw json.RawMessage) (json.RawMessage, error) {
	var obj map[string]json.RawMessage
	if json.Unmarshal(raw, &obj) != nil {
		return raw, nil
	}
	changed := false
	for _, field := range encryptedFindingFields {
		var value string
		if obj[field] == nil || json.Unmarshal(obj[field], &value) != nil {
			continue
		}
		plain, err := decryptIfEncrypted(value)
		if err != nil {
			return nil, fmt.Errorf("finding %s %s: %w", scalarString(obj["id"]), field, err)
		}
		if plain == value {
			continue
		}
		encoded, err := marshalJSON(plain)
		if err != nil {
			return nil, err
		}
		obj[field] = encoded
		changed = true
	}
	if !changed {
		return raw, nil
	}
	return marshalJSON(obj)
}

// marshalJSON encodes v without HTML escaping, so a description containing
// <script> reads back as written rather than as \u003cscript\u003e.
func marshalJSON(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}
