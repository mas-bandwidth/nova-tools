# Antigravity fleet authentication capability

## Supported consumer account path

The documented Antigravity CLI account path is native authentication on each
target. `agy` retrieves a saved session from the operating system keyring. Over
SSH it presents an authorization URL; the operator approves in a browser and
returns the resulting code to that CLI process. This allows browser approval
away from the target, but it does not establish one portable grant for a fleet.
[Installation and auth](https://antigravity.google/docs/cli/install).

The installation and reference documentation do not specify consumer session
export/import, a sealed subscription-token environment variable, cross-host
refresh semantics, or a supported multi-host token-sharing contract. The
capability is therefore **per-target native OAuth; portable consumer grant
unestablished**. This is an evidence boundary, not a claim that such a provider
feature cannot exist. Credential-cache copying and keychain extraction do not
fill that boundary.
[CLI reference](https://antigravity.google/docs/cli/reference/).

## Runtime and allowance evidence

Headless `agy` uses previously cached authentication; a non-interactive process
without it refuses with an authentication error. A successful initialization
does not prove that the target's runtime user can subsequently access its
keyring. Locked or unavailable keyring services are a documented failure mode.
[Headless mode](https://antigravity.google/docs/cli/headless),
[Troubleshooting](https://antigravity.google/docs/cli/troubleshooting/).

Account authentication, plan entitlement, remaining quota, and control of a
running harness are separate facts. Google AI plans provide baseline quota,
including CLI use; paid plans can also consume purchased AI credits beyond that
quota. Included-allowance-only operation requires the provider's AI Credit
Overages setting to be `Never`, with quota exhaustion held until refresh.
Authentication alone cannot certify that spending policy.
[Plans](https://antigravity.google/docs/plans/).

The Gemini API-key option skips the Google account session and sends model
requests to the Gemini API. It is a distinct provider path, not proof of consumer
subscription use. Enterprise ADC, business licenses, and Cloud project billing
are also distinct from the consumer plan path.
[Installation and auth](https://antigravity.google/docs/cli/install),
[Enterprise](https://antigravity.google/docs/enterprise).

## Existing friend adapter boundary

`internal/friend/adapter_antigravity.go` delivers messages through the desktop
language server's AgentAPI mailbox. It does not launch the native `agy` CLI.
CLI OAuth documentation does not prove desktop installation, desktop sign-in,
live conversation control, or receipt of a friend message. Provisioning must
identify the selected surface before it declares readiness.

## Central provisioning contract

A central Go provisioner can plan a target's native executable, private runtime
identity, and required approval without reading authentication stores. It must
report this provider as requiring target-specific native authorization until a
supported portable grant is established. A future remote approval bridge needs
a documented native login invocation and completion signal, bounded execution,
and a private channel for the authorization code; codes and auth URLs do not
belong in public receipts or ordinary logs.

The logical account profile remains independent of machine assignment. A move
selects the same profile but creates or verifies native authorization for the
new target runtime user; it never treats an account-profile name as a portable
credential. The supported remote design starts an interactive `agy` process over
SSH on the selected target and routes browser approval back to that exact
process. It remains a design pending a native completion/status contract and
runtime verification. Provider refresh-token lifetime, renewal behavior,
revocation scope, simultaneous-host allowance, and a machine-readable auth-only
status command are unestablished by the cited docs. No Gemini API fallback is
selected when account authentication is unavailable.

Running friends through this path requires a separate CLI harness integration.
The existing desktop adapter cannot inherit CLI readiness. The CLI integration
must define its own native launch, output/receipt, cancellation, and runtime
identity contracts before an authenticated CLI target becomes a runnable friend.
The documented CLI transports text prompts as `user` NDJSON messages and emits
`init`, `step_update`, and per-turn `result` events. It resumes a specific
conversation by ID. Streaming sessions reject `control_request` and
`control_response`; CLI slash commands such as `/usage` cannot run in that stream.
Thus text-turn transport exists, while permission interaction, authentication
inspection, and quota inspection need separate supported contracts.
[Headless mode](https://antigravity.google/docs/cli/headless).

This capability note does not implement remote login, declare any target ready,
or authorize a provider turn. No provider calls or credential inspection are
needed to render the capability. Readiness remains gated on the selected
installed surface, native authentication in its runtime context, included-only
spending policy, and a separately authorized control proof.
