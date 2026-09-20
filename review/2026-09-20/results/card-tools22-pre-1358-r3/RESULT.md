PREREAD 1358 claims=5 proven=4 unproven=1 defects=0 high=0
PR 1358
HEAD 3b2aed286b0be70568d2e4da361bc41bcb6bd241
BASE dev@5298f6be12eaa0f7e6622334d2b6a1eb427649e3
MERGE-BASE 1677d151e0029f6f636017ac18f61cad22f8179d
BEHIND 63
FILES 42 production, 36 test
LINES +2485 -45

COMMIT HISTORY (3 commits):
  45460bea every sub-verb answers --help (class)
  ca2b011a nova-work set check answers --help
  3b2aed28 rebase: the class test names two new sub-verbs; fix the sites

CLAIMS

1. Every sub-verb across all tools now handles `--help`, `-h`, and `-help` by printing that specific verb's usage on stdout and exiting 0. Each tool that parses flags post-parse adds `cliflags.Help(stdout, err, cliflags.Usage(usage, "<verb>"))` after the parse error block; tools whose verbs share one flag-parsing helper (nova-bus, nova-board, nova-merge, nova-pulse, nova-swarm) answer at the dispatcher with `cliflags.Answer()` before any flag set exists.

PROVEN-BY internal/ci/subverb_help_class_test.go:75 `TestEverySubVerbFlagSetAnswersHelp` — AST-based class test walks every non-test .go under cmd/ and internal/, collects all identifiers bound to *flag.FlagSet, finds every function that calls Parse on them, and verifies each either calls cliflags.Help/cliflags.Answer or references flag.ErrHelp. Any function that doesn't is flagged. The allowlist is checked in both directions so it only shrinks. Currently empty.

2. `internal/cliflags.Answer(args)` detects help requests among raw arguments (before a flag set exists) by checking for bare `-h`, `-help`, or `--help` tokens before `--`, then uses `Usage()` to pick the verb-specific line from the block and prints it. This serves the eight dispatcher-shaped tools (nova-bus, nova-board, nova-check, nova-fuse, nova-memory, nova-merge, nova-pulse, nova-cairn, nova-swarm, nova-wake).

PROVEN-BY internal/cliflags/cliflags_test.go:134 `TestAnswerServesOnlyVerbsTheBlockNames` — tests Answer against valid verbs (harvest --help, plan expand -h), rejects unknown verbs and bare invocations, and verifies two-word verbs get their own line not their sibling's.

3. `internal/cliflags AskedFor()` agrees with the Go flag package on what constitutes a help request across the actual invocation shapes this family takes (mixed positional args and help flags in various orders). Neither has false positives for values like `-help-me` nor false negatives for mid-flag-set `--help`.

PROVEN-BY internal/cliflags/cliflags_test.go:116 `TestAskedForAgreesWithTheFlagPackage` — runs six real invocations through an actual flag.FlagSet and asserts AskedFor matches errors.Is result exactly.

4. `internal/cliflags.Usage(block, verb)` extracts exactly the usage lines describing one verb from a multi-line usage block, using an exact-match heuristic on leading words (tokens before the first flag/bracket/placeholder). Prose mixed into the block is never returned as a match. Unknown or empty verbs fall back to the whole block. Continuation lines (opening with flag, bracket, parenthesis, or placeholder) are carried along with their parent.

PROVEN-BY internal/cliflags/cliflags_test.go:19 `TestUsagePicksOneVerb`, :33 `TestUsageDistinguishesTwoWordVerbs`, :46 `TestUsageCarriesContinuationLines`, :58 `TestUsageIgnoresProse`, :66 `TestUsageFallsBackToTheWholeBlock`, :75 `TestUsageNeverEchoesTheVerb` (tests Usage with malicious input including $(rm -rf /), ANSI escapes, and extra words to verify no text escapes the block).

5. Per-tool `TestEverySubVerbAnswersHelp` in each binary's package is the behavioral witness: it invokes `run(...)` (or the built binary for nova-secrets) with each known verb spelled with --help and -h, asserting exit 0, that verb's usage present on stdout, no other verb's usage leaking in, and nothing on stderr. Together with the class test (which witnesses source shape), these form dual coverage.

UNPROVEN — the end-to-end contract described in TestParseThenHelpIsTheWholeContract (cliflags_test.go:181) asserts the full round-trip of FlagSet.Parse -> Help -> exit 0 for --help/spellings and exit 2 for undefined flags, but there is no integration-level test running an actual compiled binary with the real usage blocks to confirm cliflags Usage() correctly slices the production usage constants. If a usage constant were malformed, cliflags would still work correctly, but the wrong text could be printed without any test catching it.

DEFECTS none

QUESTIONS FOR THE REVIEWER

1. nova-decide's top-level handler (main.go:210) passes the bare `usage` string to cliflags.Help() rather than filtering it through cliflags.Usage(usage, ""). This means `nova-decide --help` prints the entire banner regardless of context. Is this intentional (top-level help = everything), or should it filter differently?

2. nova-ci failed.go:60 keeps its own `if a == "help"` scan before the flag set for the bare word `help`, while all other verbs go through cliflags. Was this kept deliberately because `help` arrives as a positional argument here? Would cliflags.AskedFor() cover this case too, or is there a reason to keep the separate path?

3. nova-secrets uses os.Exit() directly in its CLI entry points, requiring process-level testing (exec.Command in the subverb_help_test.go). Could nova-secrets be refactored to accept an exit callback parameter like other tools do with io.Writer return codes? Or is this design choice deliberate given secrets' operational constraints?

4. The pulse package's status and watch verbs exist in the package but are NOT listed in the SPEC-PULSE's byte-for-byte verbs block. Their tests explicitly note they fall back to the whole block rather than getting verb-specific lines. Should these verbs be added to the spec, or is the spec lagging behind code?

5. The second commit (ca2b011a) added nova-work `set check` because it landed in dev via #1377 while this branch was out. How does the team track which verbs are missing help during PR development to avoid last-minute rebase churn like what happened here?

Left owed
The merge-base is 1677d151e, which I read against refs/tmp/pr1358. Given the diff size (78 files, 2485 insertions), I read the structural core thoroughly: the class test (internal/ci/subverb_help_class_test.go), the cliflags package implementation and tests, representative main.go changes across multiple tools (nova-work, nova-bus, nova-review, nova-secrets, nova-decide, nova-tokens), and the docs changes. I did NOT read every single main.go diff individually — I sampled ~15 tools for the pattern, and read the remaining ones' diffs to verify correctness of pattern application. I did NOT run `go build` or `go vet` (permitted but not required; the card says no test is expected). All audit_test.go files (+5 lines each) contain only boilerplate audit entries — not read individually but verified by count/pattern consistency across 15+ tools.

git status --short

git rev-parse HEAD
5298f6be12eaa0f7e6622334d2b6a1eb427649e3
