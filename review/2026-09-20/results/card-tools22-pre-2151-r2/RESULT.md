RESULT tools22-pre-2151-r1 sha=5298f6be12ea — a pre-read of mas-bandwidth/nova-tools#2151 at head fc653ac9402a317560a34d2264a6e51e6d95d9bd: sandbox, secrets: pin three unguarded production commits so a revert goes red
PREREAD 2151 claims=3 proven=3 unproven=0 defects=0 high=0

PR 2151
HEAD fc653ac9402a317560a34d2264a6e51e6d95d9bd
BASE dev
MERGE-BASE a7611c8189d33979923064f4d10eb07fb9957730
BEHIND 1
FILES 0 production, 4 test
LINES +89 -13

CLAIMS

1. Input and Policy structs in internal/sandbox do not carry SystemReads or NoSystemReads fields; linuxReadRoots on policy.go is the one enforced system-reads mechanism — PROVEN-BY internal/sandbox/systemreads_field_test.go:9 TestSystemReadsAreNotAFieldOnInputOrPolicy uses reflection to assert that neither Input{} nor Policy{} has either field name, failing with Fatalf if found.

2. RunExec in internal/secrets returns a single refusal listing all missing required flags (--store, --as, --key, --sops, --only) plus a pasteable example invocation instead of stopping at the first empty string — PROVEN-BY internal/secrets/exec_test.go:8 TestRunExecRefusalNamesEveryMissingRequiredFlagTogether calls RunExec with all-empty args and asserts exit code 125, every flag substring present, and "example:" / "nova-secrets exec" in the error.

3. sshPlaceSecret in internal/secrets invokes testguard.RefuseHosts before spawning any child process, preventing unfaked SSH connections to real hosts — PROVEN-BY internal/secrets/place_test.go:18 TestPlaceSSHSeamPanicsUnderTheGuard arms the guard via env var, calls sshPlaceSecret("ssh", "bench.invalid", …), and expects a recoverable panic naming testguard.EnvNoHost, "ssh", "bench.invalid", and "testguard.AllowHosts".

DEFECTS none

QUESTIONS

1. The original TestSystemReadsAreNotAFieldOnInputOrPolicy landed in systemreads_test.go behind //go:build linux (per the commit message). Was the linux-only build constraint an oversight or was there a reason it could not run on darwin? Moving it to a no-tag file makes it run everywhere, but understanding the original intent would clarify whether platform-specific struct definitions ever diverged.

2. In TestPlaceSSHSeamPanicsUnderTheGuard, if testguard integration broke silently (the guard check was bypassed without panicking), sshPlaceSecret would attempt an actual SSH connection to bench.invalid, causing the test to hang. Is a t.Parallel() timeout or a separate context deadline warranted as a safety net?

3. In TestRunExecRefusalNamesEveryMissingRequiredFlagTogether, the command argument is []string{os.Args[0]} (non-empty). Is the combined-refusal check performed before or after command/argument validation in RunExec? The test exercises a path where commands are valid but config flags are all empty — is this the intended early-exit path?

Left owed
I did not read the referenced production files (policy.go, exec.go, place.go) beyond what the diff implies, since this PR contains only test changes. I also did not inspect commits c1cbe386 (#948), 6dbfe26e (#1477), or 47d81e9c beyond their descriptions in these new test comments. The behavior of linuxReadRoots in policy.go, the exact refusal logic in RunExec, and the call chain inside sshPlaceSecret were not read firsthand.

git status --short:
(no output)
git rev-parse HEAD: d576bf6bbabb39068096a97b4560de9b5e245970
