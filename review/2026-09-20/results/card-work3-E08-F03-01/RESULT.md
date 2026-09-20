RESULT work3-E08-F03-01 sha=f01a0c42d7de34 — nova-work E08-F03 acceptance criterion, criterion E08-F03-01 (docs/roadmaps/nova-work.sexp): Index every known friend, assignments and working task references
DONE
CRITERION E08-F03-01 STATE unmet

BRANCH rowan/work3-E08-F03-01
REPO mas-bandwidth/nova-tools
PATHS lisp/nova-work/tests/replays-8645.lisp

SPEC-WORK line (docs/SPEC-WORK.md:3326-3327):
  A `friends` section names **every known friend, idle, resting and unavailable
  ones included**, with the `working` task references and the pending and
  acknowledged assignments under each.

TEST TestE08F03IndexEveryKnownFriendAssignments (docs/SPEC-WORK.md:3326-3327)

Suite counts (two runs, identical):
  NOVA-WORK SLICE1 total=424 pass=415 fail=9
  NOVA-WORK SLICE1 total=424 pass=415 fail=9

Result: RED. The test records a working friend ("alice", who takes a live lease
on acme/work/f1/t1) beside an idle known friend ("bob", no lease, no offer, no
assignment) and asks whether the resident friends section names both. It does
not: the friend index as it exists today names only live holders of open work,
so "bob" is dropped. Failure text:

  every known friend is indexed, idle bob included; expected (ALICE BOB), the
  friends index named ("alice")

Meaning for the roadmap: the E08-F03-01 row is unverified because the criterion
is genuinely unmet, not because a test is missing. SPEC-WORK.md:3322 promises "a
stable friend identity index and a reverse assignment index ... in the same
resident model, under the one writer and the one journal"; the kernel's resident
model (wstate, src/state.lisp) carries no friends/assignments index at all. The
`:friend` verb (src/new-verbs.lisp) writes a `:friend` event whose apply (src/
state.lisp:596-605) only advances the revision and writes nothing, and the only
friend-scoped summaries that do exist — src/indexes.lisp `state-holder-index`
("each live holder and the open item ids it holds") and the config list
`fleet-friends` — name neither the idle friend nor the assignments. The working
task references (holder -> open ids) and the leasebook's pending-offer
index-friend/index-node are the closest implemented pieces, and they do not cover
"every known friend, idle/resting/unavailable included".

The other 8 failing tests are environmental, not this criterion and not this
change: endpoint-is-local-and-private (socket bind permission denied) and the
session/request-line tests (cannot create /tmp/nova-work-reqline-*).

git status --short:
   M lisp/nova-work/tests/replays-8645.lisp

head 2a3632dd27b4a995ab9944104290f2b6c6e3eca4

Left owed: a resident friends section under the one writer/journal that names
every known friend (idle/resting/unavailable included) with the `working` task
references and the pending and acknowledged assignments under each, and the
stable friend-identity + reverse-assignment indexes SPEC-WORK.md:3322 names.
