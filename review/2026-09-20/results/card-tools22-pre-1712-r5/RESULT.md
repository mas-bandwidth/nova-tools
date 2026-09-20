tools22-pre-1712-r4
PREREAD 1712 claims=11 proven=11 unproven=0 defects=1 high=0
PR 1712, HEAD 902efaa251d2f4afb65e898cc45f7f53e9da3630, BASE cap-slice2-source-check, MERGE-BASE d4676d3954ed9952b4ddc43c8b27382f75c6a25f, BEHIND 0, FILES 11 production, 5 test, LINES +1746 -29

1. The live sampler reads the harness's own sqlite database in a goroutine beside the launch's select, bounded by LiveSampleLimit (5s) with no two reads overlapping, and a slow reader never delays the deadline. PROVEN-BY cmd/nova-swarm/native_budget_sample_test.go:564 TestLiveSamplerNeverRunsTwoReadsAtOnce — asserts maxFlight <= 1; cmd/nova-swarm/native_budget_sample_test.go:510 TestNativeSamplerNeverOverlapsAndNeverDelaysTheDeadline — asserts deadline ends the card despite a reader that sleeps 600s

2. The token budget is `spent >= n` where `spent` = tokens_in + tokens_out + reasoning; cache_write and cache_read are excluded from the sum. PROVEN-BY cmd/nova-swarm/native_budget_sample_test.go:376 TestNativeLineReportsWhatTheFinalReadSaw — asserts budget=157/50000 for input=100, output=50, reasoning=7 against cache_write=9000, cache_read=90000

3. The NATIVE OK line carries `stopped=<tokens|max_turns|max_cache_read|unverifiable>` when a budget fires, and never carries it when the deadline or a TERM ends the card. PROVEN-BY cmd/nova-swarm/native_budget_stop_test.go:766 TestNativeBudgetStopsTheCardAndKeepsWhatItPublished — asserts stopped=tokens; cmd/nova-swarm/native_budget_sample_test.go:553 TestNativeSamplerNeverOverlapsAndNeverDelaysTheDeadline — asserts stopped="" when the deadline fired

4. A budget stop ends the card via Reap (terminate + wait + kill), exit 1, rc=-1 on the line, and the row carries `end=budget` with rc=dash. PROVEN-BY cmd/nova-swarm/native_budget_stop_test.go:728 TestNativeBudgetStopsTheCardAndKeepsWhatItPublished — asserts rc=-1, stopped=tokens, exit 1, end=budget, rc=dash, grandchild killed, RESULT.md unchanged

5. The PROMPT-DEFECT line (from a card budget stop) is printed on native's stdout AFTER the NATIVE verdict line and is written into no file. PROVEN-BY cmd/nova-swarm/native_budget_stop_test.go:1060 TestNativeCardBudgetStopsAndPrintsThePromptDefect — asserts PROMPT-DEFECT line after NATIVE OK, absent from RESULT.md

6. Three consecutive failed reads end the card with `end=budget-unverifiable`, `stopped=unverifiable`, exit 1; two failures then an answer end nothing. PROVEN-BY cmd/nova-swarm/native_budget_stop_test.go:992 TestNativeThreeFailedReadsEndTheCardUnverifiable — both sub-cases asserted

7. Under `--tokens unmetered` with no card budget, the line prints `budget=unmetered` whatever the harness reported, and the sampler is not started. PROVEN-BY cmd/nova-swarm/native_budget_sample_test.go:430 TestNativeUnmeteredIsNeverAFigure — asserts budget=unmetered for a harness with 100+50+7=157 spend

8. Two-launch accounting keeps rows disjoint: rows hold each launch's own final figures (40 and 70), the line prints their sum (110/100), and only the stopping row carries `end=budget`. PROVEN-BY cmd/nova-swarm/native_budget_stop_test.go:881 TestNativeTwoLaunchAccounting — asserts rows 40 and 70, line 110/100, end=budget on second row only

9. A first launch that reached the budget alone is never launched again (StopWord check before relaunch). PROVEN-BY cmd/nova-swarm/native_budget_stop_test.go:945 TestNativeALaunchThatReachedTheBudgetAloneIsNeverLaunchedAgain — asserts one launch only, stopped=tokens

10. The `end` column joins CardUsageColumns after `ended` and before `rc` (14 columns); all readers map by header name so old 13-column files still parse. PROVEN-BY internal/pulse/status_index_test.go:376 TestStatusIndexMapsUsageColumnsByHeaderName — asserts both column counts parse rc and usd correctly

11. The live sampler counts card turns from `<job>/harness-output.log` (not `harness.log`) via CountCardTurnsIn, and the card budget (max_turns, max_cache_read) is enforced by the same samples. PROVEN-BY cmd/nova-swarm/native_budget_stop_test.go:1060 TestNativeCardBudgetStopsAndPrintsThePromptDefect — asserts stopped=max_cache_read and stopped=max_turns via FAKE-TURNS

DEFECT low cmd/nova-swarm/nativesample.go:89 — the `startLiveSampler` function's first call with empty args (`startLiveSampler("", 0, cfg, outLog)`) creates a sampler that immediately calls s.Stop(), yet the function allocates fields including a channel and two sync.Once values that are never used — the sampler is immediately replaced when a budget exists, so the unused allocation is wasted but harmless — a single call with the no-op path would avoid the alloc

QUESTIONS
1. The base branch (cap-slice2-source-check) already carried `usageInterval`, `tokens`, and `unmetered` on `nativeRunConfig` — is there a companion PR that lands the token-budget flag parsing for `--tokens` and `--usage-interval` on the `native` verb, or was it already landed?
2. The `TestNativeTwoLaunchAccounting` test sets `NOVA_SWARM_PROVIDER_BACKOFF=1s` to pin retry jitter — is this env var the only way the test avoids a race against the default 5-20s jitter, or is there a test hook that should be used instead?
3. The `defect` string is passed from the sampler to the caller through `nativeRunResult` — why is it not rendered by `stoppedSuffix()` alongside the rest of the verdict line, rather than printed as a separate `fmt.Fprintln` after it?

Left owed — I read the full diff (2046 lines of patch) and all production files in full. I did not read the docs files (CLI.md, TESTS.md) in their entirety — only the diff sections — because they are documentation whose correctness is tested by the test assertions, not by my reading of prose.

git status: nothing (clean inside repo/)
HEAD: d576bf6bbabb39068096a97b4560de9b5e245970===FILE=== card-tools22-pre-1712-r4/usage.tsv
job	attempt	started	ended	rc	provider	model	tokens_in	tokens_out	cache_write	cache_read	reasoning	usd
card-tools22-pre-1712-r4	1	2026-09-20T19:29:11Z	2026-09-20T19:50:12Z	0	openrouter	deepseek/deepseek-v4-flash	301487	13784	0	6936832	11353	0.0618
