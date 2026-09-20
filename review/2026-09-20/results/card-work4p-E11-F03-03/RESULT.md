RESULT work4p-E11-F03-03 sha=5298f6be12ea — nova-work E11-F03: does the green MOVE? criterion E11-F03-03: applicable-unknown-is-not-eligible: with no live session and no --snapshot, a snapshot past a bound, a missing notes index or an unregistered model, print NOTES FAIL and no eligible row
DONE
CRITERION E11-F03-03 VERDICT HOLDS
REPO mas-bandwidth/nova-tools
NO-BRANCH
lisp/nova-work/tests/replays-applicable-delegation.lisp applicable-unknown-is-not-eligible
NOVA-WORK SLICE1 total=419 pass=411 fail=8
lisp/nova-work/src/assignment.lisp:460: ((not notes-index) "notes index unloadable")
NOVA-WORK SLICE1 total=419 pass=396 fail=23
TEST applicable-unknown-is-not-eligible FAIL spec=docs/SPEC-WORK.md:4346 expected=fail-no-eligible-on-no-source;fail-on-past-bound;fail-on-missing-index;unknown-model-not-eligible: a missing notes index prints NOTES FAIL
lisp/nova-work/src/assignment.lisp:461: ((eq source :none) "no live session and no snapshot")
NOVA-WORK SLICE1 total=419 pass=383 fail=36
TEST applicable-unknown-is-not-eligible FAIL spec=docs/SPEC-WORK.md:4346 expected=fail-no-eligible-on-no-source;fail-on-past-bound;fail-on-missing-index;unknown-model-not-eligible: no source prints NOTES FAIL
NOVA-WORK SLICE1 total=419 pass=411 fail=8
Noticed Baseline has 8 failures unrelated to this criterion
