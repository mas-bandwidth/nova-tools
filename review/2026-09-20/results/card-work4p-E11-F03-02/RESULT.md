RESULT work4p-E11-F03-02 sha=5298f6be12ea — nova-work E11-F03: does the green MOVE? criterion E11-F03-02: applicable-cap-never-hides-a-deny: with more active notes than --max and the only :deny in the note that sorts last, applicable prints excluded with that id and NOTES MORE; goal show prints the constraint row uncut
DONE
CRITERION E11-F03-02 VERDICT HOLDS
REPO mas-bandwidth/nova-tools
NO-BRANCH
lisp/nova-work/tests/acceptance/slice-05-durable-journal.lisp:115 applicable-cap-never-hides-a-deny
NOVA-WORK SLICE1 total=419 pass=411 fail=8
src/assignment.lisp:493: "(ordered (append constraints narrative)))"
NOVA-WORK SLICE1 total=419 pass=410 fail=9
TEST applicable-cap-never-hides-a-deny FAIL spec=docs/SPEC-WORK.md:5498-5502 expected=constraint-row-before-cut-rows;notes-more;fail-no-eligible-no-row: the constraint row is shown before any cut row: the deny is never hidden
No second mutation tried
git status --short: (empty)
NOVA-WORK SLICE1 total=419 pass=411 fail=8
Noticed 8 pre-existing failures unrelated to this criterion
