RESULT tools22-pre-2003-r2 sha=5298f6be12ea — a pre-read of mas-bandwidth/nova-tools#2003 at head 637a8738bc4ab5b33760848218f831e844fbda7b: nova-sandbox: policy on linux prints the Landlock ruleset, not the darwin profile template (#14
PREREAD 2003 claims=4 proven=2 unproven=2 defects=0 high=0

PR 2003
HEAD 637a8738bc4ab5b33760848218f831e844fbda7b
BASE dev
MERGE-BASE 3666f40487a97447c1632e4bebf2a40585077e4f
BEHIND 5
FILES 3 production, 1 test
LINES +148 -1

CLAIMS

1. On linux builds the `policy` verb now prints the Landlock ruleset text (backend, ABI, network promise, read-only roots, optional roots, reads, read-noexecs, writes, writefiles, GPU mode) instead of printing the darwin sandbox-exec shell profile template.
PROVEN-BY cmd/nova-sandbox/policy_linux_test.go:20 TestPolicyVerbOnLinuxPrintsLandlockRuleset — the integration test calls the policy verb on linux with --net-deny and asserts the output does not contain "darwin sandbox-exec profile" and that it contains "backend=", "read=", "write=", "net=".

2. The new helper function `policyText()` in main.go dispatches to `LandlockPolicyText()` when backend is "landlock" and falls through to `DarwinProfile()` otherwise; `policyVerb` calls `policyText(p)` instead of calling `DarwinProfile(p)` directly.
PROVEN-BY-EXISTING cmd/nova-sandbox/main.go:891 `policyText` definition and cmd/nova-sandbox/main.go:936 `policyText(p)` call replacing the prior `DarwinProfile(p)` call. No standalone test exercises the non-landlock branch of `policyText`.

3. `LandlockPolicyText()` renders a textual representation of the landlock ruleset: header comments, backend name, ABI version (with notes when clamped or unavailable), network promise, read-only roots from the roots table (skipped if absent), optional roots, read/read-noexec sets, write paths, writable device files, and GPU mode — all in the same order that `addRules` applies them.
UNPROVEN — the test checks only four labels ("backend=", "read=", "write=", "net="). It does not assert the presence of abi, writefile, gpu, or opt-root entries.

4. Non-linux builds compile because a stub `LandlockPolicyText()` exists in an `!linux` file and returns an error naming the platform's actual backend.
UNPROVEN — no test invokes the non-linux stub and asserts its error message. The build constraint ensures compilation but nothing exercises the runtime path.

DEFECTS none

QUESTIONS FOR THE REVIEWER

1. The `policyText` helper compares `sandbox.Backend == "landlock"` by string value. Backend is a const so this never changes, but would `runtime.GOOS == "linux"` be more robust as a design choice, especially if the project ever experiments with alternative Linux sandboxes?

2. `LandlockPolicyText` emits every entry from `p.OptRoots` unconditionally (no absence check), while the comment above says "skipped if absent — the same skip addRules applies." In `addRules`, OptRoots errors are silently swallowed via `_ = addPathRule(...)`, so the wall also proceeds even if an optional root doesn't exist. Should the printed text match addRules exactly (i.e., still list absent OptRoots since that is what the wall sees) or should absent OptRoots be filtered out for clarity?

3. The POLICY OK line added `read-noexec=%d` to stderr between the merge-base and the PR head — was that change part of this PR's intent, or did it come from intermediate commits on dev that landed after the PR forked off at 3666f404?

4. `LandlockPolicyText` is gated behind `//go:build linux` but references `available()`, `wallABI()`, `linuxReadRoots`, `linuxWriteFiles`, and `writePaths()` which live in `wrap_linux.go` and other linux-gated files. If someone adds a new non-linux-specific caller of `LandlockPolicyText`, will the linker catch the missing symbols, or could there be a silent compilation failure? Is any cross-reference documentation needed?

Left owed — I did not diff the complete wrap_darwin.go, profile.go, or wrap_linux.go beyond what was necessary to confirm symbol availability. I also did not run `go vet` or `go build` to verify compilation; I relied on static analysis of build tags and package-level variable assignments.

git status --short

git rev-parse HEAD
d576bf6bbabb39068096a97b4560de9b5e245970
