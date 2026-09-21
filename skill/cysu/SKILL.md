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

Every command except `key` needs an API token. It is read from the `CYSU_TOKEN` environment variable, or from `--token <token>` on the command line. If the CLI reports a missing or rejected token, stop and ask the user for it.

## Projects

Resources in Cyber Supplies often belong to a project. Projects are created by the user. To view available projects:

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
    "password": "12345",
    "location": "https://example.com/login"
  }
]
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

To read a stored file without writing it to disk:

```
cysu storage read --path <storagePath>

{
  "data": "<base64>"
}
```

**IMPORTANT** - The contents are base64 encoded, because a stored file may be binary. To print the raw contents, decode it first.

## JavaScript

Used for collecting and downloading the JavaScript from the target. This is two steps, some time apart.

First, submit one or more URLs. A page URL is enough, the service follows every script reference and chunk it finds from there:

```
cysu javascript add  --url <url> [--url <url>...]

{
  "success": true,
  "urls": ["https://example.com/"]
}
```

**IMPORTANT** - Submitting only queues the URL. The service then fetches, beautifies and analyses each file, and recovers original sources from any source map. This takes time. Downloading before the service has finished returns `{"error": "No matching files to export"}` rather than an empty result.

Once collected, the JavaScript files for a domain can be downloaded with the following:

```
cysu javascript download  --domain <target-domain>

{
  "success": true,
  "domain": "example.com",
  "output": "/home/user/cysu-js/example.com",
  "manifest": "/home/user/cysu-js/example.com/manifest.jsonl",
  "files": 31,
  "size": 2483910
}
```

The files will then automatically be stored in this directory structure:

```
~/
  cysu-js/
    <target-domain>/
      manifest.jsonl
      path/
        to/
          file_1.js
    <another-domain>/
      path/
        to/
          file_2.js
    sources/          originals recovered from source maps
    historical/       earlier versions of a file
```

**CRITICAL** - The JavaScript files returned are ALWAYS beautified and decompressed. Do NOT attempt to do this yourself.

**IMPORTANT** - Each domain directory holds a `manifest.jsonl`, which indexes the collection as JSON Lines. Use it to find matches rather than grepping the tree. An `export` header first, then a `file` line per script and a `source` line per original recovered from a source map, then every `match`:

```
{"type": "export", "domain": "example.com", "files": 31, "matches": 412}
{"type": "file",   "path": "example.com/static/app.js", "analysis": "complete"}
{"type": "source", "path": "sources/…/src/api.ts", "bundle": "example.com/static/app.js"}
{"type": "match",  "path": "example.com/static/app.js", "kind": "url", "value": "/api/v2/users", "line": 412}
```

Paths are relative to `~/cysu-js`, so a match line opens the right file at the right line.

To remove all JavaScript files that are currently downloaded:

```
cysu javascript clear

{
  "success": true
}
```

## Out-of-Bounds

Used for testing out-of-bounds (blind) attacks. Returns a public HTTPS URL containing a running InteractSH server, from which hits can be pulled. Currently does not support hosting payloads.

To create an out-of-bounds URL:

```
cysu oob create

{
  "id": "<urlId>",
  "url": "https://<urlId><nonce>.cybersupplies.site/"
}
```

**Important** - Only ONE URL can be active at the same time. If you create a URL, the previous one will CEASE to exists, and it's stored hits will be LOST.

To read the hits from the currently active URL:

```
cysu oob pull-hits

{
    "events": [
       {
          "protocol": "https",
          "raw-request": "GET /probe HTTP/1.1\r\nHost: <hostname>\r\n\r\n",
          "raw-response": "<HTTP response>",
          "remote-address": "203.0.113.10",
          "timestamp": "2026-09-14T12:34:56.123456789Z"
        },
        ...
    ]
}
```
