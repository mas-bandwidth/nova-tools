RESULT tools22-pre-1955-r1 sha=5298f6be12ea — a pre-read of mas-bandwidth/nova-tools#1955 at head 7fa7dc2884da: nova-pulse launch resolves --bench names against the machines registry before the batch (#1905)
PREREAD 1955 claims=8 proven=5 unproven=3 defects=4 high=0

PR 1955
HEAD 7fa7dc2884dab41f34fe352d77e71b0c4db33ce1
BASE dev
MERGE-BASE 23d9698b0b862641a7569dd268c6f1ed8c4d57b0
BEHIND 9
FILES 3 production, 2 test
LINES +94 -7

CLAIMS

1. launch resolves its `--bench` names against the machines registry before the batch is admitted, so a runner host is refused and never handed to nova-swarm batch.
   PROVEN-BY internal/pulse/machines_test.go:232 TestLaunchRefusesARunnerHostBench — names batman (a runner), asserts exit 2, the `PULSE REFUSED bench=batman reason=runner-host` line, and that the fake nova-swarm's argv.log stays empty.
2. a refused bench name prints one `PULSE REFUSED bench=<name> reason=<reason> remedy="…"` line and launch exits 2.
   PROVEN-BY internal/pulse/machines_test.go:245 (the line prefix) and :248 (exit == 2).
3. an unknown machine, the coordination bench and a services host are refused at launch the same way.
   UNPROVEN — only the runner-host reason is driven at the launch seam (machines_test.go:232). RequireBench covers the other reasons, but only in internal/fleet/registry_test.go:151 (TestRequireBenchRefusesTheCoordinationHostAndAnUnknownMachine), which never runs launch; no launch test names a coordination/services/unknown bench.
4. every refused name gets its own line, not one line per run.
   UNPROVEN for launch — the loop at internal/pulse/launch.go:225 prints one line per name, but no launch test names two refused benches; fill's TestFillNamesEveryRefusedBench (machines_test.go:77) proves the pattern for fill only.
5. naming a bench without `--machines` is refused outright (PULSE REFUSED missing --machines, exit 2, no batch).
   UNPROVEN — implemented at internal/pulse/launch.go:216-218, but no test drives launch with a bench named and Machines empty; TestFillRefusesWithoutTheMachinesRegistry (machines_test.go:101) is fill-only.
6. a launch with no `--bench` names nothing and runs exactly as before, with no registry required.
   PROVEN-BY-EXISTING internal/pulse/launch_test.go:73, :108, :190, :238 (TestLaunchRefusesUnderSlots, TestLaunchAdmitsThroughCardsForm, TestLaunchCarriesTheFileBudget, TestLaunchQueuesRemainder) — all call Launch with empty Bench and Machines and expect the pre-change exit codes and argv; each would go red if the guard fired or altered behavior when no bench is named.
7. the refused launch reaches no nova-swarm batch at all.
   PROVEN-BY internal/pulse/machines_test.go:251 (the fake swarm's argv.log stays empty).
8. the CLI wires the new `--machines` flag through to Launch, and `--benches`/`--bench` still reach the batch unchanged when given.
   PROVEN-BY cmd/nova-pulse/main_test.go:328-341 TestLaunchPassesBenchesThrough — exit 0 with `--machines`, batch argv carries `--benches <file>` and `--bench studio,space`; it would go red if the flag were not wired (the guard would then refuse missing --machines).

DEFECTS

DEFECT medium docs/SPEC-PULSE.md:735 — the normative launch synopsis still omits `--machines`, but the code now refuses any `--bench` invocation without it ("missing --machines; refusing to guess") — a reader or tool building launch from the spec constructs an invocation the code rejects, and AGENTS.md declares the spec normative ("where the code and the spec disagree, one of them has a bug and the tests decide which") — fix: add `[--machines <file>]` to the SPEC-PULSE launch synopsis and describe the guard (this PR updated only docs/CLI.md).

DEFECT low internal/pulse/launch.go:39 — the `Machines` field comment "Empty leaves the verb unguarded, the same narrowing the fleet verbs and harvest --bench carry" contradicts the code, which refuses outright (launch.go:216-218) whenever a bench is named and Machines is empty — the opposite of unguarded, and it contradicts the CLI.md paragraph this same PR adds — a reader of the struct is told the launch is unguarded when it is not — fix: reword the comment to say a named bench with no registry is refused.

DEFECT low internal/pulse/launch.go:227 — the refusal loop matches only `*fleet.Refusal` via errors.As and silently drops any other error `RequireBench` returns (admitting the launch), diverging from the sibling `requireBench` (internal/pulse/harvestbench.go:58-75) which has an `err != nil` fallback — unreachable today because RequireBench returns only *Refusal, but a silent-admit false-green if that ever changes — fix: add the `err != nil` refusal branch like requireBench.

DEFECT low docs/CLI.md:1352 — "a launch with no `--bench` names nothing and runs on this machine exactly as before" overstates the guard: with `--benches <file>` given but no `--bench`, the file's rows still reach `nova-swarm batch` over ssh (runBatch appends `--benches` whenever non-empty) and the new guard never resolves them — a reader is told no registry is ever needed when `--bench` is absent — fix: qualify the sentence to no `--benches` and no `--bench`, or resolve the file's names too.

QUESTIONS

1. The deployment's launch command: does it name `--bench`, and if so does it pass `--machines queue/control/machines.tsv`? Nothing in the repo forces the flag (cmd/nova-pulse only wires it, defaulting empty); the LaunchInput comment at launch.go:40 asserts "cmd/nova-pulse names it on every real invocation," and if any real launch names benches without `--machines`, this change turns every such launch into a hard exit-2 refusal.
2. Only `--bench` names are resolved; the `--benches <file>` table's rows, which also carry ssh targets to the batch, are not held against the registry. Is that a deliberate boundary, or should the file's names be resolved too?
3. launch now refuses outright when `--bench` is named and `--machines` is empty, while `harvest --bench`'s `requireBench` (harvestbench.go:58) returns 0 in the same situation — the comment claims "the same narrowing the fleet verbs and harvest --bench carry," but the two verbs' policies are opposite. Which is the intended contract?
4. The real fleet example (internal/fleet/testdata and TestTheExampleIsTheFleetWeHave) marks `studio` as coordination+runner, not bench, yet CLI.md:1341 still says one pulse fills "the Studio and the Space in one tick" and the updated TestLaunchPassesBenchesThrough models `studio` as a bench — under the new guard a production `--bench studio` is refused. Has that "Studio" example been superseded?

Left owed

Read in full: all five changed files — internal/pulse/launch.go (whole file at head), internal/pulse/machines_test.go (whole file), cmd/nova-pulse/main_test.go (the changed test in full, plus surrounding helpers), docs/CLI.md (the launch section and its surroundings), and the cmdLaunch region of cmd/nova-pulse/main.go. Supporting context read as needed: internal/fleet/registry.go and registry_test.go, harvestbench.go's requireBench, fill.go, fleetverbs.go, manager.go's splitList, harvest.go's refusal, launch_test.go, fake_test.go, wire.go's launch caller, and SPEC-PULSE's launch/fleet sections. Not read: the remainder of cmd/nova-pulse/main.go (866 lines, untouched by the diff), the rest of docs/CLI.md, and the 9 landed integration batches between the merge base and dev (I confirmed none rewrites this PR's hunks; build and vet pass). No test run was performed; none is required.

git status --short

git rev-parse HEAD
7fa7dc2884dab41f34fe352d77e71b0c4db33ce1===FILE=== card-tools22-pre-1955-r1/usage.tsv
job	attempt	started	ended	rc	provider	model	tokens_in	tokens_out	cache_write	cache_read	reasoning	usd
card-tools22-pre-1955-r1	1	2026-09-20T19:26:08Z	2026-09-20T19:40:19Z	0	opencode	deepseek-v4-flash	84349	39843	0	4061440	0	0.1367
