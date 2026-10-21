# Cyber Supplies CLI

This is the CLI the agent and user use for interacting with our backend.

## Installing

```
git clone https://github.com/cyber-supplies/cysu-cli.git
cd cysu-cli
cat install.sh | sh
```

## Build From Source

```
go vet ./... 2>&1 && go build -o cysu ./cmd/cysu 2>&1 && echo "BUILD OK"
./cysu help
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
git tag -a v0.3.{VERSION_NUMBER} -m "v0.3.{VERSION_NUMBER}"
git push origin v0.1.{VERSION_NUMBER}
gh run watch --exit-status
```

The tag is the version. The workflow builds and publishes `@cyber-supplies/cysu` to GitHub Packages, and stamps the tag into the binary so that `cysu help` reports it. A build from source reports `dev`.

## Environment Variables

`CYSU_TOKEN` - Supplies the API token.

`CYSU_PROVIDER` - The default provider for profile installation: `AGENTS`, `CLAUDE`, or `CODEX`.

## Usage

```
cysu - CyberSupplies CLI

Usage:
  cysu <resource> <action> [flags]

Storage:
  cysu storage list [--dir <dir>]
  cysu storage upload --file <local> --path <path>
  cysu storage download --path <path> --output <local>
  cysu storage read --path <path>
  cysu storage delete --path <path>

JavaScript:
  cysu javascript add --url <url> [--url <url>...]
  cysu javascript list [--domain <domain>]
  cysu javascript download --domain <domain>
  cysu javascript clear

Endpoints:
  cysu endpoints list [--domain <domain>] [--path <path>] [--method <method>] [--full]

  cysu endpoints list-scope

  cysu endpoints add-scope --scope <regex>
  cysu endpoints set-scope --scope <regex>

Notes:
  cysu notes list [--domain <regex>] [--path <regex>]

  cysu notes create --domain <domain> [--path <path>] --content <content>
  cysu notes modify --note <note> [--domain <domain>] [--path <path>] [--content <content>]
  cysu notes delete --note <note>

Email:
  cysu email accounts
  cysu email list --account <accountsId> 
  cysu email read --account <accountId> --email <emailId>

Profiles:
  cysu profiles install --scope <global|project> --profile <profile> [--provider <provider>]
  cysu profiles uninstall --scope <global|project> [--provider <provider>] 

  cysu profiles list
  cysu profiles read --profile <profile>
  cysu profiles remove --profile <profile>

  cysu profiles create --profile <profile> [--example]
  cysu profiles edit [--profile <profile>] [--all]
  cysu profiles save [--profile <profile>] [--all]

Accounts:
  cysu accounts list [--location <location>]
  cysu accounts get --account <account>
  cysu accounts set-cookie --account <account> --cookie <cookie>

  cysu accounts create --username <user> --password <pass> --location <location>
  cysu accounts modify --account <account> [--username <user>] [--password <pass>] [--location <location>]
  cysu accounts remove --account <account>

Out-of-Bounds:
  cysu oob create
  cysu oob pull-hits

Key:
  cysu key init [--force]
  cysu key path
  cysu key verify
```

Account passwords and cookies are encrypted locally with the CLI master key before upload and
decrypted only when an account is returned. The backend stores opaque ciphertext. `accounts list`
never returns credentials; its optional location value is a PostgreSQL regular expression. Create,
modify, and remove require a USER token, while USER and LLM tokens may list, get, and set cookies.

Notes are short pieces of text (at most 500 characters) that an agent keeps about a domain and,
optionally, a path on it, for example `cysu notes create --domain example.com --path /login
--content "Uses JWT"`. A domain and path can have any number of notes. Domains are stored in
lowercase; a note created without `--path` applies to the whole domain and has an empty path.
Content is encrypted at rest by the storage service. USER and LLM tokens can use every notes command.

`notes list` without flags returns all of your notes, oldest first. `--domain` (case-insensitive)
and `--path` (case-sensitive) are PostgreSQL regular expressions that match anywhere unless
anchored, so `--domain example.com` also matches `api.example.com`. Use
`--domain '^example\.com$'` for an exact domain and `--path '^$'` for notes without a path. Quote
patterns so the shell leaves them unchanged.

`notes modify` changes only the fields you pass and keeps the rest. It cannot clear a path; delete
the note and create it again instead. To find the note it lists all of your notes, since the API
has no single-note lookup. `notes delete` prints the deleted note's id.

## Authentication

The recommended way to authenticate is to set an environment variable:

```
export CYSU_TOKEN=sk-xxxx
```

It is also possible to pass the token directly via a CLI flag:

```
cysu projects list --token sk-xxxx
```
