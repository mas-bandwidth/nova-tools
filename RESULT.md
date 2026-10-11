head: 2fc5d13eab0c1a82f750acd07b1e0689d6fdba05
branch: sprint/fx-empty-fixes-file-after-the-clear.w1.g2.e17
verdict: not-done
gate: -
output: -
report: docs/fixes.sexp and FIXES.md do not exist in this repo; internal/roadmap/ missing; cannot process fixes ledger

## Body

The task requires editing docs/fixes.sexp and regenerating FIXES.md via make roadmap, but neither file exists in the repository. Additionally, internal/roadmap/ and the required tests (TestFixesIsGeneratedFromTheSexp, TestRoadmapIsGeneratedFromTheSexp) are absent. The ledger reconciliation cannot proceed without these files.
