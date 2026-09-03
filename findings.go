package main

import (
	"flag"
	"fmt"
)

// Handler for the findings resource
func runFindings(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("findings: missing action (list|get|create|delete)")
	}

	action, rest := args[0], args[1:]

	switch action {

	case "list":
		fs := flag.NewFlagSet("findings list", flag.ContinueOnError)
		project := fs.String("project", "", "project id")

		if err := fs.Parse(rest); err != nil {
			return err
		}

		if err := requireFlags(map[string]string{"project": *project}); err != nil {
			return err
		}

		return request("GET", "/projects/"+esc(*project)+"/findings", nil)

	case "get":
		fs := flag.NewFlagSet("findings get", flag.ContinueOnError)
		project := fs.String("project", "", "project id")
		finding := fs.String("finding", "", "finding id")

		if err := fs.Parse(rest); err != nil {
			return err
		}

		if err := requireFlags(map[string]string{"project": *project, "finding": *finding}); err != nil {
			return err
		}

		return request("GET", "/projects/"+esc(*project)+"/findings/"+esc(*finding), nil)

	case "create":
		fs := flag.NewFlagSet("findings create", flag.ContinueOnError)
		project := fs.String("project", "", "project id")
		title := fs.String("title", "", "finding title")
		desc := fs.String("desc", "", "finding description")
		severity := fs.String("severity", "", "finding severity (optional)")

		if err := fs.Parse(rest); err != nil {
			return err
		}

		if err := requireFlags(map[string]string{"project": *project, "title": *title, "desc": *desc}); err != nil {
			return err
		}

		body := map[string]any{"title": *title, "description": *desc}

		if *severity != "" {
			body["severity"] = *severity
		}

		return request("POST", "/projects/"+esc(*project)+"/findings", body)

	case "delete":
		fs := flag.NewFlagSet("findings delete", flag.ContinueOnError)
		project := fs.String("project", "", "project id")
		finding := fs.String("finding", "", "finding id")

		if err := fs.Parse(rest); err != nil {
			return err
		}

		if err := requireFlags(map[string]string{"project": *project, "finding": *finding}); err != nil {
			return err
		}

		return request("DELETE", "/projects/"+esc(*project)+"/findings/"+esc(*finding), nil)

	default:
		return fmt.Errorf("findings: unknown action %q (list|get|create|delete)", action)
	}
}
