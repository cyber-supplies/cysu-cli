package main

var endpointsResource = resource{
	name:  "endpoints",
	title: "Endpoints",
	api:   true,
	commands: []command{
		{
			name:  "list",
			flags: []flagSpec{projectFlag},
			run: func(v flagValues) error {
				return request("GET", apiPath("projects", v["project"], "endpoints"), nil)
			},
		},
		{
			name: "search",
			flags: []flagSpec{
				projectFlag,
				{name: "query", placeholder: "query", usage: "regex to match endpoints against", required: true},
			},
			run: func(v flagValues) error {
				// The backend exposes search as a QUERY-method route.
				return request("QUERY", apiPath("projects", v["project"], "endpoints", "search"),
					map[string]any{"query": v["query"]})
			},
		},
	},
}
