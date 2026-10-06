# nova-tokens dogfood, 2026-10-06 (dsh)

Tool: nova-tokens. Built from the base `sprint/mechanical-2026-10-02` at
`cb5fb8d4c3290f710d22a21a86aa8d229e4db905` on a Linux bench and used as
`nova-tokens v1.0.1-0.20261006204015-cb5fb8d4c329 linux/amd64 go1.26.6`.
Read cold: only `nova-tokens -h`, `nova-tokens help`, `nova-tokens <verb> -h`
and the tool's page under `docs/` (`docs/SPEC-TOKENS.md`). Every verb was run at
least once with its real flags in a temp directory: the `setup:` and `example:`
lines as printed, the store-free success paths, `--dry-run` and `--json`, the
refusals, and a shrink. No code was changed; a finding is recorded, never fixed
here.

## Findings

1. `nova-tokens fold --out ./out --day 2026-09-11 --repos ./repos.tsv --claude bench=./transcripts`,
   then `nova-tokens session --claude-session ./session.jsonl --out ./out`, then
   `nova-tokens sum --out ./out --month 2026-09` (the printed `setup:` and `example:` blocks,
   run as printed)
   Printed (first 3 lines of `sum`):
   ```
   SUM MONTH month=2026-09 at=2026-10-06T20:48:24Z build=v1.0.1-0.20261006204015-cb5fb8d4c329 days=1 first=2026-09-11 last=2026-09-11 missing=0 rows=2 turns=1
   SUM PAIR model=claude-fable-5-1 repo=schema input=812 output=40 cache_write=1200 cache_read=90000 reasoning=- rough=0 dashes=0,0,0,0,1 nonutc=0 days=1
   SUM PAIR model=claude-fable-5-1 repo=unattributed input=812 output=40 cache_write=1200 cache_read=90000 reasoning=- rough=0 dashes=0,0,0,0,1 nonutc=0 days=1
   ```
   Its later lines read `SUM MODEL model=claude-fable-5-1 input=1624 output=80 cache_write=2400 cache_read=180000 ...`
   and `SUM TOTAL input=1624 output=80 cache_write=2400 cache_read=180000 ...`.
   Expected: the transcript holds one message (`example-1`, input 812), so the tool's own first
   run should report 812 once. The `setup:` copies `window.jsonl` to `session.jsonl`, and the
   `example:` folds both, so the same message id is booked twice under two source names
   (`claude:bench` and `claude-session`) into one day. `check` still prints `CHECK OK ... rows=2`
   and no refusal names the shared id; the day file itself doubles every type.
   Grade: URGENT

2. `nova-tokens ledger --out ./out --month 2026-08 --redis 127.0.0.1:1`
   Printed:
   ```
   LEDGER FAILED month=2026-08 days=0 rows=0 bad=0
   ```
   exit 1, on stdout, stderr empty; `--json` carries no `why`, and `--dry-run` prints the same
   with `dry_run=true`.
   Expected: the same verb's day form names the cause and the next command
   (`LEDGER FAILED day=2026-09-13 why=no day file; fold --day 2026-09-13 first`), and with day
   files present it names the store (`LEDGER FAILED store=redis err=... connection refused`).
   A month with no files is a delivery that cannot be fixed from the line, because the line has
   no reason and no remedy.
   Grade: URGENT

3. `nova-tokens fold --out ./f --day 2026-09-11 --repos ./repos.tsv --claude one=./tr`, then
   `nova-tokens session --claude-session ./tr/u.jsonl --out ./s` (one transcript that matches no
   repo rule)
   Printed (the day row each writes):
   ```
   2026-09-11	claude-fable-5-1	unknown	10	1	1	1	-	0	utc	claude:one
   2026-09-11	claude-fable-5-1	unattributed	10	1	1	1	-	0	utc	claude-session
   ```
   Expected: one name for one concept. The help's vocabulary is `--unattributed` and
   `other=<pct>%`; the same no-rule-matched spend becomes two `SUM PAIR` buckets (`unknown` and
   `unattributed`) instead of one, and in the printed example it does exactly that.
   Grade: NEXT

4. `nova-tokens report --redis 127.0.0.1:6379 --month 2026-09` (a reachable store holding no
   `tokens:ledger` rows for that month)
   Printed:
   ```
   REPORT FAILED month=2026-09 source=redis indexed=0
   ```
   exit 1, no remedy. `--dry-run` prints the same, and `report --redis 127.0.0.1:1 --month 2026-09 --dry-run` still dials (`REPORT FAILED store=redis err=... connection refused`).
   Expected: name that the month has no indexed days and the command that would index it
   (`nova-tokens ledger --out <dir> --month 2026-09 --redis ...`); and since report's help says
   it reads the store "with or without --dry-run", a reader has no way to see the store form's
   success shape without a live store.
   Grade: NEXT

5. `nova-tokens fold --out ./out --day 2026-09-11 --repos ./repos.tsv --claude bench=./transcripts`
   (a fresh temp directory)
   Printed:
   ```
   TOKENS REFUSED: --out does not exist: ./out; it wants the directory the day files are written to; run: nova-tokens help
   ```
   exit 2.
   Expected: a local write verb makes the directory it is about to fill, or the refusal names the
   one command to run (`mkdir -p ./out`); `run: nova-tokens help` is not the action. The printed
   `setup:` hides this from a reader by making the directory first.
   Grade: NEXT

6. `nova-tokens report --who ada --day 2026-09-11 --repos ./repos.tsv --claude one=./tr --note ./no/dir/note.md`
   Printed (stdout first 3 lines):
   ```
   2026-09-11	ada	claude-fable-5-1	unknown	input	10
   2026-09-11	ada	claude-fable-5-1	unknown	output	1
   2026-09-11	ada	claude-fable-5-1	unknown	cache_write	1
   ```
   then on stderr `REPORT REFUSED: --note ./no/dir/note.md: atomicfile: parent directory for "no/dir/note.md": lstat no/dir: no such file or directory; run: nova-tokens help`, exit 2.
   Expected: the note is not written, but stdout holds a complete-looking four-row note body with
   no unwritten marker; a caller that captures stdout cannot tell the note was refused. The
   refusal should come before the body, or the body should say it was not written.
   Grade: NEXT

7. `nova-tokens fold --out ./o1 --day 2026-09-11 --repos ./repos.tsv --claude a=./inner-schema/tr`,
   then `nova-tokens fold --out ./o2 --day 2026-09-11 --repos ./repos.tsv --claude b=./plain/tr`
   Printed (the day row each writes):
   ```
   2026-09-11	claude-fable-5-1	unknown	10	1	1	1	-	0	utc	claude:a
   2026-09-11	claude-fable-5-1	schema	10	1	1	1	-	0	utc	claude:b
   ```
   `a`'s transcript lives in a directory named `inner-schema` and names no path in its message;
   `b`'s lives in `plain` but its `tool_use` names `/work/schema/wire.md`.
   Expected: the help says "the first match on a session's path wins", which a reader takes as the
   session's own path, so `inner-schema` should match and `plain` should not. The rule actually
   matches the paths the session's messages name. The help should say which path it means.
   Grade: NEXT

## What could not be run

No scratch store was started (the card's rules forbid starting a server), so `ledger`'s write and
`report --redis`'s success shape were not exercised. The store verbs were run against an
unreachable address for the failures and, read-only, against a store that already answered; every
other verb ran against a temp directory.

READ 7/10 — the banner answers what, how and how-to, every verb's `-h` exits 0 and names its
effect class and the exit table, and the refusals name every problem at once with a remedy; the
score is held down by the ledger month's silent failure, the store report's missing reason, and
the sentence about a session's path.

USE 6/10 — the temp-directory first run works and every store-free verb ran, but that first run
inflates its own numbers and passes its own `check`, the unmatched repo is named two ways, and the
delivery verb can fail without saying why; with a scratch store the delivery paths could raise it.

urgent=2 next=5
