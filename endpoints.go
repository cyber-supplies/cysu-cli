package main

import (
	"flag"
	"fmt"
)

// Handler for the endpoints resource
func runEndpoints(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("endpoints: missing action (list|search)")
	}

	action, rest := args[0], args[1:]

	switch action {

	case "list":
		fs := flag.NewFlagSet("endpoints list", flag.ContinueOnError)
		project := fs.String("project", "", "project id")

		if err := fs.Parse(rest); err != nil {
			return err
		}

		if err := requireFlags(map[string]string{"project": *project}); err != nil {
			return err
		}

		return request("GET", "/projects/"+esc(*project)+"/endpoints", nil)

	case "search":
		fs := flag.NewFlagSet("endpoints search", flag.ContinueOnError)
		project := fs.String("project", "", "project id")
		query := fs.String("query", "", "search query")

		if err := fs.Parse(rest); err != nil {
			return err
		}

		if err := requireFlags(map[string]string{"project": *project, "query": *query}); err != nil {
			return err
		}

		// The backend exposes this as a QUERY-method route (see commands.md).
		return request("QUERY", "/projects/"+esc(*project)+"/endpoints/search", map[string]any{"query": *query})

	default:
		return fmt.Errorf("endpoints: unknown action %q (list|search)", action)
	}
}
