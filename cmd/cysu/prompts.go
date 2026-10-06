package main

// prompts.go stores named system prompts through the gateway.

import (
	"bytes"
	"encoding/json"
	"net/url"
	"os"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

var promptIDFlag = flagSpec{name: "prompt", placeholder: "id", usage: "prompt id", required: true}
var promptFileFlag = flagSpec{name: "file", placeholder: "file", usage: "prompt Markdown or JSON file", required: true}
var promptProfileFlag = flagSpec{name: "profile", placeholder: "profile", usage: "profile name"}

var promptsResource = resource{
	name: "prompts", title: "Prompts", api: true,
	commands: []command{
		{name: "list", flags: []flagSpec{promptProfileFlag}, run: func(v flagValues) error {
			path := apiPath("prompts")
			if profile := v["profile"]; profile != "" {
				path += "?profile=" + url.QueryEscape(profile)
			}
			return request("GET", path, nil)
		}},
		{name: "create", flags: []flagSpec{promptFileFlag, promptProfileFlag}, run: func(v flagValues) error {
			doc, err := readPromptDocument(v["file"], true)
			if err != nil {
				return err
			}
			if v["profile"] != "" {
				doc["profile"] = v["profile"]
			}
			if doc["profile"] == nil {
				doc["profile"] = "default"
			}
			return request("POST", apiPath("prompts"), doc)
		}},
		{name: "modify", flags: []flagSpec{promptIDFlag, {name: "file", placeholder: "file", usage: "prompt Markdown or JSON file"}, promptProfileFlag}, run: func(v flagValues) error {
			doc := map[string]any{}
			if v["file"] != "" {
				var err error
				doc, err = readPromptDocument(v["file"], false)
				if err != nil {
					return err
				}
			}
			if v["profile"] != "" {
				doc["profile"] = v["profile"]
			}
			if len(doc) == 0 {
				return usageErrorf("cysu prompts modify --prompt <id> [--file <file>] [--profile <profile>]", "prompts modify: provide --file or --profile")
			}
			return request("PUT", apiPath("prompts", v["prompt"]), doc)
		}},
	},
}

// readPromptDocument accepts JSON API fields or Markdown with YAML front
// matter. A modify file may provide only the fields it changes.
func readPromptDocument(path string, requireComplete bool) (map[string]any, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, failf("reading prompt file %s: %v", path, err)
	}
	if !info.Mode().IsRegular() {
		return nil, failf("prompt file %s is not a regular file", path)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, failf("reading prompt file %s: %v", path, err)
	}
	if !utf8.Valid(raw) {
		return nil, failf("prompt file %s is not valid UTF-8", path)
	}
	raw = bytes.TrimPrefix(raw, []byte{0xef, 0xbb, 0xbf})
	var doc map[string]any
	if json.Unmarshal(raw, &doc) == nil && doc != nil {
		return validatePromptDocument(doc, path, requireComplete)
	}
	text := strings.ReplaceAll(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\r", "\n")
	lines := strings.Split(text, "\n")
	if len(lines) < 3 || lines[0] != "---" {
		return nil, failf("prompt file %s must be a JSON object or Markdown with YAML front matter", path)
	}
	closing := -1
	for i := 1; i < len(lines); i++ {
		if lines[i] == "---" {
			closing = i
			break
		}
	}
	if closing < 0 {
		return nil, failf("prompt file %s has no closing YAML front-matter delimiter", path)
	}
	var metadata struct {
		Name        string `yaml:"name"`
		Description string `yaml:"description"`
		Profile     string `yaml:"profile"`
	}
	if err := yaml.Unmarshal([]byte(strings.Join(lines[1:closing], "\n")), &metadata); err != nil {
		return nil, failf("parsing prompt file %s: %v", path, err)
	}
	doc = map[string]any{}
	if metadata.Name != "" {
		doc["name"] = metadata.Name
	}
	if metadata.Description != "" {
		doc["description"] = metadata.Description
	}
	if metadata.Profile != "" {
		doc["profile"] = metadata.Profile
	}
	if content := strings.TrimSpace(strings.Join(lines[closing+1:], "\n")); content != "" {
		doc["content"] = content
	}
	return validatePromptDocument(doc, path, requireComplete)
}

// validatePromptDocument catches malformed fields before contacting the API.
func validatePromptDocument(doc map[string]any, path string, requireComplete bool) (map[string]any, error) {
	allowed := map[string]bool{"name": true, "description": true, "content": true, "profile": true}
	for key, value := range doc {
		if !allowed[key] {
			return nil, failf("prompt file %s has unsupported field %q", path, key)
		}
		text, ok := value.(string)
		if !ok || strings.TrimSpace(text) == "" {
			return nil, failf("prompt file %s has empty or non-string %q", path, key)
		}
	}
	if name, ok := doc["name"].(string); ok && !slugNameValid(name) {
		return nil, failf("prompt file %s: name must be a lowercase hyphenated slug", path)
	}
	if requireComplete {
		for _, key := range []string{"name", "description", "content"} {
			if doc[key] == nil {
				return nil, failf("prompt file %s is missing %q", path, key)
			}
		}
	} else if len(doc) == 0 {
		return nil, failf("prompt file %s has no fields to modify", path)
	}
	return doc, nil
}
