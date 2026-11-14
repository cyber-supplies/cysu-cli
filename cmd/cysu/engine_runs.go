package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// engineRunStage identifies a stage and bounds downloads to its event count
// when the run was fetched, so an active agent cannot keep the export running.
type engineRunStage struct {
	Index      *int   `json:"index"`
	Profile    string `json:"profile"`
	EventCount *int64 `json:"eventCount"`
}

var engineProfileIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
var engineReservedFilename = regexp.MustCompile(`(?i)^(con|prn|aux|nul|com[1-9]|lpt[1-9])(?:\.|$)`)

// clearRuns removes local run exports while keeping the runs directory.
// An absent directory is created empty; remote runs are untouched.
func clearRuns() error {
	return clearEngineDirectory("runs")
}

// clearEngineDirectory clears a local engine collection while keeping its root.
func clearEngineDirectory(collection string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("locating home directory: %w", err)
	}
	root := filepath.Join(home, "cysu", "engine", collection)
	if err := os.MkdirAll(root, 0700); err != nil {
		return fmt.Errorf("creating local %s directory: %w", collection, err)
	}
	info, err := os.Lstat(root)
	if err != nil {
		return fmt.Errorf("checking local %s directory: %w", collection, err)
	}
	if !info.IsDir() {
		return failf("local %s path is not a directory: %s", collection, root)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return fmt.Errorf("reading local %s directory: %w", collection, err)
	}
	for _, entry := range entries {
		if err := os.RemoveAll(filepath.Join(root, entry.Name())); err != nil {
			return fmt.Errorf("clearing local %s: %w", collection, err)
		}
	}
	return printJSON(map[string]any{"success": true, "directory": root})
}

// engineStageFilename keeps profile names where possible, avoids Windows
// device names, and numbers duplicate names without overwriting another stage.
func engineStageFilename(profile string, used map[string]bool) (string, error) {
	if !engineProfileIDPattern.MatchString(profile) {
		return "", failf("engine returned an invalid stage profile")
	}
	stem := profile
	if engineReservedFilename.MatchString(stem) {
		stem = "stage-" + stem
	}
	name := stem + ".md"
	for n := 2; used[strings.ToLower(name)]; n++ {
		name = stem + "-" + strconv.Itoa(n) + ".md"
	}
	used[strings.ToLower(name)] = true
	return name, nil
}

// getRun exports all stages as readable transcripts in profile-named Markdown
// files. Short references resolve to the full run ID before paths are built.
// Active runs export the events currently available; another call refreshes them.
func getRun(v flagValues) error {
	raw, err := engineSend(v, http.MethodGet, "/api/runs/"+url.PathEscape(v["run"]), nil)
	if err != nil {
		return err
	}
	var run struct {
		ID     string           `json:"id"`
		Status string           `json:"status"`
		Stages []engineRunStage `json:"stages"`
	}
	if err := json.Unmarshal(raw, &run); err != nil || !workflowIDPattern.MatchString(run.ID) || run.Stages == nil {
		return failf("engine returned an invalid run")
	}
	run.ID = strings.ToLower(run.ID)
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("locating home directory: %w", err)
	}
	root := filepath.Join(home, "cysu", "engine", "runs", run.ID)
	if err := os.MkdirAll(filepath.Dir(root), 0700); err != nil {
		return fmt.Errorf("creating engine runs directory: %w", err)
	}
	// Download into a sibling directory so failed transfers leave an earlier
	// export intact and never expose truncated stage files.
	temp, err := os.MkdirTemp(filepath.Dir(root), ".run-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temp)
	used := map[string]bool{}
	indices := map[int]bool{}
	stages := make([]map[string]any, 0, len(run.Stages))
	for _, stage := range run.Stages {
		if stage.Index == nil || *stage.Index < 0 || indices[*stage.Index] || (stage.EventCount != nil && *stage.EventCount < 0) {
			return failf("engine returned an invalid stage")
		}
		indices[*stage.Index] = true
		name, err := engineStageFilename(stage.Profile, used)
		if err != nil {
			return err
		}
		file, err := os.OpenFile(filepath.Join(temp, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return err
		}
		count, downloadErr := writeStageEvents(v, run.ID, stage, file)
		closeErr := file.Close()
		if downloadErr != nil {
			return downloadErr
		}
		if closeErr != nil {
			return closeErr
		}
		stages = append(stages, map[string]any{
			"index": *stage.Index, "profile": stage.Profile,
			"path": filepath.Join(root, name), "events": count,
		})
	}
	if err := installRunExport(temp, root); err != nil {
		return err
	}
	return printJSON(map[string]any{
		"success": true, "run": run.ID, "status": run.Status,
		"directory": root, "stages": stages,
	})
}

// writeStageEvents follows sequence paging and renders every event as readable
// text, retaining full messages while limiting tool input and output length.
func writeStageEvents(v flagValues, runID string, stage engineRunStage, dst io.Writer) (int, error) {
	transcript := engineTranscript{dst: dst, pending: map[string]json.RawMessage{}}
	var after int64
	count := 0
	for stage.EventCount == nil || after < *stage.EventCount {
		path := "/api/runs/" + url.PathEscape(runID) + "/stages/" + strconv.Itoa(*stage.Index) +
			"/events?limit=5000&after=" + strconv.FormatInt(after, 10)
		raw, err := engineSend(v, http.MethodGet, path, nil)
		if err != nil {
			return count, err
		}
		var page struct {
			Events []json.RawMessage `json:"events"`
			Next   *int64            `json:"next"`
		}
		if err := json.Unmarshal(raw, &page); err != nil || page.Events == nil || page.Next == nil {
			return count, failf("engine returned invalid stage events")
		}
		if len(page.Events) == 0 {
			return count, transcript.flush()
		}
		for _, event := range page.Events {
			var cursor struct {
				Seq int64 `json:"seq"`
			}
			if err := json.Unmarshal(event, &cursor); err != nil || cursor.Seq <= after {
				return count, failf("engine returned invalid stage event sequence")
			}
			if stage.EventCount != nil && cursor.Seq > *stage.EventCount {
				return count, transcript.flush()
			}
			if err := transcript.write(event); err != nil {
				return count, err
			}
			after = cursor.Seq
			count++
		}
		if *page.Next != after {
			return count, failf("engine returned invalid stage event cursor")
		}
	}
	return count, transcript.flush()
}

// installRunExport replaces a previous export only after all stages download.
// Keep the previous directory available for rollback if installation fails.
func installRunExport(temp, root string) error {
	info, err := os.Lstat(root)
	if os.IsNotExist(err) {
		return os.Rename(temp, root)
	}
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return failf("run export path is not a directory: %s", root)
	}
	backup, err := os.MkdirTemp(filepath.Dir(root), ".run-backup-*")
	if err != nil {
		return err
	}
	if err := os.Remove(backup); err != nil {
		return err
	}
	if err := os.Rename(root, backup); err != nil {
		return err
	}
	if err := os.Rename(temp, root); err != nil {
		if restoreErr := os.Rename(backup, root); restoreErr != nil {
			return failf("installing run export: %v; previous export is at %s (restore failed: %v)", err, backup, restoreErr)
		}
		return err
	}
	return os.RemoveAll(backup)
}
