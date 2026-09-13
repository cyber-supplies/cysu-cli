package main

// Tests for the flag plumbing in cli.go.

import (
	"strings"
	"testing"
)

// acceptsFlag reports whether c takes the named flag within r.
func acceptsFlag(r resource, c command, name string) bool {
	for _, f := range r.flags(c) {
		if f.name == name {
			return true
		}
	}
	return false
}

func TestGlobalFlags(t *testing.T) {
	for _, r := range resources {
		for _, c := range r.commands {
			if !acceptsFlag(r, c, "debug") {
				t.Errorf("cysu %s %s does not accept --debug", r.name, c.name)
			}

			// --token belongs to the commands that reach the backend. The
			// debug commands take it too, so they can report on a token
			// without the framework insisting on one.
			want := r.usesAPI(c) || r.name == "debug"
			if got := acceptsFlag(r, c, "token"); got != want {
				t.Errorf("cysu %s %s accepts --token = %v, want %v", r.name, c.name, got, want)
			}
		}
	}
}

// A command's own flag list is shared between invocations, so building the
// full list must not append into it.
func TestFlagsDoNotMutateTheCommand(t *testing.T) {
	c, ok := projectsResource.lookup("get")
	if !ok {
		t.Fatal("projects get is missing")
	}

	before := append([]flagSpec(nil), c.flags...)
	projectsResource.flags(c)

	if len(c.flags) != len(before) {
		t.Fatalf("flags changed the command's own list: %d flags, want %d", len(c.flags), len(before))
	}
	for i := range before {
		if c.flags[i].name != before[i].name {
			t.Errorf("flag %d is now %q, want %q", i, c.flags[i].name, before[i].name)
		}
	}
}

func TestDebugFlagTurnsTracingOn(t *testing.T) {
	captureTrace(t) // restores the tracing state when the test ends
	setTracing(false)

	res := resource{
		name: "example",
		commands: []command{{
			name: "run",
			run:  func(flagValues) error { return nil },
		}},
	}

	if err := res.run([]string{"run"}); err != nil {
		t.Fatalf("run: %v", err)
	}
	if tracingEnabled {
		t.Error("tracing is on without --debug")
	}

	if err := res.run([]string{"run", "--debug"}); err != nil {
		t.Fatalf("run --debug: %v", err)
	}
	if !tracingEnabled {
		t.Error("--debug did not turn tracing on")
	}
}

// The usage line is what a failing command tells the caller, so --debug has
// to appear there to be discoverable at the moment it is wanted.
func TestUsageLineMentionsDebug(t *testing.T) {
	c, ok := projectsResource.lookup("get")
	if !ok {
		t.Fatal("projects get is missing")
	}

	usage := c.usageLine(projectsResource)
	for _, want := range []string{"[--debug]", "[--token <token>]"} {
		if !strings.Contains(usage, want) {
			t.Errorf("usage line %q does not mention %s", usage, want)
		}
	}
}
