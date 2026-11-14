# Engine v3 migration validation

Validated on 2026-11-14 against engine `v3.0.0` using the CLI migration
branch build. CLI npm publication is separate from this branch.

## Automated checks

- `go vet ./...` and `go test -race ./...` pass.
- 42 passing test cases including subtests; 28 cover engine v3.
- Cross-compilation succeeds for Linux, macOS and Windows, on amd64 and arm64.
- Regressions cover installer-only provisioning, wrong tokens/rate limits,
  command dispatch, independent passphrases, provider authentication and
  credential lifecycle, code/state preservation, secret redaction in valid
  and malformed errors, provider validation/Auto removal, native Claude
  transcript paging/tool association/errors/final-answer deduplication,
  Codex transcript coalescing, trusted/untrusted IP TLS, refused redirects,
  archive integrity/failure cleanup, schedule patches and feed draining.

## Live GCP verification

`dojo.cybersupplies.app` serves engine `3.0.0`. The new VM is
`cysu-engine-v3-dojo` in `europe-west1-b`, with a reserved IP `34.78.15.57`.
The release installer provisioned it with `--ip` and `--token`; Let's Encrypt
issued a publicly trusted IP certificate. The engine listens on loopback.
The VM has no service account; SSH uses IAP.

The existing hostname/IP and its Caddy certificate remain on the old VM.
Caddy now proxies to `https://34.78.15.57` with normal upstream certificate
verification. The old engine container/data remain available for rollback.
**The old VM is still the hostname's proxy and must not be deleted.**

Settings, encryption passphrase, Codex subscription credentials, two workflows,
27 historical runs and four schedules were copied. Copied schedules stayed
paused until cutover; the old schedules were then paused before the new ones
were resumed. All four are enabled only on v3. Temporary test workflows,
profiles, secrets, schedules and runs were removed after verification.

The migrated CLI passed live checks for:

- Settings/passphrase updates; saved credential status; OpenRouter key/model
  updates; secret create/list/delete with debug redaction.
- Native Codex and Claude login URLs, login status and cancellation.
- Workflow save/read, explicit provider selection and Auto omission; plan
  resolution; missing explicit Claude credentials failing without fallback.
- Schedule create/read/edit/pause/resume/delete and migration of existing schedules.
- Successful Codex subscription and OpenRouter executions, run status,
  logs, transcript export and server archive download.
- A fresh successful Codex rerun through the final hostname, `watch --follow`
  draining its final page, cancellation, and server run deletion.
- TLS verification, unauthenticated 401 and browser-Origin 403 through both
  the new IP and the final hostname.

The original OpenRouter model `x-ai/grok-4.1-fast` returned a retirement 404.
The saved default was updated to the provider's recommended replacement,
[`x-ai/grok-4.3`](https://openrouter.ai/x-ai/grok-4.3). That model then returned
402 because the account could not reserve the requested maximum output
allowance. A temporary workflow override to `openai/gpt-4.1-nano` completed
successfully with the same key and Codex harness. Failed OpenRouter runs
remained on OpenRouter; no credential-provider failover occurred. Codex
subscription remains the default selected provider.

Claude's completed browser sign-in and subscription inference require the
account owner's interaction and were not exercised live. One-time code
submission and native event handling are covered by fixtures. Daybreak status
was read; the billable Daybreak check was not run during this deployment.

## Rollback

The old VM holds `/root/cysu-engine-pre-v3-20261114.tar.gz` and
`/etc/caddy/Caddyfile.pre-v3-20261114`. To roll back, first pause schedules on
v3 and let active runs finish. Preserve any data/credential changes made since
cutover, restore the old Caddyfile, reload Caddy, then resume the old schedules.
Do not enable both schedulers at once. The new engine is also reachable by its
HTTPS IP for diagnosis while the hostname points elsewhere.
