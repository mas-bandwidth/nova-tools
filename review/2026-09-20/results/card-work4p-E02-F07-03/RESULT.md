RESULT work4p-E02-F07-03 sha=5298f6be12ea — nova-work E02-F07: does the green MOVE? criterion E02-F07-03: Derive who, stale, expired and handoffs views from lease history; distinguish responsible from working-now
DONE
CRITERION E02-F07-03 VERDICT HOLDS
REPO mas-bandwidth/nova-tools
NO-BRANCH
test file/name: lisp/nova-work/tests/replays-8663.lisp — `delegated-to-a-sleeper-then-recovered` (SPEC-WORK.md:4216); drives src/fleet.lisp `stale-entries`, `friend-who`, `reassign-node`. (`branch-and-window-required` in tests/acceptance/slice-05-durable-journal.lisp only asserts the validate-ask refusal guard for who/stale/handoffs under closed/root branches and would NOT notice a broken view derivation.)
baseline count line: NOVA-WORK SLICE1 total=419 pass=411 fail=8
mutation 1, src/fleet.lisp:342 — before: `(recovery-session-offers session)))` after: `'()))` (stale-entries no longer derives the `stale` view from the recovery-session's offer history)
count line after mutation 1: NOVA-WORK SLICE1 total=419 pass=410 fail=9
red output after mutation 1 (verbatim): TEST delegated-to-a-sleeper-then-recovered FAIL spec=docs/SPEC-WORK.md:4216 expected=offer-refused-exit-2;anyway-written-and-in-stale;reassigned-with-reading-cited;prior-lease-fenced;heartbeat-on-waking-refused;first-who-shows-reassignment: the written offer appears in stale: NIL
mutation 2, src/fleet.lisp:385 — before: `(when r` after: `(when nil` (friend-who never shows the reassignment, the `who` view is empty)
count line after mutation 2: NOVA-WORK SLICE1 total=419 pass=410 fail=9
red output after mutation 2 (verbatim): TEST delegated-to-a-sleeper-then-recovered FAIL spec=docs/SPEC-WORK.md:4216 expected=offer-refused-exit-2;anyway-written-and-in-stale;reassigned-with-reading-cited;prior-lease-fenced;heartbeat-on-waking-refused;first-who-shows-reassignment: the first who shows the reassignment: NIL
restored git status --short: (empty)
restored count line: NOVA-WORK SLICE1 total=419 pass=411 fail=8
Noticed: (1) baseline is red out of the box — 8 failures, all sandbox-environmental (Socket error in bind: 13 Permission denied on `endpoint-is-local-and-private`; Can't create directory /tmp/nova-work-reqline-* on the seven request-line CLI cases); all seven named E02-F07 tests PASS before mutation. (2) `delegated-to-a-sleeper-then-recovered` asserts the `who` and `stale` views derived from history, but the criterion also names `expired` and `handoffs` views and "distinguish responsible from working-now": no test anywhere in the tree asserts the expired view (leasebook-expire/lease-until is only exercised indirectly), the `handoffs` lease-transition-log view (state-lease-log rows), or the responsible-vs-working-now distinction (SPEC-WORK.md:136). Those clauses of E02-F07-03 are covered by no named test and would rot unseen; only the who/stale-from-history clause is earned here.