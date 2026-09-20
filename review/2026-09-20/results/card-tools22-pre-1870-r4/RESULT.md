RESULT tools22-pre-1870-r1 sha=5298f6be12ea — a pre-read of mas-bandwidth/nova-tools#1870 at head 6f901e1c87e2
PREREAD 1870 claims=4 proven=3 unproven=1 defects=0 high=0

PR 1870	HEAD 6f901e1c87e24fa54ecad0b4d126868bf51983a3	BASE dev	MERGE-BASE 0f0e46c0e26bfbab7fc84c0dfead4f551fb1c503	BEHIND 21
FILES 2 production, 2 test	LINES +185 -1

CLAIMS

1. (3b) bench-standard.sh now resolves each of `go` and `sbcl` off PATH via `readlink -f` and produces a DRIFT line when the resolved target is not under any sandbox-wall read root (the hard-coded system roots plus `$HOME/sdk`).
PROVEN-BY internal/ci/benchstandard_wall_toolchain_test.go:60 TestBenchStandardDriftsOnAToolTheWallCannotExecute — places sbcl at `.local/bin/sbcl` (outside every granted root) and asserts exactly one DRIFT line carrying `$HOME/sdk`, `sdk/sbcl-`, and `EXECUTE`.

2. A tool under `$HOME/sdk/<tool>-<ver>/` is accepted by (3b): no DRIFT line is produced for tools installed there.
PROVEN-BY internal/ci/benchstandard_wall_toolchain_test.go:98 TestBenchStandardAcceptsAToolUnderAGrantedRoot — installs sbcl at `sdk/sbcl-2.5.8/bin/sbcl` inside a fake HOME and asserts zero DRIFT lines; same for go.

3. Every lisp card was forced onto the single bench whose sbcl lived in `/usr/bin/sbcl` because interpreters placed under `$HOME/.local/bin` satisfy `command -v` but are `Permission denied` inside the sandbox wall — E09-G1 took 1036 s on vision vs 248–393 s elsewhere, and an r1785 worker fetched its own SBCL into `$TMPDIR`.
UNPROVEN — the comment block in the new test file narrates these events with concrete numbers, but the diff contains no test that asserts them, no log artifact, and nothing verifiable in this change. The story is asserted, not witnessed.

4. AGENTS.md now lists `walltoolchain` alongside the ten existing class-test names, pointing readers to its entry in docs/SPEC-CI.md.
PROVEN-BY-EXISTING AGENTS.md:62 — the one-line insertion is visible in the diff stat.

DEFECTS none

QUESTIONS

1. How does the darwin side handle symlinks that resolve outside granted trees? `internal/swarm/toolchain.go` documents the problem — brew-installed launchers point into `<Cellar>/<ver>/` which may be outside any named root — and declares that darwin roots include versioned Cellar prefixes resolved at runtime via the `Tool` field. The bash check (3b) only exists for Linux; is there a separate darwin mechanism, or does the darwin bench rely entirely on the Go-side wall to do the right thing without a pre-flight standard check?

2. Is `$HOME_DIR/sdk` alone sufficient for all bench-provisioning layouts? `internal/swarm/toolchain.go` grants `go/pkg/mod` as a non-exec root and excludes `~/go/bin` intentionally (writable-by-user). The (3b) loop only tests executability against `$HOME/sdk` and system roots — if someone puts a custom `go` binary under `~/go/bin/go` (not a symlink into sdk), (3b) would report drift even though the provisioned toolchain tree is fine. Is that the intended behaviour (flag it) or would a false-positive drift line confuse operators?

3. Does the `benchStandardWithTool` helper guarantee isolation from other test state? It sets `PATH=<fake>:...<env_PATH>` by appending the OS path separator (which happens to be `:` on Linux but looks like a concatenation bug at a glance), and relies on other parts of the REAL bench-standard.sh output for noise — those unrelated DRIFT lines might change independently and affect combined-output parsing. Is the noise stable enough, or should the test run the script with fewer environment variables to reduce coupling?

Left owed
Read no farther than the opening comments and one grep across `internal/swarm/toolchain.go`, `internal/sandbox/wrap_linux.go`, and `internal/ci/toolchainroots_class_test.go`. Specifically: did not read the darwin-specific sections of `toolchain.go`, did not verify that `toolchainroots_class_test.go` (pre-existing) aligns its own system-root list with the (3b) bash check, and did not read past line ~50 of `wrap_linux.go` (confirmed `linuxReadRoots` definition).

cd /home/nova/rowan-working/tmp/84069665-945a-4358-bce5-b203fcfa7ba0-card-tools22-pre-1870-r1/jobs/card-tools22-pre-1870-r1/repo && git status --short
cd /home/nova/rowan-working/tmp/84069665-945a-4358-bce5-b203fcfa7ba0-card-tools22-pre-1870-r1/jobs/card-tools22-pre-1870-r1/repo && git rev-parse HEAD
(no output)
d576bf6bbabb39068096a97b4560de9b5e245970
