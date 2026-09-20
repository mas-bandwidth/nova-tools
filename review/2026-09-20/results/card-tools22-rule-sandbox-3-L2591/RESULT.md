RESULT tools22-rule-sandbox-3-L2591 sha=5298f6be12ea — does the code at this base do what docs/SPEC-SANDBOX.md rule 3 says?
CONFORMS internal/sandbox/wrap_linux.go:258
SPEC docs/SPEC-SANDBOX.md:2591 rule 3
PKG internal/sandbox
ASK The linux body must discover the kernel's Landlock ABI, build a ruleset whose handled-access mask (and ABI 6 scopes) cover exactly what that ABI defines, add every path rule through an O_PATH descriptor, then — after LockOSThread, PR_SET_NO_NEW_PRIVS and landlock_restrict_self — fork the command and wait while forwarding SIGINT and SIGTERM to the child, clamping an ABI above the table with `used=<n>` and refusing `net_unenforceable` and `landlock_abi_unknown`.

Deciding lines (each item of the rule, code that implements it):
- ABI discovery: `internal/sandbox/landlock_linux.go:201` `func landlockABI() (int, bool) {` via seam `internal/sandbox/wrap_linux.go:104` `var available = landlockABI`.
- Handled-access mask per ABI: `internal/sandbox/landlock_linux.go:125` `func handledFS(abi int) uint64 {` (masked down, never up), `netHandled` :151, `scopedFor` :159.
- O_PATH fds per rule: `internal/sandbox/landlock_linux.go:229` `const oPath = 0x200000 // O_PATH: not in syscall on every GOARCH`; :230 `fd, err := syscall.Open(path, oPath|syscall.O_CLOEXEC, 0)`.
- LockOSThread: `internal/sandbox/wrap_linux.go:257` `runtime.LockOSThread()`.
- PR_SET_NO_NEW_PRIVS + landlock_restrict_self: `internal/sandbox/landlock_linux.go:250` `syscall.RawSyscall(syscall.SYS_PRCTL, prSetNoNewPrivs, 1, 0)` and :253 `syscall.Syscall(sysLandlockRestrictSelf, ...)`; called from `internal/sandbox/wrap_linux.go:258` `if err := restrictSelf(rulesetFd); err != nil {`.
- Fork-and-wait, SIGINT/SIGTERM forwarded to the child: `internal/sandbox/wrap_linux.go:266` `signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)` and :275 `_ = cmd.Process.Signal(sig)`; then `cmd.Wait()` at :282.
- ABI 6 scopes: `internal/sandbox/landlock_linux.go:66` `scopeAbstractUnixSocket = 1 << 0` / `scopeSignal = 1 << 1`; applied in `rulesetAttr.Scoped` via `scopedFor` :159.
- Clamp above the table, `used=<n>`: `internal/sandbox/landlock_linux.go:92` `func wallABI(abi int) (used int, clamped bool)`; consumed at `internal/sandbox/wrap_linux.go:208` `used, _ := wallABI(abi)` and printed only when clamped by `ClampedABI` (`wrap_linux.go:131`) in `cmd/nova-sandbox/main.go:372`.
- net_unenforceable refusal: `internal/sandbox/wrap_linux.go:213` `return ExitRefused, refuse("net_unenforceable", "--net-deny needs landlock abi 4 ...", abi)`.
- landlock_abi_unknown refusal: `internal/sandbox/wrap_linux.go:200` `return ExitRefused, refuse("landlock_abi_unknown", ...)`.
- Standard-library-only (item 3's decision): the linux body imports only `io, os, os/exec, os/signal, path/filepath, runtime, strconv, syscall, unsafe`; raw syscall numbers and constants, no x/sys or third party.

GUARDED-BY internal/sandbox/wrap_linux_test.go:38 TestNewerLandlockABIIsClampedToTheTableOnLinux
GUARDED-BY internal/sandbox/wrap_linux_test.go:98 TestLandlockABIBelowTheTableRefusesOnLinux
GUARDED-BY cmd/nova-sandbox/wall_linux_test.go:141 TestLandlockWallClampsAnABIAboveTheTable (end-to-end, asserts `abi=<kernel> used=<wall>` and the clamped note)
GUARDED-BY cmd/nova-sandbox/wall_linux_test.go:102 TestLandlockWallRefusesWriteOutsideJob (end-to-end: the wall holds, SANDBOX OK backend=landlock)

UNGUARDED findings (behaviour real, nothing forces it red):
- `net_unenforceable` (wrap_linux.go:213): no test drives the `available` seam to an abi < 4 with `--net-deny`; it appears only as a name in the probe-reason allowlist, cmd/nova-sandbox/main_test.go:1513.
- The SIGINT/SIGTERM forwarder in Run (wrap_linux.go:265-281): no test in internal/sandbox; cmd/nova-sandbox/run_test.go:439 TestSuperviseForwardsATerminatingSignalToTheGroup exercises the swarm supervisor's reap, a different function.

Greps run:
- `grep -rn "landlock" --include='*.go' internal/sandbox/`
- `grep -rn "net_unenforceable\|SIGINT\|SIGTERM\|LockOSThread\|used=" --include='*.go' internal/sandbox/ cmd/nova-sandbox/`
- `grep -rn "func Test" --include='*_test.go' internal/sandbox/`
- `grep -rn "func Test" --include='*_test.go' cmd/nova-sandbox/ | grep -i "landlock\|wall\|net\|signal"`
- `grep -rn "net_unenforceable\|NetDeny\|NetEnforceable" --include='*_test.go' internal/sandbox/ cmd/nova-sandbox/`
- `grep -rn "refuse(" internal/sandbox/wrap_linux.go internal/sandbox/landlock_linux.go`
- `go list -f '{{.Imports}}' ./internal/sandbox/`

Left owed: none.

`git status --short`:
(empty)