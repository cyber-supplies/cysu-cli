# Engine provider authentication

Engine commands accept `--engine` and `--token`, or `CYSU_ENGINE_URL` and
`CYSU_TOKEN`. HTTPS certificates are verified and credential requests refuse
redirects. Provision the engine token on its host with `install.sh --token`;
the CLI never enrolls or rotates it through HTTP.

## Sign in

```sh
cysu engine auth --provider codex
cysu engine auth --provider claude
cysu engine auth --provider openrouter --model provider/model
```

Codex is the default when `--provider` is omitted. This flag chooses which
provider's credentials to configure. **The execution provider is selected in
the workflow**, not by the auth command.

Codex returns a browser URL and device code. Claude returns a URL; finish
its native subscription sign-in in the browser, then submit the complete
one-time code, including any `#state` suffix:

```sh
cysu engine auth-code --id LOGIN_ID
```

OpenRouter keys and Claude codes use hidden terminal prompts. For automation,
pipe the value to the same command with `--stdin`. Submitted credentials are
redacted from debug and error output.

Initial OpenRouter configuration requires both a key and a namespaced model
ID. Later key updates can omit `--model` to retain the saved default. To
update only the credential's default model:

```sh
cysu engine auth --provider openrouter --model provider/new-model --model-only
```

The existing `auth --passphrase <passphrase>` flag remains available to set
the Cyber Supplies encryption passphrase during authentication. Omitting it
keeps the engine's current passphrase. No engine token is sent in a settings
body.

## Login state and saved credentials

```sh
cysu engine auth-status --provider codex
cysu engine auth-status --provider claude
cysu engine auth-status --provider openrouter
cysu engine login-status --provider claude
cysu engine cancel-auth --provider claude
cysu engine forget-auth --provider claude
```

`auth-status` inspects saved credentials. `login-status` and `cancel-auth`
apply to an ongoing Codex or Claude browser login. `forget-auth` removes the
selected provider's saved credentials. Forgetting Claude credentials returns
409 while a run owns its volume.

A slow login startup or code exchange returns its current state after
30 seconds; poll `login-status` to continue checking it. Claude login attempts
expire after 15 minutes. The CLI handles success, failure, cancellation and
expiry without starting another attempt automatically.

## Workflow provider

Set the optional provider in the workflow's `settings.yaml`, then use the
existing `save-workflow` command:

```yaml
name: example
request:
  provider: claude
  stages:
    - profile: your-profile
```

`request.provider` accepts `codex`, `claude`, or `openrouter`. Omit it for
saved-credential selection in order **Codex → Claude → OpenRouter**. Do not
use `auto`, an empty string, null, or a stage-level provider. An explicit
provider without saved credentials fails; execution errors never switch
providers.
