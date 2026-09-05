package main

// storage.go is the `cysu storage` command: files kept on the backend so they
// are reachable from any machine or session.
//
// The backend never carries file contents itself. It hands out short-lived
// presigned URLs (see the Storage routes in openapi.yaml):
//
//	list      GET  /projects/{id}/storage/files          -> [{id, path}, ...]
//	upload    POST /projects/{id}/storage/files {path}   -> {id, path, uploadUrl}
//	          then PUT the contents to uploadUrl
//	download  GET  /projects/{id}/storage/files/{fileId} -> {id, path, downloadUrl}
//	          then GET downloadUrl
//	read      as download, but the contents are printed (base64) rather
//	          than written to a local file
//
// On the command line files are addressed by their storage path, which is
// what the caller knows; download and read resolve it to a file id via the
// listing. The presigned URLs are used once and never printed.
//
// Contents are compressed with zstd and then encrypted with the master key
// before upload (see encryption.go), and decrypted and decompressed after
// download; the storage bucket only ever holds ciphertext. Paths stay in the
// clear so files can be listed and found. A file is processed in one piece,
// so it is held in memory during transfer.

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/klauspost/compress/zstd"
)

var storageResource = resource{
	name:  "storage",
	title: "Storage",
	api:   true,
	commands: []command{
		{
			name: "list",
			flags: []flagSpec{
				projectFlag,
				{name: "dir", placeholder: "dir", usage: "only list files under this directory"},
			},
			run: func(v flagValues) error {
				return listFiles(v["project"], v["dir"])
			},
		},
		{
			name: "upload",
			flags: []flagSpec{
				projectFlag,
				{name: "file", placeholder: "local", usage: "local file to upload", required: true},
				{name: "path", placeholder: "path", usage: "destination path in storage", required: true},
			},
			run: func(v flagValues) error {
				return uploadFile(v["project"], v["file"], v["path"])
			},
		},
		{
			name: "download",
			flags: []flagSpec{
				projectFlag,
				{name: "path", placeholder: "path", usage: "path of the file in storage", required: true},
				{name: "output", placeholder: "local", usage: "local file to write", required: true},
			},
			run: func(v flagValues) error {
				return downloadFile(v["project"], v["path"], v["output"])
			},
		},
		{
			name: "read",
			flags: []flagSpec{
				projectFlag,
				{name: "path", placeholder: "path", usage: "path of the file in storage", required: true},
			},
			run: func(v flagValues) error {
				return readFile(v["project"], v["path"])
			},
		},
	},
}

// ---------------------------------------------------------------------------
// Commands
// ---------------------------------------------------------------------------

// listFiles prints the project's file listing. Without dir the backend
// response is printed as is. With dir the listing is filtered here (the
// backend has no directory concept) and printed as {success, dir, files}.
func listFiles(project, dir string) error {
	raw, err := send("GET", apiPath("projects", project, "storage", "files"), nil)
	if err != nil {
		return err
	}
	if strings.TrimSpace(dir) == "" {
		printResponse(raw)
		return nil
	}

	files, err := decodeFileList(raw)
	if err != nil {
		return err
	}
	matches := []json.RawMessage{} // never null in the output
	for _, f := range files {
		if underDir(f.Path, dir) {
			matches = append(matches, f.raw)
		}
	}
	return printJSON(struct {
		Success bool              `json:"success"`
		Dir     string            `json:"dir"`
		Files   []json.RawMessage `json:"files"`
	}{true, dir, matches})
}

// uploadFile compresses and encrypts the contents of local, registers
// remote with the backend and PUTs the ciphertext to the presigned URL it
// returns. The reported size is that of the original file.
func uploadFile(project, local, remote string) error {
	info, err := os.Stat(local)
	if err != nil {
		return failf("%v", err)
	}
	if !info.Mode().IsRegular() {
		return failf("%s is not a regular file", local)
	}
	plaintext, err := os.ReadFile(local)
	if err != nil {
		return failf("%v", err)
	}
	compressed, err := compress(plaintext)
	if err != nil {
		return fmt.Errorf("compressing %s: %w", local, err)
	}
	ciphertext, err := encrypt(compressed)
	if err != nil {
		return err
	}

	raw, err := send("POST", apiPath("projects", project, "storage", "files"),
		map[string]any{"path": remote})
	if err != nil {
		return err
	}
	ticket, err := decodeTicket(raw, "uploadUrl")
	if err != nil {
		return err
	}

	// application/octet-stream is what the backend signs the URL for; a
	// presigned PUT must repeat the content type exactly.
	resp, err := transfer("PUT", ticket.URL, bytes.NewReader(ciphertext), int64(len(ciphertext)),
		"application/octet-stream")
	if err != nil {
		return err
	}
	resp.Body.Close()

	return printJSON(struct {
		Success bool   `json:"success"`
		ID      string `json:"id"`
		Path    string `json:"path"`
		Size    int    `json:"size"`
	}{true, ticket.ID, ticket.Path, len(plaintext)})
}

// A fetchedFile is the outcome of fetchFile: the listing entry the path
// resolved to, the file's plaintext contents and whether they were stored
// encrypted.
type fetchedFile struct {
	storedFile
	contents  []byte
	encrypted bool
}

// fetchFile finds the stored file at remote, fetches its presigned URL,
// downloads the contents into memory and decrypts and decompresses them. A
// file that was stored unencrypted (by other means) is returned as it is,
// and reported as such. Shared by download and read, which differ only in
// where the contents end up.
func fetchFile(project, remote string) (fetchedFile, error) {
	raw, err := send("GET", apiPath("projects", project, "storage", "files"), nil)
	if err != nil {
		return fetchedFile{}, err
	}
	files, err := decodeFileList(raw)
	if err != nil {
		return fetchedFile{}, err
	}
	var file *storedFile
	for i := range files {
		if samePath(files[i].Path, remote) {
			file = &files[i]
			break
		}
	}
	if file == nil {
		return fetchedFile{}, failf("no stored file at path %q; check `cysu storage list`", remote)
	}

	raw, err = send("GET", apiPath("projects", project, "storage", "files", file.ID), nil)
	if err != nil {
		return fetchedFile{}, err
	}
	ticket, err := decodeTicket(raw, "downloadUrl")
	if err != nil {
		return fetchedFile{}, err
	}

	resp, err := transfer("GET", ticket.URL, nil, 0, "")
	if err != nil {
		return fetchedFile{}, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return fetchedFile{}, failf("downloading %s: %v", remote, err)
	}
	plaintext, encrypted, err := decryptBytesIfEncrypted(data)
	if err != nil {
		return fetchedFile{}, fmt.Errorf("stored file %s: %w", remote, err)
	}
	if encrypted {
		if plaintext, err = decompress(plaintext); err != nil {
			return fetchedFile{}, fmt.Errorf("stored file %s: decompressing: %w", remote, err)
		}
	}
	return fetchedFile{storedFile: *file, contents: plaintext, encrypted: encrypted}, nil
}

// downloadFile fetches the stored file at remote and writes its contents to
// output. The file is written next to its final location and renamed into
// place, so a failed transfer never leaves a truncated file behind.
func downloadFile(project, remote, output string) error {
	file, err := fetchFile(project, remote)
	if err != nil {
		return err
	}

	size, err := writeAtomically(output, bytes.NewReader(file.contents))
	if err != nil {
		return err
	}
	abs, err := filepath.Abs(output)
	if err != nil {
		abs = output
	}
	return printJSON(struct {
		Success   bool   `json:"success"`
		ID        string `json:"id"`
		Path      string `json:"path"`
		Output    string `json:"output"`
		Size      int64  `json:"size"`
		Encrypted bool   `json:"encrypted"`
	}{true, file.ID, file.Path, abs, size, file.encrypted})
}

// readFile fetches the stored file at remote and prints its contents as
// {"data": "<base64>"}, so a caller can use a file without writing it to
// disk. The contents are base64-encoded because they may be binary, which
// a JSON string cannot carry as it is.
func readFile(project, remote string) error {
	file, err := fetchFile(project, remote)
	if err != nil {
		return err
	}
	return printJSON(struct {
		Data string `json:"data"`
	}{base64.StdEncoding.EncodeToString(file.contents)})
}

// writeAtomically streams r into a temporary file beside path and renames it
// into place. Parent directories are created as needed. The file is private
// to the user (mode 0600): stored files may hold credentials.
func writeAtomically(path string, r io.Reader) (int64, error) {
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return 0, fmt.Errorf("creating %s: %w", dir, err)
		}
	}
	tmp := path + ".part"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return 0, failf("%v", err)
	}
	n, err := io.Copy(f, r)
	if err != nil {
		f.Close()
		os.Remove(tmp)
		return 0, failf("writing %s: %v", path, err)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return 0, fmt.Errorf("closing %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return 0, fmt.Errorf("installing %s: %w", path, err)
	}
	return n, nil
}

// ---------------------------------------------------------------------------
// Compression
// ---------------------------------------------------------------------------

// compress squeezes data with zstd at its default level: a better ratio
// than gzip on the text this CLI mostly moves, and much faster to undo.
func compress(data []byte) ([]byte, error) {
	enc, err := zstd.NewWriter(nil)
	if err != nil {
		return nil, err
	}
	defer enc.Close()
	return enc.EncodeAll(data, make([]byte, 0, len(data))), nil
}

// decompress reverses compress.
func decompress(data []byte) ([]byte, error) {
	dec, err := zstd.NewReader(nil, zstd.WithDecoderConcurrency(0))
	if err != nil {
		return nil, err
	}
	defer dec.Close()
	return dec.DecodeAll(data, nil)
}

// ---------------------------------------------------------------------------
// Paths
// ---------------------------------------------------------------------------

// cleanPath normalises a storage path for comparison: leading slashes are
// dropped so "/a/b" and "a/b" name the same file.
func cleanPath(p string) string {
	return strings.TrimLeft(strings.TrimSpace(p), "/")
}

func samePath(a, b string) bool { return cleanPath(a) == cleanPath(b) }

// underDir reports whether path lies inside dir. "/" and "" mean the root,
// which holds everything.
func underDir(path, dir string) bool {
	prefix := strings.Trim(strings.TrimSpace(dir), "/")
	if prefix == "" {
		return true
	}
	return strings.HasPrefix(cleanPath(path), prefix+"/")
}

// ---------------------------------------------------------------------------
// Response decoding
// ---------------------------------------------------------------------------

// storedFile is one entry of the listing: the fields the CLI acts on, plus
// the entry exactly as the backend sent it for printing.
type storedFile struct {
	ID   string
	Path string
	raw  json.RawMessage
}

// decodeFileList reads a listing response. The spec describes a bare array;
// the backend wraps its other collections as {"success": true, "<name>":
// [...]}, so an object is searched for a "files" array, then any array.
func decodeFileList(raw []byte) ([]storedFile, error) {
	entries, ok := fileEntries(raw)
	if !ok {
		return nil, failf("unexpected file list response: %s", summarize(raw))
	}
	files := make([]storedFile, 0, len(entries))
	for _, entry := range entries {
		var fields struct {
			ID   json.RawMessage `json:"id"`
			Path string          `json:"path"`
		}
		if err := json.Unmarshal(entry, &fields); err != nil {
			return nil, failf("unexpected file list entry: %s", summarize(entry))
		}
		files = append(files, storedFile{ID: scalarString(fields.ID), Path: fields.Path, raw: entry})
	}
	return files, nil
}

func fileEntries(raw []byte) ([]json.RawMessage, bool) {
	var entries []json.RawMessage
	if json.Unmarshal(raw, &entries) == nil {
		return entries, true
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(raw, &obj) != nil {
		return nil, false
	}
	if json.Unmarshal(obj["files"], &entries) == nil {
		return entries, true
	}
	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if json.Unmarshal(obj[k], &entries) == nil {
			return entries, true
		}
	}
	return nil, false
}

// A ticket is the backend's answer to an upload or download request: the
// file's identity and the presigned URL to move its contents through.
type ticket struct {
	ID, Path, URL string
}

// decodeTicket reads a ticket whose URL is stored under urlKey ("uploadUrl"
// or "downloadUrl"). Fields are looked up at the top level first and then
// inside nested objects, so a wrapped {"success": true, "file": {...}}
// response decodes the same way as the bare object the spec describes.
func decodeTicket(raw []byte, urlKey string) (ticket, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return ticket{}, failf("unexpected storage response: %s", summarize(raw))
	}
	t := ticket{
		ID:   scalarString(findField(obj, "id")),
		Path: scalarString(findField(obj, "path")),
		URL:  scalarString(findField(obj, urlKey)),
	}
	if t.URL == "" {
		return ticket{}, failf("storage response has no %s: %s", urlKey, summarize(raw))
	}
	return t, nil
}

// findField returns the value under key, searching obj and then any nested
// objects depth-first (keys in sorted order, for determinism).
func findField(obj map[string]json.RawMessage, key string) json.RawMessage {
	if v, ok := obj[key]; ok {
		return v
	}
	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		var nested map[string]json.RawMessage
		if json.Unmarshal(obj[k], &nested) != nil {
			continue
		}
		if v := findField(nested, key); v != nil {
			return v
		}
	}
	return nil
}

// scalarString renders a JSON scalar as text: a string unquoted, a number
// as written. Ids are strings in the spec but integers elsewhere in the
// backend; either way they end up in a URL path as text.
func scalarString(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return string(raw)
}
