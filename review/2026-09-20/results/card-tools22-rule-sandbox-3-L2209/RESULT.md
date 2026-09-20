RESULT tools22-rule-sandbox-3-L2209 sha=5298f6be12ea — does the code at this base do what docs/SPEC-SANDBOX.md rule 3 says?
CONFORMS internal/sandbox/landlock_linux.go:20
SPEC docs/SPEC-SANDBOX.md:2209 rule 3
PKG internal/sandbox
ASK The linux sandbox body must drive the three Landlock syscalls (create_ruleset=444, add_rule=445, restrict_self=446) through Go's stdlib `syscall` package on both amd64 and arm64, and the restrict-then-fork body must use `runtime.LockOSThread`, `prctl` (PR_SET_NO_NEW_PRIVS) and the three raw syscalls — achievable with the standard library alone, no `golang.org/x/sys/unix`.
Deciding lines:
- internal/sandbox/landlock_linux.go:13-16 imports only `"syscall"` and `"unsafe"` — no `golang.org/x/sys/unix`.
- internal/sandbox/landlock_linux.go:18-24: `// The three syscalls, x86-64 and arm64 alike ... const ( sysLandlockCreateRuleset = 444; sysLandlockAddRule = 445; sysLandlockRestrictSelf = 446 )`.
- internal/sandbox/landlock_linux.go:249-250: `const prSetNoNewPrivs = 38`; `syscall.RawSyscall(syscall.SYS_PRCTL, prSetNoNewPrivs, 1, 0)`.
- internal/sandbox/landlock_linux.go:253: `syscall.Syscall(sysLandlockRestrictSelf, ...)` after the prctl, in `restrictSelf`.
- internal/sandbox/wrap_linux.go:257: `runtime.LockOSThread()` immediately before `restrictSelf` and `cmd.Start()`.
- Verified `GOOS=linux GOARCH=amd64` and `GOOS=linux GOARCH=arm64` `go build ./internal/sandbox/` both succeed with stdlib only.
UNGUARDED
Greps ran: `grep -rn "x/sys" --include='*.go' --include='go.mod' --include='*.md' .`; `ls internal/sandbox/`; `grep -rn "func Test" --include='*_test.go' internal/sandbox/`; `grep -rn "444\|445\|446\|sysLandlock\|SYS_PRCTL\|PR_SET_NO_NEW_PRIVS\|LockOSThread" --include='*_test.go' .`; `grep -rn "landlock_linux\|wrap_linux\|syscall" --include='*_test.go' internal/ci/ cmd/nova-sandbox/`. No internal/sandbox test pins the syscall numbers, the prctl, or the LockOSThread sequence; the e2e wall tests (cmd/nova-sandbox/wall_linux_test.go) exercise the raw syscalls only on a real Landlock kernel, and no test asserts the stdlib-only import property of the linux body (runwin_test.go does this for the windows body only).
Left owed: none