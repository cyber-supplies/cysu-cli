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
cysu storage upload --file ./report.txt --path docs/report.txt

{
  "success": true,
  "id": "ZG9jcy9yZXBvcnQudHh0",
  "path": "docs/report.txt",
  "size": 6
}

cysu storage download --path docs/report.txt --output ./report.txt

{
  "success": true,
  "id": "ZG9jcy9yZXBvcnQudHh0",
  "path": "docs/report.txt",
  "output": "/home/user/report.txt",
  "size": 6,
  "encrypted": true
}
```

When downloading, 'path' may be a directory. To read a stored file without writing it to disk:

```
cysu storage read --path docs/report.txt

{
  "data": "cmVwb3J0"
}
```

**Important** - The contents are base64 encoded, because a stored file may be binary. To print the raw contents, decode it first.

```
cysu storage read --path docs/report.txt | jq -r '.data' | base64 --decode
```

To delete one stored file by its exact path:

```
cysu storage delete --path docs/report.txt

{
  "id": "ZG9jcy9yZXBvcnQudHh0",
  "path": "docs/report.txt"
}
```

## Proxy

A proxy may be installed on your system to help with JavaScript and Endpoint collection. To check if it is installed:

```
cysu proxy status

{
  "installed": true
}
```

When the proxy is installed, system commands like cURL will automatically be routed via the proxy. This means endpoints and javascript are automatically collected.

To setup the proxy with Playwright or another browser tool, use the configuration:

```
- Server: `https://proxy.cybersupplies.app:443`
- Username: `cysu`
- Password: ${CYSU_TOKEN}
```

## JavaScript

Used for collecting and downloading the JavaScript from the target.

First, check what has been collected so far, optionally filtered by domain, without downloading it:

```
cysu javascript list --domain example.com

[
  {
    "path": "example.com/static/app.js",
    "updatedAt": "2026-09-25T10:00:00.000Z",
    "status": "current"
  }
]
```

If you are missing key files, submit one or more URLs for processing. The best URLs to use are main JavaScript files, which are often held on a separate CDN domain:

```
cysu javascript add --url https://cdn.example.com/assets/index.js

{
  "success": true,
  "urls": ["https://cdn.example.com/assets/index.js"]
}
```

**Important** - Submitting queues the URL. The service then fetches, beautifies and analyses each file, and recovers original sources from any source map. Files are grouped by the host that served them. Scripts loaded from another host (e.g. a CDN) are listed and downloaded under that domain.

The JavaScript files for a domain can then be downloaded with the following:

```
cysu javascript download --domain example.com

{
  "success": true,
  "domain": "example.com",
  "output": "/home/user/cysu/js/example.com",
  "manifest": "/home/user/cysu/js/example.com/manifest.jsonl",
  "files": 31,
  "size": 2483910
}
```

**Important** - This can take up to 2 minutes.

The files will then automatically be stored in this directory structure:

```
~/cysu/js/
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
jq -r 'select(.type == "match") | .kind' ~/cysu/js/example.com/manifest.jsonl | sort -u
```

To pull the unique values for a specific kind of match (url_search_params in the example):

```
jq -r 'select(.type == "match" and .kind == "url_search_params") | .value' ~/cysu/js/example.com/manifest.jsonl
```

## Endpoints

Endpoints are saved from the proxy as agents work on the application. Only 200->399 responses status codes are saved.

To list all the currently captured endpoints:

```
cysu endpoints list --domain '^example\.com$'

{
  "endpoints": [
    {
      "id": "971ced69-ff4e-44f9-973a-a9764a29d973",
      "domain": "example.com",
      "method": "GET",
      "path": "/api/v2/me"
    }
  ]
}
```

**Important** - The endpoints list is not exhaustive. It lists endpoints that the proxy has previously seen, but doesn't guarantee full completion, or up-to-date information.

To further filter by path and method:

```
cysu endpoints list --domain <domain> --path <path> --method <method>
```

**Important** - The parameters 'domain' and 'path' are regex strings, by default matching on strings containing the regex. I.e 'example', will match domains 'example.com' or 'example2.com'

To show the request/response pairs on each endpoint, include the flag 'full'. A domain OR path must be specified to do this:

```
cysu endpoints list --domain '^www\.example\.com$' --path '^/api/orgs/\*/admins$' --full

{
  "endpoints": [
    {
      "id": "48114cb0-bbec-40b5-9fd2-d48ef2c7687f",
      "domain": "www.example.com",
      "method": "GET",
      "path": "/api/orgs/*/admins",
      "request": {
        "method": "GET",
        "url": "https://www.example.com/api/orgs/12345/admins",
        "body": null
      },
      "response": {
        "status": 200,
        "body": {
          "admins": [
            "test"
          ]
        }
      }
    }
  ]
}
```

**Important** - The 'body' parameter is a JSON object that represents the data returned by the API. The '*' character represents a path parameter, such as an ID or numeric value. It is advised NOT to print the full output with the request/responses into your context, as the it may be very large.

Endpoints are only saved on in-scope domains, to view the current scope:

```
cysu endpoints list-scope

{
  "scope": [
    "example"
  ]
}
```

To add or replace (set) the scope:

```
cysu endpoints add-scope --scope 'example'

cysu endpoints set-scope --scope 'example'
```

**Important** - The scope parameter is a regex string, and will match any domains containing the string.

## Accounts

The user may provide you accounts to test the target. In order to list accounts you have access to:

```
cysu accounts list [--location <location>]

[
  {
    "id": "acc_01m4x1fzk6ek5bsmjq2q0jj6kc",
    "username": "alice@example.com",
    "location": "https://example.com/login"
  }
]
```

Location is a regex string, which is recommended for searching. Omitting it lists every account. To retrieve the authentication details for a particular account:

```
cysu accounts get --account acc_01m4x1fzk6ek5bsmjq2q0jj6kc

{
  "id": "acc_01m4x1fzk6ek5bsmjq2q0jj6kc",
  "username": "alice@example.com",
  "password": "12345",
  "location": "https://example.com/login",
  "cookie": "session=12345",
  "updatedAt": "2026-10-13T12:00:00.000Z"
}
```

**Important** - This returns live credentials. It is advised to not print out this output directly into your context.

Cookie may be null, it is filled in when another agent completes the login flow. If cookie is null, use username and password to login. Commit the cookie after with:

```
cysu accounts set-cookie --account <account> --cookie <cookie>
```

If cookie is not null, check it's validity, and reference it's updatedAt time, as it may be expired. If it is, refresh with the login, and apply the new cookie for future agents.

## Out-of-Bounds

Used for testing out-of-bounds (blind) attacks. Returns a public HTTPS URL containing a running InteractSH server, from which hits can be pulled.

To create an out-of-bounds URL:

```
cysu oob create

{
  "id": "abcdefghijklmnopqrst",
  "url": "https://abcdefghijklmnopqrstuvwxy12345678.cybersupplies.site"
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
    "email": "analyst@example.com"
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
