RESULT tools22-pre-1425-r4 sha=5298f6be12ea — a pre-read of mas-bandwidth/nova-tools#1425 at head 305f2a686bd9: docs: first-run transcripts for twenty verbs, and a test that runs them
PREREAD 1425 claims=5 proven=5 unproven=0 defects=0 high=0
PR 1425
HEAD 305f2a686bd9b4fda528cf80e17e235fd8b9dc7f
BASE dev
MERGE-BASE 31e351956a59a63e3da1b323abecb63da7b92cdd
BEHIND 39
FILES 2 production, 3 test
LINES +573 -45

1. Adds first-run transcript test for nova-check that runs every `$ nova-check` line from docs/TESTS.md and validates each outputs the expected shape
PROVEN-BY cmd/nova-check/firstrun_test.go:192 TestREADMEFirstRunMatchesWhatTheToolPrints

2. Adds first-run transcript test for nova-pulse that runs every `$ nova-pulse` line from docs/TESTS.md and validates each outputs the expected shape
PROVEN-BY cmd/nova-pulse/firstrun_test.go:105 TestTESTSFirstRunMatchesWhatTheToolPrints

3. Adds first-run transcript tests for nova-work that run every `$ nova-work` line from docs/TESTS.md and validate shapes and refusals
PROVEN-BY cmd/nova-work/firstrun_test.go:171 TestTESTSFirstRunMatchesWhatTheToolPrints

4. Adds Fields() function to internal/onboarding/onboarding.go to properly parse shell-like command lines with quoted arguments
PROVEN-BY cmd/nova-check/firstrun_test.go:115 (and similar uses in nova-pulse, nova-work tests)

5. Documents twenty nova-check verbs in docs/TESTS.md (quickstart, links, nocode, attest, corpus, dogfood record/ledger/gate, kernel, floors) with their expected first-run output shapes
PROVEN-BY cmd/nova-check/firstrun_test.go:140 (count assertions)

DEFECTS none

QUESTIONS FOR THE REVIEWER.
1. Why was the nova-pulse firstrun_test.go created as a new file rather than adding to an existing test file?
2. The dogfood receipts are copied to t.TempDir() per test - was this specifically to avoid CI defects like docs/SPEC-CI.md mentions?
3. Are there additional nova verbs (beyond the 20 documented) that still lack transcript tests?
4. What is the expected maintenance process when a verb's output shape changes - does the test automatically catch this?

Left owed - Full reading of testdata fixtures (example-self, example-dogfood, example-pulse) to verify they match documented expectations
git status --short
git rev-parse HEAD
d576bf6bbabb39068096a97b4560de9b5e245970
