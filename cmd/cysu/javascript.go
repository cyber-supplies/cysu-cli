package main

// javascript.go is the `cysu javascript` command: the JavaScript a site ships,
// collected and analysed by the JavaScript microservice.
//
// Two steps, some time apart. Submitting a URL only queues it; the service
// fetches the page, follows every script reference and chunk it can find,
// beautifies and analyses each file and recovers original sources from any
// source map. Downloading then takes everything collected for one domain:
//
//	add       POST /javascript/add    {url}      -> queued, nothing returned
//	download  POST /javascript/export {domain}   -> {url, expiresAt}
//	          then GET url, a ZIP of the collection
//
// Unlike the rest of the CLI these routes are not project-scoped. The service
// keeps one collection per user per domain and the backend identifies the user
// from the token, so neither command takes --project.
//
// The archive is extracted under $HOME/cysu-js/<domain>/ exactly as it is laid
// out inside, because manifest.jsonl at the root names every file and every
// analyzer match by that same path. Keeping the two in step is the point of
// the manifest: a match line is enough to open the right file at the right
// line. Entry names come from a remote site's URLs, so each one is checked to
// be sure it stays inside the collection directory.

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"strings"
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
			name: "download",
			flags: []flagSpec{
				{name: "domain", placeholder: "domain", usage: "host the files were collected from", required: true},
				{name: "output", placeholder: "dir", usage: "directory to extract into (default: $HOME/" + collectionDir + ")"},
			},
			run: func(v flagValues) error {
				return downloadJavascript(v["domain"], v["output"])
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
		if _, err := send("POST", apiPath("javascript", "add"), map[string]any{"url": raw}); err != nil {
			return err
		}
	}

	return printJSON(struct {
		Success bool     `json:"success"`
		URLs    []string `json:"urls"`
	}{true, urls})
}

// downloadJavascript asks the backend for a link to the domain's collection,
// fetches the archive and unpacks it into output (the user's home by default).
// The archive is held in memory: the service caps an export at 100 MB.
func downloadJavascript(domain, output string) error {
	root, err := collectionRoot(output, domain)
	if err != nil {
		return err
	}

	raw, err := send("POST", apiPath("javascript", "export"), map[string]any{"domain": domain})
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

	files, written, err := extractArchive(archive, root)
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

// collectionRoot is where a domain's files belong: <output>/<domain>, with
// output defaulting to cysu-js in the user's home directory.
func collectionRoot(output, domain string) (string, error) {
	name := directoryForDomain(domain)
	if name == "" {
		return "", failf("--domain must be a host name, e.g. example.com")
	}

	if strings.TrimSpace(output) == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", failf("finding the home directory: %v", err)
		}
		output = filepath.Join(home, collectionDir)
	}
	return filepath.Join(output, name), nil
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

// extractArchive writes every entry of a ZIP under root, returning how many
// files were written and their total size. Directories are created as needed;
// the archive's own directory entries carry nothing and are skipped.
func extractArchive(archive []byte, root string) (int, int64, error) {
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

		target, err := entryTarget(root, entry.Name)
		if err != nil {
			return 0, 0, err
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

// entryTarget resolves one entry name to the file it should be written to,
// refusing anything that would land outside root. The names come from the
// paths of a remote site's scripts, so they are treated as untrusted: a name
// that climbs out with "..", starts at the filesystem root, or carries a
// Windows separator or drive letter is rejected rather than repaired.
func entryTarget(root, name string) (string, error) {
	if name == "" || strings.ContainsAny(name, `\`) || strings.HasPrefix(name, "/") || filepath.IsAbs(name) {
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
	if target != root && !strings.HasPrefix(target, root+string(os.PathSeparator)) {
		return "", failf("refusing archive entry %q: it escapes the collection directory", name)
	}
	return target, nil
}

// readEntry reads one archive entry, undoing a further layer of gzip if the
// contents carry one. The service stores objects gzipped and inflates them on
// the way into the archive, so this normally does nothing; it costs one
// comparison and means a file is never left on disk still compressed.
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
	if !bytes.HasPrefix(contents, []byte{0x1f, 0x8b}) {
		return contents, nil
	}

	gz, err := gzip.NewReader(bytes.NewReader(contents))
	if err != nil {
		return contents, nil // Not gzip after all; keep what was stored.
	}
	defer gz.Close()

	plain, err := io.ReadAll(gz)
	if err != nil {
		return nil, failf("decompressing archive entry %q: %v", entry.Name, err)
	}
	return plain, nil
}
