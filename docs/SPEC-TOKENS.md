# nova-tokens, specification

`nova-tokens` is one binary at the **accounting layer**. It folds token spend
from declared sources into **one file per day**, keyed exactly by
`(day, model, repo)`, with the five token types kept apart, and it sums those
day files into a month. It reads sources. It never estimates, never fills a
gap, and never removes a file.

This spec is normative. If the code and this document disagree, one of them
has a bug, and the tests decide which. It stands beside [SPEC.md](../SPEC.md),
whose **Conventions** section (exit codes, no guessed paths, the one-line
output grammar, the cap-and-count rule, `internal/oneline` and
`internal/bounded`) applies here unchanged and is not restated. Where this tool
needs something the Conventions do not cover, it is below and it says so.

Glenn, 2026-09-11: we have an obligation to report token spend. With several
agents and several models the report is per model and per repo. It is folded
daily so month end is a sum of days. The key is exactly `(day, model, repo)`
and the value is the token types, never folded into each other. Swarms and
Freddy are counted from this bench's own sources. Friends on other machines
self-report one note per day on the bus. The tool stamps, never a person.

A prototype in this shape (`token-collate.sh`, `token-fold.sh`,
`token-fold-opencode.sh`, about 530 lines of Python under zsh) produced the
first nine day files on 2026-09-11. The form works, and every way it failed is
in the table.

| the failure, from the record | the rule that closes it |
|---|---|
| the keeper's transcripts were mode 600 under another user; the fold printed `unreadable: 9 files skipped` as its last summary line and the collation carried it as one of six notes, in a list capped at six (**2026-09-11**) | a source that cannot be read is **counted, printed on its own line, and makes the run exit 1** (rule 3) |
| DeepSeek's usage for two swarm batches lived in per-job data homes that were reclaimed with the jobs; nothing survived (**2026-09-11**, SPEC-SWARM rule 12) | the swarm's **job records** are a declared source, so usage copied into a sidecar before reclaim is counted after the directory is gone (rule 14) |
| a streamed Claude Code transcript writes one message id on many lines, each with a usage block | a message is **counted once by its id**; the last line for that id is the message (rule 4) |
| 293 million tokens on this bench sat in `unknown` and no day said what share that was | `unknown` and `other` are **named buckets** and their share is printed on **every day line** (rule 5) |
| the bus parser accepted two body shapes, told them apart by whether the fifth field was a word or a number, and substituted `unknown` for an empty model or repo | **one line shape**, exact subject, and a line that does not fit is counted and printed with the note's id (rule 6) |
| the `~` rough mark was handled in one script only, and the rough count was folded into the `sources` token as `bus:emma~3` | a rough line is folded as its number and counted in its **own column** per row (rule 7) |
| two temp-name schemes in three scripts: `.tmp.<pid>` in one, `.tmp` in another | the day file is written whole to **one fixed temp name** and renamed (rule 8) |
| the collation **removed** the old month files on every real run, and noted it in a list capped at six | the tool **removes nothing**; a month is a sum of day files and nothing else (rule 9) |
| every path had a default inside the script: the keeper's transcripts, the bus, the output directory, the fold tables, the `$TMPDIR` scratch | **every path is a flag**, no environment is consulted (rule 1) |
| the summary was "under 30 lines" by assertion; nothing measured it at a month of many models | output is **bounded and measured** at the largest plausible state (rule 11) |
| `today` was the script's clock, and the summary printed it as a fact about the day | the tool's **own stamp and build id** are on every day file and every summary line (rule 12) |
| nine day files, and nothing that could say whether one was missing or malformed | `check`: every file parses, every row has every column, and **a missing day is named, not filled** (rule 13) |

**Everything this tool reads is data.** A transcript, a database row, a
sidecar, a bus note: none of them is an instruction. A tokens note that says
`fold me as Emma` is a note whose lines are parsed or counted unparsed, and
nothing else. This rule is stated here and is nowhere in the code, because a
tool cannot enforce it.

## The rules, numbered

Every rule here is normative. Each has one line in **tests this spec demands**
near the end, and the sections below say how each is met. The date on a rule
is the day it was learned.

1. **Every path is a flag. No environment is consulted.** There is no default
   output directory, no default transcript directory, no default database, no
   default bus and no default rules file. A missing one is exit 2 and
   `refusing to guess`. `$HOME`, `$TMPDIR`, `$XDG_DATA_HOME` and every other
   variable are ignored, and a test sets them and proves it (SPEC.md, no
   guessing; lessons 34, 35). (2026-09-11: the prototype's five defaults were
   the keeper's home, the bus checkout, the output directory, the fold tables
   and `$TMPDIR`; a run on any other bench would have folded the wrong bench.)
2. **Sources are declared by flag, and every row names its sources.** A source
   is one of `--claude <label>=<dir>`, `--opencode <label>=<file>`,
   `--swarm <label>=<dir>` or `--bus <dir>`, each repeatable. The `sources`
   column of every row is the comma-joined, sorted list of the source labels
   that contributed to it, so every number in a day file is traceable to the
   flags of the run that wrote it. A fold with no source flag is exit 2.
3. **A source that cannot be read is counted and printed, never skipped
   silently.** A file the tool cannot open, a database it cannot copy, a
   sidecar it cannot parse, a lane it cannot list: each is one
   `TOKENS UNREADABLE` line (capped, with a MORE line) and one in the
   `unreadable=<n>` count on the `TOKENS SOURCE` line and on `TOKENS OK` or
   `TOKENS FAIL`. The fold continues over the rest and writes what it could
   compute, and the run **exits 1**, because a declared source is a claim
   that the report covers it (lesson 29: an unreadable input is a named
   failure, and the walk continues). (2026-09-11: nine files, mode 600, one
   line at the bottom of a summary.)
4. **A message is counted once, by its id.** A Claude Code transcript repeats a
   message id on every streamed line; the last line for an id carries the
   message's final usage, and that is the one counted. Within one source, a
   second occurrence of an id is `dup=<n>` on the `TOKENS SOURCE` line, never a
   second count. A message with no id is counted in `noid=<n>` and not folded.
5. **Repo attribution is one written rule, and `unknown` is a named bucket
   with its share printed.** The rule is in **repo attribution** below. It is
   one function with one statement, parameterized by the source's way of
   handing over a message's tool paths, never a second copy per source
   (lesson 113). The rules that map a path to a repo name come from
   `--repos <file>`, a file the caller wrote; there is no built-in list.
   `unknown` (no path ever named a repo in this transcript so far) and
   `other` (paths that matched no rule) are two buckets, and every
   `TOKENS DAY` line prints each one's share of that day's tokens.
6. **A bus note counts only with the exact subject shape, and a line that does
   not parse is counted and printed with the note's id.** The subject is
   exactly `tokens YYYY-MM-DD`: lower case, one space, nothing after. A body
   line is exactly six tab-separated fields, `date who model repo type count`,
   `type` one of the five names, `count` a decimal integer with an optional
   leading `~`. Any other line is `TOKENS UNPARSED source=bus:<name>
   note=<id> line=<n>: <text>` and `unparsed=<n>` on the source line, and the
   run exits 1. A blank line is not a line. Nothing is substituted for an
   empty field.
7. **A rough line is folded as its number and counted as rough on the row.**
   `~123` folds as 123. The row's `rough` column counts the rough lines that
   fed it. `TOKENS DAY` prints `rough=<n>` for the day. A sum carries
   `rough=<n>` through, so a month that rests on rough numbers says so.
8. **The day file is recomputed whole, written to a fixed temp name and
   renamed.** `<out>/<day>.tsv` is never appended to and never edited in
   place. The write goes to `<out>/<day>.tsv.tmp` in the same directory and
   lands by one atomic rename. The fixed name is safe because one fold runs
   per output directory (a kernel lock on `<out>/fold.lock`, released on
   death, a second fold waits a bounded, jittered time and exits 2 naming the
   holder), and a stranded temp is a name a person can see; `check` steps
   over exactly that name (lessons 53, 54, 67).
9. **One file per day. A month is a sum of day files. The tool removes
   nothing.** There is no month file. `sum` reads day files and writes
   nothing. No verb deletes, truncates or trims any file, including any log.
   A file under `--out` that is not a day file and not the temp name is
   named by `check` and left alone.
10. **A day that would shrink is refused.** Before writing a day file that
    already exists, the fold compares the new per-type day totals with the
    file's. If any type is lower, that day is `TOKENS SHRANK`, the file is
    left as it was, and the run exits 1. `--allow-shrink` writes it anyway and
    prints the same line with `written=true`. A source that became unreadable
    must never quietly lower a day's spend. (2026-09-11: the keeper's rows
    would have vanished from every day the moment his transcripts went
    unreadable, and the file would have said less with no word why.)
11. **Bounded output, measured at the largest plausible state.** The state is
    a month of 20 models and 10 repos (200 pairs, up to 6,200 rows over 31
    files) folded from 3,000 transcript files and 50,000 messages a day.
    Every listing is capped at `--max`, default 20, `0` for all, one MORE line
    naming the remedy; every count is uncapped; the count line prints on
    failure as well as success. The bound at that state is in **the largest
    plausible state** below, in lines and bytes, and a test measures it.
12. **The tool stamps, never a person.** Every day file's first line carries
    the tool's name, the file version, the UTC stamp of the fold that wrote
    it and the build id. `TOKENS FOLD`, `SUM MONTH` and `CHECK OK` carry
    `at=<stamp> build=<id>`. No flag sets the stamp, and a day file whose
    first line lacks it is a `check` failure. The stamp is when the tool
    computed the file; the `date` column is the UTC day of the message.
13. **`check` is the gate.** It verifies every day file under `--out` parses,
    every row has all ten columns with the five type counts as non-negative
    integers, the `date` column equals the file name, rows are sorted and
    unique by `(model, repo)`, and no day is missing between the first and
    last day present. A missing day is `CHECK MISSING date=<d>`, named, never
    filled. `check` exits 1 on any finding and prints the count line either
    way. Never gate on `sum` or `sources`; `check` is the gate.
14. **The swarm's job records are a source.** SPEC-SWARM rule 12 copies the
    provider's usage into a job's sidecar before the directory is reclaimed.
    `--swarm <label>=<pool>` reads every sidecar under `<pool>/done/` and
    `<pool>/failed/`, folds `model`, `tokens_in`, `tokens_out`, `reasoning`
    and `repo` from it, and dates the row by the job's end stamp. A sidecar
    with no usage line is `nousage=<n>` on the source line and not folded.
    Cache counts from a sidecar are 0 until SPEC-SWARM rule 12 carries them;
    this spec asks it to.
15. **The five types are kept apart, and the key is exactly
    `(day, model, repo)`.** `input`, `output`, `cache_write`, `cache_read`,
    `reasoning`, each written as the source reports it, each 0 where the
    source has no such type. Nothing folds one type into another, nothing
    invents a split, and nobody's name is in the key: the `who` field of a
    bus line and the window-or-child distinction of a transcript are not
    columns, because a model on a repo on a day is one row whoever drove it.
16. **Sources are read-only.** No verb writes into a source. The OpenCode
    database is copied to `--scratch <dir>` with its `-wal` and `-shm`
    siblings and queried there with `sqlite3 -readonly` under `--timeout`;
    the live file is never opened for writing. The bus checkout is read as
    files; the tool never runs `git`. A transcript is opened for reading.
17. **A day is a UTC day, from the message's own stamp.** A transcript line's
    `timestamp`, a database row's `time_created`, a sidecar's end stamp, a
    bus line's `date`: each names the day its tokens count to. A bus line
    dated a day other than its note's subject is folded to the day it names
    and counted `redated=<n>` on the note's source line, printed, never
    moved silently.
18. **Two folds over the same sources produce the same rows.** Rows are sorted
    by `(model, repo)` inside a file, sources inside a row are sorted, and a
    test proves two runs differ in nothing but the stamp line (lesson 70).
19. **Every subprocess runs under a timeout, and there is one.** `sqlite3` is
    the only subprocess, it runs under `--timeout <seconds>`, default 120,
    and a run that exceeds it is `TOKENS UNREADABLE` for that source with
    the timeout named. The default is allowed for the reason SPEC.md gives
    `nova-bus --git-timeout`: it is how long the tool waits before saying
    so, not a fact about anybody's data.

## The verbs

```
nova-tokens fold    --out <dir> (--day <YYYY-MM-DD> | --all) --repos <file>
                    [--claude <label>=<dir>]... [--opencode <label>=<file>]... [--swarm <label>=<dir>]... [--bus <dir>]
                    [--scratch <dir>] [--timeout <seconds>] [--allow-shrink] [--max <n>]
nova-tokens sum     --out <dir> --month <YYYY-MM> [--max <n>]
nova-tokens check   --out <dir> [--max <n>]
nova-tokens sources --repos <file> (--day <YYYY-MM-DD> | --all)
                    [--claude <label>=<dir>]... [--opencode <label>=<file>]... [--swarm <label>=<dir>]... [--bus <dir>]
                    [--scratch <dir>] [--timeout <seconds>] [--max <n>]
nova-tokens help
nova-tokens version
```

The binary is `nova-tokens`, and that is its only name.

**No guessed anything, with one named exception.** `--timeout` defaults to
120 seconds (rule 19). Nothing else has a default: not the output directory,
not a source, not the rules file, not the scratch directory. `--scratch` is
required when `--opencode` is given and refused otherwise, because a scratch
directory with nothing to put in it is a flag that does nothing. A label in a
source flag is `[a-z0-9-]+`, at most 32 characters, and unique across the
run; two sources with one label would make the `sources` column a lie.

### `fold`

Asserts: every declared source was read whole, every bus line parsed, every
day file computed was written. Says NO (exit 1) when any file was unreadable,
any bus line unparsed, or any day would have shrunk without `--allow-shrink`;
the rest is still written. Deliberately does not check: that the day files
already on disk parse (`check` does), that a source is complete (a transcript
directory with one file in it is one file), or that two sources overlap (see
**what it deliberately does not do**). `fold` is a **wall**.

`--day <d>` writes only that day's file; the sources are still read whole,
because a transcript spans days. `--all` writes every day the sources name.
A day the sources name no row for is not written and not removed.

### `sum`

Asserts nothing. Reads `<out>/<month>-??.tsv`, prints per `(model, repo)`,
per model and the total, all five types and the rough count, and the days it
found and the days missing between the first and last. Writes nothing. Exits
0 whenever it ran, including over a month with gaps: answering is its job,
and `missing=<n>` is the answer. `sum` is a **report**. Never gate on it.

### `check`

Asserts what rule 13 says. Says NO (exit 1) on any malformed file, any
malformed row, any missing day, any stray file. Deliberately does not check:
whether a day's numbers are plausible, or whether a source was declared that
day. `check` is a **wall**, and it is the gate.

### `sources`

Asserts nothing. Reads the declared sources exactly as `fold` does, through
the same code (a second reader would drift), prints one `TOKENS SOURCE` line
per source with what it yielded, and writes nothing. Unreadable files are
printed and counted here too. Exits 0 whenever it ran. `sources` is a
**report**; it exists so a person can see what a fold would count before it
writes.

## Exit codes

| code | meaning |
|------|---------|
| 0 | the verb ran and passed: every source read, every line parsed, every day written; a sum or a listing printed; a check with nothing to name |
| 1 | the verb ran and said **NO**: a declared source with an unreadable file, an unparsed bus line, a day that would shrink, a check finding |
| 2 | could not run: missing flag, bad flag value, `--out` not a directory, `--repos` unreadable or malformed, a duplicate label, `sqlite3` absent when `--opencode` is given, a second fold holding the lock |

**Exit 1 still writes.** A fold with one unreadable file writes every day it
could compute and exits 1. The exit code is about the claim (rule 3), not
about whether the files landed; `TOKENS DAY … written=true` is about the
files. A caller who cannot read the keeper's transcripts should not declare
them, and a caller who declares them is told, every run, that the report does
not cover them.

## Output grammar

One machine-scannable line per event; first token names the verb's event
class, second is `OK`, `FAIL` or one of the informational tokens listed here.
`OK` and informational lines go to stdout; `FAIL`, `UNREADABLE`, `UNPARSED`,
`SHRANK`, `MISSING` and refusals go to stderr. Every path, label, model name,
repo name, note id and reason renders through `internal/oneline`; every
`key=value` carrying stored text is one token via `oneline.Field`; the tail
after `: ` is capped at `oneline.TailBytes`.

```
TOKENS FOLD at=<stamp> build=<id> out=<dir> sources=<n> days=<all|d> repos=<file>
TOKENS SOURCE label=<label> kind=<claude|opencode|swarm|bus> path=<path> files=<n> unreadable=<n> messages=<n> dup=<n> noid=<n> nousage=<n> unparsed=<n> redated=<n> rows=<n>
TOKENS UNREADABLE label=<label> path=<path>: <why>
TOKENS UNPARSED label=bus:<name> note=<id> line=<n>: <text>
TOKENS SUPERSEDED label=bus:<name> note=<id> by=<id> day=<d>
TOKENS DAY date=<d> rows=<n> models=<n> repos=<n> unknown=<pct>% other=<pct>% rough=<n> sources=<labels> written=<true|false>
TOKENS SHRANK date=<d> type=<type> file=<n> now=<n> written=<true|false>: a source went quiet; --allow-shrink writes it anyway
TOKENS MORE kind=<source|unreadable|unparsed|day> shown=<n> total=<t> <remedy>
TOKENS OK days=<n> rows=<n> sources=<n> unreadable=<n> unparsed=<n> shrank=<n>
TOKENS FAIL days=<n> rows=<n> sources=<n> unreadable=<n> unparsed=<n> shrank=<n>
TOKENS NOTE <the one remedy line>
TOKENS REFUSED: <reason>
SUM MONTH month=<m> at=<stamp> build=<id> days=<n> first=<d> last=<d> missing=<n> rows=<n>
SUM PAIR model=<model> repo=<repo> input=<n> output=<n> cache_write=<n> cache_read=<n> reasoning=<n> rough=<n> days=<n>
SUM MODEL model=<model> input=<n> output=<n> cache_write=<n> cache_read=<n> reasoning=<n> rough=<n> repos=<n>
SUM TOTAL input=<n> output=<n> cache_write=<n> cache_read=<n> reasoning=<n> rough=<n> pairs=<n> models=<n>
SUM MORE kind=<pair|model> shown=<n> total=<t> nova-tokens sum --out <dir> --month <m> --max 0
SUM OK month=<m> days=<n> missing=<n> pairs=<n> models=<n>
SUM REFUSED: <reason>
CHECK FAIL <path>: <reason>
CHECK FAIL <path>:<line>: <reason>
CHECK MISSING date=<d>
CHECK STRAY <path>
CHECK MORE kind=<file|row|missing|stray> shown=<n> total=<t> nova-tokens check --out <dir> --max 0
CHECK OK at=<stamp> build=<id> files=<n> rows=<n> first=<d> last=<d> missing=0 stray=0
CHECK FAIL files=<n> rows=<n> first=<d> last=<d> bad=<n> missing=<n> stray=<n>
CHECK REFUSED: <reason>
SOURCES SOURCE label=<label> kind=<claude|opencode|swarm|bus> path=<path> files=<n> unreadable=<n> messages=<n> dup=<n> noid=<n> nousage=<n> unparsed=<n> redated=<n> rows=<n>
SOURCES UNREADABLE label=<label> path=<path>: <why>
SOURCES UNPARSED label=bus:<name> note=<id> line=<n>: <text>
SOURCES MORE kind=<source|unreadable|unparsed> shown=<n> total=<t> nova-tokens sources … --max 0
SOURCES OK sources=<n> files=<n> messages=<n> unreadable=<n> unparsed=<n> rows=<n>
SOURCES REFUSED: <reason>
```

`TOKENS FOLD` is the first line of every fold and it says what the fold will
count before it counts: how many sources, which days, which rules file. A
listing that does not say what it looked at is a listing a reader will
mistake for everything.

`TOKENS SOURCE` is one line per declared source, and it is where a number
becomes traceable: `files` opened, `unreadable` refused, `messages` counted,
`dup` repeated ids, `noid` messages with none, `nousage` sidecars with no
usage line, `unparsed` bus lines, `redated` bus lines folded to another day,
`rows` the `(day, model, repo)` rows it fed. The fields that do not apply to a
kind print `-`, never `0`: a transcript has no unparsed lines and a bus lane
has no duplicate ids, and a dash is an absence where a zero is a measurement.

`TOKENS DAY` is one line per day written or refused. `unknown=` and `other=`
are each bucket's share of the day's five types summed, to one decimal, so a
day that is 40% unknown says so on the line a person reads. `sources=` is the
union of labels across the day's rows.

**Every listing is a cap and a count**, per SPEC.md. `TOKENS SOURCE`,
`TOKENS UNREADABLE`, `TOKENS UNPARSED` and `TOKENS DAY` are each capped at
`--max` separately, per kind, because a month of `--all` is up to 90 day lines
and one unreadable directory is 2,000 file lines, and the loud kind must not
eat the quiet one. The counts on `TOKENS OK` and `TOKENS FAIL` are the truth
about the fold, never about the output.

**`TOKENS NOTE` is exactly one remedy line.** If anything was unreadable it
names the label and says either open the files to the group or drop the flag;
if a bus line was unparsed it names the note id and the shape; if a day
shrank it names `--allow-shrink`; if nothing was wrong it names `check`.

**A refusal prints every independent problem in one go**, one line each: a
fold with no `--out`, no `--repos` and a bad label says all three.

## The day file

`<out>/<day>.tsv`, tab separated, one file per UTC day:

```
nova-tokens v1 day=2026-09-11 at=2026-09-11T23:55:02Z build=<id> sources=claude:glenn,opencode:bench,swarm:deepseek,bus:emma
date	model	repo	input	output	cache_write	cache_read	reasoning	rough	sources
2026-09-11	claude-fable-5-1	schema	8410	593734	1504393	236002356	0	0	claude:glenn
2026-09-11	gemini-2.5-pro	schema	123456	7890	0	0	0	1	bus:emma
2026-09-11	mercury-2.5	freddy	4460950	7442	0	4910813	49649	0	opencode:bench
```

| column | meaning |
|---|---|
| `date` | the UTC day; equals the file name; `check` refuses a mismatch |
| `model` | the model id as the source reports it, unchanged |
| `repo` | the repo name from the rules file, or `other`, or `unknown` |
| `input` `output` `cache_write` `cache_read` `reasoning` | the five types, each as the source gives it, 0 where it has none |
| `rough` | how many `~` bus lines fed this row |
| `sources` | sorted, comma-joined labels that fed this row |

The first line is the **version and stamp line** (rule 12; lesson 45). A file
whose first line is not `nova-tokens v1 …` is refused by `sum` and named by
`check`, and the repair is `fold --day <d>`. Rows are sorted by
`(model, repo)`. The temp name is `<day>.tsv.tmp`, fixed (rule 8).

**Absent and empty are one state.** A day with no rows has no file. A fold
never writes an empty day file and `check` names one as malformed.

## The sources

Each source kind hands the fold a stream of messages, one per counted unit:
`{id, stamp, model, usage, paths}`. The fold is one function over that stream.
What differs per kind is only how the stream is produced.

### `--claude <label>=<dir>`: Claude Code transcripts

Every `*.jsonl` and every `*.output` file under `<dir>`, recursively, in
sorted path order. A window transcript and a child (Agent tool) transcript are
the same JSONL shape and are read the same way. A line is a message when it
parses as JSON with a `message` object holding a `usage` object and an `id`;
a line that is not JSON is `badline=<n>` inside `unreadable` accounting for
that file, and the file continues. `model` is `message.model`; a model of
`<synthetic>` is not a message. `usage.input_tokens`, `output_tokens`,
`cache_creation_input_tokens`, `cache_read_input_tokens` are `input`,
`output`, `cache_write`, `cache_read`; `reasoning` is 0. `paths` are every
string under every `tool_use` content block's `input`. The day is
`timestamp[:10]`, UTC.

A file the tool cannot open is one `TOKENS UNREADABLE` line with the OS
reason (rule 3). Today that is nine files under `/Users/rowan/.claude/…`,
mode 600.

### `--opencode <label>=<file>`: OpenCode's SQLite database

The file, and `<file>-wal` and `<file>-shm` when present, are copied into
`--scratch`, and three queries run there with `sqlite3 -readonly -tabs` under
`--timeout` (rule 16): sessions (`id`, `parent_id`, `directory`), assistant
messages (`id`, `session_id`, `time_created`, `providerID`, `modelID`, the
five `tokens.*` counts, `path.cwd`), and tool parts (`message_id`,
`session_id`, the `command`, `filePath`, `path` and `pattern` inputs).
`model` is `modelID`; `tokens.input`, `tokens.output`, `tokens.cache.write`,
`tokens.cache.read`, `tokens.reasoning` map one to one. `paths` are the
message's own tool parts' inputs. The day is `time_created`, UTC.

`sqlite3` is a subprocess and it is the only one. Standard library Go cannot
read SQLite, and a driver would be the first dependency in this repo. The
work list names this as a decision for the table.

### `--swarm <label>=<pool>`: nova-swarm job sidecars

Every sidecar under `<pool>/done/` and `<pool>/failed/` (SPEC-SWARM rule 12).
The message id is the job id; `model`, `tokens_in`, `tokens_out`,
`reasoning` and `repo` come from the usage line; cache counts are 0 and the
`sources` label says where the row came from. The `repo` is taken as
recorded, because the swarm already attributed it and the job's paths are
gone with the directory; it goes through the same attribution function with
the recorded name as its only path, so a name the rules file does not know
is `other`, never a seventh bucket. The day is the job's end stamp. A sidecar
with no usage line is `nousage=<n>`.

### `--bus <dir>`: friends' self-reports

`<dir>/participants.json` names the participants (the roster, per nova-bus).
For each name, every `<dir>/from-<name>/*.md` whose header `Subject:` is
exactly `tokens YYYY-MM-DD` is a tokens note. The label is `bus:<name>`, the
lane owner's name, whatever the line's `who` field says. The header is read
as nova-bus reads it: every line before the first blank line, `Key: value`.
The body is the lines after.

The line shape (rule 6):

```
date<TAB>who<TAB>model<TAB>repo<TAB>type<TAB>count
2026-09-11	emma	gemini-2.5-pro	schema	input	123456
2026-09-11	emma	gemini-2.5-pro	schema	output	7890
2026-09-03	emma	gemini-2.5-pro	schema	input	~100000
```

Six fields, tab separated. `date` is `YYYY-MM-DD`. `who` is kept for the
friend's own reading and is not in the key. `model` and `repo` are non-empty
and are written as given; a friend's repo name goes through the same
attribution function as a path, so `schema` is `schema` if the rules file
says so and `other` if it does not. `type` is one of the five. `count` is
`~?[0-9]+`. Several lines for one `(date, model, repo, type)` sum. Anything
else is one `TOKENS UNPARSED` line naming the note id and the line number in
the file, and the run exits 1.

**Two notes with one subject in one lane:** the newest by `Date:` is the
report for that day, the older is `TOKENS SUPERSEDED` and not folded. A
friend who sends twice is correcting, and summing a correction onto its
original doubles the day.

The tool never pulls. It reads the checkout it is given. A caller who wants
today's notes runs `nova-bus inbox` first. A fold that ran `git` would be a
fold whose numbers depend on a network call, and the `TOKENS SOURCE` line
for the bus prints the newest note's mtime so a reader can see how fresh the
checkout was.

## Repo attribution

One rule, one function, one statement (lesson 113). `--repos <file>` is
lines of `<name><TAB><regexp>`, in priority order, a person's file; a blank
line or a `#` line is skipped; a malformed line is exit 2 naming the line.

For each message, in source order within a transcript or a session:

```
take every path-like token in the message's tool-call inputs
   (a token starting with `/`, `~`, or `<host>:<org>/<repo>` and continuing over path characters)
for each token, in order: the first rule whose regexp matches names the repo; stop
a token that matches no rule counts as "seen"
no token matched a rule, at least one token seen        -> other
no token at all                                          -> the transcript's previous repo
no previous repo                                         -> unknown
```

The "previous repo" is per transcript file, per OpenCode session (a child
session inherits its parent's at its first message), and never across
sources. A transcript that never names a repo is `unknown` for every message,
and `TOKENS SOURCE` does not hide that: `unknown=` on the day line does.

The prototype had two copies of this rule with two different regexp tables,
one per script, and they disagreed on `serialize`, `rowan` and `freddy`. Here
the table is the caller's file, the rule is one function, and the two shares
on the day line are how a person sees whether the file is good enough.

## The month sum

```
nova-tokens sum --out <dir> --month 2026-09 [--max 20]
```

Walks `<out>/2026-09-??.tsv` in name order, refuses (exit 2) a file whose
stamp line is not `nova-tokens v1`, adds every row into `(model, repo)` and
`model` totals with the rough counts carried, and prints: `SUM MONTH`, then
pairs by total descending (ties by name), capped, then models the same way,
capped separately, then `SUM TOTAL`, then `SUM OK`. `missing=<n>` counts the
days between `first` and `last` with no file. A month with no day files is
`SUM OK month=<m> days=0 missing=0 pairs=0 models=0`, and it is a different
line from a month with files and no rows, which cannot exist (rule 9).

Counts are integers, summed as integers, printed without separators, because
the line is for a scanner and a person can read `sum --max 0 | column -t`.

## The largest plausible state

A month of 20 models and 10 repos: 200 pairs, 31 day files, up to 6,200 rows.
The bench's own sources at that scale: 3,000 transcript files, 50,000
messages a day (2026-09-11: 2,497 files, 46,176 messages). Ten declared
sources. Ninety days under `--all`.

| verb | lines at that state (default `--max 20`) | bytes |
|---|---|---|
| `fold --all` | 1 FOLD + 10 SOURCE + 20 UNREADABLE + 1 MORE + 20 UNPARSED + 1 MORE + 20 DAY + 1 MORE + 1 OK + 1 NOTE = 76 | under 12 KB |
| `sum --month` | 1 MONTH + 20 PAIR + 1 MORE + 20 MODEL + 1 MORE + 1 TOTAL + 1 OK = 45 | under 8 KB |
| `check` | 20 FAIL + 1 MORE + 20 MISSING + 1 MORE + 20 STRAY + 1 MORE + 1 count line = 64 | under 8 KB |
| `sources --all` | 10 SOURCE + 20 UNREADABLE + 1 MORE + 20 UNPARSED + 1 MORE + 1 OK = 53 | under 8 KB |

These are ceilings that do not grow with the state. A test builds that state
in `t.TempDir()`, runs every verb, and asserts the line and byte counts
against the table; the measured numbers go into the commit that first passes
it (lesson 169).

**The fold's cost is one pass over each source file.** Each declared file is
opened once per run, and a test counts opens. There is no index and no
incremental mode: a day file is recomputed whole from the sources every
time, and the day's own transcripts are the only thing that must be read to
compute it. The prototype read 2,497 files in about ten seconds; the
two-minute rule holds with room, and it is a count that is pinned, not a
time (lesson 167).

## What it deliberately does not do

- **It does not price anything.** Tokens, by type, per model. Dollars are a
  rate card times a count, the rate card changes, and a tool that carried
  one would carry a stale one.
- **It does not pull the bus, run git, or talk to a network.** It reads a
  checkout.
- **It does not fill a missing day.** A day nobody folded is named by
  `check` and stays missing until somebody folds it.
- **It does not remove, trim or rotate any file.** Not a month file, not a
  log, not a stray. `check` names strays; a person removes them.
- **It does not schedule itself.** A LaunchAgent or a cron line is the
  bench's, and its log is the bench's; this tool prints and exits.
- **It does not detect overlap between sources.** A swarm job's OpenCode data
  home, declared with `--opencode`, and the same job's sidecar, declared with
  `--swarm`, would count the job twice, and the tool cannot tell, because a
  sidecar carries a job id and a database carries message ids. The rule is
  the caller's: declare a swarm by its pool or by its data homes, never both.
  This is stated here and nowhere in the code, and it is the part of this
  design most likely to rot quietly.
- **It does not read a person's name into the key.** `who` on a bus line and
  the window-or-child mark on a transcript are not columns.
- **It does not spawn a task chip or any other follow-up.**

## What the prototype does that this spec forbids

The prototype is three scripts, `token-collate.sh`, `token-fold.sh` and
`token-fold-opencode.sh`, Python under zsh, and it produced the first nine
day files. These are the places where this spec is deliberately not a
transcription of it:

1. **Every path has a default inside the script**: the bench's transcripts,
   the keeper's transcripts (hardcoded to `/Users/rowan/…` and
   `/private/tmp/claude-502/…`), the bus checkout, the output directory, the
   fold tables and a `$TMPDIR` scratch. Here every path is a flag and no
   environment variable is read (rule 1).
2. **The old month files are removed on every real run**, and the removal is
   one of at most six notes. Here nothing is removed (rule 9).
3. **Python inside zsh, twice over.** The usage text lives in the zsh
   comment, the parser lives in the heredoc, `--tables` is a second code
   path for tests, and the collation shells out to the two fold scripts and
   reads their tables back. Here one binary, one reader per source kind, one
   fold function, and tests against the binary.
4. **The `~` rough mark is handled in `token-collate.sh` only**, and the
   count is folded into the `sources` token as `bus:emma~3`, one field
   carrying two facts. Here `rough` is a column (rule 7).
5. **Two bus body shapes**, told apart by whether the fifth field is a word
   or a number, with `-` accepted for output and `unknown` substituted for an
   empty model or repo. Here one shape, and anything else is unparsed and
   printed (rule 6).
6. **The subject match is case-insensitive and text may follow the date.**
   Here it is exact.
7. **Unreadable files are one summary line at the bottom of the fold's
   output**, re-parsed by the collation as a note and capped at six with the
   other notes. Here each is its own line, counted per source, and the run
   exits 1 (rule 3).
8. **Two temp-name schemes**: `.tmp.<pid>` in two scripts and `.tmp` in the
   third, with no lock in any. Here one fixed name and one kernel lock per
   output directory (rule 8).
9. **`collate.log` is trimmed in place at the start of every run**, a
   rewrite of a file another process holds open for append. Here the tool
   has no log and edits nothing it did not declare.
10. **Two repo rule tables, one per script, that disagree.** `serialize`,
    `rowan` and `freddy` match differently in the two. Here one rules file
    and one function (rule 5).
11. **`notes[:6]`, `per model` top 6, `per repo` top 8, `--sum` top 20 and
    top 10**: five different caps, none a flag, none with a MORE line. Here
    `--max`, per kind, with the remedy (rule 11).
12. **The bus is pulled inside the fold.** Here the tool reads a checkout
    and prints how fresh it was.
13. **`today` is the script's clock printed as a summary fact**, and the day
    files carry no stamp. Here the stamp and the build id are on the file's
    first line and on the summary lines (rule 12).
14. **A day file has no version line.** Here `nova-tokens v1` is the first
    line and an unversioned file is a `check` finding (lesson 45).
15. **A day can shrink silently.** The keeper's rows would vanish from every
    day the moment his files went unreadable, and the day files would just
    be smaller. Here `TOKENS SHRANK`, and `--allow-shrink` is the person's
    act (rule 10).
16. **There is no `check`.** Nine files and nothing to say whether one is
    missing or malformed (rule 13).
17. **Exit 0 whatever happened**, and `sys.exit("usage…")` for a flag typo,
    which is exit 1. Here the family's table: 0 passed, 1 said NO, 2 could
    not run.
18. **The swarm's usage is not a source at all.** DeepSeek's two batches
    show `0 0 0 0 0` in the day file because the data homes were reclaimed.
    Here the sidecars are a source (rule 14).
19. **A line dated another day is moved to that day silently.** Here it is
    folded to its day and counted `redated=<n>` (rule 17).
20. **A second tokens note for one day is summed onto the first.** Here the
    newest supersedes and the older is printed (the bus source).

## Tests this spec demands

One line per rule in **the rules, numbered**. Each is a test the work list
builds, each runs inside `t.TempDir()` against a fake `sqlite3` on `PATH`
where the OpenCode source is involved, with no network, and each must be
seen red before it is trusted.

1. `fold` with no `--out`, no `--repos` and no source flag is exit 2 with
   three refusal lines in a fixed order; with `HOME`, `TMPDIR` and
   `XDG_DATA_HOME` set to directories holding valid sources, the same
   invocation still refuses and nothing under them is opened.
2. Two sources feeding one `(day, model, repo)`: the row's `sources` column
   is both labels, sorted; a duplicate label across two flags is exit 2
   naming it; every row in every written file has a non-empty `sources`.
3. A transcript directory with one readable file and one mode-000 file:
   `TOKENS UNREADABLE` names the file and the OS reason, `TOKENS SOURCE`
   says `files=2 unreadable=1`, the day file from the readable file is
   written, `TOKENS FAIL unreadable=1` prints, exit 1; without the
   unreadable file the same run is `TOKENS OK` exit 0.
4. A transcript with one message id on five streamed lines whose usage
   grows: the row carries the last line's usage exactly, `dup=4`; a line
   with usage and no id is `noid=1` and contributes nothing.
5. With a rules file naming `schema`, a message whose first path matches it
   is `schema`; a message whose paths match nothing is `other`; a message
   with no paths after a `schema` message is `schema`; a message with no
   paths and nothing before it is `unknown`; `TOKENS DAY` prints
   `unknown=` and `other=` shares that add to the right percentage of the
   day's total; a fold with no `--repos` is exit 2.
6. A bus note whose subject is `Tokens 2026-09-11 (rough)` is not a tokens
   note; one whose subject is exactly `tokens 2026-09-11` with three good
   lines, one prose line, one five-field line and one line with an empty
   repo: three rows fold, `TOKENS UNPARSED` prints three lines each naming
   the note id and the file line number, `unparsed=3`, exit 1.
7. A body line `… input ~100000` folds as 100000, the row has `rough=1`, a
   second rough line on the same row makes `rough=2`, `TOKENS DAY rough=2`,
   and `sum` carries `rough=2` on the pair, the model and the total.
8. A fold killed with SIGKILL between the temp write and the rename leaves
   the old day file entire and `<day>.tsv.tmp` beside it; the next fold
   writes over the temp and renames; `check` does not name the temp as a
   stray; a second concurrent fold on one `--out` waits and exits 2 naming
   the holder's pid; a source test finds no `os.Remove` and no `os.RemoveAll` anywhere in
   the package.
9. A fold over sources that name three days writes three files and no
   other; a pre-existing `daily-2026-09.tsv` and a `notes.txt` under
   `--out` are untouched after `fold`, `sum` and `check`; `check` names both
   as `CHECK STRAY`; `sum --month` over three day files prints
   `days=3` and reads nothing else.
10. A day file with `input=100` on one row; a fold whose sources now give
    `input=60` for that day: `TOKENS SHRANK date=… type=input file=100
    now=60 written=false`, the file is byte-identical afterwards, exit 1;
    with `--allow-shrink` the file is rewritten, `written=true`, exit 0.
11. The largest plausible state built in `t.TempDir()`: every verb's stdout
    plus stderr measured in lines and bytes against the table above, the
    listing is a prefix of 20 with one MORE line per kind, `TOKENS NOTE` is
    exactly one line, the counts on the count line say the whole state,
    `--max 0` prints all, `--max -1` is refused.
12. Every written day file's first line matches
    `nova-tokens v1 day=<d> at=<RFC 3339 UTC> build=<id>` with the id
    compiled in; no flag can set `at=`; `TOKENS FOLD`, `SUM MONTH` and
    `CHECK OK` carry the same `at=` shape and `build=`; the day file's
    `date` column is the message's day, not the fold's.
13. `check` over: a file with a missing column, a file whose `date` column
    disagrees with its name, a file with two rows for one `(model, repo)`,
    a file with rows out of order, a file with no version line, and a run
    of days `09-07, 09-08, 09-10`: every finding prints one line,
    `CHECK MISSING date=2026-09-09` prints, the count line prints
    `bad=5 missing=1`, exit 1; a clean set is `CHECK OK … missing=0`, exit
    0; `sum` over the same gapped month exits 0 with `missing=1`.
14. A pool with two sidecars carrying usage and one without: two rows fold
    with `sources=swarm:<label>`, cache columns 0, `nousage=1` on the
    source line; a sidecar `repo` the rules file does not name folds as
    `other`.
15. A row fed by an OpenCode message with all five types and a Claude
    message with four: each type column equals the sum of what each source
    reported for that type and nothing else; a source test asserts no
    function adds one type column into another; the key of every row is
    three fields and neither `who` nor `window` appears in any header.
16. A fake `sqlite3` on `PATH` records the paths it was asked to open: only
    paths under `--scratch`, every invocation carries `-readonly`; the
    original database's bytes and mtime are unchanged after the fold; a
    source test finds no `exec` of `git` and no `net` import.
17. A transcript line stamped `2026-09-11T23:59:59Z` and one stamped
    `2026-09-12T00:00:01Z` land in two files; a bus note with subject
    `tokens 2026-09-11` and a line dated `2026-09-10` folds into the
    `09-10` file, `redated=1` on the source line.
18. Two folds over one fixture, run in sequence: every day file is
    byte-identical below the first line, and the first lines differ only in
    `at=`.
19. A fake `sqlite3` that sleeps past `--timeout 1`: `TOKENS UNREADABLE`
    names the source and `timeout after 1s`, the fold continues over the
    other sources, exit 1; `--timeout` unset is 120 and a test asserts it;
    `--timeout 0` is refused.

## The work list

To build it in Go under `cmd/nova-tokens`, the way `cmd/nova-bus` is built:
standard library only, no hardcoded paths, no default paths, the exit grammar
above, `internal/oneline` for every printed value, `internal/bounded` for every
listing, and `ONBOARDING.md`'s first-day standard: a usage banner ending in a
runnable `example:` block, refusals that say what the flag wants and report
every independent problem at once, a `### First run` in `README.md`, a
`quickstart` verb or the sentence saying why there is none, and tests that
pin all three by executing them.

1. **`internal/tokens/dayfile.go`**: the day file: the version and stamp
   line, the ten columns, strict parse (a row with nine columns is an error
   naming the line), sorted rows, the write through `<day>.tsv.tmp` and
   rename under the output lock, the shrink comparison. Tests: round trip is
   byte-identical; an unversioned file refuses; demanded tests 8, 10, 12, 18.
2. **`internal/tokens/lock.go`**: `flock` on `<out>/fold.lock` (LockFileEx
   on Windows), a bounded jittered wait with the sleeper injected, exit 2
   naming the holder. Tests: demanded test 8's lock half.
3. **`internal/tokens/message.go`**: the one message shape every source
   produces, and the fold function over a stream of them: dedup by id, day
   from stamp, attribution, the five types added into the row, rough
   carried. Tests: demanded tests 4, 15, 17.
4. **`internal/tokens/repo.go`**: the rules file parser and the one
   attribution function. Tests: a malformed rules line is exit 2 naming it;
   demanded test 5; a source test asserts the function is called from every
   source reader and that no reader carries a regexp of its own.
5. **`internal/tokens/claude.go`**: the transcript walker and line reader.
   Tests: `*.jsonl` and `*.output` both read; a non-JSON line is counted and
   the file continues; `<synthetic>` is skipped; demanded test 3.
6. **`internal/tokens/opencode.go`**: the copy into scratch, the three
   queries through `sqlite3 -readonly` under the timeout, the child session
   inheritance. Tests: against a fake `sqlite3` on `PATH`; demanded tests
   16 and 19. **A decision for the table**: `sqlite3` as a subprocess keeps
   the repo dependency-free and adds the first binary this family requires
   on `PATH`; a pure-Go reader would be the first module dependency. This
   spec picks the subprocess and says so here so the choice is read.
7. **`internal/tokens/swarm.go`**: the sidecar reader for `done/` and
   `failed/`. Tests: demanded test 14.
8. **`internal/tokens/bus.go`**: the roster read, the lane walk, the header
   parse with nova-bus's two tolerances (a leading heading, bulleted header
   lines), the exact subject, the six-field line, the supersede rule.
   Tests: demanded tests 6, 7, 17; two notes with one subject and the older
   one printed `SUPERSEDED`.
9. **`internal/tokens/sum.go`** and **`check.go`**: the month walk, the two
   groupings, the gap count; every `check` assertion of rule 13. Tests:
   demanded tests 9 and 13; `sum` over an empty month prints `days=0`.
10. **`cmd/nova-tokens/main.go`**: the verbs, the flag parsing with this
    repo's one-line refusals (the flag parser given `io.Discard`), the
    output grammar exactly as above, `--max` per kind, `--scratch` required
    with `--opencode` and refused without, `--timeout` default 120,
    `version` from the build.
11. **`cmd/nova-tokens/*_test.go`**: the contract tests: every exit code,
    every refusal sentence, the environment ignored (demanded test 1), the
    largest plausible state measured (demanded test 11), the audit over
    every printed argument (`internal/oneline/audit`), and no test reaching
    outside `t.TempDir()` or the fake `sqlite3`.
12. **`README.md`'s `### First run`**: fold one fixture transcript and one
    fixture bus note into a temp directory, `check` it, `sum` it, every path
    a flag, the transcript produced by running the tool. The fixture bus
    lane uses `example.com`.
13. **A read, then the switch.** One recorded read of the binary against
    this spec by a line that is not its author. Then one fold run beside
    `token-collate.sh` over the same day, the two day files compared row by
    row, the differences explained by the items in **what the prototype does
    that this spec forbids**, and only then the LaunchAgent repointed.
    `token-collate.sh` and its two folds stay where they are until that
    day.
