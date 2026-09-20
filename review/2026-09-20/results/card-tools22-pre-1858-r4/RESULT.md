RESULT tools22-pre-1858-r2 sha=5298f6be12ea — a pre-read of mas-bandwidth/nova-tools#1858 at head 40d4f6969e53: docs/CLI.md: nova-merge batch's five hold flags, derived from the source (#1748)
PREREAD 1858 claims=6 proven=6 unproven=0 defects=0 high=0

PR 1858, HEAD 40d4f6969e5398527c4da0501bca8c39c6988b4f, BASE dev, MERGE-BASE 0878987f9d44eb8c7a9ad01a2180b949d46476fe, BEHIND 22, FILES 1 production, 1 test, LINES +122 -1

1. The `nova-merge batch` usage line in `docs/CLI.md` names every flag the batch verb registers in `cmd/nova-merge/batch.go`, and no others. PROVEN-BY `internal/docs/cli_merge_batch_flags_test.go:38` `TestTheCLIReferenceNamesEveryNovaMergeBatchFlag` — reads flags from batch.go via regex and asserts each is in the CLI.md usage line, and the control direction checks the usage line invents no flag.
2. Exactly one of `--reviewers <file>` and `--no-require-holds --reason <text>` is required; neither or both is exit 2. PROVEN-BY-EXISTING `cmd/nova-merge/hold_test.go:773` `TestReviewersXorNoRequireHolds` — asserts batch exits 2 with neither or both.
3. `--reviewers` requires `--lane <dir>` (which may not be the literal `"none"`). PROVEN-BY-EXISTING `cmd/nova-merge/hold_test.go:1057` `TestReviewersWithoutLaneRefuses` and `hold_test.go:1174` `TestReviewersWithLaneNoneRefuses` — assert `--reviewers` without `--lane` exits 2, and `--lane none` with `--reviewers` exits 2.
4. `--no-require-holds` requires `--reason <text>`. PROVEN-BY-EXISTING `cmd/nova-merge/hold_test.go:773` `TestReviewersXorNoRequireHolds` — asserts `--no-require-holds` without `--reason` exits 2.
5. `--untyped-comments=ignore` requires `--reason <text>`. PROVEN-BY-EXISTING `cmd/nova-merge/hold_test.go:593` `TestUntypedCommentsIgnoreIsPerRunPrintedAndCarriesAReason` — asserts `--untyped-comments=ignore` without `--reason` exits 2.
6. The BATCH OK/FAIL output lines carry `holds=`, `dispositions=`, `reviewers=`, and `untyped=` fields. PROVEN-BY-EXISTING `cmd/nova-merge/hold_test.go:499` `TestBatchOKCarriesHoldsDispositionsAndReviewers` and `hold_test.go:593` `TestUntypedCommentsIgnoreIsPerRunPrintedAndCarriesAReason` — assert the new fields appear on the verdict line.

DEFECTS none

1. The BATCH OK/FAIL output template at `docs/CLI.md:1254-1255` was not updated to include the new `holds=`, `dispositions=`, `reviewers=`, `untyped=` fields that the tool now emits. The prose mentions them ("says so on the verdict line") but a reader scanning the template alone would not see them. Was this omission deliberate, and is the template kept minimal on purpose?
2. The test `TestTheCLIReferenceNamesEveryNovaMergeBatchFlag` reads `cmd/nova-merge/batch.go` via a relative path from `internal/docs/`. Does every build or test invocation run from the repo root, or is there a `chdir` or `-tags` mechanism that guarantees this path resolves?
3. The test's regex `f\.fs\.(?:String|Bool|Int|Duration)\("([^"]+)"` would miss a flag registered through `f.fs.Var(...)`. Is there a convention in this project against `Var` registrations, or is the gap accepted as a known limitation?

Left owed — nothing. Both changed files (76 lines of docs, 111 lines of test) were read in full.

git status --short
(no output)
git rev-parse HEAD
40d4f6969e5398527c4da0501bca8c39c6988b4f===FILE=== card-tools22-pre-1858-r2/usage.tsv
job	attempt	started	ended	rc	provider	model	tokens_in	tokens_out	cache_write	cache_read	reasoning	usd
card-tools22-pre-1858-r2	1	2026-09-20T19:23:52Z	2026-09-20T19:40:05Z	0	openrouter	deepseek/deepseek-v4-flash	307919	17702	0	1549312	18081	0.0245
