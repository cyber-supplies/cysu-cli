# Cyber Supplies CLI

This is the CLI the agent and user use for interacting with our backend.

## Installing

On Linux or macOS, run the installer. Node.js (npm and npx) and git are required:

```sh
curl -fsSL https://cysu-gateway-670100724771.europe-west1.run.app/install.sh | sh
```

On Windows PowerShell, install the CLI with npm and the agent skill from a local checkout:

```powershell
npm install -g @cyber-supplies/cysu
git clone https://github.com/cyber-supplies/cysu-cli.git
npx -y skills add .\cysu-cli -g
```

Set `CYSU_TOKEN`, run `cysu key init`, then use `cysu debug check` to check the token and gateway. `cysu proxy install` is supported only on Linux.

## Build From Source

```
go vet ./... 2>&1 && go build -o cysu ./cmd/cysu 2>&1 && echo "BUILD OK"
./cysu help
```

## Releasing

The repository secret `NPM_TOKEN` must be a granular npm token with permission to publish to the `@cyber-supplies` scope and bypass 2FA. A new version tag builds Linux, macOS, and Windows binaries for x64 and arm64, checks the generated npm packages on native runners, and publishes the platform packages before `@cyber-supplies/cysu`. npm will not overwrite an existing version.

```sh
git checkout main && git pull
git tag -a v0.5.0 -m "v0.5.0"
git push origin v0.5.0
gh run watch --exit-status
```

## Environment Variables

`CYSU_TOKEN` - Supplies the API token.

`CYSU_PROVIDER` - The default provider: `AGENTS`, `CLAUDE`, or `CODEX`.

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

Key:
  cysu key init [--force]
  cysu key path
  cysu key verify
```

## Authentication

The recommended way to authenticate is to set an environment variable:

```
export CYSU_TOKEN=sk-xxxx
```

It is also possible to pass the token directly via a CLI flag:

```
cysu debug check --token sk-xxxx
```
