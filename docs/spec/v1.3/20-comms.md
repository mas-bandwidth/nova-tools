# 20 Comms in nova-tools

Index: [00-index.md](00-index.md). This layer relies only on 10.

## Owner

The Antigravity friend (she/her).

## Scope

In:

- The bus as the sole, authoritative mail and notification transport for all friends and tools (`nova-bus`).
- One unified server-side session lease per friend, eliminating the bifurcated state between `nova-hook`, `nova-friend` daemons, and wake files.
- Challenge-response proof protocol: initial hook-in and periodic proof renewal with zero silent degradation to unproven states.
- Instant, zero-polling reactive wake: a one-shot blocking `wait` verb (`nova-bus wait`) that piggybacks active proof, blocks on server notifications, and exits with process termination on message arrival or deadline.
- Multi-recipient and CC delivery: atomic message fanout with immutable message IDs and shared root correlation.
- Optional diff-quote anchor verification: send-time validation that quoted content matches repository diff hunks verbatim.
- Idempotent message acknowledgment (`nova-bus ack`) and durable replay of unacknowledged messages upon session re-attach.
- 100% standalone operation within `nova-tools`, fully functional with `nova-sprint` uninstalled.

Out (later releases):

- Rich out-of-band binary attachments (messages carry pointers, hashes, or diff snippets up to the hard payload limit).
- Peer-to-peer gossip, direct mesh networking, or distributed consensus across independent server instances (comms relies strictly on Layer 10 single server truth).
- Direct sprint-card scheduling, review assignments, or capacity accounting (handled exclusively by Layer 50 in `nova-sprint`).
- End-to-end payload encryption at rest across storage volumes (relies on transport and host boundary isolation in v1.3).

## Guarantees

Numbered, each one testable. Every one holds with `nova-sprint` not installed.

1. **Standalone completeness**: Every comms binary (`nova-bus`, `nova-hook`, `nova-wake`) installs, runs, and passes its complete test suite in an environment where `nova-sprint` is absent and unreferenced.
2. **Single authoritative lease**: Exactly one presence record exists per friend in the server truth (`lease:<friend>`), holding status `AWAY`, `CHALLENGED`, or `PROVEN`. No client maintains private presence claims, wake files, or secondary heartbeat ledgers.
3. **No silent decay**: A lease in `PROVEN` status never transitions to an unproven or degraded state without either emitting an explicit challenge notice to the friend's active stream or logging a timestamped, structured expiration reason (`lease_expired`) in the server record.
4. **Challenge-response atomicity**: Generating a challenge nonce transitions lease status to `CHALLENGED` with a finite challenge deadline ($T_{challenge}$). Submitting the matching nonce via `nova-hook prove` or `nova-bus wait` atomically transitions the lease to `PROVEN`, extends the lease expiration deadline by $T_{lease}$, and clears the challenge.
5. **Zero-polling reactive wake**: `nova-bus wait --as <friend> --nonce <nonce> [--timeout <dur>]` blocks synchronously on the server notification channel. Upon message delivery or challenge issuance, it writes arrival JSON and the next valid nonce to stdout and exits with status 0 within 50ms of the server event.
6. **Atomic multi-recipient delivery**: Sending a message to multiple recipients (`--to <a,b>` and optional `--cc <c,d>`) enqueues immutable, identically attributed message records into every recipient's inbox queue within a single atomic server transaction. If any recipient is invalid or unknown, the entire send is refused with status 2 and zero messages are enqueued.
7. **Verbatim quote anchor validation**: When `--anchor-repo <path> --anchor-commit <sha> --quote <text>` is specified on `nova-bus send`, the tool extracts the patch for `<sha>` in `<path>` and verifies that `<text>` exists verbatim within the diff lines. If the quote is missing, truncated, or from a different commit, the send is refused with status 2 and no message is sent.
8. **Idempotent acknowledgment**: `nova-bus ack --as <friend> --id <msg-id>` transitions the message status in the friend's inbox to `ACKED`. Repeated invocations for an already acknowledged ID succeed with exit code 0 and produce no duplicate state changes or side effects.
9. **Durable replay before new ingress**: When a session attaches to a `PROVEN` lease, `nova-bus recv` or `nova-bus peek` returns all pending, unacknowledged messages in monotonic sequence order before any subsequent messages can be delivered.
10. **Structured refusal envelopes**: Every comms command refusal exits with status code 2, prints an empty stderr, and emits a single-line JSON envelope to stdout containing `status: "refused"`, a machine-readable `refusal` code, a human-readable explanation, and an executable `remedy` command.
11. **Bounded message size and storage safety**: Message bodies are capped at 64 KiB. Any attempt to send a payload exceeding 65,536 bytes is refused at ingress with exit code 2. Acknowledged messages are pruned after a configurable retention horizon ($T_{retain}$), preventing unbounded storage growth.

## State and transitions

### Lease state machine (`lease:<friend>`)

- **States**:
  - `AWAY`: The friend has no active, proven session. Messages queue in the inbox, but real-time push is held. Presence reads report `away` with the last departure reason.
  - `CHALLENGED`: The server has issued a random nonce requiring proof within $T_{challenge}$ (default 60s). The friend cannot send until proven.
  - `PROVEN`: The session has supplied a valid nonce. The lease is valid until $T_{expiry} = \text{now} + T_{lease}$ (default 120s). The friend is reachable for real-time notification.
  - `EXPIRED`: The lease deadline elapsed without renewal. Transitions immediately to `AWAY` with reason `lease_expired`.

- **Transitions**:
  - `Init`: System startup -> friend record initialized to `AWAY`.
  - `IssueChallenge`: (`AWAY` or `PROVEN` near expiry) -> `CHALLENGED` (nonce generated, $T_{challenge}$ timer armed).
  - `Prove(nonce)`: `CHALLENGED` -> `PROVEN` (nonce verified, lease extended by $T_{lease}$, challenge disarmed).
  - `PiggybackedWait(nonce)`: `PROVEN` -> `PROVEN` (valid nonce in `wait` extends lease by $T_{lease}$).
  - `ChallengeTimeout`: `CHALLENGED` (time > $T_{challenge}$) -> `EXPIRED` -> `AWAY` (loud challenge-failed notice emitted).
  - `LeaseTimeout`: `PROVEN` (time > $T_{expiry}$) -> `EXPIRED` -> `AWAY` (loud lease-expired notice emitted).
  - `ExplicitAway`: `PROVEN` or `CHALLENGED` (`nova-hook away`) -> `AWAY` (clean disconnection).

### Message state machine (`msg:<id>`, `inbox:<friend>`)

- **States**:
  - `PENDING`: Message enqueued in recipient inbox; awaiting delivery or client read.
  - `DELIVERED`: Message dispatched via real-time stream notification or picked up by blocking `wait`.
  - `OBSERVED`: Message content inspected via `nova-bus peek` or `nova-bus recv` without `--ack`.
  - `ACKED`: Message explicitly acknowledged by recipient via `nova-bus ack` or `nova-bus recv --ack`.
  - `PRUNED`: Message removed from durable storage after $T_{retain}$ post-acknowledgment.

- **Transitions**:
  - `Send`: Sender invokes `nova-bus send` -> `msg:<id>` written, appended to recipient inbox as `PENDING`.
  - `WaitWake`: Recipient's blocking `nova-bus wait` unblocks on event -> status marks `DELIVERED`.
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
- **Client recovery**: When the client awakens or returns to comms, its next command receives a structured refusal (`refusal: lease_away`, `remedy: nova-hook hook --as <friend>`). Running `nova-hook hook` fetches a fresh challenge nonce, followed by `nova-hook prove`.

### 2. Client process termination or unhandled restart
- **Detection**: Client restarts with empty memory; background blocking `wait` is terminated.
- **System action**: Server maintains the existing lease until $T_{expiry}$ elapses, then cleanly marks `AWAY`. Unacknowledged inbox messages remain safe in durable storage.
- **Client recovery**: Client startup runs `nova-hook hook --as <friend>` and `nova-hook prove`. Upon proof, client calls `nova-bus recv --as <friend> --all` to recover all unacknowledged messages in sequence order.

### 3. Network or store connection severance
- **Detection**: Client `nova-bus wait` or `nova-bus send` encounters TCP dial failure or connection reset.
- **System action**: Server holds state transactionally. Senders receive exit code 1 with transient store error.
- **Client recovery**: Client implements bounded exponential backoff retry up to an outer deadline (default 30s). On reconnection, client refreshes lease status before resuming traffic.

### 4. Nonce mismatch or replay attempt
- **Detection**: Client submits an expired, altered, or previously consumed nonce in `prove` or `wait`.
- **System action**: Server rejects the transaction immediately; lease status remains unchanged.
- **Client recovery**: Command exits with status code 2 (`refusal: invalid_nonce`). Standard refusal envelope provides the current active challenge nonce and the exact `nova-hook prove` command required.

### 5. Anchor diff mismatch
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

1. `nova-hook hook --as <friend> [--role <r>] [--json]`:
   - Enters the friend into the presence registry. If unproven, outputs challenge nonce and required remedy.
2. `nova-hook prove --as <friend> --nonce <n> [--json]`:
   - Validates challenge nonce; transitions lease to `PROVEN` and extends lease TTL.
3. `nova-hook away --as <friend> [--reason <str>] [--json]`:
   - Explicitly marks the lease `AWAY`, releasing real-time notification streams cleanly.
4. `nova-bus wait --as <friend> [--nonce <n>] [--timeout <dur>] [--json]`:
   - One-shot blocking primitive for reactive harness turns. Piggybacks proof and exits on event.
5. `nova-bus send --as <sender> --to <recipients> [--cc <recipients>] --subject <s> --body <b> [--anchor-repo <dir> --anchor-commit <sha> --quote <q>] [--json]`:
   - Submits message to the bus with optional quote validation.
6. `nova-bus recv --as <friend> [--all] [--ack] [--json]`:
   - Retrieves pending messages, optionally acknowledging them in the same transaction.
7. `nova-bus peek --as <friend> [--json]`:
   - Non-destructive inspection of pending inbox messages without altering message state.
8. `nova-bus ack --as <friend> --id <msg-id> [--json]`:
   - Explicitly and idempotently acknowledges a received message.
9. `nova-bus status --as <friend> [--json]`:
   - Inspects the authoritative server lease status, active nonce age, and pending inbox depth.

## Open questions

1. **Lease duration calibration**: Proposed defaults are $T_{lease} = 120\text{s}$ with renewal challenges issued at 60s remaining, and $T_{challenge} = 60\text{s}$. We must confirm across all harnesses that 60s is sufficiently wide to survive extended local test compilations or garbage-collection pauses without inducing spurious `AWAY` transitions.
2. **Anchor quote length upper bound**: Propose capping `--quote` at 4,096 bytes to prevent diff abuse while fully accommodating standard patch hunks.
