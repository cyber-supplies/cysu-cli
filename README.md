# Cyber Supplies CLI

This is the CLI the agent and user will use for interacting with our backend.

## Installation

```
git clone https://github.com/jakdevreal/cysu-cli.git
cd cysu-cli
go vet ./... 2>&1 && go build -o cysu . 2>&1 && echo "BUILD OK"
./cysu help
```

## Usage

```
cysu - CyberSupplies CLI

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

Key:
  cysu key init    [--force]
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