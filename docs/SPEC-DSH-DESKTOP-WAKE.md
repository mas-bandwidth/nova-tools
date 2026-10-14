# DeepSeek Harness desktop wake adapter

`pkg/dshdesktop` delivers a closed notice into a DeepSeek Harness desktop
session through the harness's own hook bridge, not through the gateway. It does
not call the HTTP gateway, hold a cookie, resume a CLI process, retry an
ambiguous write, keep a delivery ledger, or claim that the model answered.

The caller supplies a private event directory, the path of the harness-side hook
config, an event ID, and one of the package's two closed local notifications:
`BusNotice` or `ChallengeNotice`. Arbitrary notification text is refused before
any write, so the external event body cannot become native turn text through
this API. The adapter appends only the separately validated event ID to the
notice, and the notice is coalesced: at most one pending notice exists, and
messages arriving while one is pending fold into "N messages available; read the
bus".

## The route

Install writes a hook config in the Claude Code hook shape. Its `PostToolUse`
command hook surfaces the pending notice after every tool call, so a notice
reaches a running turn; its `Stop` command hook forces another step when a
notice is still pending at the turn boundary, so an idle session keeps working
until the queue drains. The pending notice is one JSON file in the event
directory; the hook reads it and the harness attaches its text as context. The
external event body is never in that file: it is saved as separate event JSON,
named only by its validated event ID.

## Recipient-owned nova-hook client

Run the recipient client with `--harness deepseek` (alias `dsh`),
`--dsh-events-dir` and `--dsh-hooks-file` together, or use `NOVA_HOOK_HARNESS`,
`NOVA_HOOK_DSH_EVENTS_DIR` and `NOVA_HOOK_DSH_HOOKS_FILE`. The event directory
must exist, be canonical without symlink ancestors, and be private to its owner.
Before arming, the client validates configuration and writes the hook config;
`--once` is refused; `--dry-run` does not write the hook config or saved events.
Codex delivery and dsh delivery are exclusive: configure one route.

A challenge uses the `ChallengeNotice`; an ordinary bus message the `BusNotice`.
The saved event envelope names the identity, the Redis address, and the two model
instructions: prove a current challenge with `nova-hook prove --as <identity>
--nonce <nonce>`, and answer an ordinary message with `nova-bus send --re <id>`.
A surfaced notice is neither proof nor acknowledgment; only the model proves or
answers on the bus.

`ErrPreSend` means the pending notice was not written. The route shares the one
delivery error taxonomy with the Codex route (`ErrRejected`, `ErrOutcomeUnknown`)
but a file write has no matched rejection and no ambiguous outcome.

## Install its own harness-side setup

The route is installed by the same verb, with no new tool: `--harness deepseek`
on `nova-hook hook` carries the harness-side setup (the event directory and the
hook config path) into the running client, which writes the hook config before
arming. No gateway and no cookie: the harness's own bridge reads the pending
notice.
