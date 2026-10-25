# Cyber Supplies CLI

This is the CLI the agent and user use for interacting with our backend.

## Installing


Via the repo:

```
git clone https://github.com/cyber-supplies/cysu-cli.git
cd cysu-cli
cat install.sh | sh
```

Through the API:

```
curl -fsSL https://cysu-gateway-670100724771.europe-west1.run.app/install.sh | sh
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
cysu projects list --token sk-xxxx
```
