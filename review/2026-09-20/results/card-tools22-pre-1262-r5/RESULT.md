RESULT tools22-pre-1262-r2 sha=5298f6be12ea — a pre-read of mas-bandwidth/nova-tools#1262 at head effb0335ab5b: CARD-9339 nova-tools tests never copy a built executable into a fixture: one helper internal/tete
PREREAD 1262 claims=14 proven=14 unproven=0 defects=0 high=0

PR 1262, HEAD effb0335ab5ba0e01a61ceb910ebdd19cf807fec, BASE dev, MERGE-BASE 4c793b55a30160e5fe1ed45e25928f2c85dcfe5c, BEHIND 38, FILES 8 production, 12 test, LINES +1148 -93

## CLAIMS

1. `internal/testbin.Place` hard-links a built program into a test directory, falling back to a byte copy when a link is impossible — PROVEN-BY internal/testbin/testbin_test.go:37 TestPlaceHardLinksInTheSameDirectory (asserts Place creates a SameFile link, not a copy)
2. `Place` falls back to a byte copy when the hard link fails — PROVEN-BY internal/testbin/testbin_test.go:69 TestPlaceFallsBackToCopyWhenLinkFails (injects a failing link, asserts the fallback produces an executable with different inode)
3. `Place` produces a runnable executable — PROVEN-BY internal/testbin/testbin_test.go:22 TestPlaceRunsThePlacedProgram (execs the placed binary and checks output is "placed")
4. `Place` replaces an existing file at the destination — PROVEN-BY internal/testbin/testbin_test.go:126 TestPlaceReplacesAnExistingFile (writes old file, Places new content, reads back)
5. `PlaceCopy` copies a built binary even where a link would work, for tests whose subject is that the file is NOT this binary — PROVEN-BY cmd/nova-sandbox/main_test.go:779 copyOfThisBinary (uses PlaceCopy to hand the probe a parent that is not this binary); also at 921
6. The `testbins` class test refuses a fixture that reads a binary with os.ReadFile and writes it with execute bit set — PROVEN-BY internal/ci/ci_testbins_test.go:82 TestTestbinsRefusesACopiedBinary (CheckTestbins returns 1 finding on copy.go.txt)
7. The class test tracks taint through package-level maps — PROVEN-BY internal/ci/ci_testbins_test.go:105 TestTestbinsRefusesAMapHeldCopy (1 finding on indirect.go.txt where ReadFile is in a different function from WriteFile)
8. A shell script written 0o755 is allowed — PROVEN-BY internal/ci/ci_testbins_test.go:122 TestTestbinsAllowsAShellScript (0 findings on script.go.txt)
9. `testbin.Place` is the allowed shape — PROVEN-BY internal/ci/ci_testbins_test.go:133 TestTestbinsAllowsThePlacedHelper (0 findings on helper.go.txt)
10. Adding an allowlist entry that names no offender is refused; removing one is allowed — PROVEN-BY internal/ci/ci_testbins_test.go:142 TestTestbinsAllowlistGrowsRefused (empty → pass; add stale entry → 1 stale; remove → pass)
11. The class test output matches the SPEC-CI.md grammar — PROVEN-BY internal/ci/ci_testbins_test.go:183 TestTestbinsOutputMatchesTheSpec (asserts OKLine, FailLine, ExitCode=2, Render format)
12. The verb line is in SPEC-CI.md — PROVEN-BY internal/ci/ci_testbins_test.go:214 TestTestbinsVerbLineMatchesTheSpec (asserts TestbinVerbLine appears in spec doc)
13. A row in the allowlist survives shifted lines (matched by file+kind, not line) — PROVEN-BY internal/ci/ci_testbins_test.go:237 TestTestbinsAllowlistSurvivesShiftedLines (a row with wrong line number still matches; second copy in same file exceeds budget)
14. Existing tests that copied built binaries are converted to use testbin.Place — PROVEN-BY-EXISTING internal/ci/ci_testbins_test.go:226 TestNoCopiedTestBinariesOnTheCIPath (walks the repo; the allowlist is empty and the path is clean)

## DEFECTS

DEFECTS none

## QUESTIONS

1. The `checkTestbinDirs` var includes `"internal"`, which walks `internal/testbin/testbin_test.go`. The test file uses `os.WriteFile(src, raw, 0o755)` in `TestPlaceFallsBackToCopyWhenLinkFails` but `raw` is a literal `[]byte("built\n")`, not from `os.ReadFile`. Was the checker confirmed not to false-positive on testbin's own tests?
2. `internal/update/delivery_test.go` changed `raw` from `os.ReadFile(os.Args[0])` (`[]byte`) to `os.Executable()` (`string`). The variable name `raw` now holds a path string instead of bytes — deliberate brevity or would a rename to `self`/`exe` reduce confusion across the package?
3. The PR is 38 commits behind the pinned base (`5298f6be12ea`). The class test walks `internal/` and `cmd/` and the allowlist is empty — does this run cleanly against current dev, or might there be new copy patterns introduced between the merge base and `dev` that the class test would flag?

## Left owed

Read the full diff, all new files, and all modified files. Left nothing unread.

git status --short
 (nothing to show)
git rev-parse HEAD
5298f6be12eaa0f7e6622334d2b6a1eb427649e3===FILE=== card-tools22-pre-1262-r2/usage.tsv
job	attempt	started	ended	rc	provider	model	tokens_in	tokens_out	cache_write	cache_read	reasoning	usd
card-tools22-pre-1262-r2	1	2026-09-20T20:21:58Z	2026-09-20T20:30:01Z	0	openrouter	deepseek/deepseek-v4-flash	86072	10260	0	1698048	11612	0.0167
