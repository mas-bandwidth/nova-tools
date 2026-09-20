RESULT tools22-pre-1954-r1 sha=5298f6be12ea — a pre-read of mas-bandwidth/nova-tools#1954 at head 0b49d474509c: nova-pulse fleet standard: a darwin bench is not provisioned until Spotlight is off for its wor
PREREAD 1954 claims=3 proven=1 unproven=2 defects=3 high=1 medium=1 low=1
PR 1954
HEAD 0b49d474509c8ee76597af73ccad928f5051063c
BASE dev
MERGE-BASE 23d9698b0b862641a7569dd268c6f1ed8c4d57b0
BEHIND 9
FILES 148 production, 101 test
LINES +1149 -27135

1. A darwin bench is not provisioned until Spotlight indexing is off for its home work tree and TMPDIR (#1432)
2. The nova-pulse cut command no longer validates that the source locator resolves before writing cards
3. The nova-pulse harvest command no longer accepts validate-contract flag or outputs CUT REFUSED for unresolved locators

PROVEN-BY internal/pulse/fleetstandard_darwin_spotlight_test.go:15 TestTheDarwinStandardChecksSpotlightIsOffForTheWorkTreeAndTMPDIR — asserts spotlight-off check exists for darwin and requires "off" for both HOME and TMPDIR
UNPROVEN: Claim 2 has no test witness in the diff
UNPROVEN: Claim 3 has no test witness in the diff

DEFECT HIGH internal/pulse/fleetstandard.go:127 — Spotlight probe shell command silently treats mdutil errors as "on" — a bench without mdutil always fails provisioning — change error handling to explicitly report unknown
DEFECT MEDIUM docs/SPEC-PULSE.md:484 — The new spotlight-off check is documented in SPEC-PULSE.md but the CLI help in docs/CLI.md does not list it as a recognized check
DEFECT LOW cmd/nova-bus/main.go:77 — Output format changed from "wakes=1 body_bytes=46" to "wakes=1" which breaks tools parsing the bus response

QUESTIONS
1. Why was fillstop_test.go removed alongside the stop file feature in fill.go?
2. Does removing validate-contract from cut affect how cards from non-existent repos are handled?
3. The 100 deleted test files represent substantial coverage loss; what tests replaced them or what guarantees are now assumed?

Left owed
Full read of: docs/SPEC-PULSE.md (2200+ lines, read 100 context lines), internal/pulse/fill.go (read 40 lines), all deleted test files (100 files, read first 10 for context)

git status --short
git rev-parse HEAD 0b49d474509c8ee76597af73ccad928f5051063c===FILE=== card-tools22-pre-1954-r1/usage.tsv
job	attempt	started	ended	rc	provider	model	tokens_in	tokens_out	cache_write	cache_read	reasoning	usd
card-tools22-pre-1954-r1	1	2026-09-20T19:25:57Z	2026-09-20T19:36:33Z	0	inception	mercury-2.5	653749	982	0	107496	7785	0.0279
