# nova-work dogfood, 2026-10-06

Friend: opencode. Base: `sprint/mechanical-2026-10-02`. The tool was read cold only from
its own help (`nova-work -h`, `nova-work help`, `nova-work help import`,
`nova-work help verify`, `nova-work version -h`) and from its docs pages
(`docs/CLI.md#nova-work`, `docs/SPEC-WORK-V1.md`, `docs/SPEC-WORKLANG.md`), then
used for real on a minimal tree in a scratch directory (`$SB`). gh was the card
shim, which refuses network in a card (`no GitHub CLI in a card`), so the live
GitHub verbs were exercised through their refusals and the `--against` offline
path was exercised end to end. Every verb was invoked at least once: `version`,
`import --dry-run`, `import --out` (new, existing, mutually-exclusive with
`--dry-run`), `verify --tree --against` (equal tree and drifted tree), and the
refusals for missing/bad flags and a missing/malformed tree.

`$SB` is a scratch directory made for this run. `$a` and `$b` are two copies of
the minimal tree `verify -h` prints; `$b` had `:archived true` so the diff is one
`DRIFT` line. Tool output below elides the scratch path to `$SB`.

## Findings

1. `nova-work import --org foo --repo foo/bar --out $SB/a.lisp` (a.lisp pre-existing)
   ```
   IMPORT REFUSED: --out $SB/a.lisp exists; pass --replace to replace it: nova-work import --org foo --out $SB/a.lisp --replace; run: nova-work help
   ```
   Expected: the remedy re-runs the same call with `--replace` and keeps the caller's
   `--repo foo/bar`. What it does: the remedy drops `--repo foo/bar`, so pasting it
   runs `nova-work import --org foo --out a.lisp --replace` — a whole-organization
   import — instead of the single repository the caller named. The identical
   `--repo`-dropping remedy is emitted by the mutually-exclusive case too:
   `nova-work import --org foo --repo foo/bar --out $SB/a.lisp --dry-run` prints two
   `REFUSED` lines and both remedies omit `--repo foo/bar`.
   Grade: URGENT — the prescribed next command, if pasted, imports the entire org
   rather than the named repo: a wrong result from following the tool's own
   prescription, and the spec says a remedy keeps every caller value (one shell word).

2. `nova-work import --dry-run` (no `--org`)
   ```
   IMPORT REFUSED: --org is required; it wants the organization whose repositories are read, as GitHub spells it, refusing to guess; run: nova-work help
   IMPORT REFUSED: --dry-run with no --repo reads every repository of --org , up to --max-calls 1500 calls; name one repository and run: nova-work import --org '' --repo ''/<name> --dry-run; run: nova-work help
   ```
   Expected: one refusal — `--org` is required. What it does: the dry-run-without-
   `--repo` safety guard fires in addition, and its remedy `--org '' --repo ''/<name>`
   is not runnable (empty org, literal `<name>`); the remedy presumes an org was
   given. A stranger fixing the first refusal and re-running still hits the second.
   Grade: NEXT — friction: a redundant remedy that won't run, not a wrong verb result.

3. `nova-work version -h`
   ```
   usage: nova-work version [flags]
   nova-work is pre-alpha: not ready for production use.
   from `nova-work help`:
     nova-work version
   flags:
     --json  print the result as one JSON object instead of lines
   exit codes: 0 done, or verify found no difference; 1 verify found differences, or an import's encoded tree did not read back equal; 2 could not run (a flag, the budget, gh, GitHub, a file)
   effect: inspection: reads, writes nothing
   ```
   Expected: `version -h` to describe `version` only. What it does: the exit-code
   table ("verify found no difference; ... an import's encoded tree") and the
   `effect: inspection: reads, writes nothing` line are the shared read-verb text
   copied verbatim; `version` neither reads GitHub nor verifies nor imports.
   Grade: NEXT — help noise: one extra line to ignore, not a wrong result.

READ 8/10 -- The help and the SPEC-WORK-V1/CLI.md pages are accurate and give a
runnable offline first run (`--against` with the minimal tree); docked only for
`version -h`, which prints the read verbs' exit-code table and effect line.

USE 7/10 -- Refusals name the bad flag and what it wants, exit codes are honest,
and `verify --against` works offline exactly as documented; docked for the
`--out <existing>` remedy, which drops the caller's `--repo` so one-paste recovery
would import the whole organization instead of the named repository.

urgent=1 next=2
