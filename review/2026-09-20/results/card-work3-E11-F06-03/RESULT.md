RESULT work3-E11-F06-03 sha=f01a0c42d7de34acd4cea8199e2f35510c50cde5 — nova-work E11-F06 acceptance criterion, criterion E11-F06-03 (docs/roadmaps/nova-work.sexp): finality-rises-with-tier: a child's done is a claim; the parent moves only after its own verification with evidence bound to its own criteria; a worker's success claim alone never moves a node below the seat
DONE
CRITERION E11-F06-03 STATE verified
BRANCH rowan/work3-E11-F06-03
REPO mas-bandwidth/nova-tools
PATHS lisp/nova-work/tests/request-line.lisp

SPEC-WORK line found:
docs/SPEC-WORK.md:4592 — "**Finality rises with the tier and is never final below the seat**: a child's `done` is a claim against the parent's acceptance until the parent verifies it itself, with evidence bound to the criteria and *'not merely a worker's success claim'* ..."
(also recorded in the acceptance-criterion table at docs/SPEC-WORK.md:4653)

Test name: TestE11F06FinalityRisesWithTierA (lisp/nova-work/tests/request-line.lisp)

The kernel already satisfies the criterion: the gate predicate `need-met-p` (src/needs.lisp) reads a done child's evidence only as a claim — "recorded is not verified" — and leaves the dependent's need `need-unverified` until the parent's verification cache actually holds the raw fact the child's criterion names. A child settling `:done` (the worker's success claim) therefore never moves the dependent (a node below the seat) on its own. This behaviour is also already covered in replays-785-gate.lisp (`a-done-need-on-unverified-evidence-admits-nothing`, `a-done-need-is-unverified-without-complete-proof`); the roadmap row marks E11-F06 as `:state "missing"` only because no test had named E11-F06-03 by name.

Suite counts (run 1):
NOVA-WORK SLICE1 total=424 pass=416 fail=8

Suite counts (run 2):
NOVA-WORK SLICE1 total=424 pass=416 fail=8

The two runs agree. TestE11F06FinalityRisesWithTierA is GREEN in both; it is the test this criterion is verified by. The 8 failures are pre-existing and unrelated: the socket-driven request-line tests fail with "Can't create directory /tmp/nova-work-reqline-..." because this sandbox denies read/write on /tmp (`touch /tmp/.wtest` -> Permission denied). They touch the same file but are environment failures, not the criterion and not this card's test.

git status --short (after commit): empty

head 75f8518d763381d7a13ddf592446fde5468b0cc7

Left owed: nothing. One test named for the SPEC-WORK line, committed, suite run whole and twice with identical counts.
