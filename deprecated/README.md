# deprecated

Tools and modules that are no longer in use. They are kept here for
reference while the living tools are tightened, and this folder is deleted
when that cleanup ends.

Nothing deprecated is built, tested, shipped or maintained. A deprecated
test never runs and never stops a build, a landing or a release. Nobody
fixes deprecated code; what is general in it is lifted out into a shared
module first, and the rest is left as it is.

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
| nova-sprint | does not work; to be rewritten on nova-table | 2026-09-27 |
| nova-card, nova-friend | the sprint's runtime, implemented inside it | 2026-09-27 |
| nova-play, nova-test | new tools with no use on record | 2026-09-27 |
