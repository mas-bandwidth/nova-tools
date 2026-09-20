RESULT work4-E10-F03-61 sha=5298f6be12ea — nova-work E10-F03 criterion E10-F03-61 (sexp id E10-F03-04): Keep per-change CI within two minutes, exhaustive fault/scale suites explicit nightly or pre-release; failures block affected gates
DONE
CRITERION E10-F03-04 STATE verified
BRANCH rowan/work4-E10-F03-61
REPO mas-bandwidth/nova-tools
PATHS lisp/nova-work/src/package.lisp lisp/nova-work/src/verifier.lisp lisp/nova-work/tests/replays-8662.lisp
SPEC docs/SPEC-WORK.md:7235 "**The lanes are named because a slow gate is a gate nobody runs.** A per-change check **targets one minute and must finish inside two**; the exhaustive fault, scale and generated matrices run in an explicit pre-release, manual or nightly lane and **not on every change**. **The full preservation and recovery gates still pass at the release revision** ... **Before the lock gate each suite is mapped to its named scenarios, assertions, owner, command and CI lane**" (the lane table is SPEC-WORK.md:7089-7100: per-change = format-determinism, referential-integrity, retry-protocol, read-only-intake, undo-redo, roadmap-proof; nightly/pre-release = source-inventory ... hostile-data)
TEST TestE10F03KeepPerChangeCIWithin
BASELINE NOVA-WORK SLICE1 total=419 pass=410 fail=9
RED TEST TestE10F03KeepPerChangeCIWithin FAIL spec=docs/SPEC-WORK.md:7235 expected=per-change-targets-one-minute-finishes-in-two;exhaustive-fault-scale-suites-explicit-nightly-or-pre-release;failures-block-only-their-own-lane-gate: The variable NOVA-WORK/TESTS::*PER-CHANGE-TARGET-SECONDS* is unbound.
GREEN NOVA-WORK SLICE1 total=420 pass=411 fail=9
GREEN NOVA-WORK SLICE1 total=420 pass=411 fail=9
NEGATIVE-CONTROL NOVA-WORK SLICE1 total=420 pass=410 fail=10
NEGATIVE-CONTROL-FAIL TestE10F03KeepPerChangeCIWithin FAIL: The variable NOVA-WORK/TESTS::*PER-CHANGE-TARGET-SECONDS* is unbound.
GIT-STATUS  M lisp/nova-work/src/package.lisp
 M lisp/nova-work/src/verifier.lisp
 M lisp/nova-work/tests/replays-8662.lisp
head bfd624dc7019d979ff1156ef374274d5961aec1b
Noticed The pinned base does not run green in this sandbox: 9 failures (fail=9) are pre-existing and environmental, not logic — `endpoint-is-local-and-private` and `session-server-daemon-and-session-start` fail on `Socket error in "bind": EPERM`, and seven session/request-line cases fail on `Can't create directory /tmp` (the tests hardcode `#p"/tmp/"` and bind AF_UNIX sockets, both blocked here). They live in request-line.lisp and acceptance/slice-05, files this card may not edit, and they are clean on hulk. My change adds one test and introduces zero new failures. Also noticed: E10-F03 subfeature 3 ("Map each suite to an owner, command and CI lane without duplicating its acceptance evidence", still unchecked) is the complementary criterion this one's lane table also serves, and the authoritative two-minute/nightly enforcement already lives in the Go CI layer (internal/ci/ci_budget_test.go defaultCLCeiling, internal/ci/nightlytags_class_test.go, internal/ci/slowtests).
Left owed Wire these lanes to the actual pipeline: run the per-change subset in the two-minute path and schedule the exhaustive fault/scale matrices as a nightly/pres-release lane (Go side, internal/ci / cmd/nova-ci), and complete the per-suite owner+command mapping (E10-F03-03).
