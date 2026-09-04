package main

// projectFlag identifies the project a command acts on. Every resource that
// lives under a project (findings, accounts, endpoints, storage) reuses it.
// When omitted it falls back to the project saved by `cysu projects select`
// (see config.go).
var projectFlag = flagSpec{
	name:        "project",
	placeholder: "id",
	usage:       "project id (default: the selected project)",
	fallback:    defaultProject,
}

// explicitProjectFlag is projectFlag without the fallback, for commands where
// acting on an implicit project would be surprising or destructive.
var explicitProjectFlag = flagSpec{name: "project", placeholder: "id", usage: "project id", required: true}

var projectsResource = resource{
	name:  "projects",
	title: "Projects",
	api:   true,
	commands: []command{
		{
			name: "list",
			run: func(flagValues) error {
				return request("GET", apiPath("projects"), nil)
			},
		},
		{
			name:  "get",
			flags: []flagSpec{projectFlag},
			run: func(v flagValues) error {
				return request("GET", apiPath("projects", v["project"]), nil)
			},
		},
		{
			name: "create",
			flags: []flagSpec{
				{name: "name", placeholder: "name", usage: "project name", required: true},
				{name: "scope", placeholder: "glob", usage: "in-scope host pattern, e.g. '*google*'", required: true, multi: true},
			},
			run: func(v flagValues) error {
				// scope is sent as a bare array of glob patterns: the backend
				// takes it that way, the spec's {"id": [...]} wrapper is a
				// mistake there.
				return request("POST", apiPath("projects"), map[string]any{
					"name":  v["name"],
					"scope": v.list("scope"),
				})
			},
		},
		{
			name:  "delete",
			flags: []flagSpec{explicitProjectFlag},
			run: func(v flagValues) error {
				return request("DELETE", apiPath("projects", v["project"]), nil)
			},
		},
		{
			name:  "select",
			flags: []flagSpec{explicitProjectFlag},
			run: func(v flagValues) error {
				return runSelectProject(v["project"])
			},
		},
		{
			name:  "current",
			local: true,
			run: func(flagValues) error {
				return runCurrentProject()
			},
		},
	},
}

// runSelectProject checks that id names a project the token can see, then
// saves it as the selection. The check keeps a typo from breaking every
// later command with a confusing "not found".
func runSelectProject(id string) error {
	if _, err := send("GET", apiPath("projects", id), nil); err != nil {
		return err
	}

	path, err := selectProject(id)

	if err != nil {
		return err
	}

	return printJSON(struct {
		Success bool   `json:"success"`
		Project string `json:"project"`
		Config  string `json:"config"`
	}{true, id, path})
}

// runCurrentProject prints the selected project. Nothing selected is not an
// error: the project is null.
func runCurrentProject() error {
	id, err := currentProject()
	if err != nil {
		return err
	}
	var project *string
	if id != "" {
		project = &id
	}
	return printJSON(struct {
		Success bool    `json:"success"`
		Project *string `json:"project"`
	}{true, project})
}
