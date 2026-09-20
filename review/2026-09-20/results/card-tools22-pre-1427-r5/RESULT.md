RESULT tools22-pre-1427-r1 sha=5298f6be12ea — a pre-read of mas-bandwidth/nova-tools#1427 at head ab12e1b2058c: pulse hygiene: --lane-dirs, the lane-clone sweep that only takes finished work
PREREAD 1427 claims=11 proven=8 unproven=3 defects=0 high=0

PR 1427, HEAD ab12e1b2058ce5225ff92e59d0cfbf4f3e93d62d, BASE rowan/hygiene-diag-window, MERGE-BASE 896b632304645f15472f8b5180910c454769d2b7, BEHIND 0, FILES 4 production, 1 test, 2 doc, LINES +1143 -2

1. `nova-pulse hygiene --lane-dirs <root>` is a flag-form mode that takes no subcommand — PROVEN-BY cmd/nova-pulse/hygiene_lanes_test.go:404 TestHygieneLaneDirsFlagsRefuseNonsense (subcommand alongside --lane-dirs is refused; bare hygiene still prints usage and exits 2)

2. The verb detects candidates by checking for `.git` in the immediate child (flat) or in exactly one subdirectory within it (nested), and skips non-checkout directories — PROVEN-BY cmd/nova-pulse/hygiene_lanes_test.go:122 TestHygieneLaneDirsRemovesMergedAndClosedClones (flat and nested both go) and cmd/nova-pulse/hygiene_lanes_test.go:482 TestHygieneLaneDirsIgnoresWhatIsNotACheckout (plain directory and loose file are not candidates, not counted, never removed)

3. Four questions in order and the first no keeps the directory: safepath (strictly below root, not a symlink), clean (no git status), pushed (no commits missing from remotes), settled (PR is MERGED or CLOSED) — PROVEN-BY cmd/nova-pulse/hygiene_lanes_test.go:262 TestHygieneLaneDirsRefusesAPathOutsideTheRoot (unsafe), cmd/nova-pulse/hygiene_lanes_test.go:225 TestHygieneLaneDirsKeepsDirtyAndUnpushed (dirty and unpushed), cmd/nova-pulse/hygiene_lanes_test.go:122 TestHygieneLaneDirsRemovesMergedAndClosedClones (settled removes), cmd/nova-pulse/hygiene_lanes_test.go:168 TestHygieneLaneDirsKeepsOpenAndUnknownBranches (open and no-pr keep)

4. A read that failed is a keep; this verb never removes on a guess — UNPROVEN (no test injects a git or forge read failure and asserts the directory survives)

5. The git reads (dirty, unpushed, branch) come before the forge read, so a merged PR cannot talk past an uncommitted file — PROVEN-BY cmd/nova-pulse/hygiene_lanes_test.go:225 TestHygieneLaneDirsKeepsDirtyAndUnpushed (forge never asked when git already refused)

6. `--older-than` has one spelling, a whole number of days with a `d` suffix, and refuses hours, bare numbers, and zero — PROVEN-BY cmd/nova-pulse/hygiene_lanes_test.go:378 TestHygieneLaneDirsFlagsRefuseNonsense (36h, bare 2, 0d, missing, relative root, nonexistent root, subcommand, standalone --older-than)

7. `--dry-run` prints the same `HYGIENE LANE` line with `removed=no` and removes nothing — PROVEN-BY cmd/nova-pulse/hygiene_lanes_test.go:283 TestHygieneLaneDirsDryRunRemovesNothing

8. `--max` caps per-candidate lines at 20 by default; `--max 0` lifts the ceiling and prints all — PROVEN-BY cmd/nova-pulse/hygiene_lanes_test.go:427 TestHygieneLaneDirsOutputIsBounded (30 candidates print 20 item lines and one MORE; --max 0 prints all 30)

9. A `.git` file counts as much as a `.git` directory, so a worktree is a candidate too — UNPROVEN (no test creates a `.git` file)

10. `--older-than` and `--max` outside `--lane-dirs` are refused rather than silently ignored — PROVEN-BY cmd/nova-pulse/hygiene_lanes_test.go:410 TestHygieneLaneDirsFlagsRefuseNonsense (for --older-than), UNPROVEN for --max alone (no test passes --max without --lane-dirs)

11. The KEEP reasons `many-checkouts`, `detached`, `git`, `forge`, and `unknown-state` each keep the directory with their token — UNPROVEN (no test exercises these paths through to the output lines)

DEFECTS none

1. The `many-checkouts` reason (a candidate with >1 checkout child) is documented but untested and unreachable by today's lane workers — was it added for symmetry with the other reasons, or is there an expected scenario where one `lane-*` directory genuinely holds two repos?

2. When `--home` is passed alongside `--lane-dirs`, it is silently ignored (the doc says `--home` means nothing to it). Was this silence intentional for backward compatibility, or would a refusal or warning be preferable?

3. The `--max` flag parsed without `--lane-dirs` is refused by the code but untested. Should a follow-up add the test, or is the existing `--older-than`-without-`--lane-dirs` test sufficient precedent?

Left owed: read the full diff of 1143 lines closely — every production file read in full (cmd/nova-pulse/hygiene.go, main.go, internal/pulse/hygienelanes.go, docs/CLI.md, docs/SPEC-PULSE.md), the full test file (cmd/nova-pulse/hygiene_lanes_test.go), the bounded package contract, and the safepath package in both refs. Nothing was left unread.

nothing printed
d576bf6bbabb39068096a97b4560de9b5e245970===FILE=== card-tools22-pre-1427-r1/usage.tsv
job	attempt	started	ended	rc	provider	model	tokens_in	tokens_out	cache_write	cache_read	reasoning	usd
card-tools22-pre-1427-r1	1	2026-09-20T19:04:25Z	2026-09-20T19:13:31Z	0	openrouter	deepseek/deepseek-v4-flash	202319	8997	0	833280	20280	0.0152
