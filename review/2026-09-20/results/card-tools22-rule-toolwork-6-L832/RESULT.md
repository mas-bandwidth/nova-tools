RESULT tools22-rule-toolwork-6-L832 sha=5298f6be12ea — does the code at this base do what docs/SPEC-TOOLWORK.md rule 6 says?
CONFORMS cmd/nova-merge/batch.go:581
SPEC docs/SPEC-TOOLWORK.md:832 rule 6
PKG internal/docs
ASK An implementation must run the reading-3 hold fold at all four doors — batch while reading each member's ci-ok from the wire, land again over the receipt's members= from the wire immediately before Enqueuer.Enqueue, and queue sweep and react, each never enqueueing a held PR — printing reading 3's BATCH DROP / LAND REFUSED / holds= / dispositions= grammar, and have nova-pulse status count held PRs.
ASK is code: the rule binds the binary (where the fold runs), not a person or process.

Deciding lines:
- cmd/nova-merge/batch.go:581  `holds := merge.UnliftedHolds(vs, pr.HeadOID, pr.Author, rs)` — the fold at batch, on verdicts read from the wire (host.Verdicts, batch.go:571) while admitting each member; a held member is dropped with reading 3's grammar at batch.go:587-599 (`BATCH DROP #%d reason="head %s carries an unreleased HOLD" who=... hold=... source=...`) and the line carries `holds=%d dispositions=%s reviewers=%s` at batch.go:466.
- cmd/nova-merge/land.go:239  `holds := merge.UnliftedHolds(vs, mPR.HeadOID, mPR.Author, rs)` — the second read, over the receipt's members= (land.go:185-197), from the wire, immediately before `merge.NewEnqueuer(...).Enqueue` (land.go:285); one held member refuses the whole landing with `LAND REFUSED reason=held member=#%d ...` (land.go:243, 261).
- cmd/nova-merge/queue.go:408,422  `if len(merge.UnliftedHolds(laneVs, pr.HeadOID, pr.Author, nil)) > 0 { continue }` — `queue sweep` never enqueues a held PR (and refuses outright under a standing lane hold, queue.go:341-345).
- cmd/nova-merge/react.go:67-77 (`PRHeld` -> `merge.UnliftedHolds`) plus internal/ci/events_react.go:154-165 (`REACT hold` before `r.Enqueue`) — `react` never enqueues a held PR.
- internal/pulse/status.go:190-191  `STATUS OPEN dogfood=%d holds=%d escalations=%d` with `countLines(in.Queue, "HOLD")` — `nova-pulse status` counts held PRs (the one line this file adds).

GUARDED-BY cmd/nova-merge/hold_test.go:69 TestAHeldHeadIsDroppedFromABatchAndRefusedAtLand (batch drop + land refusal over the 02:34Z hold, confirmed passing)
GUARDED-BY cmd/nova-merge/hold_test.go:582 TestBatchOKCarriesHoldsDispositionsAndReviewers (holds=/dispositions= on BATCH OK, confirmed passing)
GUARDED-BY cmd/nova-merge/hold_test.go:1196 TestSweepAndReactNeverEnqueueHeldPR (queue sweep + react skip a lane-record-held PR, confirmed passing)
GUARDED-BY cmd/nova-merge/hold_test.go:1037 TestSweepAndReactNeverEnqueueAHeldPR (queue sweep exit 2 and REACT hold under a standing hold, confirmed passing)
GUARDED-BY internal/ci/events_test.go:253 TestReactorHoldsOnTheLanesHold (a held PR is not enqueued by react)
UNGUARDED internal/pulse/status.go:190-191 — the `STATUS OPEN ... holds=` count has no test anywhere in the tree (grep for `STATUS OPEN` in *_test.go is empty); the demanded test `status-counts-held-prs` named in SPEC-TOOLWORK.md:881 does not exist as a Go test. This is the one unguarded conformance in the rule and the cheapest kind of rot to report.

Greps ran:
- grep -rn "BATCH DROP\|LAND REFUSED\|holds=\|dispositions=" --include='*.go' internal/ cmd/
- grep -rn "UnliftedHolds" --include='*.go' internal/ cmd/ | grep -v _test
- grep -rn '"HOLD"\|HOLD"' --include='*.go' cmd/nova-pulse/ internal/pulse/
- grep -rn "STATUS OPEN" --include='*.go' . | grep -v _test
- grep -rn "STATUS OPEN\|OPEN dogfood\|dogfood=" --include='*_test.go' .
- grep -rn "sweep-and-react-never-enqueue-a-held-pr\|status-counts-held-prs" --include='*.go' .
- grep -rn "func Test" cmd/nova-merge/hold_test.go
Read: docs/SPEC-TOOLWORK.md:780-885, docs/SPEC-DECIDE.md:1122-1275, cmd/nova-merge/batch.go, land.go, queue.go:300-499, sweep.go, react.go, internal/merge/verdict.go:275-344, internal/merge/sweep.go, sweepgh.go, enqueue.go, internal/ci/events_react.go, internal/pulse/status.go.

Go test note: the named guards were confirmed with `GOMAXPROCS=8 go test ./cmd/nova-merge/ -count=1 -run '^Test...$'`; TestAHeldHeadIsDroppedFromABatchAndRefusedAtLand passes once the job wrapper repo has a git identity (the first run's failure was an environment artifact — t.TempDir() under .nova-sandbox-tmp sits inside the wrapper repo whose config had no user.name/email, so its `git commit` of the reviewers file failed; unrelated to the code under review).

Left owed
git status --short (in <JOBDIR>/repo):
```
```