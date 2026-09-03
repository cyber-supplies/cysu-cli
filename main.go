// CLI for the CyberSupplies backend.
//
// Usage:
//
//	cysu <resource> <action> [flags]
//
// Resources: projects, findings, accounts, endpoints, key.
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

	// First run: prompt for a passphrase and create the encryption key.
	switch resource {
	case "help", "-h", "--help", "key":
	default:
		if err := ensureKey(); err != nil {
			fmt.Fprintf(os.Stderr, "cysu: %v\n", err)
			os.Exit(1)
		}
	}

	var err error

	switch resource {

	case "key":
		err = runKey(args)
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

const DESCRIPTION = `cysu - CyberSupplies CLI

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

Key (encryption):
  cysu key init    [--force]   Create the encryption key from a passphrase
  cysu key path                Print the key file location
  cysu key verify              Check a passphrase against the stored key

Environment:
  CYSU_API_URL      Override the backend base URL (default `+baseURL+`)
  CYSU_KEY_FILE     Override the key file location (default <user-config-dir>/cysu/master.key)
  CYSU_PASSPHRASE   Supply the passphrase non-interactively (setup/verify)
`

func usage() {
	fmt.Fprint(os.Stderr, DESCRIPTION)
}
