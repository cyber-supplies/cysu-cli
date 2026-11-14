package main

// javascript_export.go downloads a domain's collection through the paged
// export, which replaces one large ZIP built inside a single request (and cut
// off by the gateway's timeout on big collections) with a list read a page
// at a time and files fetched straight from storage:
//
//	POST /javascript/export/files {domain}            -> {exportId, pages, expiresAt}
//	GET  /javascript/export/files/{exportId}?page=N   -> {page, pages, expiresAt, entries}
//	     then GET each entry's download link
//
// The service freezes the file list when the export is created, so a crawl
// running meanwhile cannot shift the pages. Each entry carries the fields of
// its manifest.jsonl line plus a short-lived download link and its matches,
// so the files and the manifest end up exactly where the ZIP put them: the
// entry and match lines are byte for byte the ZIP's. Only the header line is
// written here, and it leaves out the user ID the service's one carries.
//
// A service without the paged export answers "Not found", and the download
// falls back to the ZIP export.

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

// exportDownloadConcurrency is how many files are fetched from storage at once.
const exportDownloadConcurrency = 32

// exportCallTimeout bounds each call to the export API. A page reads its
// files' analyses and signs their links, which takes seconds, not minutes.
const exportCallTimeout = 2 * time.Minute

// pagedExport is the reply to creating an export.
type pagedExport struct {
	ExportID  string `json:"exportId"`
	Pages     int    `json:"pages"`
	ExpiresAt string `json:"expiresAt"`
}

// exportPage is one page of an export. Entries stay raw so their manifest
// lines can be written with the service's own field order and escaping.
type exportPage struct {
	Page    int               `json:"page"`
	Pages   int               `json:"pages"`
	Entries []json.RawMessage `json:"entries"`
}

// exportEntry is one file of a page: where it goes, how to fetch and check
// it, and its lines for manifest.jsonl.
type exportEntry struct {
	path         string
	digest       string
	download     string
	manifestLine []byte
	matchLines   [][]byte
}

// downloadJavascript fetches a domain's collection page by page and writes
// it under the user's home directory, with manifest.jsonl in the domain's
// directory, as the ZIP export did.
func downloadJavascript(domain string) error {
	base, root, err := collectionPaths(domain)
	if err != nil {
		return err
	}

	raw, err := sendWithTimeout("POST", apiPath("javascript", "export", "files"),
		map[string]any{"domain": domain}, exportCallTimeout)
	if err != nil {
		if isRouteNotFound(err) {
			return downloadJavascriptArchive(domain, base, root)
		}
		return err
	}
	var created pagedExport
	if json.Unmarshal(raw, &created) != nil || created.ExportID == "" || created.Pages < 1 {
		return failf("unexpected export response: %s", summarize(raw))
	}
	createdAt := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")

	var entryLines, matchLines [][]byte
	var files int
	var written int64
	for page := 0; page < created.Pages; page++ {
		entries, err := fetchExportPage(created.ExportID, page, created.Pages)
		if err != nil {
			return err
		}
		n, size, err := downloadExportEntries(created.ExportID, page, created.Pages, entries, base)
		if err != nil {
			return err
		}
		files += n
		written += size
		for _, entry := range entries {
			entryLines = append(entryLines, entry.manifestLine)
			matchLines = append(matchLines, entry.matchLines...)
		}
	}

	manifest, err := exportManifest(domain, createdAt, entryLines, matchLines)
	if err != nil {
		return err
	}
	size, err := writeAtomically(filepath.Join(root, manifestName), bytes.NewReader(manifest))
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
	}{true, domain, root, filepath.Join(root, manifestName), files + 1, written + size})
}

// isRouteNotFound reports whether the backend answered that the route does
// not exist, which is how a service from before the paged export replies.
// "No matching files" is a different message and is not a fallback case.
func isRouteNotFound(err error) bool {
	return errorMessage(asCLIError(err).raw) == "Not found"
}

// fetchExportPage reads one page of an export and splits its entries.
func fetchExportPage(exportID string, page, pages int) ([]exportEntry, error) {
	path := apiPath("javascript", "export", "files", exportID) + "?page=" + strconv.Itoa(page)
	raw, err := sendWithTimeout("GET", path, nil, exportCallTimeout)
	if err != nil {
		return nil, err
	}
	var body exportPage
	if json.Unmarshal(raw, &body) != nil {
		return nil, failf("unexpected export page: %s", summarize(raw))
	}
	if body.Page != page || body.Pages != pages {
		return nil, failf("export page %d came back as page %d of %d, expected %d pages", page, body.Page, body.Pages, pages)
	}

	entries := make([]exportEntry, 0, len(body.Entries))
	for _, raw := range body.Entries {
		entry, err := parseExportEntry(raw)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

// parseExportEntry splits one entry into its manifest line (every field but
// download and matches, in the order the service sent them) and one match
// line per match, which the manifest prefixes with the type and entry path.
func parseExportEntry(raw json.RawMessage) (exportEntry, error) {
	var entry exportEntry
	var line bytes.Buffer
	var pathRaw, matchesRaw json.RawMessage

	dec := json.NewDecoder(bytes.NewReader(raw))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return entry, failf("unexpected export entry: %s", summarize(raw))
	}
	line.WriteByte('{')
	for dec.More() {
		tok, err := dec.Token()
		key, isKey := tok.(string)
		if err != nil || !isKey {
			return entry, failf("unexpected export entry: %s", summarize(raw))
		}
		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			return entry, failf("unexpected export entry: %s", summarize(raw))
		}

		switch key {
		case "download":
			json.Unmarshal(value, &entry.download)
			continue
		case "matches":
			matchesRaw = value
			continue
		case "path":
			pathRaw = value
			json.Unmarshal(value, &entry.path)
		case "digest":
			json.Unmarshal(value, &entry.digest)
		}
		if line.Len() > 1 {
			line.WriteByte(',')
		}
		name, _ := json.Marshal(key)
		line.Write(name)
		line.WriteByte(':')
		line.Write(value)
	}
	line.WriteByte('}')

	if entry.path == "" || !isSHA256(entry.digest) || entry.download == "" {
		return entry, failf("export entry lacks a path, digest or download link: %s", summarize(raw))
	}
	entry.manifestLine = line.Bytes()

	var matches []json.RawMessage
	if len(matchesRaw) > 0 && json.Unmarshal(matchesRaw, &matches) != nil {
		return entry, failf("export entry %q has malformed matches", entry.path)
	}
	for _, match := range matches {
		if len(match) < 2 || match[0] != '{' {
			return entry, failf("export entry %q has malformed matches", entry.path)
		}
		var m bytes.Buffer
		m.WriteString(`{"type":"match","path":`)
		m.Write(pathRaw)
		if rest := bytes.TrimSpace(match[1:]); len(rest) > 1 {
			m.WriteByte(',')
		}
		m.Write(match[1:])
		entry.matchLines = append(entry.matchLines, m.Bytes())
	}
	return entry, nil
}

// isSHA256 reports whether s is a lowercase hex SHA-256 digest.
func isSHA256(s string) bool {
	if len(s) != sha256.Size*2 {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// downloadExportEntries fetches a page's files, several at once, and writes
// each to its path under base. Entries with the same contents are fetched
// once. Links last minutes, so a page whose downloads failed is read again
// for fresh links and those files are tried once more. It returns how many
// files were written and their total size.
func downloadExportEntries(exportID string, page, pages int, entries []exportEntry, base string) (int, int64, error) {
	// Every target is checked before anything is fetched: the names come
	// from a remote site's URLs.
	targets := map[string][]string{}
	var digests []string
	for _, entry := range entries {
		target, err := entryTarget(base, entry.path)
		if err != nil {
			return 0, 0, err
		}
		if _, seen := targets[entry.digest]; !seen {
			digests = append(digests, entry.digest)
		}
		targets[entry.digest] = append(targets[entry.digest], target)
	}

	links := exportLinks(entries)
	var files int
	var written int64
	failed, err := fetchExportObjects(digests, links, targets, &files, &written)
	if err != nil {
		return 0, 0, err
	}
	if len(failed) > 0 {
		fresh, err := fetchExportPage(exportID, page, pages)
		if err != nil {
			return 0, 0, err
		}
		retry := make([]string, 0, len(failed))
		for digest := range failed {
			retry = append(retry, digest)
		}
		failed, err = fetchExportObjects(retry, exportLinks(fresh), targets, &files, &written)
		if err != nil {
			return 0, 0, err
		}
		for _, cause := range failed {
			return 0, 0, cause
		}
	}
	return files, written, nil
}

// exportLinks maps each digest on a page to its download link.
func exportLinks(entries []exportEntry) map[string]string {
	links := make(map[string]string, len(entries))
	for _, entry := range entries {
		links[entry.digest] = entry.download
	}
	return links
}

// fetchExportObjects downloads each digest and writes it to all of its
// targets. A failed download is returned per digest for the caller to retry;
// a failed write stops everything, since retrying would not help.
func fetchExportObjects(digests []string, links map[string]string, targets map[string][]string,
	files *int, written *int64) (map[string]error, error) {
	var mu sync.Mutex
	failed := map[string]error{}
	var writeErr error
	work := make(chan string)
	var wg sync.WaitGroup

	for range min(exportDownloadConcurrency, len(digests)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for digest := range work {
				link, ok := links[digest]
				if !ok {
					mu.Lock()
					failed[digest] = failf("export page no longer lists %s", digest)
					mu.Unlock()
					continue
				}
				contents, err := fetchExportObject(link, digest)
				if err != nil {
					mu.Lock()
					failed[digest] = err
					mu.Unlock()
					continue
				}
				for _, target := range targets[digest] {
					n, err := writeAtomically(target, bytes.NewReader(contents))
					mu.Lock()
					if err != nil {
						if writeErr == nil {
							writeErr = err
						}
					} else {
						*files++
						*written += n
					}
					mu.Unlock()
				}
			}
		}()
	}
	for _, digest := range digests {
		mu.Lock()
		stop := writeErr != nil
		mu.Unlock()
		if stop {
			break
		}
		work <- digest
	}
	close(work)
	wg.Wait()
	return failed, writeErr
}

// fetchExportObject downloads one file and checks it against its digest, the
// SHA-256 of its plain contents. Storage may hand back the gzip it keeps the
// file in rather than inflating it, so gzip that fails the check is inflated
// and checked again. A relative link (a local service) is resolved against
// the API's base URL.
func fetchExportObject(link, digest string) ([]byte, error) {
	resolved, err := resolveDownloadLink(link)
	if err != nil {
		return nil, err
	}
	resp, err := transfer("GET", resolved, nil, 0, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	contents, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, failf("downloading %s: %v", digest, err)
	}

	if sha256Hex(contents) == digest {
		return contents, nil
	}
	if bytes.HasPrefix(contents, []byte{0x1f, 0x8b}) {
		if reader, err := gzip.NewReader(bytes.NewReader(contents)); err == nil {
			inflated, err := io.ReadAll(reader)
			if err == nil && sha256Hex(inflated) == digest {
				return inflated, nil
			}
		}
	}
	return nil, failf("download of %s does not match its digest", digest)
}

// resolveDownloadLink returns link as an absolute URL.
func resolveDownloadLink(link string) (string, error) {
	ref, err := url.Parse(link)
	if err != nil {
		return "", failf("invalid download link for an export entry")
	}
	if ref.IsAbs() {
		return link, nil
	}
	base, err := url.Parse(resolveBaseURL())
	if err != nil {
		return "", failf("invalid API base URL: %v", err)
	}
	return base.ResolveReference(ref).String(), nil
}

// sha256Hex is the lowercase hex SHA-256 of data.
func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// exportManifest assembles manifest.jsonl: a header line, every entry line,
// then every match line, the order the ZIP export writes them in. The
// header's counts cover the whole export, so it is written last of all.
func exportManifest(domain, createdAt string, entryLines, matchLines [][]byte) ([]byte, error) {
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false) // as the service writes its lines
	err := enc.Encode(struct {
		Type              string `json:"type"`
		Domain            string `json:"domain"`
		IncludeHistorical bool   `json:"includeHistorical"`
		CreatedAt         string `json:"createdAt"`
		Files             int    `json:"files"`
		Matches           int    `json:"matches"`
	}{"export", domain, false, createdAt, len(entryLines), len(matchLines)})
	if err != nil {
		return nil, fmt.Errorf("encoding manifest header: %w", err)
	}
	for _, line := range entryLines {
		out.Write(line)
		out.WriteByte('\n')
	}
	for _, line := range matchLines {
		out.Write(line)
		out.WriteByte('\n')
	}
	return out.Bytes(), nil
}
