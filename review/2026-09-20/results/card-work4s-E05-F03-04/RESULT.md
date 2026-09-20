RESULT work4s-E05-F03-04 sha=5298f6be12ea — nova-work E05-F03: does the contract say it? criterion E05-F03-04: reuse-only-valid-review: reuse a review only for unchanged reviewed content, acceptance and dependencies; retain independent friend gates, reviewer-selected integrating depth and repeat triggers
DONE
CRITERION E05-F03-04 SPEC PARTIAL
REPO mas-bandwidth/nova-tools
NO-BRANCH
grep -n "reuse-only-valid-review" docs/SPEC-WORK.md: 1 hit
grep -n "reuse a review" docs/SPEC-WORK.md: 0 hits
grep -n "friend gates" docs/SPEC-WORK.md: 1 hit
docs/SPEC-WORK.md:5565: | reuse-only-valid-review | Same-scope review is reusable; changed acceptance/dependencies invalidate it; independent friend gates cannot be replaced by reuse. |
PARTIAL clauses without contract: "reviewer-selected integrating depth" and "repeat triggers"
:by-feature test names for E05-F03: review-cycles-stay-visible; an-attested-only-need-is-met-by-its-attestation-and-by-nothing-less; reconcile-preserves-contradiction; regression-opens-repair-work; reuse-only-valid-review
Test existence: reuse-only-valid-review found in lisp/nova-work/tests/replays-8648.lisp; review-cycles-stay-visible, an-attested-only-need-is-met-by-its-attestation-and-by-nothing-less, reconcile-preserves-contradiction, regression-opens-repair-work not directly found in tests/
git status --short: 
Noticed: E05-F03-04 specific identifier not found in repo; only E05-F03 feature exists with reuse-only-valid-review test
