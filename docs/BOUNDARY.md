# The nova-tools boundary

nova-tools is a small set of independent command-line tools, each one a single
top-level `package main`, that know nothing about any workflow built on them.

The shape and the size are checked by `boundary/boundary_test.go`, which runs
under plain `go test ./...` and needs no network, sockets or real time.

## The allowed shape

The top level holds seven tools: `bus`, `config`, `sandbox`, `secrets`,
`reserve`, `cairn` (each a `package main`) and the fleet plays (`fleet`, Ansible
plus a `doc.go`, no program). Beside them are `tla` (models), `docs`,
`boundary` and `contract` (test-only packages) and `.github`.

The root files are exactly README.md, LICENSE, go.mod, go.sum, .gitignore,
.gitattributes and BUDGET. The root holds README.md only; every other document
(changelog, roadmap, testing notes, specs) goes in `docs/`. Any other top-level
entry fails the test, and so does any symlink.

The test never looks inside `docs/`: it may hold assets (images) and anything
else.

## No library layer

There is no `pkg/`, `internal/` or `cmd/`. A tool is a directory of files in
`package main`. A tool imports nothing from this module: not another tool, not
a shared package. Duplicating a few lines beats a shared layer, because a
shared layer is how 14 thousand lines became 239 thousand.

A tool may exec user-supplied or system binaries but never a sibling nova-*
tool by name: a sibling dependency is a library in disguise.

`go.mod` has no `replace` directive and only the four `tool` entries listed in
the test. A module that any `.go` file imports counts as direct whatever its
`// indirect` comment says.

The only shared things are the standard library and the direct dependencies
listed in `boundary_test.go`. A new direct dependency is added to that list in
the same PR, with the reason written here under "Dependencies".

## The budget rule

`BUDGET` has one line per tool, `name  max-non-test-go-lines`, counting the
`*.go` files that are not `_test.go`. The test fails when a tool is over. Each
budget is the size the tool reached when its spec was settled, plus 50. A tool
over budget means the spec is not minimal; raising one is a PR that says why.

`BUDGET` lines are exactly `name  number`, with no comments or duplicates, and
include `boundary` itself (400).

## Adding a tool

1. Make a new top-level directory with `package main` files.
2. Add its name to the allowed list in `boundary_test.go` (and to `mainTools`).
3. Add a line to `BUDGET`.
4. Keep it to a few hundred lines, with a spec in `docs/`.

## What goes elsewhere

Anything that knows our workflow lives outside this repository: policy belongs
to the sprint; personal tools belong in the owner's own tools repository.
If a tool needs to know a workflow to do its job, it is in the wrong place.

## Dependencies

The direct requirements are: pgx, go-redis, yaml.v3, x/sys, testify.

## History

2026-11-05: 239,138 -> 14,247 lines when the sprint's machinery was removed.
