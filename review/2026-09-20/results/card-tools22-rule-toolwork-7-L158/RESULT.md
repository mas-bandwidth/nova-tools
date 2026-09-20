RESULT tools22-rule-toolwork-7-L158 sha=5298f6be12ea
GAP cmd/nova-decide/outcome.go:56

SPEC docs/SPEC-TOOLWORK.md:158 rule 7
PKG internal/decide (core), cmd/nova-pulse (harvest flow)
ASK The implementation must read accept= from each eligible card's OUTCOME line during harvest and call nova-decide outcome --log <path> --unit-id <id> --result green|red|blocked with the mapping accept=ok→green, accept=reject→red, anything else→blocked.

The command nova-decide outcome exists and is correctly implemented:
cmd/nova-decide/outcome.go:56-98 func runOutcome maps result values ("green"|"red"|"blocked"|"skipped") to ladder outcomes via outcomeFor() at line 32-44, validates flags, reads the decision log, looks up the last DECISION row for the unit, writes an OutcomeEntry (routelog.go:160), and prints a receipt.

But nothing in the harvest/pulse/swarm code calls nova-decide outcome during the toolwork harvest flow:
- internal/pulse/harvest.go: Harvest() classifies results and pushes PRs but never invokes outcome writing
- internal/decide/harvestclass.go:329 AppendOutcomeRow() writes to outcomes.jsonl (a separate file for tuning), not to the route log
- No code maps accept=ok → "green" or accept=reject → "red" before calling any outcome-writing function
- Internal functions OutcomeEntry() and AppendEntry() are only called within nova-decide itself (outcome.go:91-92, route.go:263), never by the harvest path

Tests found in internal/docs/:
```
grep -rn "func Test" --include='*_test.go' internal/docs/ | head -30
```
These are meta-tests verifying documentation conventions (e.g., tests_stream_rule_test.go checking TESTS.md structure). None test rule 7's harvest-outcome integration.

Relevant tests outside internal/docs/:
```
cmd/nova-decide/outcome_test.go - tests the CLI verb directly
cmd/nova-decide/outcome_append_test.go - tests appending multiple outcome rows
internal/decide/harvestclass_test.go - tests classification logic (separate concern)
```
No test verifies that harvest calls nova-decide outcome for eligible cards. UNGUARDED.

Grep evidence:
```
grep -rn "nova-decide outcome\|--unit-id.*--result\|OutcomeEntry\|AppendOutcomeRow" --include='*.go' cmd/ internal/ | grep -v '_test.go' | head -20
# Only shows usage within nova-decide itself; none from pulse/swarm/harvest paths
grep -rn "AcceptOK\|AcceptReject" --include='*.go' cmd/ | head -20
# Only in internal/decide/harvestclass.go where they feed Classification, never mapped to "green"/"red"
```

Left owed
git status --short:
?? .lease
?? .nova-sandbox-tmp/
?? RESULT.md
?? harness-output.log
?? opencode.json
?? repo/
