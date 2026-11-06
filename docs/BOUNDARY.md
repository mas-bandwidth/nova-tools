# Keeping the tools small

Each tool lives in one top-level directory and runs on its own. It should be
useful without knowing the workflow of the team that built it.

[The boundary tests](../tests/boundary/boundary_test.go) check the repository layout,
package imports, dependencies and line budgets during `go test ./...`.
They need no outside service.

## Layout

The six binaries live in `bus`, `config`, `sandbox`, `secrets`, `reserve` and
`cairn`. Each uses `package main`. The seventh piece, `fleet`, holds Ansible
plays, Go tests and a package comment; it has no command-line program.

Beside them are `docs` and `tests`, and `.github` holds CI. `tests` holds the
checks that span tools: `tests/boundary` (the layout test), `tests/contract` (the
command-line contract), `tests/tla` (the TLC runner and the pinned jar
checksum) and `tests/BUDGET`. Each tool's models stay in `<tool>/tla/`. The
root files are `README.md`, `LICENSE`, `go.mod`, `go.sum`, `.gitignore` and
`.gitattributes`. Git metadata is allowed too.

Each tool's guide is its own `README.md`, with a `SPEC.md` beside it when it
has tests or models to explain. Put shared documentation and images under
`docs/`. Keep build outputs outside
the checkout. An extra top-level entry fails the layout test, even when Git
ignores it.

Top-level symlinks are rejected. The recursive symlink check skips `.git` and
`docs`; it checks the other directories. The boundary tests do not inspect the
contents of `docs/`.

## Independence

Each tool is one directory of `package main` files. A tool must not import another
package from this module. A few repeated lines are preferable to a shared layer
that ties the tools together.

Tools may run a user-selected command or a system helper. They should not call
a sibling nova tool by name to implement their own job. Review this rule in
code; the import check alone cannot detect dependencies through subprocesses.

Workflow policy belongs in nova-sprint or another consumer. Personal tools
belong in their owner's repository.

## Dependencies

[go.mod](../go.mod) has no `replace` directive. Its direct dependencies are
listed in the test:

| Dependency | Use |
|---|---|
| pgx | PostgreSQL access for config. |
| go-redis | Redis access for bus. |
| yaml.v3 | Reading and writing the YAML files of a secrets store. |
| x/sys | Platform system calls. |
| testify | Test assertions. |

A module imported by a Go file counts as direct even if go.mod labels it
indirect. Add a new direct dependency to the test's allowlist in the same PR,
and explain its purpose here. Remove unused direct dependencies from both.

The permitted Go tool directives are errcheck, txtar, deadcode and staticcheck,
as named in `allowedTools`. Adding another requires updating that list too.

## Line budgets

[tests/BUDGET](../tests/BUDGET) gives each Go directory a maximum number of non-test source
lines. Each line contains a directory name and an integer, separated by spaces.
Comments, blank lines and duplicate names are rejected.

The count includes comments and blank lines in `.go` files, but excludes
`_test.go` files. A directory with Go files needs a budget entry. The boundary
and contract test packages have entries as well.

A new tool's budget is its size when its design was settled, plus 50 lines.
If a change exceeds its budget, first look for work the tool no longer needs
to do. A budget increase needs a reason in the same PR. Lower the budget when
code is removed, and remove temporary headroom after the work it covers lands.

## Adding a tool

Create a top-level `package main` directory with a `README.md` guide. Add the
directory to `allowedTop`, `mainTools` and `tests/BUDGET`. Explain the single job it
does and why the existing tools do not already cover it. Keep its dependencies
and examples small enough that a new reader can follow them.
