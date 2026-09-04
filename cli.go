package main

// cli.go is the small command framework behind `cysu <resource> <action>`.
//
// Each resource (projects, findings, ...) is a table of commands. A command
// declares the flags it accepts and a run function that receives the parsed
// values. Flag parsing, required-flag checks, help text and error messages are
// all derived from that table, so they cannot drift apart from each other.
// Failures are reported through the cliError type in errors.go.

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"
)

// A flagSpec declares one flag a command accepts.
type flagSpec struct {
	name        string // without the leading dashes
	placeholder string // value name shown in help, e.g. "id" in --project <id>
	usage       string
	required    bool // an empty value is a usage error
	boolean     bool // a switch that takes no value, e.g. --force
	multi       bool // may be repeated; every occurrence is kept, e.g. --scope

	// fallback supplies the value when the flag is omitted, e.g. the selected
	// project for --project. It returns an error when there is no default
	// either; a usage-class cliError gets the command's usage line appended,
	// so it reads like any other usage error.
	fallback func() (string, error)
}

// flagValues holds the parsed flags of one invocation, keyed by flag name.
// Boolean flags are stored as "true"/"false"; read them with enabled.
// Repeatable flags are stored NUL-separated (a byte that cannot occur in a
// command-line argument); read them with list.
type flagValues map[string]string

const multiSep = "\x00"

func (v flagValues) enabled(name string) bool { return v[name] == "true" }

// list returns every value given for a repeatable flag, in order.
func (v flagValues) list(name string) []string {
	if v[name] == "" {
		return nil
	}
	return strings.Split(v[name], multiSep)
}

// multiValue collects every occurrence of a repeatable flag.
type multiValue []string

func (m *multiValue) String() string     { return strings.Join(*m, ", ") }
func (m *multiValue) Set(s string) error { *m = append(*m, s); return nil }

// A command is one action of a resource, e.g. `cysu findings create`.
type command struct {
	name  string
	flags []flagSpec
	run   func(v flagValues) error

	// local marks a command of an API resource that never talks to the
	// backend (e.g. `projects current`): it needs neither a token nor the key.
	local bool
}

// A resource groups the commands that act on one kind of object.
type resource struct {
	name     string
	title    string // section heading in the help text
	api      bool   // talks to the backend: needs an API token and the master key
	commands []command
}

// usesAPI reports whether c talks to the backend when run within r.
func (r resource) usesAPI(c command) bool { return r.api && !c.local }

// flags returns every flag c accepts within r: its own, plus --token for
// commands that talk to the backend.
func (r resource) flags(c command) []flagSpec {
	if !r.usesAPI(c) {
		return c.flags
	}
	return append(slices.Clone(c.flags), tokenFlag)
}

// ---------------------------------------------------------------------------
// Dispatch
// ---------------------------------------------------------------------------

// run dispatches args (an action name followed by its flags) to the matching
// command.
func (r resource) run(args []string) error {
	if len(args) == 0 {
		return usageErrorf(r.usageLine(), "%s: missing action", r.name)
	}
	name, rest := args[0], args[1:]

	cmd, ok := r.lookup(name)
	if !ok {
		return usageErrorf(r.usageLine(), "%s: unknown action %q", r.name, name)
	}

	values, err := cmd.parse(r, rest)
	if err != nil {
		return err
	}

	// API commands resolve their token and the master key before they touch
	// the network, so a missing token fails fast and the first-run passphrase
	// prompt happens up front rather than mid-request. Both run after parsing
	// so a usage error never triggers the prompt.
	if r.usesAPI(cmd) {
		if err := loadAPIToken(values); err != nil {
			return err
		}
		if err := ensureKey(); err != nil {
			return err
		}
	}
	return cmd.run(values)
}

func (r resource) lookup(name string) (command, bool) {
	for _, c := range r.commands {
		if c.name == name {
			return c, true
		}
	}
	return command{}, false
}

// usageLine renders the resource's invocation with every action it offers,
// e.g. "cysu projects {list|get|create|delete} [flags]".
func (r resource) usageLine() string {
	names := make([]string, len(r.commands))
	for i, c := range r.commands {
		names[i] = c.name
	}
	return "cysu " + r.name + " {" + strings.Join(names, "|") + "} [flags]"
}

// usageLine renders the command's full invocation within r, e.g.
// "cysu findings get --project <id> --finding <id> [--token <token>]".
func (c command) usageLine(r resource) string {
	return strings.TrimRight("cysu "+r.name+" "+c.name+" "+flagSynopsis(r.flags(c)), " ")
}

// parse parses args against the flags c accepts within r. On -h it prints
// the command's help and returns flag.ErrHelp.
func (c command) parse(r resource, args []string) (flagValues, error) {
	qualified := r.name + " " + c.name
	flags := r.flags(c)
	usage := c.usageLine(r)

	fs := flag.NewFlagSet(qualified, flag.ContinueOnError)
	fs.SetOutput(io.Discard) // errors and help are rendered by us, not by flag

	strs := map[string]*string{}
	bools := map[string]*bool{}
	multis := map[string]*multiValue{}
	for _, f := range flags {
		switch {
		case f.boolean:
			bools[f.name] = fs.Bool(f.name, false, f.usage)
		case f.multi:
			multis[f.name] = &multiValue{}
			fs.Var(multis[f.name], f.name, f.usage)
		default:
			strs[f.name] = fs.String(f.name, "", f.usage)
		}
	}

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			c.printHelp(os.Stderr, r)
			return nil, err
		}
		return nil, usageErrorf(usage, "%s: %v", qualified, err)
	}
	if extra := fs.Args(); len(extra) > 0 {
		return nil, usageErrorf(usage, "%s: unexpected argument %q", qualified, extra[0])
	}

	values := flagValues{}
	var missing []string
	for _, f := range flags {
		if f.boolean {
			values[f.name] = strconv.FormatBool(*bools[f.name])
			continue
		}
		if f.multi {
			items := []string(*multis[f.name])
			for _, item := range items {
				if strings.TrimSpace(item) == "" {
					return nil, usageErrorf(usage, "%s: empty value for --%s", qualified, f.name)
				}
			}
			if f.required && len(items) == 0 {
				missing = append(missing, "--"+f.name)
			}
			values[f.name] = strings.Join(items, multiSep)
			continue
		}
		value := *strs[f.name]
		if strings.TrimSpace(value) == "" && f.fallback != nil {
			var err error
			if value, err = f.fallback(); err != nil {
				return nil, withUsage(err, usage)
			}
		}
		values[f.name] = value
		if f.required && strings.TrimSpace(value) == "" {
			missing = append(missing, "--"+f.name)
		}
	}
	if len(missing) == 1 {
		return nil, usageErrorf(usage, "%s: missing required flag %s", qualified, missing[0])
	}
	if len(missing) > 1 {
		return nil, usageErrorf(usage, "%s: missing required flags %s", qualified, strings.Join(missing, ", "))
	}
	return values, nil
}

// withUsage appends the command's usage line to a usage-class cliError
// raised outside parse (such as a flag fallback's "nothing selected" error).
// Other errors pass through unchanged.
func withUsage(err error, usage string) error {
	var e cliError
	if errors.As(err, &e) && e.exit == exitUsage {
		return usageErrorf(usage, "%s", e.message)
	}
	return err
}

// ---------------------------------------------------------------------------
// Help text
// ---------------------------------------------------------------------------

// label renders the flag as it is typed, e.g. "--project <id>" or "--force".
func (f flagSpec) label() string {
	if f.boolean {
		return "--" + f.name
	}
	return "--" + f.name + " <" + f.placeholder + ">"
}

// synopsis is label wrapped in brackets when the flag is optional. A
// repeatable flag shows that it may be given more than once.
func (f flagSpec) synopsis() string {
	switch {
	case f.multi && f.required:
		return f.label() + " [" + f.label() + "...]"
	case f.multi:
		return "[" + f.label() + "...]"
	case f.required:
		return f.label()
	default:
		return "[" + f.label() + "]"
	}
}

// flagSynopsis renders flags for a usage line, e.g.
// "--project <id> --title <title> [--severity <sev>]".
func flagSynopsis(flags []flagSpec) string {
	parts := make([]string, len(flags))

	for i, f := range flags {
		parts[i] = f.synopsis()
	}

	return strings.Join(parts, " ")
}

// printHelp writes the detailed help for one command (shown on -h).
func (c command) printHelp(w io.Writer, r resource) {
	flags := r.flags(c)

	fmt.Fprintln(w, "Usage: "+c.usageLine(r))

	if len(flags) == 0 {
		return
	}

	fmt.Fprintln(w, "\nFlags:")
	rows := make([][]string, len(flags))
	for i, f := range flags {
		usage := f.usage
		if f.multi {
			usage += " (repeatable)"
		}
		if f.required {
			usage += " (required)"
		}
		rows[i] = []string{"  " + f.label(), usage}
	}
	writeAligned(w, rows)
}

// printHelp writes this resource's section of the top-level help. Shared
// flags such as --token are left out; they are documented once in main.
func (r resource) printHelp(w io.Writer) {
	fmt.Fprintf(w, "%s:\n", r.title)

	rows := make([][]string, len(r.commands))

	for i, c := range r.commands {
		rows[i] = []string{"  cysu " + r.name + " " + c.name, flagSynopsis(c.flags)}
	}

	writeAligned(w, rows)

	fmt.Fprintln(w)
}

// writeAligned prints rows as space-padded columns without trailing spaces.
func writeAligned(w io.Writer, rows [][]string) {
	var buf bytes.Buffer
	tw := tabwriter.NewWriter(&buf, 0, 0, 2, ' ', 0)
	for _, row := range rows {
		fmt.Fprintln(tw, strings.Join(row, "\t"))
	}
	tw.Flush()

	for _, line := range strings.Split(strings.TrimRight(buf.String(), "\n"), "\n") {
		fmt.Fprintln(w, strings.TrimRight(line, " "))
	}
}
