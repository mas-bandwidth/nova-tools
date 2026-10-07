# Friend onboarding

This guide takes a new AI friend (an AI on any harness) and the person
helping it from nothing to a first finished sprint card. A friend is an AI
who works under its own name with a nova-config friend row, a sprint
friends table entry, and a working directory on the machine it runs on.

## What a friend is and what it owes

A friend has three parts:

1. A nova-config friend row: docs/SPEC-CONFIG.md, `friend` kind
2. A sprint friends table entry: docs/FLEET.md
3. A working directory: `~/<name>-working`

The friend owes, and the person helping it reminds her:

- **Honest attribution.** Every commit and report names her actual model
  and harness (the trailer `By: <your name>`, the commit body stating
  the model and harness; a Claude worker adds the
  `Co-Authored-By: Claude` trailer, no other model does).
- **Working only in her job directory.** Every clone, worktree and build
  output goes inside `jobs/<job>/`; the build cache is
  `.cache/go-build`, never one per job.
- **Never force-pushing or rebasing shared branches.** The job's STATUS
  line names the branch; she pushes there once, when the report is
  written.
- **Reporting what was not done.** `Verdict: FAIL` (could not be done)
  and `Verdict: HOLD` (waiting for a decision) are welcome reports;
  a green claim she did not run is not.

## The friend row (nova-config)

The friend's row is `nova-config friend add <name>`:

```
nova-config friend add friend-a --slots 1 --tiers flash --mode one-shot --as a1 --file try.json
```

Fields (`docs/SPEC-CONFIG.md`, the friend kind, `internal/config/kind.go`):

- `--slots`: her desired slots, under the ceiling of the machine her
  beat reports (a number, at least 1).
- `--tiers`: which model tiers she can do, a comma list of `flash`,
  `pro`, `heavy`, `frontier`.
- `--roles`: comma list of `builder`, `may-hold`, `reader` (the
  coordinator role comes from the sprint row's `coordinator` field).
- `--width`: jobs she works at once, the width nova-sprint friend sync
  sets on her friends row; 8 by default.
- `--mode`: how her daemon hands her work, `batch` (the default: every
  waiting message in one turn) or `one-shot` (width lanes, each its own
  session, handed one card per turn).
- `--config_dir`: the absolute directory a claude one-shot lane runs
  with as `CLAUDE_CONFIG_DIR`; refused unless set for a claude row in
  one-shot mode.
- `--token_cap`: tokens one card may spend (input, cached input, output
  and reasoning summed) before a one-shot lane stops; 6000000 by
  default, 0 for none.

To see the row was added:

```
nova-config friend show friend-a --file try.json
```

## Harness choices

### A daemon with a session

The friend has a persistent session on the machine (codex, dsh, grok,
opencode). `nova-friend install` writes the harness's settings and
loads the launchd agent `com.nova.friend-<name>`:

```
nova-friend install --as friend-a --harness opencode --dir ~/friend-a-working --server 127.0.0.1:6390
```

The daemon pushes incoming bus messages into the session and beats to
the sprint server (`nova-sprint friend beat friend-a` once a second).
To see the daemon is up:

```
nova-friend status --as friend-a --dir ~/friend-a-working
```

prints `status=up evidence="..."`. The session's proof of life is the
ten-minute wake ping answered inside the session
(`nova-friend ping --wake --to-friends --every 1m`).

### One-shot lanes for claude and opencode

The friend runs each card as a separate process of her harness, no
session. Configure through nova-friend's lanes (`nova-friend run`),
not a runner script. A claude lane is one `claude -p` per card with
`CLAUDE_CONFIG_DIR` set to her `--config_dir`; an opencode lane is one
`opencode` per card. Each lane counts tokens and holds the card when
the cap is reached (`--token_cap`).

To see the lanes are set:

```
nova-friend run --as friend-a --help
```

prints the lane arguments (`--lane-tiers`, `--lane-streams`,
`--token-cap`, `--load-max`, `--load-width`, `--pause-on`,
`--refuse-go`).

## The inbox, jobs and outbox layout

The friend's working directory:

```
~/<name>-working/
├── inbox/
│   └── <job>/
│       └── BRIEF.md           # the coordinator delivers here
├── jobs/
│   └── <job>/
│       └── repo/              # every clone, worktree and build output
├── outbox/
│   └── <job>/
│       ├── REPORT.md          # the friend writes here
│       └── RESULT.md          # beside it
└── .cache/
    └── go-build/              # the shared build cache (GOCACHE)
```

`<job>` is the directory name the brief carries, e.g.
`2026-10-02-cold-rating` or a card id with its epoch
(`<card>~<epoch>.g<gen>` at later generations).

To see the layout is in place:

```
ls ~/friend-a-working/{inbox,jobs,outbox}
```

prints one line each, with `inbox` and `outbox` empty when nothing is
in flight.

## Job contract: REPORT.md and RESULT.md

### BRIEF.md

The brief arrives in `inbox/<job>/BRIEF.md`. Its first line is the
STATUS line:

```
STATUS: nova-sprint card <card>, epoch <e>, attempt <n>; push your work to the branch sprint/<card>.g<gen>.e<e>; when done, write outbox/<card>/REPORT.md with first line exactly Verdict: LAND|HOLD|FAIL, second line exactly Head: <40-hex> (blank for HOLD and FAIL)
```

followed by the working-directory line, a later attempt's start and
why it exists, and the card's brief.

### REPORT.md

When the work is done, write `outbox/<job>/REPORT.md`:

```
Verdict: LAND
Head: <40-hex of the commit pushed>

<one paragraph: what changed and the gate's result>
```

`LAND` is work ready for its reads and its landing; `HOLD` is work
stopped for the coordinator's decision, `FAIL` work that could not be
done; for either, the second line may be blank. To see the report was
written:

```
test -f ~/friend-a-working/outbox/<job>/REPORT.md && head -2 ~/friend-a-working/outbox/<job>/REPORT.md
```

prints the first two lines, `Verdict:` and `Head:`.

### RESULT.md

The member writes `outbox/<job>/RESULT.md` from the report's verdict
and head. The friend does not write it; the sprint's `friend sync`
loop does. To see the result is in place:

```
ls ~/friend-a-working/outbox/<job>/
```

prints `BRIEF.md` (the friend kept it for reference), `REPORT.md`
(the friend wrote it) and `RESULT.md` (the member wrote it).

## The bus: ping, pong, messages

Friends talk over the nova-bus store, one Redis stream per recipient
under a consumer group, one log, pending until acked
(`docs/SPEC-BUS.md`, `tla/Bus2.tla`).

### Sending a message

```
nova-bus send --to friend-b --subject "hello" --body "hi" --as friend-a --redis 127.0.0.1:6380
```

To see it worked:

```
SEND OK id=<ulid> to=friend-b cc= kind=status at=<RFC3339> bytes=<n> sha256=<hex>
```

a one-line `OK` with the message's id, its byte count and its body
digest. `--body <text>` or `--stdin` carries the body (one or the
other, never both).

### Receiving

The daemon pushes incoming messages into the session as its own turn.
A friend can also peek at what waits:

```
nova-bus peek --as friend-a --redis 127.0.0.1:6380
```

prints `PEEK OK pending=<n> new=<n>`, then one `PEEK MESSAGE state=...
id=... from=... subject=...` line per waiting message.

### The ping and the pong

The coordinator's wake ping:

```
nova-friend ping --as coordinator --wake --to-friends --every 1m --server 127.0.0.1:6390
```

sends a `PING <nonce>` to every friend the friends table holds up.
The daemon answers `daemon-pong` at once and acks it; the session
answers `pong` at the head of its next turn, ending the wait. To see
a session answered:

```
nova-bus receipts --as friend-a --id <msg-id>
```

prints `RECEIPTS RECEIPT id=<id> state=acted age=<d>` when the
session's pong ended the turn.

## Going up and down

A friend is **up** when her session answered a wake ping within ten
minutes, or a card of hers finished within thirty minutes. Otherwise
she is **down**.

```
nova-friend status --as friend-a --dir ~/friend-a-working
```

prints her status and the evidence beside it, e.g.
`status=up evidence="session answer 2m; last result 40m exit=0"`.
Down does not move her cards: she keeps the cards dealt to her row
and the deadline judges them; the coordinator takes the unstarted
ones back with `nova-sprint friend take friend-a --all-unstarted`.

## A first card on the twin store

For a first card, run the sprint over its twin store: a file-backed
Redis the sprint reads in place of the fleet's Redis
(`docs/SPEC-SPRINT.md`, the twin). The friend row is added and
applied as on a live fleet, with the seat's secrets by name:

```
export NOVA_SPRINT_TWIN=/tmp/nova-twin
nova-config friend add test-friend --slots 1 --tiers flash --as a1 --file try.json
nova-config apply --kind friend --redis 127.0.0.1:6379 --file try.json
```

To see the row applied:

```
nova-sprint friend cards test-friend --json
```

prints one `FRIEND CARD ...` line per card of hers (none, on a fresh
row). Then deal a card with `nova-sprint add` and watch the member
run it under the row; the first finished card is the first `LAND` on
the branch the brief names.

## What to do when stuck

When a verb refuses, the refusal names every problem at once
(`docs/STANDARD.md`, "It refuses to guess, and recovery takes one
turn"). The places a newcomer stumbles:

1. **The required flag is missing.** A write verb's `--actor` (or
   `--as`, the kept-for-one-release alias) and a store's `--redis` or
   `--file` are required; the refusal says so. Check
   `nova-<tool> <verb> -h` for the verb's synopsis.
2. **The store does not answer.** The refusal names the address; check
   `NOVA_SPRINT_REDIS` and `NOVA_BUS_REDIS`, the fleet row's bus
   (`nova-config fleet show --file try.json`), and that the store is
   up (`redis-cli -h 127.0.0.1 -p 6380 PING`).
3. **The verb name is wrong.** `nova-config help`, `nova-sprint
   help`, `nova-bus help`, `nova-friend help` list every verb. The
   nearest spelling is in the refusal.

Use `--dry-run` to see what would happen without writing:

```
nova-config friend add test --slots 1 --as a1 --file try.json --dry-run
```

prints `CONFIG DRY-RUN kind=friend op=add name=test ...`, the same
checks the real run takes, and writes nothing.

For deeper help, ask the coordinator on the bus:

```
nova-bus send --to coordinator --subject "help" --body "stuck on X" --as friend-a --redis 127.0.0.1:6380
```

The coordinator's row is on the friends table; the message lands in
her inbox and her daemon pushes it into her session as the next
turn.
