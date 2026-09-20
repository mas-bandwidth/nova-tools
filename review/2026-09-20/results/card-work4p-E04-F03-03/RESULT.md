RESULT work4p-E04-F03-03 sha=5298f6be12ea — nova-work E04-F03: does the green MOVE? criterion E04-F03-03: Report closed-in, settles-in and revives-in for windows
DONE
CRITERION E04-F03-03 VERDICT HOLDS
REPO mas-bandwidth/nova-tools
NO-BRANCH
TEST-FILE tests/replays-8660.lisp
TEST-NAME revive-appends-and-counts-latest
BASELINE NOVA-WORK SLICE1 total=419 pass=411 fail=8
DISABLED src/state.lisp:321 — (decf (wstate-closed state) delta) → (wstate-closed state) ; disabled: (decf (wstate-closed state) delta)
MUTATED-COUNT NOVA-WORK SLICE1 total=419 pass=400 fail=19
RED-LINE TEST revive-appends-and-counts-latest FAIL spec=docs/SPEC-WORK.md:5547 expected=closed-record-kept;revive-row-revived=<rev>-settles=1;third-row-settles=2;earlier-rows-unchanged;window-counts-latest-once: the window ending here has the id in closed=: expected 1 got 0
SECOND-MUTATION none needed — first mutation caught the criterion directly
RESTORED-STATUS (empty)
RESTORED-COUNT NOVA-WORK SLICE1 total=419 pass=411 fail=8
Noticed 8 pre-existing failures (socket permission, temp directory) unrelated to this criterion; all four sexp-named tests passed on baseline.