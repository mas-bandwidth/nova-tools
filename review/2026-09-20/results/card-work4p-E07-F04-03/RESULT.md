RESULT work4p-E07-F04-03 sha=5298f6be12ea — nova-work E07-F04: does the green MOVE? criterion E07-F04-03: Keep source support separate from qualified acceptance and incomplete reconciliation visible
DONE
CRITERION E07-F04-03 VERDICT HOLDS
REPO mas-bandwidth/nova-tools
NO-BRANCH
test file lisp/nova-work/tests/replays-8646.lisp, test name moving-source
baseline: NOVA-WORK SLICE1 total=419 pass=411 fail=8
disabled src/operations.lisp:748
  before: (if (and (source-capture-complete-p cap)
  after:  (if t
after mutation: NOVA-WORK SLICE1 total=419 pass=411 fail=9
red output verbatim:
  TEST moving-source FAIL spec=docs/SPEC-WORK.md:6231 expected=captured=preserved,incomplete=marked,reconciled=yes: a mixed capture claimed a consistent snapshot: expected :INCOMPLETE got :CONSISTENT
second mutation: none (first mutation already turned the named test red)
restored git status --short: (empty)
restored count line: NOVA-WORK SLICE1 total=419 pass=411 fail=8
Noticed: The tree is not green at baseline: fail=8. All eight failures are environmental, not behavioural — socket bind Permission denied (endpoint-is-local-and-private) and "Can't create directory /tmp/nova-work-reqline-*" (the seven request-line suite cases); the sandbox forbids the bind and /tmp writes. They are unrelated to this criterion and reproduce identically before and after the mutation. Also noticed: the sexp's verified row for E07-F04 names tests "moving-source"; lisp/nova-work/src/replays-8646.lisp holds a copy of the same capture model but is NOT in nova-work.asd — the live definition the suite exercises is in lisp/nova-work/src/operations.lisp (asd component src/operations). A first probe edited the unloaded src/replays-8646.lisp copy and changed nothing; the mutation that matters is in operations.lisp:748, where capture-consistent-claim's completeness/mixed check lives.