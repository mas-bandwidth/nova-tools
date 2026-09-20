RESULT tools22-rule-sandbox-28 sha=5298f6be12ea — does the code at this base do what docs/SPEC-SANDBOX.md rule 28 says?
ABSENT
SPEC docs/SPEC-SANDBOX.md:2542 rule 28
PKG internal/sandbox
ASK The sandbox package must, as part of running a wrapped command, commit and push the line's self to its git remote when the process exits — on exit 0, exit 7, and SIGKILL — printing one line naming the remote on failure and returning non-zero; on success the remote's bare repo tip matches the working tree, and after an external rm -rf a fresh clone reproduces the pushed contents byte-for-byte.

No code in internal/sandbox implements this. Rule 28 describes behaviour that belongs to **the launcher**, which runs **outside** the wall. The spec states explicitly at lines 1937–1947: "And the launcher pushes the line's self on exit. This is #69's second guard and it is the caller's rule, not the wall's: the launcher, which runs outside the wall, commits and pushes the line's home to its own remote when the wrapped process ends." The internal/sandbox package builds walls (Landlock on Linux, sandbox-exec on Darwin), egress rules, and policy enforcement — none of these concern git operations or launching.

Files examined:
internal/sandbox/containment_test.go
internal/sandbox/egress.go
internal/sandbox/egress_test.go
internal/sandbox/gpu.go
internal/sandbox/gpu_test.go
internal/sandbox/landlock_linux.go
internal/sandbox/optroots_test.go
internal/sandbox/pathcase.go
internal/sandbox/policy.go
internal/sandbox/policy_test.go
internal/sandbox/profile.go
internal/sandbox/status_unix.go
internal/sandbox/systemreads_test.go
internal/sandbox/winpath_test.go
internal/sandbox/winpath_windows_test.go
internal/sandbox/wrap_darwin.go
internal/sandbox/wrap_darwin_test.go
internal/sandbox/wrap_linux.go
internal/sandbox/wrap_linux_test.go
internal/sandbox/wrap_other.go

Greps run:
grep -rn "solo" --include='*.go' . → no matches in internal/sandbox/
grep -rn "push\|commit\|clone\|bare\|launcher" --include='*.go' internal/sandbox/ → only generic uses ("bare selector drop", "sign a push" as comment, "git init" as error message context)
grep -rn "push-on-exit\|exits 7\|SIGKILL.*push\|rm -rf.*remote" --include='*.go' internal/sandbox/ → no matches
grep -rn "func Test" --include='*_test.go' internal/sandbox/ → 64 tests, none relate to git push/commit/clone
grep -n "token\|GH_TOKEN\|HOME.*list" --include='*.go' internal/sandbox/policy.go → line 729 mentions token context for agent var scrubbing only

Left owed

git status --short
(no output)
