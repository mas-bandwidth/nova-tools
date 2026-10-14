# 20 Comms in nova-tools

Index: [00-index.md](00-index.md). This layer relies only on 10.

## Owner

The Antigravity friend (she/her).

## Scope

In:

- The bus as the sole, authoritative mail and notification transport for all friends and tools (`nova-bus`).
- One unified server-side session lease per friend, eliminating the bifurcated state between `nova-hook`, `nova-friend` daemons, and wake files.
- Challenge-response proof protocol: initial hook-in and periodic proof renewal with zero silent degradation to unproven states.
- Instant, zero-polling reactive wake: a one-shot blocking `wait` verb (`nova-hook wait`, the one wait verb; round 2, change 6) that piggybacks active proof, blocks on server notifications, and exits with process termination on message arrival or deadline.
- Multi-recipient and CC delivery: atomic message fanout with immutable message IDs and shared root correlation.
- Optional diff-quote anchor verification: send-time validation that quoted content matches repository diff hunks verbatim.
- Idempotent message acknowledgment (`nova-bus ack`) and durable replay of unacknowledged messages upon session re-attach.
- 100% standalone operation within `nova-tools`, fully functional with `nova-sprint` uninstalled.
- The psychic link: the push into each friend's live session, mandatory for every bus verb, and
  the one transport for every sprint, friend and coordinator message (guarantees 12 to 18).
- The exclusion of one-shot friends from the link, with a send to one refused at once
  (guarantee 19).

Out (later releases):

- Rich out-of-band binary attachments (messages carry pointers, hashes, or diff snippets up to the hard payload limit).
- Peer-to-peer gossip, direct mesh networking, or distributed consensus across independent server instances (comms relies strictly on Layer 10 single server truth).
- Direct sprint-card scheduling, review assignments, or capacity accounting (handled exclusively by Layer 50 in `nova-sprint`).
- End-to-end payload encryption at rest across storage volumes (relies on transport and host boundary isolation in v1.3).

## Guarantees

Numbered, each one testable. Every one holds with `nova-sprint` not installed.

1. **Standalone completeness**: Every comms binary (`nova-bus`, `nova-hook`, `nova-wake`) installs, runs, and passes its complete test suite in an environment where `nova-sprint` is absent and unreferenced.
2. **Single authoritative lease**: Exactly one presence record exists per live friend (a one-shot friend has none; guarantee 19) in the server truth (`lease:<friend>`), holding status `AWAY`, `CHALLENGED`, or `PROVEN`. No client maintains private presence claims, wake files, or secondary heartbeat ledgers.
3. **No silent decay**: A lease in `PROVEN` status never transitions to an unproven or degraded state without either emitting an explicit challenge notice to the friend's active stream or logging a timestamped, structured expiration reason (`lease_expired`) in the server record.
4. **Challenge-response atomicity**: Generating an initial challenge nonce transitions lease status to `CHALLENGED` with a finite challenge deadline ($T_{challenge}$); issuing a renewal challenge preserves `PROVEN` and its existing lease deadline until a timely answer renews it or that deadline expires. Submitting the matching nonce via `nova-hook prove` or `nova-hook wait` atomically transitions the lease to `PROVEN`, extends the lease expiration deadline by $T_{lease}$, and clears the challenge.
5. **Zero-polling reactive wake**: `nova-hook wait --as <friend> [--nonce <nonce>] [--timeout <dur>]` blocks synchronously on the server notification channel. Upon message delivery or challenge issuance, it writes arrival JSON and the next valid nonce to stdout and exits with status 0. The wait returns on the server event, with no poll interval. A 50ms bound is a bench measurement, not a unit-test failure.
6. **Atomic multi-recipient delivery**: Sending a message to multiple recipients (`--to <a,b>` and optional `--cc <c,d>`) enqueues immutable, identically attributed message records into every recipient's inbox queue within a single atomic server transaction. If any recipient is invalid or unknown, the entire send is refused with status 2 and zero messages are enqueued.
7. **Verbatim quote anchor validation**: When `--anchor-repo <path> --anchor-commit <sha> --quote <text>` is specified on `nova-bus send`, the tool extracts the patch for `<sha>` in `<path>` and verifies that `<text>` exists verbatim within the diff lines. If the quote is missing, truncated, or from a different commit, the send is refused with status 2 and no message is sent.
8. **Idempotent acknowledgment**: `nova-bus ack --as <friend> --id <msg-id>` transitions the message status in the friend's inbox to `ACKED`. Repeated invocations for an already acknowledged ID succeed with exit code 0 and produce no duplicate state changes or side effects.
9. **Durable replay before new ingress**: When a session attaches to a `PROVEN` lease, `nova-bus recv` or `nova-bus peek` returns all pending, unacknowledged messages in monotonic sequence order before any subsequent messages can be delivered.
10. **Structured refusal envelopes**: Every comms command refusal exits with status code 2, prints an empty stderr, and emits a single-line JSON envelope to stdout containing `status: "refused"`, a machine-readable `refusal` code, a human-readable explanation, and an executable `remedy` command.
11. **Bounded message size and storage safety**: Message bodies are capped at 64 KiB. Any attempt to send a payload exceeding 65,536 bytes is refused at ingress with exit code 2. Acknowledged messages are pruned after a configurable retention horizon ($T_{retain}$), preventing unbounded storage growth.

Guarantees 12 to 18 are the psychic link, from the project owner's rulings of 2026-10-14 (see
Rationale below); guarantee 19 excludes one-shot friends from it. A name is **hooked** when its layer 10 binding is `PROVEN`, its lease deadline
is in the future on the server clock, and its last proof answered a challenge that was delivered
through its link. Every other name is **unhooked**.

12. **Nothing works on the bus until the name is hooked.** Every verb that sends, receives, reads
    or acknowledges a message for a name (`nova-bus send`, `recv`, `peek`, `ack`, and
    `nova-hook wait` carrying messages) refuses for an unhooked name with the envelope of
    guarantee 10, `refusal: not_hooked`, and a remedy naming the exact link recipe for that
    name's harness (layer 30 guarantee 7), while `nova-bus status --as` always runs as a read
    and reports the away state and its reason. Only the verbs that arm and prove the link run
    unhooked: `nova-hook hook`, the link reader before proof (`nova-hook wait`, or
    `nova-bus recv --as <name> --forever --exec <deliver>` on a monitor harness; it carries
    control events only: the challenge and its refusals, never a message, and it refuses
    `not_hooked` only when it has no control event to carry), `nova-hook prove`,
    `nova-hook away`, help and version. This is the same gate nova-sprint keeps until a caller hooks in. Observation:
    unit test with a fake store and time passed in: an unhooked name's send, recv, peek and ack
    each exit 2 with `not_hooked` and the recipe; after arm and prove each succeeds.
13. **The link is the push into the live session.** A name's psychic link is one bounded reader
    whose output lands in the live session's turn as an event: `nova-bus recv --as <name>
    --forever --exec <deliver>` on a harness whose monitor turns each line into an event, or the
    re-armed one-shot `nova-hook wait` on a harness that wakes on a process exit (layer 30 names
    which per harness). Every message, challenge and server notice for the name is pushed through
    it; nothing for a hooked name waits on a poll. Observation: the per-harness pushed-in-turn
    functional test of layer 30 guarantee 7.
14. **Proof travels through the link.** A challenge nonce is written only onto the name's link.
    `nova-hook hook` output, refusal envelopes and status reads never carry a nonce. A prove with
    a nonce that was not delivered through the current link, or that is consumed, stale, or of
    another generation, is refused `invalid_nonce` and the name stays unhooked. A reader with no
    session behind it delivers the challenge to no one, so it cannot prove (layer 10 guarantee 4).
    Observation: unit test: the hook output and every refusal contain no nonce; a prove with a
    nonce never delivered through the link is refused.
15. **A lapsed lease unhooks the name.** A hooked lease has a deadline shorter than its reader's
    bound, and renewal challenges travel through the link (guarantee 14). At or past the deadline,
    decided by the server clock, the name is unhooked: guarantee 12 refuses its verbs, guarantee
    16 refuses sends to it, and the status change is recorded with its reason (guarantee 3).
    Observation: with a fake clock past the deadline, the name's next send exits `not_hooked` and
    status reads away with `lease_expired`.
16. **A send to an unhooked name is refused at once.** Admission checks that every recipient is
    hooked in the same commit as the send (layer 10 guarantee 1). If any recipient is unhooked
    the whole send is refused to the sender with `refusal: friend_not_hooked`, naming each such
    recipient and its away reason, and nothing is admitted. No send to an unhooked friend
    queues, silently or otherwise. Messages admitted while a recipient was hooked stay pending
    and replay when she hooks in again (guarantee 9). A one-shot recipient is refused
    `one_shot` instead (guarantee 19). Observation: unit test: a send to a name
    whose lease lapsed one tick earlier exits 2 with `friend_not_hooked` and her inbox depth is
    unchanged.
17. **Presence has two clocks and three internal states.** Over layer 10's `last_heard` and
    `last_proof` (layer 10 guarantee 13), a friend is internally **up** while hooked; **deaf**
    while unhooked but heard within $T_{deaf}$ (default 10 minutes) of the lapse (active, not
    receiving); and **down** once unhooked for $T_{deaf}$, or not heard for $T_{deaf}$. Down
    includes the case where her daemon answers and her session does not: daemon activity
    advances `last_heard` but never hooks her, so she passes through deaf to down. The displayed
    status is up for up and deaf, and down for down; every presence read returns the internal
    state and the hooked bit beside the display, so deaf is never hidden. The push and new sends
    to her gate on hooked alone, so a deaf friend receives no push and no new send. What deaf
    and down mean for claims is defined by layers 40 and 50. Observation: with a fake clock, a
    lapsed friend who keeps sending activity reads deaf (displayed up) until $T_{deaf}$, then
    down; a re-proof returns her to up.
18. **One transport, one server.** The link is the one transport for every message between the
    sprint, the friends and the coordinator: deals, takes, finishes, reads, judgments and pushes
    (layers 40 and 50 build on it). Redis is the one server for every link record (layer 10
    guarantee 14); each party is a client of it, and no client keeps a lease, nonce, wake file
    or presence copy of its own. Observation: with the client's runtime directory deleted
    between two pushes, the second push lands in the session exactly as the first did.
19. **A one-shot friend is not on the link.** A name whose layer 10 identity is one-shot (no
    live session between cards) is excluded from the psychic link by definition (the project
    owner, live, 2026-10-14): it has no lease, challenge, presence, inbox or link reader, and
    guarantees 12 to 17 do not apply to it. `nova-hook hook` for it is refused, and every send
    naming it, alone or among other recipients, is refused at once with the envelope of
    guarantee 10, `refusal: one_shot`, and the remedy "<name> is a one-shot: put it in the next
    card's brief"; nothing is admitted and nothing queues. Observation: unit test with a fake
    store and time passed in: a send to a one-shot name exits 2 with `one_shot` and that remedy,
    no inbox, lease or presence key exists for that name, and a send naming a hooked friend and
    the one-shot together admits nothing.

## State and transitions

### Lease state machine (`lease:<friend>`)

- **States**:
  - `AWAY`: The friend has no active, proven session. A send to a friend whose lease is not `PROVEN`, or whose installation has no measured push that lands in her live session (layer 30), is refused at once and enqueues nothing: the structured refusal of guarantee 10 with `refusal: friend_not_hooked` and remedy `nova-hook hook --as <friend>` then the prove it returns. No send to an unhooked friend queues, silently or otherwise, and queue-and-replay on attach covers only messages admitted while she was proven and hooked. Before work, the same gate: layer 30 guarantees that a friend takes no new work, and layers 40 and 50 deal none and let a lane take none, until her own session has proven the fresh challenge and the push is shown to land in that live session, checked by a self-test in which a send-to-self arrives in-turn and is answered from that turn; a helper alive, a nonce printed, or a message queued records an offer only and opens no gate. Presence reads report `away` with the last departure reason.
  - `CHALLENGED`: The server has issued a random nonce requiring proof within $T_{challenge}$ (default 60s). The friend cannot send until proven.
  - `PROVEN`: The session has supplied a valid nonce. The lease is valid until $T_{expiry} = \text{now} + T_{lease}$ (default 120s). The friend is reachable for real-time notification.
  - `EXPIRED` is not a state (round 2, proposal 3 of layer 60 and settlement (b)): it names the transition into `AWAY`, carrying its reason, `challenge_timeout` or `lease_expired`. Layer 10's names map one to one: absent and closed are `AWAY` with reason never-attached or detached; challenged, proven and away are `CHALLENGED`, `PROVEN` and `AWAY`.

- **Transitions**:
  - `Init`: System startup -> friend record initialized to `AWAY`.
  - `IssueChallenge`: `AWAY` -> `CHALLENGED` (nonce generated and written onto the link, $T_{challenge}$ timer armed).
  - `IssueRenewal`: `PROVEN` near expiry -> `PROVEN` with a renewal challenge outstanding, written onto the link. Its issue extends nothing (layer 10, Challenge row), and the name stays hooked until the lease deadline.
  - `Prove(nonce)`: `CHALLENGED` -> `PROVEN` (nonce verified, lease extended by $T_{lease}$, challenge disarmed).
  - `PiggybackedWait(nonce)`: `PROVEN` -> `PROVEN` only when the wait carries the session's answer to a fresh, unconsumed challenge; a consumed nonce extends nothing.
  - `ChallengeTimeout`: `CHALLENGED` (server time > $T_{challenge}$) -> `AWAY` with reason `challenge_timeout` (loud challenge-failed notice emitted).
  - `LeaseTimeout`: `PROVEN` (server time > $T_{expiry}$) -> `AWAY` with reason `lease_expired` (loud lease-expired notice emitted); the name is unhooked (guarantee 15).
  - `ExplicitAway`: `PROVEN` or `CHALLENGED` (`nova-hook away`) -> `AWAY` (clean disconnection).

### Displayed presence (`presence:<friend>`, derived)

The home of the deaf and displayed states of machine M1 (round 2; settlement (a) of layer 60,
proposal 2). Derived on read from the lease and layer 10's two clocks; never stored as a
second truth.

- **Internal states**: `up` (hooked), `deaf` (unhooked, heard within $T_{deaf}$ of the lapse),
  `down` (unhooked for $T_{deaf}$, or not heard for $T_{deaf}$; includes daemon answers, session
  does not).
- **Displayed**: `up` for `up` and `deaf`; `down` for `down`. Reads return the internal state and
  the hooked bit beside it (guarantee 17).
- **Transitions**: `Lapse`: `up` -> `deaf` at `LeaseTimeout` or `ChallengeTimeout` while
  `last_heard` is recent; `Silence`: `deaf` -> `down` at $T_{deaf}$ after the lapse, or when
  `last_heard` is older than $T_{deaf}$; `ReProve`: `deaf` or `down` -> `up` on `Prove`.

### Message state machine (`msg:<id>`, `inbox:<friend>`)

- **States**:
  - `PENDING`: Message enqueued in recipient inbox; awaiting delivery or client read.
  - `DELIVERED`: Message dispatched via real-time stream notification or picked up by blocking `wait`.
  - `OBSERVED`: Message content inspected via `nova-bus peek` or `nova-bus recv` without `--ack`.
  - `ACKED`: Message explicitly acknowledged by recipient via `nova-bus ack` or `nova-bus recv --ack`.
  - `PRUNED`: Message removed from durable storage after $T_{retain}$ post-acknowledgment.

- **Transitions**:
  - `Send`: Sender invokes `nova-bus send` -> if every recipient is hooked, `msg:<id>` written and appended to each recipient inbox as `PENDING` in one commit; otherwise refused `friend_not_hooked` and nothing written (guarantee 16).
  - `WaitWake`: Recipient's blocking `nova-hook wait` unblocks on event -> status marks `DELIVERED`.
  - `Fetch`: Recipient runs `nova-bus peek` / `recv` -> status marks `OBSERVED`.
  - `Acknowledge`: Recipient runs `nova-bus ack --id <id>` -> status marks `ACKED`.
  - `Compaction`: Retention timer expires on `ACKED` record -> record pruned.

### Record ownership

- The **Layer 10 Server Store** exclusively owns:
  - `lease:<friend>`: Status, current nonce hash, lease deadline, last proven timestamp, departure reason.
  - `inbox:<friend>`: Monotonic list of pending message IDs.
  - `msg:<id>`: Immutable message envelope (id, sender, recipients, cc, subject, body, timestamp, anchor metadata).
  - `acks:<friend>`: Set of acknowledged message IDs.
- The **Client** owns:
  - Zero durable message or presence state.
  - Ephemeral process memory only: the current active challenge nonce and connection socket.

## Failure modes

### 1. Challenge window expiration (deaf or unresponsive client)
- **Detection**: Server timer detects $T_{challenge}$ elapsed without receiving matching `prove`.
- **System action**: Lease transitions from `CHALLENGED` to `AWAY` (`reason: challenge_timeout`). Server appends a high-priority diagnostic notice to the friend's stream.
- **Client recovery**: When the client awakens or returns to comms, its next command receives a structured refusal (`refusal: not_hooked`, remedy: the link recipe of `nova-hook hook --as <friend>`). The session re-arms its link; the fresh challenge arrives through the link, and the session answers it with `nova-hook prove`.

### 2. Client process termination or unhandled restart
- **Detection**: Client restarts with empty memory; background blocking `wait` is terminated.
- **System action**: Server maintains the existing lease until $T_{expiry}$ elapses, then cleanly marks `AWAY`. Unacknowledged inbox messages remain safe in durable storage.
- **Client recovery**: Client startup re-arms the link with `nova-hook hook --as <friend>` and answers the challenge it delivers with `nova-hook prove`. Upon proof, client calls `nova-bus recv --as <friend> --all` to recover all unacknowledged messages in sequence order.

### 3. Network or store connection severance
- **Detection**: Client `nova-hook wait` or `nova-bus send` encounters TCP dial failure or connection reset.
- **System action**: Server holds state transactionally. Senders receive exit code 1 with transient store error.
- **Client recovery**: Client implements bounded exponential backoff retry up to an outer deadline (default 30s). On reconnection, client refreshes lease status before resuming traffic.

### 4. Nonce mismatch or replay attempt
- **Detection**: Client submits an expired, altered, or previously consumed nonce in `prove` or `wait`.
- **System action**: Server rejects the transaction immediately; lease status remains unchanged.
- **Client recovery**: Command exits with status code 2 (`refusal: invalid_nonce`). The refusal envelope names the remedy (re-arm the link); the current challenge is re-delivered through the link, never printed in the refusal (guarantee 14).

### 5. Link reader lost (a monitor expired, a wait not re-armed, a session wrapped)
- **Detection**: Renewal challenges reach no session; the server clock passes the lease deadline.
- **System action**: `LeaseTimeout`; the name is unhooked; its verbs refuse `not_hooked` and sends to it refuse `friend_not_hooked` (guarantees 12, 15, 16). Presence reads deaf, then down (guarantee 17).
- **Client recovery**: Re-arm the link, answer the challenge it delivers, drain the replay (guarantee 9).

### 6. Anchor diff mismatch
- **Detection**: `nova-bus send` with `--anchor-commit` cannot locate the verbatim quote in `git show <sha>`.
- **System action**: Send is aborted before message allocation; zero bytes written to store.
- **Client recovery**: Command exits with status code 2 (`refusal: quote_anchor_not_found`). Remedy indicates the diff search failure, prompting the sender to quote exact diff lines from the verified commit.

## Interfaces

### Below (Layer 10: Server truth)

Layer 20 requires Layer 10 to provide:

- Atomic Redis/store primitives:
  - `LEASE_SET(friend, state, nonce_hash, ttl)`
  - `LEASE_PROVE(friend, expected_nonce_hash, new_ttl) -> (bool, new_nonce)`
  - `INBOX_ENQUEUE(sender, []recipients, msg_json) -> msg_id`
  - `INBOX_ACK(friend, msg_id) -> bool`
  - `NOTIFICATION_PUB(friend, event_json)` / `NOTIFICATION_WAIT(friend, timeout)`

### Above (Layers 30, 40, 50)

Layer 20 exposes the following unified CLI commands and libraries to higher layers:

1. `nova-hook hook --as <friend> --harness <h> [--role <r>] [--json]`:
   - Arms the link: enters the friend into the presence registry and prints the link recipe for the harness (`--harness <h>`, layer 30). It never prints a nonce; the challenge is delivered only through the link (guarantee 14).
2. `nova-hook prove --as <friend> --nonce <n> [--json]`:
   - Validates challenge nonce; transitions lease to `PROVEN` and extends lease TTL.
3. `nova-hook away --as <friend> [--reason <str>] [--json]`:
   - Explicitly marks the lease `AWAY`, releasing real-time notification streams cleanly.
4. `nova-hook wait --as <friend> [--nonce <n>] [--timeout <dur>] [--json]`:
   - The one wait verb (round 2, change 6). One-shot blocking primitive for reactive harness turns; the link on exit-wake harnesses. Piggybacks proof (answer to a fresh challenge only) and exits on event.
   - `nova-bus recv --as <friend> --forever --exec <deliver>` is the link on monitor harnesses (guarantee 13).
5. `nova-bus send --as <sender> --to <recipients> [--cc <recipients>] --subject <s> --body <b> [--anchor-repo <dir> --anchor-commit <sha> --quote <q>] [--json]`:
   - Submits message to the bus with optional quote validation.
6. `nova-bus recv --as <friend> [--all] [--ack] [--forever --exec <deliver>] [--json]`:
   - Retrieves pending messages, optionally acknowledging them in the same transaction.
7. `nova-bus peek --as <friend> [--json]`:
   - Non-destructive inspection of pending inbox messages without altering message state.
8. `nova-bus ack --as <friend> --id <msg-id> [--json]`:
   - Explicitly and idempotently acknowledges a received message.
9. `nova-bus status --as <friend> [--json]`:
   - Inspects the authoritative server lease status, active nonce age, and pending inbox depth.

### Layers 40 and 50 sit on the link

Layers 40 and 50 carry every sprint, friend and coordinator message (deals, takes, finishes,
reads, judgments, pushes) over this layer's link and gate (guarantee 18). This layer knows
nothing of cards; it only refuses unhooked names and pushes through hooked links.

The rebuild retires these existing comms paths; none of them is read as authority in v1.3:
daemon polls of the bus and the sprint; ping/pong session checks and their nonce files; wake
files and the separate `nova-wake` wake ledger as a delivery path; the friend daemon's local
inbox, outbox, presence file, lane files and epoch flag; the local nudge-debt ledger; the
seat's per-minute goal nudge as a delivery path; the friend-sync roster loop; queue-for-later
sends to unhooked friends; the tick's take-back of taken cards to redeal them; and the one-shot
friend path (the friend daemon for one-shots, its presence pings and its friend-specific lane
parser), which layer 40 guarantee 12 replaces with an ordinary member lane.

## Rationale (round 2)

The project owner, live on 2026-10-14: "The psychic link is the key to reliable friends
communications." "Make it mandatory." "nothing works into the nova bus until you hook into the
psychic link first. just like sprint doesn't work until you hook in." "We need to stop making it
possible for friends to not hear message responses on the bus. Push notification hooking in is
the way." And: "Once we have the psychic link up and reliable between friends, I want you to then
rebuild the entire sprint comms around this." And: "Make sure the psychic link is client/server
and is not implemented as distributed programming. Redis is the server." And: "Part of client
server means all the state lives on the server and the clients get it and cache it as needed",
"ideally, once per-second" (layer 10 guarantees 14 and 15).

Evidence, same day: in the three hours before 14:24Z the sprint moved friend cards from working
back to ready by the tick deal 12,709 times, and finished friend reports were refused as no
longer the friend's (the Grok friend 92, the Antigravity friend 21, a one-shot dsh friend 8).
Friends missed bus replies because nothing pushed them into the live session, and senders held
successful sends to friends who could not hear. Guarantees 12 to 18 make that impossible rather
than announced; layers 40 and 50 remove the take-back.

## Open questions

1. **Lease duration calibration**: Proposed defaults are $T_{lease} = 120\text{s}$ with renewal challenges issued at 60s remaining, and $T_{challenge} = 60\text{s}$. We must confirm across all harnesses that 60s is sufficiently wide to survive extended local test compilations or garbage-collection pauses without inducing spurious `AWAY` transitions.
2. **Anchor quote length upper bound**: Propose capping `--quote` at 4,096 bytes to prevent diff abuse while fully accommodating standard patch hunks.
3. **Verb naming for the link.** The v1.3 card for this gate names `nova-bus link` and `nova-bus link --prove`; this layer keeps `nova-hook hook` and `nova-hook prove`, and the card follows the spec. Change 10's remedy text ("then the prove it returns") is read with guarantee 14: the prove answers the challenge the link returns.
4. **Cache refresh and zero polling.** Layer 10 guarantee 15 refreshes a client's cache about once per second; that refresh is a read of server state for display, never a delivery path. Delivery stays push-only through the link (guarantee 13). Default for v1.3: both hold, and a refresh never acknowledges, observes or proves anything.
