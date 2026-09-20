RESULT work4m-E02-F06-19 sha=5298f6be12ea — nova-work E02-F06 criterion E02-F06-19 (sexp id E02-F06-03): Provide reproducible build/install and small startup/status/shutdown smoke tests
DONE
CRITERION E02-F06-03 STATE verified

BRANCH (none; no production change needed — criterion already satisfied in the base)
REPO mas-bandwidth/nova-tools
PATHS (none changed)

spec line found and named: docs/SPEC-WORK.md:298-309 (the existing test cites 298-309;
the two load-bearing sentences are 302-303 and 304-309):

  "a session in another language answers `session status` with its own
   `SESSION OK … build=<identity>` field, so every running binary says which
   build it is."                                    — docs/SPEC-WORK.md:302-303

  "A session's identity, bounds, state, journal path, base revision and clip
   cadence are explicit at start and readable at any time (`session status`,
   whose `SESSION OK` carries ...), and it is stopped explicitly"
                                                    — docs/SPEC-WORK.md:304-309

test name: TestE02F06ProvideReproducibleBuildInstallAnd

baseline (whole suite, before any change):
  NOVA-WORK SLICE1 total=419 pass=411 fail=8

RED output (STEP 4): none — the criterion was already green before any production
change, so the "already met" branch of STEP 4 applies and no red/fix/control cycle
was run.

GREEN count line: the named test is green IN the baseline log:
  TEST TestE02F06ProvideReproducibleBuildInstallAnd PASS spec=docs/SPEC-WORK.md:298-309 expected=build-identity-reproducible-and-names-the-pinned-runtime;start-names-owner-and-generation;status-reads-every-bound-and-the-build;stop-fences-and-status-still-answers

NEGATIVE CONTROL: not run (no production change to revert).

what already satisfies the criterion:

  test file: lisp/nova-work/tests/replays-8663.lisp:236-284 (registered in
             nova-work.asd at line 87). NOTE: the card metadata names
             lisp/nova-work/tests/replays-8643.lisp as the test file, but the
             exact named test `TestE02F06ProvideReproducibleBuildInstallAnd`
             already lives in replays-8663.lisp in this base revision and is
             green. Adding it again to replays-8643.lisp would be a duplicate
             deftest of the same name, i.e. an unearned second green.

  production code the test drives (all present, unchanged):
    - session-build-identity   src/transport.lisp:712  (build=<identity>, 302-303)
    - session-status-line      src/transport.lisp:721  (SESSION OK reads every bound, 304-309)
    - session-identity-line    src/transport.lisp:766
    - session-start            src/session.lisp        (explicit startup)
    - session-stop-lifecycle   src/transport.lisp:815  (explicit fenced shutdown)

  The test asserts exactly the spec's stated behaviour: the build identity is
  reproducible across two calls and names the pinned SBCL runtime; session start
  prints SESSION OK naming owner and generation; session status prints a full
  SESSION OK reading state/every/max-bytes/closed-window/build; session stop
  fences the session and a fenced session still answers status.

git status --short: (empty in repo/ — no changes staged)

Noticed:
  - The roadmap record lags the code: docs/roadmaps/nova-work.sexp:646-656 still
    lists E02-F06 as state "missing" with evidence () and ROADMAP.md:339 still
    shows the criterion unchecked, even though the build/install identity and the
    startup/status/shutdown smoke test are present and green in this base. This
    card's named test already exists (in replays-8663.lisp, not the 8643 file the
    card metadata points at), so I did not fabricate a duplicate.
  - The three subfeatures of E02-F06 are cross-referenced but not individually
    labelled in the spec; the "reproducible build/install and smoke tests" reading
    maps to SPEC-WORK.md:298-309, which the existing test already cites.

Left owed: a next card must record E02-F06's verification in the roadmap ledger
(docs/roadmaps/nova-work.sexp and ROADMAP.md) — mark E02-F06-03 verified with the
existing test as evidence — since the code and test prove the criterion but the
roadmap ledger still reports it missing.
