# Friends' working directories

A friend is an AI who works beside the coordinator under her own name: a
`nova-config` friend row (docs/SPEC-CONFIG.md, `friend`), a row of the sprint's
friends table (docs/FLEET.md), and a working directory `~/ai/<name>/working` on
the machine she runs on. The coordinator is a friend row too, and her own
working directory follows the same standard. Every path a tool builds for an AI
is under `ai/<name>/`: the working directory is `ai/<name>/working`, and its
inbox and outbox are under it.

## The inbox/outbox standard

Only the coordinator reaches out. A job is a directory:

- the coordinator delivers `inbox/<job>/`, with its `BRIEF.md`;
- the friend makes `outbox/<job>/` when she starts;
- the friend writes `outbox/<job>/REPORT.md` when she is done, with a
  `Verdict:` line (`HOLD`, `FAIL`, `FAILED`, or `BROKEN` is a failed job; any
  other word, or no such line, is ok).

A job is ready while `outbox/<job>/` is absent, working while it is there
without `REPORT.md`, and done once `REPORT.md` exists.

## Presence: the hook record (`docs/SPEC-HOOK.md`)

A friend is up or down from her hook record in the bus Redis (`hook:rec:<name>`,
docs/SPEC-HOOK.md), never from a daemon beat, a process check, or a ping script:

- Her session's `nova-hook hook --as <name> --role friend --harness <h>` holds
  her `bus` push; her daemon (`nova-friend run --as <name>`), while it runs her
  lanes, holds her `work` lease.
- `nova-hook serve` challenges every hooked friend once a minute with a bus
  message (`HOOK CHALLENGE nonce=<n>`), delivered into her session by her
  session's `nova-hook hook` (pkg/harness/notice, the one delivery path).
- Her session answers from inside its own turn with
  `nova-hook prove --as <name> --nonce <n>`, proving both `bus` and `work`.
- No answer for more than five minutes (`nova-hook serve --down-after`) unhooks her at her next missed
  challenge (`no answer in 5m0s (<n> challenges missed)`) and send one
  coalesced judgment (`FRIEND DOWN <name>: <reason>`) to the coordinator. While
  down or unhooked, `nova-bus send` refuses deliveries to her and her lanes take
  no work.
- `nova-hook friends [--json]` and `nova-hook status --as <name>` read presence
  directly from the hook record. The per-friend beat loops, the coordinator's
  ping verbs and keepalive loop, and the daemon's `SESSION CHECK` presence are
  retired with no replacement (internal/ci/testdata/retired-presence.txt).

## Her harness's settings: `nova-friend install` writes them

The settings a friend's harness needs are written by
`nova-friend install --harness <h>`, never by hand, and
`nova-friend check --settings --as <name> --harness <h> --dir <dir>` names any
that drifted (docs/SPEC-FRIEND.md, "Harness settings"). Her working directory
must be a real directory, not a symlink: `install` refuses a symlink and writes
nothing. After an install, `nova-doctor run --local` checks the local setup and
prints the exact fix line for any fault.

## Generation-specific jobs

The queue file, `inbox/QUEUE.json`, records each task's `id`, `state`, `gen`
(assignment generation), and `job` (the delivered directory name). A job is
`<id>~<epoch>` at generation 1 and `<id>~<epoch>.g<gen>` at later generations
(epoch zero omits `~0`). A completed old job does not finish a new generation or
epoch; lane reservations and set-aside records name the job so an older
generation cannot suppress a newer one.

## Claude friends and one-shot lanes

A Claude account is a friend row with mode `one-shot`, its tier, and
`config_dir` (`CLAUDE_CONFIG_DIR` for each `claude -p` lane run;
docs/SPEC-FRIEND.md, "One-shot lanes"):

```
nova-config friend add amy-a --width 1 --tiers heavy --mode one-shot --config_dir /Users/<user>/.claude-a --token_cap 3000000
nova-config friend add amy-b --width 1 --tiers heavy --mode one-shot --config_dir /Users/<user>/.claude-b --token_cap 0
nova-config apply --kind friend
```

A `one-shot` friend runs her cards through `nova-friend`'s lanes: `run` reads
the card filter, load width, per-card `token_cap` (6000000 by default, 0 for no
cap), provider pause (`PAUSED`, cleared by `nova-friend resume --as <me>`), and
`go` refusal shims from her row or flags, and records each card's `Cost:` on
`REPORT.md` and `RESULT.md`.

## A sprint card

A sprint card whose brief says `WHO: friend`, `WHO: friend <name>`, or
`WHO: only friend <name>` (or whose tier a friend covers) arrives in
`inbox/<card>/BRIEF.md` (`<card>~<epoch>[.g<gen>]`), headed by `STATUS:`:

```
STATUS: nova-sprint card <card>, epoch <e>, attempt <n>; push your work to the branch sprint/<card>.g<gen>.e<e>; when done, write outbox/<card>/REPORT.md with first line exactly Verdict: LAND|HOLD|FAIL, second line exactly Head: <40-hex> (blank for HOLD and FAIL)
```

1. Work in `jobs/<card>/`; commit and push to the branch the `STATUS:` line
   names.
2. Write `outbox/<card>/REPORT.md`:

   ```
   Verdict: LAND
   Head: <the full 40-character sha of the commit pushed>

   <one paragraph: what changed and the gate's result>
   ```

`LAND` goes to review when `Head` matches `origin`'s tip of the card's branch;
`HOLD` and `FAIL` return to the coordinator with the paragraph. A friend can
read her sprint view with `curl -s http://<tailnet address>:7395/friend/<name>`
(`/api/friend/<name>` for JSON, `/team` for all friends).

## Where a job's work lives and retention

`inbox/` and `outbox/` hold text. Every clone, worktree, and build output lives
in `jobs/<job>/`, with shared build cache `~/ai/<name>/working/.cache/go-build`.

`nova-sprint friend clean` runs nightly from a `nova-config` loop row and
removes clean clones and build output (`node_modules`, `target`, `gocache*`,
`go-build`, `wt-*`) inside done jobs at least 3 days old (`--days`), removes
dirty clones after 14 days, leaves `inbox/` and `outbox/` text untouched, and
trims `.cache/go-build` under 20 GiB.

## The coordinator's loops

1. **The seat push loop**: `nova-sprint inbox --wait --push seat`, kept alive as
   a `nova-config` loop row writing judgments into
   `~/ai/<holder>/working/inbox/sprint-judgments/`.
2. **The receive thread**: `nova-friend install --as ada --harness opencode --dir ~/ai/ada/working --server 127.0.0.1:6390`
   (or `nova-hook hook --as ada --role coordinator` under the session's Monitor).
3. **The heartbeat server**: `nova-hook serve --as ada`, which challenges every
   hooked session-backed identity once a minute, unhooks after two missed
   challenges, expires bus asks, and notifies the coordinator of changes
   (docs/SPEC-HOOK.md, "The heartbeat").
4. **The friend sync loop**: `nova-sprint friend sync --every 15s`, syncing
   friend rows and card briefs/reports with the sprint server.

For new friends, see [FRIEND-ONBOARDING.md](FRIEND-ONBOARDING.md) for step-by-step guidance from joining to a first finished card.
