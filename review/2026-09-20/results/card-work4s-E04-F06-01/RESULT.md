RESULT work4s-E04-F06-01 sha=5298f6be12ea — nova-work E04-F06: does the contract say it? criterion E04-F06-01: Answer --at <revision> over O by replaying retained events in memory
DONE
CRITERION E04-F06-01 SPEC STATED
REPO mas-bandwidth/nova-tools
NO-BRANCH
grep -n "--at" docs/SPEC-WORK.md | head -20 — 41 hits (full output below as hit list)
grep -in "replay" docs/SPEC-WORK.md | head -20 — 314 hits
grep -n "retained" docs/SPEC-WORK.md | head -20 — 89 hits
grep -n "revision" docs/SPEC-WORK.md | head -20 — 392 hits
docs/SPEC-WORK.md:1837: **`--at <revision>` answers as of a past scope revision by replaying the retained log in memory to it** — no parse and no file read, but it is a replay and it counts in the answer's `replays=` — and a revision before the loaded snapshot's retention boundary is refused at exit 1, `QUERY FAIL … : at=<rev> boundary=<rev> before the retained history`, naming the archive that holds it (5653982211: *views filter by … scope revision*).
docs/SPEC-WORK.md:288: thereafter; `--at` answers from the retained history in memory, by a replay counted in `replays=`.

E04-F06 sexp entry (:by-feature evidence tests):
  as-of-reconstructs-settle-revive-settle — IN TREE (lisp/nova-work/tests/acceptance/slice-04-doing-and-journal.lisp:279)
  cursor-pinned-across-a-new-settle — IN TREE (lisp/nova-work/tests/acceptance/slice-04-doing-and-journal.lisp:331)
  default-window-opens-two-days — IN TREE (lisp/nova-work/tests/acceptance/slice-04-doing-and-journal.lisp:416)
  rule-2-unavailable-is-not-green — IN TREE (lisp/nova-work/tests/replays-8649.lisp:120)
git status --short: (empty)
Noticed: The sexp entry at line 790 lists E04-F06 title "As-of reconstruction over O" with subfeatures including "Answer --at <revision> over O by replaying retained events in memory" (this exact criterion text as first subfeature). The state field is "missing" and evidence is empty in the sexp. Line 1837 of SPEC-WORK.md directly states the criterion verbatim using "answers as of a past scope revision by replaying the retained log in memory to it" — this covers all three elements: (--at <revision>, over O [the scope], by replaying retained events/log in memory). Line 288 corroborates with the secondary phrasing "`--at` answers from the retained history in memory, by a replay". The criterion has no ambiguity or missing clause.
