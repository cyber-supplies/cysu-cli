---
name: cysu
description: Cyber Supplies CLI Instructions.
---

# Cyber Supplies

The CLI for Cyber Supplies (cysu), the autonomous security research infrastructure provider. 

## Output

Every response is JSON. Backend responses are printed on stdout and carry a boolean `success` key; when it is false, `message` explains why.

When a command fails, the CLI exits non-zero and prints exactly one JSON line on stderr:

```
{"success": false, "message": "<what went wrong>", "usage": "<correct invocation, only for argument errors>", "action": "<what to do next>"}
```

**CRITICAL** - Do what `action` says, and nothing more. It is always one of:

- Fix the arguments and re-run. `usage` shows the correct form; you do not need to run `help` again.
- Retry once; if it fails again, stop and report the error to the user.
- Stop and report to the user, or ask them for something. Never guess a token, passphrase or id to get past this.

A backend error (for example a missing project) prints the backend's own `{"success": false, "message": ...}` on stdout, followed by the action line on stderr.

A modal response has been provided alongside each command. Use this to parse the output from the commands.

## Authentication

Every command except `key` needs an API token. It is read from the `CYSU_TOKEN` environment variable, or from `--token <token>` on the command line. If the CLI reports a missing or rejected token, stop and ask the user for it.

## Projects

All resources in Cyber Supplies belong to a project. Projects are created by the user. To view available projects:

```
cysu projects list

{
  "success": true,
  "projects": [
    ...
  ]
}
```

**Important** - Select the project that the user has instructed you to, or the one that best fits your goal. To select a project:

```
cysu projects select --project <projectID>
```

Alternatively, a project can also be specified with the CLI flag `--project <projectID>` on each request. If a flag is specified, it will take priority over the currently selected project.

## Accounts

**CRITICAL** - Requires project selection OR specified flag

The user may provision you accounts for use inside the projects. To see available accounts:

```
cysu accounts list
```

To retrieve the authentication for a specific account:

```
cysu accounts get-auth --account <accountID>
```

**Important** - This returns live credentials. It is highly recommended to parse the JSON and store the credentials inside a file, rather than reading it into context.

## Endpoints

**CRITICAL** - Requires project selection OR specified flag

To view the collected endpoints for a target:

```
‌cysu endpoints list
```

To search for endpoints using regex:

```
cysu endpoints search --query
```

## Storage

**CRITICAL** - Requires project selection OR specified flag

Storage is used for holding files in a persistent location, where it can be accessed across multiple devices/sessions. To list objects in the storage:

```
cysu storage list --dir
```

To upload and download files from the storage:

```
cysu storage upload --file --path

cysu storage download --path --output
```