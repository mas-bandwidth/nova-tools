# nova-tokens, specification

`nova-tokens` is one binary at the **accounting layer**. It folds token spend
from declared sources into **one file per day**, keyed exactly by
`(day, model, repo)`, with the five token types kept apart, and it sums those
day files into a month. It reads sources. It never estimates, never fills a
gap, and never removes a file.

This spec is normative. If the code and this document disagree, one of them
has a bug, and the tests decide which. It stands beside [SPEC.md](SPEC.md),
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
   **Amendment, 2026-09-12 (rules 22, 30, 31).** `publish` adds no default
   path and consults no environment either: `--ledger`, `--batch`,
   `--v1-day`, `--public` and `--public-repos` are flags, and a
   missing one is exit 2 and `refusing to guess`. **The ledger repository is
   a required flag too**: `--repo <host>/<owner>/<name>`, host-bound, with no
   default and nothing compiled in (rule 30). It adds three named defaults,
   none of them a repository and none of them read from an environment:
   `--git-timeout` 120 seconds per subprocess, `--attempts` 3, and
   `--deadline` 60 seconds over the whole sequence. The publish lock is not a
   flag at all: it is derived from the git directory the clone itself reports
   (rule 30). The one piece of configuration read is the named
   remote's own URL, read to be checked against `--repo` and never to choose
   one; a destination is never inferred from a directory's name, from an
   environment variable, or from the fact that only one remote exists. Every
   path a run was given is resolved and checked against every other before a
   byte is written (rule 31), because a flag's name cannot prove two flags
   are not one directory.
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
   `at=<RFC 3339 UTC> build=<id>[ supersedes=<note-id>[,<note-id>…]]`, which
   is where a `report`'s stamp, build id and, for a correction, the ids of
   the notes it replaces — a set, sorted ascending, no duplicates, one or
   more — travel (rule 20 and the bus source); any other text after the
   date is not a tokens note. A body line is one of three things: six tab-separated fields,
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
   The exception is a file THIS RUN makes, named here and nowhere else: the
   fold's own `fold.lock`, the copy under `--scratch`, the fixed
   `<day>.tsv.tmp` a day is written through, and, on a platform with no
   flock, the lock sentinel the release removes. A file the tool was given is
   never one of them, and the tripwire that enforces this searches for every
   call that can empty a file -- `os.Remove`, `os.RemoveAll`, `os.Truncate`,
   `.Truncate(`, `os.Create(`, `os.WriteFile(`, `os.O_TRUNC` (the flag that
   empties the file an `os.OpenFile` opens) -- carving out those four by
   file, with the reason, and failing when a carve-out has gone stale.
   A file under `--out` that is not a day file and not the temp name is
   named by `check` and left alone.
   **Amendment, 2026-09-12 (rules 22–31).** `publish` removes nothing it did
   not make, and the files it makes are its own (rule 31), named here and
   nowhere else, each added to the carve-out list above by shape with its
   reason, the list still failing when a carve-out has gone stale: this run's
   private temporary directory `<git-dir>/nova-tokens-publish.<random>/` and
   the plumbing index inside it; with `--public`, the staged
   `<public>/<day>.tsv.<same random>.tmp` that lands by one rename in that
   same directory; and the
   publish lock `<git-dir>/nova-tokens-publish.lock` with, on a platform
   with no flock, its sentinel — the same two carve-outs rule 8's `fold.lock`
   already has, for the same reason. `<git-dir>` is what the clone itself
   reports, never an assumed `.git`. The run releases its own temporary
   directory and staged file on ordinary exit; a crash leaves them, named by a
   suffix no other run uses, and no run removes another's. It writes **nothing** into the ledger clone's working tree: the
   day's bytes become git objects under the clone's own `.git` through
   plumbing (rule 23), so there is no temp file and no rename there to carve
   out. No verb of this tool deletes, truncates or trims a file it was given,
   `publish` runs no `git` subcommand that removes, resets or cleans anything
   (rule 23's allowlist, rule 24), and a source is still never written at all
   (rule 16).
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
    it, the build id, and `turns=<n|->`: the number of messages counted into
    the day across the sources that count messages (`--claude`, `--opencode`),
    `-` when none did, so the turn count sits beside the tokens on the line
    a person reads (Rowan, 2026-09-11: the coordinator's own turn count as a
    daily number beside tokens; Emma, the same day: a turn's cost is its
    whole context, so tokens over turns is the number that explains a day).
    **`turns=` is labelled by its scope**: it counts eligible assistant
    messages across the declared message-counting sources, and the
    `sources=` on the same line is that scope; it is not, by itself, the
    coordinator's task turns or anybody's decisions, and a comparison that
    quotes it quotes `sources=` and the denominator — messages counted, over
    which sources, for which day — beside it (Stella, 2026-09-11: label the
    new metric accurately). `TOKENS FOLD`, `SUM MONTH` and `CHECK OK` carry
    `at=<stamp> build=<id>`. No flag sets the stamp, and a day file whose
    first line lacks it is a `check` failure. The stamp is when the tool
    computed the file; the `date` column is the UTC day of the message.
13. **`check` is the gate.** It verifies every day file under `--out` parses,
    every row has all eleven columns with each of the five type cells either
    a non-negative integer or exactly `-`, never empty, `day_basis` either
    `utc` or a zone name with no whitespace, the version line carries
    `turns=` as an integer or `-`, the `date` column equals the
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
    **Amendment, 2026-09-12 (rules 22, 27).** Every word above is unchanged:
    no verb writes into a source, and "the tool never runs `git`" is true of
    every verb that reads one. A ledger clone is not a source. It is a
    destination, named by `--ledger`, given to `publish` alone, and `publish`
    reads no source at all — no transcript, no database, no swarm pool, no
    export, no bus checkout. Two of a run's paths that resolve to one
    directory are exit 2, checked over resolved paths and not over flag names
    (rules 27 and 31).
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
19. **Every subprocess runs under a timeout, and there is one.** `sqlite3`
    runs under `--timeout <seconds>`, default 120, and a run that exceeds it
    is `TOKENS UNREADABLE` for that source with the timeout named. No other
    subprocess exists: an earlier draft ran `git log` over the bus checkout
    to order competing tokens notes, and that order is now explicit in the
    notes themselves (`supersedes=`, the bus source), so the tool runs no
    `git` at all and rule 16 holds without exception. The default is allowed
    for the reason SPEC.md gives `nova-bus --git-timeout`: it is how long the
    tool waits before saying so, not a fact about anybody's data.
    **Amendment, 2026-09-12 (rules 22, 27).** Glenn, live: "nova-tokens can
    and should provide an easy way to upload token daily work to git." And,
    the same day: "not sure why this got outlawed. we need to collate across
    multiple machines. git makes sense." The rule above was written for a
    tool that measured one bench locally, before a ledger repository existed
    to collate several — `mas-bandwidth/tokens` was created on 2026-09-12, at
    his instruction, after this rule and rule 16 were written — so the
    prohibition predates the need it was read as answering. "The tool runs no `git` at
    all" is narrowed to the five verbs it was written about: `fold`, `sum`,
    `check`, `sources` and `report` run none, and demanded test 16's
    fake-`git` tripwire still holds over them. There are now two subprocess
    programs and both run under a timeout: `sqlite3` under `--timeout` for
    the sources, and `git` under `--git-timeout` (default 120, the same
    reason) for `publish` and for no other verb. Nothing else here moves: the
    order of competing bus notes is still `supersedes=` and never a
    checkout's history, and this tool still imports no `net` package — the
    network on the publish path is `git`'s, under `--git-timeout`.

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
    says so. `--note <path>` writes exactly the stdout bytes to that file, through
    `<path>.tmp` and one rename, and only on `REPORT OK`: a `REPORT FAIL`
    writes nothing and leaves an existing `--note` file byte-unchanged.
    `--supersedes <note-id>`, repeatable, puts `supersedes=<id>[,<id>…]` on
    the subject — the ids sorted ascending, a repeated id refused — which is
    how a friend corrects a day, or joins competing tips into one (the bus
    source); the tool checks each id's shape and nothing else, because the
    lane is not on this machine.
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
                    [--scratch <dir>] [--timeout <seconds>] [--allow-shrink] [--max <n>]
nova-tokens report  --who <name> --day <YYYY-MM-DD> --repos <file>
                    [--claude <label>=<dir>]... [--opencode <label>=<file>]... [--provider <label>=<file>]...
                    [--supersedes <note-id>]... [--note <path>] [--scratch <dir>] [--timeout <seconds>]
nova-tokens sum     --out <dir> --month <YYYY-MM> [--max <n>]
nova-tokens check   --out <dir> [--max <n>]
nova-tokens sources --repos <file> (--day <YYYY-MM-DD> | --all)
                    [--claude <label>=<dir>]... [--opencode <label>=<file>]... [--swarm <label>=<dir>]... [--bus <dir>]
                    [--provider <label>=<file>]...
                    [--scratch <dir>] [--timeout <seconds>] [--max <n>]
nova-tokens publish (--batch <dir> | --v1-day <dir> --day <YYYY-MM-DD> --seat <label>)
                    --ledger <dir> --remote <name> --branch <name> --repo <host>/<owner>/<name>
                    [--supersede]
                    [--public <dir> --public-repos <file>] [--public-sources]
                    [--deadline <seconds>] [--git-timeout <seconds>] [--attempts <n>] [--max <n>]
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

**Amendment, 2026-09-12 (rules 22, 30, 31).** `publish` brings the exception
count to four, and not one of them names a place or a repository:
`--git-timeout` 120 seconds per subprocess, `--attempts` 3 and `--deadline`
60 seconds over the whole fetch, build, push and retry sequence — the
retained-format packet's own "3 race retries within 60s", adopted — each
allowed for the reason rule 19 gives `--timeout`. There is **no `--lock`
flag**: the publish lock is one canonical path derived from the clone's own
git directory, because two publishers free to name different locks would not
be serialized (rule 30). **The ledger repository has no default**:
`--repo <host>/<owner>/<name>` is required and host-bound, because
`<owner>/<name>` alone does not identify a destination and a compiled-in
repository would be exactly the guess rule 1 forbids (Stella, 2026-09-12).
`--ledger`, `--remote`, `--branch`, `--repo` and one of `--batch` or
`--v1-day` are required with no defaults; `--day` and `--seat` are required
with `--v1-day` and refused with `--batch`; `--public-repos` is required when
`--public` is given and refused otherwise, and `--public-sources` is refused
without `--public`, for the reason `--scratch` is. There is no staging flag:
the subset is staged inside `--public` itself, under this run's own name, so
the rename that lands it can never cross a filesystem (rule 31).

### `fold`

Asserts: every declared source was read whole, every bus line and note
parsed, no row mixed two day bases, no lane's day had two reports without a
supersession between them, every day file computed was written. Says
NO (exit 1) when any file was unreadable, any bus line or note unparsed, any
row was `TOKENS MIXED`, any lane-day was `TOKENS CONFLICT`, or any day would
have shrunk without `--allow-shrink`; the rest is still written, and a day
whose report in some lane is ambiguous is not written at all, its existing
file untouched. Deliberately does not check: that the day files
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

### `publish`

Asserts: the contribution exists and validates — a v1 day file under rule 13's
row rules, a batch under its envelopes' own validators — and that, when the
verb returns 0, every one of its files is on the ledger's named branch of the
named remote at its own path, byte-identical for a batch's content-addressed
paths and **identical under rule 26** for a v1 day, whose identity is
`rows_sha256` and not the whole file. Says NO (exit 1) on `reason=differs`,
`conflict`, `incomplete`, `malformed`, `changed`, `subset` or `unconfirmed`.
Could not run (exit 2) on a missing or bad flag, a missing `--repo`, an
effective fetch or push URL that does not match it, unrelated dirty work in
the clone, an empty `user.name` or `user.email`, a held publish lock, two
given paths that resolve to one directory, a staged path that exists, a fold
in flight, a malformed batch directory, or a batch whose schema has no
validator here. Deliberately does not check: whether the numbers are right
(`check` and `sum` read them), whether other days or other benches are
present in the ledger, whether coverage of the whole ledger is complete, or
whether the note for that day reached the bus (rule 28). `publish` is a
**wall**, and it is the only verb that runs `git`, the only verb that writes
into a git clone, and it is in **Publishing to the git ledger**, rules 22 to
31.

## Exit codes

| code | meaning |
|------|---------|
| 0 | the verb ran and passed: every source read, every line parsed, every day written; a sum or a listing printed; a check with nothing to name |
| 1 | the verb ran and said **NO**: a declared source with an unreadable file, an unparsed bus line or note, a row of two day bases, a lane-day with competing reports (`TOKENS CONFLICT`), a day that would shrink, a check finding, a `report` with nothing to show |
| 2 | could not run: missing flag, bad flag value, `--out` not a directory, `--repos` unreadable or malformed, a duplicate label, `sqlite3` absent when `--opencode` is given, a second fold holding the lock |

**Amendment, 2026-09-12 (rules 22–31).** The three meanings hold for
`publish` and the enumerations above gain its cases, which are the only ones
this amendment adds. **Exit 0**: the contribution is on the ledger's named
branch at its own paths, either pushed by this run (`state=published`,
`state=superseded`) or already there — byte-identical for a batch's immutable
paths, identical under rule 26's `rows_sha256` for a v1 day
(`state=already-published`). **Exit 1**, the verb ran and said NO:
`reason=differs` (a v1 day's path holds different rows and `--supersede` was
not given), `reason=conflict` (a content-addressed path holds bytes other
than its own digest's), `reason=incomplete` (a published coverage envelope
whose referenced closure is missing a member or holds different bytes),
`reason=malformed` (a v1 day file `check` would name, or a batch whose
envelopes do not validate), `reason=changed` (an input's digest moved under
the publication), `reason=subset` (a public subset would carry a row not on
its allowlist), `reason=unconfirmed` (the push could not be confirmed inside
`--attempts` and `--deadline`). **Exit 2**, could not run: a missing or bad
flag, `--repo` missing, both contribution kinds or neither, `--supersede` with
`--batch`, a source flag on `publish`, two given paths that resolve to one
directory, a staged path or a temporary directory that already exists,
`--ledger` that is not a git checkout, an effective fetch or push URL that
does not match `--repo` or whose shape the parser does not know, more than one
effective push URL where they do not all match, an empty `user.name` or
`user.email` in the clone, unrelated dirty or staged work in the clone, a
second publisher holding the lock, a `git` absent from `PATH`, a fold in
flight under `--v1-day`, a batch that is not exactly its own named files, and
a batch naming a schema this build has no validator for.

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
`MIXED`, `SHRANK`, `MISSING` and refusals go to stderr.
**Amendment, 2026-09-12 (rules 22, 24).** The rule above is unchanged and
`publish`'s lines obey it as written: `PUBLISH PLAN`, `FILE`, `SUBSET`,
`EXCLUDED`, `OK` and `NOTE` are informational or `OK` and go to stdout,
`PUBLISH DIRTY`, `FAIL` and `REFUSED` go to stderr, and a `PUBLISH MORE`
goes to the stream of the kind it caps — the MORE for `EXCLUDED` to stdout,
the MORE for `DIRTY` to stderr — so neither stream ever shows a list without
its MORE or a MORE without its list. `report` is the one
exception, stated in rule 20: its stdout is exactly rule 6's body lines, and
`REPORT OK`, `REPORT FAIL` and its `TOKENS UNREADABLE` lines go to stderr. Every path, label, model name,
repo name, note id and reason renders through `internal/oneline`; every
`key=value` carrying stored text is one token via `oneline.Field`; the tail
after `: ` is capped at `oneline.TailBytes`.

```
TOKENS FOLD at=<stamp> build=<id> out=<dir> sources=<n> days=<all|d> repos=<file>
TOKENS SOURCE label=<label> kind=<claude|opencode|swarm|bus|provider> path=<path> reports=<types> day_basis=<utc|mixed|<zone>> files=<n> unreadable=<n> messages=<n> dup=<n> noid=<n> nousage=<n> unparsed=<n> comments=<n> redated=<n> superseded=<n> rows=<n>
TOKENS UNREADABLE label=<label> path=<path>: <why>
TOKENS UNPARSED label=<kind>:<name> note=<id> line=<n>: <text or why>
TOKENS SUPERSEDED label=bus:<name> note=<id> by=<id> day=<d>
TOKENS CONFLICT label=bus:<name> day=<d> notes=<id,id,…>: competing reports; send a correction whose subject carries supersedes=<id>
TOKENS TOUCHED label=bus:<name> day=<d> repos=<list>
TOKENS MIXED date=<d> model=<model> repo=<repo> bases=<utc,zone>: two day bases on one row; declare one export for that day
TOKENS DAY date=<d> rows=<n> models=<n> repos=<n> turns=<n|-> unknown=<pct>% other=<pct>% rough=<n> dashes=<n> nonutc=<n> sources=<labels> written=<true|false>
TOKENS SHRANK date=<d> type=<type> file=<n> now=<n|-> written=<true|false>: a source went quiet; --allow-shrink writes it anyway
TOKENS MORE kind=<source|unreadable|unparsed|superseded|conflict|touched|mixed|day> shown=<n> total=<t> <remedy>
TOKENS OK days=<n> rows=<n> sources=<n> unreadable=<n> unparsed=<n> mixed=<n> conflict=<n> shrank=<n>
TOKENS FAIL days=<n> rows=<n> sources=<n> unreadable=<n> unparsed=<n> mixed=<n> conflict=<n> shrank=<n>
TOKENS NOTE <the one remedy line>
TOKENS REFUSED: <reason>
REPORT OK who=<name> day=<d> rows=<n> at=<stamp> build=<id> subject=<subject>
REPORT FAIL who=<name> day=<d> rows=<n> unreadable=<n>
REPORT REFUSED: <reason>
SUM MONTH month=<m> at=<stamp> build=<id> days=<n> first=<d> last=<d> missing=<n> rows=<n> turns=<n|->
SUM PAIR model=<model> repo=<repo> input=<n> output=<n> cache_write=<n> cache_read=<n> reasoning=<n> rough=<n> dashes=<in>,<out>,<cw>,<cr>,<r> nonutc=<n> days=<n>
SUM MODEL model=<model> input=<n> output=<n> cache_write=<n> cache_read=<n> reasoning=<n> rough=<n> dashes=<in>,<out>,<cw>,<cr>,<r> nonutc=<n> repos=<n>
SUM TOTAL input=<n> output=<n> cache_write=<n> cache_read=<n> reasoning=<n> rough=<n> dashes=<in>,<out>,<cw>,<cr>,<r> nonutc=<n> turns=<n|-> pairs=<n> models=<n>
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
SOURCES SOURCE label=<label> kind=<claude|opencode|swarm|bus|provider> path=<path> reports=<types> day_basis=<utc|mixed|<zone>> files=<n> unreadable=<n> messages=<n> dup=<n> noid=<n> nousage=<n> unparsed=<n> comments=<n> redated=<n> superseded=<n> rows=<n>
SOURCES UNREADABLE label=<label> path=<path>: <why>
SOURCES UNPARSED label=<kind>:<name> note=<id> line=<n>: <text>
SOURCES MORE kind=<source|unreadable|unparsed> shown=<n> total=<t> nova-tokens sources … --max 0
SOURCES OK sources=<n> files=<n> messages=<n> unreadable=<n> unparsed=<n> rows=<n>
PUBLISH PLAN at=<stamp> build=<id> kind=<batch|v1-day> ledger=<dir> repo=<host>/<owner>/<name> remote=<name> branch=<name> seat=<label|-> day=<d|-> contribution=<hex> files=<n> bytes=<n> deadline=<seconds> public=<dir|->
PUBLISH DIRTY path=<path>: <modified|staged|untracked>
PUBLISH FILE path=<path> sha256=<hex> rows_sha256=<hex|-> state=<new|present|identical|superseded|conflict|missing> supersedes=<hex|->
PUBLISH SUBSET path=<path> rows=<n> excluded=<n> repos=<list> sha256=<hex>
PUBLISH EXCLUDED day=<d> model=<model> repo=<repo>: not on --public-repos
PUBLISH MORE kind=<dirty|file|excluded> shown=<n> total=<t> <remedy>
PUBLISH OK contribution=<hex> commit=<sha> pushed=<true|false> state=<published|already-published|superseded> attempts=<n> files=<n> at=<stamp> build=<id>
PUBLISH FAIL contribution=<hex> reason=<differs|conflict|incomplete|malformed|changed|subset|unconfirmed> pushed=<true|false|->
PUBLISH NOTE <the one remedy line>
PUBLISH REFUSED: <reason>
SOURCES REFUSED: <reason>
```

Every `label=` in the block is `<kind>:<name>`, the kind one of the five
(`claude`, `opencode`, `swarm`, `bus`, `provider`) and the name the one the
caller declared, so a line names the reader that could not read something as
well as the source: a transcript line this tool cannot date is
`TOKENS UNPARSED label=claude:<name> note=<path> line=<n>: <text or why>`, and
`note=` is the note id for a bus note and the file the line came from for every
other kind.

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
`mixed` when one lane's lines carry more than one (rule 17); `<zone>` in the
block is that zone NAME as rule 17 declares it and rule 13 accepts it
(`America/Los_Angeles`, `+02:00`: no whitespace, never `utc`), not the word
`zone`, which this tool never prints; a lane is
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
`TOKENS UNREADABLE`, `TOKENS UNPARSED`, `TOKENS SUPERSEDED`, `TOKENS CONFLICT`, `TOKENS TOUCHED`,
`TOKENS MIXED` and `TOKENS DAY` are each capped at `--max` separately, per
kind, because a month of `--all` is up to 90 day lines
and one unreadable directory is 2,000 file lines, and the loud kind must not
eat the quiet one. The counts on `TOKENS OK` and `TOKENS FAIL` are the truth
about the fold, never about the output.

**`TOKENS NOTE` is exactly one remedy line.** If anything was unreadable it
names the label and says either open the files to the group or drop the flag;
if a bus line or note was unparsed it names the note id and the shape; if a
lane-day had competing reports it names the lane and the `supersedes=`
trailer; if a
row mixed two day bases it names the two labels; if a day shrank it names
`--allow-shrink`; if nothing was wrong it names `check`.

**A refusal prints every independent problem in one go**, one line each: a
fold with no `--out`, no `--repos` and a bad label says all three.

## The day file

`<out>/<day>.tsv`, tab separated, one file per UTC day:

```
nova-tokens v1 day=2026-09-11 at=2026-09-11T23:55:02Z build=<id> turns=1204 sources=claude:glenn,opencode:bench,swarm:deepseek,bus:emma,google:emma
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

The first line is the **version and stamp line** (rule 12; lesson 45), and
`turns=` on it is the day's message count across the sources that count
messages, `-` when none did, summed by `sum` onto `SUM MONTH` and `SUM TOTAL`
as `turns=`. A file
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
`--scratch`, and three queries run there with `sqlite3 -readonly -json` under
`--timeout` (rule 16; `-json` is used rather than `-tabs` because tool command inputs containing tabs/newlines corrupted TSV column splitting): sessions (`id`, `parent_id`, `directory`), assistant
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

**Amendment, 2026-09-12 (rules 22, 27).** `sqlite3` is the only subprocess on
any path that reads a source, and that is what this sentence is about. There
is a second program, `git`, and it runs for `publish` alone, under
`--git-timeout`, against the ledger clone and nothing else (rule 19's
amendment). No source reader may start it, and demanded test 27 is what holds
that.

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

**Two notes for one day in one lane are one report only when the later
names the earlier: `supersedes=<note-id>[,<note-id>…]` in the subject
trailer, and nothing else orders them.** A friend who sends twice is
correcting, and summing a correction onto its original doubles the day; so a
correction says what it corrects. A tokens note whose subject trailer carries
`supersedes=` is a **successor** of an explicit **predecessor set**: one or
more ids, sorted ascending, no duplicates, each naming a tokens note in the
same lane for the same day that itself parsed whole, and the successor is
validated whole — header, `Date:`, every body line — before it replaces
anything. When it does, each predecessor is `TOKENS SUPERSEDED
label=bus:<name> note=<id> by=<id> day=<d>`, not folded, `superseded=<n>` on
the source line, and the successor is the day's report; a chain of successors
folds to its last valid link, so two sequential corrections are one report.
The lane-day's **tips** are its parsed notes that no valid successor names.
When there is more than one tip — two roots, or two successors of one
predecessor — that lane-day is `TOKENS CONFLICT label=bus:<name> day=<d>
notes=<id,id>`, `conflict=<n>` on `TOKENS OK` or `TOKENS FAIL`, exit 1,
**no** row from that lane folds for that day, the day file is not written and
an existing one is left untouched; a correction that names only one of the
tips replaces that one and leaves the conflict, because the other tip still
stands, and the printed remedy names **every** tip. The append-only
reconciliation is a **replacement snapshot**: one fully validated note whose
predecessor set names all current tips becomes their single successor and the
lane-day's single tip, the old records untouched and each `SUPERSEDED` by
name. A predecessor set with a duplicate, an unsorted order, an id that names
a note in another lane or for another day, a missing or unparsed target, or a
cycle (at any length, through any member) refuses the whole successor as
below; the set never partially applies. No winner is inferred from `Date:`
(two sends can share a second), from the filename (a stamp in a name is a
`Date:`), from the directory listing, from the author's clock, from `INDEX`,
or from the bus's git history: `INDEX` is a derived catalogue that
`nova-bus check --rebuild-index` writes sorted by path (Stella's second read,
2026-09-11), and a commit order is a fact about the checkout and not about
which number the friend meant. A successor that names a missing note, a note
in another lane or for another day, a note that did not parse, or a note
that is itself its successor (a cycle, at any length) is `TOKENS UNPARSED`
for the whole successor with the reason after the colon and the remedy `send
a correction whose subject carries supersedes=<id>`; the earlier valid note
stays the day's report, visibly, and the run is exit 1 until the correction
is fixed. `report --supersedes <note-id>`, repeated once per predecessor,
writes the trailer, and the parser
that reads it is the serializer that writes it (lesson 113). A lone tokens
note for a day needs no order and no trailer. (Stella, 2026-09-11: with one
id per trailer, two roots or two successors leave two tips whatever is sent
next, and the printed remedy could not resolve the conflict; the set is the
missing append-only operation, and the omission was in her earlier
single-id suggestion too.)

The tool never pulls, fetches, pushes, runs `git`, or talks to a network. It
reads the checkout it is given as files (rule 16). A caller who wants today's
notes runs `nova-bus inbox` first. A fold that fetched would be a fold whose
numbers depend on a network call, and the `TOKENS SOURCE` line for the bus
prints the newest note's mtime so a reader can see how fresh the checkout
was.

**Amendment, 2026-09-12 (rules 22, 27, 28).** Every word of this paragraph
still holds, of the bus and of every verb that reads it. `publish` is the one
verb that runs `git`; it runs it only against the ledger clone named by
`--ledger`, it never opens a bus checkout, and it sends no note. A fold's
numbers still depend on no network call.

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
| `fold --all` | 1 FOLD + 10 SOURCE + 20 UNREADABLE + 1 MORE + 20 UNPARSED + 1 MORE + 20 SUPERSEDED + 1 MORE + 20 CONFLICT + 1 MORE + 20 TOUCHED + 1 MORE + 20 MIXED + 1 MORE + 20 DAY + 1 MORE + 1 OK + 1 NOTE = 160 | under 28 KB |
| `report` | up to 200 pairs x 5 types = 1,000 lines on stdout, uncapped, because the body is the artifact and a capped report would be a count sent as a total; 1 REPORT OK on stderr | under 64 KB |
| `sum --month` | 1 MONTH + 20 PAIR + 1 MORE + 20 MODEL + 1 MORE + 1 TOTAL + 1 OK = 45 | under 10 KB |
| `check` | 20 FAIL + 1 MORE + 20 MISSING + 1 MORE + 20 STRAY + 1 MORE + 1 count line = 64 | under 8 KB |
| `sources --all` | 10 SOURCE + 20 UNREADABLE + 1 MORE + 20 UNPARSED + 1 MORE + 1 OK = 53 | under 8 KB |
| `publish` (2026-09-12, rules 22–31) | 1 PLAN + 20 FILE + 1 MORE + 1 SUBSET + 20 EXCLUDED + 1 MORE + 1 OK + 1 NOTE = 46 at a batch of 20 files; a refusal is at most 20 DIRTY + 1 MORE + 20 FILE + 1 MORE + 1 FAIL + 1 NOTE + one `REFUSED` per independent problem, that list being finite and enumerated by the exit-code table's amendment (eighteen), so 20 + 1 + 20 + 1 + 1 + 1 + 18 = 62 | under 12 KB |

These are ceilings that do not grow with the state. A test builds that state
in `t.TempDir()`, runs every verb, and asserts the line and byte counts
against the table; the measured numbers go into the commit that first passes
it (lesson 169).

**The fold's cost is one pass over each source file.** Each declared file is
opened once per run, and a test counts opens; no subprocess but `sqlite3`
runs, and the same test asserts it. (**Amendment, 2026-09-12 (rule 27).**
This paragraph is the fold's cost and its scope is the read side; `publish`
starts `git`, is not on any read path, and its own bound is the `publish` row
above.) There is no index and no
incremental mode: a day file is recomputed whole from the sources every
time, and the day's own transcripts are the only thing that must be read to
compute it. The prototype read 2,497 files in about ten seconds; the
two-minute rule holds with room, and it is a count that is pinned, not a
time (lesson 167).

## Publishing to the git ledger

Glenn, live, 2026-09-12: "nova-tokens can and should provide an easy way to
upload token daily work to git." And, the same day: "not sure why this got
outlawed. we need to collate across multiple machines. git makes sense."

The history, honestly, in one sentence: v1 of this spec was written as a
local, read-only measurer of one bench, before the ledger repository existed
— `mas-bandwidth/tokens` was created on 2026-09-12, at Glenn's instruction,
after rules 16 and 19 — so the prohibition predates the need, and this
amendment is the collation across machines he names.

**What it is.** One more verb, `publish`. It uploads one **contribution** to a
clone of a git ledger the caller names. It is the only verb that runs `git`,
the only verb that writes into a git clone, and it runs on an explicit
invocation, never on a timer.

**A contribution is one of two typed kinds, and they never mix.** The
retained batch is the endpoint; the aggregate exists so a bench with day files
today is not blocked, and neither it nor the public subset may delay the
endpoint (Stella, 2026-09-12).

| kind | flag | what it is | where it lands | destination is |
|---|---|---|---|---|
| **retained batch** — the endpoint | `--batch <dir>` | one `nova.tokens.coverage/2` envelope, the `observation/2` shards it references and any new `mapping/2` envelopes | `records/…`, `mappings/…`, `coverage/…`, the retained-format packet's paths (rule 29) | **content-addressed and immutable**: a path's name is its bytes' digest, so a path is written once and never replaced |
| **v1 day file** — aggregate transport, explicitly typed | `--v1-day <dir> --day <d> --seat <label>` | one `<dir>/<day>.tsv` this bench folded | `v1-days/<seat>/<YYYY-MM>/<day>.tsv`, a subtree no records path uses | **mutable and named by the day**, so it has the one replace transition this spec allows, under `--supersede` (rule 26) |

**What a user gets.**

```
nova-tokens publish --v1-day out --day 2026-09-12 --seat studio \
                    --ledger ~/tokens --remote origin --branch main \
                    --repo github.com/mas-bandwidth/tokens
PUBLISH PLAN at=<stamp> build=<id> kind=v1-day ledger=/Users/x/tokens repo=github.com/mas-bandwidth/tokens remote=origin branch=main seat=studio day=2026-09-12 contribution=<hex> files=1 bytes=4210 deadline=60 public=-
PUBLISH FILE path=v1-days/studio/2026-09/2026-09-12.tsv sha256=<hex> rows_sha256=<hex> state=new supersedes=-
PUBLISH OK contribution=<hex> commit=<sha> pushed=true state=published attempts=1 files=1 at=<stamp> build=<id>
```

**What does not change.** Nothing on the read side. `fold`, `sum`, `check`,
`sources` and `report` run no `git`, open no network, and read a bus checkout
as files, exactly as rules 16 and 19 say; the amendment clause under rule 19,
dated 2026-09-12, narrows "no `git` at all" to those five verbs and quotes
Glenn's sentence as its reason. A friend's daily note still reaches the bus
the way rule 20 and [SPEC-BUS-DELIVERY](SPEC-BUS-DELIVERY.md) say, and that
is a different act from this one (rule 28).

**Two documents reconciled here.**
[PROPOSAL-TOKENS-FORMAT.md](PROPOSAL-TOKENS-FORMAT.md) spells this verb
`nova-tokens records publish`; the spelling in this spec is
`nova-tokens publish --batch`, because `publish` is one verb over both typed
kinds and `records` is not a verb namespace of this binary (Stella's
packaging decision, 2026-09-12). That packet's "at most 3 race retries within
60s" is adopted whole and is **the** bound: `--deadline <seconds>`, default
60, bounds the entire fetch, build, push and retry sequence, and
`--attempts`, default 3, bounds the retries inside it. `--git-timeout`
(default 120) bounds **one subprocess** and is not that budget; where the two
disagree the deadline governs, and its exhaustion is `reason=unconfirmed`.

The verb's synopsis is in **the verbs**, its lines are in **the output
grammar**, its bound is the `publish` row of **the largest plausible state**,
and its exit codes are the clause under **exit codes**. The streams are this
spec's own: `PUBLISH PLAN`, `FILE`, `SUBSET`, `EXCLUDED`, `OK` and `NOTE` go
to stdout, `PUBLISH DIRTY`, `FAIL` and `REFUSED` to stderr, and a
`PUBLISH MORE` to the stream of the kind it caps. **`REFUSED` is exit 2 and
`FAIL` is exit 1**, here as everywhere: a refusal is the verb declining to
run, a `FAIL` is the verb running and saying NO. `DIRTY`, `FILE` and
`EXCLUDED` are each capped at `--max`, per kind; `PUBLISH NOTE` is exactly one
remedy line. The rules are numbered on from rule 21.

22. **`publish` is the one verb that runs `git`, it publishes one typed
    contribution per invocation, and it runs on an explicit invocation, never
    on a timer.** `--batch` and `--v1-day` are mutually exclusive and one is
    required; `--day` and `--seat` belong to `--v1-day` alone and are refused
    with `--batch`, because a retained record carries its own day and its own
    origin (rule 29). There is no `--all` and no "today": a clock never
    chooses what is uploaded, for the reason rule 12 gives. The verb reads the
    contribution's files and writes nothing under `--out`, `--v1-day` or
    `--batch`. It schedules nothing and wakes nothing; a LaunchAgent that runs
    it is the bench's. `--ledger <dir>` is an existing clone the caller made
    and is authorized for; `publish` never clones, never creates a remote or a
    branch, never changes configuration or access, and acquires no credential,
    because a tool that provisioned access would be a tool that could acquire
    it (Stella: the caller selects an existing authorized clone; configuration
    and access changes are not part of this verb). **No local branch, no
    `HEAD`, no index and no working tree in the clone is changed**: the commit
    is built with plumbing on the fetched head and pushed by object id
    (rule 23), so a rejected push or a death mid-run strands nothing (rule 24).
    **No other verb publishes anything**, ever, as a side effect of its own
    job, and no verb installs or updates anything on the way.
    **Demanded test.** A bare repository and a real clone of it, both under
    `t.TempDir()`, a fake `git` on `PATH` for argv and real `git` for
    behaviour: one `publish --v1-day` lands one commit carrying one path;
    `--batch` with `--v1-day`, `--batch` with `--day` or with `--seat`, and
    neither kind given, are each exit 2 naming the flags; `--all` is exit 2;
    nothing under `--out`, `--v1-day` or `--batch` changed by bytes or mtime;
    no invocation names a path outside `--ledger` and the contribution; a
    `--ledger` that is not a git checkout is exit 2.

23. **`publish` uploads the contribution's bytes exactly, and builds its
    commit with plumbing.** Every file goes into the ledger byte-for-byte:
    nothing is reformatted, re-sorted, re-stamped or re-hashed, and a v1 day's
    `at=` in the ledger is the fold's.

    **The bytes are raw.** The blob is written
    `hash-object -w --no-filters --stdin`, and `--no-filters` is the whole
    point: a ledger with `*.tsv text eol=lf` in its `.gitattributes`, or a
    clone with `core.autocrlf`, would otherwise rewrite a byte the digest
    already counted — measured, 2026-09-12, Stella: input
    `636f6c3109636f6c320d0a` stored as `636f6c3109636f6c320a` through
    `--path`. No `--path` is passed, because with filters off it decides
    nothing and would only suggest they were on.

    **The commit.** The blobs go into a tree built from the fetched head's
    tree (`read-tree` into this run's own private index, rule 31 — the run
    creates the empty **directory** exclusively and lets `read-tree` create
    the index file inside it, because a zero-length file at `GIT_INDEX_FILE`
    is not an empty index and `read-tree` refuses one — then
    `update-index --add --cacheinfo 100644,<blob>,<path>`, then `write-tree`);
    the tree becomes a commit whose parent is that fetched head
    (`commit-tree`); and that commit is pushed by object id,
    `push <remote> <sha>:refs/heads/<branch>`, no leading `+`, no force of any
    spelling. The commit carries **exactly the contribution's new paths and
    nothing else** — for a batch the shards, mappings and coverage envelope it
    adds, at rule 29's paths, every mode `100644`, no symlink; a shard the
    fetched tree already holds with identical bytes is **referenced, not
    rewritten**, and is not in the commit (rule 24). A rejected push or a
    death before it leaves only unreachable objects, which git's own
    housekeeping collects.

    **What is guaranteed about refs, exactly.** `HEAD`, every local branch,
    the index and the working tree are unchanged. `fetch` runs with the empty
    `--refmap=` (invocation 5), so it ignores the clone's configured fetch
    ref mapping: publication never moves a named local ref or a named
    remote-tracking ref, whatever `remote.<name>.fetch` is. The one ref it
    writes is `FETCH_HEAD`; no remote-tracking ref, no tag, no note, no
    `refs/stash`. The demanded test asserts that promise and not the
    impossible one.

    **The argv is an allowlist, and the test is an equality check against
    it.** In this order, with these flags and no others:

    | # | argv after `git` | why |
    |---|---|---|
    | 1 | `-C <ledger> rev-parse --git-dir` | is this a checkout |
    | 2 | `-C <ledger> remote get-url --all <remote>` and `remote get-url --push --all <remote>` | the **effective** fetch and push destinations, rewriting applied, read only (rule 30) |
    | 3 | `-C <ledger> config --get user.name` and `config --get user.email` | whose name the ledger records, read from configuration and never auto-detected |
    | 4 | `-C <ledger> status --porcelain --untracked-files=all` | rule 24's dirty check |
    | 5 | `-C <ledger> fetch --no-tags --refmap= <remote> refs/heads/<branch>` | the head to build on |
    | 6 | `-C <ledger> rev-parse FETCH_HEAD` | that head's object id |
    | 7 | `-C <ledger> ls-tree -r <sha> -- <path>…` and `cat-file blob <sha>` | what this contribution's paths already hold |
    | 8 | `-C <ledger> hash-object -w --no-filters --stdin` | one blob per new file, raw |
    | 9 | `-C <ledger> read-tree <sha>`, `update-index --add --cacheinfo …`, `write-tree` | the tree, in this run's private index |
    | 10 | `-C <ledger> commit-tree <tree> -p <sha> -F -` | the commit, message on stdin |
    | 11 | `-C <ledger> push <remote> <sha>:refs/heads/<branch>` | the one write |

    Invocations 1 to 4 run once per run; 5 to 11 run once per attempt, up to
    `--attempts` and inside `--deadline`; and 8 to 10 are skipped on an attempt
    whose step 1 finds the contribution already published (rule 24). That is
    the shape the equality check expects, so a second attempt has a defined
    argv rather than an exception.

    No other subcommand may appear, and in particular none of `add`, `commit`,
    `merge`, `rebase`, `reset`, `clean`, `rm`, `checkout`, `switch`, `stash`,
    `branch`, `tag`, `clone`, `remote set-url`, `remote add`, `update-ref`,
    `gc`, `prune`, `filter-branch`, `config --add`, or `config <key> <value>`;
    and no `push` carrying `--force`, `--force-with-lease`, `--delete`,
    `--mirror`, `--tags`, `--all`, a refspec with a leading `+`, or a refspec
    whose left side is not the object id this run built. `publish` passes no
    `-c <key>=<value>` and no `--config-env`: an inline setting is a
    configuration write with a shorter life. Every invocation runs with
    stdin from `/dev/null` except the two fed on purpose (8 and 10), with no
    controlling terminal, with `GIT_TERMINAL_PROMPT=0`, and with `GIT_ASKPASS`
    set to this tool's own binary in a mode whose only behaviour is to exit 1
    — named, not guessed (rule 1). Those cover two different prompts: over
    HTTPS it is `GIT_ASKPASS` and `GIT_TERMINAL_PROMPT` that turn a credential
    prompt into a refusal, and over SSH it is the `/dev/null` stdin and the
    absent terminal, because `GIT_ASKPASS` never reaches `ssh` — an SSH
    passphrase prompt has nowhere to read from and fails instead of waiting. It sets `GIT_INDEX_FILE` for invocation 9 and no
    `GIT_AUTHOR_*` or `GIT_COMMITTER_*` at all. **The author and committer are
    the clone's own configured identity**, read at invocation 3 by
    `config --get` and never by `git var`, which auto-detects a
    `<user>@<host>` identity when the configuration is empty and cannot say
    which it returned: an empty `user.name` or `user.email` is exit 2 naming
    which one, so the refusal cannot pass or fail by hostname. The commit author is **not** provenance: a record's
    friend and bench are the record's own (rule 29).

    **The message is derived from the bytes.** Subject
    `tokens: <kind> <contribution id> files=<n>`; body one sorted line per new
    path, `<path> sha256=<hex>`, then `inventory_sha256=<hex>` (rule 26's
    always-written digest over exactly those lines), then for a v1 day its `day=`,
    `seat=`, `rows_sha256=` and the file's own `at=` and `build=`, and
    `supersedes=<rows_sha256>` on a `--supersede` run (rule 26). Nothing else.
    No force, no amend, no rebase, no tag: a correction is a new commit.
    **Demanded test.** Every file on the remote branch byte-identical to the
    contribution's, a v1 day's version line and trailing newline included; the
    commit's tree differing from its fetched parent's in exactly the new
    paths, mode `100644`; the message recomputed from the bytes and compared.
    **The byte guarantee, red first**: a ledger with `*.tsv text eol=lf`
    committed in `.gitattributes` and a day file containing CRLF publishes a
    blob whose bytes are the file's, and the same run without `--no-filters`
    fails the test. **The ref guarantee**: after a successful publish, a
    rejected one and a kill, `HEAD`, every local branch, the index and
    `git status --porcelain` are unchanged, every named remote-tracking ref
    is where it was, `FETCH_HEAD` alone may have changed, and no other ref in
    the clone moved. (**Amendment, 2026-09-12 (rule 23).** The empty
    `--refmap=` removes the verb's dependence on the clone's configured fetch
    mapping rather than narrowing which authorized clones work, so the
    demanded test adds a custom-refmap fixture: a clone whose
    `remote.origin.fetch` is a custom mapping such as
    `+refs/heads/*:refs/heads/*` must **publish successfully** with the
    explicit `--refmap=` form, and every named local ref must be unchanged
    afterwards — that clone is **not** refused because of its configuration.
    The negative control is the same clone with `--refmap=` omitted, where the
    custom mapping updates a local branch and the ref-preservation assertion
    **fails**. Synthetic local clones only, no ledger.) A clone with `user.email` unset is exit 2 naming it, with no object
    written, **and the same on a bench whose hostname would give `git var` a
    plausible identity** — the red being an identity read through `git var`; a
    batch naming a file it does not reference, a symlink, or a mode other than
    `100644` is refused before any object is written; and the fake `git`'s
    argv is compared **for equality** against the table — position,
    subcommand and flag set, an unlisted flag failing as loudly as an unlisted
    subcommand, and the push refspec asserted to be exactly
    `<the built sha>:refs/heads/<branch>`.

24. **What `publish` refuses, and how an interrupted or incremental
    contribution resolves.** Every independent problem prints at once, one
    line each. After each refusal the remote is byte-identical, the clone's
    local refs are unchanged by object id, and the caller's files, index and
    commits are untouched; that sentence is stated once and holds for every
    case below.
    - **Unrelated dirty work is exit 2.** Any modified, staged or untracked
      path under `--ledger` is one `PUBLISH DIRTY` line (capped, MORE on the
      same stream). The word is **unrelated**, SPEC-BUS-DELIVERY's own:
      everything in that working tree is unrelated to this verb, because
      rule 23 writes nothing into one. Another tool's pending contribution is
      not this tool's to publish, and its valid trailer is not permission
      (SPEC-BUS-DELIVERY, point 4).
    - **A local commit the remote lacks is not a refusal**, and a
      **clean-but-behind** clone publishes normally: rule 23 builds on the
      fetched head, so being behind is not a state to repair and `publish`
      repairs none of it.
    - **The resolution, one state machine**, run under the lock (rule 30)
      after `fetch`. Every reference of the contribution is first resolved and
      validated against **the fetched tree together with the batch**, because
      a batch referencing immutable objects the ledger already holds is the
      ordinary second daily contribution and not a failure
      ([PROPOSAL-TOKENS-FORMAT.md](PROPOSAL-TOKENS-FORMAT.md): references may
      resolve to byte-identical objects already in the ledger). Then, by the
      **contribution id's own path** — the coverage envelope's path for a
      batch, the day file's path for a v1 day:
      1. **Absent.** Nothing of this contribution is published yet. Write, in
         one commit, exactly the referenced files the fetched tree does not
         already hold, plus the coverage envelope (or the day file). An
         already-present immutable path with identical bytes is preserved and
         referenced, never rewritten; a v1 day's present-and-equal case is
         rule 26's no-op.
      2. **Present.** For a **batch**, require the **complete referenced
         closure** — every shard and mapping the envelope names, at its own
         path, with identical bytes — before reporting
         `state=already-published`, exit 0, no commit, no push; a closure
         member missing or holding different bytes is a broken prior
         publication: `PUBLISH FAIL reason=incomplete`, exit 1, naming each
         path and which it is, nothing written, nothing overwritten. For a
         **v1 day**, the comparison is rule 26's and not a byte comparison:
         the same `rows_sha256` is `state=identical` and
         `state=already-published`, exit 0, **whatever that file's version
         line says**; a different `rows_sha256` is
         `PUBLISH FAIL reason=differs`, exit 1, until `--supersede`, which
         replaces that one path in a new commit and prints
         `state=superseded`. This is the only door in this machine through
         which a destination's bytes are replaced, and `--supersede` is the
         only key (rule 26).
      3. **Conflicting bytes at an immutable path** — a `records/`,
         `mappings/` or `coverage/` path present with bytes other than the
         digest that names it demands — is `PUBLISH FAIL reason=conflict`,
         exit 1, naming the path: a content-addressed path is written once,
         and this tool never replaces one. Only the v1 day's mutable path has
         a replace transition, and only under `--supersede` (rule 26).
      4. **Identity is bound to the destination path.** Matching bytes at some
         other path are never a find, so a shard that exists elsewhere does
         not make this contribution published.
      5. **Reuse, never duplicate.** An object this run would write that the
         clone's object database already holds with the exact bytes — blob,
         tree or commit — is reused, which is SPEC-BUS-DELIVERY's "reuse an
         existing pending commit when possible" in this verb's shape. Reuse is
         by content digest, so it cannot pick up another bench's work.
      6. **Rejected, not fast-forward** — two benches pushing one branch, the
         ordinary case of collation. Fetch again, re-run this machine against
         the **new** head, and either report `already-published` or rebuild and
         push, inside `--attempts` and `--deadline`. The other bench's commit
         is preserved untouched and no append of theirs is dropped; a
         rejection is never answered with a force.
      7. **The answer was lost**, including a death between `commit-tree` and
         `push`: the same machine decides it, so nothing is ever pushed twice
         under two identities.
      Exhaustion of `--attempts` or `--deadline` is exit 1
      `reason=unconfirmed`, with the contribution id printed and every input
      still on disk for an explicit retry. `pushed=` says which kind of
      exhaustion it was, because this spec writes an unknown as `-` and a
      rejection is not unknown: **`pushed=false`** when the last attempt ended
      in a rejection the remote reported, **`pushed=-`** when the last attempt
      ended with no answer at all. Only the remote's own answer establishes
      success.
    - **A contribution that is not there, or does not validate.** A v1 day
      file missing under `--v1-day` is exit 2; one `check`'s row rules would
      name is exit 1 `reason=malformed` naming the line. A `--batch` directory
      missing, or holding anything but its own named immutable files, is exit
      2; envelopes that do not validate are exit 1 `reason=malformed`; a
      schema this build has no validator for is exit 2 `refusing to guess`
      naming the validator (rule 29).
    - **A fold that may be in flight is exit 2.** `<dir>/<day>.tsv.tmp`
      present under `--v1-day`, or that directory's `fold.lock` held, is named
      and `publish` stops: half a day is not a day. The lock is probed by
      opening `fold.lock` **without** `O_CREATE` and asking for it without
      waiting, so an absent lock file is not a held lock and the probe creates
      nothing.
    - **Input that moves under the publication is exit 1** `reason=changed`
      naming the file (rule 31's snapshot).
    - **A public subset that would carry a row not on its allowlist** is
      exit 1 `reason=subset` (rule 25).
    - **Never**: a credential prompt, a credential written or printed
      anywhere, a configuration or access change, a stash, a reset, a clean, a
      checkout over somebody's work, or the removal of any file this run did
      not make (rule 9's amendment, rule 23's allowlist).
    **Demanded test.** Each refusal red on its own, with the invariant
    sentence above asserted after every one. Unrelated dirty work (modified,
    staged, untracked) is exit 2 naming the path; a clone with an unrelated
    local commit, and one a dozen commits behind, each publish, exit 0, that
    commit still unpushed and unmoved. A v1 day file with ten columns is exit 1
    `reason=malformed`; a `<day>.tsv.tmp` beside it is exit 2; a held
    `fold.lock` is exit 2 while an absent one publishes and leaves none
    behind. Then, against real bare remotes:
    - **The incremental batch, the case that must not refuse.** Day one lands
      shards A and B and coverage C1. Day two's batch references A (already
      there, byte-identical) and adds shard D and coverage C2: it publishes,
      exit 0, one commit carrying **D and C2 only**, A untouched and not
      rewritten, and `PUBLISH FILE` says `state=present` for A and
      `state=new` for the rest — the red being a rule that called this
      `incomplete`.
    - **The genuinely broken prior publication.** Coverage C1 present but
      shard B absent, and C1 present with shard B holding different bytes, are
      each exit 1 `reason=incomplete` naming every path and its state.
    - **Conflict at an immutable path.** A shard path present with bytes that
      are not its digest's is exit 1 `reason=conflict`, and `--supersede` does
      not change that.
    - **Rejected, not fast-forward.** A second writer pushes an unrelated path
      in the seam between this run's `fetch` and `push`: the push is rejected
      and, inside `--attempts`, the contribution lands on top of the other
      writer's commit with its bytes untouched; the refspec never carries `+`;
      with `--attempts 1` the run is exit 1
      `reason=unconfirmed pushed=false` — a rejection is an answer — the
      remote holds the other writer's commit alone, and the next plain
      `publish` succeeds with no `--supersede` and no manual cleanup; a run
      whose last attempt got no answer at all prints `pushed=-` instead, and a
      test asserts the two apart. With `--deadline 0` refused and a deadline
      that expires mid-retry, the same
      `unconfirmed`.
    - **Killed between commit and push.** SIGKILL after `commit-tree` returns
      and before `push` (the fake `git` blocks in `push` for the test): the
      remote has nothing, the clone has no moved local ref and nothing staged,
      and **the next plain `publish` succeeds**, reusing the objects by digest
      and landing one commit — no manual cleanup, no exit 2 from a leftover
      (rule 31). Killed after the push landed but before its answer was read:
      the next invocation prints `state=already-published pushed=false
      attempts=0`.
    - **Two concurrent publishers**, rule 30's witness.

25. **The public subset is open-source repositories only, by an explicit
    allowlist, it stays local, and it never gates the ledger.** Glenn,
    2026-09-12: the ledger and the total reports are private, and a public
    report is an open-source subset only. Stella, the same day: keep it local
    and explicit, publication a separate act, and let it wait rather than
    delay the retained endpoint — so this rule is **optional to build, after
    rules 22 to 24 and 29 to 31**, and nothing in it gates them. `--public
    <dir>` writes one file, `<public>/<day>.tsv`, through rule 31's owned
    staged path **in that same directory** and one rename, and **pushes
    nothing**. It is offered for a v1
    day and refused with `--batch`, whose public shape is a records-layer
    decision nobody has made. `--public-repos <file>` is required with it: a
    person's file, one exact repo name per line, no patterns, because a
    pattern can admit a repository nobody vetted; `#` and blank lines are
    skipped; `unknown`, `other` and `unattributed` may never appear in it, and
    a file naming one is exit 2 naming the line, since a row nobody attributed
    cannot be shown to be open source. The subset's rows are exactly the rows
    whose `repo` cell is literally in that file; every other row is excluded,
    counted `excluded=<n>` on `PUBLISH SUBSET`, named by a capped
    `PUBLISH EXCLUDED`, and the subset's first line is
    `nova-tokens v1 subset day=<d> at=<stamp> build=<id> rows=<n> excluded=<n>`,
    so it can never be read as a total. The `sources` column is the fixed word
    `withheld`, because a source label is a person's bench and not a fact
    about an open-source repository; by the same reasoning the **seat is not
    on that first line** either. `--public-sources` writes the labels through
    and adds `seat=<label>`: one flag, one decision, typed deliberately. The
    subset is not a day file and `check` is not pointed at it. Before any byte
    is written, the rows about to be written are checked against the
    allowlist; one that is not on it is `PUBLISH FAIL reason=subset`, exit 1,
    nothing written anywhere.
    **Demanded test.** A day with rows for an allowlisted `schema`, for
    `unknown` and for a private `bench-secrets`: the subset carries `schema`
    only, `excluded=2` with both named on stdout, the same case at `--max 1`
    printing one `EXCLUDED` and its MORE line on stdout too (two lines under
    the default `--max` print no MORE, per rule 11), `sources` reading
    `withheld`, the first line carrying `rows=` and
    `excluded=` and no `seat=`, and `seat=` present with `--public-sources`;
    an allowlist naming `unknown`, `other` or `unattributed` is exit 2 naming
    the line; `--public` without `--public-repos`, `--public-sources` without
    `--public`, and `--public` with `--batch` are each exit 2; and the
    pre-write check handed a row the selector should have dropped is exit 1
    `reason=subset` with no file under `--public`, no staged file beside it and
    no commit — the red being that without the check the private row lands.
    There is no `--stage` flag to test: rule 31 stages inside `--public`.

26. **Identity, and the one replace transition.** A contribution's **id** is
    defined per kind, and nothing else is required to equal it:
    - **a batch**: the coverage envelope's own content id, which
      [PROPOSAL-TOKENS-FORMAT.md](PROPOSAL-TOKENS-FORMAT.md) defines as the
      SHA-256 of its canonical body with filenames excluded, and which that
      packet already calls the contribution id. This spec adds no second
      definition of it (Stella, 2026-09-12: the two inputs are different, and
      a listing that included the coverage path would make that path's name
      depend on a digest computed over it).
    - **a v1 day**: `rows_sha256`, the digest of every line **below** the
      version line — the rows, their tabs and the final newline, and nothing
      of the stamp.
    `inventory_sha256` is a separate digest over the sorted
    `<path> sha256=<hex>` lines of the paths a commit adds. It is **always
    written** in a commit's message and is **never an identity**: it is
    checked only against its own definition, it is never required to equal a
    contribution id, nothing is looked up by it, and no filename depends on
    it. A v1 day also prints `sha256=`, the whole
    file.

    **The v1 row-equivalence exception, stated once and used everywhere.** The
    `v1-days/` path is mutable and named by the day, so a destination whose
    bytes differ from the file at hand is not automatically a conflict; its
    three transitions are: **absent** → publish; **present with the same
    `rows_sha256`** → `state=identical`, `PUBLISH OK … pushed=false
    state=already-published attempts=0`, exit 0, no commit and no push, the
    bytes already in the ledger left exactly as they are, and nothing claimed
    to be recorded anywhere, because nothing was written; **present with a
    different `rows_sha256`** → `PUBLISH FAIL reason=differs`, exit 1, until
    `--supersede`, which replaces that one path in a new commit whose message
    carries `supersedes=<the rows_sha256 it replaces>`, the old bytes staying
    in history. On the other two transitions `--supersede` changes nothing at
    all: an **absent** path publishes exactly as it would without the flag,
    and an **identical** one is the same no-op, exit 0
    `state=already-published`, with no commit and no supersession recorded —
    the flag is permission to replace, never an instruction to write.
    `--supersede` exists for this transition only: it is refused
    with `--batch`, because a content-addressed path is never replaced and a
    retained correction is a new observation or a new coverage envelope
    (rule 24's conflict case). So a bench that folds and publishes on a
    schedule prints `already-published` for a day whose rows have not changed,
    even though its `at=` moved, and never needs `--supersede` to do it.
    Identity is never a commit id, because a commit carries a clock.
    `already-published` is
    [SPEC-BUS-DELIVERY](SPEC-BUS-DELIVERY.md)'s word, deliberately.
    **Demanded test.** Publish, then publish again unchanged: the second is
    `state=already-published pushed=false attempts=0`, exit 0, with no
    `hash-object`, no `commit-tree` and no `push`, and one commit touching
    that path. **Re-fold the same sources to the same rows** so only `at=` and
    `build=` differ: still `state=identical`, exit 0, no commit, no
    `--supersede`, and the ledger's bytes are the first upload's — the red
    being an identity over the whole file, which would make this
    `reason=differs` and turn every scheduled publish into a supersede chain
    of identical rows. A day whose rows changed by one cell is
    `reason=differs`, and `--supersede` lands one commit whose message carries
    the replaced `rows_sha256`; `--supersede` over an absent path publishes
    the same commit a plain run would, and over an identical one is
    `state=already-published` with no commit; `--supersede` with `--batch` is exit 2. A
    batch's printed `contribution=` equals the coverage envelope's content id
    recomputed by the test from the canonical body, and an
    `inventory_sha256` that disagrees with the commit's own path list fails
    while a contribution id that differs from it does **not**.

27. **The read side runs no `git`, and one test holds that line.** `fold`,
    `sum`, `check`, `sources` and `report` invoke no `git`, open no network
    and read a bus checkout as files; rule 16 is unchanged for sources and
    rule 19's sentence is narrowed to exactly those five verbs by its
    amendment clause. `publish` is the only verb with a `git` subprocess, the
    only verb that takes `--ledger`, `--remote`, `--branch`, `--repo`,
    `--batch`, `--v1-day`, `--seat`, `--supersede`, `--public`,
    `--public-repos`, `--public-sources`, `--deadline`, `--git-timeout` or
    `--attempts`, and it reads no source: a source flag on `publish` is exit 2.
    Rule 31 owns the stronger check a flag list cannot make — that no two
    paths the verb was **given** resolve to one directory — and it is honest
    about its limit: `publish` is given no source flags, so it cannot discover
    that its ledger or output directory is also somebody's transcript
    directory somewhere else, and this spec says so rather than implying a
    guarantee. This tool imports no `net` package; the network is `git`'s.
    **Demanded test.** With a fake `git` on `PATH`, a fixture bus holding
    competing notes and a ledger clone beside the sources: `fold`, `report`,
    `sum`, `check` and `sources` each leave the fake `git`'s record empty
    (demanded test 16's half, now scoped); a source walk over every package of
    this binary finds `exec.Command` in exactly two files —
    `internal/tokens/opencode.go` and the publish file — the other spawners
    `os.StartProcess` and `syscall.ForkExec`/`Exec`/`StartProcess` nowhere,
    a syntax-tree pass that flags a string literal naming the git program
    outside the explicit publisher,
    and no `net` import anywhere; `publish --claude
    x=<dir>` is exit 2; `--ledger` on any read verb is exit 2 as an unknown
    flag.

28. **The note to the bus and the upload to the ledger are two acts, and
    neither stands in for the other.** Rule 20 is unchanged: `report` prints
    the body, `--note` writes exactly those bytes on `REPORT OK`, and the note
    reaches the bus by a person's hand or by nova-bus's prepared delivery
    ([SPEC-BUS-DELIVERY](SPEC-BUS-DELIVERY.md)) — a different repository, a
    different artifact, a different destination. `publish` sends no note,
    writes nothing into a bus checkout and names no participant; `report`
    pushes nothing and takes no ledger flag.
    **Demanded test.** `publish` over a fixture holding a bus checkout beside
    the ledger leaves that checkout byte-identical and invokes no bus command;
    `report --ledger <dir>` is exit 2; one fixture day both reported and
    published lands two artifacts whose bytes differ, each verb invoking only
    its own subprocess.

29. **The retained records are the endpoint, their provenance is each
    record's own, and the missing validators are named dependencies.** Stella, 2026-09-12, deciding the packaging inside Glenn's
    ruling: `nova-tokens` owns an explicit `publish` verb that uploads into
    the caller-selected private git ledger, not a version-report message
    routed through `nova-update`, and the read verbs stay local and
    read-only. The **retained batch is the publication contract**: a
    `--batch <dir>` holding exactly one `batch.json` — one
    `nova.tokens.coverage/2` envelope whose content id is the contribution id
    — the `nova.tokens.observation/2` shards it references at their final
    relative paths, and any new `nova.tokens.mapping/2` envelopes it
    references, and no other file, symlink or executable content. Its
    destinations are the retained-format packet's own, not this verb's
    invention: `records/<friend>/<bench>/<day>/<shard-sha256-hex>.jsonl`,
    `mappings/<mapping-sha256-hex>.json`,
    `coverage/<collector-friend>/<collection-bench>/<UTC-collection-day>/<coverage-sha256-hex>.json`,
    with the reserved `_` for a null friend or bench and `unallocated` for an
    interval-only allocation; that packet's own line, "the ledger README must
    be reconciled to these exact paths before the first publication", is a
    condition on the first publication and not on this spec.
    **Provenance is the record's, never the uploader's**: a shard's
    `friend`/`bench` come from the observation's own origin, the coverage
    envelope's collector fields are the collector's, the commit author is
    whoever holds the clone (rule 23), and an origin is never rewritten,
    relabelled or moved by an upload — a changed origin is a new observation
    or a correction, never a file move. `publish` normalizes nothing, re-seals
    nothing and drops nothing: raw observations, mapping provenance and
    coverage gaps go up as retained, and an envelope naming an unavailable
    source still names it afterwards. **A v1 day file is aggregate transport,
    typed as such**: it lands under `v1-days/<seat>/…`, a subtree no records
    path uses and no records reader reads; the two kinds never appear in one
    invocation or one commit; and no verb reads a `v1-days/` file back as a
    retained record, because the aggregate has no friend, no event identity
    and no coverage, and calling it one would be the reinterpretation
    [PROPOSAL-TOKENS-RECORDS.md](PROPOSAL-TOKENS-RECORDS.md) forbids.
    **The two missing validators are dependencies with owners.**
    `nova.tokens.coverage/2` and `nova.tokens.mapping/2` have no validator in
    this repository today; until they exist, a `--batch` whose envelopes name
    them is exit 2 `refusing to guess`, saying which validator is missing,
    with the batch and its shards left exactly where they are and nothing
    about the retained work discarded, weakened or re-typed to fit the path
    that does work. The work list carries them as the batch path's blocking
    dependency, owned by the records lane (#146's construction API being the
    writer's half, and not a collector, not a publisher, and not evidence that
    collection or publication is delivered).
    **Demanded test.** Fixture batches only, synthetic, no private transcript
    and no real ledger. A validating batch lands every path of that layout,
    the shard paths taken from each record's own friend, bench and day: a run
    whose clone identity and configured author differ from the records' origin
    still writes the records' paths, and an implementation that used the
    uploader's identity fails the test. `_` and `unallocated` are exercised. A
    batch holding an unreferenced file, a symlink or a second `batch.json` is
    exit 2; one naming `nova.tokens.coverage/2` with no validator compiled in
    is exit 2 naming the missing validator, the batch directory byte-identical
    afterwards and nothing pushed; `--batch` with `--day`, `--seat`,
    `--supersede`, `--public` or `--v1-day` is exit 2 (each its own case, and
    none of them a success case); and no `records/` path appears in a v1 day's
    commit, nor a `v1-days/` path in a batch's.

30. **The destination is verified by host, owner, name and branch, for every
    effective URL, and one clone publishes one at a time.**
    `--repo <host>/<owner>/<name>` is **required and has no default**:
    `<owner>/<name>` alone is not a destination, because another host can
    serve the same pair, and a compiled-in repository would be the guess
    rule 1 forbids (Stella, 2026-09-12; this reverses rev 1's default, and the
    ledger's name now lives in the caller's command, this spec's examples and
    the bench's own wrapper). **Both directions are checked, and the push side
    is the one that writes**: `remote get-url --all` and
    `remote get-url --push --all` (rule 23, invocation 2) give the effective
    URLs with `insteadOf` and `pushInsteadOf` rewriting already applied, so a
    `pushurl` that points somewhere else cannot slip past a check of the fetch
    URL alone. Every URL returned, fetch and push, must parse and match:
    SSH `git@<host>:<owner>/<name>[.git]`, `ssh://…`, HTTPS
    `https://<host>/<owner>/<name>[.git]`, host compared
    case-insensitively and the rest exactly. Anything else is exit 2 before
    any write — a mismatch, a shape the parser does not know, or **more than
    one effective push URL that do not all match** (ambiguity is refused, not
    resolved). A URL's userinfo is **never printed**: a diagnostic prints
    host, owner, name and the literal `<redacted>` where credentials were, and
    no `PUBLISH` line ever carries a URL's password. A **local path** remote
    is a first-class case and never dressed up as a forge: it matches only
    `--repo local:<absolute path>`, resolved, which is what a test's bare
    fixture remote uses, so no test asserts a forge identity it does not have.
    **A URL's spelling authorizes nothing**: the check catches the wrong
    destination; access remains the clone's and the caller's. **One clone
    publishes one at a time**: the lock is
    `<git-dir>/nova-tokens-publish.lock`, where `<git-dir>` is what invocation
    1's `rev-parse --git-dir` answered — so a worktree, or a clone whose
    `.git` is a gitdir file, is handled and no path is assumed — canonical and
    **not settable by a flag**, because two publishers free to name different
    lock paths would not be serialized at all. It is taken with the kernel
    lock rule 8 uses, released on death, and a second publisher waits a
    bounded jittered time and then exits 2 naming the holder's pid. It
    serializes this clone only; another bench's race is rule 24's.
    **Demanded test.** A clone whose `origin` is
    `git@example.com:mas-bandwidth/tokens.git` against
    `--repo github.com/mas-bandwidth/tokens` is exit 2 printing both, and the
    same owner and name on the expected host publishes; a clone whose fetch
    URL matches while its **`pushurl` names another host** is exit 2 before
    any object is written — the red being a check of the fetch URL alone; two
    push URLs, one matching and one not, are exit 2 for ambiguity; a
    `url.<base>.insteadOf` rewrite that makes a matching URL out of a
    non-matching one is honoured, because `get-url` reports the effective
    destination; `--repo` missing is exit 2; `https://` and `ssh://`
    spellings of one destination both match; an unknown URL shape is exit 2
    naming it; a URL carrying `user:password@` is refused with `<redacted>` in
    the message and the password absent from stdout, stderr and every log; a
    bare fixture remote at a filesystem path matches `local:<path>` and is
    refused by any `<host>/<owner>/<name>` spelling. **The race witness**: two
    `publish` processes on one clone, the second exit 2 naming the lock
    holder and writing nothing, with no flag able to choose a different lock;
    then two publishers on **two** clones of one remote, concurrent, each
    contributing a different day — both contributions on the branch
    afterwards, neither commit lost, neither file overwritten, each process
    exit 0 or exit 1 `reason=unconfirmed` with no half-state to clean.

31. **This run's temporary artifacts are its own and exclusive, they never
    block the next run, and the bytes under publication are one snapshot.**
    Every path `publish` creates, it creates exclusively and it is the only
    thing that touches: one **private temporary directory per run**,
    `<git-dir>/nova-tokens-publish.<random>/` — `<git-dir>` from invocation
    1's `rev-parse --git-dir`, never an assumed `.git` — created by `mkdir`,
    which fails rather than reuses, holding this run's plumbing index, whose
    file `read-tree` creates inside it (rule 23); and, with
    `--public`, the staged file
    `<public>/<day>.tsv.<same random>.tmp`, created with `O_CREATE|O_EXCL` —
    the one place that refusal belongs — and landing by one rename **in its
    own directory**, because a rename is atomic on one filesystem and fails
    with `EXDEV` across two, and nothing could have required a separate
    staging directory to share the subset's (which is why there is no
    `--stage` flag). The suffix is **per run, not per contribution**: a
    contribution's identity is its content (rule 26) and must be stable, but a
    temporary name must not be, or a run killed after `read-tree` would leave
    a path the retry cannot create and cannot remove, and rule 24's plain
    retry would become an exit 2 — which is exactly the bug this sentence
    exists to prevent (both reads of 2026-09-12 found it). On ordinary exit, success or
    refusal, the run removes **its own** temporary directory and any staged
    file it created and did not rename, which is rule 9's carve-out for files
    THIS RUN makes and nothing more. A crash leaves that directory behind; it
    is named by a suffix no other run uses, so it blocks nothing, and no run
    removes another's — a person may, and `check` names nothing under a git
    directory. **A pre-existing path is never overwritten and never removed**: an
    existing staged path, or an existing temporary directory this run did not
    create, is exit 2 naming it; the rename onto `<public>/<day>.tsv` refuses
    a destination whose bytes differ, exit 2 naming it, for a person to
    resolve. **The alias check is over resolved paths, among the paths the
    verb accepts**: before any write, `--ledger`, `--batch`, `--v1-day`,
    and `--public` are resolved through symlinks, and any two that name one
    directory — or a containment that would make one run's output the next
    run's input, `--public` inside `--ledger`, `--public` equal to `--v1-day`
    — is exit 2 naming both flags. `publish` takes no
    source flag, so it cannot discover that one of these is also a transcript
    directory declared to some other run; that limit is stated here rather
    than implied away (rule 27). **One snapshot**: the contribution's bytes are
    read once, under the lock, and digested; every later step uses those
    bytes; and immediately before the push each input's digest is verified
    again, so a fold that landed a new day file after the lock check cannot
    change what is published — a changed digest is exit 1 `reason=changed`
    naming the file, nothing pushed.
    **Demanded test.** Two runs of **one** contribution get different
    temporary suffixes, and the second is not refused by the first's leftovers
    — the red being a digest-derived name, which makes a killed run's index
    un-creatable and un-removable and turns rule 24's plain retry into an exit
    2. A run killed after `read-tree` leaves its directory behind and **the
    next plain `publish` succeeds**, that leftover untouched. On ordinary exit
    no temporary directory of this run's remains, and an unrelated
    pre-existing directory under the git directory is untouched. A pre-existing staged
    path is exit 2 with the file byte-identical afterwards; a `--public`
    destination that exists with different bytes is exit 2 and untouched;
    and the staged file and its destination are in one directory, so the
    rename cannot fail with `EXDEV` — the red being a staged path in a
    directory a caller could put on another filesystem. Then the aliases, each
    exit 2 naming both flags and each also reached **through a symlink**
    rather than spelled directly: `--public` inside `--ledger`, `--public`
    equal to `--v1-day`, `--batch` equal to `--ledger`. Finally the snapshot: a fold rewrites
    `<day>.tsv` between the lock and the push and the run is exit 1
    `reason=changed` naming the file, the remote byte-identical, nothing
    staged left behind.

## What it deliberately does not do

- **It does not price anything.** Tokens, by type, per model. Dollars are a
  rate card times a count, the rate card changes, and a tool that carried
  one would carry a stale one.
- **It does not claim coverage.** Every total is the sum of what the declared
  sources reported; `dashes=`, `missing=`, `unknown=` and `unreadable=` say
  what it does not cover, and no line calls a sum complete.
- **It does not pull the bus, fetch, push, run `git`, or talk to a network.**
  It reads a checkout as files; competing notes are ordered by what they say
  (`supersedes=`), never by the checkout's history.
  - **Amendment, 2026-09-12 (rules 22, 27, 28).** Still true of the bus and
    of every read verb. `publish` fetches and pushes one contribution to the
    ledger clone it is given, on an explicit invocation, and orders nothing
    for the fold: see **Publishing to the git ledger**.
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
20. **A second tokens note for one day is summed onto the first.** Here a
    correction names what it corrects with `supersedes=`, the older is
    printed as superseded, and two notes with no chain between them are a
    conflict that folds nothing (the bus source).
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
   second's subject carrying `supersedes=<first id>`, with filenames whose
   lexical order opposes their send order: the successor is the day, the
   first prints `TOKENS SUPERSEDED`, `superseded=1`; the same lane with the
   files listed in reverse order, the two files' mtimes swapped, `INDEX`
   rewritten sorted by path as `nova-bus check --full --rebuild-index`
   writes it, and the notes' commit order reversed in a fixture git history,
   gives byte-identical rows and the same `SUPERSEDED` line (fold, rebuild
   `INDEX`, fold: identical); a third note superseding the second folds as
   the day with two `SUPERSEDED` lines (two sequential corrections); two
   notes for one day with no `supersedes=` between them, and two successors
   naming one predecessor, are each `TOKENS CONFLICT … notes=<id,id>`,
   `conflict=1`, no row from that lane for that day, that day's file not
   written and an existing one byte-identical, exit 1, and a later note
   superseding one of the two still leaves the other as a conflict, the
   remedy naming both remaining tips; **the replacement snapshot**: for each
   of the two shapes — two roots R1, R2, and one root with two successors S1,
   S2 — a single-parent correction `supersedes=R1` (or `S1`) still prints
   `TOKENS CONFLICT` with the new note and the unnamed tip in `notes=`, then
   one note whose trailer is `supersedes=<both tips, sorted>` clears it: the
   lane-day folds that note's rows only, two `TOKENS SUPERSEDED` lines name
   the tips, `conflict=0`, exit 0, every old note byte-identical in the
   checkout, and the fold is byte-identical with the files listed in reverse
   order and `INDEX` rebuilt; a trailer naming the same id twice, one with
   the ids unsorted, and one naming both tips where one member is a note of
   another lane, are each `TOKENS UNPARSED` for the whole successor and the
   set applies to nothing; a successor naming a missing id, one naming a note in
   another lane, one naming a note for another day, one naming a note that
   did not parse, and two notes naming each other, are each `TOKENS
   UNPARSED` for the whole successor with the reason and the remedy, the
   earlier valid note stays the day's report, exit 1; a lone note folds;
   the source tripwire finds no `os/exec` call but `sqlite3`.
   **Amendment, 2026-09-12 (demanded test 27).** This tripwire is the bus
   reader's and is scoped to the read side: `internal/tokens`'s source
   readers start `sqlite3` and nothing else, and no reader of a bus checkout
   runs `git`. The whole-binary count is demanded test 27's — `exec.Command`
   in exactly two files, the other spawners `os.StartProcess` and
   `syscall.ForkExec`/`Exec`/`StartProcess` nowhere, a syntax-tree pass that
   flags a string literal naming the git program outside the explicit
   publisher — and the two are read
   together, this one over the readers and that one over the binary.
7. A body line `… input ~100000` folds as 100000, the row has `rough=1`, a
   second rough line on the same row makes `rough=2`, `TOKENS DAY rough=2`,
   and `sum` carries `rough=2` on the pair, the model and the total.
8. A fold killed with SIGKILL between the temp write and the rename leaves
   the old day file entire and `<day>.tsv.tmp` beside it; the next fold
   writes over the temp and renames; `check` does not name the temp as a
   stray; a second concurrent fold on one `--out` waits and exits 2 naming
   the holder's pid; a source test finds no `os.Remove` and no `os.RemoveAll` anywhere in
   the package.
   **Amendment, 2026-09-12 (rules 9, 31).** This tripwire is rule 9's, and
   rule 9's carve-out list is what it reads: it fails on any call it cannot
   match to a carved-out file, and there are exactly two removals in the list
   — the lock sentinel a release removes on a platform with no flock (rule 8,
   already in rule 9 at v1), and `publish`'s own per-run temporary directory
   and unrenamed staged file, which rule 31 requires it to release on ordinary
   exit. A removal of anything else, including any file the tool was given, is
   still the failure this test exists for.
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
    `date` column is the message's day, not the fold's; the version line's
    `turns=` equals the counted messages across the Claude and OpenCode
    sources for that day, is `-` for a day fed by a bus note alone, prints
    on `TOKENS DAY`, and `sum` prints their sum on `SUM MONTH` and `SUM
    TOTAL`, `-` when every day is `-`.
13. `check` over: a file with a missing column, a file whose `date` column
    disagrees with its name, a file with two rows for one `(model, repo)`,
    a file with rows out of order, a file with no version line, a file with
    an empty type cell, a file with `day_basis` empty, a file whose version
    line lacks `turns=`, and a run of days
    `09-07, 09-08, 09-10`: every finding prints one line,
    `CHECK MISSING date=2026-09-09` prints, the count line prints
    `bad=8 missing=1`, exit 1; a file whose type cells are `-` and whose
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
    fake `git` on `PATH` records that it was never invoked, over a fixture
    bus holding competing notes; a source test finds no `net` import.
    **Amendment, 2026-09-12 (demanded test 27).** The fake-`git` half runs
    for `fold`, `report`, `sum`, `check` and `sources`, which invoke none of
    it, with a ledger clone sitting beside the fixture sources; `publish`'s
    `git` invocations are demanded test 27's, and the `net` half is unchanged
    for every verb, `publish` included.
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
    `at=`; the same over a bus lane holding two tokens notes for one day, one
    superseding the other,
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
    `~` or `#`; `--note` writes exactly the stdout bytes and nothing else, through
    `.tmp` and rename, and a `report` that fails leaves a pre-existing
    `--note` file byte-identical with no `.tmp` beside it; `report
    --supersedes <id>` prints `subject=tokens D at=… build=… supersedes=<id>`,
    `--supersedes <id2> --supersedes <id1>` prints `supersedes=<id1>,<id2>`
    sorted, and `--supersedes <id> --supersedes <id>` is `REPORT REFUSED`
    naming the duplicate;
    and a note built from it folds as the successor of `<id>` (two
    sequential `report`s, the second superseding the first, fold to the
    second's rows and one `SUPERSEDED` line);
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

**Rules 22 to 31** are in **Publishing to the git ledger** (2026-09-12), and
each one's demanded test is written out beside its rule there; those
paragraphs are the normative text and these ten lines are the index.

22. A bare fixture remote with a real clone beside it, both in `t.TempDir()`:
    one `publish --v1-day` lands one commit carrying one path and moves no
    local ref; the two contribution kinds are mutually exclusive and one is
    required; a clock never chooses the day; there is no `--all`.
23. Every file in the remote is byte-identical to the contribution's; the
    commit's tree differs from its fetched parent's in exactly the new paths
    at mode `100644`; the message is derivable from the bytes. The byte
    guarantee red first: a committed `*.tsv text eol=lf` and a CRLF-bearing
    day file publish the file's own bytes, and the same run without
    `--no-filters` fails. The ref guarantee: `HEAD`, every local branch, the
    index and `git status` unchanged, only `refs/remotes/<remote>/<branch>`
    and `FETCH_HEAD` permitted to move. An empty `user.email` is exit 2 even
    on a bench whose hostname would satisfy `git var`. The argv compared for
    equality against rule 23's table, with 1–4 once and 5–11 once per attempt.
24. Each refusal red on its own, with the invariant sentence asserted after
    each. The incremental batch that must **not** refuse: a second daily batch
    referencing an already-present shard publishes, one commit, only the new
    paths, the present one `state=present` and not rewritten. The genuinely
    broken prior publication is `reason=incomplete`; a content-addressed path
    holding other bytes is `reason=conflict`, which `--supersede` does not
    change; a v1 day whose rows changed is `reason=differs` and
    `state=superseded` with `--supersede`. Then a non-fast-forward rejection
    that rebuilds on the new head, a deadline that expires mid-retry, and a
    SIGKILL between `commit-tree` and `push` after which **the next plain
    `publish` succeeds** with no manual cleanup.
25. A public subset over a day holding an allowlisted repo, `unknown` and a
    private repo: the allowlisted rows only, both others named and counted,
    the MORE line exercised at `--max 1`, the header saying it is a subset and
    carrying no `seat=` without `--public-sources`; an allowlist naming a
    bucket word is exit 2; `--public` with `--batch` is exit 2; the pre-write
    check handed a row the selector should have dropped is exit 1
    `reason=subset` and writes nothing anywhere.
26. A second `publish` of the same contribution is
    `state=already-published pushed=false attempts=0`, exit 0, with no
    `hash-object`, no `commit-tree` and no `push`; a re-fold to the same rows
    with a later stamp is still `state=identical`; only rows that changed are
    `reason=differs`; `--supersede` with `--batch` is exit 2; a batch's
    printed `contribution=` equals the coverage envelope's own content id
    recomputed from its canonical body, and an `inventory_sha256` that
    disagrees with the commit's path list fails while a contribution id that
    differs from it does not.
27. `fold`, `report`, `sum`, `check` and `sources` never invoke the fake
    `git`, with a ledger clone beside the sources; the source walk finds
    `exec.Command` in exactly two files, the other spawners `os.StartProcess`
    and `syscall.ForkExec`/`Exec`/`StartProcess` nowhere, and a syntax-tree
    pass flags a string literal naming the git program outside the explicit
    publisher; a
    source flag on `publish`, and `--ledger` on any read verb, are each exit 2.
28. `publish` leaves a bus checkout beside the ledger byte-identical and
    writes no note; `report --ledger` is exit 2; one day reported and
    published lands two artifacts whose bytes differ, neither verb running
    the other's subprocess.
29. A fixture batch lands every path of the retained layout, the shard paths
    taken from each record's own friend, bench and day and never from the
    uploader's identity; `_` and `unallocated` are exercised; a batch holding
    an unreferenced file, a symlink or a second `batch.json` is exit 2; a
    batch naming a schema with no validator compiled in is exit 2 naming the
    missing validator, the batch untouched and nothing pushed; `--batch` with
    `--day`, `--seat`, `--supersede`, `--public` or `--v1-day` is exit 2, each
    its own case and none a success; no `records/` path appears in a v1 day's
    commit, nor a `v1-days/` path in a batch's.
30. A remote on the wrong host with the right owner and name is exit 2
    printing both; a matching fetch URL whose `pushurl` names another host is
    exit 2 before any object is written; two push URLs that do not all match
    are exit 2 for ambiguity; an `insteadOf` rewrite is honoured because
    `get-url` reports the effective destination; a URL carrying
    `user:password@` is refused with `<redacted>` and the password absent from
    every stream; `--repo` missing is exit 2; an unknown URL shape is exit 2;
    a bare fixture remote matches only `local:<path>`. The race witness: two
    publishers on one clone, the second exit 2 naming the lock holder with no
    flag able to choose a different lock; then two publishers on two clones of
    one remote, concurrent, both contributions surviving.
31. Two runs of one contribution get different temporary suffixes and the
    second is not refused by the first's leftovers; a run killed after
    `read-tree` leaves its directory and the next plain `publish` succeeds; on
    ordinary exit nothing of this run's remains and an unrelated directory
    under the git directory is untouched; a pre-existing staged path is exit 2
    with the file byte-identical; every alias case is exit 2 naming both
    flags, including each reached through a **symlink**; and a fold that
    rewrites the day file between the lock and the push is exit 1
    `reason=changed`.

## The work list

To build it in Go under `cmd/nova-tokens`, the way `cmd/nova-bus` is built:
standard library only, no hardcoded paths, no default paths, the exit grammar
above, `internal/oneline` for every printed value, `internal/bounded` for every
listing, and `ONBOARDING.md`'s first-day standard: a usage banner ending in a
runnable `example:` block, refusals that say what the flag wants and report
every independent problem at once, a `### First run` in `docs/CLI.md`, a
`quickstart` verb or the sentence saying why there is none, and tests that
pin all three by executing them.

1. **`internal/tokens/dayfile.go`**: the day file: the version and stamp
   line with `turns=`, the eleven columns, strict parse (a row with ten columns is an
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
   `supersedes=` predecessor set (sorted, no duplicates) and its chain with
   whole-note validation, the tips of a lane-day, the replacement snapshot,
   cycle, cross-lane,
   cross-day, duplicate, unsorted and missing-target refusals, and `TOKENS CONFLICT`,
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
    `report`'s stdout as the artifact, `--note` written only on OK, its OK
    line on stderr, `--supersedes`, `version` from the build.
12. **`cmd/nova-tokens/*_test.go`**: the contract tests: every exit code,
    every refusal sentence, the environment ignored (demanded test 1), the
    largest plausible state measured (demanded test 11), the audit over
    every printed argument (`internal/oneline/audit`), and no test reaching
    outside `t.TempDir()` or the fake `sqlite3`.
    **Amendment, 2026-09-12 (item 15, rules 22–31).** `publish`'s tests add a
    real `git` on `PATH` and a fake one, and a bare repository and its clone,
    all of them inside `t.TempDir()`; nothing reaches outside it, no test
    touches a real remote, a credential or the private ledger, and no test
    opens a network socket.
13. **`docs/CLI.md`'s `### First run`**: fold one fixture transcript and one
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
15. **`internal/tokens/publish.go` and the `publish` verb** (2026-09-12,
    rules 22–31): the contribution read once and digested under the lock, the
    two typed kinds and their destinations, the effective fetch and push
    destination check, the clone's cleanliness check, the raw-blob plumbing
    commit on the fetched head and the push by object id, the recovery state
    machine (absent, present, conflict, incomplete, rejected, lost) bound to
    the destination paths, the per-run temporary directory released on exit,
    the owned staged name and the resolved-path alias check, the public subset
    selector with its pre-write check, and the `git` subprocess under
    `--git-timeout` inside `--deadline` — the only place in this repository
    that names `git`. Tests: demanded tests 22–31,
    argv asserted for equality against a fake `git` on `PATH` and behaviour
    against real bare repositories in `t.TempDir()`; no network, no real
    remote, no credential, no private ledger touched by any test.
    **Its blocking dependency, named rather than worked around** (rule 29):
    the `nova.tokens.coverage/2` and `nova.tokens.mapping/2` validators and
    their encoders, owned by the records lane in `internal/records` beside
    #146's construction API. The batch path cannot be called done without
    them and must not be re-typed to avoid them; the `--v1-day` path has no
    such dependency and can land first, in its own subtree, so no retained
    work is discarded while they are built.

## Ideas folded on 2026-09-11

| source | the idea, in six words | disposition |
|---|---|---|
| Stella, spec repairs | `supersedes=<note-id>` on the subject trailer | rule 6 and the bus source: explicit supersession; `git log` and `--git-timeout` removed; rule 19 back to one subprocess |
| Stella, spec repairs | competing roots or successors are CONFLICT | `TOKENS CONFLICT`, exit 1, no winner inferred, day file untouched |
| Stella, spec repairs | validate the whole correction; reject cycles | the bus source; test 6 |
| Stella, spec repairs | INDEX is a lookup aid only | already; now also the git history is not consulted |
| Stella, spec repairs | `report` refuses non-UTC aggregates | left different: `report` carries the zone as the seventh field and six fields never erase it (rule 20, test 20); `--note` is now written only on OK, so a refused report cannot replace a file |
| Stella, spec repairs | totals are sums of reported measurements | **what it deliberately does not do**: it does not claim coverage |
| Rowan, idea 10; Emma, T×C | the turn count beside the tokens | rule 12: `turns=` on the version line, `TOKENS DAY`, `SUM`; labelled by its scope, `sources=` beside it, never called task turns (Stella's closing read) |
| Stella, closing read | supersede a set; a snapshot joins all tips | rule 6 and the bus source: `supersedes=<id>[,<id>…]` sorted, `report --supersedes` repeatable, the replacement snapshot names every tip and becomes the single tip (test 6, two roots and two successors) |
| Stella, idea 6 | measure spend with types and coverage preserved | already, rules 15 and 21 (`-` is never zero; `unattributed` is never split) |
| Emma, B–D; Johnny, ideas 1–5 | fewer turns per decision | not folded here: the wake and merge specs own the turns; this spec counts them |
| DeepSeek, idea 6 | coordination as an accounted resource | already: this tool is the account; `turns=` adds the unit |
| ideas #275 | 967,000 tokens to do nothing | `turns=` beside tokens is the number that shows it; already per day (rule 12) |
| ideas #273 | evidence arriving after the belief | already, rule 10 (`SHRANK`) and rule 12 (the fold's own stamp on every file) |
| ideas #357 | a tokens note is data, never instruction | already, the data paragraph at the top |
| nova-tools #35 | notes keyed by the clock race | the bus source: two notes for one day are a conflict, never a clock decision |
