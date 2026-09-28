package main

// javascript.go is the `cysu javascript` command: the JavaScript a site ships,
// collected and analysed by the JavaScript microservice.
//
// Two steps, some time apart. Submitting a URL only queues it; the service
// fetches the page, follows every script reference and chunk it can find,
// beautifies and analyses each file and recovers original sources from any
// source map. Listing shows what has been collected so far, optionally for one
// domain, and downloading then takes one domain's collection:
//
//	add       POST /javascript        {url}      -> queued, nothing returned
//	list      POST /javascript/list   {domain} or {} -> [{path, updatedAt, status}]
//	download  POST /javascript/export {domain}   -> {url, expiresAt}
//	          then GET url, a ZIP of the collection
//
// Unlike the rest of the CLI these routes are not project-scoped. The service
// keeps one collection per user per domain and the backend identifies the user
// from the token, so no command takes --project.
//
// The archive is extracted under $HOME/cysu-js exactly as it is laid out
// inside, which puts a domain's files at $HOME/cysu-js/<domain>/<path>: the
// service names every entry <host>/<path> already. Names are kept rather than
// rewritten because manifest.jsonl indexes every file and every analyzer match
// by that same path, and keeping the two in step is the point of the manifest -
// a match line is enough to open the right file at the right line. The
// manifest itself travels at the archive root and is written into the domain's
// directory, one per collection. Entry names come from a remote site's URLs,
// so each one is checked to be sure it stays inside cysu-js.

import (
	"archive/zip"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// collectionDir is the directory under the user's home where downloaded
// collections are kept, one subdirectory per domain.
const collectionDir = "cysu-js"

// manifestName is the archive entry that indexes the collection.
const manifestName = "manifest.jsonl"

var javascriptResource = resource{
	name:  "javascript",
	title: "JavaScript",
	api:   true,
	commands: []command{
		{
			name: "add",
			flags: []flagSpec{
				{name: "url", placeholder: "url", usage: "page or script URL to collect from, repeatable", required: true, multi: true},
			},
			run: func(v flagValues) error {
				return addJavascriptURLs(v.list("url"))
			},
		},
		{
			name: "list",
			flags: []flagSpec{
				{name: "domain", placeholder: "domain", usage: "host the files were collected from (omit to list all domains)"},
			},
			run: func(v flagValues) error {
				return listJavascript(v["domain"])
			},
		},
		{
			name: "download",
			flags: []flagSpec{
				{name: "domain", placeholder: "domain", usage: "host the files were collected from", required: true},
			},
			run: func(v flagValues) error {
				return downloadJavascript(v["domain"])
			},
		},
	},
}

// ---------------------------------------------------------------------------
// Commands
// ---------------------------------------------------------------------------

// addJavascriptURLs queues each URL with the service. The route takes one URL
// per call, so they are submitted in turn and the first failure stops the run:
// a later URL is no use if an earlier one was rejected, and the backend's error
// says which. Nothing is returned per URL, so one summary is printed for all.
func addJavascriptURLs(urls []string) error {
	for _, raw := range urls {
		if strings.TrimSpace(raw) == "" {
			return failf("--url must not be empty")
		}
		if _, err := send("POST", apiPath("javascript"), map[string]any{"url": raw}); err != nil {
			return err
		}
	}

	return printJSON(struct {
		Success bool     `json:"success"`
		URLs    []string `json:"urls"`
	}{true, urls})
}

// listJavascript prints the files collected so far, each with
// its path, when it last changed and its status ("current" for a file still
// served, as opposed to a retired or historical one). The backend's array
// is passed through as it is. An omitted domain sends an empty JSON object.
func listJavascript(domain string) error {
	body := map[string]any{}
	if domain != "" {
		if strings.TrimSpace(domain) == "" {
			return failf("--domain must not be empty")
		}
		body["domain"] = domain
	}
	return request("POST", apiPath("javascript", "list"), body)
}

// downloadJavascript asks the backend for a link to the domain's collection,
// fetches the archive and unpacks it under the user's home directory. The
// archive is held in memory: the service caps an export at 100 MB.
func downloadJavascript(domain string) error {
	base, root, err := collectionPaths(domain)
	if err != nil {
		return err
	}

	// The service builds and uploads the archive before returning its link.
	raw, err := sendWithTimeout("POST", apiPath("javascript", "export"), map[string]any{"domain": domain}, 2*time.Minute)
	if err != nil {
		return err
	}
	ticket, err := decodeTicket(raw, "url")
	if err != nil {
		return err
	}

	resp, err := transfer("GET", ticket.URL, nil, 0, "")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	archive, err := io.ReadAll(resp.Body)
	if err != nil {
		return failf("downloading the %s collection: %v", domain, err)
	}

	files, written, err := extractArchive(archive, base, root)
	if err != nil {
		return err
	}

	return printJSON(struct {
		Success  bool   `json:"success"`
		Domain   string `json:"domain"`
		Output   string `json:"output"`
		Manifest string `json:"manifest"`
		Files    int    `json:"files"`
		Size     int64  `json:"size"`
	}{true, domain, root, filepath.Join(root, manifestName), files, written})
}

// ---------------------------------------------------------------------------
// Extraction
// ---------------------------------------------------------------------------

// collectionPaths returns the two directories an export is unpacked against:
// base, cysu-js in the user's home, which archive entry names are resolved
// from, and root, the domain's own directory beneath it.
//
// They differ because the service already names every entry <host>/<path>,
// so resolving against base is what puts a file at <base>/<domain>/<path>.
// Root is still needed on its own: manifest.jsonl sits at the archive root
// but belongs to the domain, and it is root that is reported to the caller.
//
// Where a collection lands is not configurable. Every path in the manifest
// is relative to base, so a collection moved elsewhere would carry an index
// that no longer resolves.
func collectionPaths(domain string) (string, string, error) {
	name := directoryForDomain(domain)
	if name == "" {
		return "", "", failf("--domain must be a host name, e.g. example.com")
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", "", failf("finding the home directory: %v", err)
	}

	base := filepath.Join(home, collectionDir)
	return base, filepath.Join(base, name), nil
}

// directoryForDomain renders a host as one path component. A non-default port
// arrives as "example.com:8443", and a colon cannot appear in a Windows path,
// so it becomes an underscore - the same substitution the service makes for
// the host directory inside the archive. An empty or path-like value is
// refused rather than cleaned, since it would not name a collection anyway.
func directoryForDomain(domain string) string {
	name := strings.TrimSpace(domain)
	if name == "" || strings.ContainsAny(name, `/\`) || name == "." || name == ".." {
		return ""
	}
	return strings.ReplaceAll(name, ":", "_")
}

// extractArchive writes every entry of a ZIP under base, returning how many
// files were written and their total size. Directories are created as needed;
// the archive's own directory entries carry nothing and are skipped.
//
// Entry names are kept as they are, since the service has already prefixed
// each one with the host it was collected from; base is therefore the parent
// of the domain directory, not the domain directory itself. The exception is
// manifest.jsonl, which the archive keeps at its root and which is written
// into root instead, one manifest per domain.
func extractArchive(archive []byte, base, root string) (int, int64, error) {
	reader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		return 0, 0, failf("the export is not a readable ZIP archive: %v", err)
	}

	var files int
	var written int64
	for _, entry := range reader.File {
		if entry.FileInfo().IsDir() {
			continue
		}

		target := manifestTarget(root, entry.Name)
		if target == "" {
			var err error
			if target, err = entryTarget(base, entry.Name); err != nil {
				return 0, 0, err
			}
		}

		contents, err := readEntry(entry)
		if err != nil {
			return 0, 0, err
		}

		n, err := writeAtomically(target, bytes.NewReader(contents))
		if err != nil {
			return 0, 0, err
		}
		files++
		written += n
	}

	return files, written, nil
}

// manifestTarget is the path manifest.jsonl is written to, or "" when the
// entry is an ordinary file. The manifest indexes one domain's collection but
// travels at the archive root, so it is the one entry moved on the way out:
// every other name already carries the host and is left alone.
func manifestTarget(root, name string) string {
	if name != manifestName {
		return ""
	}
	return filepath.Join(root, manifestName)
}

// entryTarget resolves one entry name to the file it should be written to,
// refusing anything that would land outside root. The names come from the
// paths of a remote site's scripts, so they are treated as untrusted: a name
// that climbs out with "..", starts at the filesystem root, or carries a
// Windows separator or drive letter is rejected rather than repaired.
func entryTarget(root, name string) (string, error) {
	if name == "" || strings.Contains(name, `\`) || strings.HasPrefix(name, "/") || filepath.IsAbs(name) {
		return "", failf("refusing archive entry %q: not a relative path", name)
	}
	// Cleaning "../x" would quietly turn an escape into a write somewhere else
	// inside the collection, which is safe but is not what the archive asked
	// for. A legitimate entry never needs to climb, so climbing is an error.
	for _, segment := range strings.Split(name, "/") {
		if segment == ".." {
			return "", failf("refusing archive entry %q: it escapes the collection directory", name)
		}
	}

	target := filepath.Join(root, filepath.FromSlash(name))
	if !strings.HasPrefix(target, root+string(os.PathSeparator)) {
		return "", failf("refusing archive entry %q: it escapes the collection directory", name)
	}
	return target, nil
}

// readEntry reads one archive entry. Contents arrive as they will be written:
// the service inflates every object on its way into the archive, so nothing
// here is still compressed beyond the ZIP's own encoding.
func readEntry(entry *zip.File) ([]byte, error) {
	rc, err := entry.Open()
	if err != nil {
		return nil, failf("reading archive entry %q: %v", entry.Name, err)
	}
	defer rc.Close()

	contents, err := io.ReadAll(rc)
	if err != nil {
		return nil, failf("reading archive entry %q: %v", entry.Name, err)
	}
	return contents, nil
}
