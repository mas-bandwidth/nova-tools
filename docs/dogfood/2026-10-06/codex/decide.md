# nova-decide dogfood — Codex, 2026-10-07

Read as a stranger: `nova-decide -h`, `nova-decide help`, `nova-decide <verb> -h`, and the `nova-decide` pages in `docs/CLI.md` and `docs/SPEC-NOVA-DECIDE.md`. Built the Linux binary from the current `sprint/mechanical-2026-10-02` tip `eceefa563cba4edabbdc0d123a396c3f3d9106eb` (`nova-decide v0.0.0-20261007161612-eceefa563cba linux/amd64 go1.26.6`). Ran every verb (`ask`, `read`, `score`, `attempt`, `grade`, `gate`, `brief`, `outcome`, `calibrate`, `findings`, `import`, `score-grades`, `version`, `help`) against isolated scratch files with the fixed backend; no provider calls or real sprint data.

## Findings

### 1. `findings` rejects `--max` despite the help promise — URGENT

Command:

```text
nova-decide findings --record /home/glenn/nova-bench/runs/stella-dogfood-codex-decide-b-w2-20261007/scratch/record.jsonl --since 2026-10-07 --bar 0.5 --max 10
```

Printed (exit 2; first three lines):

```text
FINDINGS REFUSED: unknown flag --max; the flags of findings are --bar, --heavy, --json, --read-shadow, --real, --record, --shadow, --since; run: nova-decide findings -h
```

Expected: `nova-decide help` says a verb that lists takes `--max` and prints `MORE` for the rest. I expected `findings` to accept `--max 10` and bound its result list. The command refuses, so the top-level help promises a flag this listing verb does not support.

### 2. Fixed choice-answer format needs an example — NEXT

Command:

```text
nova-decide ask --schema /home/glenn/nova-bench/runs/stella-dogfood-codex-decide-b-w2-20261007/scratch/schema.json --state /home/glenn/nova-bench/runs/stella-dogfood-codex-decide-b-w2-20261007/scratch/state.txt --backend fixed --answers /home/glenn/nova-bench/runs/stella-dogfood-codex-decide-b-w2-20261007/scratch/scalar-answers.json --record /home/glenn/nova-bench/runs/stella-dogfood-codex-decide-b-w2-20261007/scratch/record.jsonl
```

Printed (exit 2; first three lines):

```text
ASK REFUSED: the answers file is not JSON of the shape {<question>: {choice, p} | {noul}}: json: cannot unmarshal number into Go struct field FixedAnswer.p of type map[string]float64; run: nova-decide help
```

Expected: the help and spec say fixed choice answers use `choice` and `p`, and that probabilities are given per option. I expected an example to show that `p` must be an object mapping each option name to its probability. The refusal points to generic help, which does not show that shape; the Go decoding error exposed it only after I tried a scalar.

READ 7/10 — the command pages name the effects and most flags, but the shared `--max` promise is false for `findings` and fixed choice answers need a concrete JSON example.

USE 6/10 — the fixed backend let me exercise the complete workflow in scratch, but the listing flag refusal and the answer-file shape took extra attempts to understand.

urgent=1 next=1
