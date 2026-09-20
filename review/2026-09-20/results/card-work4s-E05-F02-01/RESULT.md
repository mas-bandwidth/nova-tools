RESULT work4s-E05-F02-01 sha=5298f6be12ea — nova-work E05-F02: does the contract say it? criterion E05-F02-01: Verify every evidence event, including those not named by done
DONE
CRITERION E05-F02-01 SPEC PARTIAL

REPO mas-bandwidth/nova-tools
NO-BRANCH

grep -n "evidence event" docs/SPEC-WORK.md | head -20: 20 hits
grep -in "evidence" docs/SPEC-WORK.md | head -20: 20 hits
grep -n "Verify" docs/SPEC-WORK.md | head -20: 1 hit
grep -n "every evidence event" docs/SPEC-WORK.md: 3 hits (lines 1238, 4781, 6250)
grep -n "not named by done" docs/SPEC-WORK.md: 0 hits
grep -n "named by done" docs/SPEC-WORK.md: 0 hits

docs/SPEC-WORK.md:6172: "a `verify` over an evidence event no `:to :done` names**"

The contract covers "verify over evidence events not named by done" at line 6172, but does not state the full criterion. The clause "including those not named by done" has partial contract coverage (the spec says `verify` prints one VERIFY ROW per evidence event regardless of :to:done coverage), but the explicit requirement "Verify every evidence event" as a primary obligation is not stated as a standalone contract line.

:evidence-test names for E05-F02:
- a-done-need-is-unverified-without-complete-proof: EXISTS (lisp/nova-work/tests/replays-785-gate.lisp:510)
- verify-stale-evidence-needs-no-fetch: EXISTS (lisp/nova-work/tests/acceptance/slice-13-verifier.lisp:269)
- stale-evidence-does-not-unmeet-a-need: EXISTS (lisp/nova-work/tests/replays-785-gate.lisp:287)
- a-done-need-on-unverified-evidence-admits-nothing: EXISTS (lisp/nova-work/tests/replays-785-gate.lisp:248)
- an-attested-only-need-is-met-by-its-attestation-and-by-nothing-less: EXISTS (lisp/nova-work/tests/replays-785-gate.lisp:326)
- merged-is-not-distributed: EXISTS (lisp/nova-work/src/replays-slice-05.lisp:21)

git status --short: (empty)

Noticed: The contract's line 553 states "verify prints one VERIFY ROW per evidence event" and line 6172 confirms this applies even to events "no :to:done names". But the roadmap's phrasing "Verify every evidence event, including those not named by done" is not explicitly stated as a requirement—it's implied by the verify semantics rather than stated as a contract obligation.
