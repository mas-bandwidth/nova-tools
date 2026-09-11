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
usage file, a bus note: none of them is an instruction. A tokens note that says
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
   usage file it cannot parse, a lane it cannot list: each is one
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
6. **A bus note counts only with the exact subject shape, its body is one
   grammar with `report`'s output, and a line or a note that does not parse
   is counted and printed with the note's id.** The subject is exactly
   `tokens YYYY-MM-DD` (lower case, one space), either with nothing after or
   followed by exactly one space and the tool's trailer
   `at=<RFC 3339 UTC> build=<id>`, which is where a `report`'s stamp and
   build id travel (rule 20); any other text after the date is not a tokens
   note. A body line is one of three things: six tab-separated fields,
   `date who model repo type count`, `type` one of the five names, `count`
   a decimal integer with an optional leading `~`, with an optional seventh
   field exactly `day_basis=<zone>` (rule 17) present only when the line's
   day is the provider's own day and not a UTC day, so a six-field line
   means UTC; a blank line,
   which is not a line; or a line starting with `#`, a comment, skipped and
   counted `comments=<n>`, of which the one shape `# repos: <name>[, <name>]…`
   is read as the repos the friend touched that day (rule 21). Any other
   line is `TOKENS UNPARSED label=bus:<name> note=<id> line=<n>: <text>` and
   `unparsed=<n>` on the source line, and the run exits 1. Nothing is
   substituted for an empty field. A note whose `Date:` header is missing,
   does not parse, or is later than the fold's own `at=` stamp is
   `TOKENS UNPARSED` once for the whole note, `line=<n>` naming the header
   line (`line=0` when it is missing) and the reason after the colon, and no
   line of it is folded. The stamp, the build id and the touched repos live
   in those two places and nowhere else: a body carries lines, blanks and
   comments, and the parser that reads it is the serializer that `report`
   writes with (lesson 113).
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
    file's. If any type is lower, or was a number in the file and is a dash
    now (`now=-`: the source that reported it went quiet), that day is
    `TOKENS SHRANK`, the file is left as it was, and the run exits 1. A dash
    in the file that is a number now is not a shrink; it is coverage
    arriving. `--allow-shrink` writes it anyway and
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
    every row has all eleven columns with each of the five type cells either
    a non-negative integer or exactly `-`, never empty, `day_basis` either
    `utc` or a zone name with no whitespace, the `date` column equals the
    file name, rows are sorted and unique by `(model, repo)`, and no day is
    missing between the first and last day present. A missing day is `CHECK MISSING date=<d>`, named, never
    filled. `check` exits 1 on any finding and prints the count line either
    way. Never gate on `sum` or `sources`; `check` is the gate.
14. **The swarm's usage files are a source.** SPEC-SWARM rule 12 writes one
    usage file per job, `<pool>/usage/<job>.tsv`, outside the directory
    `reclaim` removes, before the job's files move. `--swarm <label>=<pool>`
    reads every file under `<pool>/usage/` and nothing under `done/`,
    `failed/` or `running/`; it folds `model`, `repo`, `tokens_in`,
    `tokens_out`, `cache_write`, `cache_read` and `reasoning` from the row,
    dates the row by its `ended` stamp, and takes the job id from `job`. A
    job directory under `done/` or `failed/` with no usage file is
    `nousage=<n>` on the source line and not folded, and a file whose header
    is not the sixteen columns SPEC-SWARM names is refused by name. All five
    types are present; a cell the swarm wrote as `-` (its rule 12: a field
    the provider did not report is a dash, never a zero) stays `-` here and
    is never summed as zero, and a row for a second attempt (`attempt=2`) is
    its own row, because the swarm already keeps one file per attempt.
15. **The five types are kept apart, a type the source did not report is a
    dash, and the key is exactly `(day, model, repo)`.** `input`, `output`,
    `cache_write`, `cache_read`, `reasoning`, each written as the source
    reports it. A type the source did not report is `-` in the cell, never
    `0`, across every source: a transcript whose usage block has no key for
    it, a swarm usage file with no such column or a `-` in it, a bus note with no line
    for that `(model, repo, type)`, an export with no such column. `0` is
    written only when the source reported zero. A provider not exposing
    reasoning or cache usage is not proof that none occurred (SPEC-SWARM
    rule 12; lesson 110), and a zero that means "not measured" would sum
    into a month that claims to be complete. A row fed by two sources is,
    per type, the sum over the sources that reported that type, and `-`
    when none did; which sources report which types is on each
    `TOKENS SOURCE` line as `reports=`, so a mixed row is traceable. `sum`
    prints per type column how many rows were `-` (rule 9's `sum`). Nothing
    folds one type into another, nothing invents a split, and nobody's name
    is in the key: the `who` field of a bus line and the window-or-child
    distinction of a transcript are not columns, because a model on a repo
    on a day is one row whoever drove it.
16. **Sources are read-only.** No verb writes into a source. The OpenCode
    database is copied to `--scratch <dir>` with its `-wal` and `-shm`
    siblings and queried there with `sqlite3 -readonly` under `--timeout`;
    the live file is never opened for writing. The bus checkout is read as
    files; the tool never runs `git`. A transcript is opened for reading.
17. **A day is a UTC day, from the message's own stamp, and a row that is
    not says so.** A transcript line's `timestamp`, a database row's
    `time_created`, a swarm usage file's `ended` stamp, a bus line's `date`: each names
    the day its tokens count to. A bus line dated a day other than its
    note's subject is folded to the day it names and counted `redated=<n>`
    on the note's source line, printed, never moved silently. A provider
    export (rule 21) that carries a timestamp per row is folded to UTC days
    from those timestamps, whatever zone the export prints them in. An
    export that carries only a per-day total in the provider's own zone
    cannot be turned into UTC days by any arithmetic, and copying its date
    into a UTC file would assert a split nobody measured; such a row is
    written under the export's own date with the `day_basis` column set to
    the zone the export declares (`America/Los_Angeles`, `+02:00`, as it
    stands), never `utc`. Every other row is `day_basis=utc`. A bus line
    for such a row carries the zone as its seventh field, `day_basis=<zone>`
    (rule 6), a six-field line is a UTC day, and `report` writes the seventh
    field on exactly the lines whose row it would write under a zone, so
    the basis survives the trip through a friend's note (rule 20). An export
    that declares no zone and no timestamps is `TOKENS UNREADABLE`, never
    assumed UTC. A day, model and repo fed by rows of two different bases
    is `TOKENS MIXED`, not written, exit 1: the caller declares one export
    or the other for that day. `TOKENS DAY` prints `nonutc=<n>` rows and
    `sum` prints how many rows it added were not UTC, so a month that rests
    on a provider's local days says so on every line that adds them.
18. **Two folds over the same sources produce the same rows.** Rows are sorted
    by `(model, repo)` inside a file, sources inside a row are sorted, and a
    test proves two runs differ in nothing but the stamp line (lesson 70).
19. **Every subprocess runs under a timeout, and there are two.** `sqlite3`
    runs under `--timeout <seconds>`, default 120, and a run that exceeds it
    is `TOKENS UNREADABLE` for that source with the timeout named. `git log`
    over the bus checkout, run only to order competing tokens notes (the
    bus source), runs under `--git-timeout <seconds>`, default 60 as
    `nova-bus` has it, and a run that exceeds it is `TOKENS UNPARSED` for
    the note it was placing with `git log: timeout after <n>s` as the
    reason. No other subprocess exists. The defaults are allowed for the
    reason SPEC.md gives `nova-bus --git-timeout`: they are how long the
    tool waits before saying so, not a fact about anybody's data.

20. **A friend on another machine runs `report`, and never types a number.**
    The first user of this tool is not this bench: it is Emma, Johnny or
    Stella on a harness of their own (Glenn, 2026-09-11: "I want our friends
    who are not swarms to use it"). `report` folds that machine's own
    sources for one day, the same sources and the same attribution as
    `fold`, and prints on stdout **exactly** the body lines of rule 6
    (`date who model repo type count`, one line per (model, repo, type) the
    sources reported, `who` from the flag, and the seventh field
    `day_basis=<zone>` on exactly the lines whose row `fold` would write
    under that zone by rule 17, a provider's per-day total) and nothing
    else: no heading, no stamp, no comment. A `report` over a day every
    source dates in UTC prints six-field lines only; a `report` whose
    sources give one `(model, repo)` two bases prints `TOKENS MIXED` on
    stderr, no line for that key, `REPORT FAIL`, exit 1, the refusal `fold`
    makes (rule 17): the basis is never dropped and never guessed. A type the sources did not report has no line, so it
    folds back as `-` (rule 15); a reported zero is a line with `0`. The
    stamp and build id go on the note's Subject as the trailer rule 6
    names, and `report` prints that subject, ready for `nova-bus draft
    --subject`, on its one `REPORT OK who=<name> day=<d> rows=<n> at=<stamp>
    build=<id> subject=<subject>` line, which goes to **stderr** with the
    `TOKENS UNREADABLE` lines: this verb is the one place in the family
    where the OK line leaves stdout, because stdout is the artifact, and
    this sentence is the exception SPEC.md's Conventions allow when a spec
    says so. `--note <path>` writes exactly the stdout bytes to that file.
    A `report` for a day whose sources were all unreadable prints
    `TOKENS UNREADABLE` per source and no lines, `REPORT FAIL`, exit 1: a
    friend with nothing to show says so, never sends zeros. A `report` line
    is what `fold --bus` parses, so the two are one grammar by construction
    (lesson 113), and a `report` never carries `~`: a rough number is a
    person's, typed by hand, and the parser accepts it from a person only.
    A friend who wants the repos touched on the note appends one
    `# repos: …` line to the body by hand; it is the one line a person adds
    to a `report`, and it carries no number.

21. **A harness that shows nothing is counted from the provider's side, and
    never apportioned.** Emma's harness (Antigravity, Gemini) and Johnny's
    (Grok) record no token counts anywhere a tool can read (Emma's read of
    this spec, 2026-09-11). For them the source is `--provider
    <label>=<file>`: a billing export the account holder downloads (Google
    Cloud, xAI), one row per (day, model, type, count) after the tool's
    parser for that provider's shape, with the parser's name in the
    `sources` column. Its repo is the fixed word `unattributed`: the tool
    never splits a provider total across repos by any proportion, because a
    split nobody measured is a number nobody can defend ("never invent a
    split"). The export's rows are dated by rule 17: UTC days from
    timestamps when it has them, its own local day with `day_basis=<zone>`
    when it has only totals, never a silent mix. A type the export does not
    carry is `-` (rule 15). A friend's daily note may still carry the repos
    touched that day, as the one comment shape `# repos: <name>[, <name>]…`
    in the body (rule 6); `fold` records them beside the day as
    `TOKENS TOUCHED label=bus:<name> day=<d> repos=<list>` and adds no
    numbers to them, and a tokens note whose body is only that line and
    blanks is a valid note with zero rows. A `report` on such a harness
    prints `TOKENS UNREADABLE` per source and exits 1 (rule 20), and that
    line plus the daily `# repos:` note is the friend's whole duty. Stella's
    harness (Codex) is the third of these: it exposes no token usage, this
    spec has no Codex adapter, and her spend is counted provider-side from
    the account holder's OpenAI export under the same rule.

## The verbs

```
nova-tokens fold    --out <dir> (--day <YYYY-MM-DD> | --all) --repos <file>
                    [--claude <label>=<dir>]... [--opencode <label>=<file>]... [--swarm <label>=<dir>]... [--bus <dir>]
                    [--provider <label>=<file>]...
                    [--scratch <dir>] [--timeout <seconds>] [--git-timeout <seconds>] [--allow-shrink] [--max <n>]
nova-tokens report  --who <name> --day <YYYY-MM-DD> --repos <file>
                    [--claude <label>=<dir>]... [--opencode <label>=<file>]... [--provider <label>=<file>]...
                    [--note <path>] [--scratch <dir>] [--timeout <seconds>]
nova-tokens sum     --out <dir> --month <YYYY-MM> [--max <n>]
nova-tokens check   --out <dir> [--max <n>]
nova-tokens sources --repos <file> (--day <YYYY-MM-DD> | --all)
                    [--claude <label>=<dir>]... [--opencode <label>=<file>]... [--swarm <label>=<dir>]... [--bus <dir>]
                    [--provider <label>=<file>]...
                    [--scratch <dir>] [--timeout <seconds>] [--git-timeout <seconds>] [--max <n>]
nova-tokens help
nova-tokens version
```

The binary is `nova-tokens`, and that is its only name.

**No guessed anything, with one named exception.** The two timeouts have
defaults, `--timeout` 120 seconds and `--git-timeout` 60 (rule 19), and
`--git-timeout` is refused without `--bus`. Nothing else has a default: not the output directory,
not a source, not the rules file, not the scratch directory. `--scratch` is
required when `--opencode` is given and refused otherwise, because a scratch
directory with nothing to put in it is a flag that does nothing. A label in a
source flag is `[a-z0-9-]+`, at most 32 characters, and unique across the
run; two sources with one label would make the `sources` column a lie.

### `fold`

Asserts: every declared source was read whole, every bus line and note
parsed, no row mixed two day bases, every day file computed was written. Says
NO (exit 1) when any file was unreadable, any bus line or note unparsed, any
row was `TOKENS MIXED`, or any day would have shrunk without `--allow-shrink`;
the rest is still written. Deliberately does not check: that the day files
already on disk parse (`check` does), that a source is complete (a transcript
directory with one file in it is one file), or that two sources overlap (see
**what it deliberately does not do**). `fold` is a **wall**.

`--day <d>` writes only that day's file; the sources are still read whole,
because a transcript spans days. `--all` writes every day the sources name.
A day the sources name no row for is not written and not removed.

### `sum`

Asserts nothing. Reads `<out>/<month>-??.tsv`, prints per `(model, repo)`,
per model and the total, all five types, the rough count, per type column how
many rows added were `-` (`dashes=`, rule 15), how many rows added were not
UTC (`nonutc=`, rule 17), and the days it found and the days missing between
the first and last. A `-` adds nothing and is counted; it is never read as
zero. Writes nothing. Exits
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
| 1 | the verb ran and said **NO**: a declared source with an unreadable file, an unparsed bus line or note, a row of two day bases, a day that would shrink, a check finding, a `report` with nothing to show |
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
`MIXED`, `SHRANK`, `MISSING` and refusals go to stderr. `report` is the one
exception, stated in rule 20: its stdout is exactly rule 6's body lines, and
`REPORT OK`, `REPORT FAIL` and its `TOKENS UNREADABLE` lines go to stderr. Every path, label, model name,
repo name, note id and reason renders through `internal/oneline`; every
`key=value` carrying stored text is one token via `oneline.Field`; the tail
after `: ` is capped at `oneline.TailBytes`.

```
TOKENS FOLD at=<stamp> build=<id> out=<dir> sources=<n> days=<all|d> repos=<file>
TOKENS SOURCE label=<label> kind=<claude|opencode|swarm|bus|provider> path=<path> reports=<types> day_basis=<utc|zone> files=<n> unreadable=<n> messages=<n> dup=<n> noid=<n> nousage=<n> unparsed=<n> comments=<n> redated=<n> superseded=<n> rows=<n>
TOKENS UNREADABLE label=<label> path=<path>: <why>
TOKENS UNPARSED label=bus:<name> note=<id> line=<n>: <text or why>
TOKENS SUPERSEDED label=bus:<name> note=<id> by=<id> day=<d>
TOKENS TOUCHED label=bus:<name> day=<d> repos=<list>
TOKENS MIXED date=<d> model=<model> repo=<repo> bases=<utc,zone>: two day bases on one row; declare one export for that day
TOKENS DAY date=<d> rows=<n> models=<n> repos=<n> unknown=<pct>% other=<pct>% rough=<n> dashes=<n> nonutc=<n> sources=<labels> written=<true|false>
TOKENS SHRANK date=<d> type=<type> file=<n> now=<n|-> written=<true|false>: a source went quiet; --allow-shrink writes it anyway
TOKENS MORE kind=<source|unreadable|unparsed|superseded|touched|mixed|day> shown=<n> total=<t> <remedy>
TOKENS OK days=<n> rows=<n> sources=<n> unreadable=<n> unparsed=<n> mixed=<n> shrank=<n>
TOKENS FAIL days=<n> rows=<n> sources=<n> unreadable=<n> unparsed=<n> mixed=<n> shrank=<n>
TOKENS NOTE <the one remedy line>
TOKENS REFUSED: <reason>
REPORT OK who=<name> day=<d> rows=<n> at=<stamp> build=<id> subject=<subject>
REPORT FAIL who=<name> day=<d> rows=0 unreadable=<n>
REPORT REFUSED: <reason>
SUM MONTH month=<m> at=<stamp> build=<id> days=<n> first=<d> last=<d> missing=<n> rows=<n>
SUM PAIR model=<model> repo=<repo> input=<n> output=<n> cache_write=<n> cache_read=<n> reasoning=<n> rough=<n> dashes=<in>,<out>,<cw>,<cr>,<r> nonutc=<n> days=<n>
SUM MODEL model=<model> input=<n> output=<n> cache_write=<n> cache_read=<n> reasoning=<n> rough=<n> dashes=<in>,<out>,<cw>,<cr>,<r> nonutc=<n> repos=<n>
SUM TOTAL input=<n> output=<n> cache_write=<n> cache_read=<n> reasoning=<n> rough=<n> dashes=<in>,<out>,<cw>,<cr>,<r> nonutc=<n> pairs=<n> models=<n>
SUM MORE kind=<pair|model> shown=<n> total=<t> nova-tokens sum --out <dir> --month <m> --max 0
SUM OK month=<m> days=<n> missing=<n> pairs=<n> models=<n> nonutc=<n>
SUM REFUSED: <reason>
CHECK FAIL <path>: <reason>
CHECK FAIL <path>:<line>: <reason>
CHECK MISSING date=<d>
CHECK STRAY <path>
CHECK MORE kind=<file|row|missing|stray> shown=<n> total=<t> nova-tokens check --out <dir> --max 0
CHECK OK at=<stamp> build=<id> files=<n> rows=<n> first=<d> last=<d> missing=0 stray=0
CHECK FAIL files=<n> rows=<n> first=<d> last=<d> bad=<n> missing=<n> stray=<n>
CHECK REFUSED: <reason>
SOURCES SOURCE label=<label> kind=<claude|opencode|swarm|bus|provider> path=<path> reports=<types> day_basis=<utc|zone> files=<n> unreadable=<n> messages=<n> dup=<n> noid=<n> nousage=<n> unparsed=<n> comments=<n> redated=<n> superseded=<n> rows=<n>
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
`dup` repeated ids, `noid` messages with none, `nousage` swarm job
directories with no usage file, `unparsed` bus lines and whole notes,
`comments` bus `#` lines,
`redated` bus lines folded to another day, `superseded` bus notes a later
note replaced, `rows` the `(day, model, repo)` rows it fed. `reports=` is the
comma-joined list of the five type names this source reports at all
(`input,output,cache_write,cache_read` for a Claude Code transcript;
all five for a swarm usage file; all five for OpenCode; whatever the
export's columns are for a provider; for a bus lane, the types its lines
named), so a reader of a mixed row can see which source could not have
covered which cell (rule 15). `day_basis=` is `utc` for every kind but a
provider export of local-day totals, where it is the export's zone, and a
bus lane whose lines carry a seventh field, where it is that zone, or
`mixed` when one lane's lines carry more than one (rule 17); a lane is
allowed to be mixed across days, a row never. The fields that do not apply to a kind print `-`, never `0`: a
transcript has no unparsed lines and a bus lane has no duplicate ids, and a
dash is an absence where a zero is a measurement. The same rule is why a
type cell in a day file is `-` and never `0` when the source did not report
it.

`TOKENS DAY` is one line per day written or refused. `unknown=` and `other=`
are each bucket's share of the day's five types summed, to one decimal, so a
day that is 40% unknown says so on the line a person reads; a `-` cell adds
nothing to either side of that share. `dashes=` is how many of the day's
type cells are `-` and `nonutc=` how many of its rows carry a `day_basis`
other than `utc`. `sources=` is the union of labels across the day's rows.

**Every listing is a cap and a count**, per SPEC.md. `TOKENS SOURCE`,
`TOKENS UNREADABLE`, `TOKENS UNPARSED`, `TOKENS SUPERSEDED`, `TOKENS TOUCHED`,
`TOKENS MIXED` and `TOKENS DAY` are each capped at `--max` separately, per
kind, because a month of `--all` is up to 90 day lines
and one unreadable directory is 2,000 file lines, and the loud kind must not
eat the quiet one. The counts on `TOKENS OK` and `TOKENS FAIL` are the truth
about the fold, never about the output.

**`TOKENS NOTE` is exactly one remedy line.** If anything was unreadable it
names the label and says either open the files to the group or drop the flag;
if a bus line or note was unparsed it names the note id and the shape; if a
row mixed two day bases it names the two labels; if a day shrank it names
`--allow-shrink`; if nothing was wrong it names `check`.

**A refusal prints every independent problem in one go**, one line each: a
fold with no `--out`, no `--repos` and a bad label says all three.

## The day file

`<out>/<day>.tsv`, tab separated, one file per UTC day:

```
nova-tokens v1 day=2026-09-11 at=2026-09-11T23:55:02Z build=<id> sources=claude:glenn,opencode:bench,swarm:deepseek,bus:emma,google:emma
date	model	repo	input	output	cache_write	cache_read	reasoning	rough	day_basis	sources
2026-09-11	claude-fable-5-1	schema	8410	593734	1504393	236002356	-	0	utc	claude:glenn
2026-09-11	deepseek-v3	serialize	812004	40211	-	-	-	0	utc	swarm:deepseek
2026-09-11	gemini-2.5-pro	schema	123456	7890	-	-	-	1	utc	bus:emma
2026-09-11	gemini-2.5-pro	unattributed	9912340	301122	-	-	-	0	America/Los_Angeles	google:emma
2026-09-11	mercury-2.5	freddy	4460950	7442	0	4910813	49649	0	utc	opencode:bench
```

| column | meaning |
|---|---|
| `date` | the day; equals the file name; `check` refuses a mismatch; a UTC day unless `day_basis` says otherwise |
| `model` | the model id as the source reports it, unchanged |
| `repo` | the repo name from the rules file, or `other`, or `unknown`, or `unattributed` for a provider export |
| `input` `output` `cache_write` `cache_read` `reasoning` | the five types, each as the source reports it; `-` where no source that fed the row reported that type, never `0` (rule 15) |
| `rough` | how many `~` bus lines fed this row |
| `day_basis` | `utc` for a row dated from stamps; the export's own zone for a provider row that is a local-day total (rule 17) |
| `sources` | sorted, comma-joined labels that fed this row |

Eleven columns, every one written on every row. A `-` in a type cell is a
fact about the source ("did not report"), not about the day, and `sum`
counts them beside the totals it prints.

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
`output`, `cache_write`, `cache_read`; a key absent from the usage block is
`-` for that message, and `reasoning` is `-` always, because the transcript
carries no such count (`reports=input,output,cache_write,cache_read`).
`paths` are every string under every `tool_use` content block's `input`. The
day is `timestamp[:10]`, UTC.

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
`tokens.cache.read`, `tokens.reasoning` map one to one, and a column that is
NULL or absent in the row is `-`, never `0` (`reports=` all five). `paths`
are the message's own tool parts' inputs. The day is `time_created`, UTC.

`sqlite3` is a subprocess and it is the only one. Standard library Go cannot
read SQLite, and a driver would be the first dependency in this repo. The
work list names this as a decision for the table.

### `--swarm <label>=<pool>`: nova-swarm usage files

Every `<pool>/usage/<job>.tsv` (SPEC-SWARM rule 12): one header of sixteen
columns and one row per job attempt, written by the swarm's `finalize` before
the job's files move and never rewritten. The message id is `job`; `model`,
`repo`, `tokens_in`, `tokens_out`, `cache_write`, `cache_read` and
`reasoning` come from the row; a cell the swarm wrote as `-` stays `-`
(`reports=input,output,cache_write,cache_read,reasoning`), and the `sources`
label says where the row came from. The `repo` is taken as recorded, because
the swarm already attributed it and the job's paths are gone with the
directory; it goes through the same attribution function with the recorded
name as its only path, so a name the rules file does not know is `other`,
never a seventh bucket. The day is the row's `ended` stamp. A job directory
under `done/` or `failed/` with no usage file is `nousage=<n>`; nothing under
those directories is opened for anything else, so the answer is the same
before and after `reclaim`. A file whose header is not the sixteen names in
order is `TOKENS UNPARSED` naming the file and the first wrong column.

### `--provider <label>=<file>`: a billing export

For a harness that records nothing (rule 21). The file is the provider's own
export, unmodified; the label names the provider and the parser (`google`,
`xai`); an export whose shape the parser does not know is `TOKENS UNREADABLE`
with the first unparsed line quoted, never a guess. Rows land with repo
`unattributed` and the model as the export names it; a type the export has
no column for is `-`, and `reports=` on the source line names the columns it
has. The day is rule 17's: an export with a timestamp per row is folded to
UTC days from the timestamps, in whatever zone they are printed, and its
rows are `day_basis=utc`; an export with only per-day totals is folded under
its own dates with `day_basis=<zone>`, the zone taken from the export's own
declaration and printed on `TOKENS SOURCE`, never from the bench's clock or
a guess; an export with neither timestamps nor a declared zone is
`TOKENS UNREADABLE` naming what it lacks. A `(day, model, repo)` fed by a
`utc` row and a zoned row is `TOKENS MIXED` and not written.

### `--bus <dir>`: friends' self-reports

`<dir>/participants.json` names the participants (the roster, per nova-bus).
For each name, every `<dir>/from-<name>/*.md` whose header `Subject:` is
exactly `tokens YYYY-MM-DD` is a tokens note. The label is `bus:<name>`, the
lane owner's name, whatever the line's `who` field says. The header is read
as nova-bus reads it: every line before the first blank line, `Key: value`.
The body is the lines after.

The line shape (rule 6):

```
date<TAB>who<TAB>model<TAB>repo<TAB>type<TAB>count[<TAB>day_basis=<zone>]
2026-09-11	emma	gemini-2.5-pro	schema	input	123456
2026-09-11	emma	gemini-2.5-pro	schema	output	7890
2026-09-03	emma	gemini-2.5-pro	schema	input	~100000
2026-09-11	emma	gemini-2.5-pro	unattributed	input	123456	day_basis=America/Los_Angeles
```

Six fields, tab separated, with an optional seventh. `date` is `YYYY-MM-DD`.
The seventh field, when present, is exactly `day_basis=<zone>`, the zone as
rule 13 accepts it in the day file (no whitespace) and never `utc`: it puts
the line's row under the line's date with that `day_basis`, which is how a
provider's local-day total (rule 17) crosses the bus without being called
UTC. A line without it is a UTC day. `day_basis=utc` spelled out is
`TOKENS UNPARSED` (the six-field line already says UTC, and two spellings of
one fact would be two grammars), as is any seventh field that is not
`day_basis=` or any eighth field. Lines for one `(date, model, repo)` with
two bases are `TOKENS MIXED` for that row (rule 17). `who` is kept for the
friend's own reading and is not in the key. `model` and `repo` are non-empty
and are written as given; a friend's repo name goes through the same
attribution function as a path, so `schema` is `schema` if the rules file
says so and `other` if it does not. `type` is one of the five. `count` is
`~?[0-9]+`. Several lines for one `(date, model, repo, type)` sum. A type no
line named for a `(date, model, repo)` is `-` in the row (rule 15). A blank
line is skipped. A `#` line is a comment, skipped and counted; the one
comment shape `# repos: <name>[, <name>]…` (the prefix exact, names
`[a-z0-9._-]+` separated by a comma and optional spaces) yields
`TOKENS TOUCHED label=bus:<name> day=<d> repos=<list>` for the note's day
and adds no number anywhere. Anything else is one `TOKENS UNPARSED` line
naming the note id and the line number in the file, and the run exits 1.

**The note's `Date:` is validated and never used for order.** A tokens note
whose `Date:` header is missing, is not a date nova-bus writes, or is later
than this fold's own `at=` stamp is `TOKENS UNPARSED` once for the whole
note, no line of it folded, `unparsed=<n>` counted once for it, exit 1; a
correction with a bad date is refused whole rather than half-read.

**Two notes with one subject in one lane are ordered by the bus's own
history, and the order is the first-parent line of the checkout's `HEAD`.**
The bus is a git history and the note that reached that line later wins.
`INDEX` is not consulted for this: it is a derived catalogue,
`nova-bus check --rebuild-index` writes it sorted by path, and a rebuild
can reorder two same-second corrections by their filenames without
changing either note (Stella's second read, 2026-09-11); a cache cannot
supply a chronology it never kept. The order comes from the checkout. For
each parsed tokens note that competes for one day in one lane, the tool
runs `git log --first-parent --format=%H --diff-filter=A -- <path>` in the
bus checkout under `--git-timeout` (rule 19), once per competing note and
never for a lone note; the one hash it prints is the commit that added the
note to the first-parent line of `HEAD` (a note merged in from a branch
is added, on that line, by its merge commit). The notes' adding commits
are then ranked by their position in `git log --first-parent --format=%H`
of `HEAD`, read once per fold and only when some day has competing notes,
newest first. The competing note whose adding commit is newest is the
report for that day; every other is
`TOKENS SUPERSEDED label=bus:<name> note=<id> by=<id> day=<d>` and not
folded, `superseded=<n>` on the source line. `Date:` is never consulted for
this (two sends can share a second), the filename never (a stamp in a name
is a `Date:`), `INDEX` never, the author's clock never, and the order the
filesystem lists notes in changes nothing. Two competing notes added by
one commit have no order between them and the tool invents none: both are
`TOKENS UNPARSED` for the whole note with the reason
`two tokens notes for <day> in one commit <hash>` and the remedy `send a
correction`, neither folds, and a correction in its own later commit is
the report. A competing note `git log` names no adding commit for (in the
working tree and uncommitted, or on a branch not on the first-parent line
of `HEAD`) is `TOKENS UNPARSED` for the whole note with the reason
`not on main` and the remedy `nova-bus send`; the notes that can be placed
still decide the day. A `git log` that fails or exceeds `--git-timeout` is
`TOKENS UNPARSED` for the note it was placing, git's first stderr line or
the timeout as the reason, exit 1. A lone tokens note for a day needs no
order and folds with no `git` run at all. A friend who sends twice is
correcting, and summing a correction onto its original doubles the day; an
unparsed newer correction leaves the older parsed note as the report,
visibly, with the run at exit 1 until the correction is fixed.

The tool never pulls, fetches or pushes. It reads the checkout it is given,
and the one `git` it runs asks that checkout about its own history, which
no network answers. A caller who wants today's notes runs `nova-bus inbox`
first. A fold that fetched would be a fold whose numbers depend on a
network call, and the `TOKENS SOURCE` line for the bus prints the newest
note's mtime so a reader can see how fresh the checkout was.

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
| `fold --all` | 1 FOLD + 10 SOURCE + 20 UNREADABLE + 1 MORE + 20 UNPARSED + 1 MORE + 20 SUPERSEDED + 1 MORE + 20 TOUCHED + 1 MORE + 20 MIXED + 1 MORE + 20 DAY + 1 MORE + 1 OK + 1 NOTE = 139 | under 24 KB |
| `report` | up to 200 pairs x 5 types = 1,000 lines on stdout, uncapped, because the body is the artifact and a capped report would be a count sent as a total; 1 REPORT OK on stderr | under 64 KB |
| `sum --month` | 1 MONTH + 20 PAIR + 1 MORE + 20 MODEL + 1 MORE + 1 TOTAL + 1 OK = 45 | under 10 KB |
| `check` | 20 FAIL + 1 MORE + 20 MISSING + 1 MORE + 20 STRAY + 1 MORE + 1 count line = 64 | under 8 KB |
| `sources --all` | 10 SOURCE + 20 UNREADABLE + 1 MORE + 20 UNPARSED + 1 MORE + 1 OK = 53 | under 8 KB |

These are ceilings that do not grow with the state. A test builds that state
in `t.TempDir()`, runs every verb, and asserts the line and byte counts
against the table; the measured numbers go into the commit that first passes
it (lesson 169).

**The fold's cost is one pass over each source file.** Each declared file is
opened once per run, and a test counts opens; `git log` runs once per
competing tokens note plus once per fold that has any, never for a lone
note, and the same test counts invocations. There is no index and no
incremental mode: a day file is recomputed whole from the sources every
time, and the day's own transcripts are the only thing that must be read to
compute it. The prototype read 2,497 files in about ten seconds; the
two-minute rule holds with room, and it is a count that is pinned, not a
time (lesson 167).

## What it deliberately does not do

- **It does not price anything.** Tokens, by type, per model. Dollars are a
  rate card times a count, the rate card changes, and a tool that carried
  one would carry a stale one.
- **It does not pull the bus, fetch, push, or talk to a network.** It reads
  a checkout; the one `git` it runs is `git log` over that checkout, to
  order competing notes (the bus source), and a fake `git` in the tests
  records that nothing else was ever asked.
- **It does not fill a missing day.** A day nobody folded is named by
  `check` and stays missing until somebody folds it.
- **It does not remove, trim or rotate any file.** Not a month file, not a
  log, not a stray. `check` names strays; a person removes them.
- **It does not schedule itself.** A LaunchAgent or a cron line is the
  bench's, and its log is the bench's; this tool prints and exits.
- **It does not detect overlap between sources.** A swarm job's OpenCode data
  home, declared with `--opencode`, and the same job's usage file, declared
  with `--swarm`, would count the job twice, and the tool cannot tell, because a
  usage file carries a job id and a database carries message ids. The rule is
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
6. **The subject match is case-insensitive and any text may follow the
   date.** Here it is exact: the date, then nothing or the tool's one
   trailer shape (rule 6).
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
    Here the swarm's usage files are a source (rule 14).
19. **A line dated another day is moved to that day silently.** Here it is
    folded to its day and counted `redated=<n>` (rule 17).
20. **A second tokens note for one day is summed onto the first.** Here the
    note whose commit is later on the bus's first-parent line supersedes,
    and the older is printed (the bus source).
21. **A type the source did not carry is written `0`.** Every DeepSeek row
    and every Claude `reasoning` cell said zero when nothing had measured
    them. Here it is `-`, and `sum` counts the dashes (rule 15).

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
   note, and neither is `tokens 2026-09-11 at=x`; one whose subject is
   exactly `tokens 2026-09-11 at=2026-09-11T23:55:02Z build=abc123` is; one
   whose subject is exactly `tokens 2026-09-11` with three good lines, two
   blank lines, one `# folded by hand` line, one `# repos: schema, serialize`
   line, one prose line, one five-field line and one line with an empty
   repo: three rows fold, `comments=2`, `TOKENS TOUCHED … repos=schema,serialize`
   prints, `TOKENS UNPARSED` prints three lines each naming the note id and
   the file line number, `unparsed=3`, exit 1. A note with no `Date:`, a
   note with `Date: yesterday`, and a note dated one hour after the fold's
   `at=` are each one `TOKENS UNPARSED` line naming the header line and the
   reason, contribute no row, `unparsed=1` each. Two notes for one day in
   one lane with the same `Date:` to the second and different ids, the
   fixture bus a real git repository in `t.TempDir()` with the two notes
   in two commits and filenames whose lexical order opposes their commit
   order: the note whose commit is later on the first-parent line is the
   day, the other prints `TOKENS SUPERSEDED`, `superseded=1`; the same
   fixture with the lane's files listed in reverse order, the two files'
   mtimes swapped, and `INDEX` rewritten sorted by path as
   `nova-bus check --full --rebuild-index` writes it, gives byte-identical
   rows and the same `SUPERSEDED` line (fold, rebuild `INDEX`, fold:
   identical); a third note for that day, in a later commit and with a
   malformed `Date:`, is `TOKENS UNPARSED`, the second note stays the day's
   report, exit 1; two competing notes added by one commit are each
   `TOKENS UNPARSED` with `two tokens notes for <day> in one commit`,
   neither folds, and a note in a later commit becomes the day; two
   corrections from two branches reaching `HEAD` by two merges rank by
   their merge commits, the later merge winning, whatever their `Date:`
   headers and author clocks say, and the same two reaching `HEAD` by one
   merge are both `TOKENS UNPARSED` as one commit; a competing note in the
   working tree and in no commit is `TOKENS UNPARSED` with `not on main`
   and the `nova-bus send` remedy while the committed note stays the day;
   a fake `git` on `PATH` that sleeps past `--git-timeout 1` gives
   `TOKENS UNPARSED` naming `timeout after 1s`, exit 1; a lone note folds
   with the fake `git` never invoked.
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
    with `--allow-shrink` the file is rewritten, `written=true`, exit 0; a
    file with `reasoning=40` whose sources now report no reasoning at all
    is `TOKENS SHRANK … file=40 now=-`; a file with `reasoning=-` whose
    sources now report `40` is not a shrink.
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
    a file with rows out of order, a file with no version line, a file with
    an empty type cell, a file with `day_basis` empty, and a run of days
    `09-07, 09-08, 09-10`: every finding prints one line,
    `CHECK MISSING date=2026-09-09` prints, the count line prints
    `bad=7 missing=1`, exit 1; a file whose type cells are `-` and whose
    `day_basis` is `America/Los_Angeles` is clean; a clean set is
    `CHECK OK … missing=0`, exit 0; `sum` over the same gapped month exits
    0 with `missing=1`.
14. A pool with two usage files and a third job directory with none, after
    the two job directories are reclaimed: two rows fold with
    `sources=swarm:<label>` and all five types from the row, a file whose
    `cache_write` is `-` gives `cache_write=-` and one whose `reasoning` is
    `0` gives `reasoning=0`, `nousage=1` and
    `reports=input,output,cache_write,cache_read,reasoning` on the source
    line; a file with a fifteen-column header is refused naming the missing
    column; two files for one task (`attempt=1`, `attempt=2`) are two rows;
    a `repo` the rules file does not name folds as `other`; a tripwire on
    every path opened finds nothing under `done/` or `failed/`.
15. A row fed by an OpenCode message with all five types and a Claude
    message with four: each type column equals the sum of what each source
    reported for that type and nothing else, and `reasoning` is the
    OpenCode number alone; a row fed by Claude messages only has
    `reasoning=-`; a row fed by a swarm usage file only has
    `cache_write=- cache_read=-`; a row fed by a bus note with lines for
    `input` and `output` only has the other three `-`; a bus line with
    count `0` gives `0`; a fixture where every source is partial has no `0`
    in any cell it did not report; `TOKENS DAY dashes=` counts those cells;
    `sum` over those files prints `dashes=` per column on the pair, the
    model and the total equal to the rows that were `-`, and its totals add
    only the numbers; a source test asserts no function adds one type
    column into another and no reader writes a literal `0` for a type it
    did not read; the key of every row is three fields and neither `who`
    nor `window` appears in any header.
16. A fake `sqlite3` on `PATH` records the paths it was asked to open: only
    paths under `--scratch`, every invocation carries `-readonly`; the
    original database's bytes and mtime are unchanged after the fold; a
    fake `git` on `PATH` records every argument vector: each is `log` with
    `--first-parent` and `--format=%H` and a path inside `--bus`, never
    `fetch`, `pull`, `push`, `remote` or a URL; a source test finds no
    `net` import.
17. A transcript line stamped `2026-09-11T23:59:59Z` and one stamped
    `2026-09-12T00:00:01Z` land in two files; a bus note with subject
    `tokens 2026-09-11` and a line dated `2026-09-10` folds into the
    `09-10` file, `redated=1` on the source line; a provider export with
    per-row timestamps `2026-09-11T20:30:00-07:00` and
    `2026-09-11T17:30:00-07:00` lands one row in `09-12` and one in `09-11`,
    both `day_basis=utc`; an export of per-day totals declaring
    `America/Los_Angeles` lands under its own date with
    `day_basis=America/Los_Angeles`, `TOKENS SOURCE day_basis=America/Los_Angeles`,
    `TOKENS DAY nonutc=1`, and `sum` prints `nonutc=1`; an export with
    neither timestamps nor a zone is `TOKENS UNREADABLE`; a `utc` row and a
    zoned row for one `(day, model, repo)` print `TOKENS MIXED`, write no
    row for that key, `mixed=1`, exit 1.
18. Two folds over one fixture, run in sequence: every day file is
    byte-identical below the first line, and the first lines differ only in
    `at=`; the same over a bus lane holding two tokens notes for one day,
    with the directory listing order reversed between the runs, is
    byte-identical the same way.
19. A fake `sqlite3` that sleeps past `--timeout 1`: `TOKENS UNREADABLE`
    names the source and `timeout after 1s`, the fold continues over the
    other sources, exit 1; `--timeout` unset is 120 and a test asserts it;
    `--timeout 0` is refused.
20. `report --who emma --day D` over a fixture Claude Code directory: stdout
    is six-field lines and nothing else, one per (model, repo, type) the
    source reported and none for `reasoning`; stderr carries `REPORT OK`
    with `subject=tokens D at=<stamp> build=<id>`; a note built from
    exactly that subject and exactly the `--note` file as body, with one
    `# repos: schema` line appended by hand, placed in a fixture lane and
    folded by `fold --bus`, is a tokens note with zero unparsed, the same
    (model, repo, type, count) rows as `fold --claude` over the same
    directory, `reasoning=-`, and `TOKENS TOUCHED … repos=schema` (one
    grammar, by construction); a `report` whose every source is unreadable
    prints no lines, `REPORT FAIL`, exit 1; a `report` line never contains
    `~` or `#`; `--note` writes exactly the stdout bytes and nothing else;
    `report --who emma --day D --provider g=<export>` over the fixture
    export of per-day totals declaring `America/Los_Angeles` prints lines
    of seven fields, each ending `day_basis=America/Los_Angeles`, and a
    note built from that subject and that `--note` file, folded by
    `fold --bus`, writes the same rows as `fold --provider g=<export>`
    over the export directly, `day_basis=America/Los_Angeles` on each,
    `TOKENS DAY nonutc=` equal between the two folds, `TOKENS SOURCE
    day_basis=America/Los_Angeles` on the bus source line; the Claude
    fixture's `report` prints no seventh field; a hand-written line with
    `day_basis=utc`, one with a seventh field that is not `day_basis=`, and
    one with eight fields are each `TOKENS UNPARSED`; a note with a
    six-field and a seven-field line for one `(date, model, repo)` is
    `TOKENS MIXED` for that row.
21. A fixture Google export and a fixture xAI export fold to rows with repo
    `unattributed`, the parser name in `sources`, `-` in every type the
    export has no column for, and `day_basis` per demanded test 17; an
    export with one unknown column is `TOKENS UNREADABLE` quoting that line;
    a friend's tokens note whose body is one `# repos: schema, serialize`
    line and nothing else is a valid note with zero rows, yields
    `TOKENS TOUCHED … repos=schema,serialize`, and changes no count.

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
   line, the eleven columns, strict parse (a row with ten columns is an
   error naming the line; a type cell is an integer or `-` and an empty
   cell is an error), sorted rows, the write through `<day>.tsv.tmp` and
   rename under the output lock, the shrink comparison with `-` on either
   side. Tests: round trip is byte-identical; an unversioned file refuses;
   demanded tests 8, 10, 12, 13, 18.
2. **`internal/tokens/lock.go`**: `flock` on `<out>/fold.lock` (LockFileEx
   on Windows), a bounded jittered wait with the sleeper injected, exit 2
   naming the holder. Tests: demanded test 8's lock half.
3. **`internal/tokens/message.go`**: the one message shape every source
   produces, with each of the five types either a count or absent, the
   source's `reports` set and the day basis, and the fold function over a
   stream of them: dedup by id, day from stamp, attribution, each type
   added into the row over the sources that reported it and `-` otherwise,
   rough carried, `TOKENS MIXED` on two bases. Tests: demanded tests 4, 15,
   17.
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
7. **`internal/tokens/swarm.go`**: the usage file reader over
   `<pool>/usage/`, the sixteen-column header check, the `nousage` count
   from `done/` and `failed/` directory names alone. Tests: demanded test
   14; a tripwire that no file under `done/` or `failed/` is opened.
8. **`internal/tokens/provider.go`**: one parser per export shape (`google`,
   `xai`, `openai`), the UTC fold from per-row timestamps, the zoned row from
   per-day totals, the `reports` set from the columns present. Tests:
   demanded tests 17 and 21.
9. **`internal/tokens/bus.go`**: the roster read, the lane walk, the header
   parse with nova-bus's two tolerances (a leading heading, bulleted header
   lines), the exact subject with its one trailer shape, the `Date:`
   validation against the fold's stamp, the six-field line, blank and `#`
   lines with the one `# repos:` shape and the optional seventh field, the
   `git log` read under `--git-timeout` and the supersede rule over it,
   and the serializer `report` writes with, which is this parser's inverse
   and lives in this file. Tests: demanded tests 6, 7, 17,
   18, 20.
10. **`internal/tokens/sum.go`** and **`check.go`**: the month walk, the two
    groupings, the per-column dash counts, the non-UTC count, the gap
    count; every `check` assertion of rule 13. Tests: demanded tests 9, 13,
    15; `sum` over an empty month prints `days=0`.
11. **`cmd/nova-tokens/main.go`**: the verbs, the flag parsing with this
    repo's one-line refusals (the flag parser given `io.Discard`), the
    output grammar exactly as above, `--max` per kind, `--scratch` required
    with `--opencode` and refused without, `--timeout` default 120,
    `--git-timeout` default 60 and refused without `--bus`,
    `report`'s stdout as the artifact and its OK line on stderr, `version`
    from the build.
12. **`cmd/nova-tokens/*_test.go`**: the contract tests: every exit code,
    every refusal sentence, the environment ignored (demanded test 1), the
    largest plausible state measured (demanded test 11), the audit over
    every printed argument (`internal/oneline/audit`), and no test reaching
    outside `t.TempDir()` or the fake `sqlite3`.
13. **`README.md`'s `### First run`**: fold one fixture transcript and one
    fixture bus note into a temp directory, `check` it, `sum` it, every path
    a flag, the transcript produced by running the tool. The fixture bus
    lane uses `example.com`.
14. **A read, then the switch.** One recorded read of the binary against
    this spec by a line that is not its author. Then one fold run beside
    `token-collate.sh` over the same day, the two day files compared row by
    row, the differences explained by the items in **what the prototype does
    that this spec forbids**, and only then the LaunchAgent repointed.
    `token-collate.sh` and its two folds stay where they are until that
    day.
