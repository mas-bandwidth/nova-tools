RESULT work4s-E05-F04-01 sha=5298f6be12ea — nova-work E05-F04: does the contract say it? criterion E05-F04-01: Require prerequisite dependencies and acceptance before green
DONE
CRITERION E05-F04-01 SPEC STATED
REPO mas-bandwidth/nova-tools
NO-BRANCH
grep -n "prerequisite dependencies and acceptance" docs/SPEC-WORK.md | head -20 — 0 hits
grep -in "prerequisite" docs/SPEC-WORK.md | head -20 — 9 hits (grep -ci)
grep -in "acceptance" docs/SPEC-WORK.md | head -20 — 121 hits (grep -ci)
grep -in "green" docs/SPEC-WORK.md | head -20 — 59 hits (grep -ci)
grep -n "gate" docs/SPEC-WORK.md | sed -n '1,40p' — 150 hits (grep -c)
grep -in "before green" docs/SPEC-WORK.md | head — 0 hits
grep -in "acceptance before" docs/SPEC-WORK.md | head — 0 hits
grep -in "not green\|no cell.*green\|never green\|unverified" docs/SPEC-WORK.md | head -15 — 56 hits (grep -c)
grep -rn "parent-green-needs-dependencies" lisp/nova-work/tests/ | head -5 — 2 hits (slice-11-dependencies.lisp)
grep -rn "merged-is-not-distributed" lisp/nova-work/tests/ | head -5 — 3 hits (slice-05-durable-journal.lisp)
docs/SPEC-WORK.md:1952-1953 — "A parent is green only when every required child and every dependency gate is satisfied. *Do not average nested percentages, round 99.9 to green, treat an empty checklist as done, or count one shared leaf repeatedly within a cell* (5653970526)."
docs/SPEC-WORK.md:7802 — "Green requires the full acceptance contract, including prerequisite gates."
docs/SPEC-WORK.md:1336 — "state** — the log is never rewritten by a fetch — **but no count is ever green on unverified"
docs/SPEC-WORK.md:1287-1288 — "a note: or a bare commit:/file: never qualifies anything by itself, and a green aggregate run never qualifies a whole feature: 5649089106, CI activity and status messages are not completion evidence"
:by-feature E05-F04 (docs/roadmaps/nova-work.sexp:95): :verified 2 :total 3 :tests "parent-green-needs-dependencies; merged-is-not-distributed" — parent-green-needs-dependencies in tree (lisp/nova-work/tests/acceptance/slice-11-dependencies.lisp:124,127); merged-is-not-distributed in tree (lisp/nova-work/tests/acceptance/slice-05-durable-journal.lisp:6,1107,1109). No stale evidence.
git status --short — empty
Noticed the roadmap row "Require prerequisite dependencies and acceptance before green" (docs/roadmaps/ROADMAP.md:586) is checked `- [x]` while the feature entry's own `:state "missing"` and `:evidence ()` in docs/roadmaps/nova-work.sexp:837-848 disagree with the :by-feature :verified 2 :total 3 summary at nova-work.sexp:95; and the subfeature's contract anchor is the pair "The validator; A cell is a reference, not another state store", of which line 7802 (in the latter) and 1952-1953 carry the stated requirement, but no single line in "The validator" section repeats it verbatim.