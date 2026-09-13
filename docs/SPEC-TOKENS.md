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
| a builder's, a reader's and a repair round's tokens sat on three rows and nothing said they were one task (Stella, **2026-09-13**); nova-work's `:attempt` carried a `:usage` pointer nothing defined | a **usage receipt** per session, joined to a work node and a stage, and `cost --node` summing every stage (rules 32 to 34, **2026-09-13**) |
| finding a repeat read in a long pass was "heavy transcript digging" (Emma, **2026-09-13**); 2.2M tokens went on the same four gaps by hand with no record (**2026-09-11**) | `hot`, `diff`, and a **friction** record summed per gap (rules 35 to 37, **2026-09-13**); pricing from the caller's dated rate table, never compiled in (rule 38) |

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
   **Amendment, 2026-09-13 (rules 32, 37).** Two more files THIS RUN makes,
   added to the carve-out list by shape with their reason: the receipts
   file's `<out>/receipts/<day>.tsv.tmp`, written whole and renamed under
   `fold.lock` exactly as a day file is, and the frictions file's
   `<file>.tmp` beside it, under `<file>.lock`. Neither verb removes a file
   it did not make, and neither edits a row: a receipt and a friction are
   appended by rewriting the whole file with the old rows byte-identical.
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
    **Amendment, 2026-09-13 (rule 34), draft 2.** `check` also walks
    `<out>/receipts/`, the one subdirectory this spec names under `--out`:
    every receipts file parses with its seventeen columns, one session has one
    receipt id, the rows of one receipt id carry one `node`, one `stage`, one
    `who` and one `session`, a receipt's cells never exceed its day file's,
    and a receipts file for a day the fold has already passed and did not
    write is named. Each is exit 1. A receipts file for a day **newer than the
    newest day file, or any receipts file at all when there is no day file**
    (draft 3), is the ordinary order, not a finding — a receipt is written when
    a session ends and the fold runs that night — and is one `CHECK NOTE`,
    exit 0 (draft 2). `CHECK OK` and `CHECK FAIL`
    carry `receipts= dup= split= exceeds= orphan=`.
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
nova-tokens sum     --out <dir> --month <YYYY-MM> [--rates <file>] [--max <n>]
nova-tokens check   --out <dir> [--max <n>]
nova-tokens sources --repos <file> (--day <YYYY-MM-DD> | --all)
                    [--claude <label>=<dir>]... [--opencode <label>=<file>]... [--swarm <label>=<dir>]... [--bus <dir>]
                    [--provider <label>=<file>]...
                    [--scratch <dir>] [--timeout <seconds>] [--max <n>]
nova-tokens publish (--batch <dir> | --v1-day <dir> --day <YYYY-MM-DD> --seat <label> | --frictions <file> --seat <label>)
                    --ledger <dir> --remote <name> --branch <name> --repo <host>/<owner>/<name>
                    [--supersede]
                    [--public <dir> --public-repos <file>] [--public-sources]
                    [--deadline <seconds>] [--git-timeout <seconds>] [--attempts <n>] [--max <n>]
nova-tokens receipt   --out <dir> --repos <file> --node <id> --stage <preparation|implementation|review|correction|coordination|validation> --who <name>
                      (--claude <label>=<dir> --session <id> | --opencode <label>=<file> --scratch <dir> --session <id> | --swarm <label>=<pool> --job <id>)
                      [--resume] [--timeout <seconds>] [--max <n>]
nova-tokens cost      --out <dir> --node <id>... [--month <YYYY-MM>] [--rates <file>] [--max <n>]
nova-tokens hot       (--claude <label>=<dir> --session <id> | --opencode <label>=<file> --scratch <dir> --session <id> | --swarm <label>=<pool> --job <id>)
                      [--top <n>] [--timeout <seconds>]
nova-tokens diff      (--out <dir> --day <d1> --day <d2> | <one source flag> (--session <a> --session <b> | --job <a> --job <b>)) [--max <n>]
nova-tokens friction  add --frictions <file> --as <name> --gap <label> --tool <name|-> --attempted <text>
                      (--receipt <id> --out <dir> [--wall <duration>] | --tokens ~<n> --wall <duration>) [--issue <host>/<owner>/<repo>#<n>]
nova-tokens frictions --frictions <file> [--rates <file> --out <dir>] [--since <YYYY-MM-DD>] [--max <n>]
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
or a receipt in flight (either holds `fold.lock`, draft 2), a malformed batch
directory, or a batch whose schema has no validator here. Deliberately does not check: whether the numbers are right
(`check` and `sum` read them), whether other days or other benches are
present in the ledger, whether coverage of the whole ledger is complete, or
whether the note for that day reached the bus (rule 28). `publish` is a
**wall**, and it is the only verb that runs `git`, the only verb that writes
into a git clone, and it is in **Publishing to the git ledger**, rules 22 to
31.

**Amendment, 2026-09-13 (rules 32–38), draft 2.** Six verbs and one flag join.
Not one of them adds a default path or reads an environment variable; `--top`
on `hot` defaults to 5 and is the one new default, allowed for the reason
`--max` has 20: it is a listing's ceiling, not a fact about anybody's data —
and **`--top 0` means all**, as `--max 0` does everywhere in this family,
because a ceiling a caller cannot lift is a tool deciding what its user may
see (SPEC.md Conventions; draft 2). `--rates` is optional on `sum`, `cost` and
`frictions` and its absence prints `usd=-`, never a price; on `frictions` it
**requires `--out <dir>`** and is refused without it, because a friction row
carries a token sum and no model and its dollars are its receipt's (rule 37,
draft 2), exactly as `--scratch` is required with `--opencode` and refused
otherwise. Neither `hot` nor `diff --session` takes `--repos`: a session
listing names no repo, and a flag that decides nothing is a flag that does
nothing (draft 2). `--frictions` on `publish` is a third typed contribution
kind, refused with `--batch`, `--v1-day`, `--day` or `--public`; it is a
**second door** through a destination's bytes and its own **append**
transition is written into rules 22, 23, 24 and 26 rather than claimed to
leave them unchanged (draft 2).

### `receipt`

Asserts: one **span** of one session, read whole from one declared source,
folded by the same
reader `fold` uses, joined to one node and one stage, written as one receipt
under one drawn id. Says NO (exit 1,
`USAGE FAIL reason=<nosession|receipted|unreadable|differs|nonew>`)
and writes **nothing** when the session is not in the source, is already
receipted whole, has a file the tool cannot read, holds a stored row this run
cannot reproduce, or has nothing after the resume cursor — a receipt over half
a session
is a wrong number about one task, where a fold over half a day is a day with
its gaps named. **A session is not sealed by its first receipt** (draft 3):
`--resume` receipts the turns after the last one (rule 39), and a plain run
over a receipt whose multi-day write was interrupted completes it under the
stored id (rule 41). Deliberately does not check: that the node exists in any work
set (the id is data here), or that the session's tokens are on the day file
yet (`check` does, rule 34). `receipt` is a **wall**.

### `cost`

Asserts nothing. Reads `<out>/receipts/*.tsv`, selects the nodes named, prints
per stage, per model, per receipt and the total, with list-rate dollars and the
per-million mean when `--rates` is given and their denominator always. Exits 0
whenever it ran; a node with no receipt is `receipts=0` and a NOTE, unmeasured
and never zero. `cost` is a **report**. Never gate on it.

### `hot`

Asserts nothing. Reads one session through the source's reader, ranks its
repeated reads and its largest step outputs, prints both capped at `--top`
per kind, the session's token counts beside the step bytes, and exactly one
`HOT NOTE` paragraph that ends with what the verb cannot see. Exits 0 whenever
it saw the session, 1 (`HOT FAIL reason=<nosession|reclaimed|unreadable>`)
when it could not. `hot` is a **report**.

### `diff`

Asserts nothing. Two day files, or two sessions of one source: what moved,
largest first, capped, a dash on either side a dash and an absent row absent.
Exits 0 whenever both sides could be read; a missing side is exit 2 naming it.
`diff` is a **report**.

### `friction add` and `frictions`

`friction add` appends one measured or one rough row to the caller's frictions
file, whole-file write through `.tmp` and rename under `<file>.lock`; a file
whose existing rows do not parse is exit 2 and untouched. `frictions` sums the
file per gap, tokens descending, and is a **report**, exit 0; with `--rates`
it also requires `--out <dir>`, because the dollars it prints are the named
receipts' and nothing on the row itself carries a model (draft 2). The
`publish --frictions` kind is rule 37's and `publish`'s section governs it.

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
`state=appended`, `state=superseded`) or already there — byte-identical for a
batch's immutable paths, identical under rule 26's `rows_sha256` for a v1 day
or a frictions file (`state=already-published`). **`state=appended` is in that
list and in `PUBLISH OK`'s grammar** (draft 3): rule 26's draft-2 clause,
rule 24's second door and rule 37's demanded test all print it and exit 0, and
the two enumerations here and at `PUBLISH OK` still named three states, so the
state the append transition produces could not be printed by a tool that obeyed
its own grammar. **Exit 1**, the verb ran and said NO:
`reason=differs` (a v1 day's path holds different rows and `--supersede` was
not given), `reason=conflict` (a content-addressed path holds bytes other
than its own digest's), `reason=incomplete` (a published coverage envelope
whose referenced closure is missing a member or holds different bytes),
`reason=malformed` (a v1 day file `check` would name, or a batch whose
envelopes do not validate), `reason=changed` (an input's digest moved under
the publication), `reason=subset` (a public subset would carry a row not on
its allowlist), `reason=unconfirmed` (the push could not be confirmed inside
`--attempts` and `--deadline`). **Exit 2**, could not run: a missing or bad
flag, `--repo` missing, **more than one contribution kind, or none**
(draft 3: there have been three kinds since rule 37, and *both … or neither*
could not say that `--batch` with `--frictions` is refused), `--supersede` with
`--batch`, a source flag on `publish`, two given paths that resolve to one
directory, a staged path or a temporary directory that already exists,
`--ledger` that is not a git checkout, an effective fetch or push URL that
does not match `--repo` or whose shape the parser does not know, more than one
effective push URL where they do not all match, an empty `user.name` or
`user.email` in the clone, unrelated dirty or staged work in the clone, a
second publisher holding the lock, a `git` absent from `PATH`, a fold or a
receipt in flight under `--v1-day` (either holds `fold.lock`, draft 2), a
batch that is not exactly its own named files, and
a batch naming a schema this build has no validator for.

**Amendment, 2026-09-13 (rules 32–38), draft 2.** The three meanings hold and
the enumerations gain these cases. **Exit 0**: a receipt written, **resumed
or completed** (`state=written|resumed|completed`, rules 39 and 41, draft 3);
a `cost`,
`hot`, `diff` or `frictions` that ran; a `friction add` that landed; a
`publish --frictions` whose file is on the branch, including the append
transition; a `check` with no receipts finding, and a `check` whose only
receipts observation is the `CHECK NOTE` for days the fold has not reached
yet, or for every receipts file where there is no day file at all (rule 34,
drafts 2 and 3). **Exit 1**: `USAGE FAIL`
(`reason=nosession`, `receipted`, `unreadable`, and — draft 3 — `differs` for
a stored receipt row this run cannot reproduce and `nonew` for a `--resume`
with nothing after the cursor: nothing written in any of them); `HOT FAIL`
(`reason=nosession`, `reclaimed`, `unreadable`); a `check` finding under
`receipts/` (`DUPLICATE`, `SPLIT`, `EXCEEDS`, `ORPHAN`, a malformed row, a
stray); a
`publish --frictions` whose destination is not a prefix of the file and
`--supersede` was not given (`reason=differs`). **Exit 2**: `--stage` outside
the six names; `--node` empty; a **negative** `--top` (`--top 0` is all,
draft 2); `--tokens` without the `~`;
`--tokens ~<n>` without `--wall`; a `--gap` that is not `[a-z0-9-]{1,32}`; a
malformed or duplicate line in `--rates` or an existing frictions file;
`--rates` on `frictions` without `--out` (draft 2); `--resume` over a session
with no receipt to resume from (draft 3);
`--rates`, `--receipt`, `--resume` or `--out` where the verb does not take it;
a `diff`
whose one side is missing; two `--session` values that are equal; `--frictions`
with `--batch`, `--v1-day`, `--day` or `--public`.

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
TOKENS DAY date=<d> rows=<n> models=<n> repos=<n> turns=<n|-> unknown=<pct>% other=<pct>% rough=<n> dashes=<n> nonutc=<n> receipts=<n> sources=<labels> written=<true|false>
TOKENS SHRANK date=<d> type=<type> file=<n> now=<n|-> written=<true|false>: a source went quiet; --allow-shrink writes it anyway
TOKENS MORE kind=<source|unreadable|unparsed|superseded|conflict|touched|mixed|day> shown=<n> total=<t> <remedy>
TOKENS OK days=<n> rows=<n> sources=<n> unreadable=<n> unparsed=<n> mixed=<n> conflict=<n> shrank=<n>
TOKENS FAIL days=<n> rows=<n> sources=<n> unreadable=<n> unparsed=<n> mixed=<n> conflict=<n> shrank=<n>
TOKENS NOTE <the one remedy line>
TOKENS REFUSED: <reason>
REPORT OK who=<name> day=<d> rows=<n> at=<stamp> build=<id> subject=<subject>
REPORT FAIL who=<name> day=<d> rows=<n> unreadable=<n>
REPORT REFUSED: <reason>
SUM MONTH month=<m> at=<stamp> build=<id> days=<n> first=<d> last=<d> missing=<n> rows=<n> turns=<n|-> rates=<file|-> verified=<date|->
SUM PAIR model=<model> repo=<repo> input=<n> output=<n> cache_write=<n> cache_read=<n> reasoning=<n> rough=<n> dashes=<in>,<out>,<cw>,<cr>,<r> nonutc=<n> days=<n> usd=<n|-> per_mtok=<n|-> priced_tokens=<n> unpriced_tokens=<n>
SUM MODEL model=<model> input=<n> output=<n> cache_write=<n> cache_read=<n> reasoning=<n> rough=<n> dashes=<in>,<out>,<cw>,<cr>,<r> nonutc=<n> repos=<n> usd=<n|-> per_mtok=<n|-> priced_tokens=<n> unpriced_tokens=<n>
SUM TOTAL input=<n> output=<n> cache_write=<n> cache_read=<n> reasoning=<n> rough=<n> dashes=<in>,<out>,<cw>,<cr>,<r> nonutc=<n> turns=<n|-> pairs=<n> models=<n> usd=<n|-> per_mtok=<n|-> priced_tokens=<n> unpriced_tokens=<n>
SUM MORE kind=<pair|model> shown=<n> total=<t> nova-tokens sum --out <dir> --month <m> --max 0
SUM OK month=<m> days=<n> missing=<n> pairs=<n> models=<n> nonutc=<n>
SUM REFUSED: <reason>
CHECK FAIL <path>: <reason>
CHECK FAIL <path>:<line>: <reason>
CHECK MISSING date=<d>
CHECK STRAY <path>
CHECK MORE kind=<file|row|missing|stray|duplicate|split|exceeds|orphan> shown=<n> total=<t> nova-tokens check --out <dir> --max 0
CHECK NOTE <the one remedy line>
CHECK OK at=<stamp> build=<id> files=<n> rows=<n> first=<d> last=<d> missing=0 stray=0 receipts=<n> dup=0 split=0 exceeds=0 orphan=0
CHECK FAIL files=<n> rows=<n> first=<d> last=<d> bad=<n> missing=<n> stray=<n> receipts=<n> dup=<n> split=<n> exceeds=<n> orphan=<n>
CHECK REFUSED: <reason>
SOURCES SOURCE label=<label> kind=<claude|opencode|swarm|bus|provider> path=<path> reports=<types> day_basis=<utc|mixed|<zone>> files=<n> unreadable=<n> messages=<n> dup=<n> noid=<n> nousage=<n> unparsed=<n> comments=<n> redated=<n> superseded=<n> rows=<n>
SOURCES UNREADABLE label=<label> path=<path>: <why>
SOURCES UNPARSED label=<kind>:<name> note=<id> line=<n>: <text>
SOURCES MORE kind=<source|unreadable|unparsed> shown=<n> total=<t> nova-tokens sources … --max 0
SOURCES OK sources=<n> files=<n> messages=<n> unreadable=<n> unparsed=<n> rows=<n>
PUBLISH PLAN at=<stamp> build=<id> kind=<batch|v1-day|frictions> ledger=<dir> repo=<host>/<owner>/<name> remote=<name> branch=<name> seat=<label|-> day=<d|-> contribution=<hex> files=<n> bytes=<n> deadline=<seconds> public=<dir|->
PUBLISH DIRTY path=<path>: <modified|staged|untracked>
PUBLISH FILE path=<path> sha256=<hex> rows_sha256=<hex|-> state=<new|present|identical|appended|superseded|conflict|missing> supersedes=<hex|->
PUBLISH SUBSET path=<path> rows=<n> excluded=<n> repos=<list> sha256=<hex>
PUBLISH EXCLUDED day=<d> model=<model> repo=<repo>: not on --public-repos
PUBLISH MORE kind=<dirty|file|excluded> shown=<n> total=<t> <remedy>
PUBLISH OK contribution=<hex> commit=<sha> pushed=<true|false> state=<published|already-published|appended|superseded> attempts=<n> files=<n> at=<stamp> build=<id>
PUBLISH FAIL contribution=<hex> reason=<differs|conflict|incomplete|malformed|changed|subset|unconfirmed> pushed=<true|false|->
PUBLISH NOTE <the one remedy line>
PUBLISH REFUSED: <reason>
USAGE OK receipt=<id> pointer=note:usage:<id> node=<id> stage=<stage> session=<label>:<id> state=<written|resumed|completed> days=<n> rows=<n> input=<n|-> output=<n|-> cache_write=<n|-> cache_read=<n|-> reasoning=<n|-> started=<stamp> ended=<stamp> at=<stamp> build=<id>
USAGE FAIL session=<label>:<id> reason=<nosession|receipted|unreadable|differs|nonew> [by=<id>] [day=<d>]
USAGE NOTE <the one remedy line>
USAGE REFUSED: <reason>
COST STAGE stage=<stage> receipts=<n> tokens=<n> dashes=<n> usd=<n|-> per_mtok=<n|->
COST MODEL model=<model> receipts=<n> tokens=<n> dashes=<n> usd=<n|-> per_mtok=<n|->
COST RECEIPT receipt=<id> node=<id> stage=<stage> session=<label>:<id> started=<stamp> ended=<stamp> wall=<d> tokens=<n> usd=<n|->
COST MORE kind=<model|receipt> shown=<n> total=<t> nova-tokens cost … --max 0
COST NOTE <the one remedy line>
COST OK nodes=<n> receipts=<n> stages=<list> tokens=<n> dashes=<n> usd=<n|-> per_mtok=<n|-> priced_tokens=<n> unpriced_tokens=<n> wall=<d> rates=<file|-> verified=<date|-> at=<stamp> build=<id>
COST REFUSED: <reason>
HOT REPEAT kind=<read|command> tool=<name> count=<n> bytes=<n|->: <path or command>
HOT LARGEST step=<n> tool=<name> bytes=<n>: <path or command>
HOT MORE kind=<repeat|largest> shown=<n> total=<t> nova-tokens hot … --top 0
HOT OK session=<label>:<id> messages=<n> steps=<n> reads=<n> repeated=<n> repeated_steps=<n> repeated_bytes=<n> result_bytes=<n> unranked=<n> largest_bytes=<n> input=<n|-> output=<n|-> cache_read=<n|-> started=<stamp> ended=<stamp> at=<stamp> build=<id>
HOT NOTE <one paragraph: the repeats, the largest step, and what this verb cannot see>
HOT FAIL session=<label>:<id> reason=<nosession|reclaimed|unreadable> steps=<n>
HOT REFUSED: <reason>
DIFF ROW model=<model> repo=<repo> type=<type> from=<n|-|absent> to=<n|-|absent> delta=<n|->
DIFF ROW field=<name> from=<n|-> to=<n|-> delta=<n|->
DIFF MORE kind=row shown=<n> total=<t> nova-tokens diff … --max 0
DIFF OK kind=<day|session> from=<d or id> to=<d or id> rows_from=<n> rows_to=<n> added=<n> removed=<n> changed=<n> tokens_from=<n> tokens_to=<n> delta=<n> dashes=<n> turns_from=<n|-> turns_to=<n|->
DIFF REFUSED: <reason>
FRICTION OK at=<stamp> who=<name> gap=<label> tool=<name|-> tokens=<n> rough=<0|1> wall=<d> receipt=<id|-> issue=<host/owner/repo#n|-> rows=<n> file=<path>
FRICTION REFUSED: <reason>
FRICTIONS GAP gap=<label> tool=<name|-> count=<n> tokens=<n> rough=<n> wall=<d> usd=<n|-> unpriced_rows=<n> issues=<n> latest=<stamp>
FRICTIONS MORE kind=gap shown=<n> total=<t> nova-tokens frictions … --max 0
FRICTIONS OK gaps=<n> rows=<n> tokens=<n> rough=<n> wall=<d> usd=<n|-> issues=<n> since=<date|-> rates=<file|-> verified=<date|-> unpriced_rows=<n> at=<stamp> build=<id>
FRICTIONS REFUSED: <reason>
CHECK DUPLICATE session=<label>:<id> receipts=<id,id>
CHECK SPLIT receipt=<id> field=<node|stage|who|session> values=<a,b>
CHECK EXCEEDS date=<d> model=<model> repo=<repo> type=<type> receipts=<n> day=<n>: nova-tokens fold --out <dir> --day <d>
CHECK ORPHAN date=<d>
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

**Amendment, 2026-09-13 (rules 32–38), draft 2.** The new lines keep the rule:
`OK`, `STAGE`, `MODEL`, `RECEIPT`, `REPEAT`, `LARGEST`, `ROW`, `GAP`, `NOTE`
and `MORE` go to stdout; `FAIL`, `REFUSED`, `DUPLICATE`, `SPLIT`, `EXCEEDS`
and `ORPHAN` go to stderr. **The `receipt` verb's event token is `USAGE`, not
`RECEIPT`** (draft 2): `RECEIPT` is nova-bus's own verb token and its
informational token both (SPEC.md, the bus grammar,
`RECEIPT OK recorded=<n> …`), and two binaries sharing a first token means one
pasted line is evidence about two of them and one anchored scan matches the
wrong tool — the adoption matrix's collision, and a reader's. A verb's token
is not required to be its name here: `fold` prints `TOKENS`.
`TOKENS DAY` gains `receipts=<n>`; `SUM PAIR`, `SUM MODEL` and
`SUM TOTAL` gain `usd=<n|-> per_mtok=<n|-> priced_tokens=<n>
unpriced_tokens=<n>`, and `SUM MONTH` gains `rates=<file|-> verified=<date|->`,
all `-` without `--rates`; `CHECK OK` and `CHECK FAIL` gain `receipts=<n>
dup=<n> split=<n> exceeds=<n> orphan=<n>`. A `HOT NOTE` is the one line in this grammar
that is a paragraph, and it is still one line, capped at `oneline.TailBytes`
before the escape, because Emma asked for a paragraph and the Conventions ask
for a line, and a line long enough to be read as prose is both.

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

**Amendment, 2026-09-13 (rule 35), draft 2: `hot` and `diff --session` need
one more line kind from this reader, and that is a demanded change, not a
parenthesis.** The reader above keeps a line only when it parses as JSON with
a `message` object holding a `usage` object and an `id`, and it takes `paths`
from `tool_use` inputs; a user turn carrying a `tool_result` block has
neither, so the ratified reader discards exactly the lines `hot` must pair
with a `tool_use` to know a step's result bytes. The reader therefore **also**
surfaces, for `hot` and `diff --session` alone, each user turn's `tool_result`
blocks with their `tool_use_id` and the byte length of the content the record
holds. **This changes no count** (rules 4 and 15): a `tool_result` line
carries no `usage` object, so it is not a message, it is not deduplicated by
`id`, it feeds no `(day, model, repo)` row and no `messages=` count, and
`fold`, `sum`, `report` and `receipt` see exactly what they saw before. It was
stated in the first draft only inside a work-list item; a change to a ratified
reader belongs in the reader's own section, with its test.
**Demanded test.** A fixture transcript folded before and after the reader
change yields byte-identical day files and identical `TOKENS SOURCE` counts
(`files=`, `messages=`, `dup=`, `noid=`, `nousage=`, `unparsed=`, `rows=`);
`hot` over the same fixture pairs every `tool_use` with its `tool_result` by
`tool_use_id`; a `tool_result` the record carries by reference is `bytes=-`,
ranks nowhere, and is counted in `HOT OK`'s `unranked=`.

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
| `receipt` (2026-09-13, rules 32, 39–41, draft 3) | 1 OK or 1 FAIL + up to 20 UNREADABLE + 1 MORE + 1 NOTE = 23 | under 4 KB |
| `cost` (2026-09-13, rule 33) | 6 STAGE + 20 MODEL + 1 MORE + 20 RECEIPT + 1 MORE + 1 OK + 1 NOTE = 50, at a node with 200 receipts on 25 models | under 12 KB |
| `hot` (2026-09-13, rule 35) | 5 REPEAT + 1 MORE + 5 LARGEST + 1 MORE + 1 OK + 1 NOTE = 14 at `--top 5`, over a session of 5,000 steps and 200 MB of results; at `--top 0` the listing is the caller's, as `--max 0` is everywhere (draft 2) | under 4 KB; the NOTE under `oneline.TailBytes` |
| `diff` (2026-09-13, rule 36) | 20 ROW + 1 MORE + 1 OK = 22, over two day files of 200 rows | under 4 KB |
| `frictions` (2026-09-13, rule 37) | 20 GAP + 1 MORE + 1 OK = 22, over a file of 10,000 rows on 500 gaps | under 4 KB |
| `check` with receipts (2026-09-13, rule 34, draft 2) | the `check` row above + 20 DUPLICATE + 1 MORE + 20 SPLIT + 1 MORE + 20 EXCEEDS + 1 MORE + 20 ORPHAN + 1 MORE + 1 NOTE = 149 | under 20 KB |

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

**A contribution is one of three typed kinds, and they never mix.** The
retained batch is the endpoint; the aggregate exists so a bench with day files
today is not blocked, and neither it nor the public subset may delay the
endpoint (Stella, 2026-09-12). **The third kind, the frictions file, joined on
2026-09-13 with rule 37, and its row below is draft 2**: it is the one kind
with an **append** transition, and it is a second door through which a
destination's bytes change — named here, and in rules 22, 23, 24 and 26,
rather than added under a rule that says there is only one door.

| kind | flag | what it is | where it lands | destination is |
|---|---|---|---|---|
| **retained batch** — the endpoint | `--batch <dir>` | one `nova.tokens.coverage/2` envelope, the `observation/2` shards it references and any new `mapping/2` envelopes | `records/…`, `mappings/…`, `coverage/…`, the retained-format packet's paths (rule 29) | **content-addressed and immutable**: a path's name is its bytes' digest, so a path is written once and never replaced |
| **v1 day file** — aggregate transport, explicitly typed | `--v1-day <dir> --day <d> --seat <label>` | one `<dir>/<day>.tsv` this bench folded | `v1-days/<seat>/<YYYY-MM>/<day>.tsv`, a subtree no records path uses | **mutable and named by the day**, so it has the one **replace** transition this spec allows, under `--supersede` (rule 26); the frictions row below adds an **append** transition, which replaces nothing and needs no key (draft 2) |
| **frictions file** — a bench's friction rows (rule 37, draft 2) | `--frictions <file> --seat <label>` | one `nova-tokens frictions v1` file this bench appends to | `frictions/<seat>.tsv`, a subtree neither `records/` nor `v1-days/` uses | **mutable and named by the seat**, with the **append** transition of rule 26's draft-2 clause: a destination whose rows are a prefix of this file's publishes with no key; anything else is `reason=differs` until `--supersede` |

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
    on a timer.** **Amendment, 2026-09-13 (rule 37), draft 2: `--frictions`
    is the third kind and `--seat` is shared.** `--batch`, `--v1-day` and
    `--frictions` are mutually exclusive and one is
    required; `--day` belongs to `--v1-day` alone and is refused
    with `--batch`, because a retained record carries its own day and its own
    origin (rule 29), and with `--frictions`, because a frictions file carries
    many days and names none. **`--seat <label>` belongs to `--v1-day` and to
    `--frictions`, is required with each, and is refused with `--batch`**:
    both land in a subtree named by the seat, and the seat is what keeps one
    bench's mutable file out of another's, while a retained record's origin is
    inside the record. There is no `--all` and no "today": a clock never
    chooses what is uploaded, for the reason rule 12 gives. The verb reads the
    contribution's files and writes nothing under `--out`, `--v1-day`,
    `--batch` or `--frictions` (draft 3: the fourth was missing from a list
    rule 37 added a kind to). It schedules nothing and wakes nothing; a LaunchAgent that runs
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
    `supersedes=<rows_sha256>` on a `--supersede` run (rule 26). **Amendment,
    2026-09-13 (rule 37), draft 2:** for a **frictions file** the body carries
    `kind=frictions`, `seat=`, `rows_sha256=`, the file's own `at=` and
    `build=`, and exactly one of `appends=<hex|->` — the destination's
    `rows_sha256` before this publish, `-` when the path was absent — or
    `supersedes=<rows_sha256>` on a `--supersede` run, so the message says
    which door was used and what it was appended to. Nothing else.
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
      batch, the day file's path for a v1 day, `frictions/<seat>.tsv` for a
      frictions file (draft 3, which the first draft of this list left out of
      the entry it added a case to):
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
         `state=superseded`. This is the only door through which a **v1 day's**
         bytes are replaced (draft 3, narrowing a sentence that said *the only
         door in this machine* and was made false four paragraphs later by the
         frictions path's own `--supersede` replace), and `--supersede` is the
         only key to either mutable path (rule 26). **Amendment, 2026-09-13 (rule 37), draft 2:
         a frictions file is the second door, and going through it replaces
         nothing.** For a `--frictions` contribution the comparison is rule
         26's draft-2 clause: a destination whose rows below its version line
         are a **prefix** of this file's rows is `state=appended`, exit 0, one
         commit, no `--supersede` and not one byte of the destination
         discarded; equal rows are `state=identical` and
         `state=already-published`; rows that are neither — one dropped,
         reordered or edited — are `PUBLISH FAIL reason=differs`, exit 1,
         until `--supersede`, which replaces the path and prints
         `state=superseded`. Appending is not replacing, which is why it needs
         no key; what it must never do without one is lose a row.
      3. **Conflicting bytes at an immutable path** — a `records/`,
         `mappings/` or `coverage/` path present with bytes other than the
         digest that names it demands — is `PUBLISH FAIL reason=conflict`,
         exit 1, naming the path: a content-addressed path is written once,
         and this tool never replaces one. Only the **two** mutable paths — a
         v1 day's and a frictions file's — have a replace transition, and only
         under `--supersede` (rule 26; draft 3, where this sentence still said
         *only the v1 day's* after rule 24.2 and rule 26 had given
         `frictions/<seat>.tsv` the same replace transition under the same
         flag).
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
    - **A fold or a receipt that may be in flight is exit 2** (draft 3, the
      wording the exit table has carried since draft 2). `<dir>/<day>.tsv.tmp`
      present under `--v1-day`, or that directory's `fold.lock` held by either
      verb, is named
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
    decision nobody has made, and with `--frictions` (draft 3, the refusal the
    exit table and rule 37 already carry and this sentence did not). `--public-repos <file>` is required with it: a
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

26. **Identity, the one replace transition, and — draft 2 — the one append
    transition.** A contribution's **id** is
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
    - **a frictions file** (draft 3): `rows_sha256`, computed exactly as a v1
      day's, over every line below its own version line. It is written here,
      in the bullets that define an identity per kind, and not only in the
      prose of the append transition below, because a list of two kinds under
      a rule that governs three is a list a reader will trust.
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

    **The frictions append transition (amendment, 2026-09-13, rule 37, draft
    2).** A `frictions/<seat>.tsv` path is mutable and named by the seat, and
    its identity is `rows_sha256`, the digest of every line below the version
    line, exactly as a v1 day's. Its **four** transitions are: **absent** →
    publish, `state=new`; **present with the same `rows_sha256`** →
    `state=identical`, `PUBLISH OK … pushed=false state=already-published
    attempts=0`, exit 0, no commit and no push; **present with rows that are a
    proper prefix of this file's rows** → `state=appended`, exit 0, one commit
    whose message carries `appends=<the destination's rows_sha256>` (rule 23),
    **without `--supersede`**, the destination's rows preserved byte for byte
    beneath the new ones; **present with rows that are neither** →
    `PUBLISH FAIL reason=differs`, exit 1, until `--supersede`, which replaces
    that one path and prints `state=superseded`. The prefix test is a byte
    comparison of the destination's rows against this file's leading rows, so
    an append can only add at the end: a friction row is never edited (rule
    9), and a file that dropped or changed one is exactly what
    `reason=differs` is for. On **absent**, **identical** and **appended**,
    `--supersede` changes nothing at all, as it does for a v1 day — it is
    permission to replace, never an instruction to write — and it remains
    refused with `--batch`.
    **Demanded test.** Publish a frictions file; `friction add` one row;
    publish again: `state=appended`, exit 0, no `--supersede`, the commit
    message carrying `appends=` equal to the first upload's `rows_sha256`, and
    the ledger's leading rows byte-identical to the first upload's. Publish
    unchanged: `state=already-published`, no commit, no push. Publish a file
    with its second row deleted: `reason=differs`, exit 1, nothing written,
    and `--supersede` lands one commit carrying `supersedes=`. **Red first**
    against a rule that called every changed frictions file `differs`, and
    against one that let a non-prefix through as an append.
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

## Attempts joined to work, hot spots, frictions, adoption (2026-09-13)

**The hurts, dated.** Glenn, 2026-09-11: "We have an obligation to report
this" — token spend per day, model and repo, the key exactly
`(day, model, repo)`, a model the unit of spend. Glenn, 2026-09-12: dollar
efficiency from cheap models is an explicit goal, and the metric is *as good
and cheaper, checking included*: "Are we spending more tokens checking the
work, than if Opus just did the work, is the metric." Glenn, 2026-09-12:
quality first, then tokens and cost, then wall clock. Glenn via Stella,
2026-09-13 16:34Z (note stella-ba384f9132f5, Glenn's own words as she carried
them, draft 2): "Remember the training about reducing average cost per-token,
and reducing total tokens used as goals" — and hers in the same note, on what
the two objectives cover: "worker tokens PLUS planning/context, coordinator
review, retries, corrections and rework." Stella, 2026-09-13 15:30Z (note
stella-4b9200ddc994, draft 2), on what the next tools must measure:
"retained token/cost accounting joined to executionattempts (count review and
repair, not merely builder tokens)" — spelled as the stored note has it
(draft 3). The missing space is a **storage artifact** of that note, seen
throughout it, and not Stella's word; draft 2 quoted it repaired, which is a
quotation of something nothing holds. Emma, 2026-09-13 (bus note emma-0fd8c03d5f24), verbatim: "Locating
what caused token burns or repeat reads during long multi-step passes required
heavy transcript digging. Verb: `nova-tokens hot --session <id> --top 5` —
highlights the top repeated reads and largest step outputs in one
human-readable paragraph." Rowan, 2026-09-11: 20 children and 2.2M tokens went
mostly on fixing the same four tool gaps by hand — a friction with a cost and
no record. (The record carries two counts of that day: 20 children and 2.2M
tokens in the crystallize note, 21 and 2.4M in the window-tokens note. Neither
is load-bearing here; this spec cites the first and says the second exists,
draft 2.) Glenn, 2026-09-12: "a tool is only good if everybody adopts it";
adoption per line is the tool's done, and each unadopted line answers the four
questions (nova-tools #111 to #117).

**What this amendment is.** Five things the day file cannot say today, each a
verb or a file, none of them a change to the day file. A day file still has
eleven columns and the key `(day, model, repo)` (rule 15), `check` still
gates it (rule 13), and every number below is folded from the same declared
sources by the same readers (rule 2). What is added: a **usage receipt** that
joins one session's spend to one work node and one nova-work `:attempt`; a
`cost` that sums every stage of a node's work, review and repair included;
`hot` and `diff` over sessions and days; a **friction** record with a cost,
listed per gap; and a decision on where an **adoption matrix** lives, with its
contract. The rules are numbered on from rule 31.

| the failure, from the record | the rule that closes it |
|---|---|
| a builder's tokens were on the day file and the reader's and the repair round's were on somebody else's row, and no line said the three were one task (Stella, **2026-09-13** 15:30Z) | a usage receipt names the **node** and the **stage** — one of the six names PROPOSAL-SCHEDULING-COST already uses — and `cost --node` sums every stage, printing each (rules 32, 33) |
| nova-work's `:attempt` carries `:usage`, "a pointer to a token record (#181)", and nothing defined what the pointer names (SPEC-WORK draft 12, **2026-09-13**) | the pointer is `note:usage:<receipt-id>` — the one scheme SPEC-WORK accepts on an attempt's `:usage` today (draft 2) — the receipt is a row this tool wrote, keyed `(day, model, repo, receipt)`, and the id is a draw, so the receipt exists before the attempt event that names it (rule 32) |
| "how can we reduce our average cost per-token" and "how can we do more work with fewer tokens" were two questions with no line that answered both (Glenn, **2026-09-12** 20:45Z, both in the one minute; draft 2) | `COST OK` and `SUM TOTAL --rates` print `tokens=`, `usd=` and `per_mtok=` beside each other, with the denominator and `unpriced=` (rules 33, 38) |
| the spec said "it does not price anything" while the maintainer keeps a rate table and asked that "nova-tokens grows a rates input" (nova-tools #175, **2026-09-12** 21:25Z) | pricing is `--rates <file>`, the caller's dated table, list rate only, nothing compiled in; a model or type with no rate is `unpriced`, never zero (rule 38) |
| finding a repeat read in a long pass was "heavy transcript digging" (Emma, **2026-09-13**) | `hot`: the top N repeated reads and largest step outputs of one session, a bounded listing and one `HOT NOTE` paragraph, and what it cannot see said on the same line (rule 35) |
| two day files, and no way to say what moved but reading both | `diff`: rows added, removed and changed by type, largest movement first, capped, a dash on either side a dash (rule 36) |
| 2.2M tokens on the same four gaps and no record with a cost per gap (Rowan, **2026-09-11**) | `friction add` records the stumble with its tokens (from a receipt, or `~` by hand), its wall clock, its gap and the issue it became; `frictions` sums per gap so the next tool is chosen by measured cost (rule 37) |
| a tool nobody else ran counted as shipped; adoption was read from assumption (Glenn, **2026-09-12** 13:30Z) | an adoption matrix per line per tool from evidence the machinery already holds, never self-report alone; it lives on `nova-update` (the amendment note below) |
| one receipt per session for ever: a session resumed the next morning could not record its later turns at all (Stella, **2026-09-13**, draft 1; open at draft 2) | `--resume` receipts the messages after the last receipt's `ended`, the cursor being a stamp already on the stored rows (rule 39, **draft 3**) |
| a session was made to be one task, one node and one stage, and a coordinator's session is none of those (Stella, **2026-09-13**, draft 1; open at draft 2) | the unit is the **span**, its spans are disjoint so `cost` cannot double count, and what cannot be split is receipted whole and said to be the caller's (rule 40, **draft 3**) |
| a multi-day receipt written by two renames could crash after day one, and the session-level `receipted` check then blocked every repair (Stella, **2026-09-13**, draft 1; open at draft 2) | a plain re-run re-derives the stored span under the stored id and writes only the absent days, `state=completed`; a stored row it cannot reproduce is `reason=differs`, never a rewrite (rule 41, **draft 3**) |

### Rules 32 to 41 (rules 39 to 41 are draft 3)

32. **A usage receipt is one session's spend, folded by the same readers,
    joined to one work node and one stage, and never typed.**
    `receipt` reads exactly one session from one declared source — a Claude
    Code transcript by its session id (`--claude <label>=<dir> --session
    <id>`; **the session is every message line under `<dir>` whose `sessionId`
    field is that id, in sorted path order, whatever file holds it** — draft
    2, one reading where the first draft had two: a file named for the id
    whose lines carry another `sessionId` is not the session, and an Agent
    child transcript is inside this receipt exactly when its lines carry the
    parent's id and is its own receiptable session when they do not; the
    record decides, never the file name, which is the same rule that folds an
    OpenCode child into its parent below), an OpenCode
    session by its `session.id` (`--opencode <label>=<file> --scratch <dir>
    --session <id>`, the session and its child sessions, as the fold already
    inherits them), or a swarm job by its id (`--swarm <label>=<pool> --job
    <id>`, the one usage file `<pool>/usage/<job>.tsv`) — through the reader
    `fold` uses (a second reader would drift, rule 2's `sources`), with the
    same attribution (rule 5), the same dedup (rule 4), the same five types
    and dashes (rule 15) and the same UTC days (rule 17). It writes one
    **receipt**: one or more rows, one per `(day, model, repo)` the session
    touched, all sharing one **receipt id**, thirty-two lower-case hex
    characters read from the operating system's random source at creation, as
    nova-board draws a card id — derived from nothing two writers could both
    observe (SPEC-BOARD, the id paragraph), so a receipt on any bench gets its
    id without reading anything first. The rows land in
    `<out>/receipts/<day>.tsv` (the **receipts file**, below), one per day
    the session spanned, written whole through `<day>.tsv.tmp` and one rename
    in that directory under the same `fold.lock` rule 8 takes; the tool
    removes nothing (rule 9). Every row carries: `node`, the work node id
    exactly as the caller gave `--node <id>` — a string this tool does not
    resolve, because the work set is nova-work's and its ids are data here;
    `stage`, exactly one of `preparation`, `implementation`, `review`,
    `correction`, `coordination`, `validation` — the six names
    [PROPOSAL-SCHEDULING-COST.md](PROPOSAL-SCHEDULING-COST.md) already
    uses, taken rather than invented, and "a stage label is metadata, not a
    second spend event"; `session`, the label and session or job id,
    `<label>:<id>`; `started` and `ended`, the first and last message stamps
    the session's own records carry, UTC, so a receipt's wall clock is the
    source's and not a person's; and the receipt id. **The join is one
    pointer each way, each written once.** `USAGE OK` prints
    `pointer=note:usage:<receipt-id>`, and that is the string the caller hands
    to
    `nova-work attempt --node <id> --model <m> --bench <b> --result <p>
    --usage note:usage:<receipt-id>`; the `:attempt` event thereby names the
    receipt, and the receipt names the node. **The scheme is
    `note:<scheme>:<id>`, which SPEC-WORK accepts today** (draft 2, replacing
    a bare `usage:` that nothing accepts). SPEC-WORK's draft ships a **closed
    six** — `commit:<sha>`, `run:<owner/repo>#<id>`,
    `pr:<owner/repo>#<n>@<sha>`, `file:<path>@<sha>`,
    `test:<package>/<name>@<sha>` and `note:<scheme>:<id>`, the last "for any
    team's message store, so that no family's bus is named in the tool" — and
    it says a `note:` pointer "is accepted on a heartbeat and on an attempt's
    `:usage`, never as evidence for `:done`". That is exactly where a spend
    record belongs and exactly where it must not be: a receipt is a
    measurement, never completion evidence, and nova-work's check 5 refuses a
    `:to :done` that stands on a `note:` pointer. A pointer spelled
    `usage:<id>` is on none of the six and is refused by nova-work as it
    stands, so this spec does not spell it that way; listing `usage:` among
    the schemes would be a separate amendment to SPEC-WORK, and **nothing
    here waits on it**. The attempt's own event id is
    nova-work's, assigned after the receipt exists, and is written **nowhere
    in this tool's files**: a receipt that had to be rewritten to carry it
    would be a read-modify-write of a record rule 9 says is never edited, and
    a receipt that waited for it would be a number that could not exist until
    the attempt did. So a **builder**, a **reader** and a **repair round** on
    one task are three sessions, three receipts, three `:attempt` (or
    `:review-attest`) events, one `node` and three `stage` values —
    `implementation`, `review`, `correction` — and nothing joins them but the
    node id they each carry. A `--stage` outside the six, a `--node` that is
    empty, a session the source does not hold (`USAGE FAIL
    reason=nosession`), or a session already receipted (`reason=receipted`,
    naming the earlier id) writes nothing. **What *already receipted* means is
    narrowed by rules 39 and 41** (draft 3): a session may carry several
    receipts over disjoint spans, and a plain run over a receipt whose
    multi-day write was interrupted completes it rather than refusing it, so
    `reason=receipted` is *this span is already recorded whole* and not *this
    id has been seen*. A source file the receipt needs
    that cannot be read is `TOKENS UNREADABLE` and `USAGE FAIL
    reason=unreadable`, **nothing written**: `fold` writes what it could and
    exits 1 because a day is a sum of many files; a receipt over half a
    session is a wrong number about one task. `--who <name>` is carried as
    `who`, for a friend's reading, and is not in the key, as rule 15 says of
    the bus line's `who`. (Stella, 2026-09-13 15:30Z; SPEC-WORK draft 12, the
    `:attempt` event; #181.)
    **Demanded test.** A fixture transcript directory holding three sessions:
    `receipt --session A --node n1 --stage implementation` writes rows whose
    `(model, repo, type)` counts equal `fold --claude` over exactly the lines
    whose `sessionId` is A (draft 2), `USAGE OK` prints
    `pointer=note:usage:<32 hex>` and `rows=`, a second
    `receipt` over A is `USAGE FAIL reason=receipted` naming the first id
    with the receipts file byte-identical; sessions B (`--stage review`) and C
    (`--stage correction`) on `n1` land three receipts; a session spanning
    midnight lands rows in two receipts files under one id; `--stage builder`
    is exit 2 naming the six; `--session Z` is `reason=nosession`, exit 1,
    nothing written; a mode-000 file in the session is `reason=unreadable`
    with no `.tmp` left; two `receipt`s in parallel on one `--out` serialize
    on `fold.lock` and both land; an OpenCode fixture with a child session
    folds the child into the parent's receipt; a swarm fixture `--job J`
    writes one receipt from the usage file with `started`/`ended` from its
    `started`/`ended` columns; a Claude fixture whose Agent child transcript
    carries the **parent's** `sessionId` folds that child's messages into the
    receipt, while a second child carrying its **own** id is excluded from it
    and is receiptable on its own (draft 2); and **the pointer is one of
    SPEC-WORK's six schemes** (draft 2): a table test holds the six SPEC-WORK
    enumerates — `commit:`, `run:`, `pr:`, `file:`, `test:` and
    `note:<scheme>:<id>` — asserts that `note:usage:<32 hex>` is a
    `note:<scheme>:<id>` and is accepted on an attempt's `:usage` and refused
    as `:done` evidence, and asserts that a bare `usage:<32 hex>` matches
    **none** of the six, so the test goes red the day this spec spells the
    pointer a way nova-work refuses. It is a table of the six, not a generic
    regexp: SPEC-WORK prints an enumeration and a test against an invented
    `<scheme>:<id>` regexp would pass vacuously.

33. **`cost --node <id>` sums every receipt of a node across every stage,
    and prints total tokens, list-rate dollars and the average per million
    beside each other, with the denominator.** It walks
    `<out>/receipts/*.tsv`, or only that month's receipts files when
    `--month <m>` is given — `--node` is required either way, and `--month`
    narrows the files read, never replaces the selection (draft 2) — selects
    rows whose
    `node` equals a `--node` given (repeatable; exact match, never a prefix,
    because containment is nova-work's `:children` and not a string's shape,
    and a prefix would be a guess), and prints: `COST STAGE` per stage
    present, in the six names' order, with `receipts=`, `tokens=` (the five
    types summed where numbers, dashes counted), `usd=` and `per_mtok=`;
    `COST MODEL` per model, largest tokens first, capped; `COST RECEIPT` per
    receipt, newest `ended` first, capped, each with its `session=`, `stage=`,
    `started=`, `ended=`, `wall=` (`ended` minus `started`); then `COST OK
    nodes=<n> receipts=<n> stages=<list> tokens=<n> dashes=<n> usd=<n|->
    per_mtok=<n|-> priced_tokens=<n> unpriced_tokens=<n> wall=<d>
    rates=<file|-> verified=<date|->`, where `verified=` is the oldest
    `verified` date **among the rate rows this run actually priced with**
    (rule 38, draft 2). **Review and repair are in
    the sum by construction**: there is no flag that selects a stage, only
    `COST STAGE` lines that show each, because a cost that could be asked
    "builder only" would be the number Stella's sentence forbids. `usd=` is
    rule 38's: the caller's `--rates` file times the counts, list rate, and
    `-` when `--rates` is not given; `per_mtok=` is `usd` over
    `priced_tokens` in millions, printed only when `priced_tokens` is above
    zero (no division by zero, PROPOSAL-SCHEDULING-COST: "report zero
    counted tokens without division"), and `unpriced_tokens=` is every token
    on a model or type the rates file has no number for, so a mean can never
    fall by leaving a model out. `wall=` is the sum of receipts' spans, not
    their union: three parallel children are three wall clocks, and the line
    says `wall=` is a sum. A node with no receipt is `COST OK nodes=1
    receipts=0 tokens=0 …`, exit 0, and `COST NOTE` says no receipt names it
    — unmeasured, never zero. **`COST NOTE` prints on every run** (draft 3,
    rule 40): it names a node with no receipt when there is one, and otherwise
    says that a receipt's node and stage are the caller's and were never
    measured — one line either way, the shape SPEC.md fixes for a NOTE.
    `cost` is a **report**; never gate on it.
    (Glenn, 2026-09-12 20:45Z, where both questions are asked in the one
    minute — 21:16Z is the *Assume API rate* sentence rule 38 quotes, and the
    first draft cited it here for a question it does not carry, draft 2;
    Stella, 2026-09-13 15:30Z, stella-4b9200ddc994; Glenn via Stella,
    2026-09-13 16:34Z, stella-ba384f9132f5.)
    **Demanded test.** Three receipts on `n1` (implementation 100/10,
    review 20/5, correction 30/8 input/output) and one on `n2`: `cost --node
    n1` prints three `COST STAGE` lines in the fixed order, `tokens=173`,
    `receipts=3`; `--node n1 --node n2` adds the fourth; `--node n` (a prefix
    of `n1`) is `receipts=0` with the NOTE; with `--rates` naming one of two
    models, `usd=` is that model's alone, `priced_tokens=` and
    `unpriced_tokens=` add to `tokens=`, and `per_mtok=` equals `usd` over
    `priced_tokens` to the printed precision; without `--rates` the three
    are `-`; a rates file with `-` for `reasoning` leaves a receipt's
    reasoning tokens in `unpriced_tokens=`; a node whose only receipt has
    every cell `-` prints `tokens=0 dashes=5` and the NOTE; the source test
    finds no flag on `cost` that filters by stage.

34. **The receipts of a day are a subset of the day, one session is one
    receipt, and `check` gates both.** `<out>/receipts/<day>.tsv` is tab
    separated with the first line
    `nova-tokens receipts v1 day=<d> at=<stamp> build=<id>` and the columns
    `date model repo receipt node stage who session started ended input
    output cache_write cache_read reasoning day_basis sources` — seventeen,
    every one written on every row, the five types by rule 15, `day_basis`
    by rule 17, `sources` the one label the receipt was read from. Rows are
    sorted by `(receipt, model, repo)` and unique by that key. `check`
    (rule 13, amended) also walks `<out>/receipts/`, applies the same row
    rules, and verifies **four joins** (draft 2): every receipt's `session`
    appears under exactly one receipt id across the whole directory, **or under
    several whose `[started, ended]` spans do not overlap** (draft 3, rule 39's
    resumed session) — two receipts of one session that share any stamp are
    `CHECK DUPLICATE session=<s> receipts=<id,id>`, because an overlap is the
    double count this join exists to catch and adjacency is not; **every row sharing a receipt id
    carries one `node`, one `stage`, one `who` and one `session`** (`CHECK
    SPLIT receipt=<id> field=<node|stage|who|session> values=<a,b>`, draft 2),
    because a receipt is one session joined to one node at one stage — rule
    15's key makes the rows unique by `(receipt, model, repo)` and nothing
    else stopped one receipt from carrying two nodes, which `cost --node`
    would then count under both; for every `(day, model, repo)`
    that has receipts and a day file row, each type's receipt sum is not
    greater than the day file's cell where both are numbers (`CHECK EXCEEDS
    date= model= repo= type= receipts=<n> day=<n>`: a receipt is a subset
    of a fold over the same source, so more is a wrong receipt, a shrunken
    day, or — the common case, and the reason the line carries its remedy
    first (draft 2) — a day folded before the session ended:
    `nova-tokens fold --out <dir> --day <d>`). **A day folded before the
    session ended is that common case and is not a finding** (draft 3): the
    comparison is `EXCEEDS`, exit 1, only where the day file was written
    **after** the session ended — its own version-line `at=` (rule 12) later
    than the row's `ended` — and where the day file is the older of the two,
    or has no row for that `(day, model, repo)` at all, it is the same one
    `CHECK NOTE`, exit 0. Draft 2 repaired only the newer-day half of this
    hurt: a bench that folds `--all` during the day has today's file already,
    and every evening receipt then exceeded a stale cell and took the gate red
    on the ordinary order. Both stamps are on disk, so the test is mechanical
    and needs nothing the fold does not already write. And a receipts file
    whose day
    has no day file is `CHECK ORPHAN date=<d>`, named, because a receipt for a
    day nobody folded is a number with nothing to check it against.
    **The ordinary order is a receipt when the session ends and a fold that
    night** (draft 2), so a receipts file for a day **newer than the newest
    day file present** is not an orphan and not a finding: it is one
    `CHECK NOTE`, carrying `days=<n>` and the remedy `fold --day <d>`,
    exit 0 — the **count** and the remedy, not a list of days, because a month
    of receipts ahead of the fold would otherwise be a list cut by
    `oneline.TailBytes` with no number beside it (draft 3, and every other
    listing here carries its count). `CHECK ORPHAN` is
    a day at or before the newest day file whose own day file is absent — the
    fold went past it and did not write it, which is also `CHECK MISSING` for
    the day files themselves. **With no day file present at all there is no
    newest day file, and every receipts file is that same `CHECK NOTE`,
    exit 0** (draft 3, where *newer than the newest day file present* was
    undefined over an empty comparison and the fallback was the finding): a
    bench that receipted a session before it ever folded is the ordinary order
    at its beginning, and `CHECK MISSING` already says there are no day
    files. Each finding is exit 1. `check` never fills, and
    `receipts/` is not a stray: it is the one subdirectory this spec names
    under `--out`, and anything else under it is `CHECK STRAY`. `sum` does
    not read receipts (a day file already holds the day; receipts are its
    joins), and `TOKENS DAY` gains `receipts=<n>`, the count of receipt rows
    that day has, so a fold's line says how much of the day is joined to
    work. (Rule 13's "check is the gate", 2026-09-11; the subset invariant is
    new.)
    **Demanded test.** A receipts file with sixteen columns is `CHECK FAIL`
    naming the line; two receipt ids for one `session` is `CHECK DUPLICATE`;
    a receipt whose `input` exceeds the day file's cell **of a day file
    stamped after the session ended** is `CHECK EXCEEDS`
    carrying `fold --day` as its remedy, while an equal one is clean and a `-`
    on either side is not compared; the same receipt against a day file whose
    version-line `at=` is **earlier** than the row's `ended` is one
    `CHECK NOTE` carrying `days=1`, exit 0, and so is a receipt row whose
    `(day, model, repo)` has no day-file row at all — the red being draft 2's
    rule, under which a bench that folds `--all` during the day takes the gate
    red every evening (draft 3); two rows of one receipt id carrying two
    `node` values are `CHECK SPLIT`, and two carrying two `stage` values are
    another, while a receipt spanning two models and two repos under one node
    and stage is clean (draft 2); a
    receipts file for a day **at or before** the newest day file and with no
    day file of its own is `CHECK ORPHAN`, exit 1, while a receipts file for a
    day **newer** than every day file is one `CHECK NOTE` and exit 0 (draft
    2); a receipts directory holding two receipts files and **no day file at
    all** is that same one `CHECK NOTE`, exit 0, and never `CHECK ORPHAN`
    (draft 3); a file
    `receipts/notes.txt` is `CHECK STRAY`; a clean set is `CHECK OK … dup=0
    split=0 exceeds=0 orphan=0`; `TOKENS DAY receipts=` equals the rows in that
    day's receipts file; `sum` opens nothing under `receipts/` (a tripwire on
    opened paths).

35. **`hot` reports the repeated reads and the largest step outputs of one
    session, bounded, in the grammar and in one paragraph, and says what it
    cannot see.** The session is named as `receipt` names it (rule 32's
    three forms); `--top <n>` defaults to 5 (Emma's number) and bounds each of
    the two
    listings separately, per kind. **`--top 0` means all** and a negative
    `--top` is exit 2 (draft 2, correcting a first draft that refused `0`):
    SPEC.md's Conventions fix `0` as *all* for every ceiling in this family
    and say that "a ceiling a caller cannot lift is a tool deciding what its
    user may see"; `hot` has no `--max`, so `--top` is its only ceiling, and
    a third reading of `0` in one family is a flag two readers would spell
    differently. The `HOT MORE` remedy is therefore `--top 0`, a flag that
    works — a cap with no remedy is censorship, and a cap whose remedy is
    refused is worse. A **step** is one tool call and its
    result: for a Claude Code transcript, a `tool_use` block in an assistant
    message and the `tool_result` block in the following user message whose
    `tool_use_id` matches; for OpenCode, a `part` row of type `tool` and its
    `state.output`; for a swarm job, the job's OpenCode database at the data
    home the job record names while it exists, and `HOT FAIL
    reason=reclaimed` after `reclaim` has moved it — the usage file survives,
    the steps do not, and this verb says so rather than reading `done/` for
    anything else (rule 14's tripwire is the fold's; `hot --job` opens the
    one job directory named and nothing beside it). A **read** is a step
    whose input names a path (`file_path`, `path`, `filePath`, `pattern` with
    a path) or a command; two steps are **the same read** when their tool
    name and that path, or their command string, are byte-equal. `HOT REPEAT`
    lists the reads seen more than once, most repeats first, then most bytes
    first: `count=`, `bytes=` (the sum of the result lengths across the
    repeats), and the path or command after the colon, capped at
    `oneline.TailBytes`. `HOT LARGEST` lists the steps with the largest
    result, largest first: `step=` (its ordinal in the session), `tool=`,
    `bytes=`, and the path or command. `HOT OK session=<label>:<id>
    messages=<n> steps=<n> reads=<n> repeated=<n> repeated_steps=<n>
    repeated_bytes=<n> result_bytes=<n> unranked=<n> largest_bytes=<n>
    input=<n|->
    output=<n|-> cache_read=<n|-> started=<stamp> ended=<stamp> at=<stamp>
    build=<id>` — the same fields the output grammar prints, `unranked=`,
    `at=` and `build=` included, which the first draft's copy of this line
    dropped (draft 2); `unranked=` is the steps whose result the record does
    not carry, which rank nowhere and are counted in `steps=` — puts the
    session's own token counts (from the same messages, by rule 4) beside
    the step bytes, so a reader sees how much of a session's output tokens
    are tool results it read back. Then exactly one `HOT NOTE`, Emma's
    paragraph, one line under `oneline.TailBytes`: how many of the steps were
    repeats and what share of the result bytes they were, the one read
    repeated most and the one step that was largest, and **what this verb
    cannot see**, always said, opening with the phrase this spec **fixes** so
    a test can assert it rather than assert that a phrase exists (draft 2) —
    `cannot see: bytes are not tokens` — and then: it counts the bytes of a
    result as the record
    holds them, not the tokens they became (the
    provider's count is on the message, not the step); a result the harness
    truncated before recording is counted at its recorded length; a step
    whose result the record does not carry is `bytes=-` and not ranked
    (OpenCode's `state.output` can be absent; a transcript's `tool_result`
    can be a reference); cache reads and reasoning are per message, never
    per step; and a repeat is a repeat of the same string, so a read of one
    file by two spellings is two reads. `hot` reads sources read-only (rule
    16), runs no `git` (rule 27), and is a **report**: exit 0 whenever it
    ran, exit 1 `HOT FAIL reason=<nosession|reclaimed|unreadable>` when it
    could not see the session, with the count line printed either way.
    (Emma, 2026-09-13, emma-0fd8c03d5f24; Rowan, 2026-09-11, window tokens
    are turns: tool output read back was 287k of one day's output.)
    **Demanded test.** A fixture transcript with twelve steps: one file read
    three times, one command run twice, one 40 KB result and the rest under
    1 KB: `HOT REPEAT` prints two lines, the read first with `count=3`, the
    command second with `count=2`, `HOT LARGEST` prints the 40 KB step
    first with its ordinal, `--top 1` prints one of each and a `HOT MORE`
    per kind, `HOT OK` says `steps=12 reads=11 repeated=2 repeated_steps=5`
    and its `input=`/`output=` equal `fold`'s for that session; `HOT NOTE` is
    exactly one line, under `oneline.TailBytes`, containing the phrase the
    spec fixes for what it cannot see; a `tool_result` that is a reference
    ranks nowhere and `HOT OK` counts it in `steps=`; an OpenCode fixture
    with `state.output` NULL prints `bytes=-` for that step; a swarm job
    under `done/` with its data home reclaimed is `HOT FAIL
    reason=reclaimed`, exit 1, and the opened-paths tripwire finds only that
    job's directory; `HOT NOTE` begins with the fixed phrase
    `cannot see: bytes are not tokens`; `HOT OK` carries `unranked=` equal to
    the steps whose result the record does not hold; `--top 0` prints every
    repeat and every ranked step with **no** `HOT MORE` of either kind, and
    `--top -1` is exit 2 (draft 2).

36. **`diff` says what moved between two day files or two sessions, largest
    movement first, bounded, and a dash on either side is a dash.** Over
    days, `diff --out <dir> --day <d1> --day <d2>`: for the union of
    `(model, repo)` rows, one `DIFF ROW model= repo= type= from=<n|-|absent>
    to=<n|-|absent> delta=<n|->` per type whose two sides differ, sorted by
    absolute delta descending, then by name, capped; `delta=-` when either
    side is `-`. **A row present on one side only is ONE row line, not five**
    (draft 2): `type=-`, `from=absent` or `to=absent`, `delta=-`, counted in
    `added=` or `removed=` — five lines each saying *absent* about one row say
    nothing the one line does not, and they are four lines of a bounded
    listing spent on it (absent is not zero, and never `delta=+all`);
    then `DIFF OK kind=day from=<d1> to=<d2> rows_from=<n> rows_to=<n>
    added=<n> removed=<n> changed=<n> tokens_from=<n> tokens_to=<n>
    delta=<n> dashes=<n> turns_from=<n|-> turns_to=<n|->`. Over sessions,
    `diff <one source flag> --session <a> --session <b>` (or `--job <a>
    --job <b>`): the `HOT OK` counts of each side and their differences,
    one `DIFF ROW field=<name> from= to= delta=` per field that differs,
    then `DIFF OK kind=session …` with the same shape. `diff` reads what
    `sum` and `hot` read, through their code, writes nothing, and exits 0
    whenever it ran: a difference is the answer. A day file missing on one
    side is exit 2 naming it, not a diff against nothing. (Rule 10's
    `SHRANK` compares a day to its own past; this compares two days, or two
    sessions, and judges neither.)
    **Demanded test.** Two day files differing in one cell, one row present
    only on the right and one `-` that became a number: three `DIFF ROW`
    lines in delta order with `from=absent` and `delta=-` where the spec
    says, `added=1 changed=1 dashes=1`; identical files print no ROW and
    `changed=0`; `--max 1` prints one ROW and a MORE; a missing right-hand
    file is exit 2; two sessions differing in `repeated=` print that field
    and `kind=session`; the opened-paths tripwire finds nothing beyond the
    two named files.

37. **A friction is a stumble with a cost and a record, and `frictions`
    sums per gap so the next tool is chosen by measured cost.** `friction
    add --frictions <file> --as <name> --gap <label> --tool <name|->
    --attempted <text> (--receipt <id> --out <dir> [--wall <duration>] |
    --tokens ~<n> --wall <duration>)
    [--issue <host>/<owner>/<repo>#<n>]` appends one row to the
    caller's **frictions file**: `at who gap tool attempted tokens rough
    wall receipt issue`, tab separated, first line
    `nova-tokens frictions v1 at=<stamp> build=<id>`. `gap` is `[a-z0-9-]+`,
    at most 32 characters, the name of the pattern — the thing fixed by
    hand again — and `tool` is the existing tool it belongs to, or `-` when
    the problem is new (Glenn, 2026-09-11: add to an existing tool unless the
    problem is new). `tokens` comes one of two ways and no third: from a
    receipt, `--receipt <id>` with `--out <dir>`, the sum of that receipt's
    numeric cells with `rough=0`, and `wall` from the receipt's span unless
    `--wall` overrides it; or typed by a person as `--tokens ~<n>`, the `~`
    **required** — a hand number is rough, rule 7's mark, `rough=1` — with
    `--wall` required beside it, because a stumble with no receipt has no
    clock but the person's. A bare `--tokens 1000` is exit 2: the tool
    stamps, a person estimates, and the file says which. `--issue` is the
    issue the friction became on the forge, host-bound as rule 30 spells a
    repository (an idea is an issue, never a list in a file); `-` until it
    exists, and a later `friction add` for the same gap may carry it.
    `FRICTION OK` **prints it in that same host-bound shape**,
    `issue=<host/owner/repo#n|->`, not the bare `repo#n` draft 2's grammar line
    carried (draft 3): one fact, one spelling, and a scanner written against
    the synopsis would not have matched the output. The
    file is **append-only**: `add` reads it, checks the existing bytes parse,
    writes old rows plus the new one through `<file>.tmp` and one rename
    under an `flock` on `<file>.lock`, and a file whose existing rows do not
    parse is exit 2 naming the line, nothing written. `frictions --frictions
    <file> [--rates <file> --out <dir>] [--since <date>] [--max <n>]` prints
    one
    `FRICTIONS GAP gap=<label> tool=<name|-> count=<n> tokens=<n> rough=<n>
    wall=<d> usd=<n|-> unpriced_rows=<n> issues=<n> latest=<stamp>` per gap,
    **tokens descending**, capped, then `FRICTIONS OK gaps=<n> rows=<n> tokens=<n>
    rough=<n> wall=<d> usd=<n|-> issues=<n> since=<date|-> rates=<file|->
    verified=<date|-> unpriced_rows=<n>`. **A friction row carries one token
    sum and no model, so it cannot be priced from itself** (draft 2, where the
    first draft demanded a price it had no counts for): `usd=` is computed by
    **joining the row's `receipt` id to `<out>/receipts/*.tsv`** and pricing
    that receipt's own per-model, per-type cells by rule 38 — the same
    function `cost` uses, over the same rows, so the two agree by
    construction. `--rates` therefore **requires `--out <dir>`** and is exit 2
    without it, the way `--scratch` follows `--opencode`; and a rough row, or
    a row whose receipt id is not under `--out`, prices to nothing and is
    counted in `unpriced_rows=`. **`unpriced_rows=` is on `FRICTIONS GAP` as
    well as on `FRICTIONS OK`** (draft 3): "a gap's dollars are a lower bound,
    and the line says in how many rows" named a field only the count line
    carried, so the per-gap line made the claim and could not support it. A gap
    that priced no row prints `usd=-`, never `usd=0`, by rule 38's test for a
    line that carries no `priced_tokens=`. It is a **report**, exit 0. **Why this
    verb is on `nova-tokens` and not `nova-board`.** A friction is a
    measurement before it is an obligation: its tokens and wall clock come
    from the receipts and readers this tool owns, its listing is a cost
    table sorted by spend, and the decision it serves — which pattern to
    crystallize next — is a cost decision (Glenn, 2026-09-11: dogfood, see
    the pattern, crystallize it into few good tools; "More tools = fewer
    tokens"). The board holds what is owed, by whom, by when, with a
    default; a friction is owed by nobody until a person files the issue,
    and that issue is the board's business (nova-board `add --evidence`, or
    the forge issue itself), which is why the row carries `issue` and no
    owner, no deadline and no default. Two records for one stumble would be
    the two ledgers PROPOSAL-SCHEDULING-COST warns against; so the friction
    row is the measurement, the issue is the obligation, and the row points
    at the issue. **The frictions file is publishable** as a third typed
    contribution, `publish --frictions <file> --seat <label>`, landing at
    `frictions/<seat>.tsv` — a subtree neither `records/` nor `v1-days/`
    uses — mutable, with one transition of its own: **append**, where a
    destination whose rows below its version line are a prefix of this file's
    publishes without `--supersede`; a destination that is not a prefix is
    `reason=differs` until `--supersede`; identity is the digest of the rows
    below the version line, as rule 26 defines a v1 day's. **It does not hold
    under rules 22 to 31 as they stood; it opens its own dated door in them**
    (draft 2, where the first draft said they held unchanged and rules 22, 24
    and 26 said otherwise): rule 22 gives `--seat` to this kind as well as to
    `--v1-day` and refuses `--day` with it; rule 23 gives the commit message
    its `kind=frictions`, `seat=`, `rows_sha256=` and `appends=<hex|->`
    fields; rule 24's present case names the second door; and rule 26 carries
    the four transitions and the demanded test. `--frictions` is
    refused with `--batch`, `--v1-day`, `--day` or `--public`. (Rowan,
    2026-09-11: 2.2M tokens on four gaps by hand; Glenn, 2026-09-11,
    crystallize; Glenn, 2026-09-11, ideas are issues.)
    **Demanded test.** `friction add` with `--receipt` over rule 32's
    fixture writes one row whose `tokens` equals the receipt's sum and
    `wall` its span, `rough=0`; with `--tokens ~5000 --wall 40m` writes
    `rough=1`; `--tokens 5000` is exit 2 naming the `~`; `--tokens ~5000`
    without `--wall` is exit 2; a `--gap` with a space or 33 characters is
    exit 2; a corrupted existing row is exit 2 with the file byte-identical
    and no `.tmp`; two concurrent `add`s on one file serialize on the lock
    and both rows land; `frictions` over five rows on three gaps prints
    three `GAP` lines in tokens-descending order, `issues=` counting rows
    with an issue, `--since` excluding older rows from every count,
    `--rates --out <dir>` pricing exactly the rows whose `receipt` id is under
    `--out` — its `usd=` equal to `cost`'s over those same receipts to the
    printed precision — with `unpriced_rows=` counting the rough rows and the
    receipt-less ones **on each `FRICTIONS GAP` as well as on `FRICTIONS OK`**,
    and a gap of rough rows alone printing `usd=-` and never `usd=0`
    (draft 3), and `--rates` **without** `--out` exit 2 (draft 2);
    `publish --frictions`
    lands `frictions/<seat>.tsv`, a second publish after one more `add` is
    `state=appended`, exit 0, with no `--supersede` and a commit message
    carrying `appends=`, a
    publish whose file dropped a row is `reason=differs`, and `--frictions`
    with `--batch`, `--v1-day` or `--public` is exit 2 each.

38. **Pricing is the caller's dated rate table at list rate, nothing is
    compiled in, and a missing rate is unpriced, never zero.** `--rates
    <file>` is tab separated: `model input output cache_write cache_read
    reasoning verified source`, USD per million tokens, `#` and blank lines
    skipped, a `-` cell meaning the provider publishes no such rate (so that
    type on that model is **unpriced**), `verified` a `YYYY-MM-DD` the caller
    checked the row, `source` a URL or a word. A malformed line is exit 2
    naming it; a model named twice is exit 2 — which is why a table
    maintained elsewhere in another shape (a `who` or a `notes` column, no
    `reasoning`, or two rows for one model's peak and off-peak) is **the
    caller's to convert** into these eight columns before it is handed to
    `--rates`: this tool reads one shape, prices from it and reports
    `verified=` out of it, and a converter that guessed which of two rows to
    take would be the guess rule 1 forbids (draft 2). Pricing is `sum(count × rate /
    1,000,000)` over the cells that have both a number and a rate, per type,
    per model; every other cell's tokens go to `unpriced_tokens=`. **Every
    run that prints `usd=` prints `rates=<file>` and `verified=<date>` once,
    on its own count line** — `SUM MONTH`, `COST OK`, `FRICTIONS OK` — and
    **every listing line that prints `usd=` prints it without `rates=` and
    `verified=`** (draft 3). `unpriced_tokens=` is on the lines whose grammar
    carries it — `SUM PAIR`, `SUM MODEL` and `SUM TOTAL` — and on no others:
    draft 2 said *every listing line beneath it prints `usd=` and
    `unpriced_tokens=`*, which is false of the four listing lines this
    amendment's own grammar gives `usd=` and no `unpriced_tokens=`
    (`COST STAGE`, `COST MODEL`, `COST RECEIPT`, `FRICTIONS GAP`), and a rule
    that describes fields its grammar does not print is a rule nothing can be
    tested against. One file name repeated on two hundred
    `SUM PAIR` lines is two hundred copies of one fact; one run, one
    provenance line, and no `usd=` is ever printed by a run that did not print
    it. `verified=` is the oldest `verified` date **among the rate rows this
    run actually priced with**, not the oldest in the file, so one stale row
    nothing used cannot taint every number (draft 2), and it is `-` when no
    row was used. **A model or type with no rate is never priced at zero and
    never printed as `usd=0`** (draft 2): its tokens are `unpriced_tokens=`
    on the lines that carry that field, and **a line that priced no token
    prints `usd=-`, and `per_mtok=-` wherever it carries that too** (draft 3).
    On the four lines that carry `priced_tokens=` — `SUM PAIR`, `SUM MODEL`,
    `SUM TOTAL` and `COST OK` — *priced no token* is spelled
    `priced_tokens=0`, which is what draft 2 said; on the four that do not —
    `COST STAGE`, `COST MODEL`, `COST RECEIPT` and `FRICTIONS GAP` — it is the
    same test over the same cells, no cell beneath that line having had both a
    number and a rate, and draft 2's sentence was undefined for exactly those
    four. A `usd=0` there would be the harm this rule forbids, inverted, and a
    `usd=0` is only ever a rate of zero the caller typed. The rate is the **list rate** (Glenn,
    2026-09-12 21:16Z: "Assume API rate, and factor this in"), whatever the
    account pays; this tool knows nothing of subscriptions, credits or
    invoices, and `usd=` is never called a bill. A local model is a row with
    zeros, typed by the caller (Glenn, 2026-09-12 21:24Z: "local models are
    $0"), and a zero rate prices to zero while a `-` prices to unpriced —
    the same distinction rule 15 draws between a reported zero and an
    absence. `sum --rates <file>` adds `usd=`, `per_mtok=`, `priced_tokens=`
    and `unpriced_tokens=` to `SUM PAIR`, `SUM MODEL` and `SUM TOTAL`, so
    the month prints its blended list-rate mean beside its total tokens —
    the two numbers Glenn asked for on 2026-09-12 20:45Z ("reduce our
    average cost per-token") and in the same minute ("do more work with
    fewer tokens"). The bullet **It does not price anything** under *what it
    deliberately does not do* is amended to say what it now does not do:
    carry a rate, choose a rate, or call a rate-times-count a bill. (Glenn,
    2026-09-12 21:25Z: "We should maintain this per-model cost per-token,
    AND keep it updated as we work"; nova-tools #175.)
    **Demanded test.** A rates file with two models, one of them `-` for
    `cache_write`: `sum --rates` prices every cell that has both, `usd=` on
    `SUM TOTAL` equals the hand sum to the printed precision,
    `unpriced_tokens=` equals the tokens on the third model plus the
    `cache_write` cells of the dashed one, `per_mtok=` is `usd` over
    `priced_tokens`; a month whose every priced count is zero prints
    `per_mtok=-`; a model row with all zeros prices to `usd=0` and
    `unpriced_tokens=0`; a malformed rate line and a duplicate model are exit
    2 naming the line; `verified=` is the oldest date **among the rows the
    run priced with**, and a stale row no line used does not appear in it
    (draft 2); a `SUM PAIR` on a model the table does not name prints `usd=-`
    and never `usd=0` at `priced_tokens=0`, while `SUM MONTH` alone carries
    `rates=` and `verified=`; a `COST STAGE`, a `COST MODEL`, a
    `COST RECEIPT` and a `FRICTIONS GAP` over rows this table prices nothing on
    each print `usd=-` and never `usd=0`, and none of those four carries
    `rates=`, `verified=` or `unpriced_tokens=` (draft 3); and a source test
    over
    `internal/tokens/rates.go` finds **no floating-point literal other than 0
    and 1 and no string literal that is a model name** — the mechanical form
    of *nothing is compiled in*, where *no model name in the binary* was not
    falsifiable, `<synthetic>` being a model name the Claude reader must know
    (draft 2).

39. **A session that continues is receipted again from where the last receipt
    ended, and a receipt is a span of a session, not the session** (draft 3).
    Draft 2 sealed a session with its first receipt: `receipt` over a session
    that already had one was `USAGE FAIL reason=receipted` whatever had
    happened since, so the later turns of a session resumed the next morning
    could not be recorded at all (Stella, 2026-09-13, on draft 1: "One
    permanent session receipt at first collection also prevents recording
    later turns of an active/resumed session"). `--resume` is the second door.
    `receipt … --session <id> --resume` counts exactly the session's messages
    whose own stamp is **strictly after** the greatest `ended` among that
    session's existing receipts — that stamp is the **cursor**, it is on the
    stored rows already and needs no column and no state file — draws its own
    receipt id, and writes those rows as a receipt of its own, `USAGE OK …
    state=resumed`. A message at exactly the cursor is already counted and is
    excluded, because `ended` is the stamp of the last message counted and not
    a boundary between two of them. `--node`, `--stage` and `--who` are the
    resumed receipt's own and need not equal the earlier receipt's: a session
    that came back on a different task is exactly what rule 40 is about.
    `--resume` over a session with no receipt is exit 2 — there is nothing to
    resume from and `receipt` is the verb — and a `--resume` that finds no
    message after the cursor is exit 1 `USAGE FAIL reason=nonew`, nothing
    written, because a receipt of no messages would be a row of dashes
    claiming a span. Rule 34's `DUPLICATE` join is narrowed to match: one
    session may carry several receipt ids whose spans are disjoint, and any
    overlap is still `CHECK DUPLICATE`.
    **Demanded test.** A fixture session of ten messages, the source holding
    the first six: `receipt` writes those, then the last four are appended to
    the transcript and `receipt --resume` writes a second receipt whose rows
    are the four, `state=resumed`; the two receipts' per-type sums equal
    `fold`'s over the whole session exactly — no message counted twice and none
    dropped — and a message whose stamp **equals** the first receipt's `ended`
    is in the first receipt and not the second, the red being a `>=` cursor
    that double-counts it. `--resume` with no earlier receipt is exit 2;
    `--resume` with nothing after the cursor is exit 1 `reason=nonew` with the
    receipts files byte-identical; `check` over the two receipts is clean, and
    over two receipts of one session whose spans overlap by one stamp is
    `CHECK DUPLICATE`.

40. **A session is not a task; `cost` sums spans, and what it cannot split it
    says it cannot split** (draft 3). Stella, 2026-09-13, on draft 1: "A
    session is not necessarily one task, node, stage, model or repo. This very
    coordinator session spans implementation, review, correction and multiple
    repositories." Draft 2 answered half of that and left half. The half it
    answered: a receipt is already **many rows**, one per `(day, model, repo)`
    (rule 32), and rule 34's `SPLIT` join is over `node`, `stage`, `who` and
    `session` and **not** over model or repo, so two models and two
    repositories in one session were never a problem. The half it left: one
    session could hold only one node and one stage, for ever. With rule 39 the
    unit is the **span**: a session that built before lunch and reviewed after
    it is receipted twice — `--stage implementation`, then `--resume --stage
    review` on another `--node` if the work moved — two ids, two spans, one
    session, and `cost --node` prints each as its own `COST RECEIPT` line. The
    three properties that make that safe are stated rather than assumed: the
    spans of one session are **disjoint** (rule 39's cursor), so no token is in
    two receipts and no `cost` double counts whatever mix of nodes the session
    touched; `tokens=` is therefore a sum over disjoint sets; and `wall=`
    remains the **sum of spans and not their union** (rule 33), which across
    one session's disjoint spans is less than that session's own wall clock by
    exactly the time between them — a fact the field says by being a sum.
    **What this tool still cannot do, said rather than implied**: it cannot
    split one span between two nodes. A stretch of work that changed task with
    no stamp a caller can point at is receipted whole under one node, and
    `COST NOTE` says that a receipt's node attribution is the caller's and was
    never measured. Unknown attribution stays unknown (Stella): there is no
    fractional allocation here, none is invented, and a caller is never asked
    to split a friend's session to fit the schema — the caller chooses where to
    resume, or receipts it whole and accepts one node.
    **Demanded test.** One fixture session receipted as `implementation` on
    `n1` and resumed as `review` on `n2`: `cost --node n1` and `cost --node n2`
    each print one `COST RECEIPT`, neither counts the other's messages, their
    `tokens=` add to `fold`'s over the whole session, and `cost --node n1
    --node n2` prints `wall=` equal to the two spans summed and **strictly
    less** than `ended` minus `started` across the session; `COST NOTE` carries
    the attribution sentence on every run that printed a receipt; and the
    source test finds no code path that divides a receipt's counts by anything.

41. **A receipt that spans days is completed after a partial write, never
    blocked by one** (draft 3). A receipt spanning midnight is rows in two
    receipts files (rule 32), landed by two renames; a crash between them left
    day one written and day two absent, and draft 2's `receipted` check — which
    asked only whether the session appeared anywhere — then refused every
    repair (Stella, 2026-09-13: "Writing a multi-day receipt by independent
    renames can crash after day1 and then the session-level receipted check
    blocks repair of day2"). A receipt is a **pure function** of its span, its
    `--node`, `--stage`, `--who` and its id, so the repair is to recompute it
    rather than to record a transaction: a plain `receipt` over a session that
    already has one re-derives the rows of **that receipt's own span**
    (`started` to `ended` inclusive, read off the stored rows) under the
    **stored** id, and then
    - every day's rows present and byte-identical → `USAGE FAIL
      reason=receipted by=<id>`, nothing written, which is draft 2's case
      unchanged;
    - one or more days' rows absent → those files, and only those, are written
      under the stored id, `USAGE OK … state=completed`, the count on the line
      being the days this run added;
    - a present day's rows differing from the re-derived ones → `USAGE FAIL
      reason=differs day=<d>`, nothing written, because a receipt this tool
      cannot reproduce from its source is not one it may quietly rewrite
      (rule 9: a row is never edited, and rule 10's refusal to overwrite a
      number the record disagrees with is the same instinct).
    A session that **grew** after a complete receipt is not this case and never
    `differs`: the re-derivation is over the stored span alone, so later
    messages fall outside it and the answer is `reason=receipted` with one
    `USAGE NOTE` naming `--resume` (rule 39) as the remedy. `USAGE NOTE` is
    this verb's one remedy line, on stdout, per SPEC.md's cap-and-count rule,
    and it is why the `receipt` row of **the largest plausible state** is 23
    lines and not 22. The completing write takes `fold.lock` exactly as the
    first one did and adds no file to rule 9's carve-out list: it writes the
    same `<out>/receipts/<day>.tsv.tmp` and renames it, which rule 9's
    2026-09-13 amendment already names.
    **Demanded test.** A session spanning midnight whose second day's rename is
    killed with SIGKILL: the receipts directory holds day one under id X and no
    day two, and `check` names neither a `DUPLICATE` nor a `SPLIT`; the next
    plain `receipt` over that session writes day two **under X**,
    `state=completed`, leaving day one byte-identical; a third run is
    `reason=receipted` with nothing written. A day-two cell hand-edited makes
    that third run `reason=differs day=<d>` with both files byte-identical
    afterwards — the red being a rule that rewrote the stored row. A session
    that gained messages after a complete receipt is `reason=receipted` with
    the `USAGE NOTE` naming `--resume`, and never `differs`.

### The receipts file

```
nova-tokens receipts v1 day=2026-09-13 at=2026-09-13T17:02:11Z build=<id>
date	model	repo	receipt	node	stage	who	session	started	ended	input	output	cache_write	cache_read	reasoning	day_basis	sources
2026-09-13	claude-opus-5	schema	3f9a1c0e7b2d4a6f8c1e2d3b4a5f6e7d	schema/fixed-tables/versioning/cpp	implementation	rowan	claude:studio:8b1e…	2026-09-13T14:02:10Z	2026-09-13T14:31:44Z	8410	59373	150439	23600235	-	utc	claude:studio
2026-09-13	mercury-2.5	schema	91c2d3e4f5a6b7c8d9e0f1a2b3c4d5e6	schema/fixed-tables/versioning/cpp	review	rowan	swarm:pool:j-4471	2026-09-13T14:40:01Z	2026-09-13T14:43:12Z	44609	7442	0	49108	4964	utc	swarm:pool
```

Seventeen columns, every one written on every row; the five types by rule 15;
`day_basis` by rule 17; `sources` the one label the receipt was read from;
`session` the label and the source's own session or job id. Rows are sorted
and unique by `(receipt, model, repo)`. The first line is the version and stamp
line of rule 12's shape **without `turns=`** (draft 2): a receipt is one
session's spend, and the turn count rule 12 puts on a day file is a fact about
a day. A receipt spanning two UTC days is rows in two files under one
id, and `cost` joins them by the id.

### Adoption: the matrix lives on `nova-update` (amendment note)

**The decision, and why.** Three homes were possible. `nova-tokens` owns
spend, and adoption is not spend: a line can run a tool a hundred times and
burn nothing this tool can see, so the axis is wrong. `nova-bus check` is a
**wall** over the bus's own integrity (SPEC.md), and a wall that also printed
a matrix would be a gate doing reporting, the two kinds this family keeps
apart. `nova-update` already holds the table the matrix extends: one row per
tool, an `owner` who answers when the row is not current, `check` for what is
installed, `report` for what a host says it runs, and a note to the bus
carrying `REPORT TOOL name= version=` lines per host
([SPEC-UPDATE](SPEC-UPDATE.md), the versions file and `report`). Installed is
the column it has; **used** is the column adoption adds, and "a tool is only
good if everybody adopts it" is the sentence that turns `nova-update`'s
inventory into a verdict. So the verb is `nova-update adoption`, this note is
its contract until it is folded into SPEC-UPDATE, and SPEC-UPDATE carries a
dated pointer here so the two cannot drift.

**The contract.**

```
nova-update adoption --bus <dir> --tools <file> --since <YYYY-MM-DD> [--tokens-out <dir>] [--reports <dir>] [--max <n>]
```

`--bus <dir>` is a bus checkout read as files, its roster
`<dir>/participants.json` the **lines** (never a compiled-in list); `--tools
<file>` is the caller's table, `name<TAB>first-tokens<TAB>questions`, one tool
per line, `first-tokens` the comma-joined first tokens of that tool's output
grammar (`TOKENS,REPORT,SUM,CHECK,SOURCES,PUBLISH,USAGE,COST,HOT,DIFF,
FRICTION,FRICTIONS` for this tool, `USAGE` being the `receipt` verb's token
since draft 2, and `REPORT`, `CHECK` and `COST` among them being inert by the
rule below rather than cut from it; `SEND,INBOX,RECEIPT,NAMES,BUS` for
nova-bus, `NAMES` being SPEC.md's fifth verb token and missing from draft 2's
example), and
`questions` the URL of the issue holding the four questions for that tool (our
#111 to #117 are an instance); `--since` bounds the evidence window, required,
no default. **A first token claimed by two tools in the file is inert, not a
refusal** (draft 3, correcting draft 2's exit 2, which this family's own specs
make unrunnable): a body line whose first token two tools claim is evidence for
**neither**, because one pasted line cannot say which tool was run, and the
collision is named once per run, not once per line —
`ADOPTION INERT token=<T> tools=<name,name>`, capped at `--max` with its own
MORE. On `origin/main` at 2026-09-13 a `--tools` file naming `nova-tokens`
beside its neighbours already collides three ways out of its twelve tokens:
`COST` is nova-swarm's (`COST TASK`, `COST OK`, SPEC-SWARM.md), `REPORT` is
nova-update's (`REPORT <OK|FAIL>`, SPEC-UPDATE.md), and `CHECK OK` is
nova-board's, nova-chat's and nova-sandbox's — so draft 2's rule refused the
only tools file anybody here would write, and the `RECEIPT` rename closed one
collision of four rather than the class. **Exit 2 is for the case that leaves a
tool with nothing to be seen by**: a tool **every one** of whose `first-tokens`
another tool in the file also claims has no verdict line of its own, and is
exit 2 naming the tool and its tokens, because a matrix that could only ever
say `unknown` about it is a matrix asked the wrong question; its repair is the
one `RECEIPT` had, a rename in that tool's own spec. `USAGE` itself is clean —
no spec on `origin/main` prints it as a first token — and `TOKENS`, `SUM`,
`SOURCES`, `PUBLISH`, `USAGE`, `HOT`, `DIFF`, `FRICTION` and `FRICTIONS` leave
nova-tokens nine unique tokens after the three collisions, so no tool in this
family is exit 2 today. A tool's **version line** is never inert: its first
token is the tool's own name (or, for `nova-sandbox`, its own event token), and
no two tools in one file may share a `name`.

The **evidence is an enumerated set**, each kind read from files, none of them
a person's word alone, and every kind decidable from the `--tools` file
itself — there is no such thing here as *a count-line token*, which named no
set and which two builds would enumerate differently (draft 2). A body line is
evidence only in one of two shapes. A **version line**, in the shapes SPEC.md's
Conventions and each tool's own spec fix, **enumerated here rather than assumed
to be one** (draft 3): **four tokens** whose first is exactly the tool's `name`
and whose rest is a build identity, `<goos>/<goarch>` and a `go` version, for
eleven of the thirteen binaries; **five**, the fifth `build=<12 hex>`, for
`nova-merge` (SPEC-MERGE.md: "`version` is the Conventions' line, plus this
binary's own `build=`"); and `SANDBOX VERSION …` for `nova-sandbox`, whose
version line carries the backend and the platform a sandbox is judged by and
whose first token is that binary's **event token and not its name** (SPEC.md,
Conventions). The third shape stays decidable from the `--tools` file alone: it
is a line whose first token is one of that tool's `first-tokens` and whose
second is exactly the word `VERSION`. Draft 2's four-token rule read a pasted
`nova-merge version` as `unknown` and could not have seen `nova-sandbox` at
all. Or a **verdict
line**, whose first token is one of that tool's `first-tokens` and
whose second is exactly `OK` or `FAIL`, nothing else.

| cell | evidence, from the record | never |
|---|---|---|
| `adopted` | a note in that line's lane, dated in the window, whose body carries **the tool's version line** in one of the three shapes above — four tokens, `nova-merge`'s five, or `nova-sandbox`'s `SANDBOX VERSION` (draft 3) — or one of the tool's enumerated **verdict lines** (`<T> OK` or `<T> FAIL` for a `<T>` among that tool's `first-tokens`); or, with `--tokens-out`, a day file row whose `sources` carries `bus:<line>` (the line ran `report`) or a receipt whose `who` is the line; or a `REPORT SENT` line naming the line as `--as` | a sentence saying "I use it"; a body line whose second token is none of `OK`, `FAIL` and (on a version line) `VERSION`; a first token two tools in the file claim, which is inert for both (draft 3) |
| `trying` | with `--reports`, a `REPORT TOOL name=<tool>` line from a `nova-update report` that line sent (its `as=`), dated in the window, and no `adopted` evidence | an install on another host counted for this line |
| `declined` | a note in the line's lane whose subject is exactly `adoption <tool> declined` — the one self-report the matrix accepts, because declining is the line's to say; the note id is printed | a silence |
| `unknown` | none of the above | a guess either way |

Every cell prints with the date and the note id or path of its evidence:
`ADOPTION CELL line=<name> tool=<name> state=<adopted|trying|declined|unknown>
at=<date|-> evidence=<note-id|path|->`. For every cell that is not `adopted`,
one `ADOPTION ASK line=<name> tool=<name> state=<s>: <questions URL>` — the
four questions of #117, linked, per line per tool, which is Glenn's "review
spec, review tool and give constructive feedback that would make adoption a
no brainer" (2026-09-12) as a line a reader can act on. Then `ADOPTION TOOL
tool=<name> adopted=<n> trying=<n> declined=<n> unknown=<n>` per tool and
`ADOPTION LINE line=<name> …` per line — **each one line per unit of state and
therefore capped like the others** (draft 3): they are bounded by the
`--tools` file and by the roster, which are two caller-supplied inputs and not
ceilings, and SPEC.md's rule is that every verb printing one line per unit of
state takes `--max` — and `ADOPTION OK|FAIL lines=<n>
tools=<n> cells=<n> adopted=<n> trying=<n> declined=<n> unknown=<n>
since=<date> at=<stamp> build=<id>`: `OK` and exit 0 only when every cell is
`adopted` or `declined`; otherwise `FAIL`, exit 1, because an unadopted tool
is the tool saying NO about itself, SPEC-UPDATE's own exit-1 meaning. Every
listing is capped at `--max` per kind — `CELL`, `ASK`, `TOOL`, `LINE` and
`INERT` (draft 3) — with a MORE line per kind, so the loud kind cannot eat the
quiet one. The
verb reads the bus as files, runs no `git` and sends nothing; a matrix that
fetched would be a matrix whose cells depend on a network call. **The evidence
is cheap to check, not proof** (draft 2): every shape above is text a person
could type, and this verb claims only that the line is there, dated, in that
lane, in a shape the tool prints — the version line at least names a build
that exists, and nothing here is an attestation. The first draft said a line
"cannot write it without running it", which is false of pasted text and was
the wrong claim to rest a verdict on. **What it
cannot see, said**: a line that runs a tool and never pastes a line of its
output is `unknown`, and `ADOPTION NOTE` says so on every run; the remedy is
the tool's own `--send`/`--note` path or one pasted line, and the note names
it. (Glenn, 2026-09-12 13:30Z; nova-tools #111 to #117; #182, the private-bin
adoption check, is `trying` evidence.)

**Demanded test (for SPEC-UPDATE's list).** A fixture bus with three lanes
and a tools file naming two tools: a lane holding a note with a `TOKENS OK`
line is `adopted` for that tool with the note's id, and so is a lane holding
only the four-token `nova-tokens <identity> <goos>/<goarch> <go version>`
line (draft 2); a lane whose note says
only "I use nova-tokens daily" is `unknown` and gets an `ASK`; a body line
whose second token is neither `OK` nor `FAIL` is no evidence, and a tools file
giving one first token to two tools prints one `ADOPTION INERT` naming both,
the run continuing, and a lane whose only evidence is that token is `unknown`
for both tools; a tools file in which **every** first token of one tool is
claimed by another is exit 2 naming that tool and its tokens; a lane holding
`nova-merge <identity> <goos>/<goarch> <go version> build=<12 hex>` is
`adopted` for nova-merge and one holding a `SANDBOX VERSION …` line is
`adopted` for nova-sandbox (draft 3); a lane with a
`REPORT TOOL name=nova-bus` line it sent and no use is `trying`; a subject
`adoption nova-bus declined` is `declined` with no `ASK`; a note dated before
`--since` counts for nothing; `--tokens-out` naming a directory whose day
file has `bus:emma` in `sources` makes emma `adopted` for nova-tokens;
`ADOPTION FAIL` exit 1 with one `unknown` cell and `ADOPTION OK` exit 0 when
every cell is adopted or declined; the fake `git` records nothing; `--since`
missing is exit 2.

## What it deliberately does not do

- **It does not price anything.** Tokens, by type, per model. Dollars are a
  rate card times a count, the rate card changes, and a tool that carried
  one would carry a stale one.
  [The proposed scheduling cost layer](PROPOSAL-SCHEDULING-COST.md) uses
  separately versioned, configured weights over retained usage. That is a
  future derived view, not a pricing claim about this shipped binary.
  - **Amendment, 2026-09-13 (rule 38).** It still carries no rate and chooses
    none. Given `--rates <file>`, the caller's dated table at list rate, it
    multiplies and prints `usd=`, `per_mtok=` and the unpriced remainder
    beside the tokens, because "you can't improve what you can't measure"
    (Glenn, 2026-09-12) and the mean cost per token is the number he asked
    to drive down. It never calls that number a bill, never knows what an
    account pays, and a rate it was not given is `unpriced`, never zero.
- **It does not resolve a work node.** A receipt's `node` and the `:usage`
  pointer nova-work stores are two strings that name each other; this tool
  reads no work set and nova-work reads no receipts file. `cost --node`
  matches the id exactly and infers no containment (2026-09-13, rule 33).
- **It does not decide adoption from a person's word.** The adoption matrix
  lives on `nova-update` (the amendment note, 2026-09-13), and its `adopted`
  cell is the tool's own version line or one of its enumerated verdict lines,
  a day-file source or a receipt — never a sentence, and never *a count-line
  token*, which named no set (draft 2).
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

**Rules 32 to 41** are in **Attempts joined to work, hot spots, frictions,
adoption** (2026-09-13; rules 39 to 41 draft 3), each demanded test written
beside its rule; these ten lines are the index.

32. Three fixture sessions receipted on one node at three stages, rows equal
    to `fold` over the lines whose `sessionId` is the id,
    `pointer=note:usage:<32 hex>`, a second receipt of
    one session refused naming the first, a midnight-spanning session in two
    files under one id, `--stage builder` exit 2, `nosession` and `unreadable`
    writing nothing, two parallel receipts serialized on `fold.lock`, an
    OpenCode child folded into its parent, an Agent child folded in by its
    parent's `sessionId` and a self-id'd one excluded, a swarm job's span from
    its usage file, and `note:usage:<id>` matching one of SPEC-WORK's six
    schemes while a bare `usage:<id>` matches none.
33. `cost --node` over three stages prints them in the fixed order and sums
    them, two `--node`s add, a prefix matches nothing, `--rates` prices only
    the named model with `priced_tokens + unpriced_tokens = tokens` and
    `per_mtok` recomputed, no `--rates` prints dashes, an all-dash receipt is
    `tokens=0 dashes=5`, and no flag on `cost` filters by stage.
34. A sixteen-column receipts file, two ids for one session, two `node` or
    two `stage` values under one receipt id, a receipt above the cell of a day
    file stamped after its session ended (while an earlier-stamped day file, or
    a missing day-file row, is one `CHECK NOTE` with `days=`, draft 3), a
    receipts file for a day the fold passed, and a stray under
    `receipts/` each print their line and exit 1; an equal or dashed cell is
    clean; a receipts file for a day newer than every day file, and any
    receipts file at all where there is no day file (draft 3), is one
    `CHECK NOTE` and exit 0; `TOKENS DAY receipts=` equals the file's rows;
    `sum` opens nothing under `receipts/`.
35. Twelve fixture steps rank two repeats and one 40 KB step in the stated
    order, `--top 1` prints one of each with a MORE per kind, `HOT OK` token
    counts equal `fold`'s, `HOT NOTE` is one line under `oneline.TailBytes`
    beginning with the fixed phrase `cannot see: bytes are not tokens`, a
    reference result is unranked and counted in `unranked=`, a NULL
    `state.output` is `bytes=-`, a reclaimed swarm job is `reason=reclaimed`
    with only its directory opened, `--top 0` prints all with no MORE and
    `--top -1` is exit 2; and the Claude reader's `tool_result` change leaves
    every fold count byte-identical.
36. Two day files with one changed cell, one added row and one dash-to-number
    print three ROWs in delta order — the added row being **one** ROW with
    `type=-`, `from=absent` and `delta=-`, not five (draft 2) — identical files print none, `--max 1` prints one and a MORE, a
    missing side is exit 2, two sessions differing in `repeated=` print that
    field, and nothing beyond the two named files is opened.
37. `friction add` from a receipt equals the receipt's sum and span with
    `rough=0`, `--tokens ~5000 --wall 40m` is `rough=1`, a bare `--tokens` and
    a missing `--wall` are exit 2, a bad `--gap` is exit 2, a corrupt file is
    exit 2 and untouched, two concurrent adds both land, `frictions` orders
    gaps by tokens and honours `--since`, prices only through `--rates --out`
    with `--rates` alone exit 2, and `publish --frictions`
    lands, appends as `state=appended` without `--supersede` with `appends=`
    in the message, and refuses a dropped row; and
    `--frictions` with the other kinds is exit 2.
38. `sum --rates` prices every cell with both a number and a rate, `usd=`
    equals the hand sum, `unpriced_tokens=` is the third model plus the dashed
    type, `per_mtok=` is `usd` over `priced_tokens` and `-` at zero — as `usd=` is
    at `priced_tokens=0`, never `0` — an
    all-zero row prices to zero, malformed and duplicate lines are exit 2,
    `verified=` is the oldest date among the rows priced with, `rates=` and
    `verified=` print once per run, a `COST STAGE`, `COST MODEL`,
    `COST RECEIPT` and `FRICTIONS GAP` that priced nothing each print `usd=-`
    and carry no `rates=`, `verified=` or `unpriced_tokens=` (draft 3), and
    `internal/tokens/rates.go` holds no
    float literal but 0 and 1 and no model-name string.
39. A six-message receipt then a `--resume` over the four appended messages:
    two receipts, `state=resumed`, sums equal to `fold` over the whole session,
    the message at the cursor in the first receipt and not the second,
    `--resume` with no earlier receipt exit 2, `--resume` with nothing after
    the cursor exit 1 `reason=nonew`, `check` clean over disjoint spans and
    `CHECK DUPLICATE` over overlapping ones (draft 3).
40. One session receipted as `implementation` on `n1` and resumed as `review`
    on `n2`: neither `cost --node` counts the other's messages, the two
    `tokens=` add to `fold`'s, `wall=` over both nodes is the two spans summed
    and strictly less than the session's own span, `COST NOTE` carries the
    caller's-attribution sentence, and no code path divides a receipt's counts
    (draft 3).
41. A midnight-spanning receipt killed between its two renames: `check` names
    no `DUPLICATE` and no `SPLIT`, the next plain `receipt` writes the missing
    day under the **stored** id with `state=completed`, a third run is
    `reason=receipted`, a hand-edited stored cell makes it
    `reason=differs day=<d>` with nothing written, and a session that grew
    after a complete receipt is `reason=receipted` with the `USAGE NOTE`
    naming `--resume` (draft 3).

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

16. **`internal/tokens/receipt.go`** (2026-09-13, rules 32, 34): the drawn
    id (`crypto/rand`, thirty-two hex), the one-session selection over each
    reader (a transcript by session id, an OpenCode session with its children,
    a swarm job by id), the receipts file with its seventeen columns and
    version line, the whole-file write under `fold.lock`, the `receipted`
    check by `session`, and `check`'s four joins — including the constancy of
    `node`, `stage`, `who` and `session` under one receipt id, and the
    newest-day-file rule that makes an unfolded day a `CHECK NOTE` rather than
    an orphan (draft 2), which now covers a receipts directory with no day file
    at all (draft 3). **Draft 3 adds the three the span rules need**: the
    resume cursor (the greatest `ended` of that session's receipts, strictly
    after, drawn as a new id), the completion of a receipt whose multi-day
    write was interrupted (re-derive the stored span under the stored id, write
    only the absent days, `differs` on a stored row this run cannot reproduce),
    and the narrowed `DUPLICATE` join over overlapping spans. Tests: demanded
    tests 32, 34, 39, 40 and 41.
17. **`internal/tokens/rates.go`** (2026-09-13, rule 38): the rates file
    parser, the per-cell price with `unpriced` accounting, `per_mtok` with
    no division by zero, `verified=` oldest; used by `sum`, `cost` and
    `frictions` through one function, with `rates=`/`verified=` printed once
    per run on the count line and `verified=` computed over the rows used
    (draft 2). Tests: demanded test 38; a source test finds no float literal
    but 0 and 1 and no model-name string in this file (draft 2).
18. **`internal/tokens/cost.go`** (2026-09-13, rule 33): the receipts walk,
    exact node match, the six-stage order, per-model and per-receipt caps,
    `wall=` as a sum. Tests: demanded test 33.
19. **`internal/tokens/hot.go`** (2026-09-13, rule 35): steps from
    `tool_use`/`tool_result` pairs — the transcript reader's demanded change
    is in **`### --claude`**, with its own fold-unchanged test (draft 2), not
    in this list — from OpenCode
    `part` rows with `state.output`, and from a swarm job's data home while
    it exists; the two rankings; the one-line NOTE with the fixed cannot-see
    phrase. Tests: demanded test 35.
20. **`internal/tokens/diff.go`** (2026-09-13, rule 36): the day-file union
    by `(model, repo, type)`, absent and dash handling, the session form over
    `hot`'s counts. Tests: demanded test 36.
21. **`internal/tokens/friction.go`** and the `publish --frictions` kind
    (2026-09-13, rule 37): the frictions file, the `~` rule, `<file>.lock`,
    the per-gap sum with its `--rates --out` join through the receipts
    (draft 2), and in `publish.go` the third kind with its append
    transition, its `appends=` message field and its refusals. Tests: demanded test 37; the append
    transition red first against a rule that called every changed frictions
    file `differs`.
22. **`cmd/nova-tokens/main.go`** gains the six verbs, `--rates` on `sum`,
    `--top`, `--resume` on `receipt` (draft 3), and the `--frictions` kind on
    `publish`; `docs/CLI.md`'s
    `### First run` gains one receipt over the fixture transcript and one
    `cost --node` over it, every path a flag.
23. **`nova-update adoption`** (2026-09-13, the amendment note) is
    SPEC-UPDATE's work item, drafted there when this amendment is ratified;
    its contract and demanded test are in the note above and move whole.

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

## Ideas folded on 2026-09-13

| source | the idea, in six words | disposition |
|---|---|---|
| Emma, emma-0fd8c03d5f24 | `hot --session --top 5`, one paragraph | rule 35: two ranked listings in the grammar and one `HOT NOTE` line that is the paragraph, with what it cannot see |
| Stella, 15:30Z | count review and repair too | rules 32, 33: `stage` from the six names, `cost --node` sums every stage and has no stage filter |
| SPEC-WORK draft 12, `:attempt :usage` (#181) | the attempt points at a token record | rule 32: `note:usage:<receipt-id>` — SPEC-WORK's sixth scheme, accepted on an attempt's `:usage` today and barred from `:done` evidence, which is where a spend record belongs — receipt drawn first, the attempt names it, the receipt names the node; a bare `usage:` scheme would be a separate SPEC-WORK amendment and nothing here waits on it (draft 2) |
| Glenn via Stella, 16:34Z | lower the mean AND the total | rules 33, 38: `tokens=`, `usd=`, `per_mtok=`, `priced_tokens=`, `unpriced_tokens=` on one line |
| Glenn, 2026-09-12, nova-tools #175 | maintain the per-model rate table | rule 38: `--rates <file>`, the caller's, dated, list rate, nothing compiled in |
| Glenn, 2026-09-11, crystallize | see the pattern, then the tool | rule 37: `friction add`, `frictions` per gap, tokens descending |
| Glenn, 2026-09-11, ideas are issues | never IDEAS.md | rule 37: a friction row carries `issue=`, no owner, no deadline; the board holds the obligation |
| Glenn, 2026-09-12, adoption | a tool is good if everybody adopts it | the amendment note: `nova-update adoption`, evidence from the record, `ASK` lines linking the four questions |
| nova-tools #182 | a new parent ran an old child | `trying` evidence for the matrix; the version lines are `nova-update report`'s |
| PROPOSAL-SCHEDULING-COST | stages; report the denominator; no division by zero | taken whole: the six stage names, `priced_tokens=` beside `per_mtok=`, `-` at zero |
| Stella, draft 1 read (5655262698) | resumable event-level retention within a session | rule 39 (draft 3): `--resume`, the cursor the stored `ended`, a new id per span, `CHECK DUPLICATE` narrowed to overlapping spans |
| Stella, draft 1 read (5655262698) | a session is not one task or stage | rule 40 (draft 3): the span is the unit, spans are disjoint, `wall=` stays a sum, and an unsplittable span is the caller's attribution and says so |
| Stella, draft 1 read (5655262698) | restart recovery after a partial write | rule 41 (draft 3): re-derive the stored span under the stored id, write the absent days, `state=completed`; `differs` rather than a rewrite |
| Rowan, window tokens are turns | tool output read back is the cost | rule 35: `HOT OK` prints `result_bytes=` beside `output=` |
