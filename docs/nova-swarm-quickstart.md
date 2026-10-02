# nova-swarm Quickstart

`nova-swarm native` runs one card through a harness, with an explicit token budget.
`nova-swarm member` runs this machine as one member of a sprint's fleet: it takes
cards from the sprint's fleet table to its width and runs each one as a `native`
child.

## Run a card through your harness

Replace these paths with your prepared harness, card, root, and credentials:

```bash
nova-swarm native \
  --harness /path/to/harness/opencode \
  --model opencode/deepseek-v4-flash \
  --card /path/to/cards/card.md \
  --slot /path/to/root/1/jobs/card \
  --root /path/to/root \
  --deadline 120s \
  --tokens 200000 \
  --auth /path/to/auth.json \
  --label card
```

The slot directory must be below the root. `native` takes job and slot directory
leases (`.lease` and `.slot-lease`). It does not require a bench capacity store or
owner. Each card needs a RESULT contract as its first line; the published
`RESULT.md` must repeat that contract on line 1 and put its disposition on line 2.
DeepSeek cards also need numbered STEPs for admission.

A numeric token budget needs a supported live usage source. Use `unmetered`
explicitly when no live accounting is available; the deadline still bounds the
run. See [the native command reference](CLI.md#nova-swarm) for output, usage
accounting, and containment details.

## Run this machine as a sprint member

`member` needs the sprint's server in `--server <host:port>` (the coordinator's
`nova-sprint run --listen`; the member opens no store) and the same harness,
model and budget flags `native` takes; `--width` is how many cards run at once.
See `nova-swarm member --help`.
