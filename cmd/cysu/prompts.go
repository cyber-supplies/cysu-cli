package main

// prompts.go validates prompt documents in local profile directories.

import "strings"

// validatePromptDocument catches malformed fields before contacting the API.
func validatePromptDocument(doc map[string]any, path string, requireComplete bool) (map[string]any, error) {
	allowed := map[string]bool{"name": true, "description": true, "content": true, "profile": true}
	for key, value := range doc {
		if !allowed[key] {
			return nil, failf("prompt file %s has unsupported field %q", path, key)
		}
		text, ok := value.(string)
		if !ok || (key != "description" && strings.TrimSpace(text) == "") {
			return nil, failf("prompt file %s has empty or non-string %q", path, key)
		}
	}
	if name, ok := doc["name"].(string); ok && !slugNameValid(name) {
		return nil, failf("prompt file %s: name must be a lowercase hyphenated slug", path)
	}
	if requireComplete {
		for _, key := range []string{"name", "content"} {
			if doc[key] == nil {
				return nil, failf("prompt file %s is missing %q", path, key)
			}
		}
	} else if len(doc) == 0 {
		return nil, failf("prompt file %s has no fields to modify", path)
	}
	return doc, nil
}
