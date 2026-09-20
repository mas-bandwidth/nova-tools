RESULT tools22-pre-2004-r2 sha=5298f6be12ea — a pre-read of mas-bandwidth/nova-tools#2004 at head fc02b1dfaec749603af6ef8a9cd63a24ce2835b2: docs/TESTS.md: nova-sandbox's darwin PROBE/SANDBOX lines name every field the verbs print (#187
PREREAD 2004 claims=2 proven=1 unproven=1 defects=0 high=0
PR 2004
HEAD fc02b1dfaec749603af6ef8a9cd63a24ce2835b2
BASE dev
MERGE-BASE 3666f40487a97447c1632e4bebf2a40585077e4f
BEHIND 5
FILES 1 production, 1 test
LINES +90 -4

1. checkFieldNames regex widens from `[a-z_]+` to `[a-z0-9_-]+` so it matches hyphenated and digit-prefixed field names (`read-noexec=`, `cwdb64=`). PROVEN-BY cmd/nova-sandbox/firstrun_test.go:95 `TestTheProbeAndSandboxTranscriptsNameEveryFieldTheVerbsPrint` calls `checkFieldNames(documented[prefix])` on the SANDBOX OK line from TESTS.md which contains `read-noexec=0`; under the old regex `read-noexec` would be dropped from `documentedFields` and `reflect.DeepEqual` would fire.

2. New test `TestTheProbeAndSandboxTranscriptsNameEveryFieldTheVerbsPrint` pins the field NAMES in docs/TESTS.md's PROBE OK and SANDBOX OK transcript lines against the literal field names present in main.go's format strings. UNPROVEN — the test performs static parse-and-compare on two source files; nobody runs it at runtime, and a reviewer could edit main.go's format strings without updating TESTS.md and the test would still pass (both sides reflect the broken state).

DEFECT none

QUESTIONS
1. Why pin these two transcript lines against main.go format strings instead of running `nova-sandbox probe` and `nova-sandbox` natively on a darwin agent? The commit comment calls the CHECK OK test "portable" while noting PROBE/SANDBOX tests "cannot run portably here" — is there an explicit decision not to add a darwin-only CI step, or was darwin availability simply unknown?
2. The comment in TestTheCheckTranscriptNamesEveryFieldTheVerbPrint (line ~24 of the PR view) cites `cmd/nova-sandbox/main.go:437` as the CHECK OK format-string location, but git blame shows it at line 384 in the current tree. Is that comment outdated or target-specific, and should it be kept as a living reference or removed?
3. Both transcript checks rely on `strings.Index(line, '"'+prefix)` which finds the first opening quote after the prefix. This works today because each format string lives on one line, but the test does not guard against multi-line fmt.Sprintf chains or string concatenation. Should a comment above the extraction loop document that invariant?

Left owed — nothing omitted beyond what reading this diff supplies. The production file set includes only docs/TESTS.md; the test file is the full firstrun_test.go (read in its post-change form). main.go format strings were read via `git show` at the two known line positions (384, 607).

git status --short
fc02b1dfaec749603af6ef8a9cd63a24ce2835b2
