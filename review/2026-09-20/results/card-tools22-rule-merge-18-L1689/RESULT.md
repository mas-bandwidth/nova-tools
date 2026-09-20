RESULT tools22-rule-merge-18 sha=5298f6be12ea — does the code at this base do what docs/SPEC-MERGE.md rule 18 says?
UNGUARDED
SPEC docs/SPEC-MERGE.md:1689 rule 18
PKG internal/merge
ASK A lane classifies an entry as NEEDS-GATE when its gate record no longer matches the current base, builds the integration commit with `RUN BUILT`, names the remediation command with `RUN NOTE`, selects among competing gate records by timestamp (newest wins, red breaks ties), rejects malformed `gate` invocations, refuses invalid state files, distinguishes base-gates (three equal SHAs) from integration-gates (three differing SHAs), and races publication on a lease when the base moves between read and push.

Key implementation locations:

internal/merge/state.go:52 — StateNeedsGate = "NEEDS-GATE"
state.go:352-386 — ValidGate() enforces full 40-char head, base, merge SHAs; a gate record missing base or merge returns error (exit 2 via Decode → validate → Load)

internal/merge/read.go:154-191 — StandOfGates() sorts gate records by `at` ascending, preferring green-over-red on ties ("RED LAST"); picks the newest record for (oid, baseSHA) as Kind="merge", the newest green for an older base as Kind="head", stale as Kind="stale", else "-". Green()/Red() report verdict of deciding record.

internal/merge/classify.go:145-176 — build() fetches head ref, calls BuildIntegration(baseSHA, e.OID), prints `RUN BUILT entry=... head=... base=... merge=... ref=...`. Sets res.Note with gate command string.

internal/merge/pass.go:145-225 — baseLine() prints `RUN BASE ... gate=<green|->` calling baseGateGreen(), which checks for a record with branch==base AND head==baseSHA AND base==baseSHA AND merge==baseSHA AND verdict=="green". Prints RUN NOTE with remedy command.

internal/merge/publish.go:80-163 — merge() verifies HasObject(rec.Merge), Parents are (baseSHA, e.OID), publishes via atomic host primitive or lease push, handles RacedError → `MERGE RACED ... nothing was published; this pass stops`, verified read-back.

cmd/nova-merge/verbs.go:543-649 — cmdGate() validates required flags (head/base-sha/merge all 40-char), refuses --base flag naming --base-sha, rejects mixed SHA mixes (neither all-equal nor all-differ): verbs.go:580-586 "THE TWO KINDS, told apart by the shas."

internal/merge/plan.go:67-103 — standing() computes state and admitted value. headGateGreen admits via gate. Gate.Red() returns StateRed (classify.go:121 detail: "the newest gate record for head %s against base %s is red (%s)"). Gate.Green() + satisfied reads + not basePending → StateMergeableGreen. Otherwise NEEDS-GATE or PENDING.

Tests confirming these behaviors:

internal/merge/state_test.go:53 — TestAGateWithoutBaseOrMergeRefuses: gate records missing base or merge, or short base SHA, are refused during Decode. Guards: verbs.go:557-566 and state.go:370-375.
GUARDED-BY internal/merge/state_test.go:53 TestAGateWithoutBaseOrMergeRefuses

cmd/nova-merge/publication_test.go:196 — TestAGateIsForThePairAndTheNewestRecordDecides: green record at 13:00, red at 13:05 → B is RED and nothing merges; green at 13:09 after the red → merges ("red then green merges"). Guards: read.go:154-191 (StandOfGates sorting).
GUARDED-BY cmd/nova-merge/publication_test.go:196 TestAGateIsForThePairAndTheNewestRecordDecides

cmd/nova-merge/publication_test.go:229 — TestATieBetweenAGreenAndARedFoldsRedLast: same `at` → red decides. Guards: read.go:161-166 sort tie-breaking.
GUARDED-BY cmd/nova-merge/publication_test.go:229 TestATieBetweenAGreenAndARedFoldsRedLast

cmd/nova-merge/lane_test.go:342 — TestAPendingBaseWaitsAndTheNoteNamesTheGateCommand: pending base below main; RUN NOTE names base gate command with three equal SHAs. Guards: pass.go:145-225 (baseLine, baseGateGreen).
GUARDED-BY cmd/nova-merge/lane_test.go:342 TestAPendingBaseWaitsAndTheNoteNamesTheGateCommand

cmd/nova-merge/lane_test.go:212 — TestABranchEntryMergesOnAGateAndAReadWithNoHostedChecks: branch entry below main with base gate (three equal SHAs) plus integration gate merges. Guards: read.go, plan.go.
GUARDED-BY cmd/nova-merge/lane_test.go:212 TestABranchEntryMergesOnAGateAndAReadWithNoHostedChecks

cmd/nova-merge/publication_test.go:129 — TestAMovedBaseIsRacedBeforeAnythingIsPublished: base moved before publish → MERGE RACED, exit 1, pass stops. Guards: publish.go:108-111.
GUARDED-BY cmd/nova-merge/publication_test.go:129 TestAMovedBaseIsRacedBeforeAnythingIsPublished

cmd/nova-merge/publication_test.go:270 — TestBelowMainAHostedRedIsNamedOnTheMergeLineAndNeverBlocks: lane with base rowan/step-2, base gate (three equal SHAs) → RUN BASE gate=green, entry with green gate merges. Guards: pass.go:baseGateGreen().
GUARDED-BY cmd/nova-merge/publication_test.go:270 TestBelowMainAHostedRedIsNamedOnTheMergeLineAndNeverBlocks

cmd/nova-merge/contract_test.go:90 — "--base on gate" refusal names --base-sha. Guards: verbs.go flag parsing.
GUARDED-BY cmd/nova-merge/contract_test.go:90 TestAFlagIsRefusedOffTheVerbThatOwnsIt

cmd/nova-merge/firstrun_test.go:155-156, 189 — gate with no base-sha/merge refused; short SHAs refused. Guards: verbs.go:557-566.
GUARDED-BY cmd/nova-merge/firstrun_test.go:155 TestARefusalSaysWhatTheFlagWants

Unguared finding: The primary scenario described in rule 18's opening clause — "Two entries A and B both gated green against base X; A merges (base is now X+A); B is not merged on its recorded gate: B is NEEDS-GATE with gate=head" — is NOT directly tested. While the code paths are exercised indirectly (classification, building, note generation), no test explicitly sets up two non-conflicting entries on the same base, gates both green, merges the first, and then verifies the second becomes NEEDS-GATE with its old gate rendered stale and a new integration commit built against the new base. This is the core behavioral claim of rule 18.

Greps run:
grep -rn "NEEDS-GATE" --include='*.go' internal/merge/
grep -rn "RUN BUILT" --include='*.go' internal/merge/
grep -rn "RUN NOTE" --include='*.go' internal/merge/
grep -rn "MERGE RACED" --include='*.go' internal/merge/
grep -rn "gate.*head\|gateHead\|GateHead" --include='*.go' internal/merge/
grep -rn "two.precondition\|allEqual\|allDiffer\|base.*gate.*green" --include='*.go' internal/merge/
grep -rn "newest.*green\|newest.*red\|newest.*record\|RED LAST" --include='*.go' internal/merge/
grep -rn "short.*sha\|truncated.*sha\|twelve.*char\|12-character" --include='*.go' internal/merge/
grep -rn "func Test" --include='*_test.go' internal/merge/
grep -rn "func Test" cmd/nova-merge/ --include='*_test.go' | grep -i "gate\|two.kind\|green.*red\|red.*green\|race"
grep -rn "name no object\|BASE.*gate.*has\|INTEGRATION.*gate" cmd/nova-merge/ --include='*.go'
grep -rn "either.kind\|allEqual\|allDiffer\|no object\|BASE.*gate.*has\|INTEGRATION.*gate.*three" cmd/nova-merge/ internal/merge/ --include='*.go'

Left owed

git status --short
?? .lease
?? .nova-sandbox-tmp/
?? RESULT.md
?? harness-output.log
?? opencode.json
?? repo/
