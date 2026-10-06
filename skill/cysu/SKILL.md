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

Every command needs an API token. It is read from the `CYSU_TOKEN` environment variable, or from `--token <token>` on the command line. If the CLI reports a missing or rejected token, stop and ask the user for it.

## Storage

Storage is used for holding files in a persistent location, where it can be accessed across multiple devices/sessions. To list objects in the storage:

```
cysu storage list [--dir <dir>]
```

To upload and download files from the storage:

```
cysu storage upload --file <localFile> --path <storagePath>

{
  "success": true,
  "id": "dG1wL3Rlc3Q",
  "path": "<storagePath>",
  "size": 6
}

cysu storage download --path <storagePath> --output <localFile>

{
  "success": true,
  "id": "dG1wL3Rlc3Q",
  "path": "<storagePath>",
  "output": "<localFile>",
  "size": 6,
  "encrypted": true
}
```

To read a stored file without writing it to disk:

```
cysu storage read --path <storagePath>

{
  "data": "<base64>"
}
```

**Important** - The contents are base64 encoded, because a stored file may be binary. To print the raw contents, decode it first.

## JavaScript

Used for collecting and downloading the JavaScript from the target.

First, check what has been collected so far, optionally filtered by domain, without downloading it:

```
cysu javascript list  [--domain <target-domain>]

[
  {
    "path": "example.com/static/app.js",
    "updatedAt": "2026-09-25T10:00:00.000Z",
    "status": "current"
  },
  ...
]
```

If you are missing key files, submit one or more URLs for processing. The best URLs to use are main JavaScript files, which are often held on a separate CDN domain:

```
cysu javascript add  --url <url> [--url <url>...]

{
  "success": true,
  "urls": ["https://cdn.example.com/assets/index.js"]
}
```

**Important** - Submitting queues the URL. The service then fetches, beautifies and analyses each file, and recovers original sources from any source map. Files are grouped by the host that served them. Scripts loaded from another host (e.g. a CDN) are listed and downloaded under that domain.

The JavaScript files for a domain can then be downloaded with the following:

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

**Important** - This can take up to 2 minutes.

The files will then automatically be stored in this directory structure:

```
~/cysu-js/
  <domain>/           # One directory per domain
    manifest.jsonl
    <url_path>/
      <file_name>.js
  sources/            # Originals recovered from source maps
  historical/         # Earlier versions of a file (if requested)
```

**CRITICAL** - The JavaScript files returned are ALWAYS beautified and decompressed. Do NOT attempt to do this yourself.

Each domain directory holds a `manifest.jsonl`, which indexes the collection as JSON Lines. Use it to find matches rather than grepping the tree. This should be used when looking to extract API paths, dangerous HTML assignments, URLs, and many other things from the JS.

```
{"type":"match","path":"example.com/static/app.js","kind":"url","value":"https://example.com/help","line":47369,"column":19,"endLine":47369,"endColumn":169}
{"type":"match","path":"example.com/static/app.js","kind":"url_search_params","value":"new URLSearchParams(n)","line":70498,"column":23,"endLine":70498,"endColumn":45}
{"type":"match","path":"example.com/static/app.js","kind":"post_message","value":"k.postMessage(null)","line":1304,"column":30,"endLine":1304,"endColumn":49}
{"type":"match","path":"example.com/static/app.js","kind":"query_param","value":"a","line":32683,"column":17,"endLine":32683,"endColumn":41}
```

To extract all the unique keys:

```
jq -r 'select(.type == "match") | .kind' ~/cysu-js/example.com/manifest.jsonl | sort -u 
```

To pull the unique values for a specific kind of match (url_search_params in the example):

```
jq -r 'select(.type == "match" and .kind == "url_search_params") | .value' ~/cysu-js/example.com/manifest.jsonl
```

## Endpoints

Endpoints are saved from a proxy as agents work on the application. To list all the currently captured endpoints on a domain:

```
cysu endpoints list --domain <domain>

{
  "id": "1",
  "domain": "example.com",
  "method": "GET",
  "path": "/api/v2/me",
  "observedPath": "/api/v2/me",
  "seenCount": 1,
  "firstSeenAt": "2026-09-29T11:12:16.839Z",
  "lastSeenAt": "2026-09-29T11:12:16.839Z",
  "request": {
    "method": "GET",
    "url": "https://example.com/api/v2/me",
    "body": null
  },
  "response": {
    "status": 200,
    "body": {
      "user": {
        "id": 1,
        "name": "test"
      }
    }
  }
}
```

**Important** - The endpoints list is not exhaustive. It lists endpoints that the proxy has previously seen, but doesn't guarantee full completion, or up-to-date information.

To further filter by path and method:

```
cysu endpoints list --domain <domain> --path <path> --method <method>
```

To list ALL captured endpoints:

```
cysu endpoints list
```

**Important** - The parameters 'domain' and 'path' are regex strings, by default matching on strings containing the regex. I.e 'example', will match domains 'example.com' or 'example2.com'

## Out-of-Bounds

Used for testing out-of-bounds (blind) attacks. Returns a public HTTPS URL containing a running InteractSH server, from which hits can be pulled.

To create an out-of-bounds URL:

```
cysu oob create

{
  "id": "<urlId>",
  "url": "https://<urlId><nonce>.cybersupplies.site"
}
```

**Important** - Only ONE URL can be active at a time. Creating a new URL deregisters the previous one AND clears its locally stored hits.

To read the hits from the currently active URL:

```
cysu oob pull-hits

[
  {
    "protocol": "https",
    "raw-request": "GET /probe HTTP/1.1\r\nHost: <hostname>\r\n\r\n",
    "raw-response": "<HTTP response>",
    "remote-address": "203.0.113.10",
    "timestamp": "2026-09-14T12:34:56.123456789Z"
  }
]
```

This returns the FULL history of every hit seen so far, and is safe to call repeatedly. Each call also fetches any new hits and adds them. An empty array `[]` means no hits have arrived yet.

## Email

Used for accessing information via email, e.g OTP links, password resets, or confirmations. Does not currently support sending emails.

To see what accounts are accessible:

```
cysu email accounts

[
  {
    "id": "example",
    "email": "example
  }
]
```

To read the last 20 emails:

```
cysu email list --account <accountId>

{
  "account": "example",
  "folder": "INBOX",
  "total": 100,
  "messages": [
    {
      "id": "INBOX:1",
      "from": "test@gmail.com",
      "subject": "Testing",
      "date": "2026-10-06T07:07:51Z",
      "flags": [],
      "unread": true,
      "size": 78763,
      "preview": "This is a test email ...",
      "has_attachments": false
    }
  ]
}
```

To read the full body of a specific email:

```
cysu email read --account <accountId> --email <emailId>

{
  "id": "INBOX:1",
  "from": "test@gmail.com",
  "subject": "Testing",
  "date": "2026-10-02T18:25:20Z",
  "flags": [
    "\\Seen"
  ],
  "unread": false,
  "size": 67860,
  "preview": "",
  "has_attachments": false,
  "account": "example",
  "to": "example@gmail.com",
  "cc": "",
  "reply_to": "",
  "message_id": "",
  "body": "This is a test email and here is it's full body",
  "attachments": []
}
```