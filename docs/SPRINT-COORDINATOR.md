# The coordinator's runbook

The runbook for whoever holds the coordinator seat of a sprint: the one actor that answers the inbox, loads
work, holds and releases waves, sets the fleet, lands, and installs. It assumes this file, the `help` of each
tool it names (`nova-sprint`, `nova-config`, `nova-secrets`, `nova-swarm`, `nova-bus`, `nova-friend`,
`nova-update`, `nova-ci`, `tlacheck`) and [SPEC-SPRINT.md](SPEC-SPRINT.md), with `git`, `gh`, `go`, `make`, `jq`, `sops` and
`ansible-playbook` on the PATH; where this file and a help differ, the help is right and this file is the
defect. A change not yet in `dev` is marked "after PR N" and listed under "Open items". Notation: `<m>` a
machine, `<s>` a stream, `<id>` an inbox group id, `<card>` a primary's id. Each judgment answer below is the
line the inbox prints for it, filled in; `nova-sprint inbox --open <id>` shows the exact lines.

## 1. The seat

- The coordinator is the actor the sprint's `init` named: `nova-sprint where --json | jq -r .coordinator`.
  Its verbs are the coordinator's alone ([SPEC-SPRINT.md section 11](SPEC-SPRINT.md#11-verbs)); another actor is
  refused with nothing written. A holder taking over reads the handover file and bus note first (section 9).
- One server writes the sprint: `nova-sprint run --listen <address>:<port> --land`, beside the sprint's Redis,
  on the coordinator machine, as a kept-alive loop row (`nova-config loop list`; here `sprint-server-<m>`). It
  ticks, serves the workers' verbs on the fleet's private address and the coordinator's on
  `127.0.0.1:<port>`, and lands what the readers passed; its supervisor starts a new one when it exits 3 after
  its binary is replaced. [The server](SPEC-SPRINT.md#the-server) says what it serves.
- Every value the seat needs is read from that loop's unit on the coordinator machine, never typed from
  memory. On darwin the unit is `~/Library/LaunchAgents/com.nova.loop.sprint-server-<m>.plist` (on linux
  `systemctl --user cat nova-loop-sprint-server-<m>.service`; [FLEET.md](FLEET.md) has both layouts; the same
  row prints with `nova-config loop show sprint-server-<m>`). Its words are the store (`--store`), the seat
  (`--as`), the key (`--key`), the secret (`--only`), the Redis address, user and password variable name (the
  `env` words), the `--listen` address and the log (`StandardOutPath`):

  ```
  U=$(ls ~/Library/LaunchAgents/com.nova.loop.sprint-server-*.plist)
  A=$(plutil -extract ProgramArguments json -o - "$U")
  word() { echo "$A" | jq -r --arg k "$1" '.[index($k)+1]'; }
  envw() { echo "$A" | jq -r --arg k "$1=" '.[]|select(startswith($k))|sub($k;"")'; }
  STORE=$(word --store); SEAT=$(word --as); KEY=$(word --key); PORT=$(word --listen | sed 's/.*://')
  REDIS=$(envw NOVA_SPRINT_REDIS); RUSER=$(envw NOVA_SPRINT_REDIS_USER); RPW=$(envw NOVA_SPRINT_REDIS_PASSWORD_ENV)
  ```

- Every served verb needs only two variables, and a read (`where`, `inbox`, `card`, `log`) only the first:

  ```
  export NOVA_SPRINT_SERVER=127.0.0.1:$PORT
  export NOVA_SPRINT_ACTOR=$(nova-sprint where --json | jq -r .coordinator)
  ```

- The seat is held only by a session the push loop reaches ([The push proof](SPEC-SPRINT.md#the-push-proof)):
  until it is proven every coordinator verb is refused with `PUSH DOWN` and the setup. A Claude Code session has
  no deliver command, so its seat is on the folder adapter: install it once with the folder the session watches
  (the holder's inbox, `~/<seat>-working/inbox/sprint-judgments`), and the install prints the session's two
  commands:

  ```
  nova-sprint seat install --actor $NOVA_SPRINT_ACTOR --harness claude --target ~/$NOVA_SPRINT_ACTOR-working/inbox/sprint-judgments
  ```

  1. Run as a Monitor, from inside the session, the `monitor:` line it printed (each new file in the folder is
     one event: a judgment, or a `PROOF-<nonce>`):
     `nova-sprint seat watch <folder>`. It prints existing complete files on startup and new files every second,
     one path per flushed line; dot files and directories are skipped.
  2. Answer each `PROOF-<nonce>` the Monitor shows, at once: `nova-sprint seat pong <nonce> --actor $NOVA_SPRINT_ACTOR`.

  The push loop writes a new check every 10 minutes and the seat is down 15 minutes after the last answer;
  `seat push` says `adapter=folder proven=<RFC3339>` when live; `seat check` says `proven=<age> ago` on OK
  and `proven=-` on DOWN. A refusal carries both commands with literal `<nonce>` placeholders. The actual
  nonce appears only in the folder's `PROOF-<nonce>` filename; `seat push --json` reports `proof=pending`
  while a check awaits its answer and never exposes the nonce.
- Not served, and run where typed with credentials of their own: `run`, `tick`, `land`, `play`, `fleet sync`,
  `friend sync`, and any verb given its own `--redis`. `fleet sync` and `friend sync` read the config store, so
  they run under one `nova-secrets exec` wrapper that names variables and never a value;
  [SPRINT-COORDINATOR-SEAT.md](SPRINT-COORDINATOR-SEAT.md) builds it, each value from a command, and shows what
  each command prints. A wrapper that refuses is reported, not worked around.
- The units the coordinator's machine runs (the store, the bus, the server, its member, the push loop, the
  friend sync loop, the table) are each installed by a verb, never written by hand: `nova-redis install store|bus` and `nova-sprint install server|member|seat-push|friend-sync|table`, after card
  every-unit-installed-by-a-verb. `nova-sprint units --check` names each installed, missing or different
  ([FLEET.md](FLEET.md#the-coordinator-machines-units-installed-by-verbs)); a hand-written unit reads as
  different until the seat switches it over.
- The coordinator never starts a second server: `run`, `tick`, `land` and `play` against the store beside the
  running server are a second writer. A server that does not answer is a fault to find (section 8).
- The coordinator never writes the store by hand: no `redis-cli`, no raw ACL command, no edit of a key; every
  change is a verb. What `where` draws is the owner's (`internal/sprint/TABLES.lock`): a change is asked for.
- No secret is printed, pasted or put on a command line. No `rm -rf`.

### coordinator-needs-no-seat-only-tools

The seat runs on nova verbs alone. [COORDINATOR-TOOLS.md](COORDINATOR-TOOLS.md) lists every wrapper, script
and loop a coordinator ran beside them (the seat wrapper, the wake, the dashboard, the friends' beat, ping and
sync loops, the disk guard, the mirror loop and the hand-written units), each mapped to the verb lines that do
the same or to the card that adds the verb; a verb or flag on that page that the tool does not carry fails
`TestEveryCoordinatorToolMapsToARealNovaVerb`. A coordinator who reaches for a tool not on it adds its row first.

## 2. The day's loop

Look, then answer, then look again. `where` draws the tables, `inbox` lists the open judgments and the
HAPPENED notes that need no decision, `card` says what holds one primary:

```
nova-sprint where
nova-sprint inbox
nova-sprint inbox --open <id>
nova-sprint card <card>
nova-sprint log --card <card>
```

Waiting. `nova-sprint inbox --wait [--timeout 10m]` ends at the next tick-end note, so while any judgment
stands open (a sentinel held on purpose, a `wait`) it returns at once: poll `nova-sprint inbox` once a minute
and answer the groups not seen before. After PR 5129 `--wait` ends only for what was not open
when it began, and `nova-sprint inbox --wait --push <dir>`, run as a supervised loop (row in
[FLEET.md](FLEET.md)), writes each new judgment once as `<dir>/<note id>.md`, so nothing polls.

Answering. A decision is its commands, one per line: copy them, fill each `'<...>'`. `--group <id> --expect
<n>` acts on the whole group and refuses, changing nothing, when its size is not the printed one; `--answers
<id>` closes the judgment. The finding or report in `inbox --open <id>` is read first. A judgment that needs
the owner's number or word (a width, a route, a release order) goes to the owner on the bus and stays open.

| the inbox says | the answer | the commands as printed |
|---|---|---|
| a reader found it broken | rework with the finding; each card's fix is its reader's finding. When the finding is wrong, ask another reader | `nova-sprint rework --group <id> --expect <n> --answers <id>`; `nova-sprint ask --group <id> --expect <n> --another --answers <id>` |
| work came back failed | rework; each card's fix is the report its work gave. Drop only a card that cannot be done as written | `nova-sprint rework --group <id> --expect <n> --answers <id>`; `nova-sprint drop --group <id> --expect <n> --reason '<why>' --answers <id>` |
| a primary is blocked on something dropped | ack with the reason when the card can run without it: the ack waives the dropped needs and writes who and when on the card (`card` shows each as waived), and the card is ready at once, or at the next tick on a RUNNING machine, when nothing else holds it (a held sentinel keeps its hold), so a chain behind a dropped card is mended with nothing dropped and added again; you own the risk that it runs without the dropped card's change. Drop the dependents when it cannot | `nova-sprint ack <note>,<note> --reason '<why nothing is to be done>'`; `nova-sprint drop --group <id> --expect <n> --reason '<why>' --answers <id>` |
| stalled | `card <primary>`: its HELD line says what holds it; then the decision the judgment prints (for a primary asked already, `ask --another`) | `nova-sprint card <primary>`; `nova-sprint ask --group <id> --expect <n> --another --answers <id>` |
| stream stopped: conflict on a card | return the card, rework it from the sprint branch's tip, resume the stream | `nova-sprint return <card> --reason conflict`; `nova-sprint rework <card> --fix '<fix>'`; `nova-sprint resume --stream <s> --did 'returned <card> for rework' --answers <id>` |
| a work card is past its deadline | a card dealt and not taken waits or the fleet is levelled; a card taken and not finished goes to the load check of section 4 | `nova-sprint wait <id> --for 30m`; `nova-sprint fleet level`; `nova-sprint fleet down <m>` |
| cannot ask | bring a reader up, or wait while reads end; when machinery that exhausts reads is the cause, drop the card and add it again after the fix | `nova-sprint reader add '<reader>'`; `nova-sprint wait <id> --for 30m`; `nova-sprint drop <card> --reason '<defect and its fix>' --answers <id>` |
| sentinel reached | release only when the gate for that wave is passed | `nova-sprint release <sentinel> --reason '<what you looked at and found>' --answers <id>` |

Notes on the rows:

- Conflict. The stream is stopped until `resume`. A fix text names the cause and the way out: "Your head does
  not merge onto the sprint branch's tip: a file you touched changed after your base. Start again from the
  current tip, redo only the lines your card lists, and lower a ledger's ceiling from its value at the tip by
  exactly your own count." The other printed decision is `resume --stream <s> --did '<what you did>' --answers <id>`:
  land merges the card's head again, and a conflict only in the generated ledgers resolves itself (land regenerates
  them). A conflict outside the ledgers is answered by rework or drop, never by a resolution on the card's branch,
  which no reader has read.
- Past its deadline. A work card not taken is late 15 minutes after it was dealt (counted from the first deal
  since its last take); a card taken and not finished, 2 hours after its first take; a read, 30 minutes asked
  or 2 hours begun ([SPEC-SPRINT.md section 14](SPEC-SPRINT.md#14-the-machine)). A card dealt and not taken
  is the member's queue, not the card's fault: `wait` while the member works through its width, or
  `fleet level` to move cards a member cannot start to one with free lanes. `fleet down <m>` is for the member
  that has held the card its whole deadline.
- Cannot ask. A flash card needs one reader up and a pro card two different readers up (`where` shows the readers table). A drop on this ground
  goes on the re-add list (section 3); fixes for the machinery are never made through the sprint.
- Sentinel reached. The other printed decisions are `add --stream <s> --before <sentinel> '<new id>' --brief
  '<brief>'` and `drop`. A judgment held for a wave not yet agreed stays open, never released to clear the
  inbox.
- Every other type is answered by the line the inbox prints (`nova-sprint help`, "one answer to each
  judgment"; [SPEC-SPRINT.md section 8](SPEC-SPRINT.md#8-notifications) lists each type, its answers and
  whether `ack` is one): ready to accept (`accept`), stream branch red, merge queue rejected, needs another
  stream first (`rank <other> --first`), ci red, reads exhausted (`ask --another`), a card at its bound,
  stranded in review, fewer than two readers up (`reader up`), no member up, an operation stuck (`check`).
- Before PR 5129 the printed `ack` line with several notes is refused: ack one note at a time.

The routine kinds are answered by `nova-sprint answer` (SPEC-SPRINT.md section 8, "Answered by
nova-decide"), never by a shell loop of the coordinator's own: the night of 2026-10-02 one such loop read one card
of a grouped judgment and every stream waited under it until morning. Run it as the seat's loop, a row of its own
beside the server, with the key from the seat's secrets and never on a command line:

```
nova-secrets exec --only JEV_API_KEY -- nova-sprint answer --dry-run
nova-secrets exec --only JEV_API_KEY -- nova-sprint answer --every 60s
```

It answers each card of a broken, failed, blocked, stalled, conflict, deadline, cannot-ask, ready-to-accept or
bound judgment by itself: the verb nova-decide chose is applied, by the line the inbox prints for that card, when
its probability is at or above `decide_judgment_bar` (nova-config's sprint row: `nova-config sprint set
--decide_judgment_bar <p> --as <coordinator>`, then `nova-config apply --kind sprint`); `--bar <p>` gives one for
a run. The row ships it empty, and with no bar nothing is applied: every decision is recorded and each row says
what a bar would apply, so the record trains first. 0.8 is a starting point measured on 100 of the coordinator's own judgments, not an independent calibration (SPEC-NOVA-DECIDE.md section 13: 59
of 100 the coordinator's own verb; at 0.8 it applies 40, 39 of them the coordinator's; on blocked, 3 of 21, and
bound, 3 of 13, Jev says drop, and a drop is never applied). What it lists is yours, each with why: every drop (with the reason the
decision chose), everything under the bar, a verb the judgment does not print, a card applied before whose
judgment is still open, a card it reworked within the last hour, and a provider refusal for want of payment (402, out of credit), which it never asks
about: a payment is the owner's, so it goes to the owner on the bus. A sentinel and every other kind are left. A
card is decided once (the record, `~/nova-sprint/decide/judgment.jsonl`, answers it again), so the loop never reworks a
card round and round; each decision's outcome (landed, dropped, came back) is attached as the card's state says
it, and `nova-decide calibrate --record ~/nova-sprint/decide/judgment.jsonl --decision judgment --question
verb=rework --positive landed --negative came-back` reads the bar the record supports. The loop ends when the
machine is STOPPED; it never composes a command the inbox did not print. Each line it applies carries the
decision's `--op`, recorded as applying before it runs and applied after, so a loop stopped between the two
applies nothing twice; one ask that takes past `--timeout` (60s) is that card's failed row, and the loop goes on.

## 3. Loading work

- A stream is one line of cards: `nova-sprint add --stream <s> ...` opens it. Cards that touch one file or
  ledger belong to one stream, in order or chained with `--needs a,b` (primaries that must land first; a
  dropped or missing need raises its own judgment); `add` does not see two open cards naming one file.
- A card is a brief, a child's whole brief (at most 16 KiB; `REPO:` and `BASE:` lines; a a `tier: pro|frontier`
  line 1 is its ceiling, none is flash: every card is dealt on flash first and the machine escalates it a tier
  at its bound below the ceiling). From files, the card's id being the file's name without `.md`:

  ```
  nova-sprint add --stream <s> --brief-dir <dir>
  nova-sprint add --stream <s> --brief-file a.md --brief-file b.md
  nova-sprint add --stream <s> --brief-dir <dir> --rules fleet/child-rules.txt
  ```

  `add` holds each brief to the card lint and refuses, writing nothing, one that fails.
  `nova-swarm template --name card` prints a card that passes once its `<...>` lines are filled;
  `nova-swarm lint --card <file> --child-rules` checks a file first; `nova-sprint init --rules <file>` records
  the rule set `add` uses by default. Under `fleet/child-rules.txt`, a file the members hold, a card on
  nova-tools (or on a repository with its own `fleet/child-rules.<repo>.txt`) does not carry the rules: the
  member injects them at stage time (rules by reference, docs/SPEC-SPRINT.md section 2), and the lint refuses
  only a line that contradicts them; a card on another repository carries its own. Release the members
  before the coordinator's `nova-sprint`. A card with no brief is admitted with a NOTE and given one before it is
  dealt: `nova-sprint stop`, `nova-sprint brief <card> --brief-file <path>`, `nova-sprint start`; a card that
  has started refuses a new brief.
- Sentinels hold waves. `nova-sprint add --stream <s> --sentinel <s>-wave2` puts a stop in the line; what
  sorts after it waits (`add` prints `waits behind sentinel <id>`). `--before <id>` and `--after <id>` place
  one among cards; `--count <n> --sentinel-every <k> [--sentinel-last]` places many. A wave is loaded in
  full behind its sentinel, so ready stays full while waves wait. A sentinel raises `sentinel reached` once
  what it waits for has landed; one with nothing before it is reached at once, and that judgment stays open
  until the release (after PR 5099 it is raised only when nothing else moves, and
  `nova-sprint wait <id> --for 24h` quiets it). [SPEC-SPRINT.md section 16](SPEC-SPRINT.md#16-sentinel-cards)
  holds the rules.
- Releasing. `nova-sprint release <sentinel> --reason '<what you looked at and found>'` lands the sentinel and
  moves what waited behind it, up to the next sentinel, to ready. It is run only when the gate for that wave
  is passed, in the order agreed with the owner. A sentinel not yet reached is released when each card it
  waits for has landed, was dropped, or is in flight (taken, in review, merging), so a starving fleet frees
  the cards behind work in flight in one step; while any has not started it is refused, naming the first.
- Repair cards go to the front of their stream: `nova-sprint add --stream <s> --before <first card> <id>
  --brief-file <path>`, or for a card already added `nova-sprint rank <id> --first`
  (`nova-sprint move <id>... --stream <s>` moves cards between streams). A fix for the machinery itself is a
  pull request, never a card.
- The re-add list is a file the coordinator keeps (card id, brief file, reason, the fix it waits for); after the
  fix is installed each card is added again from its brief file.

## 4. The fleet

- Widths are the owner's numbers, set in nova-config and applied as written; no formula decides one.
  A member's width is its machine row's `width` (0 is no member). To change one:

  ```
  nova-config machine set <m> --width <n> --as <coordinator> --dry-run
  nova-config machine set <m> --width <n> --as <coordinator>
  nova-config apply --kind machine --as <coordinator>
  nova-sprint fleet sync --check
  nova-sprint fleet sync
  ```

  `nova-config machine width <m>` reads a row. `fleet sync` sets a member's width from its row, so a width
  changed with `fleet up <m> --width <n>` is set back by the next sync. A member holds up to twice its width,
  working and ready together. A sprint starts at a low width (real cards build and test) and the owner raises
  it by the `load` column.
- Readers equal workers: a machine's reader width is its member width, one number. A reader is a loop row
  (`nova-swarm member --reader`) under a readers row of its name; `nova-sprint reader add <reader>` adds the
  row. Today its width is the loop row's `--width` (`nova-config loop set <loop> --width <n> --as
  <coordinator>`, `nova-config apply --kind loop`, then the loops play of section 7), set to the machine's
  width; after PR 5124 a reader runs at its machine's width and no loop row carries one.
- Up and down. `nova-sprint fleet down <m>` holds a member whatever it beats: its unfinished cards are dealt
  to the members that are up, and what its children finish afterwards is not reported. `fleet up <m>` releases
  the hold; `nova-sprint log --member <m>` says where cards went. A machine that does not answer is held and
  reported. A member whose binary was replaced drains by itself (no new card, running cards finished and
  reported, exit 3, restart), so an install needs no hold ([section 5](SPEC-SPRINT.md#5-the-fleet) of the spec).
- The signs of a smothered machine, all measured on a loaded one: the `load` cell of `where` (CPU busy
  percent of all cores, highest of the last 10 seconds) near 100 for minutes; `top` showing system CPU far
  above user CPU while the disk is idle (`iostat`), the kernel saturated by the builds and test binaries of
  the cards; staging slow, the `STAGE` lines in the slots' `*.native.log` files carrying tens of seconds for
  a clone that takes a few on a quiet machine; the member and reader logs saying `stage-timeout` (the stage
  wall is 120 seconds) or `staging refused`, and reads ending with no verdict.
- The deadline failures it causes: cards dealt to it are not taken within 15 minutes; cards taken are not
  finished within 2 hours; reads are late at 30 minutes or 2 hours; each staging refusal withdraws the card
  and deals it to another member without spending its redeals, so the storm follows it; a member that misses
  3 beat windows of 15 seconds is down and its cards are dealt again. The cure is a lower width, the owner's
  number: put the load evidence on the bus, and hold the member with `fleet down <m>` as the stopgap, said so.

## 5. Routes

- `nova-sprint routes` prints each tier, each route, and its attempts, ok, failed, provider failures and mean
  wall. Routes are nova-config rows applied to the store (`nova-config route list`, `route show <name>`). The
  tiers are flash and pro; a frontier card is the coordinator's and is never dealt. A tier's order is
  `nova-config tier set <tier> --routes <a>,<b>,<a> --as <coordinator>`, a route named twice taking two turns.
  Flash first on every card (cost rule 1, nova-tools#5174): line 1 of its brief is its ceiling, never its
  first deal. An attempt at its bound below the ceiling is escalated by the machine (`tier_now`, a new
  attempt on the next tier, no judgment); at the ceiling the bound is your judgment. `rework <card> --tier
  <tier>` pins the card to a tier, its ceiling too, never escalated; `card <id>` prints `tier=` and
  `ceiling=` on its CARD OK line.
- A provider's failure is not a verdict on a route: a run the provider failed is redealt, never failed work,
  leaving out the routes already drawn for the card. A limit or an empty balance never takes a route out of
  the deal; it clears by itself, and a route taken out for it stays out.
- The machine rests a route by itself when three of its last ten ended takes left no result (rule 3 of
  nova-tools#5174): no work card is drawn on it for 30 minutes, the inbox says so with the cards, and
  `nova-sprint routes` prints `rested_until`. That rest is the sprint's and ends by itself; the row change
  below is yours, for work that comes back bad.
- A route rests when measured work on it is bad: its ok and failed counts from `nova-sprint routes` against
  the other routes of the tier, over the whole sprint. Resting is a reversible row change (`--dry-run` first on
  each command); bringing the route back is `--enabled true` and the same apply:

  ```
  nova-config route set <name> --enabled false --as <coordinator>
  nova-config apply --kind route --as <coordinator>
  ```

- The reason is recorded where a later reader finds it. A route row has no note field, so each change is one
  comment on the routes issue naming the route, the config revision the command printed (`CONFIG SET
  kind=route name=<name> rev=<n>`), the measured counts, who asked for it, and the condition that brings it
  back; `nova-config route history <name>` shows who and when. Routes and widths are the owner's to set.

## 6. Landing

The server's `--land` merges each card whose reads passed (one for a flash card, two for a pro card) into the sprint branch, the branch the cards'
`BASE:` line names. The sprint branch reaches the integration branch (`dev`) as a batch.

- Before it: every repair card has landed, and a reader that did not write it has read the whole diff of the
  sprint branch against `dev`, hunk by hunk. A bad hunk is fixed by a repair card on the sprint or sent out
  to a friend, never fixed inside the merge. Per-card CI runs only the touched packages, so a docs card can
  break the tests of another package that read the doc (nova-tools#5111): the merged tree runs those too.
- A batch is one branch from the tip of `dev`, every approved head merged in, tested once, landed once, in a
  fresh clone, never the working checkout. A head is approved after two cold reads say LAND; a HOLD is
  answered by its author with new commits and read again. Never force-push, squash or rebase a shared
  branch; a head's branch belongs to its author, so `dev` is merged into it and it is never rewritten.

  ```
  git clone https://github.com/mas-bandwidth/nova-tools <work>/src
  git -C <work>/src fetch origin dev <sprint branch> <head branch>...
  git -C <work>/src checkout -b lander/<date> origin/dev
  git -C <work>/src merge --no-ff origin/<sprint branch>
  git -C <work>/src merge --no-ff origin/<head branch>
  ```

- Conflicts. Only a ledger or allowlist under `internal/ci/testdata`, or `internal/sprint/TABLES.lock`, is
  resolved in the merge; any other conflict leaves that head out and goes back to its author. The stages are
  `git show :1:<file>` (base), `:2:` (ours), `:3:` (theirs). A counted ledger (a `# ceiling: N` line and rows):
  ceiling = ours + theirs - base, since both sides' removals count; rows = the three-way line set, the rows of
  ours and theirs less every row either side removed from base. A plain line-set file takes the row rule.
- Gates on the merged tree, niced, one at a time (several at once make the load-sensitive tests flake):

  ```
  make fmt
  go build ./...
  go vet ./...
  GOOS=linux go vet ./...
  GOOS=windows go vet ./...
  go vet -tags functional ./...
  GOFLAGS=-mod=readonly NOVA_TEST_NO_HOST=1 nice -n 10 go test -p 2 -count=1 ./internal/ci/
  nova-ci local --base origin/dev
  nova-ci local --base origin/dev --functional
  git grep -l '<changed doc>' -- '*_test.go'
  ```

  `nova-ci local` runs the unit tier CI would run for the batch's diff; the last line finds the packages whose
  tests read a changed doc, each run by name with `go test -p 2 -count=1 ./<package>`. A red that is green on
  both parents is a merge interaction: stop and say so. A changed `.tla` needs its records refreshed on a
  bench, never the coordinator's machine: `go run ./tools/tlacheck groups --stale` names the groups to run,
  and `go run ./tools/tlacheck merge --out tla/RUNS.tsv --keep tla/RUNS.tsv <runs>...` joins the records.
- `git push origin lander/<date>`, then `gh pr create --base dev --title 'Batch: <heads>' --body-file <file>`,
  the body listing each head's sha, its reads, and the last line of each gate; `gh pr checks <number>
  --watch` follows CI. Every job has a 2-minute cap: a darwin leg canceled under load with every test passing
  is rerun when the machine is quieter, `gh run rerun <run id> --failed`.
- Land with `gh pr merge <number> --merge`. `dev` has a merge queue that runs the functional tier the pull
  request does not; when it ejects the batch, `gh run list --event merge_group --limit 5` and
  `gh run view <run id> --log-failed` name the failing test. A red on `dev`'s own tip is fixed first by a
  small pull request of its own.
- After it: close each head the batch carried (`gh pr close <number> --comment 'carried by <batch number>'`);
  merge `dev` into the sprint branch (`git merge origin/dev`) and push it plain, so cards land on a tip that
  includes `dev`; a rejected push is fetched and merged again, never forced. Then the install.
- A refusal at any step (a protected branch, a queue rule, a permission) is terminal: say so on the bus.

## 7. The install after a landing

[FLEET.md](FLEET.md) is the adopter's path; this is the order after a landing. Every play runs from a checkout
at the landed sha, with `NOVA_SPRINT_REDIS` naming the store for the inventory, and has a `--check` form.

1. Build and install the tools. The tools play builds the release once and installs it on every machine in
   scope, then runs `nova-config migrate` and `nova-redis fn load` on the store deployer:

   ```
   ANSIBLE_INVENTORY_UNPARSED_FAILED=true ansible-playbook -i ./nova-inventory fleet/tools.yml \
     -e nova_version=<version> -e nova_source=<checkout at the sha> -e nova_dogfood_receipts=<dir> \
     --check --diff </dev/null 2>&1 | cat
   ```

   then the same without `--check --diff`; `--limit <m>,localhost` installs on one machine. A dogfood gate
   that fails on open edges is waived with
   `-e '{"nova_release_gate_args": ["--no-dogfood-gate", "--reason", "<why>"]}'`, the reason (printed and
   written into the changelog) naming what is installed. Meanwhile the server exits 3 at a tick boundary and
   its supervisor restarts it on the new build, a landing in flight is recovered by the next land, and members
   drain and restart.
2. Config. `nova-config migrate --dry-run` says `ready=yes` or names what refuses; migration numbers run
   1, 2, 3 with no gap, so when two landed changes carry one number the second renumbers. Then
   `nova-config apply --dry-run --as <coordinator>` and `nova-config apply --as <coordinator>`.
3. The loops play, where a unit's template or record changed, one machine at a time and only where that
   machine's member has working 0 in `where`: it restarts every unit whose file differs, and a unit restart
   does not drain a member (nova-tools#5096 item 26), so the cards it holds are lost.

   ```
   ansible-playbook -i ./nova-inventory fleet/loops.yml --limit <m> --check --diff </dev/null 2>&1 | cat
   ansible-playbook -i ./nova-inventory fleet/loops.yml --limit <m> </dev/null 2>&1 | cat
   ```

4. `nova-sprint fleet sync --check`, then `nova-sprint fleet sync` and `nova-sprint friend sync` under the
   seat wrapper. Check: `nova-update version` on each machine; `nova-sprint where` shows the members up with
   loads; `nova-sprint check` prints each violated invariant, none when healthy. Then the re-add list (section 3), and the friends' loops on.

### Adopting the seat build

`nova-sprint adopt <version|path> --source <checkout> --inventory <file> --reason <text>` runs the seat's
`fleet/tools.yml` play. Use `--dry-run` to check the play without writing. The new build's live manifest,
shadow tick and friend install checks run before the old server and member stop. The play then migrates the
configuration store as its owning role, loads the function library, installs the tools and restarts the
agents. A refusal after the window opens restores the previous tools and library and restarts the old agents;
the configuration migration is not undone. The command names the failed step and what the rollback did.
The tick does not invoke adoption. See [SPEC-SPRINT.md](SPEC-SPRINT.md), "Adopting a build", for the play's
checks, receipts and exit codes.

## 8. The hourly habits

- The bus. `nova-bus peek --as <coordinator>` lists what waits for the coordinator; `nova-bus recv`
  takes one in, answered with `nova-bus send --re <id>` and acked.
- Friends' outboxes. A friend works only inside its own directory: a job is delivered as
  `~/<friend>-working/inbox/<job>/BRIEF.md` and collected from `~/<friend>-working/outbox/<job>/REPORT.md`,
  whose `Verdict:` line holds the result. After PR 5126 `nova-sprint friend sync [--root <dir>]` reads those
  directories into the friends table (failed when the verdict's first word is HOLD, FAIL, FAILED or BROKEN);
  `friend down <friend>` holds a friend and `friend up` releases it.
- Disk. Every per-card artifact is removed by the machinery itself, automatically and asynchronously; a scan
  at a random time is not a plan. `df -h <slots volume>` and `du -sh <slots dir>` read the trend. A member
  starts no card while its volume has less than `--disk-floor` GiB free (default 10). After PR 5125 the member
  retires finished slots and holds the slots directory under `--slots-max-gb`; before it nothing removes them
  and the directory grows by about half a gigabyte a card. A filling volume is reported with the numbers and
  its member held with `fleet down <m>`.
- The server. While the machine is RUNNING its log (`StandardOutPath` of the unit) prints `HH:MM:SS tick` at
  least once a second. Three signs of a stall: the last `tick` line is more than 15 seconds old (`inbox` also
  shows `the machine is not ticking`); the server's resident size grows (`ps -o rss=,etime= -p <pid>` twice, a
  minute apart) while no tick prints; a store verb does not return (run `where`, `card` and `inbox` with a
  time limit). Members and readers log `did not answer`. A stalled server is reported to the owner with the
  log's last lines, the process line and the clock; the store needs no repair. To restart, with the owner
  told, take the stack first: `kill -QUIT <pid>` writes every goroutine to the log and ends the process, and
  the supervisor starts a fresh server on the installed binary.

## 9. Handing the seat over

The next holder reads one file, written the hour before the handover and dated from `date`, in its inbox
directory, and a bus note pointing to it. It holds, each item with its id and evidence:

- The open judgments: group id, kind, the cards, what each waits for and from whom; those held for the owner
  are marked.
- The held waves: each sentinel, the gate its wave waits behind, what has passed and what has not, and the
  release order agreed with the owner, in the owner's words. The pull requests in flight: number, head sha,
  reads given and owed, gates run with their last lines.
- The numbers and why: each machine's width and the load it ran at; each route resting, with the measurement,
  the config revision and the condition that brings it back; the owner's rulings, with date and words.
- The re-add list and the install state: the sha installed on each machine, the plays pending, the migrations
  applied, the server's loop row and log path; the friends' jobs out (friend, job, delivered, due).
- The stall count: what was tried for each problem that came back, how many times, how it failed; and
  anything refused or left to the owner on the bus, so another route is not tried.

`nova-sprint handover` prints most of that file from the store (the seat, streams, sentinels, open judgments,
held members, disabled routes, the last decisions, the first commands); the file adds what the store does not
hold. The next holder reads section 10, the rules, before the first decision.

The seat: the next holder runs under the actor the store names; the first `init` sets it and no later `init`
changes it ([SPEC-SPRINT.md section 11](SPEC-SPRINT.md#11-verbs)). `nova-config sprint set --coordinator
<friend>` is the deal's and routing's handover, a separate fact. The previous holder stops when the note is sent.

## 10. The rules

The coordinator's operating rules, numbered. Each is the rule in bold, why (the failure it prevents), what
does it (the verb or setting; "judgment" where the seat decides and no verb acts), and the card that makes it
mechanical where one is named. They were learned running sprints from 2026-10-02 to 2026-10-05; a rule the
machine already keeps is still listed when the coordinator must know it to read what the machine did.
`TestEveryCoordinatorRuleNamesVerbsThatExist` fails when a rule names a verb, flag or setting the tools do not
carry. `nova-sprint handover` prints each rule's number and sentence (after the card in "Open items").

### Friends

R1. **A friend is working only when its cards go from working to done; awake is not working.**
    Why: friends answered every ping and held cards for hours with no card finished.
    Done by: `nova-sprint friend cards <friend>`; `nova-sprint set --friend-finish <duration>` (past it the
    pass asks the friend what blocks it, and the blocker is removed).

R2. **A friend whose session does not answer a wake ping is woken and fixed on the pass that sees it, never
left overnight.**
    Why: a friend slept deaf all night with work assigned to it.
    Done by: `nova-friend ping --as <coordinator> --to <friend>`; `nova-friend wait-pong --from <friend> --nonce
    <n>`; `nova-friend ping-install --as <coordinator> --every <duration>`. A cause on the owner's side (credit,
    keys, an account) is `nova-sprint friend down <friend> --reason <text>`, so its cards move, and a note to the
    owner.

R3. **Every 10 minutes the friend chain is walked, each friend to its lowest failing link: up, hears,
delivered, started, progressing, finished, returned, balanced.**
    Why: a broken link was found by the owner, late, because nobody looked.
    Done by: `nova-friend check --as <coordinator>`; `nova-sprint view coordinator --json`; `nova-sprint watch
    --wake --check 10m`. A problem found by hand becomes a push the same day.

R4. **A returning friend or machine is brought up by evidence: its own beat, a pong, a card finished, never
an expectation.**
    Why: reports said friends were up that were not, and returning ones stayed down.
    Done by: `nova-friend check --as <coordinator> <friend>`; `nova-sprint friend up <friend>`; `nova-sprint
    fleet up <machine>`.

R5. **Friends work only in their own real working directory: jobs arrive in its inbox, results leave in its
outbox, and a brief names no path outside it.**
    Why: friends trampled each other's files, and symlinked directories broke on every move.
    Done by: `nova-sprint friend sync --root <dir>`; `nova-sprint friend reconcile <friend> --root <dir>`.

R6. **An order to the coordinator (stop, conserve, sleep) is the coordinator's alone; friends change only on an
order to all friends or to them by name.**
    Why: one order meant for the coordinator stopped the whole team.
    Done by: judgment.

R7. **A silent friend is debugged by the coordinator in order: the bus, the daemon's push, then the friend's own
window; the owner is never handed text to paste.**
    Why: the owner was left relaying messages by hand.
    Done by: `nova-bus peek --as <coordinator>`; `nova-friend check --as <coordinator> <friend>`; `nova-friend
    status --as <friend> --dir <dir>`.

R8. **Shared resources (machines, branches, ports, accounts) are claimed through coordinator verbs with a lease,
never friend to friend; no friend is told to wait on another.**
    Why: a hand-shaken hold starved a friend when its holder went down.
    Done by: `nova-sprint lane take <kind> --machine <m> --as <worker>`; `nova-sprint lane give <kind> --machine
    <m> --as <worker>`; `nova-sprint hold <member> --reason <text>`.

R9. **Before the coordinator goes dark, each friend gets a note naming its work; a friend that is out has its
critical work continued on its own branch.**
    Why: friends drifted without a coordinator.
    Done by: `nova-bus send --to <friend> --subject <s> --body <text>`; `nova-sprint friend take <friend>
    --all-unstarted --reason <text>`.

### Dealing and the fleet

R10. **Idle lanes are levelled before full queues are topped up.**
    Why: work was added to members with full queues while other lanes sat empty.
    Done by: `nova-sprint fleet level`; `nova-sprint friend level`; `nova-sprint set --alarm-fleet <percent>`.

R11. **A wave is never one chain: width comes from independent work.**
    Why: work queued behind one fix that only one card of the wave needed.
    Done by: `nova-sprint needs --roots`; `nova-sprint add --stream <s> --brief-dir <dir>`.

R12. **Every sprint feature is an operation on table rows or a queue between tables, or it is left out.**
    Why: added mechanisms made the design opaque.
    Done by: judgment.

R13. **Most work goes through the sprint, friend work included, as cards in streams.**
    Why: hand briefs made cost and progress invisible.
    Done by: `nova-sprint add --stream <s> --brief-dir <dir>`; `nova-sprint friend cards <friend>`.

R14. **Friends get the judgment work (design, ratings, audits); the fleet gets the mechanical work.**
    Why: judgment work failed on mechanical routes.
    Done by: `nova-config friend set <friend> --tiers <tiers> --as <coordinator>`; `nova-sprint brief <id> --tier
    frontier`.

R15. **A new sprint opens with its simplest mechanical class on flash, with judgment cards held behind a
sentinel.**
    Why: complex first cards failed expensively.
    Done by: `nova-sprint add --stream <s> --sentinel <id>`; `nova-sprint release <sentinel> --reason <text>`.

R16. **Before a load test, shares, guards, probes and one and a half times the supply are lined up, with
capacity in config, never in arguments or constants.**
    Why: changing settings under load went badly.
    Done by: `nova-config machine set <m> --width <n> --as <coordinator>`; `nova-sprint fleet sync --check`.

R17. **Speed comes from width (more machines, fewer attempts per card), not from per-card latency.**
    Why: chasing card latency did not raise throughput.
    Done by: `nova-sprint stats`.

R18. **A CI leg runs only on a machine that runs its packages in under 2 minutes; slower machines run cards.**
    Why: old machines could not hold the CI bar.
    Done by: `nova-config machine set <m> --runners 0 --as <coordinator>`.

R19. **A setting is changed at its source, the config row, never only in the store, where the next sync
overwrites it.**
    Why: store-only edits were silently reverted.
    Done by: `nova-config machine set <m> --width <n> --as <coordinator>`; `nova-config apply --kind machine --as
    <coordinator>`.

### Branches, landing and adoption

R20. **Every fix lands on the one sprint base first; the live server is built only from that base; any other
branch a card names is folded into the base every pass.**
    Why: the live server ran from a side branch a thousand commits apart from the base.
    Done by: `nova-sprint bases`; `nova-sprint server switch <binary>` (only a binary whose commit is on the
    base).

R21. **The base is promoted to dev continually, every 20 to 30 minutes or every 25 landings.**
    Why: stream branches and dev drifted apart.
    Done by: `nova-sprint promote --every 30m --landings 25`; `nova-sprint promoted --sha <merge sha>`.

R22. **Every adoption reaches every fleet machine in the same step, funded or not, and a machine back from
down adopts the latest release before it is dealt.**
    Why: idle machines fell behind and came back running old builds.
    Done by: the tools play of section 7 with no `--limit`; `nova-sprint fleet sync --check`; `nova-sprint fleet
    up <machine>` only after its install.

R23. **Before cutting a repo, a long-lived branch or an integration branch, say what stops landing on the old
line and when; a branch that keeps receiving its source's changes is a fork.**
    Why: branches knotted together.
    Done by: judgment.

R24. **Adoption is the last step of every build: merge, update every machine, restart what runs it, announce
it, use it that day, and turn each friction into a card.**
    Why: built tools went unused.
    Done by: `nova-bus send --to <all> --subject <s> --body <text>`; `nova-sprint add --stream <s> --brief-dir
    <dir>`.

R25. **Every adoption is followed the same day by a dogfood receipt: real use, each gap filed with its command
and output.**
    Why: adoption without use hid gaps.
    Done by: judgment.

R26. **A tool or loop is called used only when a dated log shows deliveries; a loop with no delivery in a day is
stopped.**
    Why: loops refused silently for days while they showed as running.
    Done by: `nova-sprint log --since <10m|RFC3339>`; `nova-bus log --max <n>`.

R27. **A replacement tool takes over only after every user has sent and received on it and its friction list is
empty; the old one stays read-only for the record.**
    Why: switchovers stranded users.
    Done by: judgment.

R28. **A thing is not built with itself until it is built and reliable: fix it by hand or by child agents,
install it, then resume.**
    Why: a broken sprint was used to fix itself.
    Done by: judgment.

R29. **A settled layer is landed, split to its own boundary (repo, module or release line), and the moving work
is built above it; the settled layer changes only by deliberate release.**
    Why: settled and moving work tangled.
    Done by: judgment.

R30. **Everything in nova-tools is useful without nova-sprint, in general terms; nova-sprint depends on
nova-tools, never the reverse.**
    Why: sprint opinions leaked into the general kit.
    Done by: judgment.

R31. **Layers are built from the bottom, one at a time, each through its gate before anything above starts;
later layers' cards are held until the layer below locks.**
    Why: earlier rebuilds failed without checked foundations.
    Done by: `nova-sprint add --stream <s> --sentinel <id>`; `nova-sprint release <sentinel> --reason <text>`.

R32. **When an upper layer needs something new below, pause, extend the lower layer under its own gate, then
resume.**
    Why: workarounds piled up above broken layers.
    Done by: `nova-sprint hold <stream> --reason <text>`; `nova-sprint unhold <stream> --reason <text>`.

R33. **What the owner states in words is locked as checked invariants; a lock changes only on the owner's own
words.**
    Why: the core machine's design drifted.
    Done by: judgment.

R34. **A release is a fast-forward promotion of a green revision on main, after a cold audit at that sha and
dogfooding on real work other than the tool itself; green CI alone never releases.**
    Why: releases went out on branch verdicts.
    Done by: judgment.

R35. **Release notes are written by an author and read cold before the cut, never generated.**
    Why: generated notes said nothing useful.
    Done by: judgment.

R36. **Docs, help and README work gets a final prose pass by the strongest prose author before rating and
landing.**
    Why: uneven prose lowered read ratings.
    Done by: judgment.

R37. **The next sprint opens only when every pull request of the last is landed, closed with a reason, or owned
with a named next step.**
    Why: unprocessed piles grew from sprint to sprint.
    Done by: `nova-sprint where --all`; `nova-sprint clear --confirm sprint`.

R38. **A branch switch never happens in a clone that live loops run from; edits go in a separate clone.**
    Why: a branch switch deleted live scripts.
    Done by: judgment.

R39. **Force-push happens only with a lease, on the agents' own branches; rewriting release-line history needs
the owner.**
    Why: commits were lost.
    Done by: judgment.

R40. **Only the coordinator merges on the forge, on named reads; while agents share one identity, an unexplained
action is traced among the agents before it is reported.**
    Why: merges and tags could not be traced.
    Done by: `nova-sprint land --dry-run`; `nova-sprint log --card <id>`.

R41. **Anything that matters is committed to git; working directories are not backed up.**
    Why: work was lost on disk.
    Done by: judgment.

R42. **When streams stick, look across streams for the common failure class and fix the class.**
    Why: per-card fixes repeated.
    Done by: `nova-sprint where`; `nova-sprint inbox`.

R43. **A pull request held up by repeated rounds stops: the reviewed head lands as it is and a follow-up card is
cut for the rest.**
    Why: fix rounds never ended on a moving base.
    Done by: `nova-sprint accept <id>`; `nova-sprint add --stream <s> --brief-file <path>`.

R44. **Landed sprint work is reviewed stream by stream before the sprint branch merges into the release line; a
bad hunk becomes a repair card.**
    Why: mechanical work risks bad code.
    Done by: `nova-sprint add --stream <s> --before <id> --brief-file <path>`.

### Cards and briefs

R45. **The same finding twice is a brief defect: fix the brief or drop the card, never rework it a third time.**
    Why: one card was reworked 262 times on one finding.
    Done by: `nova-sprint brief <id> --brief-file <path>`; `nova-sprint recut <id> --brief-file <path>`;
    `nova-sprint set --attempts <n>`.

R46. **Work a card needs outside its PATHS means a twin card with PATHS widened, never a diff outside them.**
    Why: the lander refuses files outside PATHS, and the attempt was spent.
    Done by: `nova-sprint recut <id> --brief-file <path> --new <id>`; `nova-sprint relink <old-id> <new-id>
    --reason <text>`.

R47. **A uniform failure shape across one model's cards is our contract failing (prompt or wrapper), fixed per
model family; a model is dropped only on a measured quality floor with varied failures.**
    Why: capable models were dropped for our own format bugs.
    Done by: `nova-sprint routes`; `nova-config route set <route> --enabled false --note <why> --as
    <coordinator>`.

R48. **Every cold read runs at least one temporal probe (land then reopen, claim during resolve), and each
reproduction is kept as a test.**
    Why: defects in sequences escaped happy-path reads.
    Done by: `nova-swarm template --name read`.

R49. **A score under 10 names its reasons and the work that would reach 10.**
    Why: low scores gave no path to a fix.
    Done by: judgment.

R50. **The read gate is judged only by periodic cold audits of landed work by a stronger reader, repeated when
the card mix changes, never by pass rates.**
    Why: pass rates misjudged reader quality.
    Done by: `nova-sprint stream set <s> --read-tier pro`.

R51. **A brief states its cost bound as a number the gate prints and asserts, with the model, the tests and the
size target.**
    Why: requirements were met only after rework.
    Done by: `nova-swarm lint --card <file>`.

R52. **Cards are sized by tier: one mechanical change with an existing test for flash, whole things for pro and
above, refined by measurement.**
    Why: tiny cards paid a fixed toll each; big cards failed on weak models.
    Done by: `nova-sprint brief <id> --tier pro`.

R53. **The card fits the executor's model class: small, fully specified work with the probes written in for
weaker models.**
    Why: loose tasks produced overclaiming reports.
    Done by: `nova-swarm lint --card <file>`.

R54. **Every card extends a class test as its executable spec.**
    Why: fixes were not pinned.
    Done by: `nova-swarm lint --card <file>`.

R55. **One card runs through before many are cut.**
    Why: a bad frame multiplied across a wave.
    Done by: `nova-sprint preflight --brief-dir <dir>`; `nova-sprint quack --streams <s> --count 1 --repo <url>`.

### Cost and providers

R56. **A rate limit is backed off and resumed; out of funds is held for the owner.**
    Why: a night of work landed nothing under a provider with no balance.
    Done by: `nova-sprint routes` (a rested route resumes by itself); `nova-sprint funded <provider> --reason
    <text>` after the owner pays; `nova-sprint friend down <friend> --reason <text> --until <RFC3339>` for a
    friend's limit.

R57. **Priced cost is optimised, not token counts.**
    Why: token counts misranked the levers.
    Done by: `nova-sprint cost reconcile`.

R58. **Cost per landed card (work, reads, landing) is measured every cadence, and launching stops when it beats
no alternative or the merge queue backs up.**
    Why: cost grew unmeasured.
    Done by: `nova-sprint where`; `nova-sprint stop --reason <text> --until <time>`.

R59. **A free route is trialled on one mechanical stream against flash, priced at zero in config, before it is
used.**
    Why: free routes were adopted unmeasured.
    Done by: `nova-config route add <route> --tier flash --provider <p> --model <m> --deadline <s> --as
    <coordinator>`.

R60. **When a budget is exhausted, work in flight lands and nothing new starts.**
    Why: spend continued past the budget.
    Done by: `nova-sprint stop --reason <text> --until <time>`.

R61. **Tooling work is ranked by tokens and waste removed first, then reliability, then wall clock.**
    Why: effort went to low-value fixes.
    Done by: judgment.

R62. **New rules are run on a bounded test of about 50 cards, and its cost per landed card is read before a
broad wave.**
    Why: broad releases on untested rules wasted money.
    Done by: `nova-sprint add --stream <s> --brief-dir <dir>`; `nova-sprint stats`.

### Building the tools

R63. **Test code shrinks through harnesses, constructors with defaults and table-driven cases, never copy and
paste.**
    Why: test rigs were duplicated.
    Done by: judgment.

R64. **Every layer is gated with a run at 10 to 100 times its largest real size, with a time limit per
operation, and with a slow trickle run.**
    Why: scale and ordering bugs hid at normal size.
    Done by: `nova-sprint play --simulation --seed <n>`.

R65. **Docs are written in the present tense: no parked names, dates or history.**
    Why: docs read as archaeology.
    Done by: judgment.

R66. **Every tool is built to the standard (docs/STANDARD.md), each rule naming its class test.**
    Why: tools were built inconsistently.
    Done by: judgment.

R67. **Every tool is rated cold two ways, by use (binary and help) and by reading (README then code), by several
models; it is done at 9 or better from every rater, and findings become cards.**
    Why: tools that worked were still hard for another AI to pick up.
    Done by: `nova-sprint add --stream <s> --brief-dir <dir>`.

R68. **Every setting ships a working default; a fresh install with no settings works.**
    Why: adopters had to configure before anything ran.
    Done by: judgment.

R69. **Tools are written in Go unless the host demands otherwise, as a library first with a thin command over
it; tools import each other in process and never exec each other.**
    Why: mixed languages and exec chains.
    Done by: judgment.

R70. **The simplest code wins: delete a duplicate path rather than patch it, and run a removal-only pass after
every expansion.**
    Why: code grew with each fix.
    Done by: judgment.

R71. **A proven off-the-shelf tool is adopted only against a measured need, and what dogfood shows is unused is
retired fully.**
    Why: the stack sprawled.
    Done by: judgment.

R72. **Every state machine is modelled in TLA+ beside the code, TLC runs on a bench and never the working
machine, every counterexample is checked against the code by hand, and the change cites the model.**
    Why: state machines shipped broken.
    Done by: `nova-sprint brief <id> --tier frontier` for a card that writes a model.

R73. **A new layer is mocked and driven with the owner on a disposable store before it is specified and built.**
    Why: work built ahead of the drive was thrown away.
    Done by: `nova-sprint selftest`.

R74. **Specs are settled in conversation with the owner, each decision written into the spec as it is made,
with no numeric score gate before building.**
    Why: scored spec gates did not produce good software.
    Done by: judgment.

R75. **Visual work runs from a spec file: each request edits one line, and the builder checks against it before
every restart.**
    Why: dashboard changes regressed earlier decisions.
    Done by: judgment.

### The seat's habits

R76. **The coordinator coordinates: no card work in its own session or account; work goes out as cards.**
    Why: the coordinator's account ran out and every child stopped.
    Done by: `nova-sprint add --stream <s> --brief-dir <dir>`.

R77. **A lookup or procedure done twice by hand is a missing verb, and it is filed.**
    Why: rote work was most of the coordinator's tokens.
    Done by: `nova-sprint add --stream <s> --brief-file <path>`.

R78. **No scripts: every coordinator need is a product verb, and a stopgap is named with the card that deletes
it.**
    Why: private scripts made the seat impossible to hand over.
    Done by: docs/COORDINATOR-TOOLS.md maps each stopgap; `nova-sprint seat check`.

R79. **Every rule a coordinator needs is a receipt, refusal or doc the tools print, and the handbook is
accepted only after another agent coordinates from it alone.**
    Why: the seat depended on one agent's private memory.
    Done by: `nova-sprint handover`; this section.

R80. **The main session never blocks: anything over about 15 seconds runs in the background and notifies, and
the coordinator never waits on children or CI without a watcher armed.**
    Why: the owner found the coordinator blocked.
    Done by: `nova-sprint inbox --wait --push seat`; `nova-sprint watch --wake --check 10m`.

R81. **The owner's phrases map to verbs: a new sprint is clear, adding work is add, start and stop, and pause and
unpause are stop and start.**
    Why: commands were ambiguous.
    Done by: `nova-sprint clear --confirm sprint`; `nova-sprint add --stream <s> --brief-dir <dir>`; `nova-sprint
    start`; `nova-sprint stop --reason <text> --until <time>`.

R82. **Decide inside a layer, record each decision with its reason, and bring the owner only the shape and what
touches the owner's world (credentials, machines, money).**
    Why: the owner was asked about internals.
    Done by: `nova-sprint ack <note> --reason <text>`.

R83. **Each design rule the owner states becomes a checked invariant the same day or is labelled unchecked, and
results are reviewed against the owner's sentence, not a paraphrase.**
    Why: implementations drifted from the stated design.
    Done by: `nova-sprint check`.

R84. **Every number is checked against its source before it is quoted, and the primary datum is measured rather
than a derived view.**
    Why: wrong numbers were reported.
    Done by: `nova-sprint where --json`; `nova-sprint card <id>`.

R85. **Every cap, budget and threshold is set from a measured distribution, with the numbers shown beside it.**
    Why: round guesses set limits.
    Done by: `nova-config machine set <m> --note <why> --as <coordinator>`.

R86. **A defect found in passing is fixed the same hour, failing test first, rather than parked on a ledger.**
    Why: parked defects accumulated.
    Done by: judgment.

R87. **Every machinery fix is followed within the hour by a probe of 1 to 5 cards through the changed path.**
    Why: unproven fixes went to bulk runs.
    Done by: `nova-sprint quack --streams <s> --count 1 --repo <url>`.

R88. **At the signs of a limit (fixes that do not hold, special cases multiplying), building upward stops: the
layers are named, the list goes to the owner, and they are secured from the bottom, with an attempts record per
approach.**
    Why: rebuilds repeated on a broken foundation.
    Done by: judgment.

R89. **When open pull requests climb or landings stall, nothing new starts until the wave lands.**
    Why: manual sprints ended badly.
    Done by: `nova-sprint watch --wake --check 10m --merging-over <n>`; `nova-sprint stop --reason <text>
    --until <time>`.

R90. **During a stress run, every break is recorded with a receipt and the run does not stop for fixes that are
not breakage; study at the end, then fix.**
    Why: runs stopped midway and lessons were lost.
    Done by: `nova-sprint log --since <10m|RFC3339>`.

R91. **Before a wave is released, every route is probed, the judgment-answer record is in place, and a watcher
is armed on the landed count.**
    Why: a night of zero landings went unseen.
    Done by: `nova-sprint quack --streams <s> --count 1 --repo <url>`; `nova-sprint answer --dry-run`;
    `nova-sprint watch --wake --land-after <duration>`.

R92. **The workers are asked what would have stopped the card reaching them, and the answer is adopted within the
hour.**
    Why: the same failures recurred.
    Done by: `nova-bus send --to <friend> --subject <s> --body <text>`.

R93. **A reader's findings go back as the next task with exact lines and the probe file, saying first what was
good: help, never grade.**
    Why: grading discouraged and did not fix.
    Done by: `nova-sprint rework <id> --fix <text>`.

R94. **A permission or classifier denial is final: it is logged for the owner, never reworded to get past it.**
    Why: guards were routed around.
    Done by: `nova-bus send --to <owner> --subject <s> --body <text>`.

R95. **Outcomes are reported first, in plain words, with numbers only where they change a decision.**
    Why: reports were long and full of jargon.
    Done by: judgment.

R96. **A friend's or machine's state is reported only from its own beat or output, never from expectation.**
    Why: reports were wrong about presence.
    Done by: `nova-sprint where`; `nova-friend check --as <coordinator>`.

R97. **No text, caveat or column the owner did not ask for goes on the owner's dashboard; the coordinator reads
its own role view.**
    Why: clutter and unasked notes made the page untrustworthy.
    Done by: `nova-sprint view coordinator --json`; `nova-sprint dashboard`.

### simp-retire-buswatch-b

The coordinator's hand wake script (`<coordinator-dir>/tmp/buswatch/watch.sh`) is retired:
`nova-sprint watch --wake` holds every behaviour it had, each pinned by a test, and its row
in docs/STOPGAPS.md is `STATUS: retired`. Run the verb in the background as a nova-config loop
row, `NOVA_SPRINT_SERVER=127.0.0.1:$PORT NOVA_SPRINT_ACTOR=<coordinator> nova-sprint watch
--wake --check 10m --wake-file <coordinator-dir>/tmp/buswatch/wake` (R80, R89, R91): stop the
old background run, start the verb, then delete the script.

### simp-retire-buswatch-bc.w1

The retired script's append wake is carried by the verb's `--wake-file`, so the launch
command names `<coordinator-dir>/tmp/buswatch/wake`; with no flag the file is not watched
and that half of the wake is lost. The wake-file baseline is tracked apart from the whole
state's seed, so a state written before `--wake-file` was enabled starts at the file's end
instead of waking on the lines already there. Tests:
`TestWatchWakeWatchesTheWakeFile` and `TestWatchWakeSeededOldState` (cmd/nova-sprint).

### simp-retire-buswatch-bc.w2

The retired `watch.sh` behaviours are covered by `TestWatchWakeWatchesTheWakeFile`
for append wakes and `TestWatchWakeFiresOncePerEventAndNeverLapses` for judgments,
checks, stops, friends, merge and backlog alarms, and filtered bus messages.
`TestStopgapWatchShIsRetired` checks the register's per-behaviour test citations.

## Open items

Each is a place this runbook describes a workaround; the change that removes it is named.
- [nova-tools#5096](https://github.com/mas-bandwidth/nova-tools/issues/5096), the coordinator's comfort items:
  a `--seat` flag in place of wrappers (5); the judgment line cut at the terminal width (6); `add --held`
  (15); a deadline from the take (22); a reader's beat that creates no
  row (23); `fleet/loops.yml` with no record filter (24, 25); SIGTERM draining a member (26); a read tier per
  stream (27); `fleet down` saying where cards went (21).
  [nova-tools#5152](https://github.com/mas-bandwidth/nova-tools/issues/5152): no command lists the secrets
  stores or maps a config role to its secret name.
- After PR 5129: `inbox --wait` wakes only for what is new, `inbox --wait --push <dir>` exists, and `ack`
  takes the comma list the inbox prints. Until then section 2 polls.
- After PR 5097: `rework --tier`; `fleet sync` removes a member with no machine row; the fleet footer totals
  members up; an unset width means half the cores (the help says 64 for a new member until then).
- After PR 5099: a broken read must name a file, a line or a rule or another reader is asked; a sentinel with
  nothing before it is reached only when nothing else moves; `wait` quiets a judgment for its whole period.
  After PR 5125 the member retires finished slots and caps the slots directory (`--slots-max-gb`).
- After PR 5126: the friends table has the fleet's columns but load, and the `toolanswers` ledger for
  nova-sprint rises from 52 to 56; until then the functional tier is red on
  `TestEveryCommandMeetsTheOnboardingStandard`, which holds the merge queue.
- [nova-tools#5101](https://github.com/mas-bandwidth/nova-tools/issues/5101): a route or machine row carries a
  note saying why it is set so (until then section 5's issue comment is the record).
  [nova-tools#5111](https://github.com/mas-bandwidth/nova-tools/issues/5111): a docs-only change runs the
  tests that key on the doc. [nova-tools#5122](https://github.com/mas-bandwidth/nova-tools/issues/5122): the
  server has no tick deadline; PR 5127 fixes the rebalance loop that wedged it, and until it is installed the
  verbs that compute holds (`card`, `inbox`, `ack`, `tick`, `fleet level`, `fleet up`) can spin on a fleet
  whose emptiest member refused the card at staging.
- `nova-sprint handover` prints one `RULE` line, the waves rule; printing each rule of section 10 by number
  needs a card whose PATHS take in `cmd/nova-sprint/seat.go`, where the handover's rules are set
  (`cmd/nova-sprint/handover_rules.go` holds the numbered sentences, kept equal to section 10 by its test).
- After card every-unit-installed-by-a-verb: `nova-sprint install`, `uninstall` and `units --check` are in
  nova-sprint's verb table, and `nova-swarm install disk-guard` writes the disk-guard unit in the swarm
  binary (the shared unit text is `internal/units`, which a worker may import). `nova-swarm install mirror-refresh` and a
  `nova-swarm mirror` verb stay owed: there is no mirror verb for the unit to run. The coordinator
  switches the hand-written units over after this lands; this card does not load them.
- Where the sources disagree, the help is followed here:
  - the handover notes answer `cannot ask` with a drop and `stalled` with `ask --another`; the help offers
    `reader add`, `rework`, `drop` and `wait` for the first, `card <primary>` then the printed decision for
    the second;
  - `fleet/land/ruleset-dev.json` names SQUASH as the merge queue's method, while a batch keeps every head's
    history as a merge commit: the live ruleset is read before the first landing;
  - [STANDARD.md](STANDARD.md) says landings go one at a time, the owner's rule is batches;
  - [FLEET.md](FLEET.md) sets a reader's width on its loop row, the rule is one number per machine (after PR
    5124, whose migration number collides with PR 5097's: the second to land renumbers);
  - no verb changes the sprint store's coordinator after `init`, so a seat handover moves who uses the
    actor, not the actor.
