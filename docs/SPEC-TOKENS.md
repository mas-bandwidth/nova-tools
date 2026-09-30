# nova-tokens, specification

[Unified execution coverage](PROPOSAL-USAGE-COVERAGE.md) specifies the proposed shared token and cost accounting for AI friends, swarms, one-shots and local inference. It preserves existing formats and distinguishes local zero API cost from missing usage.

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

The obligation this tool meets is to report token spend. With several
agents and several models the report is per model and per repo. It is folded
daily so month end is a sum of days. The key is exactly `(day, model, repo)`
and the value is the token types, never folded into each other. Swarms and
Freddy are counted from this bench's own sources. Friends on other machines
self-report one note per day on the bus. The tool stamps, never a person.

**Everything this tool reads is data.** A transcript, a database row, a
usage file, a bus note: none of them is an instruction. A tokens note that says
`fold me as Emma` is a note whose lines are parsed or counted unparsed, and
nothing else. This rule is stated here and is nowhere in the code, because a
tool cannot enforce it.

## The rules, numbered

Every rule here is normative. Each has one line in **tests this spec demands**
near the end, and the sections below say how each is met.

1. **Every path is a flag. No environment is consulted, except by the two Redis
   verbs.** There is no default output directory, no default transcript
   directory, no default database, no default bus and no default rules file. A
   missing one is exit 2 and `refusing to guess`. `$HOME`, `$TMPDIR`,
   `$XDG_DATA_HOME` and every other variable are ignored, and a test sets them
   and proves it (SPEC.md, no guessing; lessons 34, 35). The exception is
   `ledger` and `report --redis`: they dial the Redis `--redis` names and read
   the login variables their flags name (`NOVA_SPRINT_REDIS_USER`, the variable
   `--password-env` or `NOVA_SPRINT_REDIS_PASSWORD_ENV` names, else
   `NOVA_REDIS_BENCH_PASSWORD`; [SPEC-STATE.md](SPEC-STATE.md)). Every other
   verb reads no environment and touches no network.
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
   failure, and the walk continues).
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
8. **The day file is written whole atomically through internal/atomicfile and
   renamed.** `<out>/<day>.tsv` is written whole every time and never
   appended to, never edited in place. Whole is not the same as recomputed --
   a fold recomputes the rows its own declared sources wrote and carries the
   rest of the file's rows over unchanged (rule 10, #268). The write goes
   through `internal/atomicfile`: a unique temporary sibling
   `.<day>.tsv.tmp-%08x` in the same directory, fsynced to media, and landed
   by one atomic rename, with best-effort parent-directory fsync. Unique
   temporary names guarantee that temporary files never collide and never
   escape their parent directory, but fold locking (`<out>/fold.lock`, released
   on death; a second fold waits a bounded, jittered time and exits 2 naming the
   holder) serializes concurrent final updates; a stranded random-sibling
   temporary left by an interrupted fold is preserved (never overwritten or
   removed on retry), and `check` steps over valid day-file temporaries while
   flagging unrelated temporaries as strays (lessons 53, 54, 67).
   Before opening the fold lock or writing a day file, the writer refuses an
   output directory that is itself a symlink or a child of a symlink directory,
   including a spelling with a trailing separator or `/.`. It leaves the link
   and its target unchanged. The `fold.lock` file itself must be regular:
   a symlink (including a dangling one) or other file type is refused before
   the lock writes a PID.
9. **One file per day. A month is a sum of day files. The tool removes
   nothing.** There is no month file. `sum` reads day files and writes
   nothing. No verb deletes, truncates or trims any file, including any log.
   The exception is a file THIS RUN makes: the fold's own `fold.lock`, the copy under `--scratch`, the temporary file
   `internal/atomicfile` writes through before rename (removed on failure or
   cleanup), the report's and the ledgers' own `.tmp` files, and, on a platform with no flock, the lock sentinel the release
   removes. A file the tool was given is
   never one of them, and the tripwire that enforces this searches for every
   call that can empty a file -- `os.Remove`, `os.RemoveAll`, `os.Truncate`,
   `.Truncate(`, `os.Create(`, `os.WriteFile(`, `os.O_TRUNC` (the flag that
   empties the file an `os.OpenFile` opens), `syscall.Unlink(` (the syscall
   that unlinks a directory entry) -- in every package compiled into the
   binary, carving out by file and by call, with the reason, each site that
   empties only a file its own run made (the list is the tripwire's own, in
   `cmd/nova-tokens/contract_test.go`), and failing when a carve-out has gone
   stale. Because it scans every compiled package, the list also holds sites in
   code no verb of this binary reaches; a carve-out is a statement about a
   file, never a verb.
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
    must never quietly lower a day's spend.
    The comparison is with the MERGED file, because the fold merges by
    source: this run's rows replace the rows its own declared sources wrote,
    a row no declared source wrote is retained exactly as it is, and a row
    this fold can neither retain nor recompute -- one already summed over a
    declared and an undeclared source, or a retained row colliding with a
    recomputed one on (model, repo) -- is `TOKENS PARTIAL`, the file is left
    as it was, and the run exits 1. `--allow-shrink` does not write it: it is
    a person's word about a day going backwards, not about a row nothing on
    disk can take apart. An existing day file that carries malformed rows or
    findings fails closed before replacement: the fold refuses the day,
    reports `TOKENS UNREADABLE label=out ...` with the finding reason, leaves
    the raw file on disk untouched, and exits 1. An explicitly selected day
    whose declared source becomes empty reconciles against the existing day
    file on disk, detecting the shrink rather than silently skipping. A
    source that cannot be declared again leaves its rows retained; the escape
    is folding the day into its own `--out`. When any row is retained the version line carries
    `turns=-`, because turns counts the messages this
    run read and cannot be split per source. A totals comparison alone cannot
    catch a fold that drops another source's row, because the new numbers can
    be bigger; the merge by source is what keeps that row.
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
    a person reads: a turn's cost is its whole context, so tokens over turns
    is the number that explains a day.
    **`turns=` is labelled by its scope**: it counts eligible assistant
    messages across the declared message-counting sources, and the
    `sources=` on the same line is that scope; it is not, by itself, the
    coordinator's task turns or anybody's decisions, and a comparison that
    quotes it quotes `sources=` and the denominator — messages counted, over
    which sources, for which day — beside it. `TOKENS FOLD`, `SUM MONTH` and `CHECK OK` carry
    `at=<stamp> build=<id>`. No flag sets the stamp, and a day file whose
    first line lacks it is a `check` failure. The stamp is when the tool
    computed the file; the `date` column is the UTC day of the message.
13. **`check` is the gate.** It verifies every day file under `--out` parses,
    every row has all eleven columns with each of the five type cells either
    a non-negative integer or exactly `-`, never empty, `day_basis` either
    `utc` or a zone name with no whitespace, the version line carries
    `turns=` as an integer or `-`, the `date` column equals the
    file name, and rows are sorted and unique by `(model, repo)`. A missing day
    is `CHECK MISSING date=<d>`, named, never filled. `check` exits 1 on any
    finding and prints the count line either way. Never gate on `sum` or
    `sources`; `check` is the gate.

    **A GATE THAT CANNOT GO GREEN IS NOT A GATE.** A calendar day nobody worked
    is not a missing day, and a README, a log or an archive beside the day files
    is not a stray; a gate that names them on every run is a line people skip.
    So a calendar day between the first and the last with no file is `gap=<n>` on the
    `CHECK` line, and it is `missing` only when something this run can read says
    there was spend on it: `--strict` names every gap, and `--no-spend <file>`
    (one `YYYY-MM-DD` per line, the days that had none) names the gaps the list
    does not account for. A `*.md`, a `*.log` or a `pre-*` archive directory
    beside the day files is `notes=<n>` rather than a stray, and `--strict`
    names those too. **Both counts print on the OK line**, so nothing was hidden
    to make it green, and the two flags are one question with two answers —
    giving both is a refusal. `check` still removes nothing.

    **`--through <YYYY-MM-DD>` gates freshness.** When `--through` is given,
    `check` verifies that the last folded day under `--out` is at least the given
    day. If the directory has no folded days or its last day is older than the
    requested day, `check` prints `CHECK FAIL stale last=<d> through=<d>` on
    standard error, marks the check failed, and exits 1.
14. **A swarm pool's usage files are a source.** A pool holds one usage file
    per job attempt, `<pool>/usage/<job>.tsv`, outside the job directories, so
    a job's usage is still readable after its directory is gone. `--swarm <label>=<pool>`
    reads every file under `<pool>/usage/` and nothing under `done/`,
    `failed/` or `running/`; it folds `model`, `repo`, `tokens_in`,
    `tokens_out`, `cache_write`, `cache_read` and `reasoning` from the row,
    dates the row by its `ended` stamp, and takes the job id from `job`. A
    job directory under `done/` or `failed/` with no usage file is
    `nousage=<n>` on the source line and not folded, and a file whose header
    is not the sixteen usage columns in order is refused by name. All five
    types are present; a cell the usage file holds as `-` (a field the
    provider did not report is a dash, never a zero) stays `-` here and
   is never summed as zero, and a row for a second attempt (`attempt=2`) is
   its own row, because the pool keeps one file per attempt. Two rows
   for one job are duplicates only when they repeat the same `attempt`; a
   failed attempt followed by a retry retains both costs.
15. **The five types are kept apart, a type the source did not report is a
    dash, and the key is exactly `(day, model, repo)`.** `input`, `output`,
    `cache_write`, `cache_read`, `reasoning`, each written as the source
    reports it. A type the source did not report is `-` in the cell, never
    `0`, across every source: a transcript whose usage block has no key for
    it, a swarm usage file with no such column or a `-` in it, a bus note with no line
    for that `(model, repo, type)`, an export with no such column. `0` is
    written only when the source reported zero. A provider not exposing
    reasoning or cache usage is not proof that none occurred (lesson 110), and a zero that means "not measured" would sum
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
19. **Every subprocess runs under a timeout, and there is one.** `sqlite3`
    runs under `--timeout <seconds>`, default 120, and a run that exceeds it
    is `TOKENS UNREADABLE` for that source with the timeout named. No other
    subprocess exists: the order of competing tokens notes is explicit in the
    notes themselves (`supersedes=`, the bus source), never a checkout's
    history, so the tool runs no `git` at all and rule 16 holds without
    exception. The default is allowed
    for the reason SPEC.md gives `nova-bus --git-timeout`: it is how long the
    tool waits before saying so, not a fact about anybody's data.

20. **A friend on another machine runs `report`, and never types a number.**
    The first user of this tool is not this bench: it is Emma, Johnny or
    Stella on a harness of their own. `report` folds that machine's own
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
    After the body lines and before `REPORT OK`, a `report` prints, on
    **stderr**, one `TOKENS AVG` line per model: the day's blended cost per
    token, summed over every repo that model wrote that day. `model=` is
    `provider/model` where a source named a provider and the bare model name
    where none did; `tokens=` is input, output, cache write and cache read
    summed (reasoning is its own column and is not in the ratio's
    denominator); `usd=` is the model's cost, in dollars, from the usage
    `usd` column or a cost tick the source reported, and is `0` where no
    source reported one; `usd_per_mtok=` is that cost over those tokens,
    dollars per million tokens to four decimals. The lines are sorted by
    `usd_per_mtok` descending, capped at `--max` like every other listing,
    and a model whose `tokens=` is zero still prints one line with
    `usd_per_mtok=-`: there is no average over nothing, so the ratio is
    never divided. The one `TOKENS AVG-ALL` line is the same four fields
    summed over every model.

21. **A harness that shows nothing is counted from the provider's side, and
    never apportioned.** Emma's harness (Antigravity, Gemini) and Johnny's
    (Grok) record no token counts anywhere a tool can read. For them the source is `--provider
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
                    [--claude <label>=<dir>]... [--opencode <label>=<file>]... [--swarm <label>=<pool>]... [--bus <dir>]
                    [--provider <kind>:<label>=<file>]... [--scratch <dir>] [--timeout <seconds>] [--allow-shrink] [--max <n>]
nova-tokens report  --who <name> --day <YYYY-MM-DD> --repos <file>
                    [--claude <label>=<dir>]... [--opencode <label>=<file>]... [--provider <kind>:<label>=<file>]...
                    [--supersedes <note-id>]... [--note <path>] [--scratch <dir>] [--timeout <seconds>]
nova-tokens report  --ledger <file.tsv> --month <YYYY-MM> [--by model|repo|day] [--max <n>]
nova-tokens report  --redis <host:port> --month <YYYY-MM> [--by model|repo|day|tuple] [--max <n>]
                    [--user <name>] [--password-env <NAME>]
nova-tokens ledger  --out <dir> (--day <YYYY-MM-DD> | --month <YYYY-MM>) --redis <host:port>
                    [--user <name>] [--password-env <NAME>]
nova-tokens sum     --out <dir> --month <YYYY-MM> [--max <n>]
nova-tokens sum     --swarm-root <dir> --day <YYYY-MM-DD> --out <ledger.tsv>
nova-tokens check   --out <dir> [--strict | --no-spend <file>] [--through <YYYY-MM-DD>] [--max <n>]
nova-tokens sources --repos <file> (--day <YYYY-MM-DD> | --all)
                    [--claude <label>=<dir>]... [--opencode <label>=<file>]... [--swarm <label>=<dir>]... [--bus <dir>]
                    [--provider <label>=<file>]...
                    [--scratch <dir>] [--timeout <seconds>] [--max <n>]
nova-tokens profiles --swarm-root <dir>
nova-tokens session --claude-session <jsonl> [--out <dir>] [--day <YYYY-MM-DD>]
nova-tokens fold-pool --pool <dir> --ledger <file> [--since <stamp>]
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

`--units <set.lisp>` names a **work set**, and is how a row is attributed to a
PIECE OF WORK rather than to a repo. The repo column answers "what did this
month cost on nova-tools"; the obligation is the other question — what did
THIS piece of work cost — and a work set already names the pieces, so the tool
reads the coordinator's own taxonomy instead of inventing one. One line says
what was loaded: `TOKENS UNITS set=<id> units=<n> file=<file>`.

The rule, once, and it is deliberately coarser than the repo rule: **a unit is
attributed per TRANSCRIPT**, not per message. A repo is per message because one
window touches three repos in an hour; a child is spawned for one unit and
works on it until it stops, and attributing per message would put a child's
`gh pr view` of a sibling's PR onto the sibling's unit. Within one transcript:
take every tool-call input in order; the first token that names a unit decides
the file, and every message in it carries that unit; a file that names none is
`-`. A token names a unit when it carries that unit's `:pr` number (`#1412`,
`/pull/1412`), its `:branch`, or its `:lane`'s clone directory (`lane-<name>`
as `tmp/lane-three/` or `~/lane-three`). Each of the three is **bounded** —
`#141` does not match inside `#1412`, and the branch `rowan/x` does not match
inside `rowan/xylem` — because an unbounded substring would put one lane's
spend on another's unit and nobody would see it.

Only the Claude reader attributes units: a billing export, a swarm usage file
and a bus self-report carry no tool inputs to read one from, and their rows are
`-`, which is the truthful answer rather than a gap. A fold with no `--units`
puts every row on `-`, which is one unit value, so it splits no
`(model, repo)` row.

### `sum`

Asserts nothing. Reads `<out>/<month>-??.tsv`, prints per `(model, repo)`,
per model and the total, all five types, the rough count, per type column how
many rows added were `-` (`dashes=`, rule 15), how many rows added were not
UTC (`nonutc=`, rule 17), and the days it found and the days missing between
the first and last. A `-` adds nothing and is counted; it is never read as
zero. Writes nothing. Exits
0 whenever it ran, including over a month with gaps: answering is its job,
and `missing=<n>` is the answer. `sum` is a **report**. Never gate on it.

`--by unit` prints the **units table** instead of the two `(model, repo)`
tables: one `SUM UNIT` line per unit the month's rows named, `-` among them,
heaviest first. The `-` group is printed and never hidden — the share of a
month nobody attributed is the number that says whether the work set is good
enough, and it is the same reasoning as `unknown=` and `other=` on a fold's
day line. `SUM TOTAL` and `SUM OK` carry `units=<n>` whichever table was
printed.

`sum --swarm-root <dir> --day <d> --out <ledger.tsv>` is the one form that
writes: it walks every card's `usage.tsv` under `<dir>/*/jobs/*/`, keeps the
rows whose `started` stamp is on `--day`, and appends one row per `(model, repo)`
pair to the ledger in the ledger's own column order — `day`, `model`, `repo`,
`tokens_in`, `tokens_out`, `usd`, `cards`, `completed`, `usd_per_task`, `dashes` —
reading the header and refusing (exit 2) when it
differs, so a ledger filled by hand and one filled by this verb agree. A second
run for the same day replaces that day's rows, never doubling, so the ledger can
be filled again and again; a kept field a card did not report is `-` in the ledger, never 0,
and the trailing `dashes` column counts how many cards left each of input, output, usd and rc unknown.

A receipt that carries a `tool` column names the nova tool whose work the card
is, and `sum --swarm-root` counts those receipts per tool and prints one `TOOLS`
line after `SUM OK`, a `tool:n` per named tool in sorted order —

```
SUM OK day=<d> models=<n> cards=<n> in=<n> out=<n> usd=<x.xxxx>
TOOLS <tool>:<n>,<tool>:<n>,…
```

— so a tool nobody used in the day has no name on the line and is visible by its
absence. A receipt with no `tool` column, or a `-`, names no tool and is not
counted; when no receipt names a tool, no `TOOLS` line prints.

**Cost per completed task per (model, repo).** The routing metric is **cost
per completed task**, not price per token: a model at four times the
per-token price that finishes in a third of the turns is the cheaper model,
and it is the number that decides which seat a job goes to. The row key is
`(day, model, repo)`, because the same model costs differently against a
small tool repo and a large one. Three columns carry it on every row:

- `repo`, the repo the card's receipt names, so the row is per `(model,
  repo)`;
- `completed`, how many of that row's cards carry a receipt with `rc=0`, a
  task that finished, read from the receipt and never inferred from a card
  that reported none;
- `usd_per_task`, `usd / completed` in dollars to six decimals, and `-` when
  `completed` is zero: there is no cost over no finished task, and the ratio
  is never divided.

The column order is `day`, `model`, `repo`, `tokens_in`, `tokens_out`, `usd`,
`cards`, `completed`, `usd_per_task`, `dashes`: the ledger lands one row per
`(model, repo)` pair, as `sum` prints one `SUM PAIR` line per pair. `cards` is the count
of every card that reported, and `completed` is its own column,
not `cards` minus anything. A receipt that names no `repo` counts under
`unattributed`, so a receipt with no repo is visible rather than guessed, and
the header is the order above. A receipt whose `rc`
is `-` or empty names no completion, counts in `cards` and as the fourth count
of the `dashes` column, and never in `completed`: it is read as neither failed
nor finished.
A `completed` of zero is a valid row, never a refusal; the one refusal of
this form is the header mismatch, which names the wanted order, the order
above.

### `check`

Asserts what rule 13 says. Says NO (exit 1) on any malformed file, any
malformed row, any missing day, any stray file, or when `--through <day>` is
given and the last folded day is older than `<day>` (`CHECK FAIL stale`).
Deliberately does not check: whether a day's numbers are plausible, or whether a
source was declared that day. `check` is a **wall**, and it is the gate.

### `sources`

Asserts nothing. Reads the declared sources exactly as `fold` does, through
the same code (a second reader would drift), prints one `TOKENS SOURCE` line
per source with what it yielded, and writes nothing. Unreadable files are
printed and counted here too. Exits 0 whenever it ran. `sources` is a
**report**; it exists so a person can see what a fold would count before it
writes.

### `profiles`, `session`, `fold-pool`

`profiles --swarm-root <dir>` is a measurement over a swarm root: one
`PROFILES MODEL` line per model (card count, median `tokens_out`, overshoot
cards whose output exceeded their own card budget line) and one `PROFILES OK`
line, exit 0 whenever it ran. `session --claude-session <jsonl>` prints one
`SESSION` line (the weighted fresh-input equivalent and average context) and,
with `--out`, folds the coordinator's turns into the day file as one row per
model the transcript names, `<model>/coordinator`, beside retained rows; a
transcript that names no model on some turn is refused, never booked under a
guess. `fold-pool --pool <dir>
--ledger <file>` folds a pool's `usage/*.tsv` into the monthly ledger and
prints one `FOLD OK` line. Their lines are in the output grammar; a scanner
that reads the grammar parses them.

`report --ledger <file.tsv> --month <YYYY-MM> [--by model|repo|day] [--max <n>]`
sums one month of the pool ledger `fold-pool` writes (header `day, provider,
model, repo, tasks, tokens_in, tokens_out, cache_write, cache_read, reasoning,
usd, source`) by the `--by` group, default `model`: one `REPORT model=|repo=|day=<g>
tasks=<n> in=<n> out=<n> cache_read=<n> usd=<usd>` line per group, capped at
`--max`, then `REPORT OK month=<month> groups=<n> rows=<n> usd=<usd>`, exit 0. A
missing `--ledger` or `--month`, a malformed month, a `--by` outside the three,
a ledger that is a directory or does not read, and `--ledger` given with
`--redis` are refusals, exit 2. `report --redis` and `ledger` are the Redis
token ledger's verbs, specified in [SPEC-STATE.md](SPEC-STATE.md).

`fold-pool` folds a pool's `usage/*.tsv` rows into the monthly ledger, summed by (day of `started`, `provider`, `model`, `repo`) and upserted into rows `day, provider, model, repo, tasks, tokens_in, tokens_out, cache_write, cache_read, reasoning, usd, source=pool`, replacing any existing row for the same key so a second run leaves the ledger byte-identical; a token cell with no reported input stays `-`, a `usd` of `-` on any input makes the row's `usd` `-`, and an optional `--since <RFC 3339 stamp>` folds only rows at or after it. A pool that cannot be read is `FOLD REFUSED` naming `--pool`, and a malformed `--since` is `FOLD REFUSED` naming the stamp: a valid stamp is never blamed for an unreadable pool.

## Exit codes

| code | meaning |
|------|---------|
| 0 | the verb ran and passed: every source read, every line parsed, every day written; a sum or a listing printed; a check with nothing to name |
| 1 | the verb ran and said **NO**: a declared source with an unreadable file, an unparsed bus line or note, a row of two day bases, a lane-day with competing reports (`TOKENS CONFLICT`), a day that would shrink, a check finding, a `report` with nothing to show |
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
`MIXED`, `SHRANK`, `QUIET`, `MISSING` and refusals go to stderr.
`report` is the one
exception, stated in rule 20: its stdout is exactly rule 6's body lines, and
`REPORT OK`, `REPORT FAIL` and its `TOKENS UNREADABLE` lines go to stderr. Every path, label, model name,
repo name, note id and reason renders through `internal/oneline`; every
`key=value` carrying stored text is one token via `oneline.Field`; the tail
after `: ` is capped at `oneline.TailBytes`.

```
TOKENS FOLD at=<stamp> build=<id> out=<dir> sources=<n> days=<all|d> repos=<file>
TOKENS UNITS set=<id|-> units=<n> file=<file>
TOKENS SOURCE label=<label> kind=<claude|opencode|swarm|bus|provider> path=<path> reports=<types> day_basis=<utc|mixed|<zone>> files=<n> unreadable=<n> messages=<n> dup=<n> noid=<n> nousage=<n> unparsed=<n> comments=<n> redated=<n> superseded=<n> rows=<n>
TOKENS UNREADABLE label=<label> path=<path>: <why>
TOKENS UNPARSED label=<kind>:<name> note=<id> line=<n>: <text or why>
TOKENS SUPERSEDED label=bus:<name> note=<id> by=<id> day=<d>
TOKENS CONFLICT label=bus:<name> day=<d> notes=<id,id,…>: competing reports; send a correction whose subject carries supersedes=<id>
TOKENS TOUCHED label=bus:<name> day=<d> repos=<list>
TOKENS MIXED date=<d> model=<model> repo=<repo> bases=<utc,zone>: two day bases on one row; declare one export for that day
TOKENS DAY date=<d> rows=<n> models=<n> repos=<n> turns=<n|-> unknown=<pct>% other=<pct>% rough=<n> dashes=<n> nonutc=<n> sources=<labels> written=<true|false>
TOKENS SHRANK date=<d> type=<type> file=<n> now=<n|-> written=<true|false>: a source went quiet; --allow-shrink writes it anyway
TOKENS QUIET label=<label> day=<d>: a declared source has zero samples for an explicitly selected existing day
TOKENS PARTIAL date=<d> model=<model> repo=<repo> sources=<labels> folded=<labels> written=<true|false>: this fold declared only some of the sources that wrote the row; declare every source in the file's sources= line, or fold this day into its own --out
TOKENS MORE kind=<source|unreadable|unparsed|superseded|conflict|touched|mixed|day|partial|quiet> shown=<n> total=<t> <remedy>
TOKENS OK days=<n> rows=<n> sources=<n> unreadable=<n> unparsed=<n> mixed=<n> conflict=<n> shrank=<n> partial=<n> quiet=<n>
TOKENS FAIL days=<n> rows=<n> sources=<n> unreadable=<n> unparsed=<n> mixed=<n> conflict=<n> shrank=<n> partial=<n> quiet=<n>
TOKENS NOTE <the one remedy line>
TOKENS REFUSED: <reason>
REPORT OK who=<name> day=<d> rows=<n> at=<stamp> build=<id> subject=<subject>
REPORT FAIL who=<name> day=<d> rows=<n> unreadable=<n>
REPORT REFUSED: <reason>
TOKENS AVG day=<d> model=<provider/model> tokens=<n> usd=<n> usd_per_mtok=<n|->
TOKENS AVG-ALL day=<d> tokens=<n> usd=<n> usd_per_mtok=<n|->
SUM MONTH month=<m> at=<stamp> build=<id> days=<n> first=<d> last=<d> missing=<n> rows=<n> turns=<n|->
SUM PAIR model=<model> repo=<repo> input=<n> output=<n> cache_write=<n> cache_read=<n> reasoning=<n> rough=<n> dashes=<in>,<out>,<cw>,<cr>,<r> nonutc=<n> days=<n>
SUM MODEL model=<model> input=<n> output=<n> cache_write=<n> cache_read=<n> reasoning=<n> rough=<n> dashes=<in>,<out>,<cw>,<cr>,<r> nonutc=<n> repos=<n>
SUM UNIT unit=<unit|-> input=<n> output=<n> cache_write=<n> cache_read=<n> reasoning=<n> rough=<n> dashes=<in>,<out>,<cw>,<cr>,<r> nonutc=<n> pairs=<n> days=<n>
SUM TOTAL input=<n> output=<n> cache_write=<n> cache_read=<n> reasoning=<n> rough=<n> dashes=<in>,<out>,<cw>,<cr>,<r> nonutc=<n> turns=<n|-> pairs=<n> models=<n> units=<n>
SUM MORE kind=<pair|model|unit> shown=<n> total=<t> nova-tokens sum --out <dir> --month <m> --max 0
SUM OK month=<m> days=<n> missing=<n> pairs=<n> models=<n> units=<n> nonutc=<n>
SUM REFUSED: <reason>
CHECK FAIL <path>: <reason>
CHECK FAIL <path>:<line>: <reason>
CHECK FAIL stale last=<d> through=<d>
CHECK MISSING date=<d>
CHECK STRAY <path>
CHECK MORE kind=<file|row|missing|stray> shown=<n> total=<t> nova-tokens check --out <dir> --max 0
CHECK OK at=<stamp> build=<id> files=<n> rows=<n> first=<d> last=<d> missing=0 stray=0 gap=<n> notes=<n>
CHECK FAIL files=<n> rows=<n> first=<d> last=<d> bad=<n> missing=<n> stray=<n> gap=<n> notes=<n>
CHECK REFUSED: <reason>
SOURCES SOURCE label=<label> kind=<claude|opencode|swarm|bus|provider> path=<path> reports=<types> day_basis=<utc|mixed|<zone>> files=<n> unreadable=<n> messages=<n> dup=<n> noid=<n> nousage=<n> unparsed=<n> comments=<n> redated=<n> superseded=<n> rows=<n>
SOURCES UNREADABLE label=<label> path=<path>: <why>
SOURCES UNPARSED label=<kind>:<name> note=<id> line=<n>: <text>
SOURCES UNATTRIBUTED stem=<path> tokens=<n>
SOURCES MORE kind=<source|unreadable|unparsed|unattributed> shown=<n> total=<t> nova-tokens sources … --max 0
SOURCES OK sources=<n> files=<n> messages=<n> unreadable=<n> unparsed=<n> rows=<n> unattributed=<n|->
SOURCES REFUSED: <reason>
SESSION turns=<n> input=<n> cache_write=<n> cache_read=<n> output=<n> weighted=<n> avg_context=<n>
PROFILES MODEL model=<model> cards=<n> median_out=<n|-> overshoot=<n>
PROFILES OK models=<n> cards=<n> overshoot=<n>
PROFILES REFUSED: <reason>
FOLD OK rows=<n> tasks=<n> days=<n> ledger=<file>
FOLD REFUSED: <reason>
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

`TOKENS DAY` is one line per day written or refused. It describes the day file
(its rows, models, repos, and sources, including retained rows), while the
counts on `TOKENS OK` and `TOKENS FAIL` are the truth about the fold itself (the
rows folded in this run). `unknown=` and `other=` are each bucket's share of the
day's five types summed, to one decimal, so a day that is 40% unknown says so on
the line a person reads; a `-` cell adds nothing to either side of that share.
`dashes=` is how many of the day's type cells are `-` and `nonutc=` how many of
its rows carry a `day_basis` other than `utc`. `sources=` is the union of labels
across the day's rows.

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
date	model	repo	input	output	cache_write	cache_read	reasoning	rough	day_basis	sources	units
2026-09-11	claude-fable-5-1	schema	8410	593734	1504393	236002356	-	0	utc	claude:glenn	u3
2026-09-11	deepseek-v3	serialize	812004	40211	-	-	-	0	utc	swarm:deepseek	-
2026-09-11	gemini-2.5-pro	schema	123456	7890	-	-	-	1	utc	bus:emma	-
2026-09-11	gemini-2.5-pro	unattributed	9912340	301122	-	-	-	0	America/Los_Angeles	google:emma	-
2026-09-11	mercury-2.5	freddy	4460950	7442	0	4910813	49649	0	utc	opencode:bench	-
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
| `units` | the work-set unit this row's spend is attributed to, or `-` (rule: the unit is attributed per TRANSCRIPT, by `fold --units`) |

Twelve columns, every one written on every row. A `-` in a type cell is a
fact about the source ("did not report"), not about the day, and `sum`
counts them beside the totals it prints.

**`units` is the twelfth and is APPENDED.** The reader takes EITHER width: a
file whose header is the first eleven names is read with every row's unit `-`,
and a file whose header is the twelve is read as it is written. A fold always
writes twelve. Anything that is neither width is refused.

The first line is the **version and stamp line** (rule 12; lesson 45), and
`turns=` on it is the day's message count across the sources that count
messages, `-` when none did, summed by `sum` onto `SUM MONTH` and `SUM TOTAL`
as `turns=`. A file
whose first line is not `nova-tokens v1 …` is refused by `sum` and named by
`check`, and the repair is `fold --day <d>`. Rows are sorted by
`(model, repo, unit)` and are unique by it: a row that summed two units'
spend under one `(model, repo)` could be split back only by guessing. The write lands atomically through internal/atomicfile via a unique random-sibling temporary file `.<day>.tsv.tmp-%08x` and rename (rule 8).

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
reason (rule 3).

### `--opencode <label>=<file>`: OpenCode's SQLite database

The file, and `<file>-wal` and `<file>-shm` when present, are copied into
`--scratch`, and three queries run there with `sqlite3 -readonly -json` under
`--timeout` (rule 16; `-json` rather than `-tabs`, because a tool command input can carry tabs and newlines, which would corrupt TSV column splitting): sessions (`id`, `parent_id`, `directory`), assistant
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

### `--swarm <label>=<pool>`: swarm pool usage files

Every `<pool>/usage/<job>.tsv`: one header of sixteen columns and one row per
job attempt. The sixteen, in order, are `job`, `attempt`, `from`, `started`,
`ended`, `end`, `rc`, `provider`, `model`, `repo`, `tokens_in`, `tokens_out`,
`cache_write`, `cache_read`, `reasoning`, `usd`; every cell is looked up by
that name. The message id is `job`; `model`,
`repo`, `tokens_in`, `tokens_out`, `cache_write`, `cache_read` and
`reasoning` come from the row; a cell the file holds as `-` stays `-`
(`reports=input,output,cache_write,cache_read,reasoning`), and the `sources`
label says where the row came from. The `repo` is taken as recorded, because
the usage row already attributes it and the job's paths may be gone with the
directory; it goes through the same attribution function with the recorded
name as its only path, so a name the rules file does not know is `other`,
never a seventh bucket. The day is the row's `ended` stamp. A job directory
under `done/` or `failed/` with no usage file is `nousage=<n>`; nothing under
those directories is opened for anything else, so the answer does not
depend on what the job directories still hold. A file whose header is not the sixteen names in
order is `TOKENS UNPARSED` naming the file and the first wrong column.

### `--provider <label>=<file>`: a billing export

For a harness that records nothing (rule 21). The file is the provider's own
export, unmodified; the label names the provider and the parser (`google`,
`openai`, `xai`); an export whose shape the parser does not know is `TOKENS UNREADABLE`
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
`utc` row and a zoned row is `TOKENS MIXED` and not written. The `xai`
parser also reads a `grok usage` JSON export (a `sessionId` and a `turns`
array, chosen by the file's leading `{` or `[`), folding each turn into the
same rows: `endedAt` is the day, `primaryModelId` the model, and
`inputTokens`/`outputTokens`/`cacheCreationTokens`/`cachedReadTokens`/
`reasoningTokens` the five counts, with a field the turn did not carry left
a `-`, never a zero. `costUsdTicks` is the turn's cost, an integer count of
micro-dollar ticks — the unit `usd=` holds — folded into the model's `usd=`
on the day's `TOKENS AVG` lines (rule 20's amendment: the cost is "from the
usage `usd` column or a cost tick the source reported"); a lexeme that is
not a non-negative integer is an absence, and `usd=` is `0` where no source
reported one. The flag names that one file. A path that is not there is
`TOKENS UNREADABLE` saying the file is not there and that a session store is
not scanned; a directory is not walked for `usage.json` files.

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
`nova-bus check --rebuild-index` writes sorted by path, and a commit order is a fact about the checkout and not about
which number the friend meant. A successor that names a missing note, a note
in another lane or for another day, a note that did not parse, or a note
that is itself its successor (a cycle, at any length) is `TOKENS UNPARSED`
for the whole successor with the reason after the colon and the remedy `send
a correction whose subject carries supersedes=<id>`; the earlier valid note
stays the day's report, visibly, and the run is exit 1 until the correction
is fixed. `report --supersedes <note-id>`, repeated once per predecessor,
writes the trailer, and the parser
that reads it is the serializer that writes it (lesson 113). A lone tokens
note for a day needs no order and no trailer. The trailer names a set
because with one id per trailer, two roots or two successors would leave two
tips whatever is sent next, and no correction could resolve the conflict.

Reading the bus, the tool never pulls, fetches, pushes, runs `git`, or talks to a
network (only `ledger` and `report --redis` dial a network, and only the Redis
they are named). It reads the checkout it is given as files (rule 16). A caller who wants today's
notes runs `nova-bus inbox` first. A fold that fetched would be a fold whose
numbers depend on a network call, and the `TOKENS SOURCE` line for the bus
prints the newest note's mtime so a reader can see how fresh the checkout
was.

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

The table is the caller's file, the rule is one function, and the two shares
on the day line are how a person sees whether the file is good enough.

**`sources --unattributed` says what `other` is made of.** The share on the day
line is the diagnosis and never the remedy: without a listing, the only way to
learn which paths fell to `other` is to grep the transcripts by hand. With the
flag, `sources` tallies
every path token that reached the `other` arm, keyed by the token's leading
directory to four elements — which is where a repo is named, and which is the
shape a rule matches — and prints them heaviest first, capped by `--max`:

```
SOURCES UNATTRIBUTED stem=<path> tokens=<n>
```

`SOURCES OK` then carries `unattributed=<n>`, the total tokens that fell to
`other`; without the flag nothing is tallied and the field is `-`, because a
dash is an absence where a zero is a measurement. The tally is taken inside the
attribution ladder as the paths go past, so there is no second read of anything,
and `fold` never takes it: `sources` is the verb that only looks.

The tally holds at most 50,000 distinct stems — past that the stems already held
keep counting and no new one is admitted, so the top of the list is unaffected
and `unattributed=<n>` is still every token. The `SOURCES MORE` line's `total=`
is the stems the tally holds, and `unattributed=` on the `OK` line is the number
that is never capped.

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
messages a day. Ten declared
sources. Ninety days under `--all`.

| verb | lines at that state (default `--max 20`) | bytes |
|---|---|---|
| `fold --all` | 1 FOLD + 10 SOURCE + 20 UNREADABLE + 1 MORE + 20 UNPARSED + 1 MORE + 20 SUPERSEDED + 1 MORE + 20 CONFLICT + 1 MORE + 20 TOUCHED + 1 MORE + 20 MIXED + 1 MORE + 20 DAY + 1 MORE + 1 OK + 1 NOTE = 160 | under 28 KB |
| `report` | up to 200 pairs x 5 types = 1,000 lines on stdout, uncapped, because the body is the artifact and a capped report would be a count sent as a total; 1 REPORT OK on stderr | under 64 KB |
| `sum --month` | 1 MONTH + 20 PAIR + 1 MORE + 20 MODEL + 1 MORE + 1 TOTAL + 1 OK = 45 | under 10 KB |
| `check` | 20 FAIL + 1 MORE + 20 MISSING + 1 MORE + 20 STRAY + 1 MORE + 1 count line = 64 | under 8 KB |
| `sources --all` | 10 SOURCE + 20 UNREADABLE + 1 MORE + 20 UNPARSED + 1 MORE + 1 OK = 53 (+ 20 UNATTRIBUTED + 1 MORE with `--unattributed`) | under 8 KB |

These are ceilings that do not grow with the state. A test builds that state
in `t.TempDir()`, runs every verb, and asserts the line and byte counts
against the table (lesson 169).

**The fold's cost is one pass over each source file.** Each declared file is
opened once per run, and a test counts opens; no subprocess but `sqlite3`
runs, and the same test asserts it. There is no index and no
incremental mode: a day file is recomputed whole from the sources every
time, and the day's own transcripts are the only thing that must be read to
compute it. The two-minute rule holds with room, and it is a count that is pinned, not a
time (lesson 167).

## The efficiency card (#85), nova-tokens

The card is a measurement, taken on the bench, of what
`nova-tokens` pays once and what it pays again. This section is the part of it
that binds this tool: the transcript walk, the coordinator read, and what a run
waits on. It states the contract, not a bench recipe.

### One walk of the sources per run (`REPEATS`)

The measured bench held **1,397 `.jsonl` files** and **1,699 MB** of Claude Code
transcripts, and one fold parsed **57,239 messages**. `internal/tokens/claude.go:98`
walks the transcript directory and parses every message; the fold then folds
every day the sources name (`cmd/nova-tokens/main.go:587`, `folder.Days()`). One
walk covers all ten days in **3.27 s** (`--all`), so folding day by day repeats
the same walk once per day: ten days that way is about **35 s**, and a fold run
twice in a day parses the 1.7 GB twice to write one day's delta. There is no
per-file cache and no cursor. `dup=49765` of `messages=57239` is **87 %** of the
parsed messages discarded as already-counted.

The contract is **one walk of the sources per run**: `--all` pays for the tree
once and folds every day from that one stream, so a day-at-a-time fold is a
debugging convenience and not the retained-accounting route. A cursor or a
per-file cache that lets a second run read only the delta is the tool's, and it
does not change the day file's shape.

### The coordinator read is bounded (`COORDINATOR READ`)

`fold --day` prints 5 lines and 984 B; `fold --all` prints 14 lines and 2,297 B
for 10 days. One `TOKENS SOURCE` per source, one `TOKENS DAY` per day, one
`TOKENS OK`; the day line carries that day's shares of turns and tokens, so the
whole ledger is read from the day lines. `check` is one line per finding, capped
by `--max` (default 20), and on the live ledger it printed 33 lines because
every one of the nine day files was a finding: 9 bad, 36 missing, 2 stray.
`check` is the gate, never a table.

### What a run waits on (`WAITS ON`)

There is no clock in this tool. `--timeout` (default **120 s**) is how long it
waits on one source and not a deadline on the run. What a fold or a check
actually waits on is a person running it, and on the measured bench it was not
being run: `check` printed
`CHECK FAIL files=9 rows=0 first=2026-07-29 last=2026-09-11 bad=9 missing=36 stray=2`,
and `sum --month 2026-09` refused because the first line of a day file was not
the version line, with `fold --day <d>` as the repair. Nine of nine day files
were bad. A stale ledger is repaired by `fold --day <d>` before `check` is
trusted.

### Red tests

The card earns the same red-first bar as every rule here: seen red before it is
trusted.

- one `--all` fold walks each transcript file once and folds every day from that stream, so a day-at-a-time fold is not the retained-accounting route;
- the coordinator read is one `TOKENS SOURCE` per source, one `TOKENS DAY` per day and one `TOKENS OK`, and `check` is one line per finding bounded by `--max`;
- `--timeout` bounds one source and not the run, and a stale ledger is repaired by `fold --day <d>` before `check` is trusted.

## What it deliberately does not do

- **It does not price anything.** Tokens, by type, per model. Dollars are a
  rate card times a count, the rate card changes, and a tool that carried
  one would carry a stale one.
- **It does not claim coverage.** Every total is the sum of what the declared
  sources reported; `dashes=`, `missing=`, `unknown=` and `unreadable=` say
  what it does not cover, and no line calls a sum complete.
- **It does not pull the bus, fetch, push or run `git`, and no verb but `ledger`
  and `report --redis` talks to a network.** It reads a checkout as files; competing notes are ordered by what they say
  (`supersedes=`), never by the checkout's history.
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

## Tests this spec demands

One line per rule in **the rules, numbered**, and one (22) for
`sum --swarm-root`'s cost per completed task. Each is a test the work list
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
7. A body line `… input ~100000` folds as 100000, the row has `rough=1`, a
   second rough line on the same row makes `rough=2`, `TOKENS DAY rough=2`,
   and `sum` carries `rough=2` on the pair, the model and the total.
8. A fold killed with SIGKILL between the temp write and the rename leaves
   the old day file entire and a temporary file beside it; the next fold
   writes the day file atomically through internal/atomicfile and preserves
   the stranded temporary; `check` does not name a valid day-file temporary
   as a stray; a second concurrent fold on one `--out` waits and exits 2 naming
   the holder's pid; a source test finds no `os.Remove` and no `os.RemoveAll` anywhere in
   the package (except internal/atomicfile's own-run temporary cleanup).
   This tripwire is rule 9's: it reads rule 9's list of calls that can empty a
   file and fails on any call it cannot match to a carved-out file. The
   carve-outs are the tripwire's own list (rule 9); a removal of anything
   else, including any file the tool was given, is the failure this test
   exists for.
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
    `09-07, 09-08, 09-10`: every finding prints one line, the count line
    prints `bad=8 gap=1 missing=0`, exit 1; the same run with `--strict`
    prints `CHECK MISSING date=2026-09-09` and `missing=1`, and so does a
    `--no-spend` list that does not name that day; a `--no-spend` list that
    names it is `missing=0`; `--strict` and `--no-spend` together is exit 2;
    a file whose type cells are `-` and whose `day_basis` is
    `America/Los_Angeles` is clean; a clean set is `CHECK OK … missing=0`,
    exit 0; `sum` over the same gapped month exits 0 with `missing=1`.
    The directory listing of this repository's own `reports/tokens` — sixteen
    day files with two long gaps, `README.md`, `collate.log`,
    `pre-nova-tokens/` and a session note — is `CHECK OK`, exit 0, with the
    gaps and the notes counted on the line; under `--strict` it is the 40
    findings.
14. A pool with two usage files and a third job directory with none, after
    the two job directories are removed: two rows fold with
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
    export has no column for, and `day_basis` per demanded test 17; a
    `grok usage` JSON turn carrying `costUsdTicks` folds its cost into the
    model's `usd=` on the day's `TOKENS AVG` line, never `usd=0` where the
    source reported a tick; an export with one unknown column is `TOKENS UNREADABLE` quoting that line;
    a friend's tokens note whose body is one `# repos: schema, serialize`
    line and nothing else is a valid note with zero rows, yields
    `TOKENS TOUCHED … repos=schema,serialize`, and changes no count.

22. A fixture ledger and a fixture swarm root of cards whose receipts carry
    `repo` and `rc`: `sum --swarm-root` writes one ledger row per `(model,
    repo)` pair with `repo` from the receipt, `completed` counting only the
    `rc=0` cards, and `usd_per_task` equal to `usd / completed` to six
    decimals; a pair with `completed=0` writes `usd_per_task=-`, never a
    division, and a receipt whose `rc` is `-` counts in `cards` and `dashes`
    and never in `completed`; the header is the ten columns in order, and a
    ledger whose header differs is refused (exit 2) naming that order.

## The work list

The tool is Go under `cmd/nova-tokens` and `internal/tokens`, built the way
`cmd/nova-bus` is: no hardcoded paths, no default paths, the exit grammar
above, `internal/oneline` for every printed value, `internal/bounded` for every
listing, and `ONBOARDING.md`'s first-day standard: a usage banner ending in a
runnable `example:` block, refusals that say what the flag wants and report
every independent problem at once, a `### First run` in `docs/CLI.md`, a
`quickstart` verb or the sentence saying why there is none, and tests that
pin all three by executing them.

1. **`internal/tokens/dayfile.go`**: the day file: the version and stamp
   line with `turns=`, the twelve columns (an eleven-column file is read
   too), strict parse (a row with ten columns is an
   error naming the line; a type cell is an integer or `-` and an empty
   cell is an error), sorted rows, the atomic write through `internal/atomicfile`
   and rename under the output lock, the shrink comparison with `-` on either
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
13. **`docs/CLI.md`'s `### First run`**: fold one fixture transcript and one
    fixture bus note into a temp directory, `check` it, `sum` it, every path
    a flag, the transcript produced by running the tool. The fixture bus
    lane uses `example.com`.
