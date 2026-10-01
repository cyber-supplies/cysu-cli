// cysu is the command-line client for the Cyber Supplies API gateway.
//
// Usage:
//
//	cysu <resource> <action> [flags]
//
// Each resource is a command table in its own file. cli.go holds the framework
// that parses flags and runs those tables;
// client.go holds the HTTP layer they call into.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
)

// resources lists every command group, in the order they appear in the help.
var resources = []resource{
	javascriptResource,
	storageResource,
	proxyResource,
	oobResource,
	keyResource,
	debugResource,
}

// version is the release this binary was built from. The release workflow
// sets it from the tag; a build from source reports "dev".
var version = "dev"

func main() {
	// Read before anything is parsed, so that a malformed command line is
	// traceable too. --debug can still switch tracing on later (see cli.go).
	setTracing(tracingFromEnv())

	if len(os.Args) < 2 {
		printUsage()
		os.Exit(2)
	}
	name, args := os.Args[1], os.Args[2:]

	switch name {
	case "help", "-h", "--help":
		printUsage()
		return
	}

	res, ok := findResource(name)
	if !ok {
		exitOnError(usageErrorf(rootUsageLine(), "unknown resource %q", name))
	}
	exitOnError(res.run(args))
}

func findResource(name string) (resource, bool) {
	for _, r := range resources {
		if r.name == name {
			return r, true
		}
	}
	return resource{}, false
}

// rootUsageLine renders the top-level invocation with every resource.
func rootUsageLine() string {
	names := make([]string, len(resources))
	for i, r := range resources {
		names[i] = r.name
	}
	return "cysu {" + strings.Join(names, "|") + "} <action> [flags]"
}

// exitOnError reports err as one {"error": ...} line on stderr (see
// errors.go) and exits with its status. -h is not an error and exits 0.
func exitOnError(err error) {
	if err == nil {
		return
	}
	if errors.Is(err, flag.ErrHelp) {
		os.Exit(0)
	}
	e := asCLIError(err)
	e.print(os.Stderr)
	os.Exit(e.exit)
}

func printUsage() {
	w := os.Stderr

	fmt.Fprintf(w, "cysu %s - CyberSupplies CLI\n\nUsage:\n  cysu <resource> <action> [flags]\n\n", version)

	for _, r := range resources {
		r.printHelp(w)
	}

	// The flags every command accepts, listed once here rather than in each
	// section. They are built from the specs so the two cannot drift apart.
	fmt.Fprintln(w, "Global flags:")

	rows := make([][]string, 0, 2)
	for _, f := range []flagSpec{tokenFlag, debugFlag} {
		rows = append(rows, []string{"  " + f.label(), f.usage})
	}
	writeAligned(w, rows)

	fmt.Fprintln(w)
}
