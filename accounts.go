package main

var accountFlag = flagSpec{name: "account", placeholder: "id", usage: "account id", required: true}

var accountsResource = resource{
	name:  "accounts",
	title: "Accounts",
	api:   true,
	commands: []command{
		{
			name:  "list",
			flags: []flagSpec{projectFlag},
			run: func(v flagValues) error {
				return request("GET", apiPath("projects", v["project"], "accounts"), nil)
			},
		},
		{
			name:  "get",
			flags: []flagSpec{projectFlag, accountFlag},
			run: func(v flagValues) error {
				return request("GET", apiPath("projects", v["project"], "accounts", v["account"]), nil)
			},
		},
		{
			name: "create",
			flags: []flagSpec{
				projectFlag,
				{name: "username", placeholder: "user", usage: "account username", required: true},
				{name: "password", placeholder: "pass", usage: "account password", required: true},
				{name: "location", placeholder: "url", usage: "login location url"},
			},
			run: func(v flagValues) error {
				body := map[string]any{"username": v["username"], "password": v["password"]}
				if location := v["location"]; location != "" {
					body["location"] = location
				}
				return request("POST", apiPath("projects", v["project"], "accounts"), body)
			},
		},
		{
			name:  "delete",
			flags: []flagSpec{projectFlag, accountFlag},
			run: func(v flagValues) error {
				return request("DELETE", apiPath("projects", v["project"], "accounts", v["account"]), nil)
			},
		},
		{
			name:  "get-auth",
			flags: []flagSpec{projectFlag, accountFlag},
			run: func(v flagValues) error {
				return request("GET", apiPath("projects", v["project"], "accounts", v["account"], "auth"), nil)
			},
		},
	},
}
