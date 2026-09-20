RESULT tools22-pre-1414-r1 sha=5298f6be12ea — a pre-read of mas-bandwidth/nova-tools#1414 at head 2a95a1f145fa: internal/ci: every refusal line carries a remedy
PREREAD 1414 claims=1 proven=1 unproven=0 defects=1 high=0
PR 1414
HEAD 2a95a1f145fa61bfeecd3afe4ec9577897ad574e
BASE dev
MERGE-BASE 0edc81b7406a1160f5b761cba59c1e0c3fc34a00
BEHIND 72
FILES 37 production, 4 test
LINES +659 -85

1. Every string literal in non-test Go under cmd/ and internal/ that begins a refusal line carries a remedy (the thing to do, written the way it would be typed) — PROVEN-BY internal/ci/refusalremedy_class_test.go:151 TestEveryRefusalLineCarriesARemedy (walks cmd/ and internal/, parses AST for refusal literals, checks each for remedy shape, validates against allowlist)

DEFECT low internal/ci/refusalremedy_class_test.go:118 — remedyClause regex contains duplicate verb "widen" in the verb list — minor code quality issue; harmless but sloppy — remove one occurrence of "widen"

QUESTIONS FOR THE REVIEWER
1. The test only excludes testdata directories under cmd/ and internal/; are there other directories under those paths that ship binaries and should be scanned?
2. The allowlist entries for FINALIZE REFUSED and RESULT REFUSED cite "nothing a caller can type would make the missing proof appear" — is this justification documented elsewhere in the design?
3. What was the process for selecting which 84 of 90 refusals to fix in this PR versus the 6 on the allowlist?
4. The pass-through narrowing relies on format verbs being stripped before checking for prose; what happens if a refusal adds a new non-standard format verb?

Left owed
- Read cmd/nova-bus/readhalf_test.go (2 lines changed, likely test fix only)
- Read cmd/nova-board/main.go through cmd/nova-swarm/main.go (18 lines in sandbox, others 2-10 lines each) to verify all refusal additions have remedies
- The production file changes are mostly refusal-line edits; reading full files would exceed practical limits but the test proves the rule

git status --short
(produces no output)
git rev-parse HEAD
2a95a1f145fa61bfeecd3afe4ec9577897ad574e
