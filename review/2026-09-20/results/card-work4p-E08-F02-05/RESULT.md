RESULT work4p-E08-F02-05 sha=5298f6be12ea — nova-work E08-F02: does the green MOVE? criterion E08-F02-05: Support bounded read bundles at one captured revision and lease-time watermark, with stable paginated snapshot identity
DONE
CRITERION E08-F02-05 VERDICT HOLDS
REPO mas-bandwidth/nova-tools
NO-BRANCH
TEST lisp/nova-work/tests/replays-8642.lisp :: batches-and-pipelines (B2 block, SPEC-WORK.md:2762-2814)
BASELINE NOVA-WORK SLICE1 total=419 pass=411 fail=8
DISABLED src/transport.lisp:1006 before: `(if (< (read-bundle-revision bundle) floor)` after: `(if nil`
AFTER-MUTATION NOVA-WORK SLICE1 total=419 pass=410 fail=9
RED VERBATIM: TEST batches-and-pipelines FAIL spec=docs/SPEC-WORK.md:2762-2814 expected=read-bundle=one-revision-and-watermark,expired-page-refused,atomic=all-or-none-with-entry-id,long-op-refused-in-atomic,prefix=exact,unattempted=marked,continuation=explicit,long-op-returns-operation-id: an expired page answers no fields
RESTORED git status --short empty; restored count NOVA-WORK SLICE1 total=419 pass=411 fail=8 (matches baseline exactly)
Noticed: baseline suite is already red before any mutation, fail=8 — all 8 are sandbox/environment failures unrelated to this criterion: endpoint-is-local-and-private (Socket error in bind: 13 Permission denied) and the seven session-status-*/request-line socket tests (Can't create directory /tmp/nova-work-reqline-*). batches-and-pipelines passes at baseline, so the criterion's green is earned: disabling the page-expiry guard moves exactly that test red (pass 411->410, fail 8->9), and the sexp's named test catches it.