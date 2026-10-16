package main

// notes.go manages short notes the agent keeps on a domain and optional path.
// The notes API stores content encrypted at rest on the server side, and
// limits content to 500 characters, so the CLI sends it as plain text.

import (
	"encoding/json"
	"net/url"
	"strings"
)

var noteIDFlag = flagSpec{
	name: "note", placeholder: "note", usage: "note TypeID", required: true,
}

var notesResource = resource{
	name: "notes", title: "Notes", api: true,
	commands: []command{
		{
			name: "list",
			flags: []flagSpec{
				{name: "domain", placeholder: "regex", usage: "domain regex (case-insensitive); use ^ and $ for an exact match"},
				{name: "path", placeholder: "regex", usage: "path regex (case-sensitive); ^$ matches notes with no path"},
			},
			run: func(v flagValues) error { return listNotes(v["domain"], v["path"]) },
		},
		{
			name: "create",
			flags: []flagSpec{
				{name: "domain", placeholder: "domain", usage: "domain the note applies to", required: true},
				{name: "path", placeholder: "path", usage: "path on the domain; omit for the whole domain"},
				{name: "content", placeholder: "content", usage: "note text, at most 500 characters", required: true},
			},
			run: func(v flagValues) error {
				return createNote(v["domain"], v["path"], v["content"])
			},
		},
		{
			name: "modify",
			flags: []flagSpec{noteIDFlag,
				{name: "domain", placeholder: "domain", usage: "replacement domain"},
				{name: "path", placeholder: "path", usage: "replacement path"},
				{name: "content", placeholder: "content", usage: "replacement note text, at most 500 characters"},
			},
			validate: func(v flagValues) error {
				if strings.TrimSpace(v["domain"]) == "" && strings.TrimSpace(v["path"]) == "" && strings.TrimSpace(v["content"]) == "" {
					return cliError{message: "notes modify: provide --domain, --path, or --content", exit: exitUsage}
				}
				return nil
			},
			run: func(v flagValues) error {
				return modifyNote(v["note"], v["domain"], v["path"], v["content"])
			},
		},
		{
			name:  "delete",
			flags: []flagSpec{noteIDFlag},
			run:   func(v flagValues) error { return deleteNote(v["note"]) },
		},
	},
}

// note is one note as the API returns it.
type note struct {
	ID        string `json:"id"`
	Domain    string `json:"domain"`
	Path      string `json:"path"`
	CreatedAt string `json:"createdAt"`
	Content   string `json:"content"`
}

// listNotes prints the caller's notes, optionally filtered by domain and path
// regexes. With neither filter every note is listed.
func listNotes(domain, path string) error {
	query := url.Values{}
	if domain != "" {
		query.Set("domain", domain)
	}
	if path != "" {
		query.Set("path", path)
	}
	endpoint := apiPath("notes")
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}
	return request("GET", endpoint, nil)
}

// createNote adds a note and prints it. An omitted path is left out of the
// request, so the API stores the note for the whole domain.
func createNote(domain, path, content string) error {
	body := map[string]string{"domain": domain, "content": content}
	if path != "" {
		body["path"] = path
	}
	return request("POST", apiPath("notes"), body)
}

// modifyNote changes only the fields given. The API's PUT replaces the whole
// note, so the current note is fetched first and every field not given keeps
// its current value.
func modifyNote(id, domain, path, content string) error {
	current, err := fetchNote(id)
	if err != nil {
		return err
	}
	if domain != "" {
		current.Domain = domain
	}
	if path != "" {
		current.Path = path
	}
	if content != "" {
		current.Content = content
	}
	return request("PUT", apiPath("notes"), map[string]string{
		"id": current.ID, "domain": current.Domain, "path": current.Path, "content": current.Content,
	})
}

// deleteNote removes a note and prints the deleted note's id.
func deleteNote(id string) error {
	return request("DELETE", apiPath("notes", id), nil)
}

// fetchNote finds one of the caller's notes by id. The API has no
// single-note route, so this lists every note and picks out the match.
func fetchNote(id string) (note, error) {
	raw, err := send("GET", apiPath("notes"), nil)
	if err != nil {
		return note{}, err
	}
	var notes []note
	if err := json.Unmarshal(raw, &notes); err != nil {
		return note{}, failf("notes: unexpected response from the API")
	}
	for _, n := range notes {
		if n.ID == id {
			return n, nil
		}
	}
	return note{}, failf("Note not found")
}
