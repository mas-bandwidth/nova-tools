# nova-update dogfood — grok, 2026-10-06

Read cold, as a stranger: only `nova-update -h`, `nova-update help`,
`nova-update <verb> -h`, and the tool's own pages under `docs/`
(`docs/SPEC-UPDATE.md`). Built from the checkout at
`abb9bfecc72930ef36ce116ac2f652b0e85ad044` and used on a Linux bench as
`nova-update v1.0.1-0.20261007153756-abb9bfecc729 linux/amd64 go1.26.6`.
The `example:` block ran as printed; a hand-written manifest covered every kind
and every latest scheme the box could reach, with fake local version scripts
for the order cases and a fake `ollama` for the model digest; `apply` ran for
real against a temp directory; `report --send` ran against a fake `nova-bus`;
the refusals were run. Identities in the transcripts are placeholders (`ada`,
`bob`).

## Findings

1. One `watch` pass is split across stdout and stderr, so neither stream holds
   the pass whole.

   Command: `nova-update watch --adopt checks.tsv`, a two-check file where one
   check passes (`true`) and one refuses (`false`). stdout:

   ```
   ADOPT OK check=one detail=-
   ```

   stderr:

   ```
   ADOPT REFUSED check=two detail=exit 1 (repair the check command)
   ADOPT ESCALATE check=two to=caller: duty files an issue and a fix card (exit 1)
   ADOPT DONE sha=d71e714c5ff3 ok=1 refused=1
   ```

   The spec says "A run that is OK prints on stdout and one that is not on
   stderr, whole" and that watch's lines "keep their own order: one per check,
   then `ADOPT DONE`". Here the passing check is on stdout while the refusal
   and the ending count are on stderr: `watch ... > log` records no failure and
   no ending, and `watch ... 2> log` records no passing check. When every check
   passes, all four lines including `ADOPT DONE` are on stdout, so the split
   only appears on the run a person most needs to read. Grade: URGENT.

2. `not_found` for an argv whose first token carries a `/` claims a PATH search
   that never happened, and names an "install" step that is not a command.

   Command: `nova-update report --file abs.tsv`, the one entry's `installed`
   column being `/nonexistent/foo version`. Printed (first three lines, exit 1):

   ```
   REPORT FAILED checked=1 known=0 unknown=1 changed=- sent=- took=2ms file=abs.tsv host=- as=- entries=1 kinds=tool at=2026-10-07T15:50:29Z timeout=5s budget=1m0s max=20 snapshot=-
   REPORT UNKNOWN name=abs kind=tool path=- raw=-: not_found (install /nonexistent/foo or supply its executable path; searched PATH=<bench home dirs, then the system dirs>)
   ```

   Rule 4 says "an argv[0] carrying a `/` is that executable, no PATH
   searched", and the remedy for a bare name names "argv[0] and the PATH
   searched". I expected the `/` branch to name the path and not assert a
   search, and not to suggest `install /nonexistent/foo`; the printed remedy is
   the bare-name remedy applied one branch too wide. Grade: NEXT.

3. `report -h` reprints the manifest block dedented and drops its lead line, so
   the same help text reads differently under two verbs.

   Command: `nova-update report -h`, against `nova-update check -h`. Printed by
   `report -h` (the block after the usage lines):

   ```
     nova-update report --file versions.tsv     the six lines that say what versions.tsv holds:
     1. line 1 is the header, byte for byte: name<TAB>kind<TAB>installed<TAB>latest<TAB>apply<TAB>owner; every other line is six fields, one tab between, none empty; a line starting # is a comment
     2. kind is harness, engine, model, tool or pin; name is unique in the file; owner is who answers for it
   ```

   `check -h` prints the same block as:

   ```
   THE MANIFEST is the file --file names, written by hand, the same for both tools:
     nova-update report --file versions.tsv     the six lines that say what versions.tsv holds:
         1. line 1 is the header, byte for byte: name<TAB>kind<TAB>installed<TAB>latest<TAB>apply<TAB>owner; ...
   ```

   The spec says the usage lines are one string in the binary "so the spec and
   the help cannot drift apart", and `report`'s own usage continuation lines
   (`  <who,who> | ...`, `  [--max ...]`) lose their extra indent too. I
   expected the block byte for byte under every verb, as it is under `help`,
   `check` and `status`. Grade: NEXT.

4. `report --store` leaks four Redis client pool log lines before its refusal.

   Command: `nova-update report --store 127.0.0.1:1 --timeout 2s`. Printed
   (stderr, exit 2, first three lines):

   ```
   redis: 2026/10/07 15:50:46 pool.go:762: redis: connection pool: failed to dial after 5 attempts: dial tcp 127.0.0.1:1: connect: connection refused
   redis: 2026/10/07 15:50:46 pool.go:762: redis: connection pool: failed to dial after 5 attempts: dial tcp 127.0.0.1:1: connect: connection refused
   redis: 2026/10/07 15:50:46 pool.go:762: redis: connection pool: failed to dial after 5 attempts: dial tcp 127.0.0.1:1: connect: connection refused
   ```

   then `REPORT REFUSED: fleet beats at 127.0.0.1:1: ...; run: nova-update report -h`. I expected one refusal line, the tool's voice; the library lines
   carry a source file and line and no remedy, and they are the first thing a
   stranger's terminal shows for this verb. Grade: NEXT.

5. A manifest with no entries is `CHECK OK`, exit 0.

   Command: `nova-update check --file headeronly.tsv`, the file holding the
   header line and nothing else. Printed (stdout, exit 0, first three lines):

   ```
   CHECK OK checked=0 current=0 stale=0 newer=0 ahead=0 differ=0 unknown=0 pins=0 took=0s file=headeronly.tsv entries=0 kinds=- at=2026-10-07T15:51:43Z timeout=5s budget=1m0s max=20
   ```

   The count line is honest about `entries=0`, but the status word is OK and
   the exit is 0, so a stub or truncated manifest reads exactly like a night
   where everything is current. The tool's own reason to exist is that a
   failure never reads as up to date; I expected a refusal or a NOTE that
   nothing was read. Grade: NEXT.

6. `nova-update help`'s release line omits `cycle`, which the tool accepts.

   Command: `nova-update help` (release line), against `nova-update help release`. Printed by `help`:

   ```
     nova-update release <cut|build|install|adopt|pull> ...
       nova-tools' own release pipeline: nova-update help release prints its usage lines
   ```

   while `nova-update help release` and `nova-update release -h` both print six
   lines, the sixth being:

   ```
   nova-update release cycle --version <v> --source <dir> --out <dir> --inventory <file> --benches <a,b,...> --reason <why> --ansible <path> [--receipts <dir>] [--dry-run] [--timeout <d>]
   ```

   I expected the usage line to name every release verb it takes, since
   `release cycle` runs and `help release` lists it; a reader of the main help
   cannot discover it. Grade: NEXT.

7. A directory given to `--file` is refused with a header complaint about
   itself.

   Command: `nova-update check --file adir`, `adir` being a directory. Printed
   (exit 2, first three lines):

   ```
   CHECK REFUSED: adir: line 1: invalid header (put the header back exactly: name<TAB>kind<TAB>installed<TAB>latest<TAB>apply<TAB>owner); line 1: unreadable manifest (use lines below 1 MiB); run: nova-update check -h
   ```

   I expected a refusal that `--file` wants a readable regular file; the first
   half names a header line in a directory and invites an edit that no edit can
   make, and the MiB wording is about a file that was never read. Grade: NEXT.

## What the tool got right

- The `example:` block is real and runnable: `example --out versions.tsv` wrote
  the one-tool manifest, `report` and `status` read it, and `apply ... go --dry-run` printed the plan at exit 0. Running `example --out` again left the
  identical file `unchanged=true`, and an unrelated file was refused, not
  overwritten.
- `apply` worked for real against a temp directory: `APPLY BEFORE`, `APPLY RUN`,
  `APPLY AFTER ... was=...` and `APPLY OK` with `{version}` substituted; an
  installer that changed nothing against a newer target was `APPLY FAIL` exit 1
  naming both.
- Every `-h` (and `-h` on each `release` sub-verb) exited 0 before anything was
  read; every refusal was one line naming what the flag wants and a remedy;
  `--json` rendered the same value as the lines, refusals included.
- The order rule is exact where it claims to be: `1.10.0`/`1.9.0` is NEWER,
  `1.9`/`1.9.0` and `1.09.0`/`1.9.0` are DIFFERENT, a pseudo-version against its
  base-minus-one is AHEAD with the commit, and the glued `novatool-2.3.4` reads
  `2.3.4`.
- The model rule is real: a fake `ollama list` row's ID was compared with the
  live registry manifest's SHA-256 (twelve hex), a missing row was UNKNOWN with
  the owner's own `ollama pull`, and a bad tag was `tag_not_found` with the
  `ollama.com/library/.../tags` remedy.
- Every dead source stayed UNKNOWN, never OK: a 404 on `github:`, `npm:` and
  `brew:`, a `latest=-`, a `no_release_identity` dev build, no version token,
  the 64 KB output cap, a spent `--budget`, and a `--timeout` all exited 1.
- `report --snapshot` plus a fake `nova-bus` worked as written: the first
  `--send` wrote `delivered`, the identical repeat printed `REPORT NOTE unchanged since <id> to <to>; nothing sent` and started no bus, a changed
  observation sent again, a refused send left `delivered` absent and retried,
  and a second recipient was sent to.

## Not run

- `report --store` was run only to its refusal: no store was reachable and the
  rules forbid starting one, so its success path (`REPORT DRIFT` lines and the
  receipt) was not exercised.
- `release cut`, `build`, `install`, `adopt` and `pull` were run only through
  `-h` and their refusals: a real one needs a forge, ssh trust to a fleet and a
  built artifact tree, none of which this card provides.

READ 7/10 — the banner answers what, how and first run, the verb help names
each flag's effect and the refusal grammar is consistent and actionable; the
score is held down by `report -h` reprinting the manifest block dedented and
without its lead line, by the release usage line dropping `cycle`, and by a
`not_found` remedy that claims a PATH search for an absolute path.

USE 8/10 — a stranger with only the binary and a hand-written TSV ran a real
`example`, `check`, `status`, `report` (file, draft, send and snapshot), `apply`
(dry and real), `watch` and `adoption`, with UNKNOWN never reading as current;
the split `watch` streams and the `report --store` library noise are the two
places the output stops being one readable value.

urgent=1 next=6
