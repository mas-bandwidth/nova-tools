RESULT work4s-E04-F03-03 sha=5298f6be12ea — nova-work E04-F03: does the contract say it? criterion E04-F03-03: Report closed-in, settles-in and revives-in for windows
DONE
CRITERION E04-F03-03 SPEC STATED
REPO mas-bandwidth/nova-tools
NO-BRANCH
Grep 1: grep -n "closed-in\|settles-in\|revives-in" docs/SPEC-WORK.md | head -20 -> 33 hits
Grep 2: grep -in "settle" docs/SPEC-WORK.md | head -20 -> 194 hits
Grep 3: grep -in "revive" docs/SPEC-WORK.md | head -20 -> 65 hits
Grep 4: grep -in "window" docs/SPEC-WORK.md | head -20 -> 103 hits
Grep 5: grep -n 'closed-in=' docs/SPEC-WORK.md | head -30 -> 14 hits
Grep 6: grep -n "E04-F03" docs/roadmaps/nova-work.sexp | head -10 -> 2 hits
SPEC docs/SPEC-WORK.md:2012 "  read time. `closed-in=<n>`, printed only where a window is given, is the part of `closed=`"
SPEC docs/SPEC-WORK.md:2013 "  whose settle stamp falls inside it — *what did we complete during this interval*. **Three more"
SPEC docs/SPEC-WORK.md:2014 "  are printed with it wherever a window is given, and they count events rather than items**:"
SPEC docs/SPEC-WORK.md:2015 "  `settles-in=<n>` and `revives-in=<n>`, one per `:settle` and one per `:revive` recorded inside"
SPEC docs/SPEC-WORK.md:2016 "  the window, and `items-in=<n>`, the distinct ids those events touched. **The two grains are"
SPEC docs/SPEC-WORK.md:2017 "  never added and never substituted**: `closed-in=` answers *which items are finished as of the"
SPEC docs/SPEC-WORK.md:2018 "  window's end* and `settles-in=` answers *how much finishing happened inside it*, so an item"
SPEC docs/SPEC-WORK.md:2019 "  settled and reopened in one window is `settles-in=1 revives-in=1 items-in=1 closed-in=0`, and"
SPEC docs/SPEC-WORK.md:2067 "`from=`, `to=`, `closed-in=`, `settles-in=`, `revives-in=` and `items-in=` wherever a window was"
SPEC docs/SPEC-WORK.md:5964 "QUERY OK ask=<kind> scope=<rev> membership=<rule> branch=<open|closed|root> unit=<unit> source=<sha|-> freshest=<stamp|-> done=<n> done-unverified=<n> unknown=<n> deferred=<n> cancelled=<n> superseded=<n> stale=<n> required=<n> since-baseline=<n> private=<n> open=<n> closed=<n> gap=<n> [from=<stamp> to=<stamp> closed-in=<n> settles-in=<n> revives-in=<n> items-in=<n>] [green=<k> applicable=<n> baseline-rows=<n0> row-kind=<kind>] [held-not-worked=<n> unowned=<n>] [leases=<n>] [reports=<n> no-verb=<n> launched-unmet=<n>] [responsible=<name|->] pushed=<rev|-> rows=<n> shown=<n> pages=<n> parses=<n> replays=<n> emitted=<bytes>   (the reports= group is SPEC-AHEAD: #854 and is printed by the reports ask alone)"
SPEC docs/SPEC-WORK.md:2161 "QUERY OK ask=under scope=412 membership=category branch=root unit=leaves source=9f2c1a7e freshest=2026-09-13T18:22:41Z done=2 done-unverified=0 unknown=0 deferred=0 cancelled=1 superseded=0 stale=0 required=3 since-baseline=0 private=0 open=1 closed=3 gap=0 from=2026-09-01T00:00:00Z to=2026-09-14T00:00:00Z closed-in=3 settles-in=3 revives-in=0 items-in=3 responsible=rowan pushed=410 rows=0 shown=4 pages=4 parses=0 replays=0 emitted=612"
SPEC docs/SPEC-WORK.md:1621 "revived inside one window is counted once, as open, and `closed-in=` counts only the ids whose"
SPEC docs/SPEC-WORK.md:1797 "inside the window, and an id in both branches is a finding by rule 18 and not an arithmetic to"
TEST by-feature E04-F03 (docs/roadmaps/nova-work.sexp:88): "cow-root-partition; revive-appends-and-counts-latest; closed-rows-carry-revived-and-settles; activity-and-state-are-two-counts"
TEST cow-root-partition -> in tree (lisp/nova-work/tests/acceptance/slice-04-doing-and-journal.lisp:259)
TEST revive-appends-and-counts-latest -> in tree (lisp/nova-work/tests/replays-8660.lisp:24)
TEST closed-rows-carry-revived-and-settles -> in tree (lisp/nova-work/tests/acceptance/slice-01-reader.lisp:456)
TEST activity-and-state-are-two-counts -> in tree (lisp/nova-work/tests/acceptance/slice-04-doing-and-journal.lisp:300)
GIT-STATUS: (empty)
HEAD 5298f6be12eaa0f7e6622334d2b6a1eb427649e3
Noticed The sexp :by-feature entry at line 88 lists 4 tests while :verified 2 :total 3; the sexp :evidence for E04-F03 (line 754) is empty though the ROADMAP.md:484-489 evidence line names the same 4 tests. All 4 named tests exist in the tree; none is stale.