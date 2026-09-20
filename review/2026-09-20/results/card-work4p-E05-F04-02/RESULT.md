"RESULT work4p-E05-F04-02 sha=5298f6be12ea — nova-work E05-F04: does the green MOVE? criterion E05-F04-02: Keep merged fix, verified behavior and published distribution separate
DONE
CRITERION E05-F04-02 VERDICT HOLDS
REPO mas-bandwidth/nova-tools
NO-BRANCH
TEST-FILE tests/acceptance/slice-05-durable-journal.lisp
TEST-NAME merged-is-not-distributed
BASELINE NOVA-WORK SLICE1 total=419 pass=411 fail=8
MUTATION src/node-verbs.lisp:761
  BEFORE: (let* ((settled (remove-if-not (lambda (r) (eq :c (release-task-branch r)))
  AFTER:  (let* ((settled (remove-if-not (lambda (r) t)
MUTATION-COUNT-LINE NOVA-WORK SLICE1 total=419 pass=400 fail=19
RED-OUTPUT TEST merged-is-not-distributed FAIL spec=docs/SPEC-WORK.md:1986-1997,5093 expected=landed-sha-released-dash-while-release-open: released=- while the release task is open: expected "-" got "v0.4.3"
SECOND-MUTATION none needed (first mutation caught immediately)
RESTORED-STATUS (empty)
RESTORED-COUNT-LINE NOVA-WORK SLICE1 total=419 pass=411 fail=8
NOTICE Baseline tree has 8 pre-existing failures (fail=8). Disabled the `(eq :c (release-task-branch r))` filter in `finding-released`, causing the function to return a version for open release tasks. The named test caught it on the first mutation — verdict HOLDS.