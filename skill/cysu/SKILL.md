---
name: cysu
description: Cyber Supplies CLI Instructions.
---

# Cyber Supplies

The CLI for Cyber Supplies (cysu), the autonomous security research infrastructure provider. 

## Output

Every response is JSON. Backend responses are printed to stdout.

**CRITICAL** - When a error occurs, either CLI or Server, the "error" key will be present in the JSON. It may also include a recommended action path to take. It is highly recommended to use the presence of a key as a safety check before passing the result of the tool to other commands.

```
{"error": "Project Not Found"}
```

A modal response has been provided alongside each command. Use this to parse the output from the commands.

## Authentication

Every command except `key` and `debug` needs an API token. It is read from the `CYSU_TOKEN` environment variable, or from `--token <token>` on the command line. If the CLI reports a missing or rejected token, stop and ask the user for it.

## Projects

All resources in Cyber Supplies belong to a project. Projects are created by the user. To view available projects:

```
cysu projects list

{
  "success": true,
  "projects": [
    {
      "id": "example-app-a68fn",
      "name": "Example App",
      "scope": [
        "*example*",
        "*test*"
      ]
    }
  ]
}
```

**Important** - Select the project that the user has instructed you to, or the one that best fits your goal. To select a project:

```
cysu projects select --project <projectID>

{
  "project": "<projectID>"
}
```

Alternatively, a project can also be specified with the CLI flag `--project <projectID>` on each request. If a flag is specified, it will take priority over the currently selected project.

## Accounts

**CRITICAL** - Requires project selection OR specified flag

The user may provision you accounts for use inside the projects. To see available accounts:

```
cysu accounts list

[
  {
    "id": "acc_01m1r85wgef0x88sa4p8qrkjtb",
    "username": "pentester1",
    "location": "https://example.com/login"
  }
]
```

To retrieve the authentication for a specific account:

```
cysu accounts get-auth --account <accountID>

{
  "id": "<accountID>",
  "username": "pentester1",
  "cookies": "session=8f14e45fceea167a; token=5d41402abc4b2a76;"
}
```

**Important** - This returns live credentials. It is highly recommended to parse the JSON and store the credentials inside a file, rather than reading it into context.

## Endpoints

**CRITICAL** - Requires project selection OR specified flag

To view the collected endpoints for a target:

```
cysu endpoints list

{
  "endpoints": [
    {
      "url": "https://www.example.com/search",
      "method": "POST",
      "parameters": [
        {
          "name": "q",
          "format": "STRING",
          "optional": false
        },
        {
          "name": "limit",
          "format": "INTEGER",
          "optional": true
        }
      ]
    },
    {
      "url": "https://www.example.com/login",
      "method": "POST",
      "parameters": [
        {
          "name": "username",
          "format": "STRING",
          "optional": false
        },
        {
          "name": "password",
          "format": "STRING",
          "optional": false
        },
        {
          "name": "remember",
          "format": "BOOLEAN",
          "optional": true
        }
      ]
    }
  ]
}
```

To search for endpoints using regex:

```
cysu endpoints search --query <regex>

{
  "endpoints": [
    {
      "url": "https://www.example.com/login",
      "method": "POST",
      "parameters": [
        {
          "name": "username",
          "format": "STRING",
          "optional": false
        },
        {
          "name": "password",
          "format": "STRING",
          "optional": false
        },
        {
          "name": "remember",
          "format": "BOOLEAN",
          "optional": true
        }
      ]
    }
  ]
}
```

## Storage

**CRITICAL** - Requires project selection OR specified flag

Storage is used for holding files in a persistent location, where it can be accessed across multiple devices/sessions. To list objects in the storage:

```
cysu storage list --dir <dir>
```

To upload and download files from the storage:

```
cysu storage upload --file <localFile> --path <storagePath>

cysu storage download --path <storagePath> --output <localFile>
```

To read a stored file without writing it to disk. The contents are base64 encoded, because a stored file may be binary:

```
cysu storage read --path <storagePath>

{
  "data": "<base64>"
}
```

## Debugging

When a command fails and the error alone does not explain why, re-run it with `--debug`. Every command accepts it, and it traces each backend request on stderr as it happens:

```
cysu findings list --debug

{"debug":"request","n":1,"method":"GET","url":"https://.../api/projects/nope/findings","headers":{"Accept":"application/json","Authorization":"Bearer ****4321"}}
{"debug":"response","n":1,"status":404,"duration_ms":88,"bytes":29,"body":{"error":"Project Not Found"}}
```

**Important** - Tracing is written to stderr only. Stdout stays exactly what it would be without the flag, so it is always safe to add `--debug` to a command and keep parsing the result the same way.

Every trace record carries a `debug` key and never an `error` key, so tracing does not interfere with the safety check described above. Credentials are redacted before anything is written, so the trace is safe to read into context.

To check the setup itself, use `cysu debug info`. It runs offline and needs neither a token nor an encryption key, so it answers even when every other command is failing:

```
cysu debug info

{
  "ok": false,
  "version": "0.1.7",
  "platform": "linux/amd64",
  "api_url": "https://cysu-backend-670100724771.europe-west1.run.app",
  "token": {
    "present": false
  },
  "key": {
    "path": "/home/user/.config/cysu/master.key",
    "exists": false,
    "ok": false,
    "detail": "no key file; run `cysu key init`"
  },
  "config": {
    "path": "/home/user/.config/cysu/config.json",
    "exists": true,
    "project": "example-app-a68fn"
  },
  "problems": [
    "no API token: set CYSU_TOKEN or pass --token <token>",
    "encryption key unusable: no key file; run `cysu key init`"
  ]
}
```

`cysu debug check` reports the same and then makes one request to the backend, which separates a backend that cannot be reached from one that refused the token:

```
cysu debug check

{
  "ok": false,
  "backend": {
    "reachable": true,
    "authorised": false,
    "duration_ms": 210,
    "detail": {
      "error": "Invalid API token"
    }
  },
  "problems": [
    "backend refused the token"
  ]
}
```

**CRITICAL** - Finding a problem is still a successful run, so neither command sets the `error` key. Read the `ok` field and the `problems` list instead. If `problems` names a missing or rejected token, stop and ask the user for it rather than retrying.
