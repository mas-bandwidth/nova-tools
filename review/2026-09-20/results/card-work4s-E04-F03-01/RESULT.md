RESULT work4s-E04-F03-01 sha=5298f6be12ea — nova-work E04-F03: does the contract say it? criterion E04-F03-01: Keep baseline, discovery, deferred, cancelled and superseded distinctions
DONE
CRITERION E04-F03-01 SPEC STATED
REPO mas-bandwidth/nova-tools
NO-BRANCH
grep -n "baseline\|discovery\|deferred\|cancelled\|superseded" docs/SPEC-WORK.md (50+ hits)
grep -in "distinct\|separate\|differentiat\|distinction" docs/SPEC-WORK.md (27 hits, 6-7 relevant)
grep -n "scope event\|scope kind\|scope log\|scope revision\|scope transition" docs/SPEC-WORK.md (24 hits, 8+ relevant)
grep -n "baseline.*discovery.*defer\|scope kind\|keep.*distinction" docs/SPEC-WORK.md (3 hits relevant)
grep -n "event --kind\|kind <baseline\|delta needs no structure" docs/SPEC-WORK.md (3 hits)
docs/SPEC-WORK.md:141 `:deferred`, `:cancelled` and `:superseded` are scope events; they never enter the done count and the baseline denominator stays printed
docs/SPEC-WORK.md:1090-1098 `:baseline`, `:discovery`, `:remove`, `:require`, `:defer`, `:cancel` ... `:supersede` — the scope log: a baseline records the required set ... Each scope kind's own fields
docs/SPEC-WORK.md:1197 `baseline`, `discovery`, `defer`, `cancel`, `reopen`, `supersede` — event --kind carries only the kinds whose delta needs no structure change
docs/SPEC-WORK.md:1958-1962 Current required work keeps `:deferred` leaves ... excludes only `:cancelled` and `:superseded` leaves ... `remaining` prints `deferred=<n>`, `cancelled=<n>` and `superseded=<n>` kept apart
docs/SPEC-WORK.md:2344 nova-work event --kind <baseline|discovery|defer|cancel|reopen|supersede>
docs/SPEC-WORK.md:2929 scope and dependencies | baseline, discovery, dependency add/remove, prioritise, defer, cancel, reopen, supersede
E04-F03 sexp :tests "cow-root-partition; revive-appends-and-counts-latest; closed-rows-carry-revived-and-settles; activity-and-state-are-two-counts"
  cow-root-partition — lisp/nova-work/tests/acceptance/slice-04-doing-and-journal.lisp:259 (IN TREE)
  revive-appends-and-counts-latest — lisp/nova-work/tests/replays-8660.lisp:24 (IN TREE)
  closed-rows-carry-revived-and-settles — lisp/nova-work/tests/acceptance/slice-01-reader.lisp:456 (IN TREE)
  activity-and-state-are-two-counts — lisp/nova-work/tests/acceptance/slice-04-doing-and-journal.lisp:300 (IN TREE)
git status --short (empty)
Noticed ROADMAP.md is at repo root (ROADMAP.md, not docs/ROADMAP.md as the card's (ROADMAP.md:482) reference suggests, but the line matches. No evidence of drift between spec and roadmap.