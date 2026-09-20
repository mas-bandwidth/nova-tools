RESULT work4s-E04-F04-03 sha=5298f6be12ea — nova-work E04-F04: does the contract say it? criterion E04-F04-03: Avoid materialized transitive descendant sets and unbounded scans
DONE
CRITERION E04-F04-03 SPEC STATED
REPO mas-bandwidth/nova-tools
NO-BRANCH
grep -n "materialized transitive descendant" repo/docs/SPEC-WORK.md - hit count: 0
grep -in "materialized" repo/docs/SPEC-WORK.md - hit count: 8
grep -n "unbounded" repo/docs/SPEC-WORK.md - hit count: 13
grep -n "descendant" repo/docs/SPEC-WORK.md - hit count: 16
grep -n "E04-F04" repo/docs/roadmaps/nova-work.sexp - hit count: 10
grep -n "E04-F04-03" repo/ROADMAP.md - hit count: 0
grep -n "E04-F04" repo/ROADMAP.md - hit count: 6

docs/SPEC-WORK.md:140: "no transitive descendant sets are materialised; evidence fetching is a separate, bounded pass"
docs/SPEC-WORK.md:5907: "No transitive descendant set is materialised anywhere"
docs/SPEC-WORK.md:2761: "No unbounded scan and no network wait may hold the mutation loop"

Test names from :by-feature for E04-F04:
- indexes-and-counters: present
- reverse-dependency-index-is-bounded: present
- ready-names-the-blocker-and-the-resolver: not found in tests/
- ready-needs-every-dependency-settled: not found in tests/
- materialized-working-set: present
- working-is-a-view: not found in tests/
- history-grows-startup-does-not: present

git status --short: (empty)
Noticed: The roadmap has two clauses in E04-F04-03; the contract covers both parts separately at lines 140/5907 (descendant sets) and 2761 (unbounded scans).
