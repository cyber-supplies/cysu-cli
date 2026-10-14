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
  cysu storage list [--project <id>] [--dir <dir>]
  cysu storage upload [--project <id>] --file <local> --path <path>
  cysu storage download [--project <id>] --path <path> --output <local>
  cysu storage read [--project <id>] --path <path>

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

## Authentication

The recommended way to authenticate is to set an environment variable:

```
export CYSU_TOKEN=sk-xxxx
```

It is also possible to pass the token directly via a CLI flag:

```
cysu projects list --token sk-xxxx
```
