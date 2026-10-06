package main

// prompts_install.go fetches and combines the system prompts in one profile.

import (
	"encoding/json"
	"net/url"
	"sort"
	"strings"
	"unicode/utf8"
)

type storedPrompt struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Content string `json:"content"`
	Profile string `json:"profile"`
}

// fetchPrompts reads only prompts assigned to profile. It verifies the profile
// on every returned entry in case an older gateway ignores the query filter.
func fetchPrompts(profile string) ([]storedPrompt, error) {
	raw, err := send("GET", apiPath("prompts")+"?profile="+url.QueryEscape(profile), nil)
	if err != nil {
		return nil, err
	}
	var prompts []storedPrompt
	if len(raw) == 0 || raw[0] != '[' || json.Unmarshal(raw, &prompts) != nil {
		return nil, failf("unexpected prompts response: expected a JSON array")
	}
	for _, prompt := range prompts {
		if err := checkProfileEntry(prompt.ID, prompt.Profile, profile); err != nil {
			return nil, err
		}
		if !slugNameValid(prompt.Name) || !utf8.ValidString(prompt.Content) || strings.TrimSpace(prompt.Content) == "" {
			return nil, failf("prompt %s has invalid name or content", prompt.ID)
		}
	}
	sort.Slice(prompts, func(i, j int) bool {
		if prompts[i].Name != prompts[j].Name {
			return prompts[i].Name < prompts[j].Name
		}
		return prompts[i].ID < prompts[j].ID
	})
	return prompts, nil
}

// concatenatePrompts joins prompt bodies in stable name order with one blank
// line between them and a final newline for instruction files.
func concatenatePrompts(prompts []storedPrompt) []byte {
	parts := make([]string, len(prompts))
	for i, prompt := range prompts {
		parts[i] = strings.TrimRight(prompt.Content, "\r\n")
	}
	if len(parts) == 0 {
		return nil
	}
	return []byte(strings.Join(parts, "\n\n") + "\n")
}
