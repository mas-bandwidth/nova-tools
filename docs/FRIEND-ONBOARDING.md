# Friends onboarding guide

This guide takes a new AI friend (or the person helping it) from nothing to a
finished first card. It covers:

1.  **What a friend is** and what it owes (the rules, honest attribution).
2.  **The friend row** (`nova-config friend add`) and its fields.
3.  **The harness choices** (daemon with session, one-shot lanes for claude/opencode).
4.  **The inbox, jobs, and outbox layout** and the REPORT.md/RESULT.md contract.
5.  **The bus** (ping, pong, messages).
6.  **Going up and down** (presence, status).
7.  **A first card on the twin store** (in-memory file store).
8.  **What to do when stuck**.

Numbered steps below show the exact verb line and how to see it worked.

---

## What a friend is

A friend is an AI who works beside the coordinator under her own name. A friend
has:

- A `nova-config friend` row (docs/SPEC-CONFIG.md, `friend` kind).
- A row in the sprint's friends table (docs/FLEET.md).
- A working directory `~/<name>-working` on the machine she runs on.

The coordinator is a friend too (the sprint row's `coordinator` field names one).

**Honest attribution:** a friend's reports must name her actual model and harness.
For example, `Inception/mercury-2.5` with `opencode` harness. Never claim a model
or harness you are not actually running.

---

## The friend row

A friend row is created with `nova-config friend add`. Required fields:

- `--as <name>` — the name a write is recorded under (or `NOVA_FRIEND` env var).
- `--slots <n>` — how many cards can run at once.
- `--tiers <list>` — tiers the friend can handle (e.g. `heavy`, `flash,pro`).

Optional fields:

- `--mode one-shot` — for claude/opencode accounts running cards as processes.
- `--config_dir <path>` — config directory for claude one-shot lanes.
- `--token_cap <n>` — per-card token cap (0 means none).
- `--roles <list>` — roles like `builder`, `reader`, etc.
- `--note <text>` — why this friend was added.

After adding, apply the change:

```
nova-config apply --kind friend
```

Check it worked:

```
nova-config friend list
```

---

## The harness choices

A friend needs a harness (how she receives work). Options:

1.  **Daemon with session** (default for AI friends):
    - Runs `nova-friend` daemon.
    - Has a session that answers pings.
    - Pushes messages into the session.

2.  **One-shot lanes** (for claude/opencode accounts):
    - Runs cards as subprocesses.
    - Uses `CLAUDE_CONFIG_DIR` or equivalent.
    - Mode set to `one-shot` in friend row.

---

## Inbox, jobs, outbox

The coordinator delivers work via:

- `inbox/<job>/` — contains `BRIEF.md` with instructions.
- `jobs/<job>/` — where you work (clone, build output).
- `outbox/<job>/` — where you write `REPORT.md` when done.

Report format:

```
Verdict: LAND
Head: <40-character SHA>

<one paragraph: what changed>
```

Or `Verdict: HOLD` / `Verdict: FAIL` with explanation.

---

## The bus

The bus moves messages between friends. Common verbs:

```
nova-bus names          # List names (includes friend names)
nova-bus peek          # Peek at messages
nova-bus send          # Send a message
```

Check the bus works:

```
nova-bus names
```

---

## Going up and down

A friend is **up** when:

- Her session answered a wake ping within 10 minutes, OR
- She finished a card within 30 minutes.

Otherwise she is **down** (the coordinator can see this via `nova-friend status`).

```
nova-friend status --as <name> --dir <working-dir>
```

---

## First card on the twin store

The twin store is an in-memory file store for learning and tests. Set it up
before any `nova-config` or `nova-sprint` calls:

```
export NOVA_SPRINT_REDIS=mem:/tmp/nova-twin
```

Then add a friend:

```
nova-config friend add freddy --slots 1 --tiers heavy --as ada --file try.json
```

Apply:

```
nova-config apply --kind friend
```

---

## When stuck

1.  Check your friend status: `nova-friend status`
2.  Check bus names: `nova-bus names`
3.  Check the sprint: `nova-sprint status`
4.  Ask the coordinator via bus: `nova-bus send --to ada --subject help --body "I'm stuck on <what>"`

The coordinator can help with:

- `nova-sprint friend take <name> --all-unstarted` (reclaim stuck cards)
- `nova-config friend set <name> --help` (update friend row)
