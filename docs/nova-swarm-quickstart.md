# nova-swarm Quickstart

`nova-swarm` coordinates bounded, isolated worker execution across multiple LLM harnesses and models. Slices 1–4 provide:
1. **Per-job profiles and preimages** (`nova-swarm supervise`, `SPEC-SWARM-PROFILES.md`)
2. **Native execution** bound to a frozen configuration (`nova-swarm native`, issue #296)
3. **Result contract and receipts** (`nova-swarm verify`, issue #241)
4. **Batch scatter, wait, gather** (`nova-swarm batch --cards`, issue #353)

---

## 1. Native Run (`nova-swarm native`)

`nova-swarm native` executes one frozen run configuration against a native harness binary (e.g. `opencode`). The configuration is verified before anything is started: the harness binary must exist and be executable, the model must have a valid `provider/model` prefix, the slot directory must reside strictly under the configured root, and any auth entries are copied mode `0600` into an isolated `$XDG_DATA_HOME`.

Before the first run on a bench, its slot store is made once, by hand -- `native` refuses to
launch without one and never creates one:

```bash
nova-swarm slots init --store /path/to/root/slots-store --owner stella --capacity 1 --share 1
```

### Invocation

```bash
nova-swarm native \
  --harness /Users/Shared/nova-swarm-stella-20260913/harness-v1.18.20/opencode \
  --model opencode/deepseek-v4-flash \
  --card cards/card-smoke-ds.md \
  --slot /path/to/root/slot-ds/jobs/card-smoke-ds \
  --root /path/to/root \
  --deadline 120s \
  --slots-store /path/to/root/slots-store \
  --owner stella \
  --auth ~/.local/share/opencode/auth.json \
  --label card-smoke-ds
```

### Expected Output

On success:
```text
NATIVE OK label=card-smoke-ds rc=0 wall=36.58s card_sha256=1aec32417858179f524b045e675442e0a03220528a1a8248ac71601ea0f11419 binary_sha256=9598c27bda0e2d88ce4db5f853e25504c20ac6152e10205785a1cf8f45559952
```

On invalid configuration (refusal, exit code 2):
```text
NATIVE REFUSED: the slot directory "..." is outside the configured root "..."
```

### Invariants

- **Isolation**: Child runs with `HOME` and `XDG_DATA_HOME` pointing at `<slot>/data`.
- **Environment**: `NOVA_SWARM_JOB` is set to `<slot>`. Standard input is EOF (`/dev/null`).
- **Standard output/error**: Captured to `<slot>/native.log`.
- **Exit Code**: Returns `0` if the child exited 0; returns child exit code (or 1) if child exited non-zero; returns `2` for configuration refusals.

---

## 2. Batch Execution (`nova-swarm batch --cards`)

`nova-swarm batch` coordinates parallel execution across $N$ cards:
1. **Scatter**: Spawns one runner process per admitted card in TSV order.
2. **Wait**: Waits until all card runners exit or the batch deadline expires. Stragglers are killed on deadline expiry.
3. **Gather**: Inspects `<root>/<slot>/jobs/<label>/RESULT.md` for every card, verifies line 1 against the card's admission contract, and emits a single bounded summary packet.

### Input Format: `cards.tsv`

A tab-separated file with four columns: `label<TAB>slot<TAB>model<TAB>card-path`.

```tsv
card-smoke-mc	1	inception/mercury-2.5	/path/to/cards/card-mercury.md
card-smoke-ds	2	opencode/deepseek-v4-flash	/path/to/cards/card-deepseek.md
```

**Card convention**: Line 1 of the card file is the RESULT contract line and `RESULT.md` line 1 must equal it byte-for-byte; cards for DeepSeek models must use numbered STEPs (practice 17) or admission refuses them.


### Runner Script (`batch-runner.sh`)

The runner executable receives five positional arguments:
`$1=label`, `$2=slot`, `$3=model`, `$4=card-path`, `$5=root`.
It also inherits `NOVA_SWARM_ROOT` and `NOVA_SWARM_JOB`.

```bash
#!/usr/bin/env bash
set -euo pipefail
LABEL="$1"
SLOT="$2"
MODEL="$3"
CARD="$4"
ROOT="$5"
JOB_DIR="$ROOT/$SLOT/jobs/$LABEL"
# The bench slot lease (nova-tools#1546): native refuses to launch without a store and an
# owner. The store is made ONCE per bench, by hand, and is NOT created here -- a runner that
# made its own store would be a runner that cannot be refused.
OWNER="${NOVA_SWARM_SLOT_OWNER:?set NOVA_SWARM_SLOT_OWNER to the owner whose bench share this runner holds}"
mkdir -p "$JOB_DIR"

# Scope git root to the slot directory so OpenCode does not traverse into parent checkouts
if [ ! -d "$JOB_DIR/.git" ]; then
  git -C "$JOB_DIR" init -q
fi

# If using a custom OpenAI-compatible provider (e.g. inception/mercury-2.5)
if [ -f "$HOME/.config/opencode/opencode.json" ]; then
  cp "$HOME/.config/opencode/opencode.json" "$JOB_DIR/opencode.json"
fi
if [ -z "${INCEPTION_API_KEY:-}" ] && [ -f "$HOME/.config/freddy/env" ]; then
  export INCEPTION_API_KEY="$(cat "$HOME/.config/freddy/env" | tr -d '\n\r ')"
fi

exec nova-swarm native \
  --harness /Users/Shared/nova-swarm-stella-20260913/harness-v1.18.20/opencode \
  --model "$MODEL" \
  --label "$LABEL" \
  --card "$CARD" \
  --slot "$JOB_DIR" \
  --root "$ROOT" \
  --deadline 120s \
  --slots-store "$ROOT/slots-store" \
  --owner "$OWNER" \
  --auth ~/.local/share/opencode/auth.json
```

### Invocation

```bash
nova-swarm batch \
  --id batch-smoke-proof \
  --cards cards.tsv \
  --deadline 180 \
  --runner ./batch-runner.sh \
  --root /path/to/root
```

### Expected Output

```text
BATCH batch-smoke-proof n=2 done=2 abstain=0 usd=0.0000
card-smoke-mc PASS: pwd verified and model is inception/mercury-2.5
card-smoke-ds PASS: pwd verified and model is opencode/deepseek-v4-flash
```

If a card's `RESULT.md` is missing or line 1 does not match the contract, the card is gathered as `abstain`:
```text
BATCH batch-smoke-proof n=2 done=1 abstain=1 usd=0.0000
card-smoke-mc abstain
card-smoke-ds PASS: pwd verified and model is opencode/deepseek-v4-flash
```

### Packet Bounds

- The batch summary packet is strictly bounded: 1 `BATCH` line, $N$ per-card disposition lines, and at most 11 `HOLD:` lines (ceiling: $N + 12$ lines).
- Exit codes:
  - `0`: All cards completed (`done == n`) with no `HOLD` lines.
  - `1`: Batch completed with one or more `abstain` or `HOLD` rows.
  - `2`: Admission error (e.g. invalid TSV, missing flags, unexecutable runner).

---

## 3. Result Contract and Verification (`nova-swarm verify`)

`nova-swarm verify` checks a job's `RESULT.md` against its contract line and emits a durable receipt.

### The RESULT.md Contract

1. **Line 1**: Line 1 of the card file is the RESULT contract line and `RESULT.md` line 1 must equal it byte-for-byte; admission and gather refuse any mismatch.
2. **Line 2**: The disposition line (e.g. `PASS: ...`, `CLEAR: ...`, `HOLD: ...`).
3. **Lines 3+**: Evidence lines, bounded by `--max` (default 20 lines).

### Invocation

```bash
nova-swarm verify \
  --result /path/to/root/slot/jobs/card-smoke-ds/RESULT.md \
  --contract "# smoke-deepseek-v4-flash" \
  --label card-smoke-ds \
  --card cards/card-smoke-ds.md
```

### Expected Output

```text
RESULT OK "card-smoke-ds" line2="PASS: pwd verified and model is opencode/deepseek-v4-flash"
```

### Receipt Artifact

When verification succeeds, `nova-swarm verify` writes `<result>.receipt` alongside `RESULT.md`:

```text
label=card-smoke-ds
card_sha256=1aec32417858179f524b045e675442e0a03220528a1a8248ac71601ea0f11419
line2=PASS: pwd verified and model is opencode/deepseek-v4-flash
lines=4
```

---

## 4. Reading Job Results (`nova-swarm result`)

To print a completed job's report verbatim from a queue pool:

```bash
nova-swarm result --pool /path/to/pool --id <job-id>
```
