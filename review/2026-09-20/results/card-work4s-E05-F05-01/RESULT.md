RESULT work4s-E05-F05-01 sha=5298f6be12ea — nova-work E05-F05: does the contract say it? criterion E05-F05-01: Use independent oracle/comparator and deliberately broken assertions
DONE
CRITERION E05-F05-01 SPEC PARTIAL
REPO mas-bandwidth/nova-tools
NO-BRANCH
grep -n "independent oracle" docs/SPEC-WORK.md: 0 hits
grep -in "oracle" docs/SPEC-WORK.md: 2 hits
grep -n "comparator" docs/SPEC-WORK.md: 2 hits
grep -n "deliberately" docs/SPEC-WORK.md: 2 hits
CONTRACT lines:
docs/SPEC-WORK.md:7052-7053: **The oracle is independent and the evidence is retained.** Comparison uses **immutable source captures and an independently implemented semantic comparator**, never the production serializer checking itself.
docs/SPEC-WORK.md:7224-7226: and independent-comparator suites, **with mutation tests proving the important assertions fail when preservation is broken** — a test that still passes with its asserted behaviour deliberately broken is not regression evidence.
PARTIAL clause missing from contract: "Use independent oracle/comparator" - the spec only mentions "independent-comparator suites" and "semantically comparator" separately from "independent traversal oracle" with no unified criterion tying oracle/comparator together with deliberately broken assertions.
:by-feature test names for E05-F05: a-broken-assertion-must-fail, roadmap-proof
Test existence: a-broken-assertion-must-fail exists in lisp/nova-work/tests/replays-8641.lisp; roadmap-proof exists in lisp/nova-work/tests/replays-8649.lisp
git status --short: (empty)
Noticed: The spec section at 7223-7226 addresses "deliberately broken" assertions but does not combine it with an "independent oracle/comparator" requirement in a single contractual statement.
