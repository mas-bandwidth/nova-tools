RESULT work4p-E05-F03-04 sha=5298f6be12ea — nova-work E05-F03: does the green MOVE? criterion E05-F03-04: reuse-only-valid-review: reuse a review only for unchanged reviewed content, acceptance and dependencies; retain independent friend gates, reviewer-selected integrating depth and repeat triggers
DONE
CRITERION E05-F03-04 VERDICT HOLDS
REPO mas-bandwidth/nova-tools
NO-BRANCH
Test file: lisp/nova-work/tests/replays-8648.lisp
Test name: "reuse-only-valid-review"
Baseline: NOVA-WORK SLICE1 total=419 pass=411 fail=8
Disabled: "(and (not (scope-review-independent-gate-p review))" at control.lisp:1293
Mutated 1: "(and t ; REMOVED: (not (scope-review-independent-gate-p review))"
Count after mut1: NOVA-WORK SLICE1 total=419 pass=410 fail=9
Red output: TEST reuse-only-valid-review FAIL spec=docs/SPEC-WORK.md:4851 expected=same-scope-reused;changed-acceptance-invalidates;changed-deps-invalidate;friend-gate-not-reusable: an independent friend gate cannot be replaced by reuse
Second mutation: also replaced (equal (scope-review-acceptance review) acceptance) with t at control.lisp:1296
Count after mut2: NOVA-WORK SLICE1 total=419 pass=410 fail=9
Red output mut2: TEST reuse-only-valid-review FAIL spec=docs/SPEC-WORK.md:4851 expected=same-scope-reused;changed-acceptance-invalidates;changed-deps-invalidate;friend-gate-not-reusable: a changed acceptance invalidates the review
Restored git status --short: (empty)
Restored count: NOVA-WORK SLICE1 total=419 pass=411 fail=8
Noticed: 8 pre-existing environmental failures (socket permissions, /tmp dir creation) unrelated to this criterion; both mutations caught by the same named test confirming it earns its green
