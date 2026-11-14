# Engine v3

Build this branch with `go build -o cysu ./cmd/cysu`. Every remote engine
command accepts `--engine` and `--token`, or `CYSU_ENGINE_URL` and
`CYSU_TOKEN`. HTTPS certificates are verified, including IP certificates.
The CLI refuses redirects when sending engine credentials.

## Installation and authentication

Provision the engine **on its VPS**, using the single
[v3 installer](https://github.com/cyber-supplies/cysu-engine/releases/tag/v3.0.0):

```sh
sudo sh install.sh --ip PUBLIC_IPV4 --token usr_YOUR_TOKEN
export CYSU_ENGINE_URL=https://PUBLIC_IPV4
export CYSU_TOKEN=usr_YOUR_TOKEN
cysu engine set-passphrase
cysu engine auth # Codex subscription device login
```

The installer requires `--token`. The CLI never enrolls or rotates a token
through HTTP. An unprovisioned engine returns an installer instruction;
wrong tokens and rate limits retain the engine's error.

`set-passphrase` prompts without echoing. Use your existing Cyber Supplies
encryption passphrase. For automation, pipe it into `set-passphrase --stdin`.
The old `auth --passphrase ...` remains accepted, but the separate command
avoids putting the passphrase in shell history and process arguments.

Provider authentication does not otherwise change the encryption passphrase:

```sh
cysu engine auth --provider claude
# Open the returned URL and complete Claude's own subscription sign-in.
cysu engine auth-code --id LOGIN_ID
# Paste the complete one-time code, including any #state suffix, at the hidden prompt.
cysu engine login-status --provider claude

cysu engine auth --provider openrouter --model provider/model
# Enter the API key at the hidden prompt; --stdin also works.
cysu engine auth --provider openrouter --model provider/new-model --model-only
```

An OpenRouter key update may omit `--model` to keep the saved model. Initial
configuration needs both. `--model-only` never sends a key or a masked value.

`auth-status --provider ...` inspects saved credentials. Codex saved status
comes from `llm.codex` in `/api/settings`, independently of the active LLM
mode; the other providers have dedicated auth endpoints.
`login-status` inspects an ongoing Codex/Claude login. `cancel-auth` cancels
that attempt; `forget-auth` removes saved credentials. Both accept
`--provider`. Claude can refuse forgetting with 409 while a run owns its
volume. The CLI preserves that error.

Starting a browser login returns JSON once the URL is ready. Codex also
returns a device code; Claude does not. Slow startup/exchange returns the
current state after 30 seconds: poll `login-status` rather than starting a
new attempt. Claude attempts expire after 15 minutes. No provider is selected
in response to another provider's execution failure.

## Workflows and transcripts

```yaml
name: example
request:
  provider: claude
  globalProfile: ""
  stages:
    - profile: your-profile
```

`request.provider` accepts `codex`, `claude`, or `openrouter`. Omit it for
saved-credential selection in order **Codex → Claude → OpenRouter**. Provider
is workflow-wide; a stage cannot override it. A missing explicitly selected
credential fails instead of choosing another provider. The legacy direct
OpenAI API-key mode remains supported by the Codex harness.

`set-provider --workflow ID --provider auto` removes the field on the server
while preserving the rest of its request. Run `get-workflow` afterward to
refresh an existing local checkout before editing/saving it. Do not store
`provider: auto`, an empty string, or null in YAML.

`plan --workflow ID --objective TEXT` previews the resolved provider/stages.
`run-status --run ID` reads the compact run object; optional fields can be
absent, `trigger` is an object, and failures live in `stage.error`.
`get-run` exports Markdown, includes the run's provider in its JSON result,
and bounds each stage to the event snapshot. Claude text, tool calls/results,
nested agent messages and failures are rendered alongside the unchanged
Codex/OpenRouter event format. The final Claude answer is not repeated.

`watch --run ID` returns one JSON feed response, including a resumable cursor.
`--follow` streams one compact JSON object per line until the feed is drained;
Ctrl-C ends the watcher without cancelling the run. `get-log --run ID --stage 0`
returns the raw container log in a JSON `log` string. Stage indexes start at 0.

`download-run --run ID --output result.tar.gz` streams a finished run archive
to a private file and refuses to overwrite an existing destination. `rerun`
starts a new run from the stored request. `delete-run` deletes a finished run
from the server; `clear-runs` only removes local transcript exports.

## Settings, secrets and schedules

`settings` returns masked settings. `set-settings --file patch.yaml` updates
only supplied fields: `passkey`, `llm`, `globalProfile`, `agentToken`,
`engineLinked`. For example:

```yaml
agentToken: llm
engineLinked: true
globalProfile: ""
```

Prefer `set-passphrase` for passphrases and provider auth commands for API
keys. Legacy direct OpenAI credentials can be configured with
`llm: {mode: openai-api-key, apiKey: ...}` in a private settings file.
Never submit an entire masked settings response. A `token` field is rejected
locally. Protect and remove files containing credentials after use.

`list-secrets`, `set-secret --name NAME` (hidden prompt or `--stdin`), and
`delete-secret --name NAME` manage write-only environment secrets. The
engine validates reserved names. Submitted codes, keys, passphrases, secret
values and bearer tokens are scrubbed from debug/error output.

`daybreak-status` reads the last check. `check-daybreak` explicitly makes
small model requests and allows ten minutes for the response; it never runs
implicitly. Daybreak blue/red requires the Codex harness and is unavailable
through Claude or OpenRouter.

Existing `schedule` and `unschedule` commands remain supported.
`list-schedules`, `get-schedule`, `pause-schedule` and `resume-schedule` expose
their server operations. `update-schedule --schedule ID --file patch.yaml`
accepts a JSON/YAML patch with `name`, `workspaceId`, `objective`, `when`,
`tz`, or `enabled`. Omitted fields are retained. Example:

```yaml
when:
  type: weekly
  days: [mon, thu]
  time: "09:00"
tz: Europe/Dublin
```

The engine validates model, effort, timing, timezone, and schedule compatibility.
Changing timing/timezone can reset an interval schedule's anchor. Pausing
cancels a queued occurrence; resuming skips missed occurrences.
