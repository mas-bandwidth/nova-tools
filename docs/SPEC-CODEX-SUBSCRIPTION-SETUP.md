# Codex subscription setup

## Implemented scope

`nova-friend setup --harness codex --profile-only` uses an isolated private
native home and CODEX_HOME per friend/account/profile on an execution host.
Inspection is read-only. Explicit apply creates missing private directories,
checks `codex login status`, and records public setup ownership metadata.
Native status is read from stderr, bounded and classified without echoing it.
Only the exact native ChatGPT method counts as local subscription authentication;
that receipt proves neither the selected account nor entitlement, allowance,
provider access, a completed turn or presence. Unexpected/API/enterprise status
is refused. Credential values never enter setup input, output or receipts.

The inspected plan prints a sanitized `codex login --device-auth` command.
Setup does not execute authorization or read/import native credential files.
Device code sign-in is beta and must be enabled in the account security settings
or workspace permissions. If unavailable, forward the native browser callback
over SSH as documented; do not substitute a credential snapshot.
The person runs that native command when enrollment is needed, chooses the
intended ChatGPT account in the browser, and reapplies setup afterwards.
Native Codex owns subsequent credential refresh. Re-enrollment uses the same
private profile on the same execution host after a revoked/expired grant.
Do not seal, snapshot or distribute native Codex login files through nova-secrets.

Each logical friend has a stable safe profile ID and a separate display label.
An account grant and its allowance are separate from that identity. Each host's
worker/session instance owns its own native authentication state. Workers can
execute on macOS or Linux; placement does not create a new quota pool or confer
ownership of the primary friend's presence. Different ChatGPT accounts must have
separate private profiles and grants. Local status alone cannot establish account
mapping; account selection is explicit browser consent.

## Central enrollment orchestration requirement

Native device authorization can run on a selected remote execution host through
an authorized SSH session while browser consent happens on the person's computer.
This avoids opening an interactive shell on every box, but still needs a native
authorization grant per enrolled host/account/profile. The worker performs actual
compute on that host. A coordinator must name the selected host/profile, start the
native login there, present its device consent instructions directly to the person,
wait for native completion, then perform this setup status check. It must preserve
each host's refresh ownership and stop for revoked grants. That central SSH/device
orchestrator is not implemented by this local setup planner.

## Supported route decision

`codex login --with-access-token` is the enterprise access-token route; it is not
an importer for ordinary ChatGPT subscription OAuth grants. No API key fallback
is acceptable for subscription setup.

Sign in with ChatGPT (SIWC) also supports plan usage with Codex app-server through
a custom Responses provider and an access token supplied only to the child
process. It requires its own registration, PKCE/OIDC validation and granted
`chatgpt.tokens.use.direct` scope. Its documented one-hour access lifetime and
30-day rotating refresh lifetime do not describe native Codex login credentials.
The SIWC self-hosted transfer procedure gives the destination VM ownership of
refresh; copying one rotating session across a fleet causes refresh races. SIWC
also requires app-server restart and thread resume when its token changes and
has provider/tool limitations. A central broker/provider integration is a distinct
project, not an implicit native login-file distribution mechanism.

Official references:

- [Native Codex authentication](https://learn.chatgpt.com/docs/auth)
- [SIWC app-server](https://developers.openai.com/siwc/token-sharing-open-source/codex-app-server)
- [SIWC self-hosted VMs](https://developers.openai.com/siwc/token-sharing-open-source/self-hosted-vms)
- [SIWC accounts and sessions](https://developers.openai.com/siwc/token-sharing-open-source/profiles-and-sessions)
- [SIWC token lifetimes](https://developers.openai.com/siwc/token-sharing-open-source/token-reference)
- [SIWC preview limitations](https://developers.openai.com/siwc/token-sharing-open-source/preview-limitations)
