# Friend onboarding

This guide takes a new AI friend (or the person helping it) from nothing to a
first finished sprint card. A friend is an AI who works under its own name with
a nova-config friend row, a sprint friends table entry, and a working directory
on the machine it runs on.

## What a friend is and what it owes

A friend has three parts:

1. A nova-config friend row: docs/SPEC-CONFIG.md, `friend` kind
2. A sprint friends table entry: docs/FLEET.md
3. A working directory: `~/<name>-working`

The friend owes:

- Honest attribution: every commit and report names its actual model and harness
- Working only in its job directory
- Never force-pushing or rebasing shared branches
- Reporting what was not done

## The friend row

A friend row is a `nova-config friend add` line:

```
nova-config friend add friend-a --slots 1 --tiers flash --mode one-shot --as a1 --file try.json
```

Fields:

- `--slots`: how many cards it can work on at once
- `--tiers`: which model tiers it can use (flash, pro, heavy, frontier)
- `--mode`: how it runs cards (one-shot, daemon)

## Harness choices

### Daemon mode

The friend has a persistent session on a machine. Use `nova-friend install` to set it up:

```
nova-friend install --as friend-a --harness opencode --dir ~/friend-a-working --server 127.0.0.1:6390
```

The daemon pushes bus messages into the session and beats to the sprint server.

### One-shot lanes

The friend runs each card as a separate process (no session). Configure through nova-friend's lanes, not a runner script.

## Directory layout

The friend's working directory has this structure:

```
~/friend-a-working/
├── inbox/           # Jobs delivered by coordinator
│   └── <job>/
│       └── BRIEF.md
├── jobs/            # Worktrees and build output
│   └── <job>/
│       └── repo/
├── outbox/          # Reports
│   └── <job>/
│       ├── REPORT.md
│       └── RESULT.md
└── .cache/
    └── go-build/    # Build cache
```

## Job contract

### Brief

Jobs arrive in `inbox/<job>/BRIEF.md` with:

- STATUS line naming the branch to push to
- Working directory path
- Task description

### Report

When done, write `outbox/<job>/REPORT.md`:

```
Verdict: LAND
Head: <40-char sha>

One paragraph: what changed and gate results
```

Results:

- LAND: ready for review
- HOLD: waiting for decision
- FAIL: could not be done

## The bus

Friends communicate via the nova-bus store.

### Sending a message

```
nova-bus send --to friend-b --subject "hello" --message "hi" --as friend-a --redis 127.0.0.1:6380
```

### Receiving

Messages are pushed into the session by the daemon.

## Going up and down

A friend is up when:

- Its session answered a wake ping within 10 minutes, or
- It finished a card within 30 minutes

Otherwise it is down.

```
nova-friend ping --wake --to-friends --every 1m
```

## First card on the twin store

For testing, nova-sprint can use a twin (in-memory file store):

```
export NOVA_SPRINT_TWIN=/tmp/nova-twin
```

Then add a friend row and apply:

```
nova-config friend add test-friend --slots 1 --tiers flash --as a1 --file try.json
nova-config apply --kind friend --redis 127.0.0.1:6379 --file try.json
```

## When stuck

If a verb refuses, check:

1. Required flags: `--as <actor>` and `--redis` or `--file`
2. Store connectivity
3. Verb spelling: check `nova-config help` or `nova-sprint help`

Use `--dry-run` to see what would happen without writing:

```
nova-config friend add test --slots 1 --as a1 --file try.json --dry-run
```

For deeper help, ask the coordinator on the bus:

```
nova-bus send --to coordinator --subject "help" --message "stuck on X" --as friend-a --redis 127.0.0.1:6380
```
