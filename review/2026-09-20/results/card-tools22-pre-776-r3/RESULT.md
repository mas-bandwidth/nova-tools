RESULT tools22-pre-776-r1 sha=5298f6be12ea — a pre-read of mas-bandwidth/nova-tools#776 at head 748ca7e77ad4: nova-tools nova-swarm verify and batch: a verdict whose run carries a known failure signature (
KIND: transcript-test
DEADLINE: 2100
LEG: go
PATHS: the files the pull request changes, and nothing else
FILES: 0
TEST: none
MODE: read
TURNS: 35
SOURCE: mas-bandwidth/nova-tools#776
BASE: dev@5298f6be12eaa0f7e6622334d2b6a1eb427649e3
ROUTE: jev=flash why=- eligible=rules

THIS CARD IS A READING, NOT A REVIEW. You approve nothing and you hold nothing. You create no
branch, you change no file, you commit nothing, you push nothing. Its entire product is RESULT.md.
**A human reviewer will read what you write and is free to call every line of it wrong.**

PREREAD 776 claims=2 proven=2 unproven=0 defects=0 high=0

PR 776
HEAD 748ca7e77ad4fe3b77bbe4dfeaa2a5d31230a56a
BASE dev
MERGE-BASE 0f0e46c0e26bfbab7fc84c0dfead4f551fb1c503
BEHIND 21
FILES 3 production, 2 test
LINES +258 -2

CLAIMS
1. When a read verdict or BRANCH run carries a known failure signature in harness-output.log (toolchain, packages, fence, or permission errors), the verdict was not earned and should be scored ABSTAIN reason=signature, never done.
   PROVEN-BY internal/swarm/signature_test.go:95 TestBatchScoresSignature — tests that a card with "toolchain not available" in harness-output.log scores ABSTAIN reason=signature and exits 1, never printing the BRANCH disposition.
   PROVEN-BY internal/swarm/signature_test.go:114 TestVerifyScoresSignature — tests that CheckResult returns OK=false with reason=signature when RESULT.md has a BRANCH disposition but harness-output.log has a failure signature.
2. The failure-signature table in the binary code matches the table in docs/SPEC-SWARM.md exactly.
   PROVEN-BY internal/swarm/signature_test.go:16 TestSignatureTableMatchesSpec — reads the spec markdown, parses the failure-signature table, and compares row-by-row against the binary slice.

DEFECTS none

QUESTIONS FOR THE REVIEWER
1. The spec adds `signature` to the ABSTAIN reason list in two places (the RESULT.md format and the CARD format) - is there a parser elsewhere that validates these reasons and needs updating?
2. The failure-signature table includes "auto-rejecting" as a fence class - should a separate code path distinguish this from the existing `fence` abstain reason in the batch scoring?
3. The check for failureSignatureInFile happens in scoreCard before any line counting - are there other callers of scoreCard that expect "done" or "abstain" with different semantics when signature is present?

Left owed
- None. All changed files were read: docs/SPEC-SWARM.md, internal/swarm/batch.go, internal/swarm/contract.go, internal/swarm/signature.go, internal/swarm/signature_test.go.

git status --short
git rev-parse HEAD d576bf6bbabb39068096a97b4560de9b5e245970
