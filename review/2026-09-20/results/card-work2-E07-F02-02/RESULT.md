RESULT work2-E07-F02-02 sha=a3abdd4ad6dd — nova-work E07-F02 acceptance criterion, criterion E07-F02-02 (docs/roadmaps/nova-work.sexp): Keep partial, missing and unknown state in S/check counts
DONE
CRITERION E07-F02-02 STATE verified

BRANCH rowan/work2-E07-F02-02
REPO mas-bandwidth/nova-tools
PATHS lisp/nova-work/tests/replays-8664.lisp

SPEC-WORK line found and quoted:
  docs/SPEC-WORK.md:7740 — "cell, a cross for any other state. Partial, missing and unknown stay in S and in `check`'s"
  (the full paragraph is :7738-7742; "counts;" is the continuation on :7741)

Test added:
  TestE07F02KeepPartialMissingAndUnknown  (docs/SPEC-WORK.md:7740)

What the test asserts (behaviour, not implementation): a roadmap with three
feature rows in three different states — one fully verified (tick), one partial
(one of two required leaves settled), one unknown — runs its `check`-counts rollup
(`query --ask percent`) and asserts the completion-only view does not lose the
distinction: `green=1` counts only the fully verified row, the partial cell keeps
its `k/n=1/2`, the unknown cell keeps `unknown=1`, `done-unverified` (the
missing-evidence count) is printed as its own field and never folded into green,
and the denominator (`rows=3`, `applicable=3`) is unchanged by completion. It also
asserts the states stay distinct in S: the verified feature is in C, the partial
feature remains in O, the unknown leaf keeps `:unknown`.

Suite runs (twice, identical):
  NOVA-WORK SLICE1 total=408 pass=400 fail=8
  NOVA-WORK SLICE1 total=408 pass=400 fail=8

My test is GREEN (`TEST TestE07F02KeepPartialMissingAndUnknown PASS`). The kernel
already satisfies E07-F02-02 in this slice: partial, missing and unknown stay
distinct in S and in the roadmap `check`-counts rollup, and the completion-only
view changes no denominator. The roadmap row for E07-F02-02 is currently unticked;
this test names its SPEC-WORK line and pins it so the row can be verified.

The 8 failures are pre-existing and unrelated: all `request-line` tests that create
a directory under `/tmp`, which this sandbox forbids (`Permission denied` on
`/tmp/nova-work-reqline-*`). None touch `replays-8664.lisp` or this criterion.

git status --short (after commit): (empty — clean)

head 96d9930269bda898ff92792378af95a451402147

Left owed: nothing. One file changed, one test added, committed.
