# nova-update dogfood — Zhi (deepseek/deepseek-v4), 2026-10-06

Read cold, as a stranger: only `nova-update -h`, `nova-update help`,
`nova-update <verb> -h`, and the tool's pages under `docs/`
(`docs/SPEC-UPDATE.md`, `docs/CLI.md`). Built from the checkout at
1c05149f60a74e73707c4eb6325b84194648ffcf and used on a Linux bench as
`nova-update v1.0.1-0.20261007140251-1c05149f60a7 linux/amd64 go1.26.6`.
The `example:` block ran as printed in a scratch directory; a hand-written
manifest covered every kind and several latest schemes; `apply` ran for real
against a temp directory; the refusals were run. Identities in the transcripts
are placeholders (`ada`, `bob`).

## Findings

1. One failing `watch` pass is split across stdout and stderr, so neither
   stream holds the pass whole.

   Command:

   ```
   nova-update watch --adopt checks.tsv
   ```

   A three-check file where one check passes and two refuse. stdout:

   ```
   ADOPT OK check=git\x20version detail=git\x20version\x202.53.0
   ```

   stderr:

   ```
   ADOPT REFUSED check=missing\x20thing detail=not_found (install nosuchcmd or supply its executable path)
   ADOPT ESCALATE check=missing\x20thing to=ada: duty files an issue and a fix card (not_found)
   ADOPT REFUSED check=another\x20missing detail=not_found (install nosuchcmd2 or supply its executable path)
   ADOPT ESCALATE check=another\x20missing to=bob: duty files an issue and a fix card (not_found)
   ADOPT DONE sha=533a4352f5e5 ok=1 refused=2
   ```

   `docs/SPEC-UPDATE.md:678` says "A run that is OK prints on stdout and one
   that is not on stderr, whole", and line 683 says watch's `ADOPT` lines "keep
   their own order: one per check, then `ADOPT DONE`". Here the passing check is
   on stdout while both refusals and the ending count are on stderr: `watch ...
   > log` records no ending and no failures, and `watch ... 2> log` records no
   passing check. A pass whose checks all pass does print whole on stdout
   (verified with a one-passing-check file: `ADOPT OK`, then `ADOPT DONE
   sha=0808d8197af4 ok=1 refused=0`). Grade: URGENT.

2. `report --store` leaks four Redis client pool log lines before its refusal.

   Command:

   ```
   nova-update report --store 127.0.0.1:1 --timeout 2s
   ```

   Printed (stderr, exit 2, first three of four lines):

   ```
   redis: 2026/10/07 14:11:02 pool.go:762: redis: connection pool: failed to dial after 5 attempts: dial tcp 127.0.0.1:1: connect: connection refused
   redis: 2026/10/07 14:11:02 pool.go:762: redis: connection pool: failed to dial after 5 attempts: dial tcp 127.0.0.1:1: connect: connection refused
   redis: 2026/10/07 14:11:02 pool.go:762: redis: connection pool: failed to dial after 5 attempts: dial tcp 127.0.0.1:1: connect: connection refused
   ```

   then `REPORT REFUSED: fleet beats at 127.0.0.1:1: dial tcp 127.0.0.1:1:
   connect: connection refused; run: nova-update report -h`. I expected one
   refusal line, per the output grammar; the four library lines carry a source
   file and line and are not the tool's voice. They appear under `--json` too,
   so a machine reading stdout gets clean JSON while stderr still carries the
   noise. Grade: NEXT.

3. A directory given to `--file` is refused with a header complaint about
   itself.

   Command:

   ```
   nova-update check --file adir
   ```

   Printed (exit 2):

   ```
   CHECK REFUSED: adir: line 1: invalid header (put the header back exactly: name<TAB>kind<TAB>installed<TAB>latest<TAB>apply<TAB>owner); line 1: unreadable manifest (use lines below 1 MiB); run: nova-update check -h
   ```

   I expected a refusal that `--file` wants a regular file: the first half names
   a header line in a directory and invites the reader to fix it, which no edit
   can. The line count and MiB wording are about a file that was never read.
   Grade: NEXT.

4. The `source=` provenance for a GitHub tags read escapes `=` to `\x3d`, so the
   endpoint cannot be pasted back.

   Command:

   ```
   nova-update check --file tags.tsv --timeout 8s --budget 40s
   ```

   The file holds one row, `gitgit  tool  git version  github:git/git  none  me`.
   Printed (one finding line):

   ```
   CHECK STALE name=gitgit kind=tool installed=2.53.0 latest=2.56.0 path=/usr/bin/git source=github:git/git@https://api.github.com/repos/git/git/tags?per_page\x3d1 owner=me
   ```

   Rule 6 (`docs/SPEC-UPDATE.md:138-140`) says every line carries `source=`: the
   scheme and locator asked, and the endpoint that answered when the tags read
   did. `per_page\x3d1` is the one-token law applied to the query separator; the
   same run under `--json` carries the unescaped
   `...tags?per_page=1`, so the line rendering is the only place the endpoint is
   not usable as typed. Grade: NEXT.

5. The send-failure note tells the reader to retry with a snapshot that was
   never named.

   Command:

   ```
   nova-update report --file custom.tsv --send --as ada --to bob
   ```

   Printed (stderr, exit 1, the count line and the closing note):

   ```
   REPORT FAILED checked=8 known=4 unknown=4 changed=- sent=uncertain took=38ms file=custom.tsv host=- as=ada entries=8 kinds=engine,harness,model,pin,tool at=2026-10-07T14:11:01Z timeout=5s budget=1m0s max=20 snapshot=-
   REPORT NOTE send not confirmed: exit 2; the bus said: SEND REFUSED: --redis is required: NOVA_BUS_REDIS is unset, and with no NOVA_SPRINT_REDIS the fleet's bus row (nova-config fleet set --bus <host:port>, then apply) cannot be read either; refusing to g... (retry --send with the same --snapshot)
   ```

   The count line says `snapshot=-`, so there is no snapshot to retry with; rule
   25 says "Without `--snapshot` every `--send` sends", so the remedy names a
   mode this run never used. I expected the remedy to name the missing store
   (`NOVA_BUS_REDIS`) or to say a snapshot must be named first. Grade: NEXT.

6. A manifest with no entries exits 0 and reads as all current.

   Command:

   ```
   nova-update check --file header_only.tsv
   ```

   Printed (stdout, exit 0):

   ```
   CHECK OK checked=0 current=0 stale=0 newer=0 ahead=0 differ=0 unknown=0 pins=0 took=0s file=header_only.tsv entries=0 kinds=- at=2026-10-07T14:11:01Z timeout=5s budget=1m0s max=20
   ```

   The header-line-only file is honest about `entries=0`, but the status word is
   OK and the exit is 0, so a stub or truncated manifest reads exactly like a
   night where everything is current. The spec's own reason for the tool is that
   a failure is never read as up to date; I expected a refusal or a NOTE that
   nothing was read. Grade: NEXT.

## What the tool got right

- The `example:` block is real and runnable: `example --out versions.tsv` wrote
  the one-tool manifest, `report` and `status` read it, and
  `apply ... go --dry-run` printed the plan at exit 0.
- `apply` worked for real against a temp directory: `APPLY BEFORE`, `APPLY RUN
  ... ./setver 1.3.0`, `APPLY AFTER ... was=1.0.0`, and `APPLY OK name=setver
  from=1.0.0 to=1.3.0`, with the `{version}` token substituted and `--dry-run`
  leaving the file at its old value.
- A manifest with several problems is refused once, each problem with its line
  (`line 2: 5 fields, want 6; line 3: unknown kind weights; line 4: installed:
  argv requires single spaces and no quoting; line 5: duplicate name c`), so a
  file is fixed in one pass.
- Every `-h` exits 0 before anything is read; every refusal is one line naming
  what the flag wants and a remedy; `--json` renders the same value as the
  lines; `--max` keeps its `MORE ... total=`; a model that is not on the box is
  refused with its owner's own pull (`model_not_found ... its owner me pulls it,
  ollama pull qwen3-coder:30b`), and `apply` on a `none` row is refused with
  "installed by hand".
- `check --kind` filters the reads and prints `entries=` for the whole file
  while `checked=` and `kinds=` are the filtered run.

## Not run

- `report --store` was run only to its refusal: no store was reachable and the
  rules forbid starting one, so its success path (`REPORT DRIFT` lines and the
  receipt) was not exercised.
- `release build`, `install`, `adopt`, `pull` and `cycle` were run only through
  `help`, `-h` and their refusals: a real one needs a forge, ssh trust to a
  fleet, and a built artifact tree, none of which this card provides. `release
  cut` with no flags refused naming every missing flag in one line.
- `--send` could not reach a bus (no `NOVA_BUS_REDIS` and no fleet row), so only
  its refusal and the failure note were exercised; the send retry and snapshot
  success path were not.

READ 7/10 — the banner answers what, how and first run, every verb's `-h` names
its effect and flags, and the refusal grammar is consistent and actionable; the
score is held down by refusals that do not read cleanly at the edge (a directory
given to `--file` is blamed for a header, and the store refusal is preceded by
four library lines) and by a send note inviting a retry with a snapshot the run
itself reported as `-`.

USE 8/10 — a stranger with only the binary and a hand-written TSV ran a real
check, status, report, draft, apply (dry and real), watch and adoption against a
scratch directory, with UNKNOWN never reading as current; the split `watch`
streams and the store-path library noise are the two places the output stops
being one readable value.

urgent=1 next=5
