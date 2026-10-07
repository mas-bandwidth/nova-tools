# nova-tokens dogfood — opencode-2 (zhi), 2026-10-06

Read cold, as a stranger: `nova-tokens help`, `nova-tokens -h`, every verb's
`-h`, and the tool's pages under `docs/` (`docs/CLI.md`'s `## nova-tokens`,
`docs/SPEC.md`'s `## nova-tokens — spend, folded per day`, and the normative
`docs/SPEC-TOKENS.md` the first two point at). Built from the staged checkout at
174eac229d10f84680b90dea2976b375dbd2c7ad and used as
`nova-tokens v1.0.1-0.20261007155708-174eac229d10 linux/amd64 go1.26.6` on a
Linux bench. Fixtures were invented under a scratch directory: Claude Code
transcripts across two UTC days (a duplicate id, a message with no id, a
non-JSON line, a `<synthetic>` model, paths that match and miss the rules
file), a hand-written day file per malformed shape, a swarm pool of sixteen
column usage files with two `nousage` job directories, a bus lane with a
`participants.json`, and a `grok usage` JSON export. Every verb ran at least
once with its real flags — `fold`, `report` (local mode, store mode, `--json`,
`--dry-run`, `--supersedes`, `--note`), `ledger` (`--dry-run` and against a
refused address; no server was started), `sum`, `check`, `sources`, `profiles`,
`session`, `version`. No source file was changed; repeated folds printed
byte-identical rows below the stamp line. What was not done: no OpenCode
database was opened (there is none on the bench; the `--opencode` copy,
`--scratch` and missing-file refusals were exercised instead), and no Redis
store was dialled beyond a refused address, the rules forbidding a server here.

## Findings

1. `nova-tokens session --claude-session ./sess.jsonl --out ./sd --day 2026-09-01`

   ```
   SESSION turns=1 input=11 cache_write=3 cache_read=4 output=2 weighted=25 avg_context=18
   TOKENS DAY day=2026-09-01 written=true rows=1 retained=0 model=claude-fable-5-1 weighted=0
   ```

   The window's one turn is stamped `2026-09-11`, and `sd/2026-09-01.tsv` was
   written with `turns=0` and an all-zero row:

   ```
   nova-tokens v1 day=2026-09-01 at=2026-10-07T16:02:52Z build=… turns=0 sources=claude-session
   2026-09-01	claude-fable-5-1	unattributed	0	0	0	0	-	0	utc	claude-session
   ```

   The same happened for `--day 2026-09-12` and `--day 2026-09-13`. Expected:
   the window has no turns on that day, so nothing is written for it, or the
   one turn is booked under the named day with its real counts. A row of
   `0`s is neither: the source reported no count, and rule 15 says a type it
   did not report is `-`, never `0` ("a zero that means *not measured* would
   sum into a month that claims to be complete"); rule 9 says "a day with no
   rows has no file". The fabricated row also contradicts the run's own
   `SESSION weighted=25` with `TOKENS DAY weighted=0`, and `check` and `sum`
   accept the zero file as real. Grade: URGENT.

2. `nova-tokens fold --out ./o --day 2026-09-10 --repos ./repos.tsv --bus ./bus`, with `bus/from-ada/ada-111111111111.md` and a successor `ada-222222222222.md` whose subject trailer is `supersedes=ada-111111111111`

   ```
   TOKENS SOURCE label=bus:ada … unparsed=1 … superseded=0 rows=1
   TOKENS UNPARSED label=bus:ada note=ada-222222222222.md line=1: no such note in this lane for this day: ada-111111111111; send a correction whose subject carries supersedes=<id>
   TOKENS FAILED days=0 rows=0 sources=1 unreadable=0 unparsed=1 mixed=0 conflict=0 shrank=0 partial=0 quiet=0
   ```

   Writing the trailer in the form the tool's own conflict line prints,
   `supersedes=ada-111111111111.md`, gives the other half of the same trap:

   ```
   TOKENS UNPARSED label=bus:ada note=ada-222222222222.md line=1: the predecessor ada-111111111111.md is not <sender>-<12 hex>; send a correction whose subject carries supersedes=<id>
   ```

   Expected: one id names the predecessor. The note id the parser resolves is
   the file name (`ada-111111111111.md`), while the shape the same parser
   accepts is `<sender>-<12 hex>` (no extension) — which `report --supersedes`
   also enforces: it emits `supersedes=ada-111111111111` and refuses
   `supersedes=ada-111111111111.md` as "not <sender>-<12 hex>". So the only id
   that resolves is rejected, and the only id accepted resolves to nothing:
   no bus conflict can ever be cleared and a `report` correction can never
   replace a bus note. A conflict's remedy prints exactly the rejected form:

   ```
   TOKENS CONFLICT label=bus:ada day=2026-09-10 notes=ada-111111111111.md,ada-333333333333.md: competing reports; send a correction whose subject carries supersedes=ada-111111111111.md,ada-333333333333.md
   ```

   Grade: URGENT (a refusal whose printed remedy does not run; the whole
   supersede path is unreachable).

3. The help's own first run, run in order: setup from `nova-tokens help`, then `nova-tokens fold --out ./out --day 2026-09-11 --repos ./repos.tsv --claude bench=./transcripts`, then the help's session example `nova-tokens session --claude-session ./session.jsonl --out ./out`

   ```
   SESSION turns=1 input=812 cache_write=1200 cache_read=90000 output=40 weighted=11512 avg_context=92012
   TOKENS DAY day=2026-09-11 written=true rows=2 retained=1 model=claude-fable-5-1 weighted=11512
   ```

   The help's setup copies the one transcript to `./session.jsonl`, so the day
   file now holds the same window twice under two source labels:

   ```
   2026-09-11	claude-fable-5-1	schema	812	40	1200	90000	-	0	utc	claude:bench
   2026-09-11	claude-fable-5-1	unattributed	812	40	1200	90000	-	0	utc	claude-session
   ```

   Expected: the lines a stranger is told to run in order are safe together, or
   the help warns that folding a window and then `session`-folding the same
   window into one `--out` doubles it. Overlap between sources is deliberately
   not detected, and the duplicate-id note only fires inside one `--claude`
   fold, so the documented first run silently doubles a day's spend and
   `check` and `sum` are green about it. Grade: NEXT (friction: the first-run
   fixture should not be a trap).

4. `nova-tokens profiles --swarm-root ./swarmroot`

   ```
   PROFILES OK models=0 cards=0 overshoot=0
   ```

   `swarmroot/pool1/usage/j3.tsv` is a valid sixteen-column swarm usage file.
   The same silence came from `<root>/usage/*.tsv`, `<root>/<pool>/*.tsv`,
   `<root>/<card>/usage.tsv` beside a `PROMPT.md`, and
   `<root>/<pool>/done/<card>/usage.tsv`. Expected: one `PROFILES MODEL` line,
   or a refusal naming the file it looked for. The help says only
   "root containing the swarm pool profiles", and `docs/CLI.md` only "walks a
   swarm root's card usage files"; neither names a path or a file shape, and
   over a real usage file the verb answers a confident zero instead of saying
   what it read. Grade: NEXT (unclear help: a measurement verb a stranger
   cannot aim).

5. `nova-tokens sources --repos ./repos.tsv --all --claude bench=./utr --unattributed`, over messages whose only tool paths are `/work/alpha/one.md` and `/work/alpha/two.md`

   ```
   SOURCES UNATTRIBUTED stem=/work/alpha/one.md mentions=1
   SOURCES UNATTRIBUTED stem=/work/alpha/two.md mentions=1
   SOURCES OK sources=1 files=1 messages=2 unreadable=0 unparsed=0 rows=1 unattributed=2
   ```

   Expected: the grammar block in `docs/SPEC-TOKENS.md` prints
   `SOURCES UNATTRIBUTED stem=<path> tokens=<n>`, and the same page says the
   stem is "the token's leading directory to four elements — which is where a
   repo is named, and which is the shape a rule matches", so two files in one
   directory share one stem. The binary prints `mentions=`, and its keying is
   not the documented one: shallow paths keep the whole file name
   (`/work/alpha/one.md`, `/work/alpha/two.md` on separate lines) while a
   deeper path truncates (`/a/b/c/d/e/x.md` and `/a/b/c/d/e/y.md` both give
   `stem=/a/b/c`). A scanner written from the spec reads a field that is not
   there. Grade: NEXT (docs and binary disagree on the name and the key).

6. `nova-tokens check --out ./no-such-dir` and `nova-tokens sum --out ./no-such --month 2026-09`

   ```
   CHECK REFUSED: open ./no-such-dir: no such file or directory; run: nova-tokens help
   SUM REFUSED: open ./no-such: no such file or directory; run: nova-tokens help
   ```

   Expected: the named refusal `fold` gives for the same input:
   `--out does not exist: ./o2; it wants the directory the day files are
   written to; run: mkdir -p ./o2`. The two readers leak the raw `open` error
   and their remedy is only the generic door, so a reader is not told what the
   flag wants. Grade: NEXT (a refusal without a breadcrumb).

7. `nova-tokens fold --out ./o1 --day 2026-09-10 --repos ./repos.tsv --bus ./bus1` with a note whose header is `Date: 2026-09-10T10:00:00Z`, and `nova-tokens fold … --bus ./busX` with `participants.json` written `{"participants":["ada"]}`

   ```
   TOKENS UNPARSED label=bus:ada note=ada-111111111111.md line=2: the Date: is not a date nova-bus writes (Mon Jan  2 15:04:05 UTC 2006): 2026-09-10T10:00:00Z
   TOKENS UNREADABLE label=bus path=busX/participants.json: json: cannot unmarshal string into Go struct field .participants of type struct { Name string "json:\"name\""; Lane string "json:\"lane\"" }
   ```

   Expected: the help (`--bus <dir>  nova-bus directory with token notes`) and
   the docs would say what a lane is made of. They never do: the `Date:` header
   must be Go's `UnixDate` layout (`Thu Sep 10 10:00:00 UTC 2026` — found only
   by trial), and `participants.json` must be
   `{"participants":[{"name":"Ada","lane":"from-ada"}]}` with the lane spelled
   `from-<slug>`. The first refusal prints a format placeholder rather than an
   example and the second leaks a Go type name; a stranger cannot write a lane
   the tool reads without reverse-engineering both. Grade: NEXT (unclear
   help).

## What the tool got right

- `help`, `-h`, every `<verb> -h` answer on stdout at exit 0; a bare command
  and an unknown verb name the door in one line at exit 2.
- The no-defaults law holds: missing `--out`, `--day`/`--all`, `--repos`, a
  source flag, `--month`, `--redis`, `--swarm-root` or `--claude-session` is
  one named refusal (several in one run when several are missing), and
  `--scratch` is refused without `--opencode` and required with it.
- `check` is a real gate: it named a 10-column row, a date that disagreed with
  the file name, unsorted rows, an empty type cell, an empty `day_basis`, a
  missing version line and a version line without `turns=` in the same shape
  the spec gives; the gap/`--strict`/`--no-spend`/`--through` matrix behaved as
  written, `--strict` and `--no-spend` together is exit 2, and an empty
  `--out` is `CHECK FAILED looked at nothing`.
- `fold` merges by source, refuses a shrinking day and leaves the file
  byte-identical (`--allow-shrink` writes it and says `written=true`), prints
  `TOKENS QUIET` for a declared source that went empty on an explicitly
  selected existing day, detects a duplicate label, refuses two `--claude`
  directories that feed the same message ids, caps each listing with a `MORE`
  line and an uncapped total, and `--dry-run` writes nothing while printing
  `would_write=true`/`dry_run=true`.
- `report`'s stdout is exactly the six-field body and nothing else;
  `REPORT OK`, `TOKENS AVG` and `TOKENS AVG-ALL` go to stderr; `--note` is
  byte-identical to stdout; a failed `report` leaves an existing note
  byte-unchanged; `--supersedes` sorts the set and refuses a duplicate.
- `sum` keeps every type apart, counts the `-` cells per column (`dashes=`) and
  the non-UTC rows, prints the month even with gaps, and is a different line
  from a gate.
- `--json` exists on every verb but `version` and renders the same value as the
  lines (`report --json` carries the body in `payload`); the swarm source and
  the `xai` `grok usage` JSON both folded real rows, with `costUsdTicks`
  becoming `usd=0.012345` and `usd_per_mtok=93.5227`.
- `ledger --dry-run` reads the day files and dials no store; a refused store
  address and an empty password variable each print their own `FAILED` line at
  exit 1, as `docs/SPEC-STATE.md` gives them.

READ 7/10 — the banner, the verb helps and the pages state the no-defaults law,
the refusal grammar, the exit table, the five-types-apart rule and the
`check`-is-the-gate rule precisely and mostly truly, and every refusal I met
named its remedy; minus three for the `Date:`/`participants.json` shapes a
reader must reverse-engineer, the `SOURCES UNATTRIBUTED` grammar that names a
field the binary does not print, and `profiles`' unnamed input.

USE 6/10 — the writers are careful in the ways that matter (dry runs, atomic
day files, nothing removed, a real `check`, a merge that preserves other
sources' rows), and most verbs ran first try; minus four because `session --day`
fabricates a zero row, a bus correction can never name its predecessor, the
first run in the help doubles a day, and `profiles` answers a confident zero
over a real usage file.

urgent=2 next=5
