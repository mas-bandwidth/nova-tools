RESULT work2-E07-F06-03 sha=a3abdd4ad6dd — nova-work E07-F06 acceptance criterion, criterion E07-F06-03 (docs/roadmaps/nova-work.sexp): Preserve private state in O and validation while applying filtering only at projection boundaries
DONE
CRITERION E07-F06-03 STATE verified
BRANCH rowan/work2-E07-F06-03
REPO mas-bandwidth/nova-tools
PATHS lisp/nova-work/tests/replays-fleet-stale-tokens.lisp
SPEC-LINE docs/SPEC-WORK.md:947-949 (privacy floor :3027-3028): "A node may carry `:private true`; `render` never writes a private node or its descendants into an output file, and a public view that reaches one through a parent prints `private=<n>` and nothing of it (5653982211)."
TEST TestE07F06PreservePrivateStateInO
SUITE-COUNT-1 NOVA-WORK SLICE1 total=408 pass=400 fail=8
SUITE-COUNT-2 NOVA-WORK SLICE1 total=408 pass=400 fail=8
TEST-STATUS green. My test passes both runs, so the roadmap row for E07-F06-03 is verified and can be ticked (the row's subfeature is genuinely met by the kernel: a private node keeps its full state in O; the render filters only at the projection boundary; validation still walks O and sees the private node).
NOTE the 8 failures are pre-existing and environmental, unrelated to this card: this sandbox's /tmp is not writable by uid 1000, so the request-line socket tests (`Can't create directory /tmp/nova-work-reqline-*`) and `endpoint-is-local-and-private` (`Socket error in "bind": 13 (Permission denied)`) fail in both runs. Identical counts on two runs; no order-dependence observed.
GIT-STATUS  M lisp/nova-work/tests/replays-fleet-stale-tokens.lisp
HEAD b697cd72d9d26e9b59abef0c13795ff1ca220cc6
LEFT-OWED none
