package main

// skills.go is the `cysu skills` command: user-owned agent skills stored by
// the gateway. Skills routes accept USER tokens only; an LLM token is rejected
// by the gateway with its normal JSON error response.

import (
	"bytes"
	"fmt"
	"net/url"
	"os"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

// skillFlag identifies the skill changed or removed by a command.
var skillFlag = flagSpec{name: "skill", placeholder: "id", usage: "skill id", required: true}

// skillFileFlag identifies a SKILL.md file to create or replace a skill from.
var skillFileFlag = flagSpec{name: "file", placeholder: "file", usage: "SKILL.md file", required: true}

// skillProfileFlag assigns a stored skill to a profile. When omitted on
// create, the storage API assigns it to "default"; when omitted on replace,
// the existing profile is left unchanged.
var skillProfileFlag = flagSpec{name: "profile", placeholder: "profile", usage: "profile name"}

var skillsResource = resource{
	name:  "skills",
	title: "Skills",
	api:   true,
	commands: []command{
		{
			name:  "list",
			flags: []flagSpec{skillProfileFlag},
			run: func(v flagValues) error {
				return listSkills(v["profile"])
			},
		},
		{
			name:  "create",
			flags: []flagSpec{skillFileFlag, skillProfileFlag},
			run: func(v flagValues) error {
				return createSkill(v["file"], v["profile"])
			},
		},
		{
			name: "replace",
			flags: []flagSpec{
				skillFlag,
				skillFileFlag,
				skillProfileFlag,
			},
			run: func(v flagValues) error {
				return replaceSkill(v["skill"], v["file"], v["profile"])
			},
		},
		{
			name:  "install",
			flags: []flagSpec{providerFlag},
			run: func(v flagValues) error {
				return installSkills(v["provider"])
			},
		},
		{
			name:  "uninstall",
			flags: []flagSpec{providerFlag},
			local: true,
			run: func(v flagValues) error {
				return uninstallSkills(v["provider"])
			},
		},
		{
			name:  "remove",
			flags: []flagSpec{skillFlag},
			run: func(v flagValues) error {
				return removeSkill(v["skill"])
			},
		},
	},
}

// skillDocument is the gateway representation assembled from one SKILL.md.
type skillDocument struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Content     string `json:"content"`
	Profile     string `json:"profile,omitempty"`
}

// skillMetadata is the part of a SKILL.md read from its YAML front matter.
// Other front-matter fields are intentionally ignored so standard skill files
// containing metadata, license or compatibility keys remain usable.
type skillMetadata struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

// listSkills prints every skill owned by the authenticated user.
func listSkills(profile string) error {
	path := apiPath("skills")
	if profile != "" {
		if strings.TrimSpace(profile) == "" {
			return failf("--profile must not be empty")
		}
		path += "?profile=" + url.QueryEscape(profile)
	}
	return request("GET", path, nil)
}

// createSkill parses a SKILL.md, stores it, and prints the gateway response.
func createSkill(path, profile string) error {
	doc, err := readSkillDocument(path)
	if err != nil {
		return err
	}
	doc.Profile = profile
	return request("POST", apiPath("skills"), doc)
}

// replaceSkill parses a SKILL.md and replaces its document fields. Profile is
// changed only when --profile is provided.
func replaceSkill(id, path, profile string) error {
	doc, err := readSkillDocument(path)
	if err != nil {
		return err
	}
	doc.Profile = profile
	return request("PUT", apiPath("skills", id), doc)
}

// removeSkill deletes one skill and prints the gateway response.
func removeSkill(id string) error {
	return request("DELETE", apiPath("skills", id), nil)
}

// readSkillDocument reads and validates a regular UTF-8 SKILL.md file.
func readSkillDocument(path string) (skillDocument, error) {
	info, err := os.Stat(path)
	if err != nil {
		return skillDocument{}, failf("reading skill file %s: %v", path, err)
	}
	if !info.Mode().IsRegular() {
		return skillDocument{}, failf("skill file %s is not a regular file", path)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		return skillDocument{}, failf("reading skill file %s: %v", path, err)
	}
	doc, err := parseSkillDocument(raw)
	if err != nil {
		return skillDocument{}, failf("parsing skill file %s: %v", path, err)
	}
	return doc, nil
}

// parseSkillDocument splits YAML front matter from Markdown content and turns
// the fields required by the gateway into a skillDocument.
func parseSkillDocument(raw []byte) (skillDocument, error) {
	raw = bytes.TrimPrefix(raw, []byte{0xef, 0xbb, 0xbf})
	if !utf8.Valid(raw) {
		return skillDocument{}, fmt.Errorf("file is not valid UTF-8")
	}

	text := strings.ReplaceAll(string(raw), "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	lines := strings.Split(text, "\n")
	if len(lines) == 0 || lines[0] != "---" {
		return skillDocument{}, fmt.Errorf("missing opening YAML front-matter delimiter ---")
	}

	closing := -1
	for i := 1; i < len(lines); i++ {
		if lines[i] == "---" {
			closing = i
			break
		}
	}
	if closing < 0 {
		return skillDocument{}, fmt.Errorf("missing closing YAML front-matter delimiter ---")
	}

	var metadata skillMetadata
	if err := yaml.Unmarshal([]byte(strings.Join(lines[1:closing], "\n")), &metadata); err != nil {
		return skillDocument{}, fmt.Errorf("invalid YAML front matter: %w", err)
	}

	doc := skillDocument{
		Name:        strings.TrimSpace(metadata.Name),
		Description: strings.TrimSpace(metadata.Description),
		Content:     strings.TrimSpace(strings.Join(lines[closing+1:], "\n")),
	}
	if doc.Name == "" {
		return skillDocument{}, fmt.Errorf("front matter has no name")
	}
	if doc.Description == "" {
		return skillDocument{}, fmt.Errorf("front matter has no description")
	}
	if doc.Content == "" {
		return skillDocument{}, fmt.Errorf("Markdown content is empty")
	}
	return doc, nil
}
