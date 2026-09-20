RESULT work2-E02-F07-02 sha=a3abdd4ad6dd — nova-work E02-F07 acceptance criterion, criterion E02-F07-02 (docs/roadmaps/nova-work.sexp): Support take, heartbeat, holder-only release, handed release, one extension and explicit escalation with deadline/default fields
DONE
CRITERION E02-F07-02 STATE unmet

BRANCH rowan/work2-E02-F07-02
REPO mas-bandwidth/nova-tools
PATHS lisp/nova-work/tests/replays-8649.lisp

SPEC-WORK line found (the "The lease" contract, docs/SPEC-WORK.md:1014,1356-1391):
  docs/SPEC-WORK.md:1364-1366  `:deadline "..."` `:default :release)  ; :release | :extend-once | (:escalate "<name>")`
  docs/SPEC-WORK.md:1367-1369  the `:heartbeat` event
  docs/SPEC-WORK.md:1376-1381  `:extend-once` (one extension) and `(:escalate "<name>")` (explicit escalation)
  docs/SPEC-WORK.md:1385-1391  holder-only release, `release --handed`, and "A lease with no :deadline or no :default is refused at write time"

Test added: `TestE02F07SupportTakeHeartbeatHolderOnly` in lisp/nova-work/tests/replays-8649.lisp
  (names docs/SPEC-WORK.md:1014,1356-1391 in the comment directly above it).

Suite run (whole, twice, via ./run-tests.sh under lisp/nova-work):
  NOVA-WORK SLICE1 total=408 pass=399 fail=9
  NOVA-WORK SLICE1 total=408 pass=399 fail=9
  (Identical on both runs: no order-dependence observed. The other 8 failures are the
   pre-existing request-line/session tests failing with "Can't create directory
   /tmp/nova-work-reqline-…" — an environment/sandbox issue unrelated to this criterion.)

My test is RED:
  TEST TestE02F07SupportTakeHeartbeatHolderOnly FAIL spec=docs/SPEC-WORK.md:1014,1356-1391
      "the lease log drops the :deadline the take carried: expected "2026-09-14T21:00:00Z" got NIL"

What that means for the roadmap row: E02-F07-02 is NOT verified because it is genuinely
UNMET. The node lease (`src/take-verb.lisp`) implements only `take` and holder-only `release`.
It ignores the `:deadline`/`:default` fields entirely (the lease-log row in `src/state.lisp`
carries only :kind/:node/:by/:holder/:stamp/:rev), and there is no node `heartbeat`, no
`release --handed` handoff, no `:extend-once` extension and no `(:escalate "<name>")`
escalation. SPEC-WORK.md:1391 requires a lease with no :deadline/:default to be refused at
write time, and the present kernel admits it. The test is left RED on purpose.

git status --short:
  M lisp/nova-work/tests/replays-8649.lisp

head: c8e4865182167312bdf801702377fc802055de44

Left owed: implement the node-lease features named by E02-F07-02 — store :deadline/:default
on take, refuse a take with neither (SPEC-WORK.md:1391), add a node :heartbeat event, add
`release --handed` as a :handoff event, and derive :extend-once and (:escalate "<name>")
expiry readings — so this test (or a stronger one) turns green and the roadmap row can be
ticked.
