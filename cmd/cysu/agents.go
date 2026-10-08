package main

// agents.go validates agent documents in local profile directories.

import "strings"

// validateAgentDocument sends only fields accepted by the agent API.
func validateAgentDocument(doc map[string]any, path string) (map[string]any, error) {
	allowed := map[string]bool{"name": true, "model": true, "description": true, "content": true, "profile": true}
	for key, value := range doc {
		if !allowed[key] {
			return nil, failf("agent file %s has unsupported field %q", path, key)
		}
		text, ok := value.(string)
		if !ok || strings.TrimSpace(text) == "" {
			return nil, failf("agent file %s has empty or non-string %q", path, key)
		}
	}
	for _, key := range []string{"name", "model", "description", "content"} {
		if doc[key] == nil {
			return nil, failf("agent file %s is missing %q", path, key)
		}
	}
	if name := doc["name"].(string); !slugNameValid(name) {
		return nil, failf("agent file %s: name must be a lowercase hyphenated slug", path)
	}
	return doc, nil
}

// slugNameValid mirrors the server's resource-name constraint.
func slugNameValid(name string) bool {
	if name == "" || name[0] == '-' || name[len(name)-1] == '-' {
		return false
	}
	previousDash := false
	for _, ch := range name {
		if ch == '-' {
			if previousDash {
				return false
			}
			previousDash = true
			continue
		}
		if !((ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9')) {
			return false
		}
		previousDash = false
	}
	return true
}
