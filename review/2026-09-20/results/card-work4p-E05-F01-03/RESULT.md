RESULT work4p-E05-F01-03 sha=5298f6be12ea — nova-work E05-F01: does the green MOVE? criterion E05-F01-03: Make correction generation invalidate prior qualification
DONE
CRITERION E05-F01-03 VERDICT HOLDS
REPO mas-bandwidth/nova-tools
NO-BRANCH
test file: lisp/nova-work/tests/replays-785-gate.lisp, test name: a-removed-or-corrected-need-is-unmet (deftest at :415, "corrected-is-unverified" clause at :429-459)
baseline: NOVA-WORK SLICE1 total=419 pass=411 fail=8
disabled: lisp/nova-work/src/needs.lisp:106 in %need-evidence-qualifies-p — the generation guard that makes a `correct` invalidate prior qualification
  before:       ((not (eql generation evidence-generation)) nil)
  after:        (nil nil)
count after mutation: NOVA-WORK SLICE1 total=419 pass=408 fail=11
red output verbatim: TEST a-removed-or-corrected-need-is-unmet FAIL spec=docs/SPEC-WORK.md:4831 expected=removed-is-closed-unaccepted-for-good-and-corrected-is-unverified: a corrected need is unmet
second mutation: none needed — VERDICT HOLDS; two sibling tests on the same behaviour also went red, all naming the generation invalidation directly:
  TEST a-done-need-is-unverified-without-complete-proof FAIL spec=docs/SPEC-WORK.md:4780 expected=every-id-the-standing-done-names-is-bound-and-verified-or-the-need-is-unverified: a corrected node's older evidence proves nothing
  TEST an-attested-only-need-is-met-by-its-attestation-and-by-nothing-less FAIL spec=docs/SPEC-WORK.md:4799 expected=attested-needs-a-cached-result-a-current-generation-and-no-typed-name: an attestation of an older generation meets nothing
restored git status --short: (empty)
restored count line: NOVA-WORK SLICE1 total=419 pass=411 fail=8
Noticed the base tree was already red before any probe: 8 failures at baseline, all environmental in this sandbox — `endpoint-is-local-and-private` (Socket error in "bind": 13 Permission denied) and seven request-line tests (Can't create directory /tmp/nova-work-reqline-*; /tmp is not writable here). None relate to this criterion. Also noticed: my first probe changed the cond clause test to `((nil) nil)` which made Common Lisp evaluate NIL as a function call ("function COMMON-LISP:NIL is undefined"), crashing the whole predicate; I restored and re-probed with `(nil nil)` (a constant false test), which cleanly skips only the generation clause. The verdict rests on the second, clean probe.