# DeepSeek Harness desktop wake adapter

`pkg/dshdesktop` makes one bounded HTTP attempt to deliver a notice into a
running DeepSeek Harness desktop session, or to start a turn in one when idle.
It does not resume a CLI process, start another app server, retry an ambiguous
write, keep a delivery ledger, or claim that the model answered.

The caller supplies the loopback gateway URL, a session ID, the browser-session
cookie, an event ID, and one of the package's two closed local notifications:
`BusNotice` or `ChallengeNotice`. Arbitrary notification text is refused before
the request is built, so the external event body cannot become native turn text
through this API. `ChallengeNotice` is delivered with `mode: "steer"`, which
interrupts the running turn; `BusNotice` is delivered as a followup, which
queues and starts a turn when the session is idle. The adapter appends only the
separately validated event ID to the notice.

The delivery is one `session.prompt` Typert Remote call on the desktop's loopback
gateway: an HTTP POST to `/api/session/prompt` with a client-request envelope.
A successful result is accepted only when the response is a `server-response`
whose `result.ok` is true and `result.value.accepted` is true. A non-200 status
is a certain rejection; a response that is not the exact shape is malformed and
therefore uncertain after the request was sent.

The concrete exchange is capped at 15 seconds. The gateway must be an HTTP
loopback URL, the session ID and event ID are bounded and restricted to letters,
digits, hyphen and underscore, and the cookie must carry no control characters.
The event body itself is never placed in the prompt: the saving client writes it
as JSON to a private event directory, and the notice names only the event ID.

`ErrPreSend` means the request was definitely not sent. `ErrOutcomeUnknown` means
a send began but a matched response was not obtained; the caller must not
blindly retry. A matched rejection is certain and is returned separately from
both classes. Durable delivery and model-level acknowledgment remain the
caller's responsibility. The bus is event truth; the desktop's acceptance of the
prompt is native turn truth.

## Recipient-owned nova-hook client

Run the recipient client with `--dsh-gateway`, `--dsh-session`, `--dsh-cookie`
and `--dsh-events-dir` together, or use `NOVA_HOOK_DSH_GATEWAY`,
`NOVA_HOOK_DSH_SESSION`, `NOVA_HOOK_DSH_COOKIE` and `NOVA_HOOK_DSH_EVENTS_DIR`.
The event directory must exist, be canonical without symlink ancestors, and be
private to its owner. Before arming, the client validates configuration and
holds the same exclusive process lock the Codex route uses. `--once` is refused;
`--dry-run` does not contact the gateway, lock the receiver or write saved
events. Codex delivery and dsh delivery are exclusive: configure one route.

A challenge is steered into the running turn; an ordinary bus message is queued
as a followup. The saved envelope names the identity, the Redis address, and the
two model instructions: prove a current challenge with
`nova-hook prove --as <identity> --nonce <nonce>`, and answer an ordinary
message with `nova-bus send --re <id>`. A native accepted turn is neither proof
nor acknowledgment; only the model proves or answers on the bus.

## Install its own harness-side setup

The route is installed by the same verb, with no new tool: the `--dsh-*` flags on
`nova-hook hook` carry the harness-side setup (gateway, session, cookie and
event directory) into the running client. The cookie is the desktop's
browser-session credential, obtained through the launch-token exchange described
in the harness's connection service; it is supplied to the client at launch and
never written by the client.
