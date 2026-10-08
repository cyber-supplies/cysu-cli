package main

// prompts_install.go fetches and combines the system prompts in one profile.

import (
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

// validateStoredPrompts checks and orders prompts from a whole-profile snapshot.
func validateStoredPrompts(prompts []storedPrompt) error {
	for _, prompt := range prompts {
		if !slugNameValid(prompt.Name) || !utf8.ValidString(prompt.Content) || strings.TrimSpace(prompt.Content) == "" {
			return failf("prompt %s has invalid name or content", prompt.Name)
		}
	}
	sort.Slice(prompts, func(i, j int) bool { return prompts[i].Name < prompts[j].Name })
	return nil
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
