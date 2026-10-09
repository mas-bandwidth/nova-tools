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
Ordinary setup does not execute authorization or read/import native credential files.
Explicit `--codex-device-enroll --apply` performs the native enrollment flow below.
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

## Central native enrollment

Use `setup --codex-device-enroll --enrollment-label LABEL` with the same explicit
private profile selection. The profile ID uses lowercase ASCII letters, digits,
period, underscore and dash; a display/account label is separate. Without apply,
or with dry-run, the tool prints the public plan and performs no enrollment,
SSH connection or profile write. JSON inspection works; JSON apply is refused.

For a remote execution host, add `--target user@host --remote-program /absolute/nova-friend`.
The coordinator invokes the same reviewed Go tool remotely, compares its recorded
build version, and refuses unknown/dirty build versions. This version comparison
is compatibility evidence, not proof that either installed artifact passed review;
installation/release verification supplies that proof separately. SSH uses existing
key authentication, strict known-host checks, and explicitly disables port, agent,
X11 and control-master forwarding and local commands. The wrapper uses the existing
birth-bound native process-group cleanup and a private temporary public PID receipt;
an unverified wrapper group retains its receipt and refuses completion. Each
remote shell argument is independently quoted, and target option injection is refused.
No account grants, authentication files or private tokens cross this protocol.

Explicit apply requires the person's controlling terminal, shows the selected
host/profile/account label, then directs native device consent there without
capturing it in output, JSON, receipts or application logs. The person selects
the intended ChatGPT account in the native browser flow. The tool does not parse
an undocumented challenge format or automate consent. Native CLI diagnostics
remain native and are never read or copied by setup. Direct native login writes
`codex-login.log`; enrollment requests the documented `log_dir` under the private
CODEX_HOME and creates missing real directories with owner-only permissions; existing log
directories remain confined by the owner-only CODEX_HOME parent. It does not
claim that native consent is log-free or alter an enforced administrator policy.

On the selected host, the existing setup locks and birth-bound process receipts
serialize the private profile. Native device login runs before a subsequent exact
ChatGPT status check; a failed/cancelled login or missing subsequent ChatGPT method
cannot certify the public profile. `--enrollment-timeout` is 1-900 seconds and defaults
to 600, a caller deadline rather than a claim about native device-code lifetime.
Native cancellation uses the existing verified group cleanup, and an unverified
remote group holds retries. The SSH coordinator allows an additional 120 seconds
for setup/status/cleanup. Lost SSH connectivity never reports enrollment complete;
wait for the target's bounded native group to exit and reapply the same selection.

Browser account identity, account allowance and provider/model entitlement remain
unverified even after native status succeeds. Each enrolled execution host owns its
refresh state, and actual worker compute remains on that host. Native Codex refreshes
during use; after revocation or expiry, explicitly rerun enrollment on the same owned
profile. Enrollment neither creates presence nor starts work or a provider turn.

Example inspection (no native commands or credentials):

```sh
nova-friend setup --as worker --harness codex --dir /workers/work --home /workers/home --config-dir /workers/account-config --profile account-extra --program /usr/local/bin/codex --deny-self /coordinator --profile-only --codex-device-enroll --enrollment-label 'account label' --target user@worker-host --remote-program /usr/local/bin/nova-friend --apply --dry-run
```

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

- [Native log directory configuration](https://learn.chatgpt.com/docs/config-file/config-reference)
