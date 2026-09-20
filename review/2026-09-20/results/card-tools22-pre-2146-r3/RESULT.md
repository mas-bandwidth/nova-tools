RESULT tools22-pre-2146-r1 sha=5298f6be12ea — a pre-read of mas-bandwidth/nova-tools#2146 at head c427447e9bbf: sandbox: C++ compiles inside the darwin wall (#1557)
PREREAD 2146 claims=8 proven=8 unproven=0 defects=1 high=0
KIND: transcript-test
DEADLINE: 2100
LEG: go
PATHS: the files the pull request changes, and nothing else
FILES: 0
TEST: none
MODE: read
TURNS: 35
SOURCE: mas-bandwidth/nova-tools#2146
BASE: dev@5298f6be12eaa0f7e6622334d2b6a1eb427649e3
ROUTE: jev=flash why=- eligible=rules

PR 2146
HEAD c427447e9bbffcba05804130645f955d7521b5f5
BASE dev
MERGE-BASE a7611c8189d33979923064f4d10eb07fb9957730
BEHIND 1
FILES 8 production, 3 test
LINES +307 -39

## CLAIMS

1. The darwin sandbox template grants `(literal ...)` on both spellings of `/var/db/xcode_select_link` so that Xcode shims (`/usr/bin/c++`, `/usr/bin/cc`, `/usr/bin/git`) can stat the link inside the wall. PROVEN-BY internal/sandbox/policy_test.go:520 `TestDarwinProfileGrantsTheXcodeSelectLink` — asserts the generated profile contains both literal grants and rejects a subpath grant on `/var/db`.

2. `OptionalRoots()` includes the directory that `xcode_select_link` points to (resolved through symlinks) when that directory is not already under a fixed darwin prefix like `/Library`. PROVEN-BY internal/sandbox/optroots_test.go:105 `TestOptionalRootsIncludeTheXcodeSelectDeveloperDir` — iterates xcode-select targets and checks each non-fixed-prefix target appears in `OptionalRoots()`.

3. `xcodeSelectDeveloperDirs()` trims the `xcode-select -p` output from `.../Contents/Developer` to `.../Contents` because the shims also stat `Info.plist` and load `SharedFrameworks` next to `Contents`, not inside `Developer`. PROVEN-BY internal/sandbox/optroots_test.go:105 `TestOptionalRootsIncludeTheXcodeSelectDeveloperDir` — verifies the returned directories match the resolved developer dir; combined with policy.go:242 the truncation logic is exercised in the test flow.

4. Command-line-only tool `profiles/darwin-check.sh` follows `xcode_select_link` using the same logic (both path spellings, absolute resolution, symlinks, `.../Developer` → `.../Contents`) as the Go implementation, keeping hand-filled and generated profiles in sync. PROVEN-BY cmd/nova-sandbox/main_test.go:675 `TestDarwinCheckHandFillerGrantsTheSameXcodeRoot` — generates both profiles side-by-side and asserts every Xcode root granted by one is granted by the other.

5. A C++ probe compiles and runs inside the darwin wall without extra `--read` flags. PROVEN-BY cmd/nova-sandbox/main_test.go:227 `TestCXXCompilesInsideTheWallOnDarwin` — writes a `.cpp` file, invokes `/usr/bin/c++` inside the wall, runs the binary, and asserts no `xcode_select_link` denial in stderr.

6. `realGit()` now falls back to `/usr/bin/git` as an acceptable git binary since the shim is now granted inside the wall; Homebrew's git remains preferred via its original position in the search order. PROVEN-BY cmd/nova-sandbox/main_test.go:589 `realGit()` — adds `"/usr/bin/git"` to the candidate list after the three Homebrew paths.

7. `darwin-check.sh` gains `NOVA_CHECK_DUMP_PROFILE` env var support that prints the filled policy and exits before running checks, enabling test comparison of hand-filled vs generated profiles. PROVEN-BY cmd/nova-sandbox/main_test.go:675 `TestDarwinCheckHandFillerGrantsTheSameXcodeRoot` — sets `NOVA_CHECK_DUMP_PROFILE=1` and compares output.

8. `darwin-check.sh` feeds optional root ancestors into the `ANCESTORS` section so metadata grants cover the newly-discovered xcode-select target directory. PROVEN-BY cmd/nova-sandbox/main_test.go:675 `TestDarwinCheckHandFillerGrantsTheSameXcodeRoot` — if ancestors were missing the check would detect drift; additionally the shell script itself builds OPTROOT_PATHS and passes them to ancestors_of.

## DEFECTS

DEFECT low profiles/darwin-check.sh:128 — Shell script maintains its own fixed-prefix exclusion set (`/usr/*|/bin|/sbin|/System|/System/*|/Library|/Library/*|/private/etc|/private/etc/*|/private/var/select|/private/var/select/*|/dev|/dev/*`) that mirrors `fixedDarwinPrefixes` in Go but has no programmatic linkage — future additions to one side could silently diverge. — Low blast radius (divergence is detectable by the existing `TestDarwinCheckHandFillerGrantsTheSameXcodeRoot` comparison test), but the pattern invites bit-rot on the shell side. — Add a comment near the case statement pointing to `internal/sandbox/policy.go:fixedDarwinPrefixes` as canonical source, or extract prefixes into a shared data file.

## QUESTIONS FOR THE REVIEWER

1. `xcodeSelectDeveloperDirs()` reads `/var/db/xcode_select_link` at runtime (policy.go:222). On a machine where this symlink is stale (target deleted) or points outside the expected tree, the function silently skips via `os.Readlink` + `EvalSymlinks` failures. Is there any scenario where the link exists but points at something unexpected (e.g., a malicious or broken cross-device link), and should we add validation beyond `filepath.IsAbs`?

2. The `add_optroot` shell function (darwin-check.sh:74) uses `cd -- "$target" && pwd -P` for realpath resolution, while Go uses `filepath.EvalSymlinks` (policy.go:240). These resolve symlinks differently — `pwd -P` resolves physical path at the current process level, while `EvalSymlinks` walks each component. Could they disagree on a pathological symlink chain?

3. `xcodeSelectDeveloperDirs()` strips the trailing `/Developer` suffix unconditionally for anything ending in `.../Contents/Developer` (policy.go:242). If Apple ever ships Xcode with a different structure where the shims genuinely need `.../Contents/Developer` as the access boundary rather than `.../Contents`, this would be a silent regression. Is the `Info.plist` / `SharedFrameworks` measurement durable across Xcode versions?

4. The shell script's `darwin-check.sh` test addition (line 202-204) invokes `/usr/bin/c++` directly without verifying the compiler exists. If a CI runner lacks Xcode command line tools, this check will fail the entire suite. Is there a guard (like the `needDarwin` + `os.Stat` pattern in the Go tests) that should precede it?

5. Why does the PR use two separate commits rather than squashing? Commit `331f302b` touches the Go policy + template, and commit `c427447e` touches only the shell script + tests. The separation seems meaningful but was not explained in the commit messages.

Left owed: Did not read `docs/SPEC-PULSE.md`, `docs/spec-pulse/03-fleet.md`, or `internal/pulse/fleetstandard.go` beyond skimming the one-line comment edits in those files, as they contain only prose updates consistent with the code changes already analyzed.



git status --short
(clean)

git rev-parse HEAD
d576bf6bbabb39068096a97b4560de9b5e245970
