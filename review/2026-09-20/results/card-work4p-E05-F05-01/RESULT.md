RESULT work4p-E05-F05-01 sha=5298f6be12ea — nova-work E05-F05: does the green MOVE? criterion E05-F05-01: Use independent oracle/comparator and deliberately broken assertions
DONE
CRITERION E05-F05-01 VERDICT HOLDS
REPO mas-bandwidth/nova-tools
NO-BRANCH
test file: lisp/nova-work/tests/replays-8641.lisp
test name: a-broken-assertion-must-fail
baseline count line: NOVA-WORK SLICE1 total=419 pass=411 fail=8
disabled line: lisp/nova-work/src/control.lisp:582 (regression-evidence-p's deliberately-broken-behaviour check)
  before: `(not (funcall (regression-receipt-assertion receipt) broken)))))`
  after:  `t)))`
count line after mutation: NOVA-WORK SLICE1 total=419 pass=411 fail=8 -> NOVA-WORK SLICE1 total=419 pass=410 fail=9
red output verbatim:
TEST a-broken-assertion-must-fail FAIL spec=docs/SPEC-WORK.md:6371 expected=mutation-makes-the-assertion-fail;green-badge-only-on-real-evidence;criterion-revision-uncertainty-preserved: a test that still passes under the mutation is not evidence: expected NIL got T
second mutation: none needed — the named test went RED on the first mutation, so no second probe was required by STEP 5
restored git status --short: (empty)
restored count line: NOVA-WORK SLICE1 total=419 pass=411 fail=8
Noticed: the baseline suite is RED before any mutation — fail=8 — all eight are sandbox-environment failures unrelated to this criterion: endpoint-is-local-and-private (socket bind: Permission denied), six request-line tests (Can't create directory /tmp/nova-work-reqline-*), and notes-refuse-missing-source-or-date. They failed identically before, during, and after the probe, so they are environmental, not mutation-caused. Also noticed: src/replays-8641.lisp is a dead duplicate of the regression-receipt code in src/control.lisp; it is not listed in nova-work.asd and is not what the tests exercise — the live production copy the named test asserts is src/control.lisp.