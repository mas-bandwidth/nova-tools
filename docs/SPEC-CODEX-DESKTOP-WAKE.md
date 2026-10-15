# Codex desktop wake adapter

`pkg/codexdesktop` makes one bounded attempt to start a turn in an existing
Codex desktop chat. It does not resume a CLI process, start another app server,
retry an ambiguous write, keep a delivery ledger, or claim that the model
answered.

The caller supplies a socket path, thread ID, event ID, and one of the package's
two closed local notifications: `BusNotice` or `ChallengeNotice`. Arbitrary
notification text is refused before connecting, so the external event body
cannot become native turn text through this API. `BusNotice` is the legacy closed notice. The hook integration uses
`DeliverSaved` and a concrete private JSON file path, as described below.
`ChallengeNotice` directs it to inspect the hook challenge and answer through
the configured nova-hook identity. The adapter appends only the separately
validated event ID. The turn sets `inheritThreadSettings` so the existing chat
keeps its settings.

The exchange is initialize version 0, owner discovery version 1, then one
follower start-turn request version 2 addressed to the discovered owner. A
successful result is accepted only when its request ID and method match, the
reported owner matches the target, and `result.result.turn.id` is nonempty. The
desktop's error responses carry no method, version or owner:
`{"type":"response","requestId":…,"resultType":"error","error":"<code>"}`
(ChatGPT.app 26.1007.30912, `Resources/app.asar`, `.vite/build/src-BJv5Mk7n.js`:
the IPC router and `IpcClient.handleRequest`), so an error is matched by request
ID alone and its code is kept in the returned error. Only the codes written
before any handler runs (`no-client-found`, `client-not-found`,
`client-cannot-handle-request`, `request-version-mismatch`,
`no-handler-for-request`) are a certain rejection. A handler that threw,
`request-timeout`, `client-disconnected`, `server-closed`, an error with no
code, and a missing or unknown result type are uncertain after the follower
write.
The returned ID proves only that the desktop accepted that turn.

The concrete exchange is capped at 15 seconds. Context cancellation observed
between frames stops sooner; cancellation during a blocked native read may wait
for that fixed deadline. Outbound frames are at most 64 KiB
and inbound frames at most 1 MiB. The socket and its parent must be owned by the
current Unix user, must not be symlinks or group/world writable, and the endpoint
must be a Unix socket. Each call holds only its current frame, so 100 times more
traffic adds calls, not retained state.

`ErrPreSend` means the follower mutation frame was definitely not written.
`ErrOutcomeUnknown` means its write began but a matched response was not
obtained; the caller must not blindly retry. In nova-hook the notice Core keeps
such a notice pending as if accepted and keeps the route and the hook record as
they are; only its rule 3a pushes the same notice again
(`tla/HarnessNoticeUnknown.tla`). A matched native rejection is
certain and is returned separately from both classes. Durable delivery and
model-level acknowledgment remain the caller's responsibility. The bus is event
truth; the desktop turn ID is native turn truth. Once that ID is matched, a
later socket-close error is cleanup only: it is deliberately ignored because it
cannot revoke acceptance and must not invite a retry.

`tla/CodexDesktopWake.tla` models the boundary. Its invariants require at most
one mutation attempt, a zero-write pre-send failure, uncertainty only after a
write, accepted-turn evidence from the app, local-only prompt text, and inherited
thread settings. A weakly fair abstract timeout requires every call eventually
to reach a terminal outcome; this is qualitative liveness, not a proof of the
concrete 15-second wall bound. Three reversed configurations witness blind
retry, external body injection, and dropped settings.

## Recipient-owned nova-hook client

This source is an integration candidate, not a verified production cutover.
Keep the interim receiver until real busy and idle model proof and ACK checks
pass. `CodexDesktopWake` models one native call only. `CodexHookRecovery`
separately models startup capture, per-ID answers, existing server challenge,
possible recovery duplicates, and the absence of blind retries in one process.
Neither model proves actual model execution, spool filesystem security or a
concrete wall-clock bound. Their Linux measurements are separate gates.

Run the existing recipient client with `--codex-socket`, `--codex-thread` and
`--codex-events-dir` together, or use `NOVA_HOOK_CODEX_SOCKET`,
`NOVA_HOOK_CODEX_THREAD` and `NOVA_HOOK_CODEX_EVENTS_DIR`. The event directory
must exist, be canonical without symlink ancestors, and be private to its owner
(normally 0700). Before arming, the client validates configuration, probes the
native owner and holds the existing `pkg/filelock` exclusive process lock in
that directory. This lock prevents concurrent live clients sharing a receiver;
it is process coordination, not a delivery record. It cannot adjudicate a
native turn whose former process died. `--once` is refused; `--dry-run` does not
contact native IPC, lock the receiver or write saved events. Other harnesses
retain their monitor behavior.

The startup cursor is captured **before** HookAs or lease renewal. Startup
recovery reads the recipient stream from 24 hours before Redis TIME through
that fixed cursor, oldest first. Optional `--codex-since` (or
`NOVA_HOOK_CODEX_SINCE`) narrows ordinary recovery to a fixed UTC RFC3339 timestamp
in whole seconds, inclusive, for example `2026-10-14T20:40:00Z`. The launch
configuration persists this exact value across restarts; substituting the
current time on restart loses work and is forbidden. A future boundary is
refused against Redis TIME. Earlier ordinary messages are declared archived
history, remain retained, and work still needed must be resent. The CLI and
saved initial envelope disclose this boundary. It creates no answers or delivery
claims. A standing current server challenge remains subject to hash/epoch
validation and is recovered from its actual event even before this boundary;
that exceptional search uses the existing fixed history and byte budgets. It reads at most 4096 entries and 1 MiB of raw
message data, then uses one bounded HMGET of existing `bus.AnswersOf(identity)`
fields. Every unanswered in-window message is selected independently; a reply
to a newer message never hides an earlier unanswered one. An existing server
challenge is selected from its actual retained bus message only when its nonce
hash matches the active hook challenge. Only a current hash-and-epoch match is recognized as protocol. A stale
challenge or an external subject lookalike remains ordinary external data,
filtered by its own model answer; it must never authorize proving a stale nonce.
An obsolete protocol event is excluded only when its complete body exactly
matches the existing server challenge shape for this recipient and validated
nonce, and a single bounded sender lookup identifies a coordinator/comms role.
Altered bodies and unknown senders remain external messages. If HookAs changes
the epoch, only the tagged pre-takeover current challenge is removed; ordinary
messages remain in recovery alongside the new bootstrap. A matching challenge missing from
the captured history is an explicit incomplete-recovery failure. A challenge
arriving after cursor capture is delivered by the live stream.

One first native turn carries the ordered recovery batch and any newly created
bootstrap challenge. Its saved JSON explicitly permits duplicates: it is a new
startup recovery operation, not a retry of the prior native request. Every
process makes one attempt per event/initial batch and stops on uncertain native
failure. No live native call is retried. Existing saved files are immutable and
never deleted or rewritten. Model answers suppress corresponding bus events;
saved-file absence is never proof that an event was not previously delivered.
After startup the client streams strictly past the captured boundary, so a
message between cursor capture and lease publication is covered.

There is no stream creation/trim history in the Store interface. An empty stream also cannot prove prior coverage (oldest is `none`). If the oldest
retained entry is inside the 24-hour window, coverage before its ID is unknown;
a shorter fresh stream is not asserted to have been trimmed. One CLI diagnostic
and the recovery envelope disclose this uncertainty and tell the model to notify
the coordinator once with the oldest retained ID. Messages older than the fixed
window are outside automatic startup coverage. These limits are operational
cutover gates, not claims of complete historical delivery. Scan, message or byte
budget overflow stops startup visibly rather than advancing a cursor over
unexamined data. The selected batch is at most 128 messages and must fit the
64 KiB encoded envelope; incomplete coverage requires explicit reconciliation.

Each event is saved atomically as `<directory>/<validated event ID>.json` with
mode 0600 and NoReplace, at most 64 KiB. The fixed native notice is one compact
line identifying the saved external-data file. Body, subject, nonce and sender
never enter native turn text. Saved local receiver fields supply the validated
identity, resolved Redis address, recorded hook role/epoch and Redis save time,
and fixed proof/ACK guidance. The ordinary message or ordered recovery list is
external data. Those receiver strings identify a route; they do not authorize
execution as a different identity.

Saved files are retained external evidence only. Startup does not enumerate or
interpret them, so old files and obsolete bootstrap epochs cannot block a valid
new hook epoch. It reconstructs recovery only from bus data, per-ID model
answers and the current challenge. The exclusive directory process lock and a
fresh recovery event ID distinguish a deliberate startup batch from a blind
retry. No saved-file absence or former native outcome is treated as delivery
authority. Existing files are never replaced or deleted automatically.

Only the model answers a current challenge using `nova-hook prove --as ...
--redis ... --nonce ...`; bootstrap supplies its nonce directly, while a server
message supplies it as external challenge data. For ordinary messages the model
uses `nova-bus send --as ... --redis ... --to ... --re <message ID>` to answer.
The client never proves or sends an ACK on the model's behalf. Native acceptance
prints `native=accepted` and the turn ID, awaiting the model action.

For an individual live bus event, one bounded HGET before native delivery and
one after an unknown result may reconcile an existing model `--re` answer. A
matched answer suppresses delivery or reconciles the event without retrying.
Without one, the outcome remains unknown: the client revokes bus route proof
under its role and epoch and stops renewal without unhooking or judging model
presence. A later explicit startup recovery may repeat an unanswered message
as disclosed above. The sender and existing bus ask server own deadlines; the
configured one-minute ask deadline without a model answer is failure evidence.
No 10-second watchdog or repeated model notification is added.
