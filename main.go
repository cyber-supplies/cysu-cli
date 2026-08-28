// Command cysu is a CLI for the CyberSupplies backend.
//
// Usage:
//
//	cysu <resource> <action> [flags]
//
// Resources: projects, findings, accounts, endpoints.
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
		fmt.Fprintf(os.Stderr, "cysu: unknown resource %q\n\n", resource)
		usage()
		os.Exit(2)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "cysu: %v\n", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `cysu - CyberSupplies CLI

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

Environment:
  CYSU_API_URL   Override the backend base URL (default `+baseURL+`)
`)
}
