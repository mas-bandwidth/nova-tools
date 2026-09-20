"RESULT tools22-rule-sandbox-3-L1797 sha=5298f6be12ea — does the code at this base do what docs/SPEC-SANDBOX.md rule 3 says?
KIND: transcript-test
DEADLINE: 1800
LEG: go
PATHS: internal/sandbox
FILES: 0
TEST: none
MODE: read
TURNS: 30
SOURCE: docs/SPEC-SANDBOX.md:1797
BASE: dev@5298f6be12eaa0f7e6622334d2b6a1eb427649e3
base-repo: /tmp/nova-tools-mirror.git
base-sha: 5298f6be12eaa0f7e6622334d2b6a1eb427649e3
ROUTE: jev=flash why=- eligible=rules
CONFORMS cmd/nova-sandbox/main.go:838
SPEC docs/SPEC-SANDBOX.md:1797 rule 3
PKG internal/sandbox (code lives in cmd/nova-sandbox, not internal/sandbox)

ASK: The implementation must verify that the probe-step verb's immediate parent process is running the exact same binary (matched by device+inode identity, not name), using a platform-specific syscall to obtain the parent's executable path — proc_pidpath(2) on Darwin (not ps, because ps is setuid root and denied inside the wall) — and refuse with exit 2 and a PROBE REFUSED reason=probe_step_not_a_child line if the parent is not this binary. The check re-reads both the parent PID and image after the first read to detect exec or reparenting within the window between syscalls.

The guard checks three conditions; rule 3 covers only the third:
  1. fd 3 is an inherited pipe carrying exactly 16 nonce bytes (main.go:714)
  2. Those bytes hex-encoded match argv[0] and environment NOVA_SANDBOX_PROBE_NONCE (main.go:718-731)
  3. The parent process is running THIS binary — same device+inode, not just same name (main.go:733-742)

Deciding lines — main.go:838 calls the guard before any file is opened:
  main.go:838:	if r := notTheProbesChild(nonce, env); r != "" {
  main.go:839:		fmt.Fprintf(stderr, "PROBE REFUSED reason=probe_step_not_a_child: %s runs only as the child of a probe this binary started, and this invocation is not one (%s); nothing was opened. Run: nova-sandbox probe --write <dir> --secret <path>\n", probeStepVerbName, r)
  main.go:840:		return sandbox.ExitCannotRun   // ExitCannotRun == 2

The parent check inside notTheProbesChild:
  main.go:742:	return parentIsThisImage(self, os.Getppid, parentExecutable)
  main.go:768:func parentIsThisImage(self string, getppid func() int, imageOf func(pid int) (string, error)) string {
    Reads pid → image → pid again → image again; refuses if anything moved between reads (main.go:768-795). Uses os.SameFile for identity:
  main.go:815:func sameImage(self, parent string) bool {
  main.go:824:	return os.SameFile(selfInfo, parentInfo)

Darwin-specific parentExecutable via proc_pidpath(2) (NOT ps):
  parent_darwin.go:22:func parentExecutable(pid int) (string, error) {
    callnum PROC_INFO_CALL_PIDINFO (2), flavor PROC_PIDPATHINFO (11) — direct syscall, no subprocess. Matches spec requirement that the question be proc_pidpath(2) and not ps -o comm= -p.
  parent_darwin.go:13:// It is proc_pidpath(2) through the proc_info syscall and NOT
  parent_darwin.go:14:// `ps -o comm= -p`, because the child asks this question INSIDE the wall and ps is
  parent_darwin.go:15:// setuid root … while a set-id exec is denied inside the wall

Linux fallback:
  parent_linux.go:14:func parentExecutable(pid int) (string, error) {
    return os.Readlink("/proc/" + strconv.Itoa(pid) + "/exe")

Refusal message format matches spec — single line to stderr, exit 2, reason token:
  main.go:832:	fmt.Fprintf(stderr, "PROBE REFUSED reason=probe_step_not_a_child: %s is internal and takes <nonce> <name> <path>; it is the child of a probe this binary started and nothing else runs it\n", probeStepVerbName)
  main.go:839:	fmt.Fprintf(stderr, "PROBE REFUSED reason=probe_step_not_a_child: %s runs only as the child …\n", probeStepVerbName, r)
  main.go:846:	fmt.Fprintf(stderr, "PROBE REFUSED reason=probe_step_not_a_child: %s wants an absolute path and got %s\n", probeStepVerbName, oneline.Escape(path))

All three refusal points print the same format: the argument count (arg len != 3 is first), then the guard text (r from notTheProbesChild), then the absolute path check. Each exits 2. Spec says this reason token is outside the fixed set of six and written down here because the grammar above is fixed at six. Confirmed: grammar_test.go:34 declares probeStepRefusalReason = "probe_step_not_a_child" as the one reason outside the spec's fixed set.

GUARDED-BY cmd/nova-sandbox/main_test.go:789 TestProbeStepIsTheInternalVerb (comprehensive integration test that exercises the full guard including parent check via probeChild helper, plus copies of this binary being refused at main_test.go:855)

Also guarded by:
  cmd/nova-sandbox/main_test.go:908 TestTheParentGuardComparesFilesAndNotNames — unit tests sameImage(device+inode vs copy)
  cmd/nova-sandbox/main_test.go:1016 TestTheParentGuardRereadsTheImageAndNotJustThePid — unit tests the 4-read ordering
  cmd/nova-sandbox/grammar_test.go:110 grammar_test.go — verifies probe-step refused by hand with correct reason token

Greps ran:
  grep -rn "probe_step_not_a_child\|PROC_PIDPATH\|proc_pidpath\|parent.*executable\|parent.*binary\|probe.step\|probe-step" --include='*.go' .
  grep -rn "PROBE\|proc_pidpath\|parent.*path\|checkParent\|VerifyParent\|guard\|fd 3\|not_a_child\|step.*verb\|probeStep" --include='*.go' internal/sandbox/
  grep -rn "probe_step_not_a_child\|proc_pidpath\|probe-step\|PROBE REFUSED" --include='*.go' .
  grep -rn "func Test" --include='*_test.go' cmd/nova-sandbox/ | grep -i "probe\|parent\|guard\|step\|child"

Left owed

git status --short
