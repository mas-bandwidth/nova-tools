RESULT tools22-pre-1570-r1 sha=5298f6be12ea — a pre-read of mas-bandwidth/nova-tools#1570 at head 861c57f675cf
PREREAD 1570 claims=7 proven=5 unproven=2 defects=0 high=0

PR 1570
HEAD 861c57f675cfcc19feed19af684ef55ee62939c2
BASE dev@5298f6be12eaa0f7e6622334d2b6a1eb427649e3
MERGE-BASE 440b92d1a6d0abc7e784efaf67c0edc1056bf45d
BEHIND 52
FILES 1 production, 0 test
LINES +61 -0

--- CLAIMS ---

1. A line as written in a transcript block represents standard output; a line whose first two characters are `! ` represents standard error, and nothing else about a line indicates its stream.
PROVEN-BY-EXISTING internal/docs/tests_stream_rule_test.go:21 TestTheTranscriptDocumentSaysWhichStreamALineIsOn — the test checks the document header contains exactly these phrases: "standard output", "standard error", "`! `", and "compared apart". This commit introduces all four into the header, making the test pass.

2. Standard output is compared whole (every unmarked line must appear in order with nothing extra); standard error is compared only for the lines shown (extra progress lines are acceptable).
PROVEN-BY-EXISTING internal/onboarding/transcript_test.go:501 TestAMarkedBlockComparesEachStreamOnItsOwnTerms — creates a mixed-stream Result with extra stderr content (not in Want) and asserts Compare produces zero problems, then adds an unshown stdout line and asserts exactly one problem. Also tested at line 528-533 for missing/wrongly-ordered stderr lines.

3. The grading harness should compare standard output against unmarked lines and standard error against `!` lines, recording which stream each expectation was on.
UNPROVEN — no test validates how a grading harness implements the two-stream comparison; the claim describes harness implementation guidance that is not exercised by any existing test.

4. Until a section carries the new marker convention, an unmarked line should be read as "not yet checked" rather than "checked and found to be standard output".
UNPROVEN — this is a reading instruction for humans, not executable behaviour or a machine-checkable invariant. No test witnesses this semantic rule.

5. Preconditions are stated in prose lines beginning with a keyword within a `## nova-<tool>` section, specifically `Platform:` (the recorded platform) and `Requires:` (what a step needs that may not be present). Inline syntax `# Platform:` and `# Requires:` on `$` command lines is parsed by the transcript harness.
PROVEN-BY-EXISTING internal/ci/firstrun_platform_test.go:33 TestFirstRunTranscriptsNameTheirPlatform + internal/onboarding/transcript_test.go:398 TestStepsReadsAPreconditionStatedOnTheCommandLine — the CI test verifies sections with platform-specific tokens carry a `Platform:` line; the transcript test verifies both `# Platform:` and `# Requires:` parsing, including combined forms like `# Platform: darwin, linux; Requires: age-keygen`.

6. Every skipped step must name a precondition stated in this file; a skip for an unstated reason is a defect in this file, not a pass.
PROVEN-BY-EXISTING internal/onboarding/transcript_test.go:456 TestSkipReasonAnswersOnlyWhatTheDocumentStated — creates a step with no preconditions, calls SkipReason("plan9", nil), and asserts it returns empty (no spurious skip). Comment at line 456 quotes #1570's exact wording: "a step skipped for a reason the document does not state is a defect in the document, not a pass. An undeclared step runs."

7. Four `Requires:` lines are owed (one per section) from the dogfood run: `nova-decide` wants `JEV_API_KEY`, `nova-merge init` wants a forge credential, `nova-post send` wants a posting credential, and `nova-secrets` wants `nova-check` binary.
UNPROVEN — these are factual observations about the current state of TESTS.md. No test verifies this inventory; it could become stale or inaccurate without witnessing.

--- DEFECTS ---

DEFECTS none

--- QUESTIONS FOR THE REVIEWER ---

1. The merge base is 440b92d1 (not 5298f6be). It sits 52 commits behind dev across integration-16al through integration-16as batches. Was this PR deliberately cut late relative to dev, or did it get left behind during the hulk-gated landing cadence?

2. The transcript harness at the merge-base already contains extensive two-stream tests (`transcript_test.go:501 TestAMarkedBlockComparesEachStreamOnItsOwnTerms`, `transcript_test.go:563 TestStderrWholeMakesADroppedFindingRed`) and precondition skip tests (`transcript_test.go:456` referencing #1570 verbatim). These were NOT changed by this commit — they pre-existed. They appear to be infrastructure tests for #1549 that assert the exact conventions this PR documents in prose. Is that the right reading, or did #1570 author these as part of the same change?

3. The PR states sections are being "applied section by section under nova-tools#1549." At least four tools already have `! ` markers in their existing blocks (nova-self-talk lines 288, 294; nova-merge lines 488, 490; nova-bus line 97). Which sections still need conversion, and does the `DRAFT NOTE …` mismark note under `## nova-bus` still hold?

4. The prose introduces two document-level keywords (`Platform:` and `Requires:`) that sit alongside the inline syntax (`# Platform:` and `# Requires:` on `$` command lines). Are these meant to serve different audiences (reader-facing prose vs. test-facing pragmas), coexist indefinitely, or will one be deprecated?

--- Left owed ---

I read the full diff (docs/TESTS.md, +61 lines) in its entirety. I read internal/docs/tests_stream_rule_test.go, internal/ci/firstrun_platform_test.go, and internal/onboarding/transcript.go for context on the testing infrastructure. I did not read every cmd/*/firstrun_test.go file exhaustively — I reviewed their role summaries from the exploration agent rather than opening each one individually. The structural tests (onboarding_test.go, tests_stream_rule_test.go, firstrun_platform_test.go) were read in full.

git status --short
(prints nothing)

git rev-parse HEAD
5298f6be12eaa0f7e6622334d2b6a1eb427649e3
