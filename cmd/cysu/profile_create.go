package main

import (
	"os"
	"path/filepath"
)

// createProfile scaffolds a local profile without contacting the API.
// The directory must not already exist; saving publishes the completed profile.
func createProfile(profile string, example bool) error {
	root, statePath, err := profileEditPaths(profile)
	if err != nil {
		return err
	}
	unlock, err := lockProfileEdit(statePath)
	if err != nil {
		return err
	}
	defer unlock()
	if err := os.MkdirAll(filepath.Dir(root), 0700); err != nil {
		return err
	}
	if err := os.Mkdir(root, 0700); err != nil {
		if os.IsExist(err) {
			return failf("profile %q already exists locally at %s", profile, root)
		}
		return err
	}
	keep := false
	defer func() {
		if !keep {
			_ = os.RemoveAll(root)
		}
	}()
	for _, kind := range profileEditKinds {
		if err := os.Mkdir(filepath.Join(root, kind), 0700); err != nil {
			return err
		}
	}
	if err := writeProfileEditTags(root, []string{}); err != nil {
		return err
	}
	items := []profileEditItem{}
	if example {
		items = exampleProfileItems()
		for _, item := range items {
			raw, err := renderProfileEditItem(item)
			if err != nil {
				return err
			}
			dir := filepath.Join(root, item.Kind, item.Name)
			if err := os.Mkdir(dir, 0700); err != nil {
				return err
			}
			if err := os.WriteFile(filepath.Join(dir, profileEditFiles[item.Kind]), raw, 0600); err != nil {
				return err
			}
		}
	}
	if _, err := readProfileEditItems(root, profile); err != nil {
		return err
	}
	keep = true
	return printJSON(map[string]any{"success": true, "profile": profile, "directory": root, "items": len(items)})
}

// exampleProfileItems supplies valid, inert starter documents for every kind.
func exampleProfileItems() []profileEditItem {
	return []profileEditItem{
		{Kind: "skills", Name: "example-skill", Doc: map[string]any{
			"name": "example-skill", "description": "Example skill; replace with your own instructions.",
			"content": "Summarize the supplied information and list any unanswered questions.\n",
		}},
		{Kind: "agents", Name: "example-agent", Doc: map[string]any{
			"name": "example-agent", "model": "inherit", "description": "Example agent; customize its role.",
			"content": "Review the supplied material and return a concise summary.\n",
		}},
		{Kind: "mcp", Name: "example-mcp", Doc: map[string]any{
			"name": "example-mcp", "type": "http", "url": "https://example.com/mcp", "headers": map[string]any{},
		}},
		{Kind: "prompts", Name: "example-prompt", Doc: map[string]any{
			"name": "example-prompt", "content": "Be concise, explain your assumptions, and distinguish facts from uncertainty.\n",
		}},
	}
}
