RESULT tools22-rule-merge-14 sha=5298f6be12ea
ABSENT
SPEC docs/SPEC-MERGE.md:1659 rule 14
PKG internal/merge
ASK An implementation would have to run gates under machine-wide kernel-locked slot files (`--slots`/`--slots-dir`), cap leg fan-out with `--legs` (both refused below 1 and above 64 with exit 2 naming the flag and the cap), print one `GATE WAIT` line while a gate waits for a slot, and spawn no leg before the slot lock is held.
The rule binds the gate runner, and the spec says that runner is a second tool, not nova-merge: "A second tool runs the gate and records the verdict; `nova-merge` reads records" (docs/SPEC-MERGE.md:947-948) and "The runner is a second tool (below), and this spec fixes only the edge the lane sees" (docs/SPEC-MERGE.md:893-894). In this tree nova-merge only RECORDS a verdict — `cmdGate` in cmd/nova-merge/verbs.go:543 (`nova-merge gate --verdict green|red --summary`) — and never runs gates. No gate-runner binary exists in cmd/, and none of the rule's machinery is present anywhere in the tracked tree:
  - no `GATE WAIT` output string, no `waited=` field, no `<slots-dir>/<k>` lock: `grep -rn "GATE WAIT|waited=" .` matches only docs/SPEC-MERGE.md
  - no `--slots-dir` flag, no `--legs` flag: `grep -rn "slots-dir|--legs"` over `git ls-files` matches only docs/SPEC-MERGE.md and docs/SPEC-TOOLWORK.md:501 (a nova-pulse `--legs <tools/legs.tsv>` flag, a different tool and meaning)
  - `internal/merge/` holds no gate runner and even the demanded parity test is missing: there is no `internal/merge/parity_test.go` (work-list item 4) and no `ci-fast.yml` anywhere in the tree; the only slot mention in the package is the lane directory name `SlotsDir = "slots"` (internal/merge/state.go:42)
  - the `internal/swarm` slot/`RUN WAIT slots` machinery (internal/swarm/run.go:461, cmd/nova-swarm) is the nova-swarm worker's own bench-slot leases, a different tool with a different spec, not the merge gate runner
greps: `grep -rn "GATE WAIT" .`, `grep -rn "waited=" .`, `grep -rn "slots-dir" .`, `grep -rn '"slots"|"legs"|--slots|--legs' .` (non-SPEC, non-_test), `grep -rn "SlotsDir"`, `ls internal/merge/`, `ls cmd/`, `git ls-files` scan
Left owed===FILE=== card-tools22-rule-merge-14/usage.tsv
job	attempt	started	ended	rc	provider	model	tokens_in	tokens_out	cache_write	cache_read	reasoning	usd
card-tools22-rule-merge-14	1	2026-09-20T20:27:55Z	2026-09-20T20:33:10Z	0	opencode	deepseek-v4-flash	42945	16951	0	1219584	0	0.0449
