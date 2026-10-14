package main

// endpoints.go lists observed endpoints owned by the authenticated user.

import "net/url"

var endpointsResource = resource{
	name:  "endpoints",
	title: "Endpoints",
	api:   true,
	commands: []command{
		{name: "list-scope", run: listEndpointScope},
		{
			name:  "add-scope",
			flags: []flagSpec{{name: "scope", placeholder: "regex", usage: "case-insensitive domain regex to add", required: true}},
			run:   addEndpointScope,
		},
		{
			name:  "set-scope",
			flags: []flagSpec{{name: "scope", placeholder: "regex", usage: "replace all domain scope with this regex", required: true}},
			run:   setEndpointScope,
		},
		{
			name: "list",
			flags: []flagSpec{
				{name: "domain", placeholder: "regex", usage: "host regex (case-insensitive); use ^ and $ for an exact match"},
				{name: "path", placeholder: "regex", usage: "stored templated path regex (case-sensitive); escape literal * as \\*"},
				{name: "method", placeholder: "method", usage: "HTTP method (case-insensitive)"},
				{name: "full", boolean: true, usage: "include captured request and response; requires --domain or --path"},
			},
			validate: func(v flagValues) error {
				if v.enabled("full") && v["domain"] == "" && v["path"] == "" {
					return cliError{message: "endpoints list: --full requires --domain or --path", exit: exitUsage}
				}
				return nil
			},
			run: listEndpoints,
		},
	},
}

// listEndpointScope prints the authenticated user's domain scope patterns.
func listEndpointScope(_ flagValues) error {
	return request("GET", apiPath("scope"), nil)
}

// addEndpointScope appends a pattern without replacing existing scope.
func addEndpointScope(v flagValues) error {
	return request("PUT", apiPath("scope"), map[string]string{"scope": v["scope"]})
}

// setEndpointScope replaces all scope patterns with the supplied pattern.
func setEndpointScope(v flagValues) error {
	return request("POST", apiPath("scope"), map[string]string{"scope": v["scope"]})
}

// listEndpoints sends optional filters as query parameters and prints the API JSON.
func listEndpoints(v flagValues) error {
	query := url.Values{}
	for _, name := range []string{"domain", "path", "method"} {
		if value := v[name]; value != "" {
			query.Set(name, value)
		}
	}
	if v.enabled("full") {
		query.Set("full", "true")
	}
	path := apiPath("endpoints")
	if len(query) > 0 {
		path += "?" + query.Encode()
	}
	return request("GET", path, nil)
}
