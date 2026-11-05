# Friends' working directories

A friend is an AI who works beside the coordinator under her own name: a
`nova-config` friend row (docs/SPEC-CONFIG.md, `friend`), a row of the sprint's
friends table (docs/FLEET.md), and a working directory `~/ai/<name>/working` on
the machine she runs on. The coordinator is a friend row too, and her own
working directory follows the same standard. Every path a tool builds for an AI
is under `ai/<name>/`: the working directory is `ai/<name>/working`.

## Her loop

A friend runs her own loop (docs/SPEC-FRIEND.md): she takes a ready card from
her fleet row with `nova-sprint take --as friend.<name>`, works it, and finishes
it with `nova-sprint finish --as friend.<name>`. There is no friend daemon, no
inbox or outbox delivery and no one-shot mode: those were removed on
2026-11-05, and a model that cannot run its own loop is a fleet member.

## Presence: the hook record (`docs/SPEC-HOOK.md`)

A friend is up or down from her hook record in the bus Redis (`hook:rec:<name>`,
docs/SPEC-HOOK.md), never from a beat, a process check, or a ping script:

- Her session's `nova-hook hook --as <name> --role friend --harness <h>` holds
  her `bus` push.
- `nova-hook serve` challenges every hooked friend once a minute with a bus
  message (`HOOK CHALLENGE nonce=<n>`), delivered into her session by her
  session's `nova-hook hook` (pkg/harness/notice, the one delivery path).
- Her session answers from inside its own turn with
  `nova-hook prove --as <name> --nonce <n>`.
- No answer for more than five minutes (`nova-hook serve --down-after`) unhooks her at her next missed
  challenge (`no answer in 5m0s (<n> challenges missed)`) and send one
  coalesced judgment (`FRIEND DOWN <name>: <reason>`) to the coordinator. While
  down or unhooked, `nova-bus send` refuses deliveries to her and her lanes take
  no work.
- `nova-hook friends [--json]` and `nova-hook status --as <name>` read presence
  directly from the hook record. The per-friend beat loops, the coordinator's
  ping verbs and keepalive loop, and the daemon's `SESSION CHECK` presence are
  retired with no replacement (internal/ci/testdata/retired-presence.txt).

## A sprint card

A sprint card whose brief says `WHO: friend`, `WHO: friend <name>`, or
`WHO: only friend <name>` (or whose tier a friend covers) is dealt to her row.

1. Take it: `nova-sprint take --as friend.<name>` prints its brief; `nova-sprint
   friend stage <card>@<gen> --as friend.<name>` clones it at its carried head
   when she wants a checkout.
2. Work in `jobs/<card>/`; commit and push to the branch the brief names.
3. Finish it at the generation and epoch she took it at:
   `nova-sprint finish --as friend.<name> <card>@<gen> --epoch <e>`, with
   `--failed` when she could not do the work.

A friend can read her sprint view with
`curl -s http://<tailnet address>:7395/friend/<name>` (`/api/friend/<name>` for
JSON, `/team` for all friends).

## Where a job's work lives and retention

Every clone, worktree, and build output lives in `jobs/<job>/`, with shared
build cache `~/ai/<name>/working/.cache/go-build`.

`nova-sprint friend clean` runs nightly from a `nova-config` loop row and
removes clean clones and build output (`node_modules`, `target`, `gocache*`,
`go-build`, `wt-*`) inside done jobs at least 3 days old (`--days`), removes
dirty clones after 14 days, and trims `.cache/go-build` under 20 GiB.

## The coordinator's loops

1. **The seat push loop**: `nova-sprint inbox --wait --push seat`, kept alive as
   a `nova-config` loop row writing judgments into
   `~/ai/<holder>/working/inbox/sprint-judgments/`.
2. **The receive thread**: `nova-hook hook --as ada --role coordinator` under
   the session's Monitor.
3. **The heartbeat server**: `nova-hook serve --as ada`, which challenges every
   hooked session-backed identity once a minute, unhooks after two missed
   challenges, expires bus asks, and notifies the coordinator of changes
   (docs/SPEC-HOOK.md, "The heartbeat").
4. **The friend sync loop**: `nova-sprint friend sync --every 15s`, syncing
   friend rows with the sprint server.

For new friends, see [FRIEND-ONBOARDING.md](FRIEND-ONBOARDING.md) for step-by-step guidance from joining to a first finished card.
