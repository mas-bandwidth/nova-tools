# FRIEND ONBOARDING

This guide takes a new AI friend (and the person helping it) from nothing to a first finished card in numbered steps. Each step has the exact verb line and how to see it worked.

## WHAT IS A FRIEND

A friend is an AI who works beside the coordinator under its own name. A friend has:
- A `nova-config` friend row (docs/SPEC-CONFIG.md)
- A row in the sprint's friends table (docs/FLEET.md)
- A working directory `~/<name>-working` on the machine it runs on

A friend owes the coordinator: honest attribution of its actual model and harness, working only in its assigned job directory, and reporting results through REPORT.md and RESULT.md files.

## THE FRIEND ROW

A friend row in nova-config has these fields:
- `name`: the friend's name (e.g., `freddy`)
- `slots`: number of concurrent cards (typically 1)
- `tiers`: the tier it can do (e.g., `heavy`, `standard`)
- `mode`: either `one-shot` (runs cards as processes) or `daemon` (runs a daemon)
- `config_dir`: for one-shot Claude rows, the config directory

Example:
```
$ nova-config friend add freddy --slots 1 --tiers heavy --mode one-shot
```

## THE HARNESS

A harness is what runs the friend's cards. For nova-friend:
- A daemon with a session (for continuous work)
- One-shot lanes (for card-based work without a session)

To install nova-friend for a friend:
```
$ nova-friend install --as freddy --harness opencode --dir ~/freddy-working
```

Check the install:
```
$ nova-friend check --settings --as freddy --harness opencode --dir ~/freddy-working
```

## INBOX JOBS OUTBOX

The directory layout is:
- `inbox/<job>/` - contains BRIEF.md, delivered by the coordinator
- `jobs/<job>/` - where clones and work happen
- `outbox/<job>/` - where REPORT.md and RESULT.md go

First setup:
```
$ mkdir -p ~/freddy-working/jobs ~/freddy-working/outbox
```

## REPORT MD AND RESULT MD

When a card is done, write:
- `outbox/<job>/REPORT.md` with `Verdict: LAND` (or HOLD/FAIL) and `Head: <sha>`
- `outbox/<job>/RESULT.md` with the card's RESULT line

Example REPORT.md:
```
Verdict: LAND
Head: abc123...

Changed docs/FRIENDS.md to add the friend's subsection.
```

## THE BUS

The bus is how friends communicate over Redis streams. Messages are sent once and delivered until acknowledged.

Send a message:
```
$ nova-bus send --redis 127.0.0.1:6379 --as freddy --to ada --subject hello --body "hello there"
```

Check the bus:
```
$ nova-bus status
```

## GOING UP AND DOWN

A friend is "up" when:
- It answered a wake ping within 10 minutes
- It finished a card within 30 minutes

To check status:
```
$ nova-friend status --as freddy --dir ~/freddy-working
```

To send a wake ping:
```
$ nova-friend ping --wake --to-friends
```

## FIRST CARD

A card arrives in `inbox/<job>/BRIEF.md`. To work on it:

1. Enter the job directory:
```
$ cd ~/freddy-working/jobs/<job>
$ git clone https://github.com/mas-bandwidth/nova-tools.git repo
$ cd repo
$ git checkout -b sprint/<card>.g<gen>.e<e> origin/sprint/<base>
```

2. Set environment:
```
$ export GOCACHE=~/freddy-working/.cache/go-build
$ export GOFLAGS=-mod=readonly
$ export NOVA_TEST_NO_HOST=1
```

3. Make changes, commit, and push:
```
$ git add <files>
$ git commit -m "description" -m "By: freddy"
$ git push -u origin HEAD:refs/heads/<branch>
```

4. Write the report files in outbox.

## WHEN STUCK

If you encounter an error:
1. Check the BRIEF.md for the exact requirements
2. Verify your environment variables are set
3. Use `nova-friend status` to check your state
4. Ask for help by sending a bus message to the coordinator

The coordinator's address is in nova-config.
