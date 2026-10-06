package main

// agents.go manages user-owned agent definitions through the gateway.

import (
	"bytes"
	"encoding/json"
	"net/url"
	"os"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

var agentIDFlag = flagSpec{name: "agent", placeholder: "id", usage: "agent id", required: true}
var agentFileFlag = flagSpec{name: "file", placeholder: "file", usage: "agent Markdown or JSON file", required: true}
var agentProfileFlag = flagSpec{name: "profile", placeholder: "profile", usage: "profile name"}

var agentsResource = resource{
	name: "agents", title: "Agents", api: true,
	commands: []command{
		{name: "list", flags: []flagSpec{agentProfileFlag}, run: func(v flagValues) error {
			path := apiPath("agents")
			if profile := v["profile"]; profile != "" {
				path += "?profile=" + url.QueryEscape(profile)
			}
			return request("GET", path, nil)
		}},
		{name: "create", flags: []flagSpec{agentFileFlag, agentProfileFlag}, run: func(v flagValues) error {
			doc, err := readAgentDocument(v["file"])
			if err != nil {
				return err
			}
			if v["profile"] != "" {
				doc["profile"] = v["profile"]
			}
			if doc["profile"] == nil {
				doc["profile"] = "default"
			}
			return request("POST", apiPath("agents"), doc)
		}},
		{name: "replace", flags: []flagSpec{agentIDFlag, {name: "file", placeholder: "file", usage: "agent Markdown or JSON file"}, agentProfileFlag}, run: func(v flagValues) error {
			doc := map[string]any{}
			if v["file"] != "" {
				var err error
				doc, err = readAgentDocument(v["file"])
				if err != nil {
					return err
				}
			}
			if v["profile"] != "" {
				doc["profile"] = v["profile"]
			}
			if len(doc) == 0 {
				return usageErrorf("cysu agents replace --agent <id> [--file <file>] [--profile <profile>]", "agents replace: provide --file or --profile")
			}
			return request("PUT", apiPath("agents", v["agent"]), doc)
		}},
		{name: "remove", flags: []flagSpec{agentIDFlag}, run: func(v flagValues) error {
			return request("DELETE", apiPath("agents", v["agent"]), nil)
		}},
	},
}

// readAgentDocument accepts JSON API fields or Markdown with YAML front matter.
// A Markdown file's body becomes content; absent fields stay absent on update.
func readAgentDocument(path string) (map[string]any, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, failf("reading agent file %s: %v", path, err)
	}
	if !info.Mode().IsRegular() {
		return nil, failf("agent file %s is not a regular file", path)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, failf("reading agent file %s: %v", path, err)
	}
	if !utf8.Valid(raw) {
		return nil, failf("agent file %s is not valid UTF-8", path)
	}
	raw = bytes.TrimPrefix(raw, []byte{0xef, 0xbb, 0xbf})
	doc := map[string]any{}
	if json.Unmarshal(raw, &doc) == nil && doc != nil {
		return validateAgentDocument(doc, path)
	}
	text := strings.ReplaceAll(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\r", "\n")
	lines := strings.Split(text, "\n")
	if len(lines) < 3 || lines[0] != "---" {
		return nil, failf("agent file %s must be a JSON object or Markdown with YAML front matter", path)
	}
	closing := -1
	for i := 1; i < len(lines); i++ {
		if lines[i] == "---" {
			closing = i
			break
		}
	}
	if closing < 0 {
		return nil, failf("agent file %s has no closing YAML front-matter delimiter", path)
	}
	var metadata struct {
		Name        string `yaml:"name"`
		Model       string `yaml:"model"`
		Description string `yaml:"description"`
		Profile     string `yaml:"profile"`
	}
	if err := yaml.Unmarshal([]byte(strings.Join(lines[1:closing], "\n")), &metadata); err != nil {
		return nil, failf("parsing agent file %s: %v", path, err)
	}
	doc = map[string]any{"name": metadata.Name, "model": metadata.Model, "description": metadata.Description, "content": strings.TrimSpace(strings.Join(lines[closing+1:], "\n"))}
	if metadata.Profile != "" {
		doc["profile"] = metadata.Profile
	}
	return validateAgentDocument(doc, path)
}

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
