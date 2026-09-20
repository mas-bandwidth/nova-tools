RESULT work2-E01-F04-01 sha=a3abdd4ad6dd — nova-work E01-F04 acceptance criterion, criterion E01-F04-01 (docs/roadmaps/nova-work.sexp): Represent work-set, feature, roadmap, task, lease and event kinds
DONE
CRITERION E01-F04-01 STATE verified
BRANCH rowan/work2-E01-F04-01
REPO mas-bandwidth/nova-tools
PATHS lisp/nova-work/tests/replays-785-gate.lisp
SPEC-WORK line: docs/SPEC-WORK.md:888 — "**Kinds.** Features, tasks, attempts and leaf subtasks are distinct units (5654012267), so they are distinct kinds:"
TEST: TestE01F04RepresentWorkSetFeatureRoadmap (docs/SPEC-WORK.md:888)
SUITE RUN 1: NOVA-WORK SLICE1 total=408 pass=400 fail=8
SUITE RUN 2: NOVA-WORK SLICE1 total=408 pass=400 fail=8
MY TEST: GREEN — `TEST TestE01F04RepresentWorkSetFeatureRoadmap PASS spec=docs/SPEC-WORK.md:888`

The criterion is already satisfied by the kernel. The test asserts the six kinds by
behaviour: work-set/feature/task/roadmap are node `:type` values (via `node-type`, and
`roadmap-create` for the roadmap), a lease names a holder (`take-lease` -> `node-holder`)
and is a `:lease` event kind with its own field list (`kind-fields :lease` => `(:change
:holder)`), and an event carries a `:kind` (the take's history record reads `:kind :lease`).
The roadmap row E01-F04 is wrongly unverified: this criterion is met.

The 8 failures are pre-existing and unrelated to this change — they are the request-line
tests failing with `Can't create directory /tmp/nova-work-reqline-*`, an environment
limitation of the sandbox's /tmp, not an assertion about the kernel. Both full-suite runs
gave identical counts (no order-dependence).

git status --short: (clean) — the only change was lisp/nova-work/tests/replays-785-gate.lisp, committed and clean.
head aa9f15e57ff7bed5e2b30dc5e8037e16f6898113
Left owed: none.
