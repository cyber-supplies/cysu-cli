package main

// oob.go is the `cysu oob` resource: the out-of-bounds (OOB) interaction
// feature. The backend runs an InteractSH server that hands out a unique
// callback URL and logs any DNS/HTTP request that reaches it. This resource
// asks for such a URL and reads back that log.
//
//	cysu oob create      request a callback URL, remember its id locally
//	cysu oob pull-hits    fetch new log entries and print the full history
//
// Why a local cache: the backend's read is destructive. GET
// /oob/endpoints/{id} returns each entry exactly once and clears it, so a
// plain read shows only what arrived since the last call. To give the caller
// a stable, re-readable log (openapi.yaml: "Caching on the CLI will be
// used"), pull-hits accumulates every entry it has ever seen in a cache file
// beside config.json and prints the whole accumulated list each time.

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var oobResource = resource{
	name:  "oob",
	title: "Out-of-Bounds",
	api:   true,
	commands: []command{
		{
			name: "create",
			run:  runOOBCreate,
		},
		{
			name: "pull-hits",
			run:  runOOBPullHits,
		},
	},
}

// oobEndpoint is the POST /oob/endpoints response: the correlation id and the
// callback URL to hand back to the caller.
type oobEndpoint struct {
	ID  string `json:"id"`
	URL string `json:"url"`
}

// oobCache is the on-disk accumulated log for one endpoint. Entries are kept
// as raw JSON so every field the backend sends (including the hyphenated
// raw-request / raw-response / remote-address) is preserved and printed back
// verbatim. The id ties the cache to the endpoint it belongs to: a cache left
// over from a previous `create` is ignored rather than shown for a new one.
type oobCache struct {
	CorrelationID string            `json:"correlation_id"`
	Events        []json.RawMessage `json:"events"`
}

// runOOBCreate asks the backend for a callback URL, saves its correlation id
// as the current endpoint, resets the local hit cache (a fresh endpoint has
// no history), and prints the {id, url} back to the caller.
func runOOBCreate(flagValues) error {
	raw, err := send("POST", apiPath("oob", "endpoints"), nil)
	if err != nil {
		return err
	}

	var ep oobEndpoint
	if err := json.Unmarshal(raw, &ep); err != nil {
		return failf("oob create: could not parse backend response: %v", err)
	}
	if ep.ID == "" {
		return failf("oob create: backend returned no correlation id")
	}

	if err := setOOB(ep.ID); err != nil {
		return err
	}
	// Start the cache over for the new endpoint. The old cache, if any,
	// belonged to the endpoint this one replaced.
	if err := writeOOBCache(oobCache{CorrelationID: ep.ID, Events: []json.RawMessage{}}); err != nil {
		return err
	}

	return printJSON(ep)
}

// runOOBPullHits fetches whatever new entries the backend has for the current
// endpoint, appends the ones not already cached, and prints the full
// accumulated log. Repeated calls therefore always show the complete history,
// not just the latest drain.
func runOOBPullHits(flagValues) error {
	id, err := currentOOB()
	if err != nil {
		return err
	}
	if id == "" {
		return cliError{
			message: "no OOB endpoint: run `cysu oob create` first",
			exit:    exitUsage,
		}
	}

	cache, err := readOOBCache()
	if err != nil {
		return err
	}
	// A cache from a previous endpoint must not bleed into this one.
	if cache.CorrelationID != id {
		cache = oobCache{CorrelationID: id, Events: []json.RawMessage{}}
	}

	raw, err := send("GET", apiPath("oob", "endpoints", id), nil)
	if err != nil {
		return err
	}

	// The backend answers with a bare JSON array; an empty body is treated as
	// no new entries rather than a parse failure.
	var fresh []json.RawMessage
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &fresh); err != nil {
			return failf("oob pull-hits: could not parse backend response: %v", err)
		}
	}

	cache.Events = appendNewEvents(cache.Events, fresh)

	if err := writeOOBCache(cache); err != nil {
		return err
	}

	// Print the whole accumulated log. printJSON renders the array the same
	// way a backend response would be, so the caller parses it identically.
	return printJSON(cache.Events)
}

// appendNewEvents adds the entries in fresh that are not already in existing,
// preserving order (existing first, then new arrivals). The backend already
// delivers each entry exactly once, so this dedup is belt-and-suspenders for
// the case where a response was cached but its server-side delivery marker
// did not commit, letting an entry come back a second time.
func appendNewEvents(existing, fresh []json.RawMessage) []json.RawMessage {
	seen := make(map[string]struct{}, len(existing))
	for _, e := range existing {
		seen[eventKey(e)] = struct{}{}
	}

	out := existing
	for _, e := range fresh {
		k := eventKey(e)
		if _, dup := seen[k]; dup {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, e)
	}
	return out
}

// eventKey is the identity of one log entry for deduplication: the fields that
// together make an interaction unique. Entries that cannot be parsed fall back
// to their raw bytes, so a malformed entry is never silently merged with a
// different one.
func eventKey(raw json.RawMessage) string {
	var e struct {
		Timestamp     string `json:"timestamp"`
		RemoteAddress string `json:"remote-address"`
		RawRequest    string `json:"raw-request"`
	}
	if err := json.Unmarshal(raw, &e); err != nil {
		return string(raw)
	}
	return strings.Join([]string{e.Timestamp, e.RemoteAddress, e.RawRequest}, "\x00")
}

// ---------------------------------------------------------------------------
// Cache file
// ---------------------------------------------------------------------------

const oobCacheFileName = "oob-hits.json"

// oobCachePath is the accumulated-log file, kept next to config.json and the
// key file in the user's config directory.
func oobCachePath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("locating user config dir: %w", err)
	}
	return filepath.Join(dir, "cysu", oobCacheFileName), nil
}

// readOOBCache loads the accumulated log. A missing file is an empty cache.
func readOOBCache() (oobCache, error) {
	var cache oobCache
	path, err := oobCachePath()
	if err != nil {
		return cache, err
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return cache, nil
	}
	if err != nil {
		return cache, fmt.Errorf("reading OOB cache: %w", err)
	}
	if err := json.Unmarshal(raw, &cache); err != nil {
		return cache, fmt.Errorf("parsing OOB cache %s: %w", path, err)
	}
	return cache, nil
}

// writeOOBCache atomically replaces the cache file, creating the parent
// directory (mode 0700, shared with the key and config files) if needed.
func writeOOBCache(cache oobCache) error {
	path, err := oobCachePath()
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating config dir %s: %w", dir, err)
	}

	data, err := json.MarshalIndent(cache, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding OOB cache: %w", err)
	}
	data = append(data, '\n')

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("writing OOB cache: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("installing OOB cache: %w", err)
	}
	return nil
}
