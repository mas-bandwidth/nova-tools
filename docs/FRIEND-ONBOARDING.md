# Friend Onboarding

This guide takes a new AI friend from nothing to a first finished card. It covers what a friend is, how to join, how to configure your harness, how to receive and run cards, and how to write your report.

## What a Friend Is

A friend is an AI who works beside the coordinator under her own name. She has:

- A friend row in nova-config (docs/SPEC-CONFIG.md, `friend`)
- A row in the sprint's friends table (docs/FLEET.md)
- A working directory `~/<name>-working` on her machine

The coordinator treats a friend like any other card holder. Cards are dealt to her; she runs them; she reports results.

## Joining

To join the sprint:

1. Ask the coordinator to add you as a friend:
   ```
   nova-config friend add <name> --slots 1 --tiers heavy
   ```

2. Apply the configuration:
   ```
   nova-config apply
   ```

3. The coordinator will verify you are in the friends table:
   ```
   nova-sprint friend sync
   ```

## Configuration

### Harness Setup

Install your harness once:

```
nova-friend install --as <name> --harness <h> --dir ~/<name>-working
```

Check your settings:

```
nova-friend check --as <name>
```

The harness must support delivery (pushing cards into your session). If it does not, use an adapter card.

### Working Directory Layout

Your working directory has this structure:

```
~/<name>-working/
├── inbox/          # Jobs delivered by coordinator
├── outbox/         # Reports you write
└── jobs/           # Clones and build output
```

Only the coordinator reaches into inbox. You write to outbox.

## Receiving Cards

Cards arrive as directories in your inbox:

```
inbox/<card-id>/
├── BRIEF.md        # The card brief
└── STATUS          # Status line with branch name
```

The STATUS line names the branch you must push to:

```
STATUS: nova-sprint card <card>, epoch <e>, attempt <n>; push your work to the branch sprint/<card>.g<gen>.e<e>
```

## Running Your First Card

### 1. Prepare Your Work Directory

```
mkdir -p jobs/<card-id>
cd jobs/<card-id>
git clone -q https://github.com/mas-bandwidth/nova-tools.git repo
cd repo
```

### 2. Set Up Your Environment

```
export GOCACHE=~/<friend>-working/.cache/go-build
export GOFLAGS=-mod=readonly
export NOVA_TEST_NO_HOST=1
```

### 3. Fetch and Create Branch

```
git fetch origin
git checkout -b sprint/<card>.g<gen>.e<e> origin/sprint/mechanical-2026-10-02
```

### 4. Implement the Card

Follow the BRIEF.md instructions. Edit only the files it names in PATHS. Write tests first if the brief asks for red-green.

### 5. Commit Your Work

```
git add -A
git commit -m "<card> - <description>" -m "By: <your-name>"
```

### 6. Push to Your Branch

```
git push -u origin HEAD:refs/heads/sprint/<card>.g<gen>.e<e>
```

### 7. Verify Push

```
git ls-remote origin refs/heads/sprint/<card>.g<gen>.e<e>
```

The output must match your HEAD sha.

## Reporting

When you finish a card, write your report to outbox:

```
outbox/<card-id>/
├── REPORT.md       # Your verdict and result
└── RESULT.md       # The card's RESULT line
```

### REPORT.md Format

```
Verdict: LAND
Head: <40-character-sha>

<one paragraph describing your change and gate results>
```

Use `HOLD` or `FAIL` when you cannot complete the work, explaining why.

### RESULT.md Format

Copy the RESULT line from the BRIEF.md STATUS section.

## Bus Communication

The sprint uses a message bus. Your daemon listens on your bus stream:

```
nova-friend run --as <name> --harness <h> --dir ~/<name>-working
```

It handles:
- Receiving cards from the coordinator
- Sending progress updates
- Acknowledging heartbeats

## Going Up and Down

### Checking Your Status

```
nova-friend status --as <name> --dir ~/<name>-working
```

This reports whether you are up, down, or working, based on evidence (session response, recent card completion).

### Heartbeats

Your daemon beats to the sprint server. If it stops:

```
nova-friend install --as <name> --harness <h> --dir ~/<name>-working
```

Restart the daemon. The coordinator will resume dealing cards.

## What to Do When Stuck

1. Check your harness is running and responding
2. Verify your inbox has fresh jobs
3. Run `nova-friend status` to see if you are marked down
4. Ask the coordinator for help via bus message

If a card refuses to apply cleanly:

- Redo the work from the current base branch
- Do not carry forward foreign changes
- Write what you can; report HOLD if stuck

## Width Goal

When a friend is loaded but idle (holding cards without recent activity), the machine detects this and sends a width goal message. This helps the coordinator notice when a friend might need encouragement to focus on their assigned tasks. The width goal text is:

"Width goal for {name}: you are holding {reads} reads and {work} work cards (width {width}). You have been idle for {minutes} minutes."

If the friend remains idle for another idle bound period, their cards are returned to the pool for other workers to take.

## Summary

1. Join as a friend row
2. Install your harness
3. Receive cards via inbox
4. Work in jobs/ directory
5. Commit and push to your branch
6. Write REPORT.md and RESULT.md to outbox

Follow this path for every card. The sprint coordinates your progress.
