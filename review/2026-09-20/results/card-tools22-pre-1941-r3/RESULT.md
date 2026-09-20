RESULT tools22-pre-1941-r1 sha=5298f6be12ea — a pre-read of mas-bandwidth/nova-tools#1941 at head c05d6a4913e6
PREREAD 1941 claims=10 proven=9 unproven=1 defects=0 high=0
PR 1941 HEAD c05d6a4913e6eb8b422aaf503f6414d1d7791ffd BASE dev MERGE-BASE 4a93fd75a3b030ee36c4a16fcd55f71aa4379549 BEHIND 15
FILES 11 production, 2 test LINES +481 -21
---

## Claims

1. nova-pulse fill accepts --stagger <d> (default 3s) and --max-inflight <n> (default 32) as CLI flags — PROVEN-BY cmd/nova-pulse/fill.go:64-65 Two f.fs.Duration and f.fs.Int calls wired to FillInput fields.

2. nova-swarm batch respects --stagger, enforcing a minimum gap between two launches (remote-run or bench-pull) naming the same bench via swarm.BenchStagger — PROVEN-BY internal/swarm/batch.go:627 stagger.Wait(c.bench) on the remote branch and internal/swarm/batch.go:751 stagger.Wait(c.bench) on the pullFromBench branch.

3. nova-pulse harvest bench form respects --stagger, pacing two fetches over ssh://<bench> including the mark-harvested touches that follow — PROVEN-BY internal/pulse/harvestbench.go:207 stagger.Wait(in.Bench) before gitIn(fetch).

4. nova-pulse launch threads --stagger to nova-swarm batch via the --stagger CLI flag — PROVEN-BY internal/pulse/launch.go:222-224 if block appending --stagger to cmd.Args.

5. nova-swarm native takes a uniform-random 0-2s pause (NativeStartJitter) immediately before cmd.Start(), only in live runs — PROVEN-BY cmd/nova-swarm/native.go:500-507 cfg.jitter != nil guard calling sleep(cfg.jitter()).

6. nova-swarm native sets jitter/sleep only in cmdNative (not hand-built test configs), so tests start immediately — PROVEN-BY cmd/nova-swarm/native.go:1780-1781 only the cmdNative path assigns swarm.NativeStartJitter and time.Sleep.

7. fill holds cards past --max-inflight as FILL HELD and keeps them under --ready for next tick — PROVEN-BY internal/pulse/fill.go:331-336 liveRouteCount guards below cap; internal/pulse/fill.go:337 continue skips launch.

8. NovaStartJitter draws from the same randBelow cryptographic source ProviderRetryDelay trusts — UNPROVEN stagger.go:63 implements randBelow(int(2*time.Second)) but no test asserts the dependency.

9. Zero --stagger or zero --max-inflight produces no-op behavior (every card launches, no gaps) — PROVEN-BY internal/swarm/stagger.go:14 Wait returns immediately when gap <= 0.

10. Docs/CLI.md documents --stagger for launch, fill, and harvest forms, --max-inflight for fill, and native jittered start — PROVEN-BY docs/CLI.md:1341-1343 (launch), docs/CLI.md:1562-1578 (fill), docs/CLI.md:1698-1703 (harvest), docs/CLI.md:2293-2300 (batch/native).

## Defects

No defects found.

## Questions

1. liveRouteCount (internal/pulse/fill.go:537) scans every launched marker on every tick to count inflight cards per route. At scale (hundreds of launched cards), is this readdir+readfile loop acceptable, or should fill maintain a concurrent map as its own index?

2. BenchStagger.Wait records s.last[bench] = now() _after_ sleeping — meaning a second Wait starting exactly at gap boundary sees last == the first call's end time and sleeps the full gap again. Is this intentional (the gap is always enforced from Wait-return to Wait-return), or should it record before the sleep so consecutive rapid returns don't double-sleep?

3. NativeStartJitter uses randBelow(int(2*time.Second)), which casts a nanosecond duration to int (losing sub-microsecond precision). Is microsecond-level jitter adequate for the MaxStartups problem, or should the jitter be specified in integer milliseconds explicitly?

4. The stagger key for fill is RouteKey("fill", bench) (internal/pulse/fill.go:332). If two different pulses run fill concurrently against the same bench, each has its own --launched directory view: does this mean two fills can simultaneously exceed the combined capacity, and is that the desired independence?

5. Batch's BatchInput now carries Stagger alongside the existing MaxInflight gate. Are these intended to be complementary (per-route concurrency cap _plus_ per-bench pacing), or is there a case where someone would want one without the other and that distinction should be documented?

## Left owed

Did not run `go build` or `go vet` on the packages (no compilation artifacts created). Did not read the full history of internal/swarm/inflight.go or related concurrency primitives beyond what the diff showed. Did not inspect whether randBelow's CSPRNG provider is seeded deterministically in tests (which could make NativeStartJitter deterministic or panic-dependent on environment).

git status --short (empty — working tree clean)
git rev-parse HEAD
d576bf6bbabb39068096a97b4560de9b5e245970
