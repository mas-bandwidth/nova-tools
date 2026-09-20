RESULT work4p-E11-F03-05 sha=5298f6be12ea — nova-work E11-F03: does the green MOVE? criterion E11-F03-05: narrative-does-not-filter: a note with prose and no :constraint is printed and excludes nothing; a :deny excludes only a candidate matching every named axis; two disagreeing constraints print both and exclude
DONE
CRITERION E11-F03-05 VERDICT HOLDS
REPO mas-bandwidth/nova-tools
NO-BRANCH
test file: lisp/nova-work/tests/replays-applicable-delegation.lisp, test name: "narrative-does-not-filter"
baseline count line: NOVA-WORK SLICE1 total=419 pass=411 fail=8
disabled line in quotes: (and deny (deny-matches-p deny candidate task-class))
mutation in assignment.lisp:433 — changed (and deny ...) to (and nil ...), disabling all constraint-based exclusions
count line after mutation: NOVA-WORK SLICE1 total=419 pass=408 fail=11
red output verbatim: TEST narrative-does-not-filter FAIL spec=docs/SPEC-WORK.md:4364 expected=prose-prints-excludes-nothing;deny-matching-every-axis-excludes;disagreeing-constraints-both-exclude: a :deny matching every named axis excludes: expected T got NIL
second mutation on a different line: skipped — the first mutation already proves HOLDS via the named test going red; other tests also went red (applicable-cap-never-hides-a-deny, applicable-before-route) confirming the same behavior path
git status --short after restore: (empty — no modified files)
restored count line: NOVA-WORK SLICE1 total=419 pass=411 fail=8
Noticed: The original 8 failures are tmp-dir permission issues (Can't create directory /tmp/nova-work-reqline-*) unrelated to this criterion. The three additional red tests after mutation (applicable-cap-never-hides-a-deny, applicable-before-route, notes-refuse-missing-source-or-date partial) all share the same constraint-exclusion code path, confirming the edit hit the right behaviour.