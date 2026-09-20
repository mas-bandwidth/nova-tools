RESULT tools22-rule-sandbox-3-L1536 sha=5298f6be12ea
CONFORMS internal/sandbox/wrap_linux.go:328
SPEC docs/SPEC-SANDBOX.md:1536 rule 3
PKG internal/sandbox
ASK For each `--write` path, open it O_PATH|O_CLOEXEC and landlock_add_rule with LANDLOCK_RULE_PATH_BENEATH using the full handled access set (every read+write bit the discovered ABI defines).
CONFORMS at `internal/sandbox/wrap_linux.go:328-332`: the loop calls `addPathRule(rulesetFd, dir, write)` for every write path, where `write = writeSubset(abi) = handledFS(abi)` (the full read+write set). The `addPathRule` function at `internal/sandbox/landlock_linux.go:228` opens O_PATH|O_CLOEXEC and calls landlock_add_rule with LANDLOCK_RULE_PATH_BENEATH — matching "the same" from rule 2. The subset is verified at `landlock_linux.go:141`: `writeSubset(abi) = handledFS(abi)`, the full handled set.
UNGUARDED: no test in `internal/sandbox/` exercises rule 3. The tests in `wrap_linux_test.go` all force `available` to a refusal value and never reach `addRules`. The behavior is covered end-to-end by `cmd/nova-sandbox/wall_linux_test.go:177 TestLandlockWallAllowsReadPaths` (writes to `--write` path and redirects to `/dev/null`), but that is in `cmd/`, not `internal/sandbox/`.
GREPS: `grep -rn "writeSubset\|handledFS\|addPathRule\|--write" --include='*.go' internal/sandbox/`, `grep -rn "func Test" --include='*_test.go' internal/sandbox/`, `ls internal/sandbox/`
Left owed: nothing — rule 3 is implemented correctly and completely.
git status --short
(nothing — the repo is clean)