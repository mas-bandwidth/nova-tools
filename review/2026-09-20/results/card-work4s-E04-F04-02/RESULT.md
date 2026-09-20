RESULT work4s-E04-F04-02 sha=5298f6be12ea — nova-work E04-F04: does the contract say it? criterion E04-F04-02: Provide bounded focus, subtree, category, ready and blocker queries
DONE
CRITERION E04-F04-02 SPEC STATED
REPO mas-bandwidth/nova-tools
NO-BRANCH
grep -n "bounded focus" docs/SPEC-WORK.md | head -20: 0
grep -in "subtree" docs/SPEC-WORK.md | head -20: 19
grep -n "blocker" docs/SPEC-WORK.md | head -20: 6
grep -n "ready" docs/SPEC-WORK.md | head -20: 20
grep -n "category" docs/SPEC-WORK.md | head -20: 20
docs/SPEC-WORK.md:2937 | queries and operations | indexed counts, focus and subtree, ready and blockers, friend/model/cost/history views, bounded operation status/wait/cancel |
docs/SPEC-WORK.md:2112 | ready --node X | the work that can actually be started under X, derived from dependencies, agreed scope, acceptance readiness, ownership, availability and resource limits; every row that cannot proceed prints its exact reason and who can resolve it, because waiting is not execution (replay ready-names-the-blocker-and-the-resolver) |
docs/SPEC-WORK.md:2106 | under --repo <owner/name> --category <label> | compact listing of nodes by category with state (5654164074; taxonomy TBD); under --branch root it spans C and O in one answer, one row per id, which is the worked acceptance below |
:by-feature test names for E04-F04: indexes-and-counters, reverse-dependency-index-is-bounded, ready-names-the-blocker-and-the-resolver, ready-needs-every-dependency-settled, materialized-working-set, working-is-a-view, history-grows-startup-does-not
indexes-and-counters: exists (lisp/nova-work/tests/replays-8664.lisp)
reverse-dependency-index-is-bounded: not found by grep
ready-names-the-blocker-and-the-resolver: exists (lisp/nova-work/tests/acceptance/slice-05-durable-journal.lisp)
ready-needs-every-dependency-settled: not found by grep
materialized-working-set: not found by grep
working-is-a-view: not found by grep
history-grows-startup-does-not: not found by grep
git status --short: (empty)
Noticed The contract line 2937 explicitly names focus and subtree, ready and blockers. Category query is documented at line 2106. The roadmap says "bounded focus" but the contract only says "focus" in the table.
