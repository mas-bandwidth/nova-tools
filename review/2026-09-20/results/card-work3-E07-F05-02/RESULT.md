RESULT work3-E07-F05-02 sha=f01a0c42d7de — nova-work E07-F05 acceptance criterion, criterion E07-F05-02 (docs/roadmaps/nova-work.sexp): Record failures, repairs, denominator movement and changed scope
DONE
CRITERION E07-F05-02 STATE verified

BRANCH rowan/work3-E07-F05-02
REPO mas-bandwidth/nova-tools
PATHS lisp/nova-work/tests/replays-8642.lisp

SPEC-WORK line (found via `grep -n "Record failures and repairs" docs/SPEC-WORK.md`):
  docs/SPEC-WORK.md:7867 — "focus. Record failures and repairs as they occur. At the retrospective,
  compare the same questions and acceptance scope against the previous manual workflow, then
  change the spec."
The criterion (subfeature E07-F05-02, "Record failures, repairs, denominator movement and changed
scope") maps to this retrospective paragraph; source-sections are "Retrospective required before
production implementation" (SPEC-WORK.md:7863) and the operational-lessons scope text.

Test added: TestE07F05RecordFailuresRepairsDenominatorMovement (docs/SPEC-WORK.md:7867),
in lisp/nova-work/tests/replays-8642.lisp, following the sibling E07-F05-01 test's style in
replays-8682.lisp. It drives the roadmap scope machinery and asserts the kernel records all four:
  - a failure: a row add naming a missing member is refused by name ("no such member", exit 2)
    and writes nothing (no scope event, no revision move, denominator unchanged);
  - a repair: re-adding a removed row restores the denominator and is recorded in the scope log
    with its own reason;
  - denominator movement: roadmap-open-member-count drops/restores across remove/re-add;
  - a changed scope: a removal moves the roadmap view revision and records a :row-remove scope
    event carrying its :reason, plus the :retired list.

Suite counts (run twice, identical; suite not order-dependent):
  NOVA-WORK SLICE1 total=424 pass=416 fail=8
  NOVA-WORK SLICE1 total=424 pass=416 fail=8

My test is GREEN (PASS) in both runs, so the roadmap row for E07-F05-02 can be ticked: the kernel
already records failures, repairs, denominator movement and changed scope, and my new test names
its SPEC-WORK line so the next reader need not take my word for it.

The 8 failing tests are pre-existing and unrelated to this criterion: the session-status-over-
the-socket test and the seven request-line tests all fail with "Can't create directory
/tmp/nova-work-reqline-…" (a /tmp sandbox-permission issue, not a scope/roadmap defect).

git status --short (after commit): (clean; the single allowed file was committed)
head a8ffb6114f1cfb2da0f6f6d316ffa0a49befd2be

Left owed: none for this criterion. Note for the next reader — the 8 /tmp-related failures are
environmental and outside the criterion; they did not change across my edit.
