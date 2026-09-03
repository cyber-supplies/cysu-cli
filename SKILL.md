---
name: cysu
description: Cyber Supplies CLI Instructions.
---

# Cyber Supplies

The CLI for Cyber Supplies (cysu), the autonomous security research infrastructure provider. 

## Output

The responses from the CLI are always JSON.

**Important** - The boolean "success" key indicates whether there was an error. The "message" key will then give details about what the error was. It is wise to implement error-safety around these key facts.

A modal response has been provided alongside each command. Use this to parse the output from the commands.

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