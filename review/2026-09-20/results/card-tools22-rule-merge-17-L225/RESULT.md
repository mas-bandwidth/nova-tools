RESULT tools22-rule-merge-17 sha=5298f6be12ea — does the code at this base do what docs/SPEC-MERGE.md rule 17 says?
CONFORMS internal/merge/classify.go:195
SPEC docs/SPEC-MERGE.md:1681 rule 17
PKG internal/merge
ASK The implementation must print `RUN BLOCKED` with parseable hand commands (clone, fetch, merge, push) naming every conflicting file when an entry conflicts, skip reprocessing blocked entries on subsequent passes, and allow a new head to clear the block.

deciding lines:
internal/merge/classify.go:195:	fmt.Fprintf(p.Stderr, "RUN BLOCKED entry=%s head=%s files=%d: %s\n",
internal/merge/classify.go:233-239: if e.State == StateBlocked && e.OID != "" && strings.Contains(e.Detail, "conflicts with") { ... return c }
internal/merge/blocked.go:62-84: HandCommand prints clone/fetch/merge/push with every conflicting file named
GUARDED-BY internal/merge/blocked_test.go:27 TestTheConflictRemedyReadsTheConfiguredRemote, internal/merge/blocked_test.go:94 TestTheHandCommandQuotesEveryValueItPrints, cmd/nova-merge/lane_test.go:53 TestOnlyAConflictingEntryIsReMerged

greps run:
grep -rn "RUN BLOCKED" --include='*.go' . → classify.go:195, lane_test.go:91,106
grep -rn "HandCommand\|blockedLine" --include='*.go' internal/merge/ → blocked.go:62, classify.go:191-198
grep -rn "\-\-gc" --include='*.go' . → (no output in internal/merge or cmd/nova-merge)
grep -rn "step\.log" --include='*.go' internal/merge/ cmd/nova-merge/ → (no output)
grep -rn "no marker, see" --include='*.go' . → (no match)
grep -rn "free-text.*search.*FAIL\|search.*error.*summaris" --include='*.go' . → (no match)
ls internal/merge/ → admin.go audit.go blocked.go build.go classify.go enqueue.go etc.

Notes:
Rule 17 has multiple clauses. Clauses 1-3 apply to internal/merge:
(1) RUN BLOCKED with hand commands: implemented via blockedLine() + HandCommand(), tested by blocked_test.go and lane_test.go
(2) Second pass prints nothing new: implemented via early return in remerge() at classify.go:233, tested by lane_test.go:102-108
(3) New head clears it: remerge proceeds normally when state doesn't match, or PR is no longer CONFLICTING

Clauses 4-6 do NOT apply to internal/merge:
(4) Gate runner's --gc step.log preservation: no `--gc` flag exists in this package; refers to external gate-runner CLI
(5) Step with marker quotes summary line / step without prints `no marker, see <path>`: no marker/summary quoting code in internal/merge
(6) Source test finds no free-text search for FAIL/error in summariser: no summariser exists in internal/merge; failuresIn(batch.go:1022) searches for FAIL but lives in cmd/nova-merge, not internal/merge

Left owed

git status --short
