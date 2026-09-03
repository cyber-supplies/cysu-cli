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
cysu help


cysu findings list --project

cysu findings get --project --finding

cysu findings create --project --title --desc

cysu findings delete --project --finding



cysu projects list

cysu projects get --project

cysu projects create --name

cysu projects delete --project



cysu accounts list --project

cysu accounts get --project --account

cysu accounts create --project --username --password

cysu accounts delete --project --account

cysu accounts get-auth --project --acount


‌cysu endpoints list --project

cysu endpoints search --project --query
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