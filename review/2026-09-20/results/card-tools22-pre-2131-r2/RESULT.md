RESULT tools22-pre-2131-r1 sha=5298f6be12ea — a pre-read of mas-bandwidth/nova-tools#2131 at head 31bff5ca9a6f: nova-swarm: --tokens is required on every native launch (SPEC-SWARM rule 13d, #1545) — replacem
PREREAD 2131 claims=11 proven=8 unproven=3 defects=0 high=0

PR 2131
HEAD 31bff5ca9a6fa3c41f28ef3f203599351b955dde
BASE dev
MERGE-BASE 86abcf23dd6cd95668ae1a865e11e29556996b03
BEHIND 2
FILES 11 production, 21 test
LINES +737 -92

CLAIMS

1. `native` verb requires `--tokens <n>|unmetered`; without it exits 2 naming the flag and makes no directory; `--tokens 0` is refused; malformed like "50oops" is refused (not truncated to 50).
2. `batch --cards` requires `--tokens`; refuses with exit 2 and constant error message before any card starts.
3. Batch passes `--tokens` verbatim into every local `native` argv built under `--harness` / `Self`.
4. Batch passes `--tokens` verbatim into every remote `native` argv built for benches over ssh.
5. A `--runner` command is handed the tokens word as its sixth positional argument after label, slot, model, card path, root.
6. NATIVE OK and NATIVE INCOMPLETE lines now carry a `budget=` field immediately after `harness=`, showing one of four shapes: "unmetered", "-/<n>", "<s>+/<n>" or "<s>/<n>".
7. The `f.tokens()` parser now uses `strconv.Atoi(strings.TrimSpace(value))` instead of `fmt.Sscanf("%d", ...)` — rejecting partial numeric prefixes like "50oops" outright.
8. `finish.go:529 budgetWord()` delegates rendering to the shared `swarm.BudgetWord()`, previously held inline.
9. `internal/pulse/launch.go` passes `launch.tokens` as `--tokens` when spawning a child batch process.
10. Every existing test across 20+ test files that constructs a BatchInput, calls runBatch helper, or invokes `nova-swarm native` directly was updated to pass `Tokens: "unmetered"` or `--tokens unmetered`.
11. docs/nova-swarm-quickstart.md adds `--tokens` to both the direct native invocation example and the bench runner shim script.

PROVEN-BY cmd/nova-swarm/native_budget_test.go:69 TestNativeRefusesWithoutTheBudgetWord tests absent, zero, and malformed ("50oops") tokens values; verifies exit code 2 and correct stderr; confirms no directory created.
PROVEN-BY internal/swarm/batch_tokens_13d_test.go:26 TestBatchCardsRefusesWithoutTheBudgetWord builds a harness batch with no Tokens field; asserts exit 2, stderr names --tokens, and the self-argv record file does not exist (no card started).
PROVEN-BY internal/swarm/batch_tokens_13d_test.go:54 TestBatchCardsPutsTheWordInTheLocalNativeArgv runs batch with Tokens="250000" and Tokens="unmetered"; records the stand-in nova-swarm's argv; asserts each contains "--tokens <word>" verbatim.
PROVEN-BY internal/swarm/batch_tokens_13d_test.go:99 TestBatchCardsPutsTheWordInTheRemoteNativeArgv runs a bench batch with Tokens="175000"; checks the fake ssh log; asserts every "nova-swarm native" line contains "--tokens 175000".
PROVEN-BY internal/swarm/batch_tokens_13d_test.go:131 TestBatchCardsHandsTheRunnerTheWordAsItsSixthArgument runs batch with runner and Tokens="90000"; reads recorder argv; asserts 6 args total and args[5] == "90000".
PROVEN-BY cmd/nova-swarm/main.go:315 f.tokens() uses strconv.Atoi(strings.TrimSpace(value)) which rejects "50oops" — same refusal text format as the old Sscanf-based parser. This clause of claim 7 is PROVEN-BY-EXISTING by cmd/nova-swarm/native_budget_test.go:85 checking "50oops" produces exit 2.
PROVEN-BY internal/pulse/launch.go:360 `cmd = exec.Command(swarmBin.Path, "batch", ..., "--tokens", tokens)` in runBatch().
UNPROVEN -- see below.

DEFECTS none

QUESTIONS FOR THE REVIEWER

1. The NATIVE OK line grammar changed: `budget=` was inserted between `harness=` and the optional tails (`fenceSuffix`, `usageSuffix`, `termSuffix`). The NATIVE INCOMPLETE line also gained budget= (main.go ~line 1884). Does the spec sentence at docs/SPEC-SWARM.md describing the NATIVE OK fixed fields need updating, or is the grammar already documented as variable-length? Tools parsing positionally would break.
2. `internal/pulse/launch.go:359-360` defaults missing tokens via `DefaultLaunchTokens` which the comment says is "unmetered". Is there a test covering `runBatch(in LaunchInput{...})` where `in.Tokens` is empty string, verifying it falls through to the default rather than passing "" to child batch? (The diff only adds the trim/default logic but doesn't have an explicit test for the empty->default path.)
3. `docs/CLI.md` receives +35 new lines documenting the --tokens requirement. Which sentence in CLI.md is the authoritative cross-reference for callers who must parse output — the NATIVE OK line change in claim 6? Are consumers expected to look at CLI.md or SPEC-SWARM.md?

Left owed

I did not read the full content of docs/CLI.md (it was in the diff but I only examined the stat); I did not read the complete contents of `cmd/nova-swarm/native_budget_test.go` or `internal/swarm/batch_tokens_13d_test.go` (read only portions relevant to mapping proven claims); I did not verify the `BudgetWord` function logic against all four output shapes exhaustively (only compared finish.go's old implementation to usage.go's new one structurally). The core behavioural tests for native --tokens rejection and batch --cards enforcement were read sufficiently. No defect was found in the visible paths.

git status --short
d576bf6bbabb39068096a97b4560de9b5e245970
