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
| nova-sprint | PARKED: kept whole, to be sorted with Glenn, not deleted | does not work; to be rewritten on nova-table. Still at `cmd/nova-sprint` and named in `PACKAGES` (never tested by ci.yml's selection; certification and nightly still reach it until it moves). The three reaches that built or ran it are cut: CI's receipt step (`.github/workflows/ci.yml`, "report this run to Redis from the runner") runs this tree's `nova-ci github receipt` and no nova-sprint, `internal/typedrec` no longer builds it, and the `internal/ci` cipriority and seatredis class tests read the live packages. The silent class test (`internal/ci/silent_class_test.go`'s live list still reads its source) and the docs and allowlist rows that name its paths still name it; those are the move PR's own edits | 2026-09-27 |
| nova-card, nova-friend | RETIRED: deleted | the sprint's runtime, implemented inside it. Moved here: `cmd/nova-card`, `cmd/nova-friend`, and their command reference and transcripts under `docs/` | 2026-09-27 |
| nova-play, nova-test | RETIRED: deleted | new tools with no use on record. Moved here: `cmd/nova-play`, `cmd/nova-test`, and their command reference, usage entry and transcripts under `docs/` | 2026-09-27 |
| nova-work | PARKED: kept whole, to be sorted with Glenn, not deleted | work in progress, not done properly yet (Glenn, 2026-09-27); not part of the 1.0 release. Moved here whole: `cmd/nova-work`, its Lisp kernel `lisp/nova-work` (the tree's only Lisp system), `internal/workclient` and `internal/workreconcile` (imported by nothing else), the tool scripts whose only subject it was (`tools/asdf-carry-verify.sh` and `_test.sh`, `tools/asdf-carry-ci-ok.sh`, `tools/nova-work-parallel-suites.sh` and `_test.sh`), the four `internal/ci` class tests that read only the kernel with their two allowlists and fixtures, and its command reference and transcript under `docs/`. Moved with it because nothing living imported them once it left: `internal/friends`, `internal/landed`, `internal/ghcapture`, the `internal/ci` events bridge (`events*.go`, which only `nova-work events` ran) and `internal/docs/issue2080_test.go` (it held nova-work's roadmap row to `internal/ghcapture`'s tests). `docs/SPEC-WORK.md`, `docs/SPEC-WORKLANG.md`, `docs/nova-work-*.md`, `docs/roadmaps/nova-work.sexp`, `docs/schemas/nova-work-wire-v1.json` and `ROADMAP.md` stay under `docs/` and the root | 2026-09-27 |
| nova-swarm | RETIRED: deleted | retired for now: unfinished machinery whose pool dispatcher is reachable from no command, to be rewritten on nova-table with the sprint (Glenn, 2026-09-27). Moved here: `cmd/nova-swarm`, and its command reference, usage entry and first-run transcript under `docs/`. `internal/swarm` stays in the root module, named in `PACKAGES` with the living code that imports it | 2026-09-27 |
| nova-decide | PARKED: kept whole, to be sorted with Glenn, not deleted | parked because it is not proven yet (Glenn, 2026-09-27: "Decide is Jev stuff. I want us to use Jev in future to save tokens."); the Jev client, to return on its own when its value is measured. Moved here: `cmd/nova-decide`, `internal/jevcalib`, `internal/decide/redtriage`, and its command reference and first-run transcript under `docs/`. `internal/decide` stays in the root module, named in `PACKAGES` with the living tools that import it | 2026-09-27 |
| nova-merge | RETIRED: deleted | not proven: landing goes through the GitHub merge queue; a dev-to-main promotion verb returns when it is measured (Glenn, 2026-09-27). Moved here: `cmd/nova-merge`, `internal/merge/bench`, `internal/friendread`, and its command reference, usage entry and first-run transcript under `docs/`. `internal/merge` stays in the root module, named in `PACKAGES` with the living test that imports it | 2026-09-27 |
| nova-review | RETIRED: deleted | not proven: the packet/verdict flow was the sprint's review path; the mutate verb may be lifted alone later (Glenn, 2026-09-27). Moved here: `cmd/nova-review`, `internal/review`, `internal/prereview`, and its command reference and first-run transcript under `docs/` | 2026-09-27 |

The Jev key, `JEV_API_KEY`, stays sealed in the seats that hold it for when nova-decide returns; living code still reads it (`internal/decide`).
