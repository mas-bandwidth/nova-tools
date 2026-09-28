# nova-swarm Quickstart

`nova-swarm native` runs one card through a harness. `nova-swarm batch --cards`
starts a runner for each card, waits for the runners, and gathers their published
`RESULT.md` files. Both require an explicit token budget.

## Try a local card batch

This macOS/Linux shell example creates its own fixture in a new directory. It
uses a synthetic runner that copies the card's two-line answer into `RESULT.md`;
it calls no model or provider. Have `nova-swarm` on `PATH` before running it.

```bash
example_dir="$(mktemp -d)"
mkdir -p "$example_dir/root"
cat > "$example_dir/card.md" <<'CARD'
RESULT: smoke
PASS: local runner published the result
CARD
printf 'smoke\t1\tvendor/local\t%s\n' "$example_dir/card.md" > "$example_dir/cards.tsv"
cat > "$example_dir/runner.sh" <<'RUNNER'
#!/bin/sh
set -eu
label="$1"
slot="$2"
card="$4"
root="$5"
tokens="${6:?batch must supply the token budget}"
job="$root/$slot/jobs/$label"
mkdir -p "$job"
head -n 2 "$card" > "$job/RESULT.md.tmp"
mv "$job/RESULT.md.tmp" "$job/RESULT.md"
RUNNER
chmod +x "$example_dir/runner.sh"
nova-swarm batch \
  --id smoke \
  --cards "$example_dir/cards.tsv" \
  --deadline 30 \
  --runner "$example_dir/runner.sh" \
  --root "$example_dir/root" \
  --tokens unmetered \
  --no-route --reason local-fixture
```

The batch exits 0 and prints:

```text
BATCH smoke n=1 done=1 abstain=0 in=0 out=0 usd=0.0000 idle=0 stalled=0
smoke slot=1: PASS: local runner published the result log=0
```

The fixture remains at `$example_dir` for inspection. Its result is
`$example_dir/root/1/jobs/smoke/RESULT.md`.

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

## Use your runner in a batch

The TSV has four columns:
`label<TAB>slot<TAB>model<TAB>card-path`. A runner executable receives six
positional arguments: label, slot, model, card path, root, and the token budget
word. Pass that sixth argument through to `native` unchanged. For example:

```bash
#!/usr/bin/env bash
set -euo pipefail
LABEL="$1"
SLOT="$2"
MODEL="$3"
CARD="$4"
ROOT="$5"
TOKENS="${6:?batch must supply the token budget}"
exec nova-swarm native \
  --harness /path/to/harness/opencode \
  --model "$MODEL" \
  --label "$LABEL" \
  --card "$CARD" \
  --slot "$ROOT/$SLOT/jobs/$LABEL" \
  --root "$ROOT" \
  --deadline 120s \
  --tokens "$TOKENS" \
  --auth /path/to/auth.json
```

Make that script executable and supply it with `--runner`, as in the local
fixture. Choose the batch deadline and each card's token budget for the actual
work: the token word applies to **each card**, never to the batch total.

Without `--runner`, batch uses `--harness` to start `native` itself. The batch
entry point currently requires `--slots-store` and `--owner` in that form, even
though `native` treats those flags as compatibility inputs and takes only its
job and slot directory leases.

Batch returns 0 when all cards finish without holds, 1 when the result packet
contains abstentions or holds, and 2 when it cannot run. Missing or mismatched
results are abstentions. The packet has one summary, one disposition per card,
and at most eleven `HOLD:` lines. Use `nova-swarm verify --help` for independent
verification and receipt flags, and `nova-swarm profile --help` to summarize job
timelines.
