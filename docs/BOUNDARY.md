# The nova-tools boundary

nova-tools is a small set of independent command-line tools, each one a single
top-level `package main`, that know nothing about any workflow built on them.

The shape and the size are checked by `boundary/boundary_test.go`, which runs
under plain `go test ./...` and needs no network, sockets or real time.

## The allowed shape

The top level holds the tools (`bus`, `config`, `sandbox`, `secrets`, `slot`,
`fleet`), `tla` (models), `docs`, `boundary` (the test), `.github`, and the
root files named in the test. Any other top-level entry fails the test.

## No library layer

There is no `pkg/`, `internal/` or `cmd/`. A tool is a directory of files in
`package main`. A tool imports nothing from this module: not another tool, not
a shared package. Duplicating a few lines beats a shared layer, because a
shared layer is how 14 thousand lines became 239 thousand.

The only shared things are the standard library and the direct dependencies
listed in `boundary_test.go`. A new direct dependency is added to that list in
the same PR, with the reason written here under "Dependencies".

## The budget rule

`BUDGET` has one line per tool, `name  max-non-test-go-lines`, counting the
`*.go` files that are not `_test.go`. The test fails when a tool is over. A tool
over budget means the spec is not minimal. Raise a budget only in a PR that
says why, and lower it when code is removed.

`bus` and `sandbox` carry roughly 350 extra lines of headroom for open pull
requests. Those two budgets come down after the PRs land.

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
