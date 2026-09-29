# deprecated

Tools and modules that are no longer in use. They are kept here for
reference while the living tools are tightened, and this folder is deleted
when that cleanup ends, except what is PARKED (below).

Nothing deprecated is built, tested, shipped or maintained. A deprecated
test never runs and never stops a build, a landing or a release. Nobody
fixes deprecated code; what is general in it is lifted out into a shared
module first, and the rest is left as it is. The class tests that walk the
source tree skip this folder.

Glenn, 2026-09-27: "Tests do not run for deprecated tools and modules."
"We keep them in deprecated only as reference as we focus on living tools."

## Two ways something is deprecated

1. **Moved here.** This folder is its own Go module (`go.mod`), so the
   toolchain and CI never list anything under it.
2. **Named in `PACKAGES`.** A package that living tools still import cannot
   move yet. It is named in [`PACKAGES`](PACKAGES), CI never selects it, and
   it moves here once nothing living imports it.

## What is deprecated

Two kinds of row. RETIRED is reference only and is deleted when the cleanup
ends. PARKED is work in progress kept whole until it can be done properly: it
is not deleted at the end, it is sorted with Glenn (Glenn, 2026-09-27: "WIP
tools (like nova work, sprint etc) in deprecated until we can do them
properly"). Both are equally untested, unbuilt and unshipped while they are
here.

| tool | at the end | why | since |
|---|---|---|---|
| nova-bus `inbox --decide` | RETIRED: deleted | the bus carries messages over git and nothing else (Glenn, 2026-09-27); the classifier routes, their tests and their spec are under `nova-bus-decide/` | 2026-09-27 |
| nova-board | RETIRED: deleted | superseded: who is doing what is the job of nova-sprint as it is rebuilt on nova-table (Glenn, 2026-09-27). Moved here: `cmd/nova-board`, `internal/board`, and its spec, command reference and transcript under `docs/` | 2026-09-27 |
| nova-sprint | PARKED: kept whole, to be sorted with Glenn, not deleted | does not work; to be rewritten on nova-table. Moved here: `cmd/nova-sprint`, `docs/SPEC-SPRINT.md`, its verb guide `docs/nova-sprint/`, and its command reference and first-run transcript under `docs/`. `internal/nsprint`, `internal/sprinttable` and `internal/sprintline` stay in the root module, named in `PACKAGES`, because living tools import its `keep` packages (`nova-table`, `nova-config`); CI's selection never tests the rest. Its ci cards went with it: ci.yml's `NOVA_CI_CARDS` gates are retired. `fn load`, the verb that loaded the `nova_sprint` function library on a bare store, is not shipped; `nova-config apply` loads it only when the store has none | 2026-09-27 |
| nova-card, nova-friend | RETIRED: deleted | the sprint's runtime, implemented inside it. Moved here: `cmd/nova-card`, `cmd/nova-friend`, and their command reference and transcripts under `docs/` | 2026-09-27 |
| nova-play, nova-test | RETIRED: deleted | new tools with no use on record. Moved here: `cmd/nova-play`, `cmd/nova-test`, and their command reference, usage entry and transcripts under `docs/` | 2026-09-27 |
| nova-work | PARKED: kept whole, to be sorted with Glenn, not deleted | work in progress, not done properly yet (Glenn, 2026-09-27); not part of the 1.0 release. Moved here whole: `cmd/nova-work`, its Lisp kernel `lisp/nova-work` (the tree's only Lisp system), `internal/workclient` and `internal/workreconcile` (imported by nothing else), the tool scripts whose only subject it was (`tools/asdf-carry-verify.sh` and `_test.sh`, `tools/asdf-carry-ci-ok.sh`, `tools/nova-work-parallel-suites.sh` and `_test.sh`), the four `internal/ci` class tests that read only the kernel with their two allowlists and fixtures, and its command reference and transcript under `docs/`. Moved with it because nothing living imported them once it left: `internal/friends`, `internal/landed`, `internal/ghcapture`, the `internal/ci` events bridge (`events*.go`, which only `nova-work events` ran) and `internal/docs/issue2080_test.go` (it held nova-work's roadmap row to `internal/ghcapture`'s tests). `docs/SPEC-WORKLANG.md` stays under `docs/` because `internal/worklang` is living | 2026-09-27 |
| nova-decide | PARKED: kept whole, to be sorted with Glenn, not deleted | parked because it is not proven yet (Glenn, 2026-09-27: "Decide is Jev stuff. I want us to use Jev in future to save tokens."); the Jev client, to return on its own when its value is measured. Moved here: `cmd/nova-decide`, `internal/jevcalib`, `internal/decide/redtriage`, and its command reference and first-run transcript under `docs/`. `internal/decide` stays in the root module, named in `PACKAGES` with the living tools that import it | 2026-09-27 |
| nova-merge | PARKED: kept whole, to be sorted with Glenn, not deleted | not proven: landing goes through the GitHub merge queue; a dev-to-main promotion verb returns when it is measured (Glenn, 2026-09-27). Moved here: `cmd/nova-merge`, `internal/merge/bench`, `internal/friendread`, and its command reference, usage entry and first-run transcript under `docs/`. `internal/merge` stays in the root module, named in `PACKAGES` with the living test that imports it | 2026-09-27 |
| nova-review | PARKED: kept whole, to be sorted with Glenn, not deleted | not proven: the packet/verdict flow was the sprint's review path; the mutate verb may be lifted alone later (Glenn, 2026-09-27). Moved here: `cmd/nova-review`, `internal/review`, `internal/prereview`, and its command reference and first-run transcript under `docs/` | 2026-09-27 |
| nova-wake | PARKED: kept whole, to be sorted with Glenn, not deleted | not proven: the serve loop wedged silently for five days on a bus-cursor bound; friends still fall asleep; returns when its wake path is measured end to end (Glenn, 2026-09-27). Moved here: `cmd/nova-wake`, `internal/dispatch`, `docs/SPEC-WAKE.md`, and its command reference, usage entry and first-run transcript under `docs/`. `internal/wake` stays in the root module, named in `PACKAGES` with the living code that imports it | 2026-09-27 |
| nova-post | PARKED: kept whole, to be sorted with Glenn, not deleted | not proven; the outbound gate has released nothing and the webhook receiver has never run; returns when its fleet contract is settled and its gate is bound to the approver's lane. Moved here: `cmd/nova-post`, `internal/post` (with `hook` and `issue`), `docs/SPEC-OUTBOUND.md`, and its command reference and first-run transcript under `docs/`. `internal/ghevent` and `internal/ghevent/wire` stay living: `internal/cireceipt` (nova-ci) imports the first, `internal/gh` the second | 2026-09-27 |
| docs of parked and deleted tools | RETIRED: deleted, except what a PARKED tool above keeps | their subject is a parked or deleted tool, a design nobody builds, or a dated record; living docs describe only the 1.0 tools. Moved here under `docs/`: nova-work's `nova-work-needs-kernel.md` and `HISTORY.md`; nova-swarm's `WORKER-CARDS.md`, `PROPOSAL-SWARM-BATCH-*.md`, `drafts/` and `SWARM-PROFILES-2026-09-15.md`; the sprint's `SPEC-CARD.md`, `LESSONS-ARCHIVE.md`, `roadmaps/sprint-fixes-2026-09-22.sexp` and quack run fixtures `fixtures/quack-*.txt`; nova-pulse's `EXEMPLARS.md`, `PIT-STOP.md` and `notes-spec.md` (from the root); nova-decide's Jev prompts `jev/`; nova-friend's guide `nova-friend/`; the unbuilt nova-chat and nova-local specs `SPEC-CHAT.md`, `SPEC-LOCAL.md` and `BOX-LOCAL.md`; and the dated records `RELEASE-NOTES-2026-09-18.md` and `TEST-FLAKES.md` | 2026-09-28 |
| wording-only guards of parked docs | RETIRED: deleted, except what a PARKED tool above keeps | a test that only checks a parked doc's wording guards no product. Moved here with their docs: nova-work's `SPEC-WORK.md` (and its `internal/ci` wording tests `coordinator_handover_spec`, `recovery_spec`, `scheduling_cost_spec`, `spec_work_gate_and_report`, `spec_work_rate_convergence`, `spec_work_tokens_join`, `spec_work_widening`, and `internal/docs` `issue1594`, `spec_work_record_home`), `nova-work-next.md` (`novaworknext_source`), the wire schema `schemas/nova-work-wire-v1.json` (`internal/ci/work_schema`), the roadmap forest `roadmaps/nova-work.sexp` with `tools/roadmap-parity.sh`, its test and the forest writer class test (`internal/ci/forestwriter_class` and its allowlist), the v0.16.0 `ROADMAP.md` (`internal/docs/issue2065`), `NEXT-TOOLS.md` (`nexttools_board`), `PORTFOLIO.md` (`portfolio`), `PROPOSAL-CAPACITY.md` (`capacity_proposal`), `PROPOSAL-SCHEDULING-COST.md`, the Kubernetes fleet spec `SPEC-FLEET-KUBE.md` (`internal/docs` `issue2226`, `issue2229`, `issue2232`, `issue2233`, `issue2236`) nova-swarm's `SPEC-SWARM-PROFILES.md` (`internal/swarm/effort_spec`), the test-duration record `TEST-DURATIONS.md` with the tool that wrote it and its record test (`tools/testdur`), the unit budgets living in `nova-ci slowtests` and docs/SPEC-CI.md; the pull job system `SPEC-JOBS.md` (`nova-swarm pull`, `nova-pulse cut/fill/watch/width`, `nova-merge react/queue`, the admission kernel no living verb calls) with `internal/docs/worklang_amendment_test.go`; nova-test's `SPEC-TEST.md` (`internal/docs` `novatest`, `novatest_verbs`, `issue2206`, `issue2208`); the logging-stack design `SPEC-LOGS.md` (Loki, queries, alerts, parked emitters; `internal/docs/issue2190`), docs/SPEC-LOGS.md keeping what `internal/log` does; and the native Windows bench standard `BENCH-STANDARD-WINDOWS.md` (`internal/docs/windows_bench_standard`), docs/BENCH-STANDARD-WINDOWS.md being the WSL2 standard | 2026-09-28 |

The Jev key, `JEV_API_KEY`, stays sealed in the seats that hold it for when nova-decide returns; living code still reads it (`internal/decide`).
