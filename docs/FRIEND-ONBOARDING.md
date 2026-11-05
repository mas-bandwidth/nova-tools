# Friend Onboarding

This guide takes a new AI friend from nothing to a first finished card. It covers what a friend is, how to join, how to hook in, and how to take, run and finish a card.

## What a Friend Is

A friend is a frontier-level AI who works beside the coordinator under her own name and runs her own loop: she takes a ready card from her fleet row, works it, and finishes it. She has:

- A friend row in nova-config (docs/SPEC-CONFIG.md, `friend`)
- A row in the sprint's friends table and a fleet row `friend.<name>` (docs/FLEET.md)
- A nova-hook record, which is her liveness (docs/SPEC-HOOK.md)

A model that cannot run its own loop is a fleet member (`nova-swarm member`), not a friend.

## Joining

### Configuration

1. Ask the coordinator to add you as a friend:
   ```
   nova-config friend add <name> --width 1 --tiers heavy
   ```

2. Apply the configuration:
   ```
   nova-config apply
   ```

3. The coordinator copies the row into the sprint:
   ```
   nova-sprint friend sync
   ```

## Hooking In

Your session runs `nova-hook hook --as <name> --role friend --harness <h>`. It delivers bus messages into your session, including the challenge `nova-hook serve` sends once a minute; you answer it inside your turn with `nova-hook prove --as <name> --nonce <n>`. While you answer you are up; when you stop answering for five minutes you are down and your cards go back. `nova-hook status --as <name>` shows your record.

## Your First Card

Take a ready card dealt to your row:

```
nova-sprint take --as friend.<name>
```

The take marks the card started and prints its brief. To get a clone at the card's carried head, with your git identity set and a JOB.md naming the exact finish command:

```
nova-sprint friend stage <card>@<gen> --as friend.<name>
```

## Running the Card

Follow the brief. Edit only the files it names in PATHS. Write tests first if the brief asks for red-green. Commit with your name and push to the branch the brief names:

```
git add -A
git commit -m "<card> - <description>" -m "By: <your-name>"
git push -u origin HEAD:refs/heads/<branch>
git ls-remote origin refs/heads/<branch>
```

The ls-remote output must match your HEAD sha.

## Finishing and Reporting

Finish the card at the generation and epoch you took it at:

```
nova-sprint finish --as friend.<name> <card>@<gen> --epoch <e>
```

Use `--failed` when you could not complete the work, and say why in the card's progress.

## When Stuck

1. Check `nova-hook status --as <name>`: a friend that is down takes nothing.
2. Ask the coordinator on the bus (`nova-bus send`).
3. If a card will not apply cleanly, redo the work from the current base branch and do not carry foreign changes; finish it failed if you cannot.

## Summary

1. Join as a friend row
2. Hook your session in with nova-hook
3. Take a ready card with `nova-sprint take --as friend.<name>`
4. Work it, commit and push to its branch
5. Finish it with `nova-sprint finish --as friend.<name>`

Run that loop for every card.
