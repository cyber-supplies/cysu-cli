package main

import (
	"flag"
	"fmt"
)

func runProjects(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("projects: missing action (list|get|create|delete)")
	}
	action, rest := args[0], args[1:]

	switch action {
	case "list":
		fs := flag.NewFlagSet("projects list", flag.ContinueOnError)
		if err := fs.Parse(rest); err != nil {
			return err
		}
		return request("GET", "/projects", nil)

	case "get":
		fs := flag.NewFlagSet("projects get", flag.ContinueOnError)
		project := fs.String("project", "", "project id")
		if err := fs.Parse(rest); err != nil {
			return err
		}
		if err := requireFlags(map[string]string{"project": *project}); err != nil {
			return err
		}
		return request("GET", "/projects/"+esc(*project), nil)

	case "create":
		fs := flag.NewFlagSet("projects create", flag.ContinueOnError)
		name := fs.String("name", "", "project name")
		if err := fs.Parse(rest); err != nil {
			return err
		}
		if err := requireFlags(map[string]string{"name": *name}); err != nil {
			return err
		}
		return request("POST", "/projects", map[string]any{"name": *name})

	case "delete":
		fs := flag.NewFlagSet("projects delete", flag.ContinueOnError)
		project := fs.String("project", "", "project id")
		if err := fs.Parse(rest); err != nil {
			return err
		}
		if err := requireFlags(map[string]string{"project": *project}); err != nil {
			return err
		}
		return request("DELETE", "/projects/"+esc(*project), nil)

	default:
		return fmt.Errorf("projects: unknown action %q (list|get|create|delete)", action)
	}
}
