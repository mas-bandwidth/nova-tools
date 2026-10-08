# Dogfood: nova-update — 2026-10-06, dsh (zhi)

One friend, one tool, cold. I read only `nova-update -h`, `nova-update help`, every
verb's `-h` (including `release <verb> -h`), and the page under `docs/` the banner
names, `docs/SPEC-UPDATE.md`, then used every verb at least once with its real flags:
`example` (print, `--out`, the overwrite refusal), `version`, `check` and `status`
against scratch manifests of every kind and every verdict (EQUAL, STALE, NEWER,
DIFFERENT, AHEAD unknown, UNKNOWN, pin), the four latest schemes over the network,
`apply` dry and real, `report` (plain, `--host`, `--json`, `--snapshot`, `--draft`,
`--send`, `--store`), `watch`, `adoption`, and the `release` helps and refusals. Built
from the staged checkout at `dffea996e566` and run as
`nova-update v1.0.1-0.20261007145710-dffea996e566 linux/amd64 go1.26.6` on the Linux
bench (no go command runs on the working machine); scratch manifests and files live
under the job's `tmp/scratch`. 15–40 minutes of use. No code changed, a finding is
recorded here and never fixed here.

## Findings

1. A `kind=model` entry whose `installed` column is a version string is compared to the
   registry digest as if the string were a digest, and prints a meaningless DIFFERENT.

       $ nova-update check --file tmp/scratch/model_verstr.tsv --timeout 10s
       CHECK FAILED checked=1 current=0 stale=0 newer=0 ahead=0 differ=1 unknown=0 pins=0 took=395ms file=tmp/scratch/model_verstr.tsv entries=1 kinds=model at=2026-10-07T15:07:03Z timeout=10s budget=1m0s max=20
       CHECK DIFFERENT name=m2 kind=model installed=1.2.3 latest=365c0bd3c000 path=- source=ollama:llama3:latest owner=me
       (manifest line: `m2	model	1.2.3	ollama:llama3:latest	none	me`; exit 1)

   SPEC-UPDATE.md rule 4a is explicit that a model's version is its digest and that the
   installed side is the entry's argv read as the `ollama list` table, exactly because a
   model "has no version". The help's manifest line 3 offers `installed` as "a version
   (v1.2.3), a command name on PATH, or an argv" without excluding `kind=model`, so this
   input parses; the run then compares the human string `1.2.3` against the real digest
   `365c0bd3c000` and exits 1 with a `DIFFERENT` a nightly reads as real drift. The
   correct behaviour is the load-time refusal (or the not-on-this-box UNKNOWN) rule 4a
   names; nothing may compare a version string to a digest. Grade: URGENT.

2. `report --store` puts the go-redis client's own log lines on the result stream above
   the one-line refusal.

       $ nova-update report --store 127.0.0.1:6399 --timeout 1s
       redis: 2026/10/07 15:03:41 pool.go:762: redis: connection pool: failed to dial after 5 attempts: dial tcp 127.0.0.1:6399: connect: connection refused
       redis: 2026/10/07 15:03:42 pool.go:762: redis: connection pool: failed to dial after 5 attempts: dial tcp 127.0.0.1:6399: connect: connection refused
       REPORT REFUSED: fleet beats at 127.0.0.1:6399: context deadline exceeded; run: nova-update report -h
       (all three on stderr; with `--timeout 2s`, four redis lines precede the same refusal, whose tail is `... connect: connection refused`)

   The grammar in SPEC-UPDATE.md gives `REPORT REFUSED: <reason>; run: <command>` alone,
   and SPEC.md's Conventions say an event is exactly one line; a caller that parses the
   stream line by line meets library log lines it cannot know. The refusal itself is
   right and its remedy works. Grade: NEXT.

3. A directory given to `--file` is refused as a bad line-1 header plus a size remedy the
   reader did not earn.

       $ nova-update check --file tmp/scratch
       CHECK REFUSED: tmp/scratch: line 1: invalid header (put the header back exactly: name<TAB>kind<TAB>installed<TAB>latest<TAB>apply<TAB>owner); line 1: unreadable manifest (use lines below 1 MiB); run: nova-update check -h

   A directory is not a manifest with a header mistake, and "use lines below 1 MiB" sends
   a reader who named a directory to shrink a file. One refusal naming that `--file` did
   not name a regular readable file (with the path) is what a stranger needs. Grade: NEXT.

4. A model's `latest` may be any scheme; the run then asks that source and blames the
   installed side, with a pull command for a name that is not the model.

       $ nova-update check --file tmp/scratch/model_gh.tsv
       CHECK FAILED checked=1 current=0 stale=0 newer=0 ahead=0 differ=0 unknown=1 pins=0 took=352ms file=tmp/scratch/model_gh.tsv entries=1 kinds=model at=2026-10-07T15:06:45Z timeout=5s budget=1m0s max=20
       CHECK UNKNOWN name=m1 kind=model installed=- path=/usr/bin/printf source=github:o/r@https://api.github.com/repos/o/r/tags?per_page\x3d1: model_not_found (this weight is not on this box: its owner me pulls it, ollama pull m1)
       (manifest line: `m1	model	printf 1.2.3	github:o/r	none	me`)

   Rule 4a's latest is one GET of the ollama registry, and the mirror of this mistake is
   already refused at load: `ollama:llama3` on a `tool` prints "ollama digest requires
   model kind (set kind=model)". Here a model naming `github:` loads, asks
   `api.github.com`, and then reports the installed read's `model_not_found` and tells the
   owner to `ollama pull m1` — the wrong layer and a name that is the entry's label, not
   the model. Grade: NEXT.

5. The send note truncates nova-bus's refusal mid-sentence and asks for a retry with a
   snapshot the run itself reported as `-`.

       $ nova-update report --file tmp/scratch/current.tsv --send --as zhi --to b
       REPORT FAILED checked=2 known=2 unknown=0 changed=- sent=uncertain took=18ms file=tmp/scratch/current.tsv host=- as=zhi entries=2 kinds=engine,tool at=2026-10-07T15:02:19Z timeout=5s budget=1m0s max=20 snapshot=-
       REPORT TOOL name=a kind=tool version=1.0.0 raw=1.0.0 path=/usr/bin/printf
       REPORT NOTE send not confirmed: exit 2; the bus said: SEND REFUSED: --redis is required: NOVA_BUS_REDIS is unset, and with no NOVA_SPRINT_REDIS the fleet's bus row (nova-config fleet set --bus <host:port>, then apply) cannot be read either; refusing to g... (retry --send with the same --snapshot)

   The bus's own refusal ends with the remedy for the one thing to fix, and the cap cuts
   it at "refusing to g..."; the only surviving half is a retry that names a snapshot the
   invocation did not pass (`snapshot=-`). Rule 25 says cross-process recovery needs the
   caller-named snapshot, so naming it is fair advice only when one was given; otherwise
   the note should say the bus's refusal whole and leave the retry advice to the failure
   at hand. Grade: NEXT.

6. The shipped example's `apply` installs a versioned wrapper that the example's own
   `installed` read never runs, so a real `apply go` on a box whose system Go is behind
   installs the wrapper and then fails its own after-read.

       $ nova-update apply --file tmp/scratch/exdemo/versions.tsv go --dry-run --version 1.25.0
       APPLY OK name=go dry_run=true from=1.26.6 to=1.25.0 source=local:go\x20version
       APPLY NEWER name=go kind=tool installed=1.26.6 latest=1.25.0 path=<home>/go/bin/go source=local:go\x20version owner=caller
       APPLY PLAN name=go argv=3 version=1.25.0: go install golang.org/dl/go1.25.0@latest

   The entry's installed read is `go version` (the system `go`), while the plan writes
   `go1.25.0`; the wrapper `go1.25.0 version` is never read, so the example can install
   and still report no movement. The real `apply go` (no `--version`) on this bench
   happened to be already equal and installed `<home>/go/bin/go1.26.6` for nothing;
   the mismatch only shows when the target differs, which is the case the example exists
   to teach. The `example:` block and the first-run section offer this apply as the
   dry-run to try, so it should install what the entry reads. Grade: NEXT.

7. A `watch` pass is split across the two streams, so neither stream holds the ordered
   pass the spec promises.

       $ nova-update watch --adopt tmp/scratch/checks.tsv
       (stdout)
       ADOPT OK check=ok-check detail=1.2.3
       ADOPT OK check=slow detail=-
       (stderr)
       ADOPT REFUSED check=bad-check detail=exit 1 (repair the check command)
       ...
       ADOPT DONE sha=10d9e523f406 ok=2 refused=3
       (exit 1)

   SPEC-UPDATE.md's grammar section closes with "`watch`'s `ADOPT` lines keep their own
   order: one per check, then `ADOPT DONE`". The pass is not OK as a whole, so the count-
   line rule would put it on stderr entire; instead the two `ADOPT OK` lines go to stdout
   and the refusals, the escalations and the `ADOPT DONE` digest go to stderr. A caller
   reading stdout sees two OKs and never the pass's `sha=`, and a caller reading stderr
   never sees which checks passed, so the documented sequence exists only if the caller
   merges both streams in order. Grade: NEXT.

8. A header-only manifest is `CHECK OK checked=0` at exit 0.

       $ nova-update check --file tmp/scratch/header_only.tsv
       CHECK OK checked=0 current=0 stale=0 newer=0 ahead=0 differ=0 unknown=0 pins=0 took=0s file=tmp/scratch/header_only.tsv entries=0 kinds=- at=2026-10-07T15:08:37Z timeout=5s budget=1m0s max=20

   A file whose only line is the header checks nothing and says OK, so a stranger who
   wrote the header and has not added entries yet reads green; the count line's
   `entries=0 kinds=-` is the only hint. An empty inventory is worth an explicit word
   (a NOTE, or a refusal naming that no entry was read) rather than the same line a
   complete manifest prints. Grade: NEXT.

## What worked

Every verb answers `-h` at exit 0 before reading anything; `help` and each verb's help
carry their effect, flags and exit codes, and the usage block is the binary's own string.
A missing flag refuses in one line naming every missing flag (`release cut` named
`--repo, --from, --version, --changelog` at once). A malformed manifest refuses once with
every problem and its line (five fields; an empty field; a duplicate name; an unknown
kind; two adjacent whitespace characters; a leading one; `ftp:`; `local:` with no locator; a `pin` with
a non-local source; an `ollama:` source on a tool), and `--json` renders the same refusals
on stdout. The version read is the whole identity (a two-line `printf` kept only its first
line; `go1.26.6` read `1.26.6`; a pseudo-version stayed whole), the four network schemes
answered (`github:cli/cli` 2.102.0, `npm:left-pad` 1.3.0, `brew:jq` 1.8.2, the ollama
digest of `llama3:latest` read as `365c0bd3c000`), a 404 on `github:` made the one
`/tags?per_page=1` retry and its remedy, `apply` printed `BEFORE`/`RUN`/`AFTER` and a
real scratch install moved `1.0.0` to `1.1.0` and `{version}` substituted, `--dry-run`
wrote nothing, `--timeout` cut a `sleep 3` and `--budget` bounded the run, `report`
answered with no network and its `--snapshot` wrote `observed`, `changed=yes` then
`changed=no`, `--draft` printed the note body with the blank header line and the `MORE`
lines under `--max 1`; `watch` printed `ADOPT OK`/`REFUSED`/`ESCALATE`/`DONE` with a
stable `sha=` (the stream split is finding 7), `adoption` filtered by friend, capped, and
refused an unknown state, and every pin rule held (`pins=` counted the one DIFFERENT pin
and the pin line printed first).

## Not run

- `report --store` was run only to its refusal: no store is reachable and the rules forbid
  starting one, so the success path (`REPORT DRIFT` lines and the receipt) was not seen.
- `report --send` and `watch --as --to` were run only to their no-bus outcome
  (`NOVA_BUS_REDIS` unset, `sent=uncertain`); no confirmed `SEND OK` was reached, so the
  snapshot `delivered`/suppression path was not exercised.
- `release build`, `install`, `adopt`, `pull` and `cycle` were run only through `help`,
  `-h` and their refusals: a real one needs a forge, ssh trust to a fleet and a built
  artifact tree, none of which this card provides.
- One real `apply` ran the example's own Go command and installed
  `<home>/go/bin/go1.26.6` (the example's apply line); it was not removed, since
  nothing outside the job's `scratch/` and `tmp/` is deleted.

READ 8/10 — the banner, every verb's help and SPEC-UPDATE.md answer a cold reader fast
and truthfully, and the refusals name the next command; the score is held down by the
manifest's two syntaxes for an argv (`installed` bare, `latest` `local:`), the model
rules the help only states in the spec (findings 1 and 4), and the empty file that reads
green (finding 8).

USE 7/10 — every verb ran for real against scratch manifests and files, no UNKNOWN ever
read as current, and the refusals carried the next command; the score is held down by the
model comparison that should have refused, the store refusal wrapped in library log
lines, the watch pass split across two streams, and the send note that cuts the bus's own
remedy.

urgent=1 next=7
