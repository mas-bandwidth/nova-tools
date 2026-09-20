RESULT work4-E10-F03-60 sha=5298f6be12ea — nova-work E10-F03 criterion E10-F03-60 (sexp id E10-F03-03): Map each suite to an owner, command and CI lane without duplicating its acceptance evidence
DONE
CRITERION E10-F03-03 STATE verified
BRANCH rowan/work4-E10-F03-60
REPO mas-bandwidth/nova-tools
PATHS lisp/nova-work/src/control.lisp lisp/nova-work/src/package.lisp lisp/nova-work/tests/replays-8641.lisp
SPEC-WORK docs/SPEC-WORK.md:7239-7240 — "invalidates that receipt. **Before the lock gate each suite is mapped to its named scenarios, assertions, owner, command and CI lane**; before a release the exact-revision results are attached." (lanes named at docs/SPEC-WORK.md:7089-7098)
TEST TestE10F03MapEachSuiteToAn
BASELINE NOVA-WORK SLICE1 total=419 pass=410 fail=9
RED TestE10F03MapEachSuiteToAn FAIL spec=docs/SPEC-WORK.md:7239-7240,7089-7098 expected=each-suite=owner-command-lane;lane=per-change-six-nightly-fifteen;owner=non-empty;command=non-empty;evidence=not-duplicated: The variable NOVA-WORK/TESTS::*PER-CHANGE-SUITES* is unbound.
GREEN RUN 1 NOVA-WORK SLICE1 total=420 pass=411 fail=9
GREEN RUN 2 NOVA-WORK SLICE1 total=420 pass=411 fail=9
NEGATIVE CONTROL NOVA-WORK SLICE1 total=420 pass=410 fail=10 — TestE10F03MapEachSuiteToAn FAIL ...: The variable NOVA-WORK/TESTS::*PER-CHANGE-SUITES* is unbound. (the only test that changed)
GIT-STATUS  M lisp/nova-work/src/control.lisp
  M lisp/nova-work/src/package.lisp
  M lisp/nova-work/tests/replays-8641.lisp
HEAD 98a94c78e298dfa42070ae07029b9669c2b8112b
Noticed The 9 baseline failures are environmental, not criterion-related: two socket binds fail with "EPERM (Operation not permitted)" and seven fail with "Can't create directory /tmp" — this sandbox forbids socket bind and writing under /tmp, so the session/endpoint tests cannot run here; they would run on hulk's gate. The previous card's other finding still holds: the roadmap sexp's E10-F03 source-sections ("Required test suites", "Staged verification and release") cite headings that no longer exist under those names; that is a roadmap-row citation issue distinct from this criterion, so I left it. The spec does not enumerate per-suite owners/commands/observables (7098: "each row still needs its fixture, command and observable before the lock gate"), so I grounded owner="stella" (the Preservation-and-recovery section author, SPEC-WORK.md:7045) and command="./run-tests.sh" (the lisp command) uniformly. Note: src/replays-8641.lisp is an orphan copy not registered in nova-work.asd; its live code is folded into src/control.lisp, so the production change went there, not into the orphan.
Left owed Per-suite owners, commands and observables are not yet uniquely named in the spec (SPEC-WORK.md:7098); a next card must fill those in once they are fixed. The E10-F03 roadmap row still cites "Required test suites" / "Staged verification and release" — headings that no longer exist — which is the sibling finding to fix.
