package main

import (
	"flag"
	"fmt"
)

func runAccounts(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("accounts: missing action (list|get|create|delete)")
	}
	action, rest := args[0], args[1:]

	switch action {
	case "list":
		fs := flag.NewFlagSet("accounts list", flag.ContinueOnError)
		project := fs.String("project", "", "project id")
		if err := fs.Parse(rest); err != nil {
			return err
		}
		if err := requireFlags(map[string]string{"project": *project}); err != nil {
			return err
		}
		return request("GET", "/projects/"+esc(*project)+"/accounts", nil)

	case "get":
		fs := flag.NewFlagSet("accounts get", flag.ContinueOnError)
		project := fs.String("project", "", "project id")
		account := fs.String("account", "", "account id")
		if err := fs.Parse(rest); err != nil {
			return err
		}
		if err := requireFlags(map[string]string{"project": *project, "account": *account}); err != nil {
			return err
		}
		return request("GET", "/projects/"+esc(*project)+"/accounts/"+esc(*account), nil)

	case "create":
		fs := flag.NewFlagSet("accounts create", flag.ContinueOnError)
		project := fs.String("project", "", "project id")
		username := fs.String("username", "", "account username")
		password := fs.String("password", "", "account password")
		location := fs.String("location", "", "login location url (optional)")
		if err := fs.Parse(rest); err != nil {
			return err
		}
		if err := requireFlags(map[string]string{"project": *project, "username": *username, "password": *password}); err != nil {
			return err
		}
		body := map[string]any{"username": *username, "password": *password}
		if *location != "" {
			body["location"] = *location
		}
		return request("POST", "/projects/"+esc(*project)+"/accounts", body)

	case "delete":
		fs := flag.NewFlagSet("accounts delete", flag.ContinueOnError)
		project := fs.String("project", "", "project id")
		account := fs.String("account", "", "account id")
		if err := fs.Parse(rest); err != nil {
			return err
		}
		if err := requireFlags(map[string]string{"project": *project, "account": *account}); err != nil {
			return err
		}
		return request("DELETE", "/projects/"+esc(*project)+"/accounts/"+esc(*account), nil)

	default:
		return fmt.Errorf("accounts: unknown action %q (list|get|create|delete)", action)
	}
}
