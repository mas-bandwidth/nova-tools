RESULT tools22-rule-wake-10-L3293 sha=5298f6be12ea — does the code at this base do what docs/SPEC-WAKE.md rule 10 says?
CONFORMS cmd/nova-wake/serve.go:390
SPEC docs/SPEC-WAKE.md:3293 rule 10
PKG internal/wake
ASK The serve loop must dispatch only To:-addressed notes in bus order, mark Cc: notes as cc without dispatching them, handle kills at three boundaries (before-spawn, before-delivered, after-delivered) by writing the correct state transitions and surfacing uncertain entries with WAKE UNCERTAIN and WAKE BLOCKED lines, support --on-note-idempotent retries (one automatic retry of interrupted attempt=1, blocking the queue until the retry's delivered rc=0), refuse a second automatic retry, coalesce queued notes into a single invocation, batch at --batch-max, send receipts only after delivered rc=0, refuse --redeliver for non-uncertain entries, and preserve bus order across restarts.
Deciding lines:
  cmd/nova-wake/serve.go:404-413  recordNote: "to" queues, "cc" counts only, default ignores
  cmd/nova-wake/serve.go:511-516  runBatch writes dispatching then spawns; serveKillPoint "before-spawn" returns before spawn
  cmd/nova-wake/serve.go:519-521  serveKillPoint "before-delivered" returns after spawn, before delivered
  cmd/nova-wake/serve.go:528-548  non-zero exit marks Uncertain with WAKE UNCERTAIN; zero exit marks Delivered
  cmd/nova-wake/serve.go:554-556  serveKillPoint "after-delivered" returns after delivered
  cmd/nova-wake/serve.go:286-311  recover: finds dispatching entries, marks Uncertain, prints WAKE UNCERTAIN; idempotent retry for attempt=1
  cmd/nova-wake/serve.go:316-323  blocked returns first uncertain id
  cmd/nova-wake/serve.go:496-503  blockedOnce prints WAKE BLOCKED line
  cmd/nova-wake/serve.go:609-617  redeliverOne refuses non-uncertain; runs batch for uncertain
  cmd/nova-wake/serve.go:562-564  receipt sent only after delivered rc=0
  cmd/nova-wake/serve.go:474-488  dispatch collects batch up to batchMax
  cmd/nova-wake/serve.go:427-435  remember preserves bus order
GUARDED-BY cmd/nova-wake/serve_test.go:28 TestServeSurfacesTheUncertainAndWakesOnlyTo (basic To/Cc dispatch, cc=1, ids-only assertion)
GUARDED-BY cmd/nova-wake/serve_test.go:120 TestServeAKilledDispatchIsUncertainAndBlocksTheQueue (kill before-spawn/before-delivered, WAKE UNCERTAIN, WAKE BLOCKED, queued=1, uncertain=1, redeliver, refuse non-uncertain)
GUARDED-BY cmd/nova-wake/serve_test.go:200 TestServeAKillAfterDeliveredFiresNothingMore (kill after-delivered fires zero more)
GUARDED-BY cmd/nova-wake/serve_test.go:226 TestServeTheIdempotentRetryAndItsCompletionBoundary (idempotent retry, completion boundary, non-zero retry is uncertain, B queued behind)
GUARDED-BY cmd/nova-wake/serve_test.go:300 TestServeBatchesAndReceiptsAfterDelivered (batch-max 2 fires two then one; receipt after rc=0, no receipt for rc=3, WAKE FIRED rc=3)
GUARDED-BY cmd/nova-wake/serve_test.go:487 TestServeRunsNoThirdAttemptOnItsOwn (no third automatic run)
GUARDED-BY cmd/nova-wake/serve_test.go:526 TestServeCoalescesWhatWaitedAndMeasuresHowLongItWaited (three notes queued fire in one invocation)
GUARDED-BY cmd/nova-wake/serve_test.go:585 TestServeKeepsBusOrderAcrossARestart (bus order preserved across restart)
GUARDED-BY cmd/nova-wake/serve_test.go:760 TestServeDoesNotLoseANoteWhoseCommandFailed (non-zero exit -> uncertain, not delivered; redeliver works after fix)
GUARDED-BY cmd/nova-wake/serve_test.go:842 TestServeBlocksQueuedNotesBehindFailedDispatchUntilRedeliver (uncertain blocks queue, redeliver unblocks)
Greps run:
  grep -rn "serve\|on-note\|on_note\|redeliver\|uncertain\|WakesOnly\|TestServe" --include='*.go'
  grep -n "func Test" --include='*_test.go' cmd/nova-wake/
  grep -rn -i "release\|surviv\|sigkill\|blocked\|child" --include='*_test.go' cmd/nova-wake/
  grep -n "block" cmd/nova-wake/serve_test.go
Left owed: none — the implementation in serve.go and the ten guarding tests together cover every clause of rule 10.
git status --short: (nothing to commit, working tree clean)