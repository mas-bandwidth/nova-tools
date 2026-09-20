RESULT tools22-pre-1992-r2 sha=5298f6be12ea — a pre-read of mas-bandwidth/nova-tools#1992 at head bb065442ded6751d92a0fd19bce6afdd54ec8473: bench-standard: check the whole toolchain manifest, not just go and sbcl
PREREAD 1992 claims=7 proven=2 unproven=5 defects=2 high=0

PR 1992
HEAD bb065442ded6751d92a0fd19bce6afdd54ec8473
BASE dev
MERGE-BASE a78f3ea93b08a1ac5fd01608ecc61b1040f3a6f2
BEHIND 6
FILES 1 production, 0 test
LINES +88 -1

CLAIMS

1. The PR adds section (8) to bench-standard.sh that validates every toolchain binary required for all card legs — rust/C#, Elixir, .NET, Erlang/OTP, Node.js, Java, Dart, C/C++, build tools, git, and sqlite3 — whereas the previous script checked only go and sbcl.
   UNPROVEN

2. Each toolchain row calls tc_pin(), which verifies three things: the binary is on PATH, its version output matches an expected regex pin (or any value when want="-"), and the resolved real path falls within directories the sandbox wall grants (sdk, standard system paths, or approved homebrew/macOS Java locations).
   UNPROVEN

3. If $HOME_DIR/sdk/env.sh does not exist, drift is raised because cards and gates receive a NON-LOGIN, NON-INTERACTIVE shell that cannot read ~/.profile-based PATH entries.
   UNPROVEN

4. go retains its separate NOVA_GO floor-version check above the new block and is excluded from tc_pin() because go uses a range (">=1.26") rather than an exact pin.
   PROVEN-BY-EXISTING tools/bench-standard.sh:122 The original go check at origin/dev lines 122-130 is untouched in the diff; the comment on line 373 explicitly states why go keeps its own check.

5. The toolchain manifest covers cargo, rustc, dotnet, elixir, erl (via OTP release), node, javac, dart, cc, c++, make, cmake, git, and sqlite3 — eleven distinct toolchain families, with sqlite3 flagged as an undeclared dependency found during #1948 investigation.
   UNPROVEN

6. After the universal tc_pin rows, a dedicated block checks .NET-specific runtime prerequisites: presence of first-use sentinel files under $HOME_DIR/sdk/dotnet-home/.dotnet/ and world-writable permissions on /tmp/.dotnet, both measured as required for builds inside the wall.
   UNPROVEN

7. The "STANDARD OK" success line gains a trailing `toolchains=all-legs` suffix to signal that toolchain validation passed.
   PROVEN-BY-EXISTING cmd/nova-pulse/fleet.go:374 Production parsing uses `strings.Contains(text, "STANDARD OK")` which tolerates extra trailing fields; however, fleet_survey_test.go:42 defines `fleetStandardOK` as an exact string constant lacking the `free=` field and the new `toolchains=all-legs` field, and this constant is injected as canned test input. Whether any downstream consumer parses positionally is repository-dependent.

DEFECT medium tools/bench-standard.sh:363 homebrew path allowance is incomplete — only /opt/homebrew/Cellar/go/*, /opt/homebrew/Cellar/sbcl/*, and /opt/homebrew/opt/openjdk/* are whitelisted, but other toolchains (node, dotnet, elixir, rustc, dart, etc.) commonly installed via brew on macOS also resolve under /opt/homebrew/Cellar/<formula>/<ver>. Those installs will trip the OUTSIDE-every-root drift error despite working perfectly inside the sandbox wall. — A bench running macOS with brew-installed toolchains gets false-positive DRIFTs; reviewers who do not have those tools locally will not notice. Fix: add /opt/homebrew/Cellar/<bin>/* allowances for each non-go/sbcl/non-javac toolchain binary, or accept the parent /opt/homebrew/* as a general root consistent with how the wall treats it.

DEFECT low tools/bench-standard.sh:394-396 the dotnet sentinel loop prints a drift line for every non-matching glob expansion BEFORE finding an existing sentinel and breaking. If the directory contains multiple sentinel files (from successive incomplete .NET initialisations), earlier ones that happen to sort alphabetically before the existing file trigger spurious drift messages. — Multiple redundant DRIFT lines for a single logical state; end users see noise rather than a clean pass/fail. Fix: collect the result in a variable and emit one drift only after iterating all sentinels, or use `find … -name '*.dotnetFirstUseSentinel' -print -quit` and test whether the output is empty.

QUESTIONS FOR THE REVIEWER

1. The sqlite3 entry (line 391) checks `-version` but sqlite3's CLI prints its library version format (`sqlite3 <version>` or `sqlite3: command not found`). Does `--version` produce the expected output shape for sqlite3, or does it fall through silently as "-any-"? If sqlite3 lacks a --version flag, the tc_pin call would execute `sqlite3 --version` which may either print nothing (matched by "-" wildcards) or exit non-empty, confusing the parse.

2. The env.sh existence check (line 370) only verifies the file exists; it does not source it or probe the resulting PATH. A file that exists but does not export PATH correctly would pass this check while still failing cards in practice. Should the check source env.sh in a subshell and assert that go (or another key tool) resolves afterward?

3. On Linux, the wall grants exactly `~/sdk` (EXECUTE) and `~/go/pkg/mod` (read-noexec). The tc_pin case pattern accepts `/usr/*`, `/bin/*`, and `/lib/*` without restriction. Are all binaries under /usr/bin actually visible to cards at the wall level, or only a subset provisioned by the provisioning standard? If /usr/lib tools are invisible, accepting /usr/* is over-permissive.

4. The homebrew allowance on line 363 covers only go, sbcl, and openjdk. Is the omission of other brew-installed toolchains intentional (expecting them only under ~/sdk on CI benches) or an oversight? What does the provisioning standard say about where non-go/sbcl Java toolchains should live on darwin machines?

Left owed

Did not read internal/swarm/toolchain.go beyond the toolchainRoots map declaration (lines 150-163); did not trace how swarm.ToolchainRootNames is used elsewhere in the test suite; did not grep for other consumers of the "STANDARD OK" output line across the entire repo to assess the blast radius of adding `toolchains=all-legs`; did not examine docs/CLI.md or docs/SPEC-CI.md for any documentation that mentions bench-standard.sh's output grammar. Did not attempt go build of the affected packages — permitted but not required by the card scope.

git status --short
git rev-parse HEAD
5298f6be12eaa0f7e6622334d2b6a1eb427649e3
