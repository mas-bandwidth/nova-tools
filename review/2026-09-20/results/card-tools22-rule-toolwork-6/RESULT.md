RESULT tools22-rule-toolwork-6 sha=5298f6be12ea
NOT-CODE — binds a card writer / process, not a binary

SPEC docs/SPEC-TOOLWORK.md:942 rule 6
PKG internal/docs
ASK A transcript-test card writer must report document/tool discrepancies as BLOCKED drift rather than silently editing docs/TESTS.md, leaving resolution to a person.

Rule summary: When the document and the tool disagree, the card's line 2 is `BLOCKED drift <file:line>` with both lines, and which is wrong is a person's decision resolved by a subsequent fix-red or text card (e.g., nova-post's fake channel per Q5).

Why NOT-CODE:
1. "A `transcript-test` card may not touch `docs/TESTS.md` (§5)" — the constraint lives in §5 rule 2's PATHS table (transcript-test may only use `cmd/<tool>/firstrun_test.go` and `testdata/firstrun/**`, never `docs/TESTS.md`). The mechanism to enforce this — kind-specific PATHS restrictions via the kinds table in `internal/pulse/kinds.go` — is NOT YET IMPLEMENTED. Comment at `internal/swarm/lintheader.go:55-58`: "The kinds table is `internal/pulse/kinds.go`, which SPEC-TOOLWORK.md §5 rule 3 makes the one source of truth and which is not on `dev` either. Writing a second table here to close them would be the same mistake ... so they wait for T06a."
2. "When the document and the tool disagree the card's line 2 is `BLOCKED drift <file:line>`" — this instructs a CARD WRITER what to put in their RESULT.md. No binary prints `BLOCKED drift`; `harvest.go:504` recognizes cards whose line 2 starts with "BLOCKED" as "mismatch" but does not produce the drift format.
3. "which of the two is wrong is a person's decision" — explicit process language, not code behavior.

Existing relevant code (supports detection but does not implement the rule):
- `internal/onboarding/transcript.go:CompareTranscript` — compares tool output against docs/TESTS.md line-for-line (found via grep on `CompareTranscript`)
- `internal/dogfood/ledger_test.go:125` — test comment "docs drift is a finding, not a silent drop"
- `cmd/nova-check/hygiene.go:87-98` — validates KIND against declared types and PATHS generally (no kind-specific PATHS constraints)
- `internal/hygiene/glob.go:ValidatePaths` — validates PATHS glob form (no literal segment, matches at least one file)
- `internal/decide/ladder.go:53` — `KindTranscriptTest = "transcript-test"` exists as a recognized kind

Greps run:
```
grep -rn "drift" --include='*.go' . | head-30 (drift means worker/card drift, transcript drift, spec drift — none produce "BLOCKED drift")
grep -rn "false.*transcript\|transcript.*disagre\|BLOCKED drift" --include='*.go' . (no hits beyond spec prose)
grep -rn "transcript.test\|transcript-test\|KindTranscriptTest" --include='*.go' . (KIND defined, no PATHS enforcement)
grep -rn "transcript-test.*rejects.*edit" --include='*.go' . (red test listed in spec but no corresponding Go function found)
ls internal/docs/ (28 test files, none implementing rule 6)
```

Left owed

git status --short
