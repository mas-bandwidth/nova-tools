RESULT work4s-E04-F04-01 sha=5298f6be12ea — nova-work E04-F04: does the contract say it? criterion E04-F04-01: Build and incrementally update indexes for IDs, containment, dependencies and repositories
DONE
CRITERION E04-F04-01 SPEC STATED
REPO mas-bandwidth/nova-tools
NO-BRANCH
grep -n "incrementally update index" docs/SPEC-WORK.md | head -20 -> 0 hits
grep -in "index" docs/SPEC-WORK.md | head -40 -> 40 hits (214 total)
grep -n "containment\|dependenc\|repositor" docs/SPEC-WORK.md | head -40 -> 40 hits
grep -n "E04\|F04\|indexes\|IDs" docs/SPEC-WORK.md | head -40 -> 40 hits
grep -n "increment" docs/SPEC-WORK.md | head -20 -> 12 hits
grep -n "build.*index\|index.*build\|rebuild\|rebuildable" docs/SPEC-WORK.md | head -20 -> 15 hits
grep -n "reverse depend\|dependency index\|dependenc" docs/SPEC-WORK.md | head -20 -> 14 hits
grep -n "build.*six\|six index\|six.*index" docs/SPEC-WORK.md | head -5 -> 2 hits
grep -n "E04-F04" docs/roadmaps/nova-work.sexp | head -10 -> 5 hits
docs/SPEC-WORK.md:5861-5865 "At load the session builds six indexes — id to node, containment adjacency, reverse dependency, repository, category (5654164074), and reverse roadmap reference: from a node id to every roadmap that has it as a first-axis member, and to every cell whose `:ref` names it — and every read walk goes through the five read-path indexes (all but the reverse roadmap, which serves the roadmap walk) — id to node, containment adjacency, reverse dependency, repository and category."
docs/SPEC-WORK.md:140 "indexes built at load and updated incrementally"
docs/SPEC-WORK.md:2472 "After a mutation, update affected indexes"
docs/SPEC-WORK.md:5893 "A mutation updates only the affected index entries and invalidates only the affected derived values"
The :by-feature test names for E04-F04: indexes-and-counters; reverse-dependency-index-is-bounded; ready-names-the-blocker-and-the-resolver; ready-needs-every-dependency-settled; materialized-working-set; working-is-a-view; history-grows-startup-does-not
  indexes-and-counters -> found in lisp/nova-work/tests/replays-8664.lisp:141
  reverse-dependency-index-is-bounded -> found in lisp/nova-work/tests/acceptance/slice-11-dependencies.lisp:110
  ready-names-the-blocker-and-the-resolver -> found in lisp/nova-work/tests/acceptance/slice-05-durable-journal.lisp:41
  ready-needs-every-dependency-settled -> found in lisp/nova-work/tests/acceptance/slice-11-dependencies.lisp:71
  materialized-working-set -> found in lisp/nova-work/tests/replays-8646.lisp:62
  working-is-a-view -> found in lisp/nova-work/tests/acceptance/slice-02-close-and-counters.lisp:553
  history-grows-startup-does-not -> found in lisp/nova-work/tests/acceptance/slice-04-doing-and-journal.lisp:446
  All 7 test names found in tree.
git status --short -> empty
Noticed: The sexp has two E04-F04 entries — the :by-feature at line 89 (:verified 4 :total 4 with tests) and the features definition at lines 764-777 (:state "missing" :evidence () — stale definition entry, but the :by-feature evidence section at line 89 is the authoritative verification record.