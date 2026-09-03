// CLI for the CyberSupplies backend.
//
// Usage: cysu <resource> <action> [flags]

package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	resource := os.Args[1]
	args := os.Args[2:]

	var err error

	switch resource {

	case "projects":
		err = runProjects(args)

	case "findings":
		err = runFindings(args)

	case "accounts":
		err = runAccounts(args)

	case "endpoints":
		err = runEndpoints(args)

	case "help", "-h", "--help":
		usage()
		return

	default:
		fmt.Fprintf(os.Stderr, "Unknown resource %q\n\n", resource)
		usage()
		os.Exit(2)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
}

func usage() {
	helpString := `
		Usage:
		cysu <resource> <action> [flags]

		Projects:
		cysu projects list
		cysu projects get     --project <id>
		cysu projects create  --name <name>
		cysu projects delete  --project <id>

		Findings:
		cysu findings list    --project <id>
		cysu findings get     --project <id> --finding <id>
		cysu findings create  --project <id> --title <title> --desc <description> [--severity <sev>]
		cysu findings delete  --project <id> --finding <id>

		Accounts:
		cysu accounts list    --project <id>
		cysu accounts get     --project <id> --account <id>
		cysu accounts create  --project <id> --username <user> --password <pass> [--location <url>]
		cysu accounts delete  --project <id> --account <id>

		Endpoints:
		cysu endpoints list   --project <id>
		cysu endpoints search --project <id> --query <query>

		Other:
		cysu help
	`

	fmt.Fprint(os.Stderr, helpString)
}
