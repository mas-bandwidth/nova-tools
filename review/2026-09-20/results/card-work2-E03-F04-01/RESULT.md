RESULT work2-E03-F04-01 sha=a3abdd4ad6dd — nova-work E03-F04 acceptance criterion, criterion E03-F04-01 (docs/roadmaps/nova-work.sexp): Support evidence-guarded state transitions including blocked, done, deferred, cancelled and superseded
DONE
CRITERION E03-F04-01 STATE unmet

BRANCH rowan/work2-E03-F04-01
REPO mas-bandwidth/nova-tools
PATHS lisp/nova-work/tests/replays-8651.lisp

SPEC-WORK line found: docs/SPEC-WORK.md:1219-1242 ("States and transitions, derived"),
the transition table paragraph whose guards the criterion names. Key sentences:
  - :1219 "allowed transitions are a table the validator holds: from `:todo` to `:doing`, `:blocked`,"
  - :1237 "A `:to :done` transition must name evidence events whose criteria cover every `:acceptance` …"
  - :1241-1242 "A transition to `:blocked` without `:blocked-by` is refused."

Test added: TestE03F04SupportEvidenceGuardedStateTransitions (lisp/nova-work/tests/replays-8651.lisp)

Suite count lines (two identical runs):
  NOVA-WORK SLICE1 total=408 pass=399 fail=9
  NOVA-WORK SLICE1 total=408 pass=399 fail=9

Test colour: RED. It fails at the `:blocked` assertion:
  "a transition to :blocked carrying :blocked-by must be supported, got: MUTATION FAIL node=acme/work/f1/t1: unsupported: verb state-to-blocked is not in slice 1"

What that means for the roadmap row: E03-F04-01 is UNMET, not merely unverified. The
kernel (src/kernel.lisp) supports only `state --to doing`, `state --to done`
(evidence-guarded, rule 5) and `event --kind reopen`; `:event-cancel` reaches a terminal
`:cancelled`. It has no `state --to blocked` verb and no `:defer`/`:supersede` scope events,
so the transitions to `:blocked`, `:deferred` and `:superseded` the criterion names are not
supported. The test stays red as the proof; the roadmap row should not be ticked.

Pre-existing suite failures unrelated to this change (8, all environmental/sandbox):
`endpoint-is-local-and-private` (socket bind permission denied) and the seven
request-line/session tests failing on "Can't create directory /tmp/nova-work-reqline-…".
These are not caused by this card's single-file edit.

git status --short (after commit): clean (empty).

head 016f5689e713fcc80e79a65071c413437340bfea

Left owed: none.
