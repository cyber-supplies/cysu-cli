package main

// mcp.go manages stdio and HTTP MCP server entries through the gateway.

import (
	"net/url"
	"strings"
)

var mcpIDFlag = flagSpec{name: "mcp", placeholder: "id", usage: "MCP server id", required: true}
var mcpTypeFlag = flagSpec{name: "type", placeholder: "type", usage: "server type: stdio or http", required: true}
var mcpURLFlag = flagSpec{name: "url", placeholder: "url", usage: "server URL", required: true}
var mcpCommandFlag = flagSpec{name: "command", placeholder: "command", usage: "stdio command"}
var mcpArgFlag = flagSpec{name: "arg", placeholder: "arg", usage: "stdio argument", multi: true}
var mcpHeaderFlag = flagSpec{name: "header", placeholder: "name:value", usage: "HTTP header", multi: true}
var mcpProfileFlag = flagSpec{name: "profile", placeholder: "profile", usage: "profile name (default: default)"}

var mcpResource = resource{
	name: "mcp", title: "MCP", api: true,
	commands: []command{
		{name: "list", flags: []flagSpec{mcpProfileFlag}, run: func(v flagValues) error {
			path := apiPath("mcp")
			if profile := v["profile"]; profile != "" {
				path += "?profile=" + url.QueryEscape(profile)
			}
			return request("GET", path, nil)
		}},
		{name: "create", flags: []flagSpec{mcpTypeFlag, mcpURLFlag, mcpCommandFlag, mcpArgFlag, mcpHeaderFlag, mcpProfileFlag}, run: func(v flagValues) error {
			entry, err := mcpEntry(v)
			if err != nil {
				return err
			}
			return request("POST", apiPath("mcp"), entry)
		}},
		{name: "modify", flags: []flagSpec{mcpIDFlag, mcpTypeFlag, mcpURLFlag, mcpCommandFlag, mcpArgFlag, mcpHeaderFlag, mcpProfileFlag}, run: func(v flagValues) error {
			entry, err := mcpEntry(v)
			if err != nil {
				return err
			}
			return request("PUT", apiPath("mcp", v["mcp"]), entry)
		}},
		{name: "remove", flags: []flagSpec{mcpIDFlag}, run: func(v flagValues) error {
			return request("DELETE", apiPath("mcp", v["mcp"]), nil)
		}},
	},
}

// mcpEntry builds the complete body required by both create and replace.
func mcpEntry(v flagValues) (map[string]any, error) {
	typeName := strings.TrimSpace(v["type"])
	profile := strings.TrimSpace(v["profile"])
	if profile == "" {
		profile = "default"
	}
	entry := map[string]any{"type": typeName, "url": strings.TrimSpace(v["url"]), "profile": profile}
	switch typeName {
	case "stdio":
		if strings.TrimSpace(v["command"]) == "" {
			return nil, failf("stdio MCP server requires --command")
		}
		if len(v.list("header")) != 0 {
			return nil, failf("--header is only valid for HTTP MCP servers")
		}
		args := v.list("arg")
		if args == nil {
			args = []string{}
		}
		entry["command"] = strings.TrimSpace(v["command"])
		entry["args"] = args
	case "http":
		if v["command"] != "" || len(v.list("arg")) != 0 {
			return nil, failf("--command and --arg are only valid for stdio MCP servers")
		}
		parsed, err := url.Parse(v["url"])
		if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			return nil, failf("HTTP MCP server requires an http or https --url")
		}
		headers := map[string]string{}
		for _, item := range v.list("header") {
			name, value, ok := strings.Cut(item, ":")
			name = strings.TrimSpace(name)
			if !ok || name == "" {
				return nil, failf("--header must be name:value")
			}
			if _, exists := headers[name]; exists {
				return nil, failf("duplicate HTTP header %q", name)
			}
			headers[name] = strings.TrimSpace(value)
		}
		entry["headers"] = headers
	default:
		return nil, failf("--type must be stdio or http")
	}
	return entry, nil
}
