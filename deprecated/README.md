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

Since 2026-09-30 the tools that a living tool replaced are deleted (the
RETIRED table below names each, with what replaced it). What is left is
nova-work, which stays whole because pieces of it may yet be pulled out, and
the files a living package still reads.

| tool | at the end | why | since |
|---|---|---|---|
| nova-work | PARKED: kept whole, to be sorted with Glenn, not deleted | work in progress, not done properly yet (Glenn, 2026-09-27); not part of the 1.0 release. Kept whole here: `cmd/nova-work`, its Lisp kernel `lisp/nova-work` (the tree's only Lisp system), `internal/workclient` and `internal/workreconcile` (imported by nothing else), the tool scripts whose only subject it was (`tools/asdf-carry-verify.sh` and `_test.sh`, `tools/asdf-carry-ci-ok.sh`, `tools/nova-work-parallel-suites.sh` and `_test.sh`), the `internal/ci` class tests that read only the kernel with their allowlists and fixtures (`lispduplicate_class`, `lispkernel_class`, `lisptemppath_class`, `work_kernel_dep`, `work_schema`, `forestwriter_class`) and the wording tests of its spec (`coordinator_handover_spec`, `recovery_spec`, `scheduling_cost_spec`, `spec_work_*`), and its command reference and transcript under `docs/` (`CLI.md` and `TESTS.md` carry its section only). Kept with it because `cmd/nova-work` imports them: `internal/friends`, `internal/landed`, `internal/ghcapture`, and the `internal/ci` events bridge (`events*.go`, which only `nova-work events` ran); `internal/docs/issue2080_test.go` holds nova-work's roadmap row to `internal/ghcapture`'s tests. Its docs stay: `SPEC-WORK.md` with its wording tests (`internal/docs` `issue1594`, `spec_work_record_home`), `nova-work-next.md` (`novaworknext_source`), `nova-work-needs-kernel.md`, `HISTORY.md`, the wire schema `schemas/nova-work-wire-v1.json`, the roadmap forest `roadmaps/nova-work.sexp` with `tools/roadmap-parity.sh` and its test, the v0.16.0 `ROADMAP.md` it is compared with (`internal/docs/issue2065`), and the specs its code cites: `SPEC-DECIDE.md` (the kernel's `decide` protocol), `SPEC-JOBS.md` (the events bridge; `internal/docs/worklang_amendment_test.go`) and `SPEC-LOGS.md` (the structured event lines; `internal/docs/issue2190`). `docs/SPEC-WORKLANG.md` stays under `docs/` because `internal/worklang` is living | 2026-09-27 |
| files a living package reads | kept until that reader is lifted out or deleted | living tests and living output name these paths, so deleting them fails the living package: `docs/SPEC-MERGE.md` (`internal/merge` eff83 test), `docs/SPEC-DECIDE.md` and `docs/decide/` questions, criteria and its one example (`internal/decide` tests read the questions and the criteria they name; `SPEC-DECIDE.md` cites the example), `docs/jev/*.txt` (`internal/nsprint/jev` prompt test), `docs/WORKER-CARDS.md` (the refusal text `nova-swarm` prints cites its practice 17). Their tools are deleted (see RETIRED) | 2026-09-30 |

## RETIRED: deleted 2026-09-30

Each of these was a parked or deprecated tool, module or document set whose
job a living tool now does, or that is retired as unproven with nothing
replacing it. The code is in git history, in the commit before the deletion.

| name | replaced by | deleted |
|---|---|---|
| nova-board (`cmd/nova-board`, `internal/board`, its spec, command reference and transcript) | `cmd/nova-sprint`: the four tables on nova-table (work, readers, merge, fleet), `where`, `card`, `log`, `ask`, with `internal/sprint` | 2026-09-30 |
| nova-sprint, the old tool (`cmd/nova-sprint`, `SPEC-SPRINT.md`, `nova-sprint/`, its tests and transcripts, `internal/nsprint` tests) | the rebuilt `cmd/nova-sprint` on `internal/sprint` (`internal/sprint/machine` is the tick, `internal/sprint/store` the table binding). The living verbs `take`, `finish`, `read`, `accept`, `rework`, `merge`, `ci`, `inbox --wait`, `play --simulation` do the old tool's runtime, review and landing | 2026-09-30 |
| nova-card (`cmd/nova-card`) | `cmd/nova-sprint` `card`, `take`, `finish`: the card is a row of the work table | 2026-09-30 |
| nova-friend (`cmd/nova-friend`, `docs/nova-friend/`) | nothing as a tool: friends are peers and readers over `cmd/nova-bus`, never rows in the fleet table; retired | 2026-09-30 |
| nova-play (`cmd/nova-play`, `SPEC-PLAY.md`) | nothing: a tool with no use on record; `cmd/nova-sprint` `play` is the sprint simulation and shares only the name; retired as unproven | 2026-09-30 |
| nova-test (`cmd/nova-test`, `SPEC-TEST.md`, `TEST-DURATIONS.md`, `tools/testdur`) | `cmd/nova-ci` (`slowtests`, the unit budgets in `docs/SPEC-CI.md`) and `tools/functionalrun`; retired | 2026-09-30 |
| nova-decide (`cmd/nova-decide`, `internal/jevcalib`, `internal/decide/redtriage`) | nothing: the Jev client is retired as unproven; `internal/decide` and the Jev prompts stay for the living packages that read them (above) | 2026-09-30 |
| nova-merge (`cmd/nova-merge`, `internal/merge/bench`, `internal/friendread`; `docs/SPEC-MERGE.md` is kept above) | `cmd/nova-sprint` `merge` and the lander, with the GitHub merge queue | 2026-09-30 |
| nova-review (`cmd/nova-review`, `internal/review`, `internal/prereview`, `SPEC-REVIEW.md`) | `cmd/nova-sprint` `read` (`--begin`, `--ok`, `--broken`) and the readers table | 2026-09-30 |
| nova-wake (`cmd/nova-wake`, `internal/dispatch`) | the tick-end wake: `cmd/nova-sprint` `inbox --wait` blocks on the tick-end line, one wake per tick with the whole inbox (`internal/sprint/inbox.go`, `internal/sprint/machine/tick.go`); `internal/wake` is deleted | 2026-09-30 |
| nova-post (`cmd/nova-post`, `internal/post`, `SPEC-OUTBOUND.md`) | nothing: the outbound gate released nothing and its receiver never ran; retired as unproven | 2026-09-30 |
| nova-bus `inbox --decide` (`nova-bus-decide/`) | `cmd/nova-bus` carries messages over git and nothing else | 2026-09-30 |
| nova-swarm's old parked docs (`WORKER-CARDS.md` kept above; `PROPOSAL-SWARM-BATCH-*.md`, `drafts/`, `SWARM-PROFILES-2026-09-15.md`, `SPEC-SWARM-PROFILES.md` with `internal/swarm/effort_spec_test.go`, `SPEC-FLEET-KUBE.md` with its five `internal/docs` tests, `PROPOSAL-SCHEDULING-COST.md`, `PROPOSAL-CAPACITY.md`) | `cmd/nova-swarm` and `docs/SPEC-SWARM.md` | 2026-09-30 |
| the sprint's and nova-pulse's docs (`SPEC-CARD.md`, `LESSONS-ARCHIVE.md`, `roadmaps/sprint-fixes-2026-09-22.sexp`, `fixtures/quack-*.txt`, `EXEMPLARS.md`, `PIT-STOP.md`, `notes-spec.md`, `spec-pulse/`, `USAGE.md`) | `cmd/nova-sprint` and `docs/SPEC-SPRINT.md` | 2026-09-30 |
| unbuilt specs and dated records (`SPEC-CHAT.md`, `SPEC-LOCAL.md`, `BOX-LOCAL.md`, `NEXT-TOOLS.md`, `PORTFOLIO.md`, `BENCH-STANDARD-WINDOWS.md`, `RELEASE-NOTES-2026-09-18.md`, `TEST-FLAKES.md`, `TEST-DURATIONS.md`, `SPEC-BOARD.md`) with their `internal/docs` wording tests | nothing: nobody builds them (`docs/BENCH-STANDARD-WINDOWS.md` is the WSL2 standard) | 2026-09-30 |

The Jev key, `JEV_API_KEY`, stays sealed in the seats that hold it for when a Jev client returns; living code still reads it (`internal/decide`).
