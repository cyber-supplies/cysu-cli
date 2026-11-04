---
name: cysu
description: Cyber Supplies CLI Instructions.
---

# Cyber Supplies

The CLI for Cyber Supplies (cysu), the autonomous security research infrastructure provider.

Further documentation: https://docs.cybersupplies.app/

## Output

Every response is JSON. Backend responses are printed to stdout.

**CRITICAL** - When a error occurs, either CLI or Server, the "error" key will be present in the JSON. It may also include a recommended action path to take. It is highly recommended to use the presence of a key as a safety check before passing the result of the tool to other commands.

```
{"error": "Project Not Found"}
```

A modal response has been provided alongside each command. Use this to parse the output from the commands.

## Authentication

Every command needs an API token. It is read from the `CYSU_TOKEN` environment variable, or from `--token <token>` on the command line. If the CLI reports a missing or rejected token, stop and ask the user for it.

There are two authentication modes you may be in for the CLI, 'user' mode, or 'llm' mode. To verify which mode you are currently in:

```
cysu profiles list

// LLM
{"error":"Invalid token"}

// User
[
  ...
]
```

The user authentication can access all commands. LLM authentication can access only commands marked as 'llm'. Certain subcommands may be restricted as well if not documented here.

## Storage [LLM]

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

## Proxy [LLM]

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

## JavaScript [LLM]

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

## Endpoints [LLM]

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

## Accounts [LLM]

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

## Email [LLM]

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

## Notes [LLM]

Notes allow you to store learnings about a website in a structured manner. They are broken down by domain, and optionally by path.

**Important** - An individual note is limited to 350 characters. Make each note as short and concise as possible. If you need more space, add extra notes.

To list notes currently written:

```
cysu notes list [--domain <regex>] [--path <regex>]

[
  {
    "id": "note_01m5fet0rbfvgv96mp048mvm9h",
    "domain": "example.com",
    "path": "",
    "createdAt": "2026-10-21T14:45:54.163Z",
    "content": "Test"
  }
]
```

**Important** - Reading notes should be done by tuning the domain and path to the correct areas

To create a note:

```
./cysu notes create --domain <domain> --content <content> [--path <path>]

{
  "id": "note_01m6kyb6hte2zazfntnd6za52a",
  "domain": "example.com",
  "path": "/test",
  "createdAt": "2026-11-04T18:50:04.754Z",
  "content": "test"
}
```

To edit and remove a note:

```
cysu notes modify --note <note> [--domain <domain>] [--path <path>] [--content <content>]

cysu notes delete --note <note>
```


## Profiles [USER]

Profiles allow a user to convert their methodology into repeatable LLM workflows that can be later instantiated on any machine. They consist of Skills, Prompts, MCP Servers, and Agents.

To list the current profiles:

```
cysu profiles list     

[
  {
    "id": "example",
    "tag": [
      "example-tag",
      "1"
    ]
  }
]
```

Profiles are edited by pulling the files onto the local machine, then pushing the changes/additions to Cyber Supplies. To pull a specific profile or all profiles:

```
cysu profiles edit [--profile <id>] [--all]

// --profile

{
  "directory": "/home/jakubjn/cysu/profiles/example",
  "items": 11,
  "profile": "example",
  "success": true
}

// --all

{
  "profiles": [
    {
      "directory": "/home/jakubjn/cysu/profiles/example",
      "items": 11,
      "profile": "example",
      "success": true
    }
  ],
  "success": true
}

```

**Important** - This pulls the current profiles into ~/cysu/profiles, and override any current changes. The command '--all' will wipe the directory and replace it with the currently synced profiles.

The directory ~/cysu/profiles expects the following structure:

```
/home/jakubjn/cysu/profiles
└── <profile-name>
    ├── agents
    │   └── <agent-name>
    │       └── AGENT.md
    ├── mcp
    │   └── <mcp-name>
    │       └── settings.json
    ├── prompts
    │   └── <prompt-name>
    │       └── AGENTS.md
    ├── skills
    │   └── <skill-name>
    │       └── SKILL.md
    └── tags.txt // New-line delimited tags
```

To save a profile after edits, or to save all profiles currently stored locally:

```
cysu profiles save [--profile <id>] [--all]

// --profile

{
  "changes": {
    "created": 0,
    "deleted": 0,
    "unchanged": 11,
    "updated": 0
  },
  "directory": "/home/jakubjn/cysu/profiles/example",
  "profile": "example",
  "success": true
}

// --all

{
  "profiles": [
    {
      "changes": {
        "created": 0,
        "deleted": 0,
        "unchanged": 11,
        "updated": 0
      },
      "directory": "/home/jakubjn/cysu/profiles/example",
      "profile": "example",
      "success": true
    }
  ],
  "success": true
}
```

**Important** - Saving '--all' is destructive, any profile missing from the local sync will be removed from Cyber Supplies.

To create a new profile, optionally with example data filled in:

```
cysu profiles create --profile <id> [--example]

{
  "directory": "/home/jakubjn/cysu/profiles/example",
  "items": 4,
  "profile": "example",
  "success": true
}
```

The profile will not be created in Cyber Supplies until it is saved.

## Engine [USER]

The engine allows running multi-profile workflows, where each profile runs inside of an isolated containe with cysu and security tools attached. 

The engine is hosted on the user's infrastructure and must be configured. To check if the engine is configured and successfully configured:

```
cysu engine status

{
  "version": "0.2.0",
  "docker": "29.9.3",
  "image": "ghcr.io/cyber-supplies/cysu-engine-workspace:0.2.0",
  "apiUrl": "https://cysu-gateway-670100724771.europe-west1.run.app",
  "cysu": true,
  "llm": {
    "mode": "codex",
    "ready": true
  },
  "configured": true
}
```

If configured is false, or an error occurs, the engine is not available to use, and the user must set CYSU_ENGINE_URL, as well as run 'cysu engine auth --passphrase'. If the user has directly asked to use the engine, then do not attempt to bypass this issue, surface it immediately to the user.

To list the current workflows in the engine:

```
cysu engine get-workflows

[
  {
    "id": "8e4759b1-9998-4851-a717-2bd88dd1bfb1",
    "name": "rexample",
    "request": {
      "globalProfile": "global",
      "model": "GPT-5.6-sol",
      "prompt": "",
      "reasoningEffort": "high",
      "stages": [
        {
          "profile": "recon"
        },
        {
          "profile": "attack"
        },
        {
          "profile": "triage"
        }
      ]
    },
    "createdAt": "2026-11-04T09:19:23.685Z",
    "updatedAt": "2026-11-04T09:19:23.685Z"
  }
]
```

To create a workflow:

```
cysu engine create-workflow --name <name> [--prompt <text>] [--profile <id>] [--global-profile <id>] [--model <model>] [--reasoning-effort <effort>]

{
  "id": "1156627f-36cd-407f-ba5f-87b6df501b49",
  "name": "example",
  "request": {
    "globalProfile": "global",
    "model": "gpt-5.6-sol",
    "prompt": "example",
    "reasoningEffort": "high",
    "stages": [
      {
        "profile": "attack"
      }
    ]
  },
  "createdAt": "2026-11-04T18:14:47.715Z",
  "updatedAt": "2026-11-04T18:14:47.715Z"
}
```

**Important** - Profile can be used multiple times. The workflow will be created with the profiles executing in the order provided.

To delete or edit a workflow:

```
cysu engine delete-workflow --workflow <id>

cysu engine edit-workflow --workflow [--name <name>] [--prompt <text>] [--profile <id>] [--global-profile <id>] [--model <model>] [--reasoning-effort <effort>]
```

To run a workflow, then get it's results:

```
cysu engine run --workflow <id>

cysu engine get-run --run <id> [--cursor]

{
  "run": {
    "id": "e1bfb412-8aa7-4e9e-8905-9ae31be624a0",
    "name": "example",
    "status": "succeeded",
    "workspace": {
      "id": "1156627f-36cd-407f-ba5f-87b6df501b49",
      "name": "example"
    },
    "stages": [
      {
        "index": 0,
        "profile": "example",
        "tag": "",
        "status": "succeeded"
      }
    ]
  },
  "items": [
    {
      "stage": 0,
      "profile": "example",
      "seq": 1,
      "time": "2026-11-04T18:29:17.383Z",
      "kind": "attempt",
      "text": "initial (gpt-5.6-sol)"
    },
    {
      "stage": 0,
      "profile": "example",
      "seq": 4,
      "time": "2026-11-04T18:29:23.639Z",
      "kind": "message",
      "text": "I’m using the `cysu` skill because the workspace instructions require its storage workflow. I’ll first validate that the required objective fields exist; if any are unset, I’ll stop as instructed."
    },
  ],
  "cursor": "1",
  "done": true
}
```

To list previous runs:

```


[
  {
    "id": "e1bfb412-8aa7-4e9e-8905-9ae31be624a0",
    "status": "succeeded",
    "createdAt": "2026-11-04T18:29:11.923Z",
    "startedAt": "2026-11-04T18:29:11.926Z",
    "finishedAt": "2026-11-04T18:29:32.401Z",
    "cancelRequested": false,
    "name": "example",
    "globalProfile": "",
    "model": "gpt-5.6-sol",
    "reasoningEffort": "high",
    "stageCount": 1,
    "stages": [
      {
        "index": 0,
        "profile": "attack",
        "tag": "",
        "status": "succeeded"
      }
    ]
  }
]
```
