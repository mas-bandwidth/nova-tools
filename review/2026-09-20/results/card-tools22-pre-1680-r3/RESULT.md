RESULT tools22-pre-1680-r2 sha=5298f6be12ea — a pre-read of mas-bandwidth/nova-tools#1680 at head 74825420150d: nova-merge: batch and land refuse a member carrying an unlifted HOLD (#1572)
PREREAD 1680 claims=13 proven=13 unproven=0 defects=0 high=0
PR 1680
HEAD 74825420150d1cda82ac0bafba9c7dda55a2d062
BASE dev
MERGE-BASE 31e351956a59a63e3da1b323abecb63da7b92cdd
BEHIND 39
FILES 7 production, 5 test
LINES +977 -38

CLAIMS
1. `nova-merge batch` gains --readers <login,...> that names which readers' HOLD verdicts the gate inspects.
PROVEN-BY cmd/nova-merge/batch_test.go:65 TestBatchDropsAMemberCarryingAnUnliftedHold — sets a hold verdict via l.host.SetVerdicts(), runs batch with --readers gafferongames, asserts exit ≠ 0 and stderr contains "BATCH DROP" naming the login and timestamp.
2. Batch drops a member whose pull request carries an unlifted HOLD from a named reader, printing the LOGIN and TIMESTAMP on the rejection line.
PROVEN-BY cmd/nova-merge/batch_test.go:79 — asserts stderr contains 'BATCH DROP #1 reason="an unlifted HOLD from gafferongames at 2026-09-19T02:34:25Z'.
3. An APPROVE from the same reader, at the same head, after the HOLD, lifts it so the member is admitted.
PROVEN-BY cmd/nova-merge/batch_test.go:94 TestAnApproveAtThisHeadFromTheSameReaderLiftsTheHold — sets hold + approve on same head from same reader, asserts exit 0 and stdout contains "members=1".
4. An APPROVE at an OLDER HEAD lifts nothing; the held member is still dropped.
PROVEN-BY cmd/nova-merge/batch_test.go:118 TestAnApproveAtAnOlderHeadDoesNotLiftTheHold — hold at current head, approve at older sha 6adbbd1d, asserts "members=none" and "dropped=1".
5. A HOLD from someone NOT in --readers does not drop any member.
PROVEN-BY cmd/nova-merge/batch_test.go:142 TestAHoldFromSomeoneOutsideTheNamedReaderSetDoesNotDrop — hold from "some-lane-bot", readers=["gafferongames"], asserts exit 0 and "members=1".
6. `--ignore-hold <n>` admits the member despite a hold, printing a BATCH NOTE on stderr naming the ignored hold.
PROVEN-BY cmd/nova-merge/batch_test.go:164 TestIgnoreHoldAdmitsTheMemberAndSaysWhichHoldItSteppedOver — sets hold from gafferongames, passes --ignore-hold 1, asserts exit 0 and stderr contains "BATCH NOTE #1 holds=ignored".
7. Without --readers and without --no-require-holds, batch refuses at exit 2 before running, printing "--readers <login,...>" and "#1572" on stderr.
PROVEN-BY cmd/nova-merge/batch_test.go:187 TestBatchRefusesWithNeitherReadersNorTheLoudWaiver — no --readers flag, asserts exit 2, stderr contains "--readers <login,...>" and "#1572".
8. --no-require-holds runs the gate anyway and prints "BATCH NOTE holds=unread" on stderr.
PROVEN-BY cmd/nova-merge/batch_test.go:210 TestNoRequireHoldsRunsAndSaysTheHoldsWereNotRead — hold present, --no-require-holds given, asserts exit 0 and stderr contains "BATCH NOTE holds=unread".
9. `nova-merge land` checks holds on its own receipt batch AND every member the receipt names, refusing (exit 1) if any carry an unlifted HOLD.
PROVEN-BY cmd/nova-merge/hold_test.go:238 TestLandRefusesWhenAMemberOfTheReceiptCarriesAnUnliftedHold — constructs a BATCH OK receipt naming member 1551, posts hold on that member, calls land with --readers, asserts exit 1, stderr contains "LAND REFUSED" and "pull request 1551 carries an unlifted HOLD".
10. A HOLD on the batch's OWN pull request stops landing too.
PROVEN-BY cmd/nova-merge/hold_test.go:268 TestLandRefusesWhenTheBatchItselfCarriesAnUnliftedHold — posts hold on batch PR 1560, calls land, asserts exit 1 and queue untouched.
11. Land goes through once the reader approves the batch's very head.
PROVEN-BY cmd/nova-merge/hold_test.go:291 TestLandGoesThroughWhenTheHoldIsLiftedAtThisHead — hold + approve on batch head, asserts exit 0 and "LAND OK pr=1560".
12. land refuses at exit 2 without --readers and without --no-require-holds, never touching the queue.
PROVEN-BY cmd/nova-merge/hold_test.go:310 TestLandRefusesWithNeitherReadersNorTheLoudWaiver — no --readers, asserts exit 2, len(q.enqueued)==0, stderr contains "--readers" and "#1572".
13. Internal package merge exposes Host.Verdicts(n int)([]Verdict,error), ParseVerdictLine(body)(word,head,ok), UnliftedHolds(vs,head,readers), decodeVerdicts(comments,reviews,n) — all parsed from PR comments and reviews.
PROVEN-BY-EXISTING internal/merge/verdict_test.go:12 TestParseVerdictLineReadsTheShapesThisRepositoryCarries — 12 input shapes (real PR comment bodies) with expected word/head/ok; TestUnliftedHoldsIsLiftedOnlyByThatReadersApproveAtThisHead — 11 scenarios covering time-order, head-matching, cross-reader isolation, case-folding, tie-resolution; TestDecodeVerdictsReadsCommentsAndReviews — paginated JSON decoding from GitHub API shape.

DEFECTS none

QUESTIONS FOR THE REVIEWER
1. verdict.go:71 `ParseVerdictLine` takes the first non-empty line of a comment body only. Are there real PR comment patterns on this repository where the verdict token and head appear on a LATER line? The test table at verdict_test.go:12 includes cases like `"Thanks — reading now.\n\nHOLD at \`...\`"` which returns false, but is this conservative-by-design or are there known examples that would break?
2. verdict.go:130 `UnliftedHolds` sorts verdicts per reader by `At` timestamp using string comparison. The timestamps come from GitHub's RFC3339 format (`created_at`, `submitted_at`). Has anyone verified that GitHub always produces lexicographically sortable RFC3339 stamps? If they ever switch to include timezone offsets like `+0000` vs `Z`, the sort order could change.
3. batch.go:~270 `batchHoldRead()` converts read errors (`host.Verdicts()`) to a general "could not be read" refusal. Does the calling code distinguish between auth failures, rate-limits, and genuine parse errors downstream? Or is it all collapsed into the same exit-1 refusal? Same question for landHoldRead at land.go:~220.
4. hold_test.go:238 uses a synthetic receipt line `BATCH OK name=integration-12t2 base=... head=... members=1551,...`. Is the `landSubjects` function (which parses this receipt to find member PRs) tested with receipts containing unexpected formatting — extra whitespace, missing fields, malformed member lists — or only the happy-path shape?

Left owed
I did not read the full text of batch.go (the existing code, not the diff) to understand the broader context of the batch gate and how the hold-dropping integrates with the existing member-evaluation pipeline. I did not read the full land.go beyond the diff hunks. I did not read the existing docs/ directory for a CLI.md that the rules mention. I relied on the diff hunks and test assertions alone for claims 1-13.

git status --short

git rev-parse HEAD
d576bf6bbabb39068096a97b4560de9b5e245970
