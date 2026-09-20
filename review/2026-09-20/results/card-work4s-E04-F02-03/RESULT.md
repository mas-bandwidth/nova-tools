RESULT work4s-E04-F02-03 sha=5298f6be12ea — nova-work E04-F02: does the contract say it? criterion E04-F02-03: Print completed required leaves as k/n and never average percentages
DONE
CRITERION E04-F02-03 SPEC STATED
REPO mas-bandwidth/nova-tools
NO-BRANCH
grep -n "completed required leaves" docs/SPEC-WORK.md | head -20 = 2 hits (1952, 7801)
grep -in "k/n" docs/SPEC-WORK.md | head -20 = 2 hits (1952, 2103)
grep -in "average percentage" docs/SPEC-WORK.md | head -20 = 0 hits
grep -in "average" docs/SPEC-WORK.md | head -20 = 8 hits
grep -in "percentages" docs/SPEC-WORK.md | head -20 = 4 hits
grep -n "Print completed required leaves as k/n and never average percentages" ROADMAP.md = 1 hit (472)
grep -n "E04-F02" docs/roadmaps/nova-work.sexp | head -10 = 2 hits (87, 743)
grep -rn "percent-axis-on-a-matrix" lisp/nova-work/tests/ | head -5 = 2 hits (acceptance/slice-09-replays-roadmap.lisp:519 comment, :522 deftest)
docs/SPEC-WORK.md:1952: - **Cell progress** = completed required leaves / required leaves, printed as `k/n`, never as a lone percentage. A parent is green only when every required child and every dependency gate is satisfied. *Do not average nested percentages, round 99.9 to green, treat an empty checklist as done, or count one shared leaf repeatedly within a cell* (5653970526).
docs/SPEC-WORK.md:7801: For each cell, show completed required leaves, total required leaves and unknown leaves.
docs/SPEC-WORK.md:7804: It is not the average of cell percentages.
:by-feature E04-F02 at docs/roadmaps/nova-work.sexp:87 -> (:feature "E04-F02" :verified 3 :total 3 :tests "percent-axis-on-a-matrix"); test "percent-axis-on-a-matrix" IS in the tree at lisp/nova-work/tests/acceptance/slice-09-replays-roadmap.lisp:522 (also ROADMAP.md:474 records it as verified-criteria evidence at dev 4c793b55, 336/336); sexp:743 :evidence entry for E04-F02 is empty () but the by-feature :tests field names the in-tree test, so evidence is not stale
git status --short: (empty)
Noticed: both clauses of the roadmap row (print completed required leaves as k/n; never average percentages) are contract-covered and the spec agrees with the row — no drift; the :by-feature entry shows E04-F02 :verified 3 :total 3 consistent with the row's - [x] checked subfeatures