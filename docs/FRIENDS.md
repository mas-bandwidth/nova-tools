# Friends' working directories

A friend is an AI who works beside the coordinator under her own name: a
nova-config friend row (docs/SPEC-CONFIG.md, `friend`), a row of the sprint's
friends table (docs/FLEET.md), and a working directory `~/<name>-working` on
the machine she runs on. The coordinator is a friend row too (the sprint row's
`coordinator` names one), and her own working directory follows the same
standard. This page is the standard for that directory: how a job arrives, how
it is reported, where its work lives, and how the work is removed once done.

## The inbox/outbox standard

Only the coordinator reaches out. A job is a directory:

- the coordinator delivers `inbox/<job>/`, with its `BRIEF.md`; `<job>` begins
  with its date, `2026-10-02-cold-rating`;
- the friend makes `outbox/<job>/` when she starts;
- the friend writes `outbox/<job>/REPORT.md` when she is done, with a
  `Verdict:` line (its first word HOLD, FAIL, FAILED or BROKEN is a failed job;
  any other word, or no such line, is ok).

A job is ready while `outbox/<job>/` is absent, working while it is there
without `REPORT.md`, and done once `REPORT.md` exists. `nova-sprint friend
sync` reads these directories (writing in them only a sprint card's brief, below)
into the friends table's `ready`, `working`, `done` and `ok%`.

## Is she doing work: `nova-friend status`

`nova-friend status --as <name> --dir <dir>` decides her status from evidence,
never from her daemon's beat alone, and shows the evidence beside it
(docs/SPEC-FRIEND.md, "A friend's status, from evidence"):

```
status=down why="no session answer 12m" evidence="harness unknown; no session answer 12m; no limit; 2 undelivered; last result 40m exit=0"
```

## Her harness's settings: `nova-friend install` writes them

The settings a friend's harness needs in its own config are written by
`nova-friend install --harness <h>`, never by hand, and `nova-friend check
--settings --as <name> --harness <h> --dir <dir>` names any that drifted
(docs/SPEC-FRIEND.md, "Harness settings"). Her working directory must be a real
directory, not a symlink to one: install refuses a symlink and writes nothing
(on 2026-10-05 a Codex writable root that was a symlink took no writes for ten
hours). Name the real path with `--dir`.

## Her inbox pushes to her: the push proof

`nova-friend install` and `run` refuse a harness nothing pushes into: one
with no deliver command (every surveyed harness; not claude, which runs each card as a process of its own and proves by the folder: its check is written as `<dir>/inbox/SESSION-CHECK-<nonce>` for a live session to answer) is refused
before anything is written, with the adapter card as the remedy; a dsh
session under an agent preset is refused with `start a session in <dir> with
no agent preset and name it with --session <id>`. `run` delivers one SESSION
CHECK before its loop and exits 2 when no pong comes back within five
minutes. Her beat carries her session's last proof, and the sprint deals
nothing to a friend whose proof is older than fifteen minutes
(docs/SPEC-FRIEND.md, "The push proof"). The bus never refuses a message on
the proof: `nova-bus send` and `recv` print `push=<state> for <name>` as a
NOTE and deliver all the same (docs/SPEC-BUS.md,
bus-requires-inbox-push-proof).

A dsh session that cannot take a turn defers its messages and never loses
one, but it does not read up: a headless turn whose output carries the agent
preset refusal or `MISSING_CREDENTIAL`, whatever its exit code (dsh has
exited 0 with the refusal), is a failed delivery. On the first one the
daemon marks the session broken, `status` reads her down with the reason
(`dsh session <id>: agent preset minimal` or `dsh: missing credential`), the
seat is told once, and every message stays pending, tried again every ten
seconds; the first turn that succeeds clears it. Fix the session (start one
with no agent preset, or store the provider's key for the headless profile);
no restart is needed (docs/SPEC-FRIEND.md, "A turn the session cannot take").

## Generation-specific jobs

The queue file, `inbox/QUEUE.json`, records each task's `id`, `state`, `gen`
(assignment generation) and `job` (the delivered directory name). A job is
`<id>~<epoch>` at generation 1 and `<id>~<epoch>.g<gen>` at later generations.
Epoch zero omits `~0`, matching the delivered job name. A legacy task with no
`gen` means generation 1; with no `job`, the lane selects the highest epoch
of that generation. It never falls back to another generation. With `job`
present, the lane requires that exact directory and matching generation.

A completed old job does not finish a new generation or epoch: queue sync
resets its state and clears its old deliverable for the new assignment.
Completion of the same job is preserved. Lane reservations and set-aside records
name the job, so an older generation cannot suppress or reserve a newer one. Legacy
set-aside records containing only a card id apply to generation 1. Progress passes only the numeric
sprint epoch, without the generation suffix.
## Claude friends: one account each

A Claude account is a friend of its own: a friend row with mode `one-shot`,
the tier it can do, and `config_dir`, the account's Claude config directory,
which each of her lanes runs `claude -p` with as `CLAUDE_CONFIG_DIR`
(docs/SPEC-FRIEND.md, one-shot lanes). Four accounts on one machine are four
rows, each its own working directory and daemon. The example, four heavy-tier
rows of one person's four accounts:

```
nova-config friend add amy-a --slots 1 --tiers heavy --mode one-shot --config_dir /Users/<user>/.claude-a
nova-config friend add amy-b --slots 1 --tiers heavy --mode one-shot --config_dir /Users/<user>/.claude-b
nova-config friend add amy-c --slots 1 --tiers heavy --mode one-shot --config_dir /Users/<user>/.claude-c
nova-config friend add amy-d --slots 1 --tiers heavy --mode one-shot --config_dir /Users/<user>/.claude-d
nova-config apply --kind friend
```

A claude row in one-shot mode without `config_dir` runs no lane: her daemon
says so on its record with the `nova-config friend set` that fixes it.

## One-shot lanes at parity with the card runner

A friend whose delivery mode is `one-shot` runs her cards through nova-friend's lanes, not through a runner script:
`run` takes the card filter, the load width, the per-card token cap, the provider pause, the go refusal and the card's
cost from the friend row (`row_tiers=`, `row_streams=`, `row_token_cap=`, `row_load_max=`, `row_load_width=`,
`row_pause_on=`, `row_refuse_go=`) or its flags, and says a card's cost on its `REPORT.md` (`Cost:` under `Head:`) and
`RESULT.md`. A provider failure stops every lane under way (each card kept for later), writes `PAUSED` in her state
directory, and her beat says her down with the provider's message while it stands; `nova-friend resume --as <me>` is the
person bringing her up. A dealt card outside her tiers is never run, and the coordinator is asked by bus to take it back
(`nova-sprint friend take`): the sprint server serves no friend's own take-back yet. Until nova-config's row and
`nova-sprint friend beat` carry the `row_*` words, set them with `run`'s flags (docs/SPEC-FRIEND.md,
opencode-lanes-parity-b.w1).

### friend-token-cap-bb.w2

The per-card token cap is the friend row's `token_cap`, 6000000 by default and 0 none. `nova-sprint friend beat` answers it as `row_token_cap=`, including 0. Every one-shot lane (a claude lane and an opencode lane) counts the card's tokens as it runs — input, cache read, cache write, output and reasoning, summed from the harness's own usage record — and when the sum reaches the cap the lane stops its own child and holds the card with the reason `token cap <cap> reached at <n> tokens` plus the usage so far. The friend's next card proceeds. The row example, one heavy row with the cap halved and one with none:

```
nova-config friend add amy-a --slots 1 --tiers heavy --mode one-shot --config_dir /Users/<user>/.claude-a --token_cap 3000000
nova-config friend add amy-b --slots 1 --tiers heavy --mode one-shot --config_dir /Users/<user>/.claude-b --token_cap 0
nova-config apply --kind friend
```

## A sprint card

Each sync pass sends a batch-mode friend (the default) at most one status wake for the cards it delivers, naming their count and up to ten card ids with the remaining count, her inbox directory and the sentence that starts the work. A friend whose store row says mode `one-shot` keeps one wake per card so her runner starts its lane. A pass that delivers no file sends no wake. The per-card inbox file is the record: a failed batch wake leaves every delivered file in place and is said and recorded once for the pass as `NFriendNotWoken`.

A card of the sprint whose brief says `WHO: friend`, `WHO: friend <name>`, or
`WHO: only friend <name>`, and a card with no WHO line whose tier a friend covers,
is offered to a friend before the fleet (docs/SPEC-SPRINT.md section 1, a friend's card; the owner,
2026-10-03: "Could we try expressing the work left for nova-tools-1.1.0 into
cards, and doing it via the sprint, but doing parts on friends where we would
normally do friend work."). It arrives as a job like any other:
`inbox/<card>/BRIEF.md` (after a clear, `<card>~<epoch>`), written by
`nova-sprint friend sync`. Its first line is the STATUS line:

```
STATUS: nova-sprint card <card>, epoch <e>, attempt <n>; push your work to the branch sprint/<card>.g<gen>.e<e>; when done, write outbox/<card>/REPORT.md with first line exactly Verdict: LAND|HOLD|FAIL, second line exactly Head: <40-hex> (blank for HOLD and FAIL)
```

then the working-directory line below, a later attempt's start (the current tip
of the card's base branch on origin, never an older base, with the work of the
last attempt that pushed carried onto it by her, redone where it does not
apply, and the Head she reports on that tip; nothing checks that descent, and
the sprint's only check of her finish is that Head is origin's tip of her
branch) and why it exists (`This attempt exists because:`, `A reader found:`, `The coordinator
asks:`), a blank line, and the card's brief. What a friend does with it:

1. Work in `jobs/<card>/` as for any job; commit, and push the commit to the
   branch the STATUS line names (never another: the sprint reads and lands
   origin's tip of that branch, and nothing else).
2. Write `outbox/<card>/REPORT.md` in this form, exactly:

   ```
   Verdict: LAND
   Head: <the full 40-character sha of the commit pushed>

   <one paragraph: what changed and the gate's result>
   ```

   `LAND` is work ready for its reads and its landing; `HOLD` is work stopped
   for the coordinator's decision, `FAIL` work that could not be done; for
   either, `Head:` may be left out with line 2 blank, and the first paragraph
   says why. The first line is exactly `Verdict: LAND|HOLD|FAIL`, and the
   second exactly `Head: <40-hex>`. Reading stays lenient: the first
   `Verdict:` and `Head:` lines are read wherever they sit (markdown marks
   around them are fine). Headers outside the first two lines add a NOTE to
   the card naming their line numbers. The first paragraph that is neither
   of them nor a heading goes onto the card, cut to 1 KiB before the NOTE.

The sync finishes the card on its next run after the report (the period of the
loop that runs it: 15 s in the friend sync loop, below): `LAND` goes to review at
origin's tip of the branch when that tip is the full sha `Head:` names; a Head
that is not the tip (a commit not pushed there, or pushed to after the report)
or a branch origin does not hold finishes nothing, and the sync says so naming
both shas each run until the report's Head is the tip (or the card's deadline
passes); a `LAND` with no full sha, or any other word, comes back failed saying
what it lacks; `HOLD` and `FAIL` come back failed to the coordinator with the
paragraph. A sprint card is not counted among the
inbox's jobs: the friends table counts it from the sprint.

A friend pulls her own view of the sprint whenever she wants it with one curl, `curl -s
http://<tailnet address>:<port>/friend/<name>` (the coordinator's dashboard pull port,
`7395` by default; `/api/friend/<name>` is the same as JSON), and reads the sprint line,
her row, and a line per card dealt to her with its state, its deadline and its branch. It
is read-only: it changes nothing, and her inbox and outbox stay how work arrives and is
reported.
`curl -s http://<tailnet address>:<port>/team` is every friend at once, each with the
cards she holds, so friends see what each other are on (`/api/team` as JSON).

## The friend sync loop

`nova-sprint friend sync --every <d>` is the loop: it syncs, waits `d`, and
syncs again until it is interrupted (the owner, 2026-10-04: "Golang nova-tools
and nova-sprint verbs only"; "Make the ping loop mechanical!!!!"). Each pass
reopens the store, so it runs at the sprint's epoch then, and, given no
`--actor`, acts as the sprint's coordinator seat as the store says it then, so
a seat that moves takes the loop with it. A pass that changed something (a
friend added, taken off or updated, a card delivered or finished) prints its
lines; a pass with nothing to do prints nothing; a failing pass is said once on
stderr as `FRIEND-SYNC FAILING`, again only when what it says changes, and the
first pass that is ok after it prints `FRIEND-SYNC OK again after <n> failing
passes`. An interrupt ends it with 0 (`FRIEND-SYNC STOP interrupted`), and a
binary replaced under it with 3, so its supervisor starts the new one.

A loop failing for a minute (four passes) is one judgment in the coordinator's
inbox, `friend sync keeps refusing`, naming the refusal and its remedy (the
loop's own line, `schema config is at version 35 and this binary carries 36;
run: nova-config migrate`, say): the loop records its standing failure on the
fleet table (`FRIEND-SYNC JUDGMENT recorded`), the tick raises the judgment on
the record, once, and the first ok pass clears the record, which closes the
judgment (`ack` or `wait` are its only answers). The loop keeps retrying every
pass as before. On 2026-10-08 the loop refused from 01:05 to 08:51 ET, no card
was delivered or collected for 7h45m, and its one FAILING line in its own log
told no one.

It is installed as a nova-config loop row kept alive on the machine that holds
the friends' working directories, its secrets by name from that machine's seat
and no shell in its argv; `--root` is left out, so the friends' directories are
under the unit's HOME:

```
nova-config loop add friend-sync --machine bench-a --argv '["/usr/bin/env","NOVA_PG_PASSWORD_ENV=NOVA_PG_CONFIG_PASSWORD","NOVA_SPRINT_REDIS=127.0.0.1:6380","NOVA_SPRINT_REDIS_USER=coordinator","NOVA_SPRINT_REDIS_PASSWORD_ENV=NOVA_REDIS_COORDINATOR_PASSWORD","NOVA_BUS_REDIS=127.0.0.1:6381","nova-sprint","friend","sync","--every","15s","--pg","postgres://nova_config@127.0.0.1:5432/nova"]' --seat bench --keys NOVA_PG_CONFIG_PASSWORD,NOVA_REDIS_COORDINATOR_PASSWORD --keepalive true --as ada
```

Its log is the loop's, `~/nova-bench/loops/friend-sync.log`. It replaces the
hand-written launch agent `com.nova.friend-sync`, a zsh `while` loop around
`friend sync` that read the seat with `where --json | jq`: once the row is
applied and its unit runs, that agent is retired, unloaded and its plist
removed (`launchctl bootout gui/$(id -u)/com.nova.friend-sync`, then
`rm ~/Library/LaunchAgents/com.nova.friend-sync.plist`), so one loop syncs.

### The beat loops are retired, with no replacement

The per-friend beat loops are retired, and nothing replaces them: the launch
agents `com.nova.loop.friend-beat-<friend>`, each a zsh `while` loop running
`nova-sprint friend beat <friend>` every second while the friend's app process
existed. Their beat measured that an app was open, not that she could work: on
2026-10-04 one friend read up for four hours under a refusing harness, and
another read working 8 for an hour while running nothing. They were retired by hand on
2026-10-05: each unloaded and its plist removed, as above
(`launchctl bootout gui/$(id -u)/com.nova.loop.friend-beat-<friend>`, then
`rm ~/Library/LaunchAgents/com.nova.loop.friend-beat-<friend>.plist`). No loop
row, wrapper, flag or other shell loop beats for a friend, and none is to be
added: a "beat while a named process runs" flag is exactly what this forbids
(it supersedes the beat half of card simp-retire-ping-and-beat-loops).

A friend is up only on evidence from her own session (docs/SPEC-FRIEND.md,
"Presence is her session's evidence"): a wake ping her session answered within
ten minutes (the coordinator's ping loop sends `nova-friend ping --wake`; her
daemon pushes it into her session as a turn; her session runs the pong line;
the coordinator writes `friend health --state up`), or a card of hers finished
within thirty (friend sync's collect of the report her session wrote). Her
nova-friend daemon still runs `friend beat` once a second: the sprint records
it and shows its age, and it never makes her up, nor does any other beat sent
for her. Otherwise she reads down, her row naming the evidence missing and the
age of the last of each (`where --json`, `friends[].evidence`). Down does not
move her cards: she keeps the cards dealt to her row and the deadline judges
them; the coordinator takes the unstarted ones back with `nova-sprint friend
take <friend> --all-unstarted` (or `friend down`), and nothing does it by
itself.

## Where a job's work lives

inbox/ and outbox/ hold text: the brief, the report, the evidence. A job's
clones, worktrees and build output live in `jobs/<job>/`, the same `<job>` as
its inbox directory, and nowhere else; the build cache is the friend's one
cache, `.cache/go-build`, never one per job. Every brief to a friend (and every
brief to a coordinator's child) carries this line, with the name and the job
filled in:

```
Work in ~/<name>-working/jobs/<job>/: every clone, worktree and build output goes inside it, GOCACHE=~/<name>-working/.cache/go-build, and the report goes to ~/<name>-working/outbox/<job>/REPORT.md.
```

A clone left inside `inbox/<job>/`, beside its brief (the layout before this
line), is found there too.

## Retention: `nova-sprint friend clean`

A done job's clones are removed by the machine, nightly, by the rule the bench
slots keep (a launch that is done leaves no checkout; docs/SPEC-WORKER.md,
`member`), carried to a friend's jobs (ideas#833; the owner, 2026-10-02:
"cleanup must be auto!"). `nova-sprint friend clean`, for every friend row
(docs/SPEC-SPRINT.md, `friend clean`):

1. A job is `inbox/<job>/` or `jobs/<job>/`; it is done when
   `outbox/<job>/REPORT.md` exists, and its age is that report's.
2. Inside a done job at least 3 days old (`--days`), a clone with nothing
   uncommitted, no stash and no commit missing from every remote-tracking ref
   is removed, and so is build output (`node_modules`, `target`, `gocache`,
   `gocache-*`, `.gocache`, `go-build`, `wt-*`).
3. A clone with work nowhere else is dirty: it is listed in the loop's log
   each night with its path, age and why, and removed once its job is 14 days
   old whatever its state, the removal saying it was dirty.
4. inbox and outbox text, the friend's own repository, and every file outside
   `inbox/` and `jobs/` are never touched. A job not done is never touched,
   however old.
5. The friend's `.cache/go-build` is held under 20 GiB, least recently used
   entries first, as the member holds its pool's.

`--dry-run` prints every removal and listing with the bytes it would free and
removes nothing. The run ends `FRIENDS-CLEAN OK freed=<bytes> listed=<n>`.

It runs from a loop row on the machine that holds the directories, once a day,
reading the roster from the config store:

```
nova-config loop add friend-clean-bench-a --machine bench-a --argv '["/usr/bin/env","NOVA_PG_PASSWORD_ENV=NOVA_PG_CONFIG_PASSWORD","nova-sprint","friend","clean","--pg","postgres://nova_config@localhost:5432/nova"]' --seat bench --keys NOVA_PG_CONFIG_PASSWORD --every 86400 --as ada
```

Its log is the loop's, `~/nova-bench/loops/friend-clean-bench-a.log`.

## The coordinator's loops

A cold coordinator, AI or person, needs only `nova-sprint` and `nova-friend`
and the install lines on this page (the owner, 2026-10-04: "no bash scripts";
"anything you rely on that is a bespoke tool as coordinator, that has to,
absolutely go"; "You should have setup children so you get notified when
nova-sprint sends messages to you."). The coordinator's three earlier scripts
are replaced with verbs:

1. **The seat push loop**: `nova-sprint inbox --wait --push seat` is installed as
   a `nova-config` loop record kept alive on the coordinator machine:

```
nova-config loop add seat-push --machine bench-a --argv '["/usr/bin/env","NOVA_SPRINT_SERVER=127.0.0.1:6390","nova-sprint","inbox","--wait","--push","seat"]' --keepalive true --as ada
```

   Its log is the loop's, `~/nova-bench/loops/seat-push.log`. It writes each new
   judgment and note into the holder's inbox directory,
   `~/<holder>-working/inbox/sprint-judgments/`, and follows the seat when it
   moves. It replaces the hand-written launch agent `com.nova.loop.seat-push-<seat>`
   (a zsh script under `nova-secrets exec`).

2. **The receive thread**: The coordinator is a friend row like any other (the
   sprint row's `coordinator` names her). Her receive thread is her own
   `nova-friend` daemon, installed once:

```
nova-friend install --as ada --harness opencode --dir ~/ada-working --server 127.0.0.1:6390
```

   It parks on her bus stream, pushes incoming messages into the session, and
   beats to the sprint server. Her name has to be a name on the bus store's
   roster (`nova-bus names`). When the bus store is a Redis apart from the
   sprint store, `nova-config apply` does not write it there; friend sync
   writes it at her first deal (`FRIEND-CARD BUS-NAMES added=<name>`;
   docs/SPEC-FRIEND.md, bud-delivery-knows-the-bud-b.w1). It replaces `com.nova.loop.wake-serve-<seat>` and
   `nova-wake`.

3. **Session proof of life**: The 10-minute ping loop
   (`com.nova.loop.friend-ping-<seat>`) is retired in favour of the friends'
   session proof of life (card fr-session-proof-of-life): `nova-friend` tracks
   presence via nonces answered by each friend's session, so no background ping
   script is run. The wake ping to the sessions is the verb `nova-friend ping
   --wake --to-friends --every <d>` (installed with `ping-install`), which
   pings every friend the friends table holds up and tells the coordinator
   which sessions were deaf (docs/SPEC-FRIEND.md, "The wake ping loop").

For new friends, see [FRIEND-ONBOARDING.md](FRIEND-ONBOARDING.md) for step-by-step guidance from joining to a first finished card.
