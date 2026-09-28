# deprecated

Tools and modules that are no longer in use. They are kept here for
reference while the living tools are tightened, and this folder is deleted
when that cleanup ends.

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

| tool | why | since |
|---|---|---|
| nova-bus `inbox --decide` | the bus carries messages over git and nothing else (Glenn, 2026-09-27); the classifier routes, their tests and their spec are under `nova-bus-decide/` | 2026-09-27 |
| nova-board | superseded: who is doing what is the job of nova-sprint as it is rebuilt on nova-table (Glenn, 2026-09-27). Moved here: `cmd/nova-board`, `internal/board`, and its spec, command reference and transcript under `docs/` | 2026-09-27 |
| nova-sprint | does not work; to be rewritten on nova-table. Still at `cmd/nova-sprint` and named in `PACKAGES` (never tested by ci.yml's selection; certification and nightly still reach it until it moves). The three reaches that built or ran it are cut: CI's receipt step (`.github/workflows/ci.yml`, "report this run to Redis from the runner") runs only the installed binary and never this tree, `internal/typedrec` no longer builds it, and the `internal/ci` cipriority and seatredis class tests read the live packages. The silent class test (`internal/ci/silent_class_test.go`'s live list still reads its source) and the docs and allowlist rows that name its paths still name it; those are the move PR's own edits | 2026-09-27 |
| nova-card, nova-friend | the sprint's runtime, implemented inside it. Moved here: `cmd/nova-card`, `cmd/nova-friend`, and their command reference and transcripts under `docs/` | 2026-09-27 |
| nova-play, nova-test | new tools with no use on record. Moved here: `cmd/nova-play`, `cmd/nova-test`, and their command reference, usage entry and transcripts under `docs/` | 2026-09-27 |
