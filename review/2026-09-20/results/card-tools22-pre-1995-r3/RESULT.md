RESULT tools22-pre-1995-r2 sha=5298f6be12ea — a pre-read of mas-bandwidth/nova-tools#1995 at head 8230c5fb6a64: tools: a measured verifier for the ASDF-only approval carry (instrument only, rule not in force
PREREAD 1995 claims=8 proven=2 unproven=6 defects=1 high=0

PR 1995
HEAD 8230c5fb6a6450ff4a6acf7f740683d6f3ec851c
BASE dev
MERGE-BASE a78f3ea93b08a1ac5fd01608ecc61b1040f3a6f2
BEHIND 6
FILES 3 production, 1 test
LINES +738 -0

CLAIMS

1. asdf-carry-verify.sh decides whether an approval at commit A carries to a rebased commit B when the only change between A and B is the component list of lisp/nova-work/nova-work.asd.
PROVEN-BY-EXISTING tools/asdf-carry-verify_test.sh:1 a) happy path case builds four real commits and expects CARRY OK from the real verifier operating on real git objects.

2. Eight structural stages verify: (1) one-to-one commit pairing via range-diff, (2) byte-identical commit messages and identical per-commit path sets, (3) B adds exactly the components A added and removes none of baseB's, (4) shared components keep their relative order, (5) no new duplicate components (tolerating pre-existing ones from nova-tools#1989), (6) every non-.asd blob A touched is byte-identical in B and B touched nothing extra, (7) four full ASDF suites run alone with preserved test-name sets and outcomes plus B being green, (8) ci-ok is green at the exact SHA B.
UNPROVEN — the test suite drives each stage via injected fake-suite/fake-ci outputs and sabotage files; no case exercises individual stages (range-diff pairing, message comparison, component ordering, etc.) in isolation with assertions about their internal decision logic.

3. The output writes a single verdict file containing either `CARRY OK` or `CARRY REFUSED reason=<reasons>` with all measurement results, pinned SHAs, and verbatim range-diff.
PROVEN-BY-EXISTING tools/asdf-carry-verify_test.sh:67-86) cases check both the exit code and the presence of `CARRY OK` / refusal reason patterns in the output string captured from stdout.

4. `asdf-carry-ci-ok.sh` queries GitHub's check-runs API paginated for a specific commit SHA and returns success only when a check named `ci-ok` has status=completed and conclusion=success.
UNPROVEN — the test overrides it entirely via ASDF_CARRY_CI; no test calls the real function.

5. The script refuses rather than silently passing when invoked with --no-suite or --no-ci flags.
PROVEN-BY-EXISTING tools/asdf-carry-verify_test.sh:226-230) explicit expect_refused checks for "suite-not-run" and "ci-not-checked".

6. Pre-existing duplicate components (nova-tools#1989) that are identical in A and baseB are reported but allowed; any *new* duplicate causes refusal.
PROVEN-BY-EXISTING tools/asdf-carry-verify_test.sh:215-223) BASE_DUP=1 triggers duplicate reporting in the output and passes; adding tests/t2 duplicate on top of tolerated ones fails with "new-duplicate-component".

7. This tool creates no vote, releases no other holder, and waives no security or merge-history gate — it is purely an instrument.
UNPROVEN — absence-of side-effects cannot be demonstrated by a test; verified by reading the source (no email, webhook, file-write, or approval-state mutation anywhere outside the temp verdict).

8. The PR contains only these three new files with no changes to existing code.
UNPROVEN — no test asserts "these are the only changed files"; verified manually by diff stat review.

DEFECTS low tools/asdf-carry-verify.sh:89 set -u — unset variables cause immediate exit rather than a diagnostic message; in practice the argument parser requires all four SHAs so this matters only for callers who pass empty strings accidentally, where the failure would be cryptic. What would fix it: remove -u and add explicit emptiness checks with descriptive errors (already done per-variable in lines 91-96 anyway, making -u redundant).

QUESTIONS

1. The comment block says Stella gave a CONDITIONAL YES but does not cite the thread or condition. Where can a reviewer find Stella's actual conditional terms beyond the inline reference `stella-abebd6464110`?

2. Stage 7 runs four ASDF suites sequentially ("this needs a quiet bench"). Is there documented guidance or monitoring on how often two suites sharing a host caused failures during nova-tools#1699, and is that issue still possible today?

3. The grep pattern `(:file "[^"]*")` used by `components_of()` could theoretically match substrings in comments or other (:file ...) constructs outside the :components list. Under what .asd structure could this return false components, and has anyone audited nova-work.asd against this assumption?

4. The test builds synthetic repos under mktemp that differ from real nova-tools topology. Does the test cover a case where baseB was created by a merge commit (not a linear rebase of baseA), i.e., where $BASE_B is not on a straight ancestry line from $BASE_A?

Left owed
I read all three production files in full and the test file in full. No files were skipped.

git status --short
 (prints nothing — clean)
git rev-parse HEAD
d576bf6bbabb39068096a97b4560de9b5e245970
