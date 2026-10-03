# SPEC-TOOLWORK — branch hygiene, and tests that execute documents

Two mechanical guarantees the toolchain holds over its own tree, each with no
person and no model standing between a change and the answer:

- **Hygiene**: a branch is clean, by four checks that read no prose.
- **Tests that execute documents**: every example a stranger pastes from the
  documentation is run by a test and compared with what the document says.

## Hygiene

`nova-check hygiene --repo <dir> --base <ref> --head <ref> --identity "Name <email>[,…]" [--paths <glob>[,…]] [--kind <kind>]`
checks `<base>..<head>` and prints one `HYGIENE FINDING reason=<token> at=<where>: <why>`
line per finding, then `HYGIENE OK` or `HYGIENE NO` with the base, head, paths and
finding count. It exits 0 clean, 1 with findings, 2 when it could not run. It decides
nothing.

1. **One package, one entry point.** The checks are `internal/hygiene.Check`, and
   `nova-check hygiene` is its command. A second copy of these rules would be a second
   definition of clean. A check that could not run is exit 2, never a clean answer.
2. **`identity`.** Every commit in the range has author **and** committer in the
   identity set, and none is a merge commit; anything else is reason `identity` at the commit's sha12.
   `--identity` is required and repeatable with commas: a range checked against nobody
   would admit anybody, so there is no default identity and no reading of the
   repository's own git config.
3. **`out-of-path`.** When `--paths` is given, it is a list of repository-relative
   globs: no `..`, no absolute path, no bare `**`, at most 8 entries. Every path in
   `git diff --name-only <base>..<head>` matches one, and a rename counts on both
   sides. Without `--paths` the diff is not bounded: the check is skipped and the
   output says so with `paths=-`.
4. **`stray-file`.** No added file matches the stray list (`internal/hygiene/stray.txt`:
   `RESULT.md`, `PROMPT.md`, `scratch/**`, `*.log`, `*.orig`, `*.rej`, `*.test`,
   `*.out`, `.DS_Store`, editor swap files and the rest); no added file is over 1 MiB;
   no changed file has a mode other than `100644` or `100755`, is a symlink or a
   submodule; and no changed file holds a conflict marker. The list is one embedded
   data file, and an exception in it names the kind it is for.
5. **`secret`.** Every added line is matched against key SHAPES held as one embedded
   data file (`internal/hygiene/keyshapes.txt`: PEM private-key headers,
   `AGE-SECRET-KEY-1`, the forge's token prefixes, the provider key prefixes), never
   against a key's value: the check holds no key and reads none. A match is
   reason `secret` at `<path>:<line>`, and the matched text is never printed, only its path,
   line and shape name.
6. **A kind is declared by the tool.** The kinds are the name set in
   `internal/hygiene/kinds.txt`. `--kind` names one of them or is refused with the
   list; there is no default kind. A kind unlocks only the stray exceptions that name
   it, and nothing a branch contains widens it. The third field of a row is `gated`
   or `ungated`. A missing field or any other value is `gated`. The header lint
   reads that field when it decides whether `TEST: none` is a declaration. The
   field selects no command, and a name the file does not hold is not declared
   and is not `ungated`.

**Tests:** `internal/hygiene` and `cmd/nova-check` hold each check red on a fixture
repository: a foreign committer, a merge commit, a rename on both sides, `RESULT.md`
in the diff, a conflict marker, mode `100600`, a file over one mebibyte, a symlink and
a submodule, a secret shape whose text never appears in the output, a `--paths` list
with `..` or a bare `**`, and an undeclared `--kind`.

## Tests that execute documents

Every command carries a `### First run` transcript in its `## <tool>` section of
`docs/TESTS.md`. These rules make each one executed, not merely present.

1. **Every transcript is executed, line for line, in order.** For every `## <tool>`
   section, a test in `cmd/<tool>` runs every `$` line of every fenced block under
   `### First run` and compares each command's **whole** output with the block under
   it: same number of lines, same lines, same order, in-process, in one temporary
   directory for the sitting.
2. **One comparator, in one place.** `onboarding.CompareTranscript(doc, got, volatile)`
   is the only comparison a `firstrun_test.go` makes. Values are compared **as
   written**; the only values matched by shape are the run-owned fields of one shared
   table, `onboarding.Volatile` (`at=`, `took=`, `created=`, a temporary path, a fresh
   sha, a name a recorded fixture carries that the document writes as the reader's
   variable, and the stamp on the `branch=` a nova-secrets seal or seat inject commits on).
   A test may name a field from that table and may not invent one.
3. **The class test asserts execution, not existence.**
   `TestEveryTranscriptIsExecutedLineForLine` walks `docs/TESTS.md`'s sections and
   fails for any tool whose package has no test calling the comparator on that tool's
   section, and for any `firstrun_test.go` that compares any other way. Its allowlist
   is the sections not yet executed, each named with the issue that owes it, and it is
   shrink-only in both directions.
4. **The comparator is seen red three ways.** Its own tests carry three one-edit
   seeds, a line dropped, a value altered and a line moved, and each is red.
5. **A section that one platform cannot reproduce says which.** A `Platform:
   <goos>[,<goos>]` line under the `## <tool>` heading; elsewhere the test is a named
   skip, and the class test requires every platform named to be a leg `ci.yml` runs,
   so a skipped transcript is still executed somewhere.
6. **The other documents a stranger pastes from are counted, and the count only
   shrinks.** Every `$ ` line in a fenced block of `README.md`, `docs/USAGE.md` and
   `docs/CLI.md`, and every `example:` line of every `help`, is either executed by a
   test through the same comparator (`internal/ci/testdata/compared_examples.txt`) or
   listed in `internal/ci/testdata/unexecuted_examples.txt` with its reason. A new
   unexecuted example fails the class test on the change that adds it.

**Tests:** `internal/onboarding` (a dropped line, a moved line, an altered value, a
volatile field outside the table), `internal/ci` (every transcript executed line for
line, the platform line names a CI leg, the unexecuted examples only shrink) and
`internal/docs` (every transcript section executed).
