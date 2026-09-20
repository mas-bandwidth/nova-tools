RESULT work4p-E04-F02-03 sha=5298f6be12ea — nova-work E04-F02: does the green MOVE? criterion E04-F02-03: Print completed required leaves as k/n and never average percentages
DONE
CRITERION E04-F02-03 VERDICT HOLDS
REPO mas-bandwidth/nova-tools
NO-BRANCH
Test file: lisp/nova-work/tests/acceptance/slice-09-replays-roadmap.lisp
Test name: "percent-axis-on-a-matrix"
Baseline count line: NOVA-WORK SLICE1 total=419 pass=411 fail=8 (8 pre-existing failures in the tree)
Disabled line: "lisp/nova-work/src/roadmap.lisp:1174" which produced the k/n output:
(format nil "QUERY ROW cell=~A,~A k/n=~D/~D unknown=~D" m (or axis "-") done total unknown)
Mutation applied: changed format string to remove "k/n=" (replaced with "%REMOVED-k/n-")
Count line after mutation: NOVA-WORK SLICE1 total=419 pass=410 fail=9
Red output verbatim: TEST percent-axis-on-a-matrix FAIL spec=docs/SPEC-WORK.md:5590 expected=applicable-is-per-axis-member;rows-and-baseline-rows-roadmap-wide;percentage-over-applicable;zero-applicable-prints-no-percentage;matrix-requires-axis;non-matrix-refuses-axis;partial-cell-k-n-and-unknown: the partial cell's k/n was not 1/2: QUERY OK ask=percent node=rm axis=c1 row-kind=feature green=9 applicable=10 rows=10 baseline-rows=10 done-unverified=0 percent=90%
Second mutation: none needed - the named test went RED on the first mutation, confirming the test catches the broken k/n behavior
Restored git status --short: (empty)
Restored count line: NOVA-WORK SLICE1 total=419 pass=411 fail=8 (matches baseline exactly)
Noticed the tree had 8 pre-existing failures before any mutations were applied; these are unrelated to this criterion probe
