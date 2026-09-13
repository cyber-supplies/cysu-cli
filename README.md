# Cyber Supplies CLI

This is the CLI the agent and user use for interacting with our backend.

## Installing

```
git clone https://github.com/jakdevreal/cysu-cli.git
cd cysu-cli
cat install.sh | sh
```

## Build From Source

```
go vet ./... 2>&1 && go build -o cysu ./cmd/cysu 2>&1 && echo "BUILD OK"
./cysu help
```

## Testing

```
go test ./... 2>&1
```

## Releasing

To set authentication using github CLI:

```
npm config set @cyber-supplies:registry https://npm.pkg.github.com
npm config set //npm.pkg.github.com/:_authToken "$(gh auth token)"
```

To create a release:

```
git checkout main && git pull
git tag -a v0.1.{VERSION_NUMBER} -m "v0.1.{VERSION_NUMBER}"
git push origin v0.1.{VERSION_NUMBER}
gh run watch --exit-status
```

The tag is the version. The workflow builds and publishes `@cyber-supplies/cysu` to GitHub Packages, and stamps the tag into the binary so that `cysu help` reports it. A build from source reports `dev`.

## Usage

```
cysu 0.1.7 - CyberSupplies CLI

Usage:
  cysu <resource> <action> [flags]

Projects:
  cysu projects list
  cysu projects get      [--project <id>]
  cysu projects create   --name <name> --scope <glob> [--scope <glob>...]
  cysu projects delete   --project <id>
  cysu projects select   --project <id>
  cysu projects current

Findings:
  cysu findings list    [--project <id>]
  cysu findings get     [--project <id>] --finding <id>
  cysu findings create  [--project <id>] --title <title> --desc <description> [--severity <sev>]
  cysu findings delete  [--project <id>] --finding <id>

Accounts:
  cysu accounts list      [--project <id>]
  cysu accounts get       [--project <id>] --account <id>
  cysu accounts create    [--project <id>] --username <user> --password <pass> [--location <url>]
  cysu accounts delete    [--project <id>] --account <id>
  cysu accounts get-auth  [--project <id>] --account <id>

Endpoints:
  cysu endpoints list    [--project <id>]
  cysu endpoints search  [--project <id>] --query <query>

Storage:
  cysu storage list      [--project <id>] [--dir <dir>]
  cysu storage upload    [--project <id>] --file <local> --path <path>
  cysu storage download  [--project <id>] --path <path> --output <local>
  cysu storage read      [--project <id>] --path <path>

Key (encryption):
  cysu key init    [--force]
  cysu key path
  cysu key verify

Debug:
  cysu debug info   [--token <token>]
  cysu debug check  [--token <token>]

Global flags:
  --token <token>  API token (overrides CYSU_TOKEN)
  --debug          trace backend requests on stderr (or set CYSU_DEBUG=1)
```

## Authentication

The recommended way to authenticate is to set an environment variable:

```
export CYSU_TOKEN=sk-xxxx
```

It is also possible to pass the token directly via a CLI flag:

```
cysu projects list --token sk-xxxx
```

## Debugging

Every command accepts `--debug`, which traces each backend request on stderr as it happens. `CYSU_DEBUG=1` does the same for a whole session:

```
cysu findings list --debug

export CYSU_DEBUG=1
```

Tracing only ever writes to stderr. Stdout is byte for byte what it would be without the flag, so a parser reading the results is unaffected and it is safe to turn on at any point:

```
{"debug":"request","n":1,"method":"GET","url":"https://.../api/projects/nope","headers":{"Accept":"application/json","Authorization":"Bearer ****4321"}}
{"debug":"response","n":1,"status":404,"duration_ms":88,"bytes":29,"body":{"error":"Project Not Found"}}
```

Each record is one line of JSON carrying a `debug` key. The `n` pairs a response with the request it answers. No record ever carries an `error` key, which stays reserved for a command that actually failed.

Credentials are always redacted, and there is no way to turn that off:

- an API token keeps only its last four characters
- passwords, cookies and anything else under a credential-shaped field name are replaced entirely
- the query string of a presigned storage URL is dropped, since that is where the signature lives
- a body over 4 KiB is redacted first and truncated afterwards

### Checking the setup

`cysu debug info` reports what the binary is configured to do. It runs offline and needs neither a token nor an encryption key, so it still answers when nothing else works:

```
cysu debug info

{
  "ok": false,
  "version": "0.1.7",
  "platform": "linux/amd64",
  "go": "go1.25.7",
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

`cysu debug check` reports the same and then makes one request to the backend, which distinguishes a backend that cannot be reached from one that refused the token:

```
cysu debug check

{
  "ok": false,
  ...
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

Finding a problem is still a successful run. Both commands exit 0 and list what they found under `problems`, with `ok` set to false. The `error` key appears only when the command itself could not run.
