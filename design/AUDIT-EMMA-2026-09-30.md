# Audit: Emma's Slice — Dead Code & Duplication (2026-09-30)

Branch `emma/audit-deadcode-emma-slice` at `origin/sprint/foundation` (220b1d091).
Covers assigned packages: `cmd/nova-sprint`, `internal/sprint/*`, `internal/sprint/driver`, `refmodel`, `cmd/nova-work`, `internal/workfile`, `workgh`, `worklang`.
Per Glenn & Rowan (rowan-926fc55ea2f7, rowan-52e0376a1e87): `deprecated/` is excluded; living tree only.

## 1. Classification: KEEP / DELETE / DEPRECATED

| Package / Component | Action | Living Caller (file:line) or Evidence / Rationale |
|---|---|---|
| `cmd/nova-sprint` | KEEP | Living CLI tool: `main.go:38` (`os.Exit(a.run(...))`), exposes live verbs (`run`, `tick`, `play`, `take`, `finish`, `read`, `merge`, `fleet`). |
| `internal/sprint` (living core) | KEEP | `internal/sprint/store/steps.go:11` (`sprint.AddStep`), `steps.go:43` (`sprint.Resolve`), `steps.go:49` (`sprint.Deal`), `internal/sprint/store/tick.go:13` (`sprint.TickParts`). Core steps, round indexes, schema, state, and judgments. |
| `internal/sprint` (event rules: `rules_*.go`, `readplan.go`, `partial.go`, `rule.go`) | DELETE | 0 callers outside dead `internal/sprint/machine` and unit tests. The RT1-RT3 agenda-key dispatch loop is superseded by Glenn's dirty-driven table pumps (`THE-TICK-IS-DIRTY-DRIVEN.md`). |
| `internal/sprint/store` | KEEP | Living Redis backend: `cmd/nova-sprint/main.go:28,204` (`st.Pinned`), `cmd/nova-sprint/run.go:17` (`st.Tick`), `cmd/nova-sprint/verbs.go:37`. |
| `internal/sprint/driver` | KEEP | World simulation harness: `cmd/nova-sprint/play.go:11,74` (`&driver.Driver{...}.Loop()`). Essential for end-to-end multi-member simulation. |
| `internal/sprint/refmodel` | KEEP | Differential testing oracle: `internal/sprint/store/differential_harness_test.go:22` (`refmodel.Abstract`), `differential_fixes_test.go:14`. Pins store step semantics. |
| `internal/sprint/machine` | DELETE | 0 living callers (`machine.Run` has no command caller). Event-driven tick (RT1-RT3) superseded by dirty-driven table pumps (`THE-TICK-IS-DIRTY-DRIVEN.md`). Delete rather than deprecate. |
| `internal/sprint/verbs` | DELETE | 0 callers across the repository. Pure duplicate implementation of CLI verbs over `sprintfn.Twin`/`sprintfn.Redis`. Dead alternative to `cmd/nova-sprint/verbs.go` and `store/steps.go`. |
| `internal/sprint/stepbuild` | DELETE | 0 living callers outside dead `machine` and `verbs`. Lua argv layout and JSON packing obsolete under table pumps and direct Redis store pipelines. |
| `internal/sprint/sprintfn` (twin & client) | DEPRECATED | 0 production callers. Only imported by tests in Stella's slice: `internal/nsprint/fn/sprint_parts_lua_test.go:13`, `profile_sprint_test.go:17`. Park in `deprecated/` until Stella's slice retires `sprint_*.lua`. |
| `internal/sprint/sprintfn` (intents: `twin_intents.go`) | DELETE | 0 living callers. IT14 derive phase (waitfor/needmet/waive) superseded by per-table dirty change queues (`THE-TICK-IS-DIRTY-DRIVEN.md`). |
| `cmd/nova-work` | KEEP | Living CLI tool: `cmd/nova-work/main.go:129` (`main()`), implements `import` and `verify` per `docs/SPEC-WORK-V1.md`. Verified by `cmd/nova-work/main_test.go`. |
| `internal/workfile` | KEEP | Tree model & diff: `cmd/nova-work/import.go:13` (`workfile.EncodeTree`), `cmd/nova-work/verify.go:11` (`workfile.DecodeTree`, `workfile.Diff`), `internal/workgh/fetch.go:12`. |
| `internal/workgh` | KEEP | Read-only GraphQL capture: `cmd/nova-work/main.go:26` (`workgh.GhQuery`), `cmd/nova-work/import.go:14` (`workgh.Fetch`, `workgh.Plan`), `cmd/nova-work/verify.go:12`. |
| `internal/worklang` (s-expr & workset: `worklang.go`, `workset.go`, `units.go`) | KEEP | Living parser & units model: `internal/workfile/decode.go:9,21` (`worklang.Read`), `internal/tokens/units.go:9,55` (`worklang.ParseWorkSet`), `internal/redisq/issue2277_test.go:29`. |
| `internal/worklang` (plan engine: `plan.go`, `expand.go`, `graph.go`, `attempt.go`, `admit.go`) | DEPRECATED | 0 living callers. Dead plan-expansion and admission kernel from parked nova-work v0. Move to `deprecated/internal/worklang` as reference for future `nova-work v2` job graph work. |

## 2. Cross-Tool Duplication Table

| Pattern / Repetition | Living Callers & Occurrences | Proposed Target Module | Recommended Extraction & Rationale |
|---|---|---|---|
| **Redis client setup & auth** | `cmd/nova-sprint/main.go:87-124`, `cmd/nova-table/main.go:68-120`, `cmd/nova-redis/main.go`, `cmd/nova-swarm/member.go:72`, `internal/sprint/store/redis.go` | `internal/redisconn` | Unify dial logic, env resolution (`NOVA_SPRINT_REDIS`, `NOVA_REDIS_ADDR`, `NOVA_SPRINT_REDIS_USER`), and `libraryMatches` check into `redisconn.OpenClient`. |
| **One-line escape & formatting** | `cmd/nova-sprint/main.go:25`, `cmd/nova-work/main.go:25`, `cmd/nova-table/render.go:8`, `cmd/nova-bus/main.go`, `internal/log/log.go:31`, `internal/buildinfo/buildinfo.go:42` | `internal/oneline` | Package exists. Replace lingering ad-hoc manual escapes (`strings.ReplaceAll("\n", " ")`) across tools with standard `oneline.Escape` / `oneline.Field`. |
| **Flag parsing & refusals** | `cmd/nova-sprint/main.go:221-277`, `cmd/nova-work/main.go:221-240`, `cmd/nova-bus/main.go:285`, `cmd/nova-config/main.go:258`, `cmd/nova-check/main.go:195`, `cmd/nova-table/main.go:101` | `internal/cliflag` | Every CLI re-implements `refuse(stderr, verb, what)` returning 2, plus `flag.FlagSet` silent parsing and unexpected arg validation. Extract standard helper `cliflag.Parse` / `cliflag.Refuse`. |
| **Exit code conventions** | `cmd/nova-sprint/main.go:7`, `cmd/nova-work/main.go:8-9`, `cmd/nova-bus/main.go`, `cmd/nova-check/main.go`, `cmd/nova-table/main.go` | `internal/cliflag` (or `exitcode`) | Standardize exit codes (0=success, 1=business/diff refusal, 2=usage/infrastructure failure, 3=configuration drift) into shared constants. |
| **Help banners & routing** | `cmd/nova-sprint/main.go:225`, `cmd/nova-sprint/verbs.go`, `cmd/nova-work/main.go:31-127`, `cmd/nova-bus/main.go`, `cmd/nova-table/main.go` | `internal/cliflag` | Consolidate subverb help dispatch (`help [<verb>]`, `-h`, `--help`) and banner templating currently hand-rolled in every `main.go`. |
| **Atomic file writes & locking** | `cmd/nova-work/main.go:254-273` (`writeFile`), `cmd/nova-bus/reply.go`, `cmd/nova-tokens/dayfile.go`, `internal/config/store.go` | `internal/atomicfile` & `internal/filelock` | Packages exist. `cmd/nova-work` duplicates tempfile+rename pattern (`writeFile`) instead of calling `atomicfile.WriteFile`. Convert to standard `atomicfile`. |
| **Functional test fixtures** | `cmd/nova-sprint/*_functional_test.go`, `cmd/nova-work/main_test.go`, `cmd/nova-swarm/*_functional_test.go`, `internal/sprint/store/*_functional_test.go` | `internal/testutil` (or `testredis`) | Replay harnesses, ephemeral store spinup, container port probing, and test env isolation are re-implemented per package. Unify under `testredis` / `testutil`. |
| **Seat / secrets exec lines** | `cmd/nova-sprint/main.go:115-120`, `cmd/nova-table/main.go`, `cmd/nova-swarm/native.go`, `cmd/nova-ci/main.go:87`, `internal/seatcred/seatcred.go` | `internal/seatcred` | Consolidate ACL user/password environment resolution and `nova-secrets exec` wrapping into `seatcred.ResolveAuth(getenv)`. |
| **Card & brief readers** | `cmd/nova-sprint/card_story.go`, `internal/worklang/units.go`, `internal/tokens/units.go`, `internal/nsprint/card/` | `internal/cardfile` | Parsing `.work` unit sexps, markdown task cards, and brief files is fragmented. Factor out work-unit and card header readers into a shared parser. |
| **JSON output shapes** | `cmd/nova-sprint/verbs.go` (`--json`), `cmd/nova-table/main.go` (`--json`), `cmd/nova-bus/main.go` (`--json`), `cmd/nova-check/main.go` (`--json`) | `internal/cliflag` | Standardize envelope shape for machine-readable CLI output (`{"status":"ok", "data":...}` vs lines) and streaming JSON encoders. |

## 3. Recommended Actions & Next Steps

1. **Immediate PR 1 (Deletions in `internal/sprint`)**:
   - Delete dead parked packages: `internal/sprint/machine`, `internal/sprint/verbs`, `internal/sprint/stepbuild`, and `twin_intents.go` in `sprintfn`.
   - Delete dead event-rule files in `internal/sprint/`: `rules_*.go`, `readplan.go`, `partial.go`, `rule.go` (removes >10,000 lines of dead code).
2. **Immediate PR 2 (Deprecate `worklang` plan engine & `sprintfn`)**:
   - Move `internal/worklang` plan files (`plan.go`, `expand.go`, `graph.go`, `attempt.go`, `admit.go`) to `deprecated/internal/worklang/` for reference.
   - Retain `internal/worklang/` (`worklang.go`, `workset.go`, `units.go`) for `nova-work` and `nova-tokens`.
   - Park `internal/sprint/sprintfn` in `deprecated/` or coordinate removal with Stella's Lua audit.
3. **Consolidation PR 3 (`atomicfile` & `oneline` adoptions)**:
   - Switch `cmd/nova-work/main.go:writeFile` to `atomicfile.WriteFile`.
   - Replace manual escapes with `oneline.Escape`.
