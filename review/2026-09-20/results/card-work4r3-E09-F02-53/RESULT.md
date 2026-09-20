RESULT work4r3-E09-F02-53 sha=5298f6be12ea — nova-work E09-F02 criterion E09-F02-53 (sexp id E09-F02-03): Track pending, confirmed and failed outbound actions with receipts
DONE
CRITERION E09-F02-03 STATE verified
BRANCH rowan/work4r3-E09-F02-53
REPO mas-bandwidth/nova-tools
PATHS lisp/nova-work/src/package.lisp lisp/nova-work/src/pricing.lisp lisp/nova-work/tests/replays-8645.lisp
docs/SPEC-WORK.md:7576-7581 "Track correspondence actions as pending, confirmed or failed with request IDs and receipts. Reporting a fix or closing an issue is a distinct outbound action under the team's configured authority, tied to the required work and actual landing/acceptance evidence. A locally completed attempt, a deferred work node, or a scope reduction does not by itself close the public issue. Retry uncertain outbound actions idempotently and preserve concurrent human changes. A reopened issue creates a reconciliation signal; it neither disappears nor silently erases previous completion evidence."
TestE09F02TrackPendingConfirmedAndFailed
BASELINE NOVA-WORK SLICE1 total=419 pass=411 fail=8
RED NOVA-WORK SLICE1 total=420 pass=411 fail=9
RED-FAIL "TEST TestE09F02TrackPendingConfirmedAndFailed FAIL spec=docs/SPEC-WORK.md:7576-7581 expected=pending-confirmed-failed-with-request-id-and-receipt;idempotent-retry;reopen-reconciles-without-erasing: The function NOVA-WORK/TESTS::MAKE-CORRESPONDENCE-LEDGER is undefined."
GREEN NOVA-WORK SLICE1 total=420 pass=412 fail=8
GREEN-TEST "TEST TestE09F02TrackPendingConfirmedAndFailed PASS spec=docs/SPEC-WORK.md:7576-7581 expected=pending-confirmed-failed-with-request-id-and-receipt;idempotent-retry;reopen-reconciles-without-erasing"
NEGATIVE-CONTROL NOVA-WORK SLICE1 total=420 pass=411 fail=9
NEGATIVE-CONTROL-FAIL "TEST TestE09F02TrackPendingConfirmedAndFailed FAIL spec=docs/SPEC-WORK.md:7576-7581 expected=pending-confirmed-failed-with-request-id-and-receipt;idempotent-retry;reopen-reconciles-without-erasing: The function NOVA-WORK:MAKE-CORRESPONDENCE-LEDGER is undefined."
git status --short "M  lisp/nova-work/src/package.lisp" "M  lisp/nova-work/src/pricing.lisp" "M  lisp/nova-work/tests/replays-8645.lisp"
head 5a91e58fa5c096fd1faf8bd9e1726498ff52c02f
Noticed The eight pre-existing failures are the bench/wall only (seven "Can't create directory /tmp/nova-work-reqline-..." and one "Socket error in bind: 13"), none changed between baseline and green; the temp-dir suffix is the only diff. The subfeature's sibling criteria (E09-F02-01 mapping, E09-F02-02 remote-text-as-data) are already verified under read-only-intake and hostile-data; this card adds only the third subfeature. Adapter mutation seam remains read-only (no provider is realised in slice 1).
Left owed A live issue-intake session that records these outbound actions durably against a journal (request id -> pending/confirmed/failed + receipt) and reconciles a reopened public issue against the ledger, so the pure model's reopen signal binds to a real provider revision boundary (E09-F03 migration and E09-F05 adapter safety build on it).
