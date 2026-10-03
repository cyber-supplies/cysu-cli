package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/pelletier/go-toml/v2"
	"gopkg.in/yaml.v3"
)

var profileAgentName = regexp.MustCompile("^[a-z0-9]+(-[a-z0-9]+)*$")
var profileMCPID = regexp.MustCompile("^mcp_[0-7][0-9a-hjkmnp-tv-z]{25}$")
var profileHeaderName = regexp.MustCompile("^[!#$%&'*+.^_`|~0-9A-Za-z-]+$")

// Render and validate everything in memory before creating a backup or
// replacing any provider files. MCP commands are stored, never executed.
func prepareProfile(layout profileLayout, bundle profileBundle) ([]profileChange, error) {
	var plan []profileChange
	for _, target := range layout.Targets {
		if err := validateProfileTarget(target); err != nil {
			return nil, err
		}
		change := profileChange{Target: target, Files: map[string][]byte{}}
		switch target.Kind {
		case "skills":
			prepared, err := prepareSkills(bundle.Skills, target.Path)
			if err != nil {
				return nil, err
			}
			for _, skill := range prepared {
				change.Files[filepath.Join(skill.Name, "SKILL.md")] = skill.data
			}
		case "agents":
			for _, agent := range bundle.Agents {
				if !profileAgentName.MatchString(agent.Name) {
					return nil, failf("agent %s has an invalid name", agent.ID)
				}
				name := agent.Name + ".md"
				if layout.Provider == skillProviderCodex {
					name = agent.Name + ".toml"
				}
				if _, exists := change.Files[name]; exists {
					return nil, failf("duplicate agent name %q", agent.Name)
				}
				raw, err := renderProfileAgent(layout.Provider, agent)
				if err != nil {
					return nil, err
				}
				change.Files[name] = raw
			}
		case "config":
			raw, err := readProfileConfig(target.Path)
			if err != nil {
				return nil, err
			}
			config, err := decodeProfileConfig(layout.Provider, raw)
			if err != nil {
				return nil, err
			}
			servers, err := profileMCPServers(layout.Provider, bundle.MCP)
			if err != nil {
				return nil, err
			}
			config[profileMCPKey(layout.Provider)] = servers
			change.Data, err = encodeProfileConfig(layout.Provider, config)
			if err != nil {
				return nil, err
			}
		case "legacy-skills":
			// Keep legacy Codex skills in the selected scope inactive until uninstall.
		default:
			return nil, failf("unknown profile target %q", target.Kind)
		}
		plan = append(plan, change)
	}
	return plan, nil
}

func renderProfileAgent(provider string, agent storedAgent) ([]byte, error) {
	for _, value := range []string{agent.Model, agent.Description, agent.Content} {
		if strings.TrimSpace(value) == "" || !utf8.ValidString(value) {
			return nil, failf("agent %s has invalid model, description, or content", agent.ID)
		}
	}
	if provider == skillProviderCodex {
		metadata := map[string]any{
			"name": agent.Name, "description": strings.TrimSpace(agent.Description),
			"developer_instructions": agent.Content,
		}
		// Codex inherits the parent model by omitting this field, whereas
		// Claude uses the explicit "inherit" value.
		if model := strings.TrimSpace(agent.Model); model != "inherit" {
			metadata["model"] = model
		}
		return toml.Marshal(metadata)
	}
	metadata := struct {
		Name        string `yaml:"name"`
		Description string `yaml:"description"`
		Model       string `yaml:"model"`
	}{agent.Name, strings.TrimSpace(agent.Description), strings.TrimSpace(agent.Model)}
	raw, err := yaml.Marshal(metadata)
	if err != nil {
		return nil, err
	}
	return bytes.Join([][]byte{[]byte("---\n"), raw, []byte("---\n\n"), []byte(agent.Content), []byte("\n")}, nil), nil
}

func profileMCPServers(provider string, servers []storedMCP) (map[string]any, error) {
	result := make(map[string]any, len(servers))
	for _, server := range servers {
		if !profileMCPID.MatchString(server.ID) {
			return nil, failf("MCP entry has an invalid id")
		}
		if _, exists := result[server.ID]; exists {
			return nil, failf("duplicate MCP id %s", server.ID)
		}
		config := map[string]any{}
		switch server.Type {
		case "stdio":
			if strings.TrimSpace(server.Command) == "" || server.Args == nil || server.Headers != nil {
				return nil, failf("MCP server %s has an invalid stdio configuration", server.ID)
			}
			config["command"] = strings.TrimSpace(server.Command)
			config["args"] = server.Args
		case "http":
			u, err := url.Parse(server.URL)
			if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || server.Headers == nil || server.Command != "" || server.Args != nil {
				return nil, failf("MCP server %s has an invalid HTTP configuration", server.ID)
			}
			for name, value := range server.Headers {
				if !profileHeaderName.MatchString(name) || strings.ContainsAny(value, "\r\n\x00") {
					return nil, failf("MCP server %s has an invalid HTTP header", server.ID)
				}
			}
			config["url"] = server.URL
			headersKey := "headers"
			if provider == skillProviderCodex {
				headersKey = "http_headers"
			}
			config[headersKey] = server.Headers
		default:
			return nil, failf("MCP server %s has an unsupported type", server.ID)
		}
		if provider != skillProviderCodex {
			config["type"] = server.Type
		}
		result[server.ID] = config
	}
	return result, nil
}

func profileMCPKey(provider string) string {
	if provider == skillProviderCodex {
		return "mcp_servers"
	}
	return "mcpServers"
}

func decodeProfileConfig(provider string, raw []byte) (map[string]any, error) {
	config := map[string]any{}
	if len(bytes.TrimSpace(raw)) == 0 {
		return config, nil
	}
	if provider == skillProviderCodex {
		if err := toml.Unmarshal(raw, &config); err != nil {
			return nil, failf("invalid Codex TOML configuration; repair config.toml before installing or uninstalling a profile")
		}
	} else {
		// UseNumber retains large integer settings rather than rounding them.
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		if !json.Valid(raw) || decoder.Decode(&config) != nil || config == nil {
			return nil, failf("invalid provider JSON configuration; repair it before installing or uninstalling a profile")
		}
	}
	if value, exists := config[profileMCPKey(provider)]; exists {
		if _, valid := value.(map[string]any); !valid {
			return nil, failf("provider MCP configuration must be an object")
		}
	}
	return config, nil
}

func encodeProfileConfig(provider string, config map[string]any) ([]byte, error) {
	if provider == skillProviderCodex {
		return toml.Marshal(config)
	}
	raw, err := json.MarshalIndent(config, "", "  ")
	return append(raw, '\n'), err
}

// Restore only the original MCP section; settings edited while a
// profile is installed remain in place. If unrelated settings are unchanged,
// return the original bytes too, preserving original formatting and comments.
func restoreProfileConfig(provider string, current, original []byte) ([]byte, error) {
	now, err := decodeProfileConfig(provider, current)
	if err != nil {
		return nil, err
	}
	before, err := decodeProfileConfig(provider, original)
	if err != nil {
		return nil, fmt.Errorf("invalid profile configuration backup: %w", err)
	}
	key := profileMCPKey(provider)
	saved, existed := before[key]
	delete(before, key)
	delete(now, key)
	if reflect.DeepEqual(now, before) {
		return original, nil
	}
	if existed {
		now[key] = saved
	}
	if len(now) == 0 && original == nil {
		return nil, nil
	}
	return encodeProfileConfig(provider, now)
}
