package main

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// engineScheduleFlags exposes the engine's five timing rules in one command.
func engineScheduleFlags() []flagSpec {
	return []flagSpec{
		workflowRefFlag,
		{name: "objective", placeholder: "text", usage: "objective for every scheduled run", required: true},
		{name: "type", placeholder: "type", usage: "once, daily, weekdays, weekly, or every", required: true},
		{name: "occurrence", placeholder: "value", usage: "once: YYYY-MM-DDTHH:MM; daily/weekdays: HH:MM; weekly: 'mon,thu HH:MM'; every: hours [from YYYY-MM-DDTHH:MM]", required: true},
		{name: "tz", placeholder: "timezone", usage: "IANA timezone, e.g. Europe/Dublin; defaults to the machine's timezone when detectable"},
		{name: "name", placeholder: "name", usage: "optional schedule label"},
	}
}

// validateEngineSchedule validates the occurrence according to the selected
// type before authentication. The engine resolves timezone clock changes and
// rejects one-off dates that have passed.
func validateEngineSchedule(v flagValues) error {
	invalid := func(message string) error { return cliError{message: "schedule: " + message, exit: exitUsage} }
	if err := validateEngineObjective(v["objective"], "schedule"); err != nil {
		return err
	}
	kind := v["type"]
	occurrence := v["occurrence"]
	validDate := func(value string) bool {
		_, err := time.Parse("2006-01-02T15:04", value)
		return len(value) == 16 && err == nil
	}
	validTime := func(value string) bool {
		_, err := time.Parse("15:04", value)
		return len(value) == 5 && err == nil
	}
	switch kind {
	case "once":
		if !validDate(occurrence) {
			return invalid("--occurrence for once must be a real local date and time in YYYY-MM-DDTHH:MM format")
		}
	case "daily", "weekdays":
		if !validTime(occurrence) {
			return invalid("--occurrence for " + kind + " must be a 24-hour time in HH:MM format")
		}
	case "weekly":
		parts := strings.Fields(occurrence)
		if len(parts) != 2 || !validTime(parts[1]) {
			return invalid("--occurrence for weekly must be 'mon,thu HH:MM'")
		}
		names := map[string]string{
			"mon": "mon", "monday": "mon", "tue": "tue", "tuesday": "tue",
			"wed": "wed", "wednesday": "wed", "thu": "thu", "thursday": "thu",
			"fri": "fri", "friday": "fri", "sat": "sat", "saturday": "sat", "sun": "sun", "sunday": "sun",
		}
		seen := map[string]bool{}
		days := []string{}
		for _, value := range strings.Split(parts[0], ",") {
			day := names[strings.ToLower(value)]
			if day == "" {
				return invalid("--occurrence for weekly must list valid days, e.g. 'mon,thu 09:00'")
			}
			if !seen[day] {
				days = append(days, day)
				seen[day] = true
			}
		}
		v["occurrence"] = strings.Join(days, ",") + " " + parts[1]
	case "every":
		parts := strings.Fields(occurrence)
		if len(parts) != 1 && len(parts) != 3 {
			return invalid("--occurrence for every must be hours, optionally followed by 'from YYYY-MM-DDTHH:MM'")
		}
		hours, err := strconv.Atoi(parts[0])
		if err != nil || hours < 1 || hours > 168 {
			return invalid("--occurrence for every must specify 1–168 whole hours")
		}
		v["occurrence"] = strconv.Itoa(hours)
		if len(parts) == 3 {
			if parts[1] != "from" || !validDate(parts[2]) {
				return invalid("--occurrence for every must be hours, optionally followed by 'from YYYY-MM-DDTHH:MM'")
			}
			v["occurrence"] += " from " + parts[2]
		}
	default:
		return invalid("--type must be once, daily, weekdays, weekly, or every")
	}
	if !v.provided("tz") {
		v["tz"] = localScheduleTimezone()
	}
	v["tz"] = strings.TrimSpace(v["tz"])
	if v["tz"] == "" || v["tz"] == "Local" {
		return invalid("provide --tz with an IANA timezone such as Europe/Dublin or UTC; the local timezone could not be determined")
	}
	return nil
}

// localScheduleTimezone discovers an IANA name without substituting the
// server's timezone or a fixed UTC offset. Unknown platforms require --tz.
func localScheduleTimezone() string {
	zoneName := func(path string) string {
		if _, name, found := strings.Cut(filepath.ToSlash(path), "zoneinfo/"); found {
			return strings.TrimPrefix(strings.TrimPrefix(name, "posix/"), "right/")
		}
		return ""
	}
	if value, exists := os.LookupEnv("TZ"); exists {
		tz := strings.TrimPrefix(strings.TrimSpace(value), ":")
		if tz == "" {
			return "UTC"
		}
		if name := zoneName(tz); name != "" {
			return name
		}
		if filepath.IsAbs(tz) {
			return "" // A custom timezone file does not identify an IANA zone.
		}
		return tz
	}
	if path, err := filepath.EvalSymlinks("/etc/localtime"); err == nil {
		if name := zoneName(path); name != "" {
			return name
		}
	}
	if raw, err := os.ReadFile("/etc/timezone"); err == nil {
		return strings.TrimSpace(string(raw))
	}
	return ""
}

// engineScheduleBody translates validated flags into the separate schedules
// resource; the workflow template itself is neither modified nor copied.
func engineScheduleBody(v flagValues) map[string]any {
	kind := v["type"]
	when := map[string]any{"type": kind}
	switch kind {
	case "once":
		when["at"] = v["occurrence"]
	case "every":
		parts := strings.Fields(v["occurrence"])
		hours, _ := strconv.Atoi(parts[0])
		when["hours"] = hours
		if len(parts) == 3 {
			when["start"] = parts[2]
		}
	case "weekly":
		parts := strings.Fields(v["occurrence"])
		when["days"] = strings.Split(parts[0], ",")
		when["time"] = parts[1]
	default:
		when["time"] = v["occurrence"]
	}
	body := map[string]any{"workspaceId": v["workflow"], "objective": v["objective"], "when": when, "tz": v["tz"]}
	if v.provided("name") {
		body["name"] = v["name"]
	}
	return body
}

// scheduleWorkflow creates the schedule and prints the engine's JSON response,
// including its ID, timezone, summary and next run time.
func scheduleWorkflow(v flagValues) error {
	return engineRequest(v, http.MethodPost, "/api/schedules", engineScheduleBody(v))
}

// validateScheduleID requires the engine's exact schedule UUID, not a workflow
// ID lookup, name, or short prefix. Validate before authentication or requests.
func validateScheduleID(v flagValues) error {
	if !workflowIDPattern.MatchString(v["schedule"]) {
		return cliError{message: "--schedule must be a full schedule UUID; use the id returned by schedule", exit: exitUsage}
	}
	v["schedule"] = strings.ToLower(v["schedule"])
	return nil
}

// unscheduleWorkflow removes the schedule while retaining its previous runs.
// The engine returns an empty 204; provide the CLI's usual JSON acknowledgment.
func unscheduleWorkflow(v flagValues) error {
	if _, err := engineSend(v, http.MethodDelete, "/api/schedules/"+url.PathEscape(v["schedule"]), nil); err != nil {
		return err
	}
	return printJSON(map[string]string{"deleted": v["schedule"]})
}
