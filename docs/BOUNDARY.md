# Keeping the tools small

Each tool lives in one top-level directory and runs on its own. It should be
useful without knowing the workflow of the team that built it.

[The boundary tests](../boundary/boundary_test.go) check the repository layout,
package imports, dependencies and line budgets during `go test ./...`.
They need no outside service.

## Layout

The six binaries live in `bus`, `config`, `sandbox`, `secrets`, `reserve` and
`cairn`. Each uses `package main`. The seventh piece, `fleet`, holds Ansible
plays, Go tests and a package comment; it has no command-line program.

Supporting directories are `tla`, `docs`, `boundary`, `contract` and `.github`.
The root files are `README.md`, `LICENSE`, `go.mod`, `go.sum`, `.gitignore`,
`.gitattributes` and `BUDGET`. Git metadata is allowed too.

Put other documentation and images under `docs/`. Keep build outputs outside
the checkout. An extra top-level entry fails the layout test, even when Git
ignores it.

Top-level symlinks are rejected. The recursive symlink check skips `.git` and
`docs`; it checks the other directories. The boundary tests do not inspect the
contents of `docs/`.

## Independence

There is no `pkg`, `internal` or `cmd` layer. A tool must not import another
package from this module. A few repeated lines are preferable to a shared layer
that ties the tools together.

Tools may run a user-selected command or a system helper. They should not call
a sibling nova tool by name to implement their own job. Review this rule in
code; the import check alone cannot detect dependencies through subprocesses.

<<<<<<< HEAD
`go.mod` has no `replace` directive and only the four `tool` entries listed in
the test. A module that any `.go` file imports counts as direct whatever its
`// indirect` comment says.

The only shared things are the standard library and the direct dependencies
listed in `boundary_test.go`. A new direct dependency is added to that list in
the same PR, with the reason written here under "Dependencies".

## The budget rule

`tests/BUDGET` has one line per tool, `name  max-non-test-go-lines`, counting the
`*.go` files that are not `_test.go`. The test fails when a tool is over. Each
budget is the size the tool reached when its spec was settled, plus 50. A tool
over budget means the spec is not minimal; raising one is a PR that says why.

`BUDGET` lines are exactly `name  number`, with no comments or duplicates, and
include `boundary` itself (`tests/boundary`, 400).

### Raised budgets

- `bus` 600 -> 700 (2026-11-05): a named ACL user (`NOVA_BUS_USER`) and the
  cold reviews' fixes, each a rule the spec now states: name and `--re`
  shapes, deduped recipients, empty-subject refusal, VERB STATUS lines
  (`HOOK OK`, `WHO OK`, `PENDING OK`), the holder and budget in `HOOK WAIT`,
  and an unreachable server said plainly with the reconnect log rate-limited.
- `bus` 700 -> 1500 (2026-11-05): the wake is the bus. A hook delivers each
  message into its session and wakes it when idle; v2 had dropped that for
  every harness but Claude Code, which is the bus's whole point. This restores
  v1.6.29 nova-hook's wake and nothing else (no recovery, lanes or friend
  machinery): `--harness codex` (the desktop IPC socket and saved events),
  `grok` (the wake file), `antigravity` (agentapi send-message), the receipts,
  the start-up checks, the /dev/null refusal for `claude`, and the pending
  read past a failed wake (bus/tla/Bus.tla, BusWake.cfg). 1450 lines plus 50.

## Adding a tool

1. Make a new top-level directory with `package main` files.
2. Add its name to the allowed list in `boundary_test.go` (and to `mainTools`).
3. Add a line to `tests/BUDGET`.
4. Keep it to a few hundred lines, with a spec in `docs/`.

## What goes elsewhere

Anything that knows our workflow lives outside this repository: policy belongs
to the sprint; personal tools belong in the owner's own tools repository.
If a tool needs to know a workflow to do its job, it is in the wrong place.
=======
Workflow policy belongs in nova-sprint or another consumer. Personal tools
belong in their owner's repository.
>>>>>>> 961730a02 (docs: human-readable per-tool docs (docs/v2 rebased onto main))

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

[BUDGET](../BUDGET) gives each Go directory a maximum number of non-test source
lines. Each line contains a directory name and an integer, separated by spaces.
Comments, blank lines and duplicate names are rejected.

The count includes comments and blank lines in `.go` files, but excludes
`_test.go` files. A directory with Go files needs a budget entry. The boundary
and contract test packages have entries as well.

A new tool's budget is its size when its spec was settled, plus 50 lines.
If a change exceeds its budget, first look for work the tool no longer needs
to do. A budget increase needs a reason in the same PR. Lower the budget when
code is removed, and remove temporary headroom after the work it covers lands.

## Adding a tool

Create a top-level `package main` directory and a spec under `docs/`. Add the
directory to `allowedTop`, `mainTools` and `BUDGET`. Explain the single job it
does and why the existing tools do not already cover it. Keep its dependencies
and examples small enough that a new reader can follow them.

## History

On 2026-11-05 the repository went from 239,138 lines to 14,247, when the
sprint's machinery moved out.
