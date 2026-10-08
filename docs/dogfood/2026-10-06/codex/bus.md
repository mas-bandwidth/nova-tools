# Dogfood: nova-bus — 2026-10-06, Codex

For the cold-read pass, I used only tool-owned surfaces — `nova-bus -h`,
`nova-bus help`, each verb's `-h`, and `docs/SPEC-BUS.md` — then used every verb
against an isolated Redis 8 container bound to `127.0.0.1:16392` on a Linux
bench for store-using verbs, and exercised the store-free version and help
paths there. The scratch roster named ada and bob. A doc-shaped synthetic push
fixture remained `push=none`, so the send and
recv paths were exercised at their deaf-name refusals; I did not send a live
message or contact a friend. The container was stopped after the run. The
binary came from this checkout at `6ff31d5b369cc7402b39d36d6e898865007f3a7c`.

## Findings

1. `nova-bus help --json`

   ```text
   {"result":{"verb":"","status":"refused","exit":2,"remedy":"nova-bus help","why":["unknown verb \"--json\"; the verbs are wait, send, peek, recv, ack, receipts, overdue, log, names, version"]},"facts":{}}
   ```

   The banner says every verb takes `--json`, and `help` appears in the usage.
   Expected the help command to render its result as JSON, or the banner to state
   which verbs accept the flag. `nova-bus help wait --json` also prints text
   help and exits 0. Grade: NEXT.

2. `nova-bus send --as ada --to bob --subject hello --body "are you there?" --dry-run --redis 127.0.0.1:16392`

   ```text
   SEND REFUSED: deaf: ada has no proven push since never: no daemon has recorded one; the remedy: ada runs its friend daemon with a deliver adapter for its harness (nova-friend install --as ada --harness <h> --dir <d>) and its session answers the daemon's SESSION CHECK, which records the proof; nova-bus names shows every name's push; run: nova-bus help
   SEND REFUSED: deaf: bob has no proven push since never: no daemon has recorded one; the remedy: bob runs its friend daemon with a deliver adapter for its harness (nova-friend install --as bob --harness <h> --dir <d>) and its session answers the daemon's SESSION CHECK, which records the proof; nova-bus names shows every name's push; run: nova-bus help
   ```

   The banner's first-run setup says a Redis roster naming ada and bob, but this
   call still refuses until both have a fresh friend-session push proof. Expected
   the first-run setup to state that prerequisite so the example is runnable only
   after the needed sessions are installed and answering checks. Grade: NEXT.

3. `nova-bus ack --as bob --id 01ARZ3NDEKTSV4RRFFQ69G5FAV --redis 127.0.0.1:16392`

   ```text
   ACK OK acked=0 asked=1 login=none
   ACK ID id=01ARZ3NDEKTSV4RRFFQ69G5FAV acked=false
   ```

   This is the fixed ID in the top-level first-run example. The example's earlier
   `send` prints a different generated ID, and `recv --exec true` may already ack
   the delivered message. Expected the example to use the actual ID it just
   received or identify this as an illustrative placeholder; as written it exits
   0 after acknowledging nothing. Grade: NEXT.

READ 6/10 — The spec and verb help explain the bus, outputs, and refusals, but the first-run prerequisites are incomplete and the JSON promise does not fit `help`.

USE 5/10 — The inspection verbs, wait timeout, wake-file path, and refusals worked as described, but no send or receive could complete on a roster-only scratch setup because the required session proof was not documented as part of first-run setup.

Coverage: `wait`, `send`, `peek`, `recv`, `ack`, `receipts`, `overdue`, `log`, `names`, `version`, and `help`; each verb's `-h`; JSON, dry-run, stdin, wake-file, kind, timeout, unknown-verb, and missing-recipient paths.

urgent=0 next=3
