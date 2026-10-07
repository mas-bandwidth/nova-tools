# nova-ci dogfood — codex (zhi), 2026-10-07

A stranger's pass over `nova-ci` built from `3c658bbc698c`, the commit this pass
fetched; `cmd/nova-ci`, `internal/ci/slowtests` and `internal/scaffold` are
identical at the base tip this attempt carries the report onto, `60f8a299d`, so
the observations below hold there too. I read only the tool's own help
(`nova-ci -h`, `nova-ci help`, `nova-ci <verb> -h`) and its page `docs/CLI.md`,
built it with `go build ./cmd/nova-ci`, and used every
verb at least once with its real flags. The timing verbs ran against a scratch
Go module at `example.com/scratch/mod` holding one package, `beta`, with a fast
test, a 60 ms test and a `SLEEPS:` skip; `functional`, `local`, the two
scaffolds, `bench run` and `github receipt --dry-run` ran against that module or
a throwaway directory. No Redis store was dialled and nothing was fixed here.

## Findings

### 1. `slowtests --allowlist` and `--sleeps` refuse a module-relative column whose first element is not `cmd/`, `internal/` or `tools/` — URGENT

**Command** (run in the scratch module `example.com/scratch/mod`, whose only
package `beta` sits at the module root; `allow.ok` holds one row,
`example.com/scratch/mod/beta<TAB>TestSlow<TAB>0.5<TAB>0.1s@run1`):

    nova-ci slowtests --package-budget 0.2 --allowlist allow.ok < events.json

**Printed** (stderr, exit 2):

    nova-ci slowtests REFUSED: --allowlist allow.ok: line 1: package "example.com/scratch/mod/beta" must be the full module-relative path; run: nova-ci slowtests -h

The same line is printed for the module-relative column `beta` (`package "beta"
must be the full module-relative path`) and for a package row (`-`); `--sleeps`
refuses its row the same way; and the named remedy `nova-ci slowtests -h` still
shows only the `internal/pkg<TAB>test<TAB>seconds<TAB><measured>s@<where>` shape
with the `internal/ci/slowtests` example, never the rule the parser enforces.

The rule is narrower than "not nova-tools". `refuseShortPackage`
(`internal/ci/slowtests/slowtests.go:434-449`) accepts any package column whose
first path element is `cmd`, `internal` or `tools`, and `matches` there names an
event by equality or trailing-path suffix. A module-relative column under one of
those roots in this scratch module is therefore accepted and matched to the
event's import path by its trailing elements; what is refused is a
module-relative column whose first element is none of those three — `beta`
here, or a package under `pkg`. `example.com/scratch/mod/beta` is a
module-qualified path and `beta` is the module-relative path the flag's text
asks for, yet the run refuses both and never says the column must be
module-relative and start under `cmd`, `internal` or `tools`.

**Expected:** `nova-ci help` and `docs/CLI.md` both promise "slowtests and
functional work in any Go module", and the flag's own text calls the first
column the package's "full module-relative path" — `beta` is exactly that for
this module — so the row should raise that package's budget. Where a column is
wrong, the refusal should say what the column wants, and the named `slowtests
-h` should carry the rule instead of a page that omits it.

**Grade:** URGENT

### 2. `new-rule` and `new-verb` treat any module with a `go.mod` as a nova-tools checkout and write into it — URGENT

**Command** (run in a throwaway directory holding only `go.mod` with
`module example.com/scratch/thrown2`):

    nova-ci new-rule scratch-rule

**Printed** (stdout, exit 0; the scratch rule's name is written `<rule>` here
so this report names no path that is not in the tree):

    wrote internal/ci/<rule>_class_test.go
    wrote internal/ci/testdata/<rule>/fixture.txt
    wrote make/rule_<rule>.mk

`nova-ci new-verb --dry-run mytool foo` in the same kind of module plans a
command, its test and a fixture under `cmd/<tool>/testdata/`, and a make
fragment `make/verb_<tool>_<verb>.mk`; a directory with no `go.mod` is refused
("is not a nova-tools checkout (no go.mod)"). The check is the file's presence,
not the module.

**Expected:** `nova-ci help` says "local, new-rule and new-verb need a
nova-tools checkout" and `new-rule -h` says "(local write; needs a nova-tools
checkout)", so a module that is not nova-tools should be refused with that
remedy before three class-rule files land in a stranger's tree; as it stands the
help names a requirement the verb does not check, so the help lies about the
verb and the write lands where the help promised a refusal.

**Grade:** URGENT

## What was not done

- No Redis store or seat was available and starting one is out of scope, so
  `github receipt` ran only with `--dry-run` (fields checked, `ev=-`, exit 0);
  no receipt row was written.
- `bench run --host <bench> --dir ./beta -- go version` answered
  `BENCH-RUN REFUSED: no bench answered: <bench> (ssh exit 255: ...); run: ssh
  <bench> true`, exit 2 — the documented no-bench form; no command ran on a
  bench.
- `local` was not run against a real diff: in the scratch module it refuses
  "not inside a git checkout", and a throwaway git module with a `go.mod` and no
  `Makefile` refuses "has no readable Makefile", both with a remedy; a green or
  red `local` run is unproven.
- `new-rule`/`new-verb` writes above are in throwaway directories inside the job
  directory; nova-tools itself was never written to.

## What worked (no finding, kept short)

`version` prints the one version line; `slowtests --example` prints the built-in
CI-SLOW/CI-LOAD pair at exit 0 and `slowtests -h` exits 0; `slowtests` refuses a
terminal stdin and reports a `SLEEPS:` skip that no `--sleeps` ledger names;
`functional ./beta` answers `CI FUNCTIONAL OK packages=0
reason=no-functional-tag-in-1-dirs` and `functional ./nope` refuses a pattern
matching no package; `local` refuses a directory that is not a git checkout; the
`--json` twin of the slowtests refusal is the same value as the line; `github
receipt --dry-run` checks its fields and dials nothing.

## Gate

    go test -count=1 -timeout 600s ./internal/docs ./internal/ci

    ok  	github.com/mas-bandwidth/nova-tools/internal/docs	1.641s
    ok  	github.com/mas-bandwidth/nova-tools/internal/ci	9.568s

    go test -count=1 -timeout 600s ./internal/docs -run TestDocsTreeIsConsistent

    --- PASS: TestDocsTreeIsConsistent (0.00s)
    ok  	github.com/mas-bandwidth/nova-tools/internal/docs	0.017s

READ 7/10 — the banner, the per-verb helps and the refusal grammar answer a cold
reader fast and the tool refuses to guess on bad flags and missing files; held
down by a row shape and an "any Go module" promise that disagree, a remedy that
does not carry the rule, and two scaffold help lines that name a check the verbs
do not make.

USE 6/10 — every verb ran at least once with its real flags against a scratch
module or a throwaway directory and the refusals were read; held down because
the one real budget flag (`--allowlist`, with `--sleeps`) refuses a root-level
package in the module the banner promises, accepts a column only when its first
element is `cmd/`, `internal/` or `tools/`, and the scaffold verbs write into a
module they say they need not be in.

urgent=2 next=0
