RESULT work4r3-E02-F06-19 sha=5298f6be12ea — nova-work E02-F06 criterion E02-F06-19 (sexp id E02-F06-03): Provide reproducible build/install and small startup/status/shutdown smoke tests
DONE
CRITERION E02-F06-03 STATE verified

BRANCH rowan/work4r3-E02-F06-19
REPO mas-bandwidth/nova-tools
PATHS lisp/nova-work/tests/replays-8643.lisp

SPEC-WORK.md:299-301 quoted: "packaging and its supported platforms are pinned and tested before any release — and for the first pilot they are pinned here, decided by Glenn, 2026-09-15: the runtime is SBCL and the platforms are the E02-F06 matrix below." The build=<identity> field and the explicit startup/status/shutdown lifecycle are the adjoining :302-310 and :2463.

TestName TestE02F06ProvideReproducibleBuildInstallAnd

BASELINE (STEP 3): NOVA-WORK SLICE1 total=419 pass=411 fail=8

RED (STEP 4): the test was GREEN with no production change — the criterion was ALREADY met. No red existed to show. The two runs below prove it:
  - suite-red.log (my test added, no production change): NOVA-WORK SLICE1 total=420 pass=412 fail=8
  - TEST TestE02F06ProvideReproducibleBuildInstallAnd PASS spec=docs/SPEC-WORK.md:299,302-310

GREEN (STEP 5): skipped — no production change made; the criterion is satisfied by existing code.

EXISTING CODE THAT SATISFIES E02-F06-03:
  - src/transport.lisp:712 session-build-identity — deterministic "SBCL-<version>" build= field.
  - src/transport.lisp:721 session-status-line — SESSION OK reads every bound plus build=.
  - src/transport.lisp:815 session-stop-lifecycle — explicit stop fences and releases owner; fenced session still answers status.
  - A same-named test already exists and passes at tests/replays-8663.lisp (TestE02F06ProvideReproducibleBuildInstallAnd), plus coverage in tests/acceptance/slice-12-session-ownership.lisp (session-status, session-stop, session-server-daemon-and-session-start).

NEGATIVE CONTROL (STEP 6): skipped — criterion already met, no production change to revert. My test cannot go red without also deleting the pre-existing transport.lisp implementation, which is out of scope and forbidden by the constraints.

git status --short:
  M lisp/nova-work/tests/replays-8643.lisp

head 4b8c1a655fd34a5b9a90743b04004f7dd9bd8625

Noticed: docs/roadmaps nova-work ROADMAP.md:339 still marks E02-F06-03 unchecked even though the code (transport.lisp) and a passing test (replays-8663.lisp) already carry it; the ROADMAP bookkeeping is stale, not the criterion. A same-named deftest already lived in tests/replays-8663.lisp, so the suite now runs the name twice (harmless: harness has no name-uniqueness check).

Left owed: a roadmap-editing card to tick ROADMAP.md:339 off the checklist, and a decision on whether the duplicate deftest name (8643 vs 8663) should be merged into one slice.
