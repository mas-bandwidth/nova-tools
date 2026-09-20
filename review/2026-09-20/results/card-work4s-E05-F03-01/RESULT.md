RESULT work4s-E05-F03-01 sha=5298f6be12ea — nova-work E05-F03: does the contract say it? criterion E05-F03-01: Record exact-revision reviewer findings and author dispositions
DONE
CRITERION E05-F03-01 SPEC STATED
REPO mas-bandwidth/nova-tools
NO-BRANCH
grep -in "exact-revision" docs/SPEC-WORK.md — 2 hits
grep -in "reviewer findings" docs/SPEC-WORK.md — 0 hits
grep -in "author disposition" docs/SPEC-WORK.md — 0 hits
grep -in "disposition" docs/SPEC-WORK.md — 75 hits
grep -in "finding" docs/SPEC-WORK.md — 82 hits
grep -in "exact revision" docs/SPEC-WORK.md — 4 hits
grep -in "reviewer" docs/SPEC-WORK.md — 11 hits
grep -in "finding ids" docs/SPEC-WORK.md — 1 hit
grep -in "review" docs/SPEC-WORK.md | grep -in "exact" — 6 hits
grep -n "E05-F03" docs/roadmaps/nova-work.sexp — 3 hits
grep -rn "review-cycles-stay-visible" lisp/nova-work/tests/ — 3 hits
grep -rn "an-attested-only-need-is-met-by-its-attestation-and-by-nothing-less" lisp/nova-work/tests/ — 2 hits
grep -rn "reconcile-preserves-contradiction" lisp/nova-work/tests/ — 4 hits
grep -rn "regression-opens-repair-work" lisp/nova-work/tests/ — 4 hits
grep -rn "reuse-only-valid-review" lisp/nova-work/tests/ — 3 hits
docs/SPEC-WORK.md:7207: - **`review-cycles-stay-visible`** — each required friend's exact-revision review and finding ids,
docs/SPEC-WORK.md:7208:   the author's dispositions and the clearance recorded; valid evidence reused and only the affected
docs/SPEC-WORK.md:7209:   delta reread; **repeated review and repair cycles visible as work and as operational cost**.
Supporting, same paragraph: docs/SPEC-WORK.md:7240 "before a release the exact-revision results are attached"; lock-gate reinforcement docs/SPEC-WORK.md:7377 "each requested friend's explicit disposition at this exact revision, with unresolved, unavailable and reserved reviewers recorded separately and no reply never counted as approval".
VERDICT STATED: docs/SPEC-WORK.md:7207-7209 state the roadmap row "Record exact-revision reviewer findings and author dispositions" verbatim-in-effect — "exact-revision review and finding ids" (reviewer findings at an exact revision), "the author's dispositions and the clearance recorded" (author dispositions, recorded).
:by-feature E05-F03 at docs/roadmaps/nova-work.sexp:825 has :state "missing" and :evidence () — empty; the test names live in the aggregate :tests entry at docs/roadmaps/nova-work.sexp:94. All five named tests exist in the tree: review-cycles-stay-visible (lisp/nova-work/tests/replays-8648.lisp:257), an-attested-only-need-is-met-by-its-attestation-and-by-nothing-less (lisp/nova-work/tests/replays-785-gate.lisp:326), reconcile-preserves-contradiction (lisp/nova-work/tests/replays-8640.lisp:57), regression-opens-repair-work (lisp/nova-work/tests/replays-8648.lisp:24), reuse-only-valid-review (lisp/nova-work/tests/replays-8648.lisp:220). No stale test evidence.
git status --short: (empty)
Noticed: the :by-feature entry for E05-F03 (nova-work.sexp:825) still reports :state "missing" with :evidence (), while the sexp's :tests aggregate (nova-work.sexp:94) marks E05-F03 :verified 4 :total 4 — a discrepancy between the two sexp views; a roadmap-internal matter, not a SPEC-WORK contract gap. The test annotation replay-8648.lisp:249 pins review-cycles-stay-visible to SPEC-WORK.md:6368-6370, which in this tree now holds unrelated protocol text — stale line anchor in the test file only, the contract line has moved to 7207-7209.