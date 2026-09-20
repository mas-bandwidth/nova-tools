RESULT work2-E07-F03-01 sha=a3abdd4ad6dd — nova-work E07-F03 acceptance criterion, criterion E07-F03-01 (docs/roadmaps/nova-work.sexp): Regenerate ROADMAP from primary O data
DONE
CRITERION E07-F03-01 STATE verified

BRANCH rowan/work2-E07-F03-01
REPO mas-bandwidth/nova-tools
PATHS lisp/nova-work/tests/replays-8680.lisp

SPEC-WORK line found and quoted:
  docs/SPEC-WORK.md:115 — "revision, and none of them is a second store. `ROADMAP.md` is regenerated, never edited; a"
  (the same sentence continues on :116 with "hand-typed percentage is a bug (5653970526). **O is the primary form...**".
  The failures-it-closes table at docs/SPEC-WORK.md:142 states the same contract: "`render --check` fails on drift; the table lives between two markers and O owns it".)

Test added:
  TestE07F03RegenerateROADMAPFromPrimaryO (in lisp/nova-work/tests/replays-8680.lisp), names docs/SPEC-WORK.md:115 directly above it.
  It builds a roadmap over two feature rows, renders, settles one row's task in O, renders again, and asserts the
  regenerated table flips that row from `state=open` to `state=done` while the other row stays open — i.e. the table is
  derived from O's current state, never a stored hand-edited markdown.

Suite counts (run twice, identical):
  run 1: NOVA-WORK SLICE1 total=408 pass=400 fail=8
  run 2: NOVA-WORK SLICE1 total=408 pass=400 fail=8

My test is GREEN in both runs:
  TEST TestE07F03RegenerateROADMAPFromPrimaryO PASS spec=docs/SPEC-WORK.md:115 expected=roadmap-regenerated-from-o;row-open-before-settle;row-done-after-settle

The 8 failures are pre-existing and environment-bound (socket `bind` permission denied and `Can't create directory /tmp/...`
in the socket/request-line tests); none is the roadmap criterion, none touches this file, and the count is stable across both runs.

What this means for the roadmap row: E07-F03-01 is now verified at this revision by a landed test, so the ROADMAP.md row
"Regenerate ROADMAP from primary O data" should be ticked green (it is one of the 76 currently unverified criteria, and
this card pins it with a passing test).

git status --short (after commit): (empty — the single permitted path was committed)
head 6151965b1139a5a1454c9e84959a5499b292f920

Left owed: none. One file touched, none other.
