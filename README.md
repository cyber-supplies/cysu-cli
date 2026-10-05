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

`CYSU_PROFILE` - The profile to use globally.

## Usage

```
cysu - CyberSupplies CLI

Usage:
  cysu <resource> <action> [flags]

Storage:
  cysu storage list      [--project <id>] [--dir <dir>]
  cysu storage upload    [--project <id>] --file <local> --path <path>
  cysu storage download  [--project <id>] --path <path> --output <local>
  cysu storage read      [--project <id>] --path <path>

JavaScript:
  cysu javascript add       --url <url> [--url <url>...]
  cysu javascript list      [--domain <domain>]
  cysu javascript download  --domain <domain>
  cysu javascript clear

Skills:
  cysu skills list [--profile <profile>]
  cysu skills create --file <file> [--profile <profile>]
  cysu skills replace --skill <id> --file <file> [--profile <profile>]
  cysu skills remove  --skill <id>
  cysu skills install [--provider <provider>]
  cysu skills uninstall [--provider <provider>]

Agents:
  cysu agents list
  cysu agents create --file <file> [--profile <profile>]
  cysu agents replace --agent <id> [--file <file] [--profile <profile>]
  cysu agents remove  --agent <id>

MCP:
  cysu mcp list
  cysu mcp create --type <type>
  cysu mcp modify --type <type>
  cysu mcp remove  --mcp <id>

Email:
  cysu email accounts
  cysu email list --account <accountsId> 
  cysu email read --account <accountId> --email <emailId>

Profiles:
  cysu profile install [--profile <profile>] [--provider <provider>] --scope <global|project>
  cysu profile uninstall [--provider <provider>] --scope <global|project>

Out-of-Bounds:
  cysu oob create
  cysu oob pull-hits

Key (encryption):
  cysu key init    [--force]
  cysu key path
  cysu key verify

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
