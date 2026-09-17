# nova-swarm — specification

`nova-swarm` is one binary at the **worker layer**. It runs a pool of **one-task
workers** — any provider, any model, through one harness — each with its own
working directory, its own data home, its own job directory and its own deadline
held by the machinery rather than by the worker.

This spec is normative. If the code and this document disagree, one of them has
a bug, and the tests decide which. It stands beside [SPEC.md](SPEC.md), whose
**Conventions** section — exit codes, no guessed paths, the one-line output
grammar, the cap-and-count rule, `internal/oneline` and `internal/bounded` —
applies here unchanged and is not restated.

A swarm in this shape ran three batches of tasks in one morning and a night of
ten before that. The form works, and every way it failed is in the table.

| the failure, from the record | the rule that closes it |
|---|---|
| six workers on one data home: `database is locked`, and five of six did nothing (**2026-09-10**) | a **slot**: its own working directory and its own data home per running worker |
| an API key in an argument is in the process table, and in a log, and in a transcript somebody pastes (**the leak that taught it**) | the key is **read as data from a file, never sourced, never an argument, never printed** — and the config file written for the harness carries the env var's *name*, never its value |
| a worker asked to loop ran until somebody noticed | the deadline is held by the **machinery**, outside the worker, and the worker is told its own deadline in its prompt |
| a worker read a file outside its job directory, the harness refused the read, and the worker treated the refusal as fatal: **two runs ended on a refused read, today** | the sandbox rule is stated in the prompt: a refused read is **not** the end of the run |
| a task was given as one sentence and came back as a plan | task **templates** with the learned conditions baked in, and `RESULT.md` with only a plan is a **failed** task |
| 25 of 67 findings in batch 1 were duplicates of the owed list in the pull request body nobody read first | the `read-pr` template's first condition: **read the owed list first and mark duplicates** |
| 5 of 67 were wrong because a rule was paraphrased from memory | **quote every rule verbatim with `file:line`** |
| a worker that died at the deadline had found things and written none of them | **append each finding the moment it exists**, never at the end |
| one worker read 40 files and finished nothing | a **file budget** in the task, and batch 3 went from 0 to 2 of 3 complete |
| a result file was rewritten while triage was reading it | a report is **published by rename**: whole revisions, `RESULT.md.tmp` renamed over `RESULT.md`; the tool reads only the renamed file, identifies a revision by its content hash, and never by an mtime (rule 16) |
| a bounded review that found nothing was counted as a plan, so a worker was rewarded for finding something (**Stella's read, 2026-09-11**) | completion evidence is the head's `findings: <n>` line, separate from the count: `findings: 0` is **`clean`**, a report with no head is `plan-only` (rule 8) |
| a dispatcher killed with workers alive released the pool lock, and a second dispatcher could reuse a slot whose data home still had a writer | slots are **durable ownership** on disk: a restart adopts a live worker by its pid file, reclaims a slot whose pid is dead, and **quarantines** a slot it cannot decide (rule 17) |
| "the slot is written before the child starts" named no transaction: a crash between the fork and the write left a running worker nobody tracked (**Stella's second read, 2026-09-11**) | the **launch transaction** (rule 18): the runner reserves the slot with a placeholder, the child writes its own pid, pgid and start stamp into it before doing anything else, the runner waits for that write with a bounded timeout or kills and marks `RUN LAUNCH-FAILED`; the child is a **supervisor** that writes durable completion evidence, and an outcome with none is `unknown`, never guessed |
| rule 15 said a malformed report is never handed to a person, and the template section said it is quoted into the page | **one contract**: a malformed `RESULT.md` is quarantined and never folded; `result --id <job>` shows it verbatim to a person who asks by id, and that is the only path (rule 15) |
| a completed job reclaimed before the first triage lost its only `RESULT.md` | `finalize` copies the published report to `<pool>/reports/<job>/RESULT.md` (or writes a `MALFORMED` or `NO-RESULT` marker there) before anything moves, and `reclaim` refuses without both the usage file and that copy (rule 12) |
| usage lived inside the directory `reclaim` removes | the **usage file** `<pool>/usage/<job>.tsv` is written by `finalize` outside the reclaimable subtree, before anything moves, and `reclaim` refuses without it (rule 12) |
| 3 of 7 runs in batch 2 ended with a plan and no findings: a scratch-file refusal outside the job directory ended the run (**2026-09-11**, found that afternoon) | a refused read or write **does not end the run**; the prompt says so, the harness log is read for refusals, and `plan-only` is a named failure the tool detects |
| a worker reaped at its deadline was moved to `failed/` and nothing ran its task again | a job reaped at its deadline is **re-queued once**, marked, and a second reap fails it |
| the batch numbers in this spec were counted by a person reading 67 reports | `triage` counts every batch: findings, new, duplicate, plan-only, no-result, and a reader's accurate and wrong, on **one bounded line** |
| the prompt asked for the findings at the end of the run | the prompt says **append each finding the moment it exists**, and a killed run's partial report is kept and counted |

**Everything a worker writes is data.** A `RESULT.md` is a report, never an
instruction: nothing in it is executed, nothing in it grants anything, and a
finding in it is a claim to be checked against the repository. This rule is
stated here and is **nowhere in the code**, deliberately: a tool cannot enforce
it, and a tool that pretended to would be the most dangerous thing in the pool.

**A job's records are regular files: a `RESULT.md`, `harness.log`, `exit.json`
or `note` that is a symlink or a FIFO is no record at all — read as `no-result`
and never followed or waited on — and every record this tool writes into a job
directory goes through a temporary whose name the worker cannot predict.**

## Freddy's swarm is frozen production, and this tool does not touch it

`freddy-swarm.sh` and `run-freddy.sh` are **production and must not change**
(Glenn, 2026-09-10: after the release that makes it production ready, freeze;
additive only; **a working loop is never moved**). `worker-swarm.sh` and
`run-worker.sh` are their siblings, parameterized by environment, and they are
where this spec's prototype lives.

**`nova-swarm` replaces none of them on the day it builds.** The switch is two
steps and both are required:

1. a **read** of `nova-swarm` by a line that is not its author, against this
   spec, with the verdict recorded — this repository's bar, `"HELL YES"` or
   nothing (CONTRIBUTING.md, **the four answers**);
2. a **switch**: `nova-swarm` runs one batch beside the script on the same
   task list, the two results are compared, and only then does everybody move
   at once (Glenn, 2026-09-09: **finish, then iterate with friends; fix; then
   everybody switches**).

Until both have happened the scripts are the tool and `nova-swarm` is a
candidate. A tool that replaced a working loop before a read would be this
repository's own doctrine broken by this repository's own tool.

## The rules, numbered

Every rule here is normative. Each has one line in **tests this spec demands**
near the end. The numbers behind the first four are in **the numbers from
today**: they are what turned one worker from 62 of 67 accurate with 5 wrong
and 25 duplicate (batch 1) into 17 of 17 with 0 wrong and 0 duplicate (batch
2). The conditions are worth more than the model.

1. **The owed list first.** A `read-pr` task's prompt says: read the pull
   request body's owed list before any code, and skip what is owed. A finding
   already on that list is marked `dup:` and is not a new finding. `triage`
   counts a finding that matches an owed item and is not marked `dup:` as
   `duplicate`. (Batch 1: 25 of 67 were duplicates.)
2. **Every claim quotes its rule verbatim, beside the line.** A finding line
   carries the rule it rests on, quoted word for word, with `file:line`, on the
   same line or the next. A finding with no quote is counted `unquoted` and is
   not counted `accurate` by anybody. (Batch 1: 5 of 67 were wrong, each a
   paraphrase.)
3. **Append as found, never at the end.** The prompt says: append each finding
   to the report file the moment it exists. A run killed at its deadline keeps
   its partial report, and `triage` counts every finding line in it. (Batch 2:
   3 of 7 runs ended with a plan and no findings.)
4. **A file budget per run.** Every task carries a file budget, `add --files
   <n>`, with no default. The prompt states the number and says what to do when
   it is spent: write what you have and stop. (Batch 3: with a budget 2 of 3
   complete; without, 0 of 3.)
5. **A refused read does not end the run.** The worker's prompt says so in one
   sentence. The harness refuses a read or a scratch write outside the job
   directory, and the worker continues with what is inside. `nova-swarm`
   reads the harness log for refusals and prints `refusals=<n>` on `RUN DONE`,
   so a `plan-only` result beside `refusals=1` is a diagnosis rather than a
   silence. (2026-09-11: a scratch-file refusal ended 3 of 7 runs.)
6. **The key file is data.** The key lives in one file, `~/.config/<provider>/env`
   or the path the worker description names. It is read as data, never sourced,
   never on a command line, never in a log, never in a file this tool writes.
   (The leak that taught it.)
7. **One clone per job; a written deadline; a default action.** Each job has
   its own clone under its own job directory, never shared. Each job carries a
   deadline in seconds and the default action at it: the machinery reaps the
   worker and records what is on disk. The swarm never waits forever. A worker
   silent past its deadline is reaped and its job is re-queued once by default,
   with `requeued=1` in the new task's sidecar; `run --no-auto-retry` instead
   finalizes that attempt without an automatic descendant. A job reaped a second
   time goes to `failed/` with `reaped=2` and is not re-queued again.
8. **Results are counted per batch, on one line, and completion is evidence
   separate from the finding count.** `triage` classifies every report as
   `ok`, `clean`, `plan-only` or `no-result`, and prints one `TRIAGE BATCH`
   line: `findings=<n> new=<n> dup=<n> unquoted=<n> clean=<n> plan_only=<n>
   no_result=<n> accurate=<n|-> wrong=<n|->`. The evidence of completion is
   the report's `## Head`, whose first line is `findings: <n>` (the
   template below); a worker writes the head when the work is done and not
   before. `ok` is a head with `findings: <n>`, `n > 0`; **`clean`** is a head
   with `findings: 0` — a bounded review that finished and found nothing, or
   a `probe-row` that finished with `not done` and its reason, which its
   template calls a complete task; `plan-only` is a `RESULT.md` with **no
   head**, whatever else it holds; `no-result` is no `RESULT.md` at all. A
   `clean` report lands in `done/`, counts as complete on `RUN OK`, and is
   never a failure: the tool must not pay a worker for finding something, and
   a classifier that equated no findings with no work did exactly that. The
   count of finding lines is a separate number and never decides the class.
   `accurate` and `wrong` are a reader's verdicts recorded by `nova-swarm
   verdict`; a dash means nobody has recorded one, and a dash is never a zero.
9. **N workers are N processes.** Each worker has its own job directory and its
   own report file. No file is written by two workers. The coordinator merges
   the reports once, at the end: scatter, then merge, and the merge is the
   only serial step.
10. **A job has a note file, and its report counts the notes it read.** Every
    job directory holds `<job>/note`, created empty before the worker starts
    and named in the prompt. The coordinator appends to it with `nova-swarm
    note --pool <dir> --task <id> --text <text>`, one line per note, stamped
    by the tool; nothing else writes it. The prompt says: between steps, read
    the note file, and count what you read. The report's `## Head` carries
    `notes read: <n>`, and `RUN DONE` carries `notes=<sent>/<read>`, where
    `sent` is the tool's own count of the lines it appended and `read` is the
    report's number, or a dash when the report has none. The count is
    mandatory because a job that ignored a note cannot be told apart from one
    that got none, and a coordinator who cannot tell the difference cannot
    redirect anything. A note is data to the worker and never an instruction
    to the tool. (2026-09-11: a running child could not be redirected; there
    was no path a message could take.)
11. **A job is one blocking process, reported once.** The worker is one child
    of the dispatcher and runs in its own process group. It spawns no
    background subtasks, and the prompt says so in one sentence: do the steps
    in a line; a task that needs two independent things is two tasks. When
    the worker exits, the dispatcher checks the process group; a process still
    alive in it, or a child the harness log shows was backgrounded, is `RUN
    VIOLATION id=<id> background=<n>`, the survivors are killed, and the result
    is quarantined: it moves to `failed/` with `violation=background` in the
    sidecar, and `triage` does not count it. A job reports exactly once: one
    `RESULT.md`, one `RUN DONE` or one `RUN VIOLATION`, and never a second
    notification. (2026-09-11: children stopped mid-task to wait on their own
    background tasks, then reported twice.)
12. **Usage is written to a file outside the reclaimable subtree, by
    `finalize`, before anything else happens to the job.** The **usage file**
    is `<pool>/usage/<job>.tsv`, one per job id, one header line and one row,
    tab-separated, these columns in this order: `job`, `attempt`, `from`,
    `started`, `ended`, `end`, `rc`, `provider`, `model`, `repo`,
    `tokens_in`, `tokens_out`, `cache_write`, `cache_read`, `reasoning`,
    `usd`. `attempt` is `1` for a fresh job and `2` for the one automatic
    re-queue (rule 7), `from` is the earlier job id or `-`, `started` and
    `ended` are UTC stamps the tool wrote, `end` is one of `done`, `killed`,
    `budget`, `budget-unverifiable` (rule 13), `violation`, `failed`, `unknown`
    (no completion evidence, rule
    17), `launch-failed` (rule 18), and `rc` is the worker's exit code, `-`
    for `unknown` and `launch-failed`. A
    field the provider did not report is the literal `-`, never `0`; `0` is
    written only when the provider reported zero. For OpenCode the source is
    the SQLite database in the data home the dispatcher exported for that job
    (`XDG_DATA_HOME`, per job, so one job's usage is one database), read
    once, read-only; the five token columns are `nova-tokens`'s five types
    (SPEC-TOKENS rule 14 reads this file, and reads `-` as unknown, never as
    zero). `finalize` is the runner's step after every end — exit, reap,
    budget, violation — once the process group is dead: it writes
    `<pool>/usage/<job>.tsv.tmp`, fsyncs it and renames it into place; then
    it **copies the published report**: `<job>/RESULT.md`, if one was
    published, is copied byte for byte to `<pool>/reports/<job>/RESULT.md`
    through `.tmp` and rename, and when that copy does not parse (rule 15) a
    marker `<pool>/reports/<job>/MALFORMED` holding `line=<n>` is written
    beside it, and when no `RESULT.md` was published a marker
    `<pool>/reports/<job>/NO-RESULT` is written instead; beside whichever
    was written goes `<pool>/reports/<job>/REV`, holding `attempt=<n>` and
    the SHA-256 of the copy (or of the empty string for a marker), so the
    retained report is keyed by job, attempt and content hash; and **only then**
    moves the job's files to `done/` or `failed/`, and only then prints the
    job's `RUN` line. `triage` and `result --id` read `<pool>/reports/<job>/`
    for a finalized job and `<job>/RESULT.md` only for a running one, so the
    first triage after a reclaim reads the same bytes it would have read
    before it. The usage file is written once and
    never rewritten; a job's second attempt is a new job id with its own
    file, so `cost` sums each attempt once and a retry never double-counts.
    A job whose runner died before `finalize` (rule 17) is finalized by the
    next dispatcher on start, or by hand with `finalize --pool <dir> --task
    <id>`, which is refused if the job's process group is still alive.
    `reclaim` removes a job directory, and it requires **both** the usage file
    and the report copy: a `reclaim` of a job with no usage file is refused on
    one line, `RECLAIM REFUSED id=<id>: no usage file at <path>`, and a
    `reclaim` of a job with no `<pool>/reports/<job>/RESULT.md`, `MALFORMED`
    or `NO-RESULT` is refused on one line, `RECLAIM REFUSED id=<id>: no report
    copy at <path>; nova-swarm finalize --pool <dir> --task <id>`, because the
    evidence would be inside the thing about to be removed; for a profiled
    attempt the same precondition includes its non-secret profile snapshot and
    receipt, copied under `<pool>/reports/<job>/` and checked by
    `snapshot_hash` before the move. The authoritative profile snapshot lives
    under the coordinator-owned protected `<pool>/evidence/<job-id>/PROFILE.json`
    path, where `<job-id>` is the concrete attempt id; a worker-visible
    `<job>/PROFILE.json` is a worker-writable sanitized projection and never
    the trust root. The catalog path is likewise outside every writable pool,
    job, slot, scratch, worker data home and configured `read_roots`. A `reclaim` whose
    copy does not hash to `REV`, or whose `REV` is missing, is refused the
    same way, `report copy does not match REV`, because a persistence that
    cannot be verified is not a persistence; `finalize` on a
    job whose usage file exists but whose copy is missing writes the copy and
    prints `FINALIZE OK … existed=true`. `cost` reads `<pool>/usage/` and nothing else, so it answers
    after the directory is gone; a killed attempt's partial usage stands in
    its own row. (2026-09-11: DeepSeek's usage for two batches lived in
    per-worker data directories that were reclaimed with the jobs, and
    nothing survived.)

13. **The swarm's own tokens are budgeted per job, and the machinery ends the
    job at the budget it can see.** Every job carries `--tokens <n>` (no
    default; `add`, `batch` and `requeue` without it are exit 2 naming the
    flag, and `0` is refused) or the explicit word `--tokens unmetered`, a
    caller's statement that this provider has no live accounting and the
    deadline is the only stop, printed as `budget=unmetered` on every `RUN`
    line for the job. The worker description declares its usage source,
    `usage: opencode` or `usage: none`; `run` refuses at exit 2, before the
    first worker, a pool whose description says `none` while any pending
    task carries a numeric budget, naming the task, because a budget nothing
    can observe is a promise the tool cannot keep. Otherwise; the supervisor samples the provider's usage as the job runs
    (OpenCode's data dir, per job, read-only, every `--usage-interval`
    seconds, default 5, a tool property like a timeout) and ends the job when
    the observed sum passes the budget, recording `RUN BUDGET id=<id>
    spent=<n> of=<n>`; a job that ends this way keeps the findings it
    appended so far (rule 3). **The budget is a stop condition on
    observations, not a ceiling on spend**: usage arrives after the tokens are
    spent, so the overshoot is bounded by one sample interval plus the
    provider's own delay, and the usage row (rule 12) carries the true final
    sum, never the sum at the stop. When no usage has been observed — the
    provider reports nothing, or reports late — the budget cannot fire, the
    deadline still ends the job, and `RUN DONE` / `RUN KILLED` carry
    `budget=<spent|->/<n>`, `-` for no observation, so a job that ran under an
    unobservable budget is visible as such and never reported as under
    budget; a partial observation (some columns `-`) counts the columns it
    has and prints `budget=<n>+/<n>` with the plus. A usage source that
    **fails to read** — the database unreadable, the query erroring, as
    distinct from reporting no rows yet — on three consecutive samples ends
    the job: `RUN BUDGET-UNVERIFIABLE id=<id> slot=<n> samples=3: <reason>`,
    `end=budget-unverifiable`, findings kept, files to `failed/`, because a
    numeric budget the tool has stopped being able to see is a budget the
    caller believes is enforced and is not. A worker
    is handed a task file and the files the task names, never a conversation
    and never a repository to wander: the task template's file list is the
    reading list, `--files` is its ceiling, and a job that reads past it is
    the refusal rule 1 already names. (2026-09-11: 21 children spent 2.4M
    tokens on this bench, most of it re-reading what the task could have
    handed them.)
13b. **A card carries its own budget, and a runaway card is a prompt defect.**
    The worker description optionally carries `max_turns` and `max_cache_read`;
    the supervisor samples usage every `--usage-interval` beside the token
    budget, and when the observed `cache_read` exceeds `max_cache_read` or the
    harness log's assistant turns (counted the way usage counts assistant rows,
    or the usage row count where the log has fewer) exceed `max_turns`, it stops
    the task on the deadline's stop path: the job moves to `failed/` with
    `end=budget` in `usage.tsv`, findings kept, and the task's report carries
    `PROMPT-DEFECT task=<id> reason=budget cache_read=<n> max=<m> turns=<t>`.
    (2026-09-17: one Flash card ran 16 minutes and 3.2M cache-read tokens for
    one fix; the average card is 1.6M cache-read for 40-60k of prompt.)
13c. **A card budget below the harness's measured startup cost is refused at
    load, never applied.** A cap is only a budget if it is above what the
    harness spends before the card's first turn: a known-answer probe writes
    the measurement to `<root>/startup-cost.tsv`, a header and one row of
    `harness_sha256`, `first_usage_cache_read` and `first_usage_turns`, and
    `run` and `native` read it whenever a worker description sets
    `max_cache_read` or `max_turns`. A `max_cache_read` below twice
    `first_usage_cache_read`, or a `max_turns` below `first_usage_turns+2`, is
    refused at exit 2 before any launch, naming both numbers:
    `BUDGET REFUSED max_cache_read=<m> startup=<s> (want >= 2x)`. No
    measurement accepts the budget -- the first run has nothing to compare
    against -- and the first finished task writes the file, so the next run has
    a fact rather than a guess. (Stella, 2026-09-17: her 2k cap was smaller
    than the harness's own first context, so every card would have died at
    once.)
14. **The coordinator spends one command to spin a swarm up and one line to
    read it down.** Spinning up is `add --task <file>` per job from a
    template, or `batch --tasks <dir>` for many; the coordinator writes no
    prompt by hand for a job a template covers, and a template is a file in
    the repo, read by the tool, never pasted into a turn. Reading down is
    `triage --batch <id>`: one bounded block, counts first (done, refused,
    reaped, plan-only, budget), then the top `--max` findings by the
    template's own ranking (a finding line with its evidence path), then
    `TRIAGE MORE kind=finding shown=<n> total=<t> at=<path>` for the rest. The coordinator's window
    never holds a worker's transcript, never a raw `RESULT.md` unless it
    asks for one by id (`result --id`), and never the runner's log. **Batch membership** is the
    sidecar's `batch=<id>`, written by `batch` for every task it queues (the
    id is `<UTC stamp>-<label>-<rand6>`, printed on `BATCH OK`) and `-` for
    a task queued by `add`; `triage --batch <id>` walks exactly the jobs whose
    sidecar carries that id, wherever they sit, and is `TRIAGE REFUSED` for an
    id no sidecar carries. Every verb this rule names is in **the verbs**
    below with its output line and its exit codes. A named friend spinning up
    a swarm pays the same: one command, one line back.
    (2026-09-11: the window read twenty reports of forty lines each and
    wrote twenty prompts of thirty lines each; that cost is the
    coordinator's tokens, and it is the tool's to remove.)
15. **A result has one shape, so the fold is mechanical and a person reads
    counts.** `RESULT.md` is the template below and nothing else: a head
    with the verdict words and counts, then items with the line quoted, the
    rule, the fix; the runner parses it, and a result that does not parse
    is `RUN MALFORMED id=<id> line=<n>`, its class is `malformed`, its files
    go to `failed/` with `malformed=<line>` in the sidecar, and **it is
    quarantined: never folded into a page, never counted as `ok` or `clean`
    whatever its head says, and no finding line inside it is folded, valid or
    not** — a parser that salvaged the lines it liked would be a parser with
    an opinion. The one path from a malformed report to a person is `result
    --id <job>`, which prints the file verbatim to the person who asked for
    it by id; `triage` never quotes it and no page holds a line of it. A
    malformed report whose head says `findings: 0` is `malformed`, not
    `clean`. A batch's findings across jobs are de-duplicated by **(repo,
    rev, file, line, rule)** before `triage` prints them — `repo` and `rev`
    are the head's `repo: <owner>/<name>` and `rev: <sha>` lines, and two
    findings merge only when both reports carry both and they are equal, so
    equal `file:line` and rule in two codebases, or in two revisions of one,
    are two findings; `file` is normalized before the compare — relative to
    the repository root, a leading `./` stripped, `\` read as `/` — and the
    merged finding carries every contributing job id, `jobs=<id,id,…>`, on
    the page and on its triage line, so a de-duplicated finding never loses
    the report it came from — and the duplicate count is printed, because a
    coordinator reading the same finding five times is five times the tokens
    for one fact.
16. **A report is published by rename, and the tool reads only what was
    published.** The worker's report is `<job>/RESULT.md`, and every write of
    it is a whole revision: the prompt says, in one sentence with the two
    commands, write the whole file to `<job>/RESULT.md.tmp` and rename it over
    `<job>/RESULT.md`. `rename` is atomic within a directory, so a reader
    sees the previous revision or the new one and never a prefix. The runner
    and `triage` read `RESULT.md` only, never `RESULT.md.tmp`, and there is no
    mtime anywhere in the tool: a revision's identity is the SHA-256 of its
    bytes, and `<pool>/triage.json` records, per job id, the hash of the
    revision last folded into a page. (The ONE exception is rule 19's `reap`,
    which reads a harness log's age to decide whether a slot is finished; it
    never reads or decides a report's identity, and it never touches a
    `RESULT.md`.) A `RESULT.md` whose hash is not the
    recorded one is new and is folded; a file whose hash **changes between
    the read and the end of the parse** — a writer that ignored the protocol
    and appended in place — is not folded this run, prints `TRIAGE SKIPPED
    id=<id>: changed while read`, and is folded by the next run when it holds
    still; nothing is recorded as consumed unless the bytes recorded are the
    bytes in the page. **A read that COLLIDED is not a change.** Every read of
    a report — the live `RESULT.md`, the retained copy, and the second hash —
    waits out a concurrent replace at that path, bounded, the way rule 17's
    reads do; a record that is GONE still answers at once. Only a second hash
    that DIFFERS is `changed while read`. Without that wait, on a platform
    where a read inside a replace window fails, a report that had not changed
    by one byte is skipped, and one that was published is counted as one that
    was not. After the job's process group is dead, a leftover
    `RESULT.md.tmp` is left where it is as data, never renamed by the tool and
    never read, and the job's `RUN` line carries `unpublished=true`; the
    published revision is what is counted. (Stella, 2026-09-11: a copy taken
    during an append is a prefix, and two revisions can share an mtime.)
17. **A dispatcher that dies leaves durable ownership, and the next one
    recovers it or quarantines it.** A slot is a file, `<pool>/slots/<n>.json`,
    written by the launch transaction of rule 18 and holding, once launched,
    `{job, state, pid, pgid, pid_started, runner_pid}` where `pid_started` is
    the process start stamp the kernel reports for that pid; each job also
    holds `<job>/pid` with the same fields. (2026-09-12: the launched slot
    file also holds `exit_attest`, the sha256 of a per-launch secret the
    supervisor mints in its own memory, and `<job>/pid` carries no `nonce` —
    see rule 18.) There are **two kernel locks, and
    each dies with its holder**: `<pool>/run.lock` excludes dispatchers only
    and is held by `run` for its whole life; `<pool>/slots.lock` protects
    each brief slot-state transition — reserve, identify, orphan, release —
    is held for the read, the compare and the rename of one slot file and
    nothing longer, and is **never held while waiting** for a process, a
    handshake or a timeout — the one wait it may cover is the filesystem's
    own replace collision, **bounded at 2s**, which a rename under this lock
    waits out on a platform whose rename is not atomic against a reader
    (Windows), and which is a property of the filesystem rather than of any
    job — so a supervisor identifies while its parent still
    owns `run.lock` and a recovering dispatcher owns `run.lock` while the
    supervisor it is deciding about takes `slots.lock`. On start, before it
    claims any pending task, a
    dispatcher reads every slot file and decides each one: `state=reserved`
    and `runner_pid` dead — **orphaned, and ambiguous**: no child has
    identified itself, but a spawned supervisor may be paused before its
    identify (rule 18, step 3) and cannot be proven absent by a dead runner,
    so the slot is **quarantined** (`RUN QUARANTINE slot=<n> id=<id>:
    reserved, launch unproven`), never allocated, and its file is rewritten
    atomically to `state: "orphaned"` with the `nonce` unchanged — **under
    `slots.lock`, after re-reading the file and rechecking that it still
    says `state: "reserved"` with the `nonce` first read**: if it now reads
    `state: "launched"` the supervisor's identify landed first and the
    dispatcher follows the adopt path below instead, and if it reads
    anything else the dispatcher decides the new content by this rule, so an
    orphaning never overwrites an identify that just completed; the kept
    `nonce` is
    what makes the stale supervisor's identify fail (rule 18); **launch
    absence is established**, and only then is the task returned to
    `pending/` with `launch=unlaunched` in the sidecar and the slot freed
    (`RUN RECLAIM … end=unlaunched`), by exactly one of: `<job>/aborted.json`
    carrying the slot's `nonce` and `survivors=0`, written by the supervisor
    that lost its identify (an `aborted.json` with `survivors>0` **retains
    the quarantine**, `RUN QUARANTINE slot=<n> id=<id>: aborted, survivors=<n>`,
    because a process in that group is not a proven absence); the runner
    that spawned it having killed its group at
    `--launch-timeout` (rule 18, step 4, the runner alive to do it); or a
    person removing the slot file. A slot is never freed on a dead
    `runner_pid` alone, and every release of a slot file — reclaim, unknown,
    `LAUNCH-FAILED`, `end=unlaunched`, free-on-finalize — is a rename made
    under `slots.lock`; the pid
    is alive **and** its start stamp matches the file — **adopt**: the job is
    watched from here, its deadline computed from the recorded start, and it
    ends with the same `RUN DONE`, `RUN KILLED` or `RUN VIOLATION` it would
    have had, printed by this dispatcher, from the supervisor's completion
    evidence `<job>/exit.json` (rule 18), because a dispatcher cannot `wait`
    on a process that is not its child and never pretends to; the pid is
    dead, no process in the recorded group is alive, and `<job>/exit.json`
    exists with the slot's `nonce` — **reclaim**: `finalize` runs for the job, its files move as rule
    12 says, and the slot is freed; the pid is dead, the group is dead, and
    there is **no** `exit.json` — the outcome is **unknown**: `finalize` runs
    with `end=unknown`, the files go to `failed/` with `end=unknown` in the
    sidecar, the published `RESULT.md`, if any, is kept and copied but the
    job is never `ok` or `clean`, and the slot is freed; anything else — a
    pid alive with a different start stamp (pid reuse), a dead leader with a
    survivor in the group, an unreadable slot file, a slot file with no
    matching `<job>/pid`, a `reserved` slot whose `runner_pid` is alive under
    another start stamp, an `exit.json` whose `nonce` is not the slot's — is
    **quarantined**: the slot is never allocated for
    the rest of this run, `RUN QUARANTINE slot=<n> id=<id|->: <reason>` is
    printed once, and `STATUS OK` carries `quarantined=<n>` until a person
    ends the survivor and removes the slot file. A slot is never decided by
    a directory scan, a timer or an age. (Stella, 2026-09-11: killing the
    dispatcher released its lock while workers still ran, and a second
    dispatcher could reuse their slots and their data homes — the
    2026-09-10 failure by a third route.)
18. **The launch is a transaction with a handshake, and the child writes its
    own identity.** Starting a job is these steps, in this order, and a kill
    at any boundary leaves a pool the next dispatcher decides by rule 17:
    (1) **reserve** — under `slots.lock` the runner writes
    `<pool>/slots/<n>.json` with `{job, state: "reserved", runner_pid,
    runner_started, reserved_at, nonce}` and no pid — `nonce` is twelve hex
    characters from the OS random source, drawn once per launch and never
    reused — through `.tmp` and rename, and
    moves the task into `running/`; (2) **spawn** — the runner forks the
    **supervisor**, `nova-swarm supervise --pool <dir> --task <id> --slot
    <n> --nonce <hex>`, this binary again, as the leader of a new process
    group; (3)
    **identify** — the supervisor, **before doing anything else**, writes its
    own `pid`, `pgid`, kernel start stamp and the `nonce` it was handed into
    the slot file and into
    `<job>/pid` with `state: "launched"`, each through `.tmp` and rename,
    and the slot write is a **compare-and-swap under `slots.lock`** (rule
    17: never `run.lock`, which the runner holds for its whole life and
    which would deadlock this step against the handshake): the supervisor
    takes `slots.lock`, reads the slot file, compares, renames and releases,
    and the write
    lands only if the slot file still reads `state: "reserved"` with the
    `nonce` the supervisor was handed; on any other content — no file, a
    different `nonce`, `state: "orphaned"` (rule 17), a later reservation —
    the supervisor **aborts before step 5**, in this order: it never spawns
    the harness; it counts the processes in its own group other than itself
    (there should be none, because nothing has been spawned); it writes
    `<job>/aborted.json` with `{nonce, reason, at, survivors}` through
    `.tmp`, **fsync and rename, so the durable acknowledgement exists before
    its own death can be observed**; then it exits 2 with `SUPERVISE
    ABORTED slot=<n> id=<id>: reservation changed`. It never kills its own
    group before that file is on disk, and it never claims an absence it
    did not observe: `survivors>0` in the file keeps the slot quarantined
    (rule 17). A stale supervisor
    therefore never writes over a live reservation and exactly one launch
    owns a slot;
    (4) **handshake** — the runner waits for `state: "launched"` to appear in
    the slot file, up to `--launch-timeout` seconds (default 10, a tool
    property like a timeout), **holding `run.lock` and no other lock while
    it waits**. The timeout is **one deadline for the whole handshake**,
    taken once from the monotonic clock: every retrying read inside it —
    including the 2s replace-collision wait of rule 17 — gets only what is
    left of that deadline, so the handshake ends at `--launch-timeout`
    however many reads collide, and `after=<d>` is the time that actually
    passed. The wait has a **second end condition**, because a timeout is
    the bound for a supervisor that is alive and merely slow and not a
    diagnosis: if the supervisor **exits** without having written an
    identity — it lost its compare-and-swap, it could not write the file, or
    it was killed — the runner reads the slot once more, in case the
    identify landed in the same instant, and then ends the handshake at once
    rather than waiting out a clock for a process that is gone, printing
    `RUN LAUNCH-FAILED id=<id> slot=<n> after=<d>: the supervisor exited
    without writing an identity` with `<job>/aborted.json`'s reason appended
    when that file carries this launch's `nonce`. On either end condition
    the runner kills the
    supervisor's group, writes `launch=failed` into the sidecar, moves the
    task to `failed/`, frees the slot under `slots.lock`, and prints `RUN LAUNCH-FAILED id=<id>
    slot=<n> after=<d>: no identity within <n>s` for the timeout; only after the handshake is
    `RUN START` printed, with the identified pid; (5) **release to work** —
    the supervisor spawns the harness as its child in the same group, with
    the key in the child's environment (rule 6), and holds the job's
    deadline and budget (rules 7 and 13); a **failed read of the slot file
    while a job is watched is not evidence that the job has ended** — the
    dispatcher asks the observables instead (the record is GONE, or
    `<job>/exit.json` carries this launch's `nonce`), with the job's own
    deadline, measured from its start, as the outer bound — and a job that
    ends with its slot file present and unreadable has that slot **retired
    for the rest of the run**, never freed — UNLESS the job's own
    `<job>/exit.json` carries this launch's `nonce` and `attest`, the
    supervisor's completion evidence, in which case the end is confirmed and
    the slot is freed: an unreadable file is an unanswered question and the
    observable answers it, while a file the dispatcher could not read and
    whose job left no such evidence it cannot call free, which is the answer
    rule 17 gives the same file at start-up; (6) **finalization** — when the
    harness exits, the supervisor writes `<job>/exit.json` with `{rc, signal,
    ended, survivors, nonce}` through `.tmp` and rename, **the durable completion
    evidence** — an `exit.json` whose `nonce` is not the slot file's is not
    evidence of anything and the slot is quarantined (rule 17), so a stale
    record from an earlier launch of the same job cannot finalize a later
    one — then exits; the runner, or an adopting dispatcher, reads that
    file, runs the group check of rule 11 and `finalize` of rule 12, and
    prints the job's one `RUN` line. (2026-09-12: `exit.json` also carries
    `attest`, the per-launch secret itself, written only in `endWith` after the
    job's whole process group is dead; its sha256 lives in the slot file's
    `exit_attest`, and a reader accepts the record as the supervisor's own word
    only when both the `nonce` and the `attest` match — an `exit.json` whose
    `nonce` matches but whose attestation is absent or wrong is quarantined,
    never reclaimed.) The runner never allocates a slot whose
    file exists in any state, so the worker cap `--workers` counts reserved
    slots as held. When no live `run` holds `<pool>/run.lock`, `supervise`
    refuses at exit 2 unless its nonce matches a readable slot in `reserved`,
    `orphaned` or `launched` state. This exception lets an already spawned
    supervisor reach the rule 18 identification or abort boundary after its
    runner dies. A matching slot admits that recovery path; it does not bypass
    the remaining launch checks. `supervise` remains an internal runner verb.
    (Stella, 2026-09-11: two files
    written by the parent are not one atomic step, and a replacement
    dispatcher cannot reap a process that is not its child. Stella, final
    read: one pool lock held for `run`'s life cannot also be the lock the
    child's identify takes, and a supervisor's death cannot precede its
    acknowledgement — hence `slots.lock`, and `aborted.json` before exit.)
    **The supervisor ignores SIGHUP**, for that same sentence: a supervisor is
    its runner's child in a group of its own, and when the runner dies POSIX
    has the kernel send a newly orphaned group that holds a stopped process
    SIGHUP and then SIGCONT. The hangup's default action ended a supervisor
    before identify, before `aborted.json` and before `exit.json` — a job that
    vanished with no durable evidence of any kind (ubuntu and macOS, measured
    2026-09-12). Nothing here ends a job by hangup: a job ends at its deadline,
    at its budget, at the runner's group kill, or by `stop`.
19. **The caches are shared per bench, and a finished slot's working bytes are
    reaped.** A native job's toolchain and modules are the same for every card
    under one root, so `native` and the supervisor point the harness child at
    **one shared cache root**, `<root>/cache`, by `GOMODCACHE`, `GOCACHE` and
    `NPM_CONFIG_CACHE`, and name it as a permitted write root beside the job
    directory (SPEC-SANDBOX rule 17). The root is never a job's data home: 120
    cards that each download the Go toolchain and every module into their own
    data home filled hulk and vision to 100% (issue #1048). `nova-swarm reap
    --root <dir> [--older <duration, default 1h>] [--dry-run]` removes, for
    every slot whose job published a `RESULT.md` or whose newest harness log is
    older than `--older`, the slot's `data/`, `tmp/` and `jobs/*/scratch`,
    keeping `RESULT.md`, `usage.tsv` and the logs, and prints `REAP OK
    slots=<n> freed=<bytes>`. A slot no result and no old log has is live and is
    untouched, and `run` calls the same function at task end unless the worker
    description sets `keep_data: true` (a task property, like a budget, never a
    default), which leaves every slot's `data/` where it is for a person who
    needs to read it. The shared `cache/` is never a reap target: it is the
    thing the next job reuses.

## The card is a pipeline, not a loop (issue #856)

Glenn, 2026-09-16, on why every tool call re-sent the context: *"The idea is for
it to have no memory between calls Rowan. The idea is to just do work."* In this
repository a card is a pipeline of stateless model calls, not an agent loop.
Each rule below carries the hurt that made it, and **Red tests for this
section** lists the red test for each rule: one per rule, seen red first,
against the fake harness and the fixture card, with no network.

P1. **One model call per step, and its input is exactly what the card names.**
    Each STEP that needs the model is one call whose input is exactly the named
    inputs -- a file or a line range, the rule, the previous step's output -- and
    whose output is one artifact: a test file, a patch, a RESULT line. Cost is
    the sum of the steps' inputs and the sum of nothing else. (Hurt: #855 ran
    1,068 cards, 62M input and 1,435M cache-read, because the harness carried a
    growing transcript so the model could decide a next step the card already
    named; the transcript was pure cost.)

P2. **The harness runs the tools, with no model call.** In the pipeline, the
    harness runs the tools, with no model call: clone, checkout, test run,
    commit and the RESULT copy are the machinery's, never a model turn, and the
    harness log shows each call's input size so the cost is a fact. (Hurt: the
    same #855 cards paid a model turn to decide to run the test the card had
    already named.)

P3. **No memory between calls.** A step never sees a transcript, a prior turn or
    a running agent; it sees the named inputs and nothing else. (Hurt: the
    growing transcript was the cost, and recollection was the only thing the
    loop used it for.)

P4. **`MODE: explore` is the one place the loop stays.** A read that must find
    where a rule lives may say `MODE: explore` on its own line; only then is the
    agentic loop admitted, because a search the card cannot name in advance is
    the one step whose next input is not known when the card is written. A card
    that does not say it is a pipeline. (Hurt: a rule that let every card loop
    is the 62M-input day.)

P5. **A fourth call without `MODE: explore` is refused, with the remedy line.**
    A pipeline card names at most three model calls. A card that asks for a
    fourth without the mode word is refused at admission, before any worker
    starts, and the refusal names the keyword, `MODE: explore`, and the rule it
    serves, because a refusal a caller cannot act on is a refusal wasted.
    (Hurt: the fix card that paid thirty turns of 66k for three steps.)

P6. **A `MODE: explore` card carries a turn budget the harness enforces.** The
    card names its budget on a `TURNS: <n>` line; the harness stops the card at
    that turn count and the partial RESULT names the budget, so an explore read
    that wanders ends on a number the card chose rather than on the deadline it
    was given. (Hurt: an explore read with no budget is the runaway the deadline
    could only end, and a deadline names no defect.)

P7. **The fix-card shape is three calls, not thirty turns.** Step 1 (model):
    inputs are the issue text plus the named test file and the named source
    file; output is the red test as a patch. The harness applies it, runs the
    test and captures the failing lines. Step 2 (model): inputs are the failing
    lines and the source file; output is the fix as a patch. The harness applies
    it and runs the package tests. Step 3 (model, tiny): inputs are the two
    patches' stat and the test tail; output is the RESULT lines. The harness
    commits and writes `RESULT.md`. Three calls of 10-60k tokens each instead of
    30 turns x 66k. (Hurt: the shape of #855's worst card, measured.)

**Red tests for this section.** One line per rule, seen red first:

- P1, P2, P3, P7: `TestTheFixCardRunsInThreeModelCalls` -- the fixture fix card
  runs in exactly three model calls and the harness log shows their input sizes;
  the fix-card shape is three calls, not thirty turns, and no memory between
  calls and the harness runs the tools, with no model call are the assertions
  the fixture makes on the log.
- P4, P5: `TestAdmissionRefusesAFourthCallWithoutExplore` -- a fourth call without `MODE: explore` is refused, with the remedy line, and the same card with `MODE: explore` is admitted.
- P6: `TestExploreOverTurnBudgetIsStoppedWithTheBudgetNamed` -- a `MODE: explore` card carries a turn budget the harness enforces: over its turn budget it is stopped and the partial RESULT names the budget.

This is the same shape as the pulse child (created, one job, exit) and the
meaning of "pull the intelligence up, push down to machinery."

## The verbs

```
nova-swarm add      --pool <dir> --task <file>|--stdin --files <n> --tokens <n>|unmetered [--label <text>] [--template <name>] [--profiles <file> --profile <id>] [--model <id>] [--deadline <duration>] [--max-input <bytes>]
nova-swarm batch    --pool <dir> --tasks <dir> --files <n> --tokens <n>|unmetered [--label <text>] [--template <name>] [--profiles <file> --profile <id>] [--model <id>] [--deadline <duration>] [--max-input <bytes>]
nova-swarm batch    --id <id> --cards <file> --deadline <seconds> --runner <cmd> --root <dir> [--idle <seconds>] [--benches <file>] [--bench <name>[,<name>...]] [--no-wall]
nova-swarm bench    probe --benches <file> --bench <name>
nova-swarm bench    size  --benches <file> --bench <name> [--max <n>]
nova-swarm native   --harness <path> --model <provider/model> --card <file> --slot <dir> --root <dir> --deadline <duration> [--label <text>] [--auth <file>] [--worker <file>]
nova-swarm reap     --root <dir> [--older <duration>] [--dry-run]
nova-swarm run      --pool <dir> --workers <n> --hours <h> --worker <file> [--profiles <file>] [--bench <name>] [--max <n>] [--no-auto-retry] [--launch-timeout <s>] [--usage-interval <s>] [--backoff <s>] [--sandbox <path>] [--no-sandbox]
nova-swarm supervise --pool <dir> --task <id> --slot <n> --nonce <hex> (--sandbox <path>|--no-sandbox)   (spawned by run; refused by hand, rule 18)
nova-swarm status   --pool <dir> [--max <n>]
nova-swarm stop     --pool <dir>
nova-swarm requeue  --pool <dir> --task <id> --task-file <file>|--stdin --files <n> --tokens <n>|unmetered [--label <text>] [--profiles <file> --profile <id>] [--model <id>] [--max-input <bytes>]
nova-swarm verdict  --pool <dir> --task <id> --who <name> --accurate <n> --wrong <n>
nova-swarm triage   --pool <dir> (--batch <id> | [--dir <dir>]...) [--since <stamp>] [--all] [--no-state] [--max <n>]
nova-swarm result   --pool <dir> --id <job>
nova-swarm template --name <read-pr|probe-row|fix-card|result|worker|profiles|setup|capacity>
nova-swarm cost     --pool <dir> [--since <stamp>] [--by model|day|repo] [--summary-only] [--max <n>]
nova-swarm note     --pool <dir> --task <id> --text <text>
nova-swarm finalize --pool <dir> --task <id>
nova-swarm version
nova-swarm reclaim  --pool <dir> (--task <id> | --done) [--max <n>]
nova-swarm verify    --result <file> --contract <line> --label <text> [--card <file>] [--max <n>] [--run-record <file>] [--usage <file>]
nova-swarm lint      --card <file> [--max <n>]
nova-swarm quickstart --pool <dir>
nova-swarm native    --harness <path> --model <provider/model> --card <file> --slot <dir> --root <dir> --deadline <duration> [--label <text>] [--auth <file>] [--config <file>] [--worker <file>]
nova-swarm publish   --job <dir> --branch <name> --base main --title <t> --body-file <f> [--touched <list>]
nova-swarm route     --card <file> --routes <routes.tsv> [--floor 0.9] [--default <worker json>] [--key-env <name>] [--base-url <url>]
nova-swarm help
```

`route` picks the worker by a typed decision behind a floor. It sends the
card's first 1500 characters as the state with four questions — `kind` a
choice among probe, read, spec, fix, feat and port; `complexity` a score over
four levels from one file mechanical to cross-cutting or under-specified;
`needs_strong` and `touches_private` noul — and reads a routes table of
`kind<TAB>complexity<TAB>worker json path<TAB>class` rows where class is
public (a free/contributor route) or paid. When `touches_private` is at or
above 0.5 the public-class rows are skipped; the row for (`kind`, rounded
complexity) wins, else the nearest lower complexity for that kind, else
`--default`. It prints one `ROUTE` line and exits 0, or 3 with
`worker=<default>` when the kind confidence is below `--floor` (a suggestion,
never an authorization: the caller keeps today's behaviour as the fallback),
or 2 on refusal.

`batch --root` and `native --slot`/`--root` are made **absolute and
symlink-resolved** at admission, and that one spelling is what reaches the
runner, `NOVA_SWARM_ROOT`, the wall's argv and every later compare. Absolute
alone is not enough: on darwin `/var` is a symlink to `/private/var`, so one
directory reached admission under two names depending on how the caller typed
it. A path that does not exist yet resolves its deepest existing ancestor.

`native --config <file>` copies an `opencode.json` beside the carried auth into
the job's data home, mode 0600. Only the provider named by `--model` is checked:
a config whose entry for THAT provider has no key in `--auth` is refused before
anything runs, naming the provider and never the key; a provider whose options
carry a `baseURL` and no `apiKey` (ollama on localhost) needs no key and is
admitted without one, so its card runs walled on the local model. Every other
provider in the file — a person's config names all of them — is copied verbatim
and not checked, because this run never calls it.

`--tokens <n>` is the token budget (rule 13). It has no default and `0` is
refused, on `add` and on `batch` alike, for the reason `--files` has none.

`--no-auto-retry` belongs only to `run`. It finalizes deadline and true-429
outcomes without an automatic same-text descendant, while preserving the
attempt's report, usage, failure reason and manual `requeue`. It is an
invocation policy, not task metadata: a later `run` that recovers an ended job
needs the flag again. Absent it, the default automatic behavior below applies.

`batch --tasks <dir>` queues one task per regular file directly under `<dir>`,
in name order, each with the same `--files`, `--tokens`, `--template` and
`--deadline`, and stamps every sidecar with one new `batch=<id>`; it prints
`BATCH OK id=<id> tasks=<n> pending=<n>` and exits 0, is `BATCH REFUSED` at
exit 1 when `<dir>` holds no regular file, and queues nothing at all when any
one file cannot be read (exit 2, naming it): a batch is all of its tasks or
none.

`triage --batch <id>` restricts the walk to the jobs whose sidecar carries
that batch id and prints `TRIAGE BATCH batch=<id> …`; an id no sidecar carries
is `TRIAGE REFUSED` at exit 1. Without `--batch` the line prints `batch=-`.

`result --id <job>` prints one `RESULT OK id=<id> rev=<sha12> class=<ok|clean|
plan-only|malformed> bytes=<n> from=<path>` line to stdout and then the
published report **verbatim**, from `<pool>/reports/<job>/RESULT.md` for a
finalized job or `<job>/RESULT.md` for a running one; it is the one path by
which a malformed report reaches a person (rule 15), it never goes through
`internal/oneline` for the body because the body is the thing asked for, and
it is `RESULT REFUSED` at exit 1 for an id not in the pool or a job with no
published report (`NO-RESULT`). It is never called by `run` or `triage`.

`finalize` writes the usage file for one ended job whose runner died before
doing it (rules 12 and 17); it is refused while the job's process group is
alive, and it is a no-op with `FINALIZE OK` if the file already exists. `run`
does the same thing for every ended job it adopts or reclaims, so the verb is
for a person and never for a loop.

(2026-09-12: a slot file that cannot be READ, and a slot file that is GONE,
never authenticate worker-written evidence. `finalize` reads the slot file
first; a read ERROR is a refusal that names the slot path, and an ABSENT slot
file is accepted only when a record the tool itself wrote outside the worker's
write set — the usage file — proves the job was already finalized; otherwise
the worker-written `exit.json` is refused and the outcome stays `unknown`.
Likewise the supervisor publishes the per-launch attestation only after it has
confirmed the job's process group is dead, using the identity it retained at
launch — the pgid and start stamp it recorded when it started the harness,
never the worker-writable pid file — and if the group cannot be confirmed dead
by the deadline it writes `exit.json` without the attestation and with
`end=unknown`.)

`--files <n>` is the file budget (rule 4). It has no default: a budget this
tool supplied would be a guess about somebody else's task. Zero is refused,
because a worker that may open no file is a worker asked for a plan.

`verdict` records a reader's counts for one task: how many of its findings
were accurate and how many wrong, by name, into the task's sidecar. It is the
only way `accurate` and `wrong` reach a batch line, and it is a person's act.

`lint --card <file>` checks one card's mechanical shape **before any spend**,
with no model and no probe: it reads the one file it was handed and names every
defect by check, line and excerpt. The checks are the shape tonight's card
deaths cost: line 1 starts with `RESULT: `; `STEP 1` clones or enters the repo
with `cd`; the `STEP` lines are numbered `1, 2, 3, …` in order; the card names
a reproducing test, or says `probe`/`read` when it only reads; it names a test
command (`go test`, `pytest`, …) or states `no tests`; it carries a deadline or
the words `finish within`; it names a file or a package; scratch is named
absolutely, never as a bare relative path; no `../` path appears anywhere,
because the wall refuses a path above the job; it never invokes `nova-sandbox`;
its final step writes `RESULT.md` with the `RESULT: ` line first; and the card
is under 12000 bytes. A card that satisfies all of them prints one line,
`LINT OK card=<name> checks=<n>`, and exits 0; a card that drifts prints
`LINT DRIFT card=<name> <check>: <line>: <excerpt>` for each finding and exits
2, so a caller can refuse to admit it. `--max`, default 20 and 0 for all, bounds
the printed findings and adds one `LINT MORE` line naming the remedy; it never
changes the verdict. The verb is the practice-17/18/23/25 shape made mechanical:
it is a check, not a judgment, and a card that passes it is admitted to the wall
rather than proven.

The binary is `nova-swarm`, and that is its only name.

**No guessed anything.** There is no default pool, no default worker
description, no default number of workers and no default deadline. A missing one
is exit 2 and `refusing to guess`. `--workers` is capped at **64** (Glenn,
2026-09-10) and a request above it is a **refusal**, not a silent clamp: the
prototype prints a note and clamps, and a caller who asked for 200 workers has a
belief about throughput that a note at the top of a log does not correct.

**The task text is never an argument.** `add` takes `--task <file>` or
`--stdin`. The prototype takes it as `$1`, which puts a multi-paragraph task
into the process table and into every `ps` a bench user runs, and a task carrying
a quoted rule carries quotes into a shell. A file or a stream, always.

**`--worker <file>`** names the legacy/default worker description: which provider,
which model, which env var the provider reads, which base URL, and where the key
file is. It remains accepted unchanged. An optional `--profiles <file>` names a
trusted profile catalog; a task's `profile` selects a complete worker profile
from it. The default worker is used when a task has no profile, so an existing
`--worker` invocation remains the compatibility path and this tool still has no
implicit opinion about whose model runs.

**A description names its key by `key_file` or by `secret`, exactly one (issue
#881).** `key_file` is the old shape: a path, the plaintext key read as data
(rule 6). `secret` is the NAME of an environment variable — `"secret":
"DEEPSEEK_API_KEY"` — delivered into the runner's own environment by `nova-secrets
exec` around the run (docs/SPEC-SECRETS.md, the second caller): the key is sealed
once and delivered at use, and it is **never a file on disk**. When `secret` is
set, `run` and `supervise` require that variable to be present and non-empty in
their own environment and pass it to the harness under the description's
`env_var`; a description with neither, or with `secret` set and the variable
absent, is refused naming the variable and the remedy — run the binary under
`nova-secrets exec --only <NAME> -- <this command>`. The value is never written
to any file, never printed, and never in a RUN or SUPERVISE line; the harness
config still carries the variable's NAME, never the value (the key section
below). The wall's probe has no key file to prove it cannot read, and runs its
other checks without one (SPEC-SANDBOX rule 10).

**`worker check <description.json>`** validates a description before any launch and
starts nothing. It loads the file with the same strict loader `run` and `native` use,
then asks what the loader does not: the harness is a name on `PATH` or a path that
exists and this machine will execute; `harness_args` carries both `{model}` and
`{prompt}`; with `--env`, a description's `secret` is present and non-empty in this
process's own environment (the variable is named and the value is never read into a
line); `worker_dir` exists or its nearest existing ancestor is a writable directory;
every `read_roots` entry exists; the deadline parses; `class`, when present, is `public`
or `paid`; and `max_cache_read` and `max_turns`, when present, are positive. It prints
one `WORKER OK <name> model=<m> provider=<p> class=<c>` line when the description can
launch, or one `WORKER DRIFT <field>: <why>` per problem, exit 2. `--max <n>` caps the
drift lines, 0 for all. No worker, provider or network call is made.

**`version`** prints the Conventions' one line — `nova-swarm <build identity>
<goos>/<goarch> <go version>`, exit 0 — from `internal/buildinfo`, the same
resolution every binary here uses. It takes no flags and no arguments.

**`native`** runs one card under a frozen configuration (issue #296): a native
harness binary, a slot strictly under the configured root, and a deadline held
by the machinery. Folding the local one-shot into the native verb,
`nova-swarm native --model ollama/<tag>` runs the card on the Studio's local
model with the same wall, card contract and RESULT rules; admission refuses with
one line `ADMIT REFUSED benchmark window open until <stamp>` when the file named
by `NOVA_BENCH_WINDOW` (or `~/.config/nova/bench-window`, a single RFC 3339
stamp) is in the future, so a local job never runs beside a benchmark.

**`native --worker <file>` makes the description the source of the model and of
the key** (issue #881): a key is authorized for one model only, the description
pins that one, and a `--model` whose model half differs is refused on one line
naming **both** models, exit 2, before any directory is made. Without `--worker`,
`native` keeps `--model` as today. A description that names `"secret": "<NAME>"`
(above) takes the key from this process's own environment — `nova-secrets exec`
set it around the run — so `native` passes NAME through to the harness's
environment by name and writes **no auth file** under the job; the harness config
it writes carries the description's own provider declaration, `{env:NAME}`, never
the value, and `--auth` and `--config` are refused with such a description. A
description whose key is a `key_file` is the legacy shape, and there `--auth`
still copies the provider's secret into the job's data home on disk — printing
one `NATIVE NOTE` line that it does — because the legacy shape is the one that
names a file.

**`secret` implies `env_var`, and the model gate compares one qualified name**
(issue #881): a description that names `"secret": "<NAME>"` but no `env_var`
loads with `env_var` defaulting to NAME — the key arrives by that NAME and the
harness reads it by that NAME, so the two fields are the same string unless the
description says otherwise — and `native --worker` compares provider/model as
one name, where a description's `model` without a slash takes the description's
`provider` as its prefix, so provider `opencode` with model `deepseek-v4-flash`
is `opencode/deepseek-v4-flash`, the name `--model` carries; a real mismatch is
still refused naming both, exit 2, before any directory is made.

**`native` owns `TMPDIR`, and it is outside every repository** (issue #460,
landed in #558). The job directory is a git repository — admission wants one — so a
`TMPDIR` under it makes every `t.TempDir()` a directory inside a repo, and a
test that asserts "not a repo" goes red on every card for a cause the card did
not make. `native` exports `TMPDIR=<slot>/tmp/<label>` into the harness
environment — the slot directory is never a repository — and prints
`tmp=<path>` on `NATIVE OK`; a card sets no `TMPDIR` of its own.

**The Go module and build caches are shared by the whole bench, not one per card** (card
8963). Go derives `GOMODCACHE` and `GOCACHE` from `HOME`, and a native run makes the child's
`HOME` its data home, so every slot downloaded its own module cache — and a toolchain — and
grew to five to seven gigabytes, almost all of it `data/go/pkg/mod` and
`data/.cache/go-build`, where a Lisp slot is 330 MB. Instead `native` makes
`<root>/cache/go-mod` and `<root>/cache/go-build` (mode 0755) before the child starts, hands
the harness `GOMODCACHE`, `GOCACHE` and `GOTOOLCHAIN=local`, and adds `<root>/cache` to the
wall's write set, so every card of the bench shares one cache and a card's data home holds
only harness state. The sharing is safe: Go's caches are concurrency-safe by design and the
module cache is read-mostly — a card extracts a module it needs and Go's own lock serializes
the write — and `GOTOOLCHAIN=local` keeps a card from fetching a toolchain the bench did not
pin. `--no-shared-caches` restores the old behaviour exactly, with no names set and the
caches under `HOME`, for a bench that wants one slot's caches isolated; the slot's data home
layout is otherwise untouched.

**The harness's own fence is configured by the run, never left to its
defaults** (issue #644). OpenCode asks before a tool touches a path it calls
external, and a `run` with no terminal answers every such question by rejecting
it — `permission requested: external_directory (<dir>/*); auto-rejecting` — after
which the model stops and publishes nothing. So `native` writes the harness
config for every run, whether or not `--config` named a provider config (the
provider's bytes and its own rules are carried beside the fence block, never
replaced), and its `permission.external_directory` allows:

- **the job directory and everything under it**, in both the `/*` and `/**`
  spellings — the harness's own matcher turns `*` into `.*`, which crosses `/`,
  so `<job>/*` already admits `<job>/scratch/*`; `/**` is written beside it
  because that is the spelling a person reads as "everything under here"; and
- **on a `--no-wall` run only**, every absolute path the card named on a
  `READ:` line, and that path's parent with a `/*` on it, which is what the
  fence asks about for a file. A walled run takes none of them: the WALL owns
  what the child may read (SPEC-SANDBOX rule 1), and the fence is not a wall.

**Nothing ABOVE the job is ever named** — not the `jobs` parent. The harness
resolves a card's `../scratch` after the card's own `cd repo`, so a parent rule
buys nothing, and on a bench with no wall it would hand one card every sibling
job in the slot.

**What is proven and what is not.** The reporting half is proven: a harness that
prints its rejection line and exits 0 is reported and scored `fence`. The
config half is **defence, not a demonstrated cure**: a real run of this harness
build (OpenCode 1.18.20) against a card that writes `../scratch` from `repo/`,
and against one that reads a path outside the job, was rejected NEITHER with the
block NOR without it, so this build was not shown to honour
`permission.external_directory` at all. The block pins what was otherwise left
entirely to the harness's defaults, and the rejections on the record
(`git worktree add ../wt-<n>` on the Studio, `/sys/kernel/security/lsm` on
Space) were not reproduced here.

Everything else is still `ask`, which in a `run` is a rejection — and a
rejection is now REPORTED: `native` reads its own capture and carries
`fence=rejected path=<p>` on its `NATIVE OK` line, which the batch reads and
scores `ABSTAIN reason=fence`, never `no-result`.

**A local model is one slot, and it stays off the critical path.** The fault
behind the silent-harness rule below (issue #591, landed in #604) was a local
model emitting its tool calls as raw text the harness does not parse, so no
tool ran and nothing was written: the local route needs a model whose
tool-call format the harness parses, the adoption probe tries the configured
local models in order and records which answer
(`probe-local-model-supports-tools`, open), and a batch gives the local route
one slot at most.

**A native run captures the child's output to `<job>/harness-output.log`,
walled or not** — the same file, the same bytes, alongside `<slot>/native.log`
— so an unwalled card's failure is as diagnosable as a walled one's.
`--no-wall` removes the containment and nothing else: it never removes the
evidence (issue #608, every Space no-result of 2026-09-16). **The name
`harness.log` belongs to the writers that already own it** — the legacy
supervisor, which pins the harness's own output there, and a `batch`, which pins
its runner's stdout there — and `native` never writes it: one file, one writer,
and the evidence of a native run is `harness-output.log`. **Every writer of a
job's logs appends and none truncates**, `batch`'s runner pipe and the
supervisor's own open included: two processes write a card's `harness.log` at
their own offsets, and a truncating open destroys the head of what the other
already wrote.

**A card's minutes are measured per phase, and the measurement is a file the native
run writes** (card 8964, Glenn 2026-09-17: *"speed up average wall clock time per
card; make each card operate more efficiently in tokens and in time from start to
finish; look at single cards"*). The harness log carried no timestamps, so no one
could say which of clone, deps, read, edit, test, retry and result spent a card's
wall. The harness now **reports its own model turns and tool calls on the child's
output** with an adapter grammar, two lines per span so both ends are observed and
never guessed — `NOVA-TIMELINE TURN BEGIN` / `END in=<n|-> out=<n|->` and
`NOVA-TIMELINE TOOL BEGIN name=<tool> cmd=<command to end of line>` / `END
name=<tool> rc=<n>` — and `native` **timestamps each report as it arrives**, holding
one row per model turn and per tool call in `<job>/timeline.tsv` beside the card's
`RESULT.md` and `usage.tsv`. The six columns are exactly `t_start`, `t_end`, `tool`,
`wall_ms`, `input_tokens` and `output_tokens`: a token count the harness did not
report is the empty cell, never a zero, and an event without a `BEGIN` (a span
already open closes where the next began) or without an `END` is dropped rather
than invented. **`nova-swarm profile --jobs <glob>` reads only those files** — it
launches no worker and calls no model — and prints one `PROFILE
job=<label> wall=<s> turns=<n> tools=<n> clone=<s> deps=<s> read=<s> edit=<s>
test=<s> retry=<s> result=<s>` line per job and one `PROFILE SUMMARY jobs=<n>
mean_wall=<s> ...` line with the mean per phase. The phase is inferred from the tool
call's command: `git clone`/`git fetch` is clone; `go mod`, and only the first `go
build`, is deps; `Read`/`Grep`/`cat`/`sed -n` is read; `Edit`/`Write` is edit; `go
test`/`run-tests.sh`/`make test` is test, and a test run after a failing test run
(`rc=` non-zero) is retry; a `RESULT.md` write is result. The wall is the span from
the first start to the last end, and a job whose harness reported nothing phases as
nothing rather than failing the fold.

**A silent harness is never OK, and this is the one definition of it.** The
`NATIVE OK` line always carries `harness=<ok|silent>`, and a run is `silent`
**iff** the capture above holds no line the child wrote **and** no `RESULT.md` is
found anywhere `gather` looks for one — the job root, `repo/`, one directory
below it (issue #594). Everything else is `ok`. Three consequences, each a fault
someone had: a harness that **spoke** and published nothing is `ok` and its card
scores `no-result`, because there is evidence to read; the wall's own `SANDBOX`
lines in that capture are **not** the harness speaking and are skipped, exactly as
the gather's `log=<n>` skips them, or a walled run could never be called silent;
and a result published under `repo/` is a run that worked, so the lookup is the
gather's own and never a shallower one. The fault that wrote the rule was a local
model whose tool calls the harness never parsed (issue #591): no tool ran, nothing
was written, the child exited 0 and the line said OK. `gather` scores such a card
`ABSTAIN reason=harness-silent`, before `no-result` and before `rc=<n>`.

**The wall's `SANDBOX OK` line is a producer's one-line record and `native` reads
it as one** (issue #572). The wall renders `cwd=<dir>` through the same
`internal/oneline` field encoding every path slot uses, so a job directory whose
path holds a space — the configured root under `stella 2` — arrives as one token
with the space escaped (`stella\x202`). `native` **decodes that field before it
compares the wall's cwd with the job directory**. Taking the escaped token
literally made the two spellings differ as strings while the refusal escaped both
again and displayed them identically, so a job that had already completed its
`RESULT.md` and its usage row was refused as a pre-launch failure that could be
replayed. The containment check is unchanged: a cwd that is not the job directory
still refuses, and only the one field the comparison reads is decoded.

`status`, `triage`, `result`, `template` and `cost` **report** and exit 0
(their refusals are exit 1 as the table says). `run`, `add`, `batch`,
`requeue`, `note`, `finalize` and `reclaim` are the verbs that act; `supervise`
is `run`'s child and nobody's verb.

## Batch: scatter, wait, gather

A batch is the `batch=<id>` already in the sidecar (rule 14), held through
four parts: **scatter** admits n cards with one batch id and one deadline;
**wait** ends every card or the deadline; **gather** folds the batch into one
bounded packet, mechanically; **read** hands the packet once to one agent.
The admission half is `batch --tasks <dir>` and the two proposals it cites —
`docs/PROPOSAL-SWARM-BATCH-RECEIPTS.md` and
`docs/PROPOSAL-SWARM-BATCH-ADMISSION-CONTRACT.md`. **This section adds no
dispatcher extension beyond those two proposals**: no new admission boundary,
no new `run` verb, and the receipts stay exactly as the proposals define them.

### scatter — one admission of n cards, one id, one deadline

- one admission of n cards — any route: Mercury, the DeepSeek API, OpenCode
  Go or Zen — one slot each, in `--tasks <dir>` name order (rule 14);
- one `batch=<id>`, stamped on every sidecar at admission, printed on
  `BATCH OK`;
- per-card **sha256 of the admitted text**, recorded at admission, the hash
  the receipt and admission-contract proposals already demand; line 1 of the
  card's `RESULT.md` must be that admission's contract line, or the card is
  refused at gather;
- one deadline for the whole batch — the batch's own `--deadline`, never a
  deadline any single card sets.
- one `BATCH` lock file per local slot the batch takes — `<root>/<slot>/BATCH`
  holding `id=<batch> pid=<n> at=<stamp>`, written at allocation and removed at
  slot end. Slots are unique across batches by the tool, never by the
  coordinator counting (issue #457); the paragraphs below say what a live and a
  stale lock each do.

**Admission is per card, never per batch.** A card refused at admission — a
shape refused under `docs/WORKER-CARDS.md` practice 17, or a repository it
cannot reach without credentials — is **one `ABSTAIN` row** with
`reason=admission <why>`, and every other card runs. The refusal is said once
on stderr, `ADMIT REFUSED <label> <why>`, and the `BATCH` line counts the card
under `abstain`. One card's shape never takes a batch down with it: on
**2026-09-15** one card whose *quoted issue text* held the word `launcher` made
`ADMIT REFUSED` for the whole batch and **34 cards never ran** (issue #529).
The practice-17 word check reads the card's **contract lines — lines 1-3: the
contract line, the role line and `STEP 1`** — and nothing below them, because a
card that quotes a launcher is a card *about* one, not a card run by one.

**One batch holds one slot, and the lock dies with its holder** (issue #457).
At slot allocation the batch writes `<root>/<slot>/BATCH` carrying
`id=<batch> pid=<n> at=<stamp>`, and removes it at slot end. A slot whose lock
is **live** — the holder's pid is alive — is not free: an auto-allocated card
skips it, and a card that *named* it is refused with
`ADMIT REFUSED slot=<n> held-by=<id> pid=<n>`, that card alone abstaining with
`reason=admission`. A **stale** lock, whose pid no process holds, is taken over
once and said out loud: `BATCH NOTE slot=<n> stale-lock id=<id> taken`. Two
batches that allocated at the same moment once took slot 1 twice and both cards
were lost. **A batch removes only the locks it took**, never the live lock of
the batch that refused it. This is per card, and it supersedes the whole-batch
refusal that landed with the lock in #568: an admission refusal is one card's,
the slot's as much as the shape's (issue #529).

**The batch allocates its own slots from a range** (issue #618). `--slots <lo>-<hi>` is
the range the batch may take, and the `slot` column of the cards file is **optional**: an
empty column or `-` asks for allocation, and the batch gives that card the lowest free slot
in `[lo,hi]`, skipping a slot whose lock is live. A hand slot inside the range is kept; a
hand slot **outside** it is refused at admission for that card alone,
`ADMIT REFUSED slot=<n> range=<lo>-<hi> card=<label>`, and never reaches the runner —
the fault this rule closes launched five read batches with slots 230-235 while the runner
refused everything above 228, and every card scored ABSTAIN `rc=2` with no log to read.

### wait — all end, or the deadline

`wait` blocks until every card has ended **or** the batch's deadline has
passed. A card past the deadline is an **abstain** row on the packet, **never
a hang**: the wait ends at the deadline and reports the stragglers; it does
not wait for them. The wait reads sidecars and usage files — the pool's own
accounting, never a card's process. **Missing contact is `unknown`, not
failure**: a card the machinery cannot reach is `unknown`, never failed.

- `--idle <seconds>` (default 300) is the per-card idle timeout, held beside
  the batch deadline, never instead of it. A card writes its own log —
  `harness.log` under its job directory, the runner's stdout pinned to a
  regular file — and the batch also reads the **CPU time of the card's whole
  process tree**, the runner's children and their children with it. **Idle
  means no child activity**: a card is alive while either its log grows or its
  tree's CPU time advances, and it is killed only when *neither* moved for
  `idle` seconds. On **2026-09-15** cards 664-670 were killed `idle 300s`
  inside a `go test` that prints nothing for minutes, and the loop raised
  `--idle` to 900 s, which only delays the same kill (issue #593): a busy
  silent harness is working, and a sleeping one is not. Idle is measured
  against that card alone: the batch returns on its **slowest still-working
  card**, not on the deadline, because a dead card is removed from the wait as
  soon as it stops moving. The tree is read once per poll for the whole batch,
  at most once per `idle/4` seconds and never faster than twice a second, by
  the kernel's own process table — never by matching a command line and never
  by running `ps`. A platform whose process table this repo cannot read
  watches the log alone, as it did before.
- A card killed for idleness is scored
  `<label> slot=<n>: ABSTAIN reason=idle=<s> log=<n> watched=<path>` on the
  packet — an abstain that names *why* it stopped and the log it watched, never
  a bare missing result — and the BATCH line's `idle=<n>` counts those kills.

### gather — one bounded packet, mechanically

`gather` reads every card's `RESULT.md` and folds the batch into **one
bounded packet**. **A result written inside the clone is the card's result.**
`STEP 1` makes `repo/` the model's cwd, so a model publishes `RESULT.md`
there; gather read only the job root, scored the card `no-result`, and the
work was lost (issue #594). Gather takes `RESULT.md` at the job root, else at
`repo/RESULT.md`, else one directory further down — `repo/<clone>/RESULT.md` —
copies it up to the job root and says so once on stderr,
`BATCH NOTE <label> RESULT.md copied up from <path>`. A job root that holds a
result of its own keeps it: nothing is ever overwritten, and **the `RESULT`
contract is unchanged** — line 1 is still the card's contract line, and the
rules below still decide. The fold itself is:

- the batch id and n;
- per-card disposition lines, **line 2 of each `RESULT.md`, verbatim**;
- evidence collapsed to **counts and `HOLD:` quotes only** — never a
  transcript, never a report body, never a finding's wording;
- usage per card and the batch total;
- bytes bounded: counts, not lists; the packet does not grow with the batch.
- `--then <command>` (optional) names a follow-on that runs only when every
  card is done and none stalled or idle-killed: the command runs once, with
  `sh -c`, in the batch's root, with `BATCH_ID`, `BATCH_DONE` and `BATCH_N`
  in its environment, and the packet prints `BATCH THEN rc=<n>`. A batch that
  is not all done prints `BATCH THEN SKIPPED done=<d> n=<n> abstain=<a>
  stalled=<s>` and exits 3, so the follow-on never runs on an abstain.

**A card whose `RESULT.md` line 1 is not its contract line is refused.** Line
1 is the card's contract line, the line by which it was admitted; a line 1
that differs before the end of that line is a different card, and folding it
would fold a stranger's words into the batch. The refusal names the card and
its line, and the card is `refused` on the packet, not folded — rule 15's
quarantine, applied to the batch.

**A `RESULT.md` carrying its contract line is `done` whatever the harness exit
code was**, unless the card abstained in its own words — a line 1 or a line 2
beginning `ABSTAIN`. The contract decides, never the child's rc and never its
timing. The card generator can truncate the issue title, so the card's
contract line may be a **prefix** of the `RESULT.md` line 1 rather than the
whole of it: a line 1 that **begins with** the contract line — after trailing
spaces are trimmed — is still this card and is `done`, and its longer tail is
named on the card line as `tail=<n>` (the number of chars past the contract
line). A line 1 that differs before the end of the contract line is a
different card and stays `line1-mismatch`.

**Every abstain names ONE reason token**, so the packet is the whole read and a
coordinator never opens a `RESULT.md` to learn why (issue #461):

| token | the card |
|-------|----------|
| `line1-mismatch` | published a result whose line 1 is not its contract line |
| `no-result` | ended with rc 0 and published no `RESULT.md`, at the job root or below it |
| `fence` | was stopped by the HARNESS'S OWN permission fence: it auto-rejected a path and the model stopped there, so the card never got to publish (issue #644). The path follows as `path=<p>` |
| `wall` | was stopped by the WALL — the harness's permission auto-reject line, or the sandbox's own `SANDBOX REFUSED` / `Operation not permitted` on a path outside the write set — and published no `RESULT.md`. The refused path and the last `STEP <n>` the card reached follow as `path=<p> step=<n>`, and one `WALL task=<id> path=<p> step=<n> [commits=<n> branch=<name>]` report line is printed on the notes so a harvester can still push the commits the dead card left (issue #644's follow-up) |
| `harness-silent` | its harness wrote nothing at all — no word in the run's capture and no `RESULT.md`, at the job root or below it — so the card never ran (issue #591) |
| `runner-refused` | its RUNNER exited before the harness started — no `NATIVE` line and no `harness-output.log` — so the non-zero exit code is the runner's, not the harness's; the runner's last line follows as `last=<line>` (issue #618) |
| `rc=<n>` | ended non-zero and published no `RESULT.md`, at the job root or below it, and its harness DID run |
| `idle=<s>` | was killed because neither its log nor its process tree moved for `<s>` seconds |
| `deadline` | was killed at the batch's deadline and published no `RESULT.md` |
| `result-after-deadline` | published a matching `RESULT.md` that only landed because the deadline fired, so it is late, not done |
| `card-abstain` | abstained in its own words: line 1 or line 2 begins `ABSTAIN` |
| `admission` | was refused at admission; the reason follows the token |
| `input-limit` | was refused for size, by the provider's own structured signal (issue #163) |
| `bench-unreachable` | ran on a bench the pull could not reach, so nothing about it is known here |

**The RESULT is the contract, and `harness-silent` is for a card that has none.**
A `RESULT.md` whose line 1 matches the card is **`done` whatever the harness exit
code was** (issue #577); the exit code is recorded on the card's own `NATIVE OK`
line and decides nothing here. The one timing that overrides it is the deadline: a
matching result that only landed because the deadline fired is
`result-after-deadline`, not done. So `harness=silent` and `reason=harness-silent`
apply **only when there is no matching result anywhere `gather` looks** — the job
root, `repo/`, and one directory below it (issue #594): `native` asks that same
question with that same lookup before it prints its line, and `gather` reads the
token off the line rather than guessing from files a batch creates itself.

**The order:** the result is read first, and a matching result decides — `done`,
or `result-after-deadline` at the deadline — before `card-abstain` (line 1 or
line 2 beginning `ABSTAIN`) and `line1-mismatch`. With no matching result: the
kill classes the machinery watched itself — `idle=<s>`, `input-limit` — and
`admission`, then `fence`, then `wall`, then `harness-silent`, then `deadline`, then the pair `no-result`
(ended clean) and `rc=<n>` (ended non-zero), which are one slot split by the exit
code. **`fence` and `wall` come before `harness-silent`, `deadline`, `no-result` and `rc=<n>`**:
a card the machinery's own fence or the OS wall stopped is neither a model that published
nothing nor a harness that never ran, and reading it as either sends a coordinator to the
model for a wall this tool built (issue #644). A card that published a matching `RESULT.md`
anyway is done: the wall is named only when the report is absent, because a wall that was
survived is not the card's end. **`rc=<n>` is never first**:
an exit code from a harness that never ran the card is nothing to go and read.
`idle=<s>` and `input-limit` are decided before the result is read at all — they
are what the machinery watched happen, true whether a result exists or not;
`deadline` is not, because a matching result at the deadline is late, not lost.

The card's line carries the token and its own log count —
`<label> slot=<n>: ABSTAIN reason=<token> log=<n>` — and at most one bounded
field after it where the remedy needs a path: `watched=<path>`, the log the
idle monitor watched, or `job=<dir>`, the job directory that holds no result.
A `done` card whose line 1 ran longer than its contract line carries one more
bounded field, `tail=<n>` — the number of chars past the contract line — so a
coordinator reads how the worker's title extended the generator's truncation;
an identical line prints no `tail` field. **A stall is `log=0`**: a card that
ended with no output after the wall opened is counted on the `BATCH` line's
`stalled=<n>` and reads its own emptiness on its line.

The copied-up result above is the same rule the bench pull holds under
**Benches**, rule 3 of the pull (#581), and both print the one `BATCH NOTE`
line. Replays this section demands, beside the tests #577 named:
`idle-watch-counts-child-activity` (`TestIdleWatchCountsChildActivity`, landed
in #603), `gather-copies-result-up-from-repo` (`TestGatherCopiesResultUpFromRepo`,
#603), `native-silent-harness-is-not-ok` (`TestNativeSilentHarnessIsNotOK`, PR
#604), `native-tmpdir-is-outside-any-repo` (`TestNativeTmpDirIsOutsideAnyRepo`,
#558), `local-route-is-one-slot` (two local-model cards in one batch: one runs,
one is `ABSTAIN reason=admission local route is one slot`; open), and, for the
range and the pre-run refusals (issue #618), `TestBatchAllocatesSlots`,
`TestBatchRefusesHandSlotOutOfRange`, `TestBatchScoresRunnerRefused` and
`TestBatchLineNamesUniformAbstain`; and, for the per-turn timeline and the phase
profile (card 8964), `TestNativeRunWritesTimeline` and `TestProfilePrintsPhases`.

### read — one agent, one packet, once

**One agent reads the one packet once** and carries the dispositions to the
pull requests — or `nova-review`'s outbox carries them. The packet is the
whole read; a reader never walks the reports behind it, because the reports
are the thing the packet replaced.

### The packet's grammar

```
BATCH <id> n=<n> done=<n> abstain=<n> in=<n> out=<n> usd=<sum> idle=<n> stalled=<n> [benches=<n>] [uniform-abstain=<reason>]
BENCH <name> slots=<n> done=<n> abstain=<n> in=<n|-> out=<n|-> usd=<x.xxxx>
<label> slot=<n>: <line 2, verbatim, capped> log=<n> [tail=<n>]
<label> slot=<n>: ABSTAIN reason=<line1-mismatch|no-result|fence|harness-silent|runner-refused|rc=<n>|idle=<s>|deadline|result-after-deadline|card-abstain|admission <why>|input-limit|bench-unreachable> log=<n> [watched=<path>|job=<dir>|path=<p>|last=<line>]
CARD <id> sha=<sha12> state=<done|abstain|unknown|refused> usd=<n.nnnn|-> line=<line 2, verbatim, capped> [wall=none]
ADMIT REFUSED <label> <why>
ADMIT REFUSED slot=<n> held-by=<id> pid=<n>
ADMIT REFUSED slot=<n> range=<lo>-<hi> card=<label>
ADMIT REFUSED bench=<name>: <reason>
BATCH NOTE slot=<n> stale-lock id=<id> taken
BATCH NOTE <label> RESULT.md copied up from <path>
HOLD: <one bounded quoted line>
```

`BATCH` is the packet's first line: the id, the admitted n, the cards done,
the cards abstain, the token and usd totals, `idle=<n>` — how many cards the
idle timeout killed — and `stalled=<n>`, how many ended with no output at all.
One card line per card, in admission order: its label, its resolved slot, and
either its disposition line — line 2 verbatim, capped — or `ABSTAIN` with its
one reason token, each carrying that card's own `log=<n>`. `ADMIT REFUSED` and
`BATCH NOTE` are said once on stderr, never inside the packet, so the packet's
bytes stay bounded by n. `HOLD:` lines carry evidence a count would hide, each capped. Counts
and caps bound the packet's bytes; a packet never lists a finding and never
quotes a report body.

**A batch that abstains uniformly is one PIT-STOP signal** (issue #618). When
every card in the batch abstains with the same reason token and none ran, the
`BATCH` line carries `uniform-abstain=<reason>`. It is one lost batch, not n
independent faults: `nova-pulse status` counts it as a pit stop, and the manager
policy escalates it at once and never requeues it. The fault that wrote the rule
left five read batches to the same mistake, and a coordinator who had to be
asked; the token is the thing nobody has to read a log to see.

### What this section does not do

- **No cross-batch scheduling.** A batch waits for its own cards and no
  other; nothing schedules one batch around another, and no batch is held
  for another's deadline.
- **No retries.** A failed card is a row on the packet; `requeue` with
  changed text is a person's decision (rule 11), and the only automatic
  retry is the admission contract's one `refused`/`write_before_task` retry.
- It does not judge findings, does not merge findings, and does not write to
  the board, the bus or any repository — it reads cards and builds one packet.

## Benches: a remote bench reached by ssh, with pinned cores

A **bench** is a machine that runs slots: the local machine, or any machine
the caller can `ssh` to, with no assumption about our fleet (Glenn,
2026-09-15: "we can help people, and ourselves, by extending nova-swarm to
support remote swarms"). The Studio ran 40 cards at once and its load
average reached 47 (2026-09-15): one bench is the width of a pulse, and a
second bench doubles it. Nothing here changes
what a card is, what a slot is, or what `gather` reads.

**The benches table** is `--benches <file>`, one header line and one row per
bench, tab-separated, these columns in this order. There is no default path,
and nova-tools keeps no host name in source or in a committed file: without
`--benches` the row `local` is the only row that exists. The coordinator's
bench writes the file, or nova-work's fleet registry emits it — the fleet
registry is nova-work's; when it exists, `nova-work fleet --benches-out
<file>` writes this file and swarm reads it; until then a person writes it.

| column | what it holds |
|---|---|
| `name` | one word; the local machine is the row `local` |
| `host` | an ssh alias from the caller's own ssh config, or `local` |
| `root` | the swarm root on that host, absolute there |
| `cores` | a `taskset` list, `1-15` or `2,4,6`, or `-` for no pinning: every darwin bench, and a linux bench without `taskset` |
| `harness` | the harness binary on that host, absolute there |
| `auth` | the harness auth file on that host, absolute there, mode `0600` |
| `wall` | `sandbox` or `none`: what `bench probe` found, and what a batch may run |

The table is data: nothing in it is executed, and a host is only ever an
argument to `ssh`. The row `local` with `host=local` is the machine the batch
runs on and behaves exactly as a batch with no table: `--runner` runs its
cards and no `ssh`, `taskset`, copy or pull happens. A relative `root`,
`harness` or `auth`, a `cores` list that does not parse, a `wall` that is
neither word, or a name used twice is `BATCH REFUSED` at exit 2 naming the row.
One row, as an example only — the tool ships none:

```
b2	b2	/home/me/swarm	1-15	/home/me/.local/bin/opencode	/home/me/.config/nova/auth	none
```

**The auth file is placed by a person, never by the tool.** The tool never
reads, prints or copies `auth`; it names the path on the bench's `native`
command line and checks that it exists with mode `0600` (`stat`, never a
read). Rule 6 seen from a bench: the key is data, and here not even data this
tool holds.

**`--bench <name>[,<name>...]` on `batch --cards`** allocates the cards across
the named benches. The cards file's `slot` column is `<n>` (local slot n, as
today), `<bench>:<n>` (slot n on that bench), or `-` (unassigned). An
unassigned card is dealt in `--bench` order — `local` first when it is named,
then the others round-robin — each bench giving the lowest slot number it has
not yet given; a bench whose cores are all taken leaves the rotation, and a
bench with `cores=-` never does. A card naming a bench not in `--bench` is
`BATCH REFUSED` at exit 2 naming the card and the bench. A friend is never a
bench: names are checked against the friends list the bus knows, a match is
`ADMIT REFUSED bench=<name> is a friend`, and `--bench` sends nothing on the bus.

**Pinning is a per-row fact, not a swarm constant.** Where `cores` is a list,
slot `n` runs on the `n`-th core (`1-15`: slot 1 on core 1, slot 15 on core
15), admission requires `taskset` on the bench, and more slots than cores is
`ADMIT REFUSED bench=<name> slots=<n> cores=<n>` before any card starts,
because an ssh session lands on core 0 and an unpinned process would share
the OS's core. Where `cores` is `-` (darwin, or linux without `taskset`) no
`taskset` is required or invoked, and the local darwin row is byte-for-byte
today's behaviour. On a remote bench `--runner` is not used: the batch builds
the `native` command itself from the bench row and the card row, and the run is

```
ssh <host> setsid [taskset -c <core>] <root>/bin/nova-swarm native --harness <harness> --model <model> --label <label> --card <root>/cards/<label>.md --slot <root>/<n>/jobs/<label> --root <root> --deadline <s> --auth <auth> [--no-wall]
```

whose first output line is `RUN pgid=<n>`, the remote process group `native`
runs under, printed before any card work so the batch can name it. **The
card is the only file copied out**, to `<root>/cards/<label>.md` by `rsync`
(or `scp`), before the run; the repository is cloned by the card on the bench
as today, and no runner script, config or key crosses the wire. **Three files
come back** after the run, into the local root at
`<root>/<bench>-<n>/jobs/<label>/`: `RESULT.md`, `usage.tsv`, and the last
64 KiB of `native.log` — so `gather` reads what it reads today, one directory
per card, and nothing in `gather` knows a bench exists.

**The pull is three rules, and a bench run lost its results to each of them on
2026-09-15.** (1) The pull **waits up to 30 s for `RESULT.md` to exist on the
bench**, asking once a second: the remote slot's shell returns before the
harness's last write has landed, and a pull that copies at once copies nothing.
(2) Each file is **its own explicit `scp`**, named on both sides — never one
`rsync` with an include filter, because a filter that matches nothing exits 0
and a copy of nothing then reads as a card that abstained. (3) When `RESULT.md`
is absent from the job but present under `repo/`, or one directory below it, it
is **copied up into the job** and the batch prints

```
BATCH NOTE <label> RESULT.md copied up from <path>
```

so the card's mistake is on the record and its work is not lost to it (#581
landed this line as `SPACE NOTE …`; a host word in an output token is wrong,
and it is renamed by #602). A pull
that cannot reach the bench at all — `ssh`'s own exit 255, not a remote command
saying no — scores the card `ABSTAIN reason=bench-unreachable`; a bench that
answers and holds no result is the ordinary missing-result abstain and not that.

**Deadline and idle are held here, on the local machine, as today.** The batch
process owns the batch deadline and the per-card `--idle`. The local wrapper
holds its `ssh` child in a process group of its own; on deadline or idle it
sends `SIGTERM` to that group, then `ssh <host> pkill -TERM -g <pgid>` with
the pgid from the `RUN pgid=<n>` line, then `-KILL` after 5 s, and the card is
scored as a local card: `<label>: ABSTAIN reason=deadline`, or `reason=idle=<s>`
counted in `idle=<n>`. If `ssh` itself is unreachable then, the slot is
`<label>: ABSTAIN reason=bench-unreachable` and gather records what was pulled
(`RESULT.md` absent, `usage.tsv` absent) with the last local log line. A
network drop after `RESULT.md` was written is recovered by a second pull at
gather: one retry, 30 s, none after. The reason in `ABSTAIN reason=<token>` is
one token, the set issue #461 gives every card: `line1-mismatch | no-result |
fence | harness-silent | rc=<n> | idle=<s> | deadline | result-after-deadline | card-abstain |
admission <why> | input-limit | bench-unreachable`, and the card's line carries its own `log=<n>` after it. The idle watch on a
remote card asks `ssh <host> stat -c %s <root>/<n>/jobs/<label>/native.log` —
bytes, the growth a local log is measured by, never an mtime (rule 16) — no
more than once per `--idle/3` seconds. A bench unreachable at a poll is not a
dead card: the card stays `unknown` until the batch deadline, when it is
`ABSTAIN reason=bench-unreachable` (**wait**: missing contact is `unknown`, not failure).

**The cost of a bench, per card:** one `ssh` and one `rsync` at the start,
one `rsync` at the end, one `ssh stat` per `--idle/3` while it runs, and
nothing per poll beyond the idle watch the batch already keeps.

**The wall on a bench** is whatever `nova-sandbox check` reports there: on
darwin `sandbox-exec`, and on linux `landlock` with the kernel's Landlock ABI
wherever that kernel has it — the Landlock body is built (SPEC-SANDBOX,
"Linux"), so a linux bench is `wall=sandbox` like any other and needs no
`--no-wall`. A linux kernel without Landlock still reports `none`. A bench runs unwalled only when **both** the
table says `wall=none` and the batch was typed with `--no-wall`, passed
through to `native` on the bench; the `--no-sandbox` paragraph above is the
rule here, unchanged — argv, never a default, never implied by a missing
backend — and the batch prints its line, `RUN UNSANDBOXED id=<label>
slot=<bench>:<n>: no OS containment; every read and write this job makes is
yours`, on stderr before the card starts. A `wall=none` bench without
`--no-wall` is `BATCH REFUSED` naming the bench and the flag. The bench's
`NATIVE OK` line carries `sandbox=none-by-flag` as it does today, and the
card's `CARD` line in the packet carries `wall=none`, so the packet a reader
holds says which cards ran unwalled.

**A bench's width is a measured power of two, and the loop fills to it and
drops down under load** (Glenn, 2026-09-15, #528, open; the same rule is
SPEC-PULSE **Rate and convergence** 3). `bench size --benches <file> --bench
<name> [--max <n>]` runs the known-answer card `W` times concurrently for
`W = 1, 2, 4, ...` and keeps doubling while three rules hold at the end of each
round: (a) the one-minute load is at most `1.25 x cores`; (b) throughput
scales — cards per minute at `W` is at least `1.5 x` cards per minute at
`W/2`; (c) no card abstained (idle, deadline, refusal). The width is the last
`W` that held. It is recorded on the bench row as three optional trailing
columns — `width`, `measured` (a stamp), `version` (the tool's sha8) — a row
without them is unmeasured and fills by cores as today; the adopt step
re-measures whenever the tool version or the machine changes. A batch fills a
bench up to its width and, per tick, launches at most `cores x 1.5 - load`
cards, never more than `cores` in one tick — the bench's **headroom** (1.25 was
the first setting; Glenn raised it: be aggressive) — so a loaded or less
capable bench drops down without changing its width. `bench size` ends with one line:

```
BENCH WIDTH bench=<name> width=<W> cores=<n> rows=<n>
```

**Presence of a bench is not presence of a friend.** A bench answering ssh
says a machine is up; nothing here writes a presence line, and `nova-wake` is
untouched.

**`bench probe --benches <file> --bench <name>`** proves one bench before a
batch is pointed at it: one line per check, each bounded, every check run, in
this order — ssh reachable; `root` writable (one probe file written and
removed); `<harness> --version` runs; `<root>/bin/nova-swarm version` on the
bench equals this binary's own `version` line, refused on a mismatch naming
both; `cores` valid against `nproc --all` there, or `-`; pin, `pin=taskset`
when `taskset` is on the bench's `PATH` and `pin=none` when not, a refusal only
when `cores` is a list and `pin=none`; `auth` present with mode `0600`, never
read; the wall as `nova-sandbox check` reports there. It ends with one line,
exit 0 or 1:

```
BENCH CHECK name=<name> check=<ssh|root|harness|version|cores|pin|auth|wall> ok=<true|false> [<one bounded value>]
BENCH OK name=<name> cores=<n|-> pin=<taskset|none> wall=<sandbox|none>
BENCH REFUSED name=<name> check=<first failing check>: <reason> (more <n>)
```

With `--bench` the `BATCH` line gains `benches=<n>`, followed by one `BENCH`
line per named bench, before the first `CARD` line; `in` and `out` are the
bench's token sums from the pulled `usage.tsv` rows, `-` when none came back;
the packet's ceiling is `N + 12 + benches` lines. Without `--bench` no line
changes and no table is read.

```
BATCH <id> n=<n> done=<n> abstain=<n> usd=<sum> idle=<n> benches=<n>
BENCH <name> slots=<n> done=<n> abstain=<n> in=<n|-> out=<n|-> usd=<x.xxxx>
```

### What this draft does not do

- **No bench discovery, no registry.** Every bench is a row a person wrote or
  nova-work's fleet registry emitted; swarm keeps no hosts of its own.
- **No bus traffic.** `batch --bench <name>` wakes nobody: a friend's name is
  refused at admission, and no line is written to the bus.
- **No scheduling by load beyond headroom.** Slots go by width, core count and
  `--bench` order; the one load the tool reads is the bench's own one-minute
  average, to launch no more than its headroom per tick (#528). A person still
  stops what else runs on a bench before a batch, and the tool never touches it.
- **No key handling.** The auth file is placed by a person; ssh keys are the
  caller's own ssh config.
- **No Windows benches, no shared root between benches, no card moving from
  one bench to another** once admitted.

### Replays this section demands

Each runs against a fake `ssh` (and `rsync`) on `PATH` that records its argv
and answers from a fixture, inside `t.TempDir()`, red before green.

1. `bench-table-parsed` — the seven columns, a `-` cores row, and each refusal above by name.
2. `bench-probe-refuses-version-mismatch` — `BENCH REFUSED check=version` names both identities.
3. `bench-probe-never-reads-auth` — the fake ssh sees `stat` on the auth path and never a read of it; mode `0644` is a refusal.
4. `batch-pins-slot-to-core` — slot 3 on `cores=1-15` runs under `taskset -c 3`, and every remote argv carries `taskset`.
5. `batch-refuses-more-slots-than-cores` — 16 slots on `1-15` is `ADMIT REFUSED bench=b2 slots=16 cores=15` and no card starts.
6. `batch-copies-card-only` — exactly one file crosses before the run, and it is the card.
7. `batch-pulls-result-and-usage` — `RESULT.md`, `usage.tsv` and a bounded `native.log` land under `<root>/<bench>-<n>/jobs/<label>/`, and `gather` folds them unchanged.
8. `batch-line-has-bench-lines` — `benches=2` and two `BENCH` lines whose `done` sum to the `BATCH` line's.
9. `deadline-kills-remote-group` — at the deadline the local ssh child's group gets `SIGTERM`, the fake ssh sees `pkill -TERM -g <pgid>` with the pgid from the `RUN pgid=` line, then `-KILL`, and the card is `ABSTAIN reason=deadline`.
10. `unwalled-bench-needs-no-wall-and-marks-result` — `wall=none` without `--no-wall` is refused; with it the `RUN UNSANDBOXED` line prints, `native` gets `--no-wall`, and the `CARD` line carries `wall=none`.
11. `local-row-unchanged-behaviour` — a cards file of bare slot numbers and no `--bench` gives the same argv, files and packet as before this section, byte for byte.
12. `remote-idle-watch-reads-size` — the idle poll is `stat -c %s`, at most once per `--idle/3`, and an unreachable bench leaves the card `unknown` until the deadline, then `ABSTAIN reason=bench-unreachable` with both files absent and the last local log line recorded.
13. `bench-name-is-not-a-friend` — a `--bench` name on the bus's friends list is `ADMIT REFUSED bench=<name> is a friend`, and the fake bus sees no line.
14. `gather-retries-pull-once` — a pull that fails after `RESULT.md` exists on the bench is retried once at gather, 30 s later, and folds as done; a second failure is `no-result`.
15. `pin-none-row-admits-without-taskset` — `cores=-` on a bench whose fake `PATH` lacks `taskset` probes `pin=none`, admits, and its argv carries no `taskset`; `cores=1-15` on the same bench is `BENCH REFUSED check=pin`.
16. `pull-waits-for-result` — the bench holds `RESULT.md` back until the third ask; the pull
    waits, then copies, and each of the three files is its own `scp` naming one file, with no
    filter and no pattern on any argv (`TestPullWaitsForResult`).
17. `pull-copies-result-up-from-repo` — a card that wrote `RESULT.md` under `repo/` one level
    down has it copied up into the job, pulled back, and `BATCH NOTE <label> RESULT.md copied
    up from <path>` printed (`TestPullCopiesResultUpFromRepo`; #602 renames #581's line).
18. `pull-scores-bench-unreachable` — a bench whose `ssh` exits 255 scores its card `ABSTAIN
    reason=bench-unreachable`, not a plain abstain and not a stall
    (`TestPullScoresBenchUnreachable`).
19. `size-doubles-until-a-rule-breaks` — a fake bench whose load crosses `1.25 x cores` at
    `W=16` records `width=8`; one whose throughput at 8 is under `1.5 x` its throughput at 4
    records `width=4`; one abstain at any round ends the doubling there.
20. `size-records-width-with-version` — the row gains `width`, `measured` and `version`, and
    a `version` unequal to the running tool's is re-measured by the adopt step.
21. `launch-fills-to-width` — a bench with `width=8`, 16 cores and no load is given eight
    cards from a batch of twelve, and the four wait in the queue rather than a ninth slot.
22. `launch-drops-down-under-load` — the same bench at load 6 is given `16 x 1.5 - 6 = 18`,
    capped at `cores` 16 and then at its width 8; at load 20 it is given four; at load 24 it
    is given none, its width unchanged on the row.

## Exit codes

| code | meaning |
|------|---------|
| 0 | the verb ran and passed: a task queued, a batch queued, a pool drained, a page written, a report printed |
| 1 | the verb ran and said **NO**: a dispatcher that exited with tasks still pending and nothing running, a `requeue` of an id that is not in the pool, a `triage` over a directory that holds no reports when one was named, a `reclaim` of a job with no usage file or no report copy, a `finalize` of a job whose process group is alive, a `run` that ended with a quarantined slot or a `LAUNCH-FAILED` job, a `batch` over a directory with no task file, a `triage --batch` of an id no sidecar carries, a `result --id` of an id not in the pool or with no published report, a `run` that refused a task whose prompt is over its `max_input` |
| 2 | could not run: missing flag (`--files`, `--tokens` included, on `requeue` as on `add`), a numeric `--tokens` on a pending task under a worker description whose `usage` is `none`, unreadable pool, unreadable worker description, a key file that is absent or empty, a description whose `secret` variable is absent or empty in the runner's own environment (naming the variable and `nova-secrets exec --only <NAME>`), `--workers` above the cap, a `batch` with an unreadable task file, a `supervise` typed by hand, bad invocation |

**A failed task is not a failed `run`.** A worker that exits non-zero moves its
files to `failed/` and the pass continues; `RUN OK` carries `failed=<n>` and
exits 0. A dispatcher that cannot start anything at all — no key, no harness on
`PATH` — is exit 2, before it starts the first worker, which is the point at
which the caller can still fix it.

A missing or empty key file is **exit 2 with the command that creates it** in the
refusal, and the refusal never prints the path's contents. The prototype does
exactly this and it is worth pinning as a contract: the refusal says what the
input wants.

## Output grammar

```
ADD OK id=<id> label=<label> template=<name|-> deadline=<d> files=<n> tokens=<n|unmetered> batch=<id|-> pending=<n>
ADD REFUSED: <reason>
BATCH OK id=<id> tasks=<n> pending=<n>
BATCH REFUSED: <reason>
BATCH <id> n=<n> done=<n> abstain=<n> in=<n> out=<n> usd=<sum> idle=<n> stalled=<n> [benches=<n>] [uniform-abstain=<reason>]
BATCH THEN rc=<n>
BATCH THEN SKIPPED done=<d> n=<n> abstain=<a> stalled=<s>
BATCH NOTE slot=<n> stale-lock id=<id> taken
BATCH NOTE <label> RESULT.md copied up from <path>
BENCH <name> slots=<n> done=<n> abstain=<n> in=<n|-> out=<n|-> usd=<x.xxxx>
<label> slot=<n>: <line 2, verbatim, capped> log=<n> [tail=<n>]
<label> slot=<n>: ABSTAIN reason=<line1-mismatch|no-result|fence|harness-silent|runner-refused|rc=<n>|idle=<s>|deadline|result-after-deadline|card-abstain|admission <why>|input-limit|bench-unreachable> log=<n> [watched=<path>|job=<dir>|path=<p>|last=<line>]
CARD <id> sha=<sha12> state=<done|abstain|unknown|refused> usd=<n.nnnn|-> line=<line 2, verbatim, capped> [wall=none]
ADMIT REFUSED <label> <why>
ADMIT REFUSED slot=<n> held-by=<id> pid=<n>
HOLD: <one bounded quoted line>
BENCH CHECK name=<name> check=<ssh|root|harness|version|cores|pin|auth|wall> ok=<true|false> [<one bounded value>]
BENCH OK name=<name> cores=<n|-> pin=<taskset|none> wall=<sandbox|none>
BENCH REFUSED name=<name> check=<first failing check>: <reason> (more <n>)
BENCH WIDTH bench=<name> width=<W> cores=<n> rows=<n>
RUN POOL workers=<n> hours=<h> worker=<name> model=<model> auto_retry=<true|false> pool=<dir>
RUN START id=<id> slot=<n> pid=<n> pgid=<n> started=<stamp> deadline=<d> tokens=<n> job=<path> [profile=<id> model_requested=<id> model_observed=<id>]
RUN LAUNCH-FAILED id=<id> slot=<n> after=<d>: <reason>
RUN ADOPT id=<id> slot=<n> pid=<n> started=<stamp> remaining=<d>
RUN RECLAIM slot=<n> id=<id> end=<done|killed|failed|budget|budget-unverifiable|violation|input-limit|provider|wall|unknown|unlaunched> dest=<done|failed|-> usage=<path|-> requeued=<true|false> [profile=<id> model_requested=<id> model_observed=<id>]
RUN QUARANTINE slot=<n> id=<id|->: <reason>
RUN BUDGET id=<id> slot=<n> spent=<n> of=<n> findings=<n>
RUN BUDGET-UNVERIFIABLE id=<id> slot=<n> samples=3 findings=<n>: <reason>
RUN MALFORMED id=<id> slot=<n> line=<n> dest=failed
RUN INPUT-LIMIT id=<id> slot=<n> after=<d> input=<n|-> max=<n|-> dest=failed: <the provider's own words>
RUN PROVIDER id=<id> slot=<n> after=<d> attempts=<n> dest=<failed> provider=<ref|-> requeued=<true|false>: <the provider's own words>
RUN DONE id=<id> slot=<n> rc=<n> after=<d> result=<ok|clean|no-result|plan-only|malformed> findings=<n> refusals=<n> notes=<sent>/<read|-> unpublished=<true|false> budget=<spent|n+|->/<n> dest=<done|failed> [log=<one bounded line of what the harness said>]
RUN VIOLATION id=<id> slot=<n> background=<n> dest=failed: <reason>
RUN KILLED id=<id> slot=<n> after=<d> deadline=<d> findings=<n> unpublished=<true|false> budget=<spent|n+|->/<n> survived=<true|false> requeued=<true|false> reaped=<1|2>
RUN MORE kind=<task> shown=<n> total=<t> nova-swarm status --pool <dir> --max 0
RUN OK started=<n> done=<n> failed=<n> killed=<n> pending=<n> recovered=<n> auto_retry=<true|false> after=<d>
RUN NOTE <the one remedy line>
RUN UNSANDBOXED id=<id> slot=<n>: no OS containment; every read and write this job makes is yours
SUPERVISE FAILED slot=<n> id=<id>: <reason>
RUN REFUSED: <reason>
RUN REFUSED reason=<sandbox_probe|no_sandbox>: <reason>
NATIVE REFUSED: <reason>
ADMIT REFUSED benchmark window open until <stamp>
NATIVE OK label=<id> job=<id> tmp=<path> rc=<n> wall=<n>s sandbox=<path|-> card_sha256=<sha> binary_sha256=<sha> config=<sha8|-> harness=<ok|silent> [fence=rejected path=<p>] [usage=none reason=<r> path=<p>]
STATUS TASK id=<id> state=<pending|running|done|failed> slot=<n|-> for=<d|-> tail=<one line>
STATUS OK pending=<n> running=<n> done=<n> failed=<n> slots=<n>/<n> quarantined=<n>
STATUS MORE kind=<task> shown=<n> total=<t> nova-swarm status --pool <dir> --max 0
TRIAGE REPORT id=<id> rev=<sha12> job=<name> result=<ok|clean|plan-only> items=<n> red=<n> green=<n> notdone=<n>: <head>
TRIAGE QUARANTINED id=<id> rev=<sha12> line=<n>: not folded; nova-swarm result --pool <dir> --id <id>
TRIAGE INPUT-LIMIT id=<id> job=<label>: <the provider's own words>
TRIAGE SKIPPED id=<id>: changed while read
TRIAGE FINDING jobs=<id>[,<id>...] at=<file:line|->: <one bounded finding line>
TRIAGE MORE kind=<report|finding> shown=<n> total=<t> at=<path> --max 0
TRIAGE BATCH batch=<id|-> reports=<n> findings=<n> new=<n> dup=<n> unquoted=<n> clean=<n> plan_only=<n> no_result=<n> malformed=<n> budget=<n> accurate=<n|-> wrong=<n|->
TRIAGE OK folded=<n> template=<n> malformed=<n> skipped=<n> items=<n> red=<n> green=<n> notdone=<n> page=<path>
TRIAGE REFUSED: <reason>
RESULT OK id=<id> rev=<sha12> class=<ok|clean|plan-only|malformed> bytes=<n> from=<path>
RESULT REFUSED: <reason>
VERDICT OK id=<id> who=<name> accurate=<n> wrong=<n>
VERDICT REFUSED: <reason>
COST TASK id=<id> attempt=<n> end=<word> in=<n|-> out=<n|-> cache_write=<n|-> cache_read=<n|-> reasoning=<n|-> usd=<n.nnnn|-> model=<model> repo=<repo|->
COST OK tasks=<n> in=<n> out=<n> cache_write=<n> cache_read=<n> reasoning=<n> dashes=<in>,<out>,<cw>,<cr>,<r> usd=<n.nnnn|-> window=<stamp>..<stamp> known_usd=<n.nnnn> usd_missing=<n>
COST REFUSED: <reason>
REQUEUE OK id=<id> from=<old-id> changed=<true>
REQUEUE REFUSED: <reason>
NOTE OK id=<id> notes=<n>
NOTE REFUSED: <reason>
FINALIZE OK id=<id> usage=<path> existed=<true|false>
FINALIZE REFUSED id=<id>: <reason>
RECLAIM OK id=<id> freed=<bytes> usage=<path>
RECLAIM REFUSED id=<id>: <reason>
RECLAIM MORE kind=<task> shown=<n> total=<t> nova-swarm reclaim --pool <dir> --all --max 0
STOP OK pool=<dir> running=<n>
QUICKSTART OK pool=<dir> pending=<n> next=add,run,triage
QUICKSTART NOTE <one remedy line>
PUBLISH OK branch=<name> head=<sha> pr=<url>
PUBLISH REFUSED: <reason>
```

The [profile proposal](SPEC-SWARM-PROFILES.md) additionally specifies
`RUN REFUSED profile=<id> reason=secrets_gate code=125` for a profiled credential-gate
refusal. This is a proposed profile-only variant, not a replacement for the
existing worker refusal field sequences above; its implementation and
variant-specific grammar checks remain part of the profile work.

`RUN POOL` is the first line of every dispatcher run and it says what will run
before anything runs: the provider, the model, the pool and the effective
automatic-retry policy. In legacy mode it says what the default worker would
run; profiled jobs carry their resolved profile and model on their per-job
`RUN START`/`RUN RECLAIM` and receipt records. `RUN OK` repeats that policy in
the terminal summary. **Neither line prints the key, the key file's contents,
or the env var's value** — only the variable's name, where a name is needed at
all.

**Every listing is a cap and a count**, per SPEC.md. `run`, `status`, `triage`,
`cost` and `reclaim` take `--max <n>`, default 20, `0` for all, one MORE line naming the
remedy. On `run`, `--max` limits only displayed `RUN` task lines; it never
limits admissions, workers, attempts or retries. The counts are the truth about
the **pool**, never about the output.
A `status` over a 67-task pool printed 67 lines in the prototype; the finding a
reader wanted was one of them.

**`RUN NOTE` is exactly one remedy line.** If anything failed it names
`nova-swarm triage`; if the pool drained it says so; if a worker was killed at
its deadline twice it names `requeue` with a smaller file budget, which is the
remedy that worked in batch 3. **A stopped pool is its own case (issue #180):**
the remedy names the stop and the act that lifts it — `rm <pool>/stop` — never a
second run with more hours, because a pool a person asked to hold still is not an
out-of-hours pool, and a recovering dispatcher must not override the stop
decision.

**`TRIAGE BATCH` is one line and it is the batch (rule 8).** It is the line
this spec's own numbers table was assembled from by hand, printed by the tool
instead. `findings` is every finding line across the batch's reports; `new` is
findings not marked `dup:`; `dup` is findings marked `dup:` plus findings that
match an owed item and were not marked; `unquoted` is findings with no verbatim
rule beside them; `clean`, `plan_only` and `no_result` are reports (rule 8:
`clean` is a head saying `findings: 0`, `plan_only` is no head, `no_result` is
no file, `malformed` is a report quarantined by rule 15 and counted in no
other column, `budget` is jobs ended by rule 13). `accurate` and `wrong` sum
the recorded verdicts, and print a dash when no verdict exists for any report
in the batch. Under `--batch <id>` the reports are those jobs and only those;
after the counts come at most `--max` finding lines, each with its evidence
path, ranked by the template's own order, then `TRIAGE MORE kind=finding
at=<path>` naming the page for the rest. The line never grows with the
batch.

## The key, read as data

The key is never in this tool, in an argument, in a log, in a prompt, in an
error message, or in any file a line at the table reads. It lives in one file
the person writes — one line, either the bare key or `NAME=<key>` — mode `0600`,
named by the worker description.

```
read the first line of the key file
strip a leading "export "
strip a leading "<VAR>=" for this worker's own variable name
strip every whitespace character
empty -> exit 2 with the command that writes the file
otherwise -> set it in the CHILD's environment only, and never log it
```

**It is read as data and never sourced.** Sourcing a key file executes it, and a
file that is one line today is a file somebody appends to tomorrow. The leak
that taught this rule was a key reaching a place a key must never be; the rule it
produced is in Rowan's memory as **the key file read as data, never sourced**,
and it is the one rule in this spec that is enforced by there being no code path
that could do otherwise: this binary has no shell.

**The harness config this tool writes carries the variable's NAME, never its
value.** The prototype writes an `opencode.json` declaring the provider with
`"apiKey": "{env:DEEPSEEK_API_KEY}"` — a reference the harness resolves from the
environment at run time. That file is on disk, in a working directory, readable;
a value written there would be a key at rest in a directory nobody treats as a
secret store. The config is rewritten at every start so a stale provider
declaration cannot outlive a worker description.

**Nothing in the key's lifetime reaches an event line.** The audit this
repository runs over every printed argument (`internal/oneline/audit`) covers it:
the key is not a printed value, and a new print of one is a test failure.

## Slots

A running worker holds a **slot**, `1..n`. A slot is:

- its **own working directory**, `<worker-dir>-<slot>`, refreshed from the home
  copy at every start, **one way** — nothing in a slot is ever written back;
- its **own data home**, `<slot-dir>/jobs/<label>/data`, exported per job as
  `XDG_DATA_HOME` so the harness keeps its own database there, one per job
  (rule 12);
- its **own job directory**, `<slot-dir>/jobs/<label>`.

**The data home is the whole reason slots exist.** The harness keeps one SQLite
database per data home, and concurrent runs against one database lock each other
out: measured **2026-09-10** with six workers, `database is locked`, five of the
six doing nothing while the pool reported six running. A pool that reports six
running and does one task's work is worse than a pool of one, because the number
is believed.

**A slot is held by exactly one worker and released only when that worker's
process is reaped.** The slot → task and task → pid maps are files,
`<pool>/slots/<n>.json` and `<job>/pid`, each holding `{job, state, pid, pgid,
pid_started, runner_pid}` (rule 17); the slot file is **reserved** by the
runner before the fork and **identified** by the child itself before it does
anything else, with the runner waiting on that handshake (rule 18), and it is
removed in the same step that finalizes the job, never on a timer and never by
a scan of directories. Every transition of a slot file is a rename made under
`<pool>/slots.lock`, a lock held for that one transition and released before
any wait; `<pool>/run.lock` is a different lock and says only that one
dispatcher runs (rule 17). The files
are the ownership, not a cache of it: a dispatcher that starts over a pool with
slot files present adopts, reclaims or quarantines each one before it claims a
task, and a quarantined slot stays out of the map for the whole run. See **the
races**.

**The refresh is one way, and it is a copy rather than a mount or a link.** A
worker that could write back into the home copy could change the next worker's
self, and the next worker would load it without anybody reading the change.

## Bench slot leases

(Glenn and Stella, 2026-09-17.) A bench is bigger than its owners: one bench,
many owners, and the slots on it are one shared pool, not one pool per owner.
A bench carries ONE slot store shared by every owner, at <bench store>/slots,
and every launcher — `nova-swarm run`, `nova-pulse launch`, a hand launch —
takes a lease per card before it runs and releases it after. The seven rules:

1. A bench carries ONE slot store shared by every owner, at <bench store>/slots,
   a directory of atomic mkdir leases each holding owner, pid, card label, until=.
2. Every launcher (`nova-swarm run`, `nova-pulse launch`, a hand launch)
   takes a lease per card before it runs and releases it after; a launch
   without a lease is refused by the launcher.
3. The broker verbs are the only way to hold a slot:

   ```
   nova-swarm slots take --store <dir> --owner <o> --n <k> --for <duration>
   nova-swarm slots list --store <dir>
   ```

   `take` grants by the owner's share from the registry file `<store>/shares.tsv`
   (columns bench, owner, share). It refuses with the holder list when the share is spent,
   and never grants past capacity minus reserve (rows `capacity` and `reserve` in shares.tsv).
4. `nova-swarm slots list --store <dir>` prints who holds what, one line per lease.
5. Reaping: a lease past until= whose pid is gone is reaped by the next take;
   drift: a pid alive past until= is DRIFT, printed by name, never reaped and never regranted.
6. In the survey, a card found running under a root with no matching lease is DRIFT in the survey.
7. Registry: shares change only by a PR to the registry, never by a note.

**Red tests** (each seen red before it is trusted):

- two owners at their shares cannot exceed capacity;
- an expired lease with a dead pid frees its slot;
- an expired lease with a live pid is DRIFT and stays;
- a launch without a lease is refused by the launcher.

A bench holds **slot leases**: the store is `<store>/slots` with one directory per lease made by `os.Mkdir` (atomic), each holding a file `lease` with lines `owner=`, `pid=`, `label=`, `until=<RFC3339>`, beside `<store>/shares.tsv` rows `capacity\t<n>`, `reserve\t<n>`, `<owner>\t<n>`. `slots take --store <dir> --owner <o> --n <k> --for <duration> [--label <text>]` first reaps every lease whose `until=` is past AND whose pid is not alive (`Alive`, signal 0) — a lease past `until=` with a live pid is `DRIFT`, stays, and counts as held — then grants `k` leases iff the owner's held+`k` stays within its share and the total held+`k` stays within `capacity` minus `reserve`, printing `SLOTS OK owner=<o> granted=<k> held=<h> share=<s> free=<f>` (exit 0) or `SLOTS REFUSED owner=<o> want=<k> held=<h> share=<s> free=<f> holders=<owner:count,...>` (exit 2); `slots release --store <dir> --owner <o> [--label <text>|--all]` frees them, and `slots list --store <dir>` prints one `SLOT <id> owner=<o> pid=<p> label=<l> until=<t> state=live|expired|DRIFT` line per lease.

**The store is the authority.** A launcher reads it before it runs and releases
its lease after; the broker is the only writer. No owner keeps a private count
that could drift from the leases on disk, and a card found running with no lease
is drift whether or not an owner believes it holds one.

## The deadline, held by the machinery

Every task carries a deadline. The default is the worker description's, and
`add --deadline` overrides it per task.

**The machinery holds it, not the worker.** The dispatcher starts the
supervisor as a child (rule 18), the supervisor starts the harness and holds
the deadline beside it, an adopting dispatcher holds it from the recorded
start, and at the deadline the holder sends a terminate to the group, waits,
then a kill.
`RUN KILLED` says so, with the count of finding lines on disk. A worker asked
to enforce its own deadline is a worker whose deadline depends on the thing
that has stopped responding.

**A reaped job runs once more, and only once by default (rule 7).** The first
reap keeps the partial `RESULT.md`, moves the task's files to `failed/`, and
queues the same task text again with `requeued=1` and `from=<old-id>` in the
new sidecar. `RUN KILLED … requeued=true reaped=1` says so. With
`run --no-auto-retry`, that same line says `requeued=false` and no automatic
descendant is queued. A second reap of the re-queued task is `reaped=2`,
`requeued=false`, and the job stays in `failed/`; the remedy line names
`requeue` with a smaller file budget, which is a person's act. One automatic
retry closes the case where a worker was silent because the provider was, and
never the case where the task was too big, which a second identical run would
only prove twice.

**The worker is told its deadline in its own prompt**, in seconds, with the
sentence that it will be killed by machinery — because a worker that knows it
has twenty minutes writes findings as it goes, and a worker that does not writes
them at the end it never reaches.

**The dispatcher's own deadline is `--hours`**, and it is required. At it, the
dispatcher starts nothing new and exits when the running workers finish or are
killed. Glenn, 2026-09-09: **every ask, child or read has a written deadline and
a default action; never wait forever.** A `stop` file in the pool stops new
admissions on demand, but does not kill or cancel workers already running,
including a retry that has already started. A run that ends over a stopped pool
names the stop in its remedy line — the stop is the concrete unavailable
function, preserved across a dispatcher's death and the next run's recovery
(issue #180): `the pool is stopped; <n> task(s) stay pending until the stop file
is removed` when work remains, or `the pool is stopped and drained; remove the
stop file to resume` when it does not.

**A wait loop never ends by scanning for its own name.** The dispatcher waits on
pids it started, and it never matches a process by its command line: that is how
19 orphaned shells happened (2026-09-10), a loop having found itself.

## The job directory and the sandbox rule

**The job directory is the only place a worker writes.** It is created before
the worker starts, it is named in the prompt, and everything the worker clones,
scratches or reports goes under it.

**And the operating system now holds that sentence, not only the prompt.** Every
job runs inside `nova-sandbox` (docs/SPEC-SANDBOX.md, "the two callers": this
tool is its dispatcher caller). The seam is the launch transaction of rule 18:
the supervisor wraps the harness it spawns, and the argv is built by the
dispatcher from the job it created and **never from the task text** — a task file
that names a directory buys nothing.

```
nova-sandbox --read <slot dir> [--read <read_roots entry>...]
             --write <job dir> --write <job dir>/data --cwd <job dir>
             --name <pool> -- <harness> <harness_args...>
```

| the list | what is in it, and why |
|---|---|
| write | the **job directory first** — the first `--write` is what the cwd and the temp directory default to — and the job's own data home beside it. Nothing else. |
| read | the **slot directory**, which is the worker home for this job: it holds the `opencode.json` this tool writes and the worker's own `AGENTS.md`, and without it the harness cannot read its own config. **The jobs of this slot live under it** (`<slot dir>/jobs/<id>`), so a job may also read the EARLIER JOBS OF ITS OWN SLOT — their `PROMPT.md`, `harness.log`, `RESULT.md` and data home. That is one worker reading its own past work under one key, and it is what naming the slot directory buys; a job of ANOTHER slot, another worker or another pool is in neither list. Then every `read_roots` entry of the worker description: a toolchain under a user directory is under no system root. |
| neither | the key file, `~/.ssh`, the `gh` configuration, the keychain, the shell history, every other line's home, every OTHER slot's directory, and this tool's own pool outside the job. |

The worker's **cwd is the job directory** (SPEC-SANDBOX rule 13), and the slot
directory above it is readable from there. It was the slot directory until the
wall landed, and it moved for two measured reasons: a cwd outside every named
path denies `getcwd(3)` and every `git` command dies with `shell-init: error
retrieving current directory` before it reads anything, and OpenCode's
`external_directory` permission is evaluated **relative to the harness's cwd**,
so a job directory that is not the cwd is "external" to the harness that is
supposed to be working in it.

**`HOME` is the job's own data home** (SPEC-SANDBOX rule 9), which is inside the
write set: the caller sets it, and a run whose `HOME` is outside every `--write`
is `SANDBOX REFUSED reason=home_outside` before the job starts. With the bench's
`HOME` inherited, the wall denies `~/.gitconfig` and the harness dies on its
first git command — a wall that lets the job start and kills its first command is
the silent sandbox SPEC-SANDBOX rule 1 exists to prevent. `XDG_DATA_HOME` stays
beside it, because the usage source reads the harness's database from exactly
that directory (rule 12).

**`--net-deny` is never passed:** the provider's API is the work, and the line
says `net=nopromise`. The key reaches the child the way it always has — read as
data before the wrap, passed by environment, the file itself in neither list
(rule 6 here and rule 6 there are the same rule seen from two sides).

**And "inside" is a question for the filesystem, not for two strings.** A
`key_file` whose placement would put it inside `worker_dir` or inside a
`read_roots` entry is refused **at load**, where a person can still move the
file, and the comparison that decides **those two** is `os.SameFile` over the
existing resolved ancestors of the key path, with a string prefix kept only as
the cheap first answer and as the only answer for a path that does not exist
yet. A prefix alone is case-sensitive and a case-insensitive filesystem — APFS
by default — folds a `key_file` typed `<dir>/Worker/.key` and a `worker_dir` of
`<dir>/worker` into one file: the spelling said the key was outside while the
slot copy put it inside the wall (#100).

**And the slot directory asks it too.** A `key_file` inside a slot directory
`<worker-dir>-<n>` is refused at load as well — that rule stands unchanged — and
because such a directory **need not exist at load** (slots are created at run, so
there may be no inode to compare) the candidate is derived from the key's own
ancestors and judged against the slot spelling `SlotDir` would build, under the
filesystem's own equality: the two directories themselves where both are there,
and otherwise the case behaviour of the directory holding them, **measured at load
by writing and removing one probe file inside that directory** — the parent of
`worker_dir`, where this tool creates slot directories anyway — rather than read
off `runtime.GOOS` (#145). These two paragraphs name mechanisms; they add no
rule.

**The probe runs once, before the first worker** (SPEC-SANDBOX rule 10): `run`
asks the machine what it can enforce and then proves the wall with the real
policy for this platform. A machine with no backend is `RUN REFUSED
reason=no_sandbox` and a wall that failed a check is `RUN REFUSED
reason=sandbox_probe`; both start **no worker at all**, because a batch that runs
unwalled under a green `RUN OK` is the failure the wall exists to close. The
probe costs one process per pass and answers a question about the machine, never
about a job.

**`--no-sandbox` is the one loud workaround, and it is THIS tool's** —
`nova-sandbox` has no such flag (SPEC-SANDBOX rule 11), so the line below is the
one place in this repository an unsandboxed run is announced. It runs
every job with no OS containment and prints one line per job, on stderr, before
the job starts:

```
RUN UNSANDBOXED id=<id> slot=<n>: no OS containment; every read and write this job makes is yours
```

It is never a default, never implied by a missing backend, and no environment
variable or file produces it: it is argv, where `ps` shows it. A platform whose
sandbox body is not built (linux and windows today) refuses every run that does
not name it.

**One thing the wrap changed below the seam, and it is named here because it is
rule 11's own machinery:** the wrapped tree stays in the **job's process group**.
`nova-sandbox`'s darwin body no longer puts its child in a group of its own,
because a group whose id no caller can learn hides the job's own tree from the
supervisor that must reap it — measured 2026-09-12: a wrapped harness that forked
a background child left it alive after the run, and the survivor count was 0
under a job recorded `done`.

**The sandbox rule, stated in the prompt:**

> A read or a write outside the job directory may be refused by the tool. **A
> refused read or write is not an error and does not end this run.** Note it,
> read or write something inside the job directory instead, and continue.

This sentence is in the prompt because **today two runs ended on a refused
read**. The worker asked for a path outside its sandbox, the harness refused, and
the worker treated the refusal as a fatal condition and stopped with nothing
written. The refusal was correct. What was missing was the worker knowing what a
refusal means — and that is a property of the prompt, which is this tool's
output, which makes it this tool's bug (Glenn, 2026-09-09: **a friend's first-run
stumble means fix the tool and the docs, not only answer them**).

**The same sentence covers a write (rule 5).** Later the same day the cause of
batch 2's three silent runs was found: each had tried to write a scratch file
outside the job directory, the harness refused, and the run ended with a plan
at the top of `RESULT.md` and no findings under it. So the sentence in the
prompt says *read or write*, and the tool does its half: after every run it
reads the harness log for the harness's own refusal lines and prints
`refusals=<n>` on `RUN DONE`. A `plan-only` beside `refusals=1` is a diagnosis
a coordinator can act on; a `plan-only` alone is the silence this spec's first
draft could not explain.

The prompt also says, in one line each: **there is no bus** — do not try to send
anything to anybody; **do not loop, poll or wait for replies**; write what you
are about to do at the top of `RESULT.md` **before** doing it, append as you go,
and stop; **every write of `RESULT.md` is whole: write it to `RESULT.md.tmp`
and rename it over `RESULT.md`** (rule 16), with the two commands spelled out;
and **when the work is done, write the `## Head` with `findings: <n>` — `0` is a
complete answer** (rule 8).

## The task templates

A task is a text, and a bare text produces a bare answer. A **template** is the
learned conditions baked in, so the conditions are not re-typed and not
forgotten. `nova-swarm template --name <name>` prints one; `add --template
<name>` wraps a task in one.

The numbers that produced these conditions are in **the numbers from today**
below. Each condition names the failure it closes.

### `read-pr` — read one pull request against the rules

```
1. READ THE PR BODY'S OWED LIST FIRST, before reading any code, and for every
   finding you report, say whether it is already on that list. A finding that
   is already owed is marked `dup:` and is not a new finding.
   [batch 1: 25 of 67 findings were duplicates of the owed list]
2. QUOTE EVERY RULE VERBATIM, with `file:line`. Never paraphrase a rule from
   memory, and never assert a rule you did not open.
   [batch 1: 5 of 67 findings were wrong, each a paraphrase]
3. APPEND EACH FINDING TO RESULT.md THE MOMENT IT EXISTS. Not at the end.
   You may be killed at your deadline; what is on disk is what you found.
4. A FILE BUDGET: read at most <n> files (the task's --files). When the budget
   is spent, write what you have and stop. Say in RESULT.md which files you
   did not open.
   [batch 3: with a budget, 2 of 3 tasks complete; without, 0 of 3]
5. A RESULT.md CONTAINING ONLY A PLAN IS A FAILED TASK. The plan belongs at
   the top, before the work; the findings are the work. A finished read that
   found nothing is NOT a failed task: write the `## Head` with `findings: 0`.
   Never report a finding to have something to report.
6. Check the board before reporting: a card that already names this is a `dup:`.
7. A SEVERITY FLOOR: emit only findings at or above `HIGH`. A finding below the
   floor is not emitted at all. State the floor in RESULT.md's `## Head`
   paragraph as `floor: HIGH`, and mark each emitted finding with its severity.
   The floor decides which findings are emitted, not how they are written:
   every emitted finding still quotes its rule verbatim with `file:line`.

Keep RESULT.md concise: omit progress narration, praise, repeated task text, and a
separate summary. Each finding keeps its proof in compact form: severity, `file:line`,
the exact quoted rule, the fix, and `dup:` status when applicable. Retain every valid
finding, its context and evidence, and any coverage limitation; do not drop context or
evidence by default. Brevity is a soft target: never hard-truncate findings or proof; if
the report overflows, preserve the proof and say so. Preserve the complete RESULT.md
shape and its mandatory `## Head`, `## Findings`, `## Per item`, `## Gates`,
`## Left owed`, and `## One line` sections.

BOUND THE REPORT (issue #74): findings only. No narration of the clone, no
restated task, no praise, no summary. One line per finding: `file:line`, the
rule in twelve words, the severity, and the fix in one clause. Keep RESULT.md
under 40 lines and every line under 300 characters, and no pipe inside backticks:
a `|` in a quote broke the table grammar twice (D12), so quote the rule without
it. Put the verdict line last. When there is nothing to report, write
`findings: 0`.
```

The seventh condition is the severity floor (#65). The noise that made AI code
review a complaint is measured, not argued: the reader emits only findings at or
above `HIGH`, writes `floor: HIGH` in the head, and a below-floor finding is not
written at all. It is the bounded-output rule applied to the reader, so a read's
verdict is easier to act on because everything that survives the floor is worth a
round. The floor decides which findings are emitted, not how they are written, so
the verbatim-quote condition still holds for every finding that survives it.

### `probe-row` — make one claim true or false

```
1. Name the claim in one sentence at the top of RESULT.md before probing it.
2. The probe is a command, a file:line, or a measurement — never an opinion.
   Paste the command and its tail into RESULT.md.
3. Append the result the moment you have it.
4. A file budget, as above.
5. A probe that could not be run is a RESULT with `not done` and the reason.
   That is a complete task; a guess is not.
```

### `fix-card` — take one card and land the fix

```
1. Read the card, and the board, before touching anything: a card already taken
   is a `dup:` and you stop.
2. Work only inside the job directory. The clone is yours; nothing outside it
   is yours.
3. Quote the rule the fix serves, verbatim, with file:line.
4. Write the gate you ran and its result into RESULT.md's Gates table. A fix
   with no gate is `not done`.
5. A file budget, as above.
6. Leave what you did not do under `Left owed`, named so the next worker can
   pick it up with no other context.
```

**A template is text and nothing else.** It is not code, it does not execute,
and `nova-swarm` does not read a worker's `RESULT.md` and act on it. The
templates are shipped in the binary, printable, and a caller may write their own
file instead — the tool has no list of blessed task shapes.

## The `RESULT.md` template

One shape, printed by `nova-swarm template --name result`, and the shape
`triage` parses:

```markdown
# <task>

## Head
findings: <n>
notes read: <n>
repo: <owner>/<name>
rev: <sha>
<one paragraph: what was asked, what the state is now, and the single most
important fact.>

## Per item
| item | state | evidence |
| --- | --- | --- |
| <the item as it was handed to me> | red / green / not done | <file:line, gate name, PR #, or the command and its tail> |

## Gates
| name | result | seconds |
| --- | --- | --- |
| <gate or command> | pass / fail / not run | <n> |

## Left owed
- <the item nobody did, named so the next worker can pick it up with no other
  context.>

## One line
<one sentence a coordinator can paste into the board.>
```

**The head's first line is `findings: <n>`, and it is the completion
evidence (rule 8).** It is written when the work is done; a report without it is
`plan-only`, a report with `findings: 0` is `clean`. The number is the worker's
own count and `triage` prints its own beside it; they may disagree, and the
worker's decides the class while the tool's decides `findings=` on the batch
line.

**`state` is one of exactly three words**: `red`, `green`, `not done`. A
fourth word is a report a coordinator has to interpret, and a coordinator reading
forty reports interprets nothing.

**A `|` inside a backtick span is text, not a cell boundary.** Rule 2 asks
every claim to quote its rule **verbatim**, and the rules of these tools are
grammar lines full of `|` — a reader obeying rule 2 has to be able to write
`` `<utc|zone>` `` in an item or a gate's command. The cells of a row are the
pipes outside its quotes; an unterminated backtick quotes to the end of the
line, which leaves too few cells and is malformed like any other row that does
not parse.

**`repo:` and `rev:` name what was read**, the repository and the revision the
worker had open; they are the first two parts of the de-duplication key (rule
15). A head without them is still a head — the report is `ok` or `clean` by
its `findings:` line — but its findings are never merged with another report's.

**A report that does not follow the template is quarantined, not degraded.** A
head without `findings: <n>`, a fourth state word, a table that does not
parse: `RUN MALFORMED id=<id> line=<n>`, the job in `failed/`, class
`malformed`, no line of it in any page and no finding from it counted (rule
15). The one finding the worker got out before it died is in the file, and the
file is one command away — `result --id <job>` — for a person; it is not the
tool's to salvage, because a tool that quoted half a malformed report into a
page would be choosing which half, and a report with no head at all is
`plan-only` (rule 8), never `malformed`.

## The `setup` agreement form (issue #184)

Printed by `nova-swarm template --name setup` and refused by `add --template`,
as `result` is: it is a form a person and a friend fill together, not a task's
conditions. It is the near-term endpoint of the per-friend safety-setup
coordination issue #184 asks for — review and agreement only, with
implementation, credential migration and deployment as separate staged work —
and it publishes the generic configuration examples and the agreement/evidence
template rather than any private security detail: every value in it is a
placeholder, no secret, key, token or private path is ever printed, and an
agreed form supplies no account access. A friend may agree, propose an
alternative, decline, or stay silent, and missing feedback is pending, never
assent. The guarantee table is the point of the form: each row says **who
enforces it** — the OS wall, a cooperating harness, or the launcher outside
the wall — so a guarantee the kernel keeps is never mistaken for one that
depends on a harness honouring its fence, and a harness row stays `unproven`
until the friend's build is shown to honour it.

```
setup — one friend's safety setup, proposed, reviewed, agreed (#184)

One form per friend, agreed before it is enforced, because a blanket restrictive
setup prevents useful work and ignores each friend's chosen harness, while a
blanket permissive one hands every friend every other friend's secrets. Print
it, fill it with the friend who would run under it, and paste the FILLED form
where the review happened; the private configuration it describes is never
pasted anywhere. Names, models, harnesses and bench layouts are not constants
of this form: every value below is a placeholder, and a friend's own choices
fill their own copy. A friend may propose an alternative, decline, or stay
silent, and missing feedback is pending, never assent. An agreed form is an
agreement and nothing more: it supplies no account access, and implementation,
credential migration and deployment are separate staged work with their own
authorization.

## The proposal (written with the friend)

friend: <name>
bench: <the machine or hosted runner this friend works on>
harness: <the harness this friend chose, and its version>
model: <the model this friend chose; never this form's business>
proposal by: <who wrote this form, and where the review is recorded>
reviewed with: <the friend's own read of this form, or pending>

read scope: <the shared inputs this friend needs to read, named once>
write scope: <this friend's own directories, and nothing above them>
execution boundaries: <one task, one process tree, one deadline, or the
  friend's own boundary and who holds it>
secret use: <the ONE seat file that holds this friend's keys, and the ONE
  variable name the harness reads; a value is never written here>
destructive controls: <what a delete, a force-push or a repository
  destruction must be unable to reach, and which ruleset forbids it>
recoverability: <what is pushed where on every exit, so a delete is a
  re-clone>
unresolved concerns: <what this friend has not agreed to, in their own words>

## The agreement (the friend's own half)

status: agree | alternative | decline | pending
alternative proposed: <the friend's own setup, in their own words, or ->
declined because: <the reason, kept honestly, or ->
pending since: <the date feedback was asked for>

## The guarantee table (filled together, one row per guarantee)

| guarantee | who enforces it | supported here | evidence |
| --- | --- | --- | --- |
| a read outside the named lists is denied | the OS wall | yes / no | nova-sandbox probe |
| a write outside the write set is denied | the OS wall | yes / no | probe step write_outside |
| no credential file is readable inside the wall | the OS wall and the caller's placement | yes / no | probe --secret <path> |
| no agent socket or agent address reaches the child | the OS wall and the environment scrub | yes / no | SPEC-SANDBOX rules 7 and 9 |
| a push from inside the job fails | the OS wall | yes / no | the four mechanisms of SPEC-SANDBOX test 27 |
| the harness asks before an outside path | a cooperating harness | yes / no / unproven | the fence example below |
| the friend's work survives a delete | the launcher, outside the wall | yes / no | the push on exit, a re-clone recovers |
| the friend's secrets stay the friend's | the seat file's own recipients | yes / no | nova-secrets check |

A row the OS wall enforces is a fact the kernel keeps on the machine this form
names. A row a cooperating harness enforces is a row the wall must not be
asked to prove: mark it unproven until the friend's harness build is shown to
honour it, and call a row supported only on the machine and the build this
form names.

## The generic examples (placeholder values only, never a private one)

the wall, one job, its lists written down in one place and never guessed:
  HOME=<data home> nova-sandbox --read <the shared reference checkout>
    --read <the worker home> --write <the job directory>
    --write <the data home> -- <the friend's harness> <args...>

the fence, the harness's own permission block, allow or deny, never ask:
  {"permission": {"external_directory": "deny", "webfetch": "<the friend's choice>"}}

the seat, one file per friend, sealed to that friend's bench key alone:
  nova-secrets exec --store <the store's working copy> --as <this friend>
    --key <the key path> --sops <the sops binary>
    --only <ONE variable name> --require <ONE variable name> -- <launcher>

the launcher, outside the wall, the friend's own lists:
  sets HOME inside a --write, passes the credential by environment read as
  data before the wrap, and pushes the friend's directories to their remote
  on every exit, clean or not.

## What is never in this form

No secret, no key, no token and no private path is ever written into this
form, a task card, a bus note, an issue or a token ledger: a name or a path
is not a secret, but a value is, and this form carries values for nobody.
Before any staged implementation is built on an agreed form, validate it on
synthetic secrets and disposable repositories and record both runs:
a denied destructive operation and successful permitted work.
```

## The `capacity` offer-and-routing form (issue #176)

Printed by `nova-swarm template --name capacity` and refused by `add --template`,
as `result` and `setup` are: it is a form a friend and a coordinator fill
together, not a task's conditions. It is the near-term endpoint of the
discover-offered-capacity-and-match-ready-work coordination issue #176 asks
for — manual census and routing log, with the four rows acceptance evidence
demands before any automatic scheduler is built, and the five capacity kinds
(coordinator, direct worker, one-shot, swarm and local) kept apart because
model slots are not interchangeable throughput units. It publishes the offer
half and the routing-log half with placeholder values only; every named
field is a thing only the friend or the coordinator knows, and a friend's
own choices fill their own copy. The offer has an expiry; past the expiry
it is excluded, not favoured and not penalised. missing contact is unknown;
stale capacity is not proof of failure and not proof of consent; an offer
nobody answered since the silent-ping window is reported as `reason=unconfirmed`,
and an expired offer is reported as `reason=expired`. Two offers sharing a
named shared-limit pool are counted once per pool in the routing log, and
utilisation is reported only against the explicit pool-specific denominator
the routing log names — coordinator, direct worker, one-shot, swarm and local
each carry their own, never summed. no key, no token, and no private host
detail is ever published, names/models/harnesses/benches stay placeholders,
and an agreed offer supplies no account access. A friend may propose an
alternative, decline, or stay silent, and missing feedback is pending, never
consent; an automatic scheduler is separate staged work with its own
authorization.

```
capacity — one friend's offered capacity and the manual routing log (#176)

One offer per friend, bounded and expiring, published before any automatic
scheduler is built, because an idle pool receiving ready work, an incompatible
offer being skipped, shared capacity counted once, and a stale offer excluded
are four separate things a coordinator has to do by hand first, in a form a
friend fills and a coordinator reads. Print the offer half, fill it with the
friend whose capacity is being advertised, and paste the FILLED offer where the
review happened; the private configuration it stands for stays on the friend's
own bench. Print the routing log half, fill it with the ready work and the
offers considered, and record the matching decision in writing so the next
review can compare the count against the offers' own quotas. Names, instances,
benches, models, harnesses and pool layouts are not constants of this form:
every value below is a placeholder, and a friend's own choices fill their own
copy. A friend may propose an alternative, decline, or stay silent, and missing
feedback is pending, never consent. A filled form supplies no account access,
and an automatic scheduler is separate staged work with its own authorization.
A form carries one capacity kind at a time from the five the SPEC-WORK friend
section distinguishes — coordinator, direct worker, one-shot, swarm and local —
because model slots are not interchangeable throughput units and a swarm
worker, a one-shot, and a local model run on different evidence and different
shared-limit pools.

## The offer (the friend's own half)

offered by: <who wrote this offer, and where the review is recorded>
reviewed with: <the friend and a coordinator, or pending>
expires: <the stamp this offer stops being an offer, never blank>

friend: <name>
instance: <the worker home or container this friend runs under>
bench: <the machine or hosted runner this friend works on>
model identity: <the provider's id and the resolved model id>
basis: <the per-token cost class — zero|flat|metered — and its pricing reference, or local>
harness: <the harness this friend chose, and its version>
supported task types: <read, text, code, replay; one or more, a comma list>
demonstrated strengths: <what the friend has been shown to do well, never an inferred claim>
demonstrated limits: <what the friend has been shown unable to do, never an inferred claim>
permitted scope: <the repositories and paths this offer may read and write>
current availability: <awake | resting | credit-limited | rate-limited | unknown — never idle because a recent message did not arrive>
concurrency: <the maximum parallel slots this offer reserves>
expected queue/latency: <the queue depth and the latency a scheduler can expect, bounded>
shared-limit pools: <the named pools whose quota this offer shares, or `[]` for none>

## The routing log (the coordinator's half)

ready work: <the dependency-ready task list being matched this cycle>
compatible offers: <the offers whose supported task types and permitted scope admit the ready work>
incompatible offers: <the offers skipped this cycle, with one reason each — wrong task type, scope mismatch, basis mismatch, capacity kind, anything but a name>
shared pool share: <the share of the named shared-limit pools, counted ONCE per pool across all offers naming it>
stale offers excluded: <the offers whose expires stamp has passed or whose contact stamp is past the silent-ping window, named and never counted>
utilization denominator: <the explicit pool-specific denominator this cycle's utilization would be reported against — a coordinator, direct worker, one-shot, swarm and local each have their own>

## The four rows acceptance evidence demands (one row each, when they occurred this cycle)

| observed | row to write |
| --- | --- |
| an idle compatible pool receiving ready work | offer=<name> task=<id> routed=true admit-gate=<gates that passed> |
| an incompatible offer being skipped | offer=<name> task=<id> reason=<what rules it out> |
| shared capacity counted once | pooled-as=<pool> reservations=<n> offers-with-that-pool=<n> shared-share=<n> |
| a stale offer excluded | offer=<name> expires=<stamp> contact=<stamp or NONE> reason=<expired or unconfirmed> |

missing contact is unknown; **stale capacity is not proof of failure and not proof of consent**, so an offer nobody answered since the silent-ping
window is excluded, not favoured and not penalised, and reported as
`reason=unconfirmed` alongside any expired offer reported as
`reason=expired`. Each capacity kind from the SPEC-WORK friend section
gets its own row when the offer names it — **coordinator capacity** is its
own row, **direct worker capacity** is its own row, **one-shot capacity** is
its own row, **swarm capacity** is its own row, and **local capacity** is its
own row — because model slots are not interchangeable throughput units, and
sharing a quota across those kinds is the double-count the form exists to
prevent.

## What is never in this form

no key, no token, and no private host detail is ever written here, a task
card, a bus note, an issue or a token ledger: a name or a path is not a
secret, but a value is, and this form carries values for nobody. A shared
account limit is named by its pool, never by the credential that holds it.
An offered capacity is not a purchase, a permission, or a promise to run;
it is the standing under which a coordinator may propose ready work, and a
friend chooses offers, reserves, and rest, not a scheduler that maximises
occupation beyond that offer.
```

## `triage` — one page

`triage` walks the job directories, takes every `RESULT.md` **whose revision
hash is not the one recorded for that job**, prints one line each, and writes
**one page**:

```
<pool>/reports/<UTC>.md
```

The page carries, per report: its job id, its revision hash, its heading, its
path, its **Per item** table and its **Left owed** list, in job-id order
(the id begins with the job's UTC stamp), oldest first. The terminal gets one
line per report and then the counts — `reports`, `template`, `malformed`,
`skipped`, `items`, `red`, `green`, `notdone` — and the page's path. That is
the whole of the terminal output, capped at `--max`, because the page is the
artifact and the terminal is an index to it.

**The consumed set is state**, `<pool>/triage.json`, holding `version`, `runs`,
`last_iso`, `last_count` and `consumed`, a map of job id to the SHA-256 of the
revision last folded into a page, written atomically through a temporary file
and a rename. `--all` ignores it, `--since <stamp>` restricts the walk to jobs
whose id stamp is at or after `<stamp>`, `--no-state` does not advance it. A
triage with no state reprints the night. There is no mtime in this tool: two
revisions with one mtime are two hashes, and an mtime a filesystem rounds is
not an identity (rule 16).

**A report is read as one revision, and a revision that moved is not
consumed.** The tool reads `RESULT.md` into memory, hashes the bytes, parses
that buffer, and hashes the file again before recording anything; a second
hash that differs from the first is `TRIAGE SKIPPED id=<id>: changed while
read`, the report is not in the page, `consumed` is not advanced for it, and
the next run takes it whole. The worker's own protocol (rule 16: write
`RESULT.md.tmp`, rename) makes a torn read impossible; the double hash makes a
worker that ignored the protocol visible rather than half-quoted.
`RESULT.md.tmp` is never opened by `triage`.

## The board link

The owed items live on a board — a GitHub issue whose comments are cards
(`mas-bandwidth/schema#876` for the lane this prototype served). `nova-swarm`
does **not** write to the board. It does two things:

1. every task's prompt carries the board's locator and the sentence **check the
   board before filing: a card that already names this is a `dup:`**;
2. `triage`'s page ends with the `One line` of every report, which is what a
   coordinator pastes into a card.

**Filing and closing cards is a person's or a coordinator's act, through the
board tool, never a worker's.** A swarm that could file would file 67 cards, 25
of them duplicates, which is exactly what batch 1 produced when nothing checked
first. The separation is the same one `nova-merge` keeps from the bus: a tool
that also announced would be two tools in a bug report.

## Cost per task, and rate limits

`cost` reports, per task and per window: **the five token types and
dollars**, with the model, the attempt and the way the job ended named. The
numbers come from the harness's own accounting, recorded into the job's usage
file `<pool>/usage/<job>.tsv` by `finalize` when the job ends (rule 12); a
task whose harness reported nothing prints `in=- out=- usd=-` rather than a
zero, because a zero is a measurement and a dash is an absence. `COST OK`
carries token `dashes=` plus `known_usd=` and `usd_missing=`: a mixed USD
subtotal remains visible, while an all-unknown USD total is `usd=-` and is
never read as measured zero.
`cost` reads `<pool>/usage/` and nothing under `done/`, `failed/` or
`running/`, which is why it answers after `reclaim`.

**`--by model|day|repo` folds the same rows into one summed line per group**, printed
after the per-task lines and before `COST OK`, one
`COST BY <group>=<value> tasks=<n> in=<sum> out=<sum> cache_write=<sum> cache_read=<sum>
reasoning=<sum> usd=<sum or ->` per group, sorted by `cache_read` descending. A group
whose rows carry a `-` in `usd` prints `usd=-`: the summed dollar figure is an absence
when any row in it is an absence, and a partial subtotal is never read as the group's
price. `--summary-only` prints the `COST BY` lines and `COST OK` and no `COST TASK`
lines, which is the page a coordinator reads to answer *which model, which day, which
repo*. A `--by` value that is not `model`, `day` or `repo` is refused rather than read
as a fourth grouping.

This exists because **a fleet can burn a session**: one fleet ran about 9M
tokens, and three parallel workflows hit the limit in 20 minutes (Glenn,
2026-09-10). A pool whose cost is invisible is a pool that is discovered to be
expensive by being cut off.

**Rate limits are the dispatcher's business.** A provider's 429 is not a failed
task by default: the dispatcher holds the slot, waits the interval the provider
names (or its own `--backoff`, default 30 seconds, doubling to a cap of 5
minutes), and retries the **same** task once. With `run --no-auto-retry`, it
records and finalizes the true-429 attempt without waiting or launching a
descendant. A second 429 on the same task fails it with `rc=429` in its
sidecar, so `triage` can see that a batch's silence was a limit rather than a
set of bad tasks. Three of seven workers in batch 2 came back
with a plan and no findings and nothing in the pool said why; the cause turned
out to be a refusal, not a limit (rule 5), but a 429 is a second road to the
same silence and the sidecar closes both.

**A provider's input limit is its own failure class, `input-limit`, and it is never
retried.** A harness that dies because the request did not fit ends the job
`end=input-limit` on `exit.json`, in the sidecar, in the usage row's `end` column and on one
`RUN INPUT-LIMIT` line carrying the measured prompt size and the provider's own sentence, so
triage says *the task was too big for the model* without opening a log; the phrases that
name it are a table the worker description may add to (`input_limit_phrases`), because every
provider says it in its own words — and because OpenCode's own sentence, `Rate limit reached:
input token limit exceeded`, was read as a 429 and earned a second identical launch that
spent another 215 seconds proving the same two specs still did not fit (2026-09-12, two of
forty Freddy jobs). **A phrase counts only on the provider's own error line** — a line whose
own LABEL is an error mark, or the line directly under one, and never any line of the
transcript that merely holds the word: the mark begins a word, at most two tokens precede it
and at most one of those is a bare word, a list marker at the head of the line makes it prose
however the mark is placed, and a quote character before it means the line is quoting rather
than reporting. The table's sentences are printed in this repository's own
source, README and this spec, and a worker's RESULT.md quotes them in a bullet or a finding
row; a job so classed is never retried, so a false mark would take a real 429's retry away
(rule 5's own lesson: a diagnosis that fires on the word for the thing, wherever it appears,
is noise in the field a reader was told to trust). **A line this family wrote itself is never
a provider talking** — anything carrying the two-token event prefix, `RUN REFUSED …`,
`SANDBOX OK …`, this class's own `RUN INPUT-LIMIT …`, is skipped whole, because a job that
runs these tools puts their lines in its own harness log — **unless that prefix's second word
is itself a mark**, which no line of this grammar has (`OK`, `REFUSED`, `DONE`, `NOTE`, `FAIL`,
`STEP`, `ABORTED`) and a shouting proxy does (`HTTP ERROR: 400 …`, `API ERROR: 400 …`), which is
the same door seen from the other side; and `refused` and `aborted` are for the same reason not
marks at all, being this repository's words and no provider's. **That the grammar holds no
event line whose second word is a mark is asserted by a test over the sources**, not by a list
anybody keeps: a claim about every printed line is one only machinery may make. A phrase a description names is a sentence — twelve characters and a space or a
digit — refused when it is read, because a job classed this way is never retried. A task may also name `max_input <bytes>`, the ceiling on the prompt this
tool hands the harness, which `run` checks **before** the launch and refuses with the same
class and the measured size: it bounds what the dispatcher can measure, and the files the
worker then opens are still `--files`.

**A launch that dies in its first seconds on a provider 5xx is retried, and only
then filed `end=provider`.** Measured on 2026-09-17: nine of forty Muse requests died in
under two seconds with `Unexpected server error … ref=err_…`, each having taken a slot and
spent its harvest on a request the provider never began, and the pool read them as ordinary
failures. So a harness exit inside the **launch grace** — the worker description's
`launch_grace`, fifteen seconds by default — whose output tail names a provider server error
(`unexpected server error`, `internal server error`, or a `502`, `503` or `529` as a whole
word) is a **launch failure** and not a finished task. The dispatcher retries the same task,
after a jittered 5–20 s and then a jittered 30–60 s, and after the third fast failure it
files it with `end=provider` and the provider's `ref=` on its `RUN PROVIDER` line, in the
sidecar and in the usage row. The retry keeps the task: each attempt is `from=` the attempt
before, the usage rows carry `attempt=1`, `attempt=2`, `attempt=3`, and a field the provider
never reported stays a dash and never becomes a zero. A slow failure — one that takes longer
than the grace — is a real run that failed and is **not** retried, and `run --no-auto-retry`
files the first fast failure without launching a descendant. A native run applies the same
rule to its launch and appends one usage row per launch, so a retried card's `usage.tsv`
carries its attempts for the one job.

**The structured signal is a field, not a sentence (issue #163).** A harness adapter records
the provider's refusal as one line in the harness log —
`INPUT LIMIT class=<token|bytes|files> value=<n> limit=<n>` — and the supervisor, `finish` and
rule 17's recovery pass all read that field and name the class from it: no mark, bare-word
bound, list marker or event-prefix rule is asked to decide it. The prose heuristic above is
the fallback for a harness with no adapter, and the class is decided from the field before a
word of it is read.

## `requeue` — the same task, changed

```
nova-swarm requeue --pool <dir> --task <id> --task-file <file> [--label <text>]
```

`requeue` takes a finished task and queues a **new** task with **new text**,
recording `from=<old-id>` in the new task's sidecar. `--files` and `--tokens`
are required on it exactly as on `add`: the remedy that worked in batch 3 was
a smaller file budget, and a requeue that inherited the old budget would
repeat the failure it was typed to fix. The text must be supplied;
a requeue with the same text is a retry, and a retry of a task that failed for
what it said will fail the same way. The remedy that worked in batch 3 was a
**file budget added to the text**, not a second attempt at the same sentence.

The old task's files stay where they are. `requeue` deletes nothing; the one
verb that removes anything is `reclaim`, and it removes a job directory only
after the job's usage file and its report copy under `<pool>/reports/<job>/`
both exist (rule 12). Reports in `reports/` and usage
files in `usage/` are never deleted by any verb: a pool is a record.

## The numbers from today

Stated because they are the evidence for every condition in the templates, and
because a spec that asserted the conditions without them would be asking to be
believed.

| batch | conditions in the task | accurate | new | wrong | duplicate | plan-only |
|---|---|---|---|---|---|---|
| 1 | none | 62 of 67 | 37 | 5 | 25 | — |
| 2 | the owed list, verbatim quotes, append-as-you-go | 17 of 17 | 16 | 0 | 0 | 3 of 7 |
| 3 | the above **plus a file budget** | — | — | — | — | 2 of 3 complete |

What the table says, in one sentence each:

- **Batch 1** was accurate and mostly useless: 25 of 67 findings were already
  owed in the pull request body, and 5 were wrong, each one a rule paraphrased
  rather than quoted.
- **Batch 2**, with three conditions in the task, was 17 for 17 with nothing
  wrong and nothing duplicated — and **3 of 7 runs ended with a plan and no
  findings**. The morning's pool could not say why. The afternoon's reading of
  the harness logs could: each of the three had a scratch-file refusal outside
  the job directory, and the run ended on it. That is rule 5, and it is why
  `refusals=<n>` is on `RUN DONE`.
- **Batch 3** added a file budget and went from workers that read forty files and
  finished nothing to **2 of 3 complete**.

The conditions are worth more than the model. That is the finding. The table
is the last one a person assembles by hand: `TRIAGE BATCH` prints it (rule 8).

## The efficiency card (#87), cross-tool

The card is a measurement, taken on the bench on **2026-09-12**, of the same
work paid for once per tool. This section is the part of it that binds
`nova-swarm`; the other tools' halves live in their own normative specs, and
nothing here restates them. The card is cross-tool, so its two rules are
stated as contract, not as a bench recipe.

### The same clone, once per swarm job

Twenty-one jobs on the measured bench each carried their own `repo`, **21
jobs**, **307 MB** of one object graph, and about **3.6M cache-read tokens**
re-deriving a tree that is byte-identical for every job on the same head.
`bin/child-clone.sh:111` already answers it for the schema repo:
`--reference-if-able` off an on-disk checkout makes a large clone cheap, and
`--dissociate` copies the objects in. So the rule is **one reference checkout
per batch**, and every job's clone is built from it: a per-job clone under the
job directory (rule 7 is unchanged — the checkout is still the job's own)
passes `--reference` off the batch's reference checkout and then `--dissociate`, so the object graph
is read once and the per-job clone is small. The tool prints the clone line it
expects when it prepares a job; a card that clones without the reference is
paying the graph again.

### The prompt text is the tool's

Five shell scripts duplicated five of the seven tools on the measured bench,
and for `nova-swarm` the live text was `run-worker-v2.sh:120` — a shell
script's private variable, not a template. That is the inverse of this spec:
the prompts and their conditions are `internal/swarm/templates.go` in the
binary, printable, and versioned with the tool (the templates are the single
highest-value thing in this spec). So **the prompt text the workers run is the
tool's**: `Prompt` and `WrapTemplate` assemble it from the named template, the
shell scripts are prototypes that this spec deliberately does not transcribe
(see "What the prototype does that this spec forbids", item 10), and the
switch away from them is the two-step switch above. No tool's live state is a
shell script's private variable.

### Red tests

The card earns the same red-first bar as every rule here: seen red before it
is trusted.

- a per-job clone built with `--reference` and `--dissociate` shares the reference checkout's object graph and still has its own working tree;
- the worker prompt carries the named template's conditions from the tool, with no shell script in the path.

## The races, taken out

**Two workers on one slot.** The dispatcher's free-slot search walked its own
slot map; if a slot was freed by one code path and the map updated by another,
two workers got the same slot, the same data home and the same SQLite database —
which is the 2026-09-10 failure arriving by a second route. So: the slot map is
the **only** authority on what is free, a slot is reserved on disk before the
fork and identified by the child before it works (rule 18), and it is freed in
the **same step that finalizes the job**. Never a directory scan, never a lock
file in the slot, never a timer. Pinned by a test that starts `n` instant-exit
workers against `n-1` slots and asserts no slot is ever held twice.

**A worker still running after its deadline.** The dispatcher terminates, waits,
kills. A child that survives the kill (a process group that outlived its leader)
is reported as `RUN KILLED … survived=true` and its **slot is not reused for the
rest of the run**: a slot whose data home may still have a writer in it is not a
free slot. A pool that re-used it would reproduce the lock failure with a
corpse. The dispatcher's own exit does not wait forever on it — `--hours` is a
deadline for the dispatcher too, and a surviving child is named in `RUN OK` so a
person can end it.

**A result file rewritten mid-triage.** Closed by publication by rename (rule
16): the worker writes whole revisions and renames them into place, so a read
sees one revision entire; and by the double hash in **triage**: a file whose
bytes change between the read and the record is skipped, not consumed, and
taken whole next time. Two revisions in one second are two hashes. A copy
taken during an append would be a prefix, and a watermark by mtime would let
the second of two same-stamp revisions be certified consumed without appearing
in a page; neither exists here.

**Two dispatchers on one pool.** Both would move the same pending task into
`running/`. The move is the claim (`rename` is atomic within a directory, and a
dispatcher that loses the rename simply takes the next task), and on top of that
`run` holds a kernel lock on `<pool>/run.lock` for its whole life and a second
`run` exits 2 naming the holder.

**A dispatcher that dies with workers alive.** The kernel lock dies with it and
a second `run` can start; the slot map is on disk (rule 17), so the second
`run` adopts every worker whose pid and start stamp match its slot file and
finishes it from the supervisor's `exit.json` (it cannot `wait` on another
dispatcher's child, and it does not pretend to), **quarantines every
`reserved` slot whose runner is dead as `orphaned`** — under `slots.lock`,
after rechecking that the file still reads `reserved` with the same `nonce`,
adopting instead if an identify landed in between — with its `nonce` kept and
the task left where it is, because a supervisor paused before its identify
cannot be proven absent by its parent's death (rule 17), and frees such a slot
only on an `aborted.json` carrying that `nonce` with `survivors=0`, or by a
person's hand; reclaims every slot
whose whole process group is gone (running `finalize` for the job first, with
`end=unknown` when there is no `exit.json`); and quarantines everything it
cannot decide: a reused pid, a dead leader with a live survivor, an unreadable
file, an `aborted.json` naming survivors. A quarantined slot is not in the map, is named on `STATUS OK
quarantined=`, and is a person's to clear. Never is a reservation freed merely
because the runner that wrote it died.
Nothing about a slot is ever inferred from a directory listing or an age. The
dispatcher is not a lifecycle container for its children — a child in its own
process group outlives a SIGKILL of its parent by design, so that a dispatcher
crash does not throw away twenty minutes of a worker's reading — which is
exactly why the ownership must be durable.

**A task file read after it was moved.** The dispatcher reads the task text
**after** the rename into `running/`, from the path it renamed to, never from the
pending path it no longer owns.

**Per-job profiles.** The legacy `--worker` contract remains the compatibility
path. The proposed trusted catalog, explicit per-job profile/model selection,
compact prompt prefixes, provider-capacity observations, frozen non-secret
attempt snapshots and Go/Zen route rules are normative in
[`SPEC-SWARM-PROFILES.md`](SPEC-SWARM-PROFILES.md). That document is the one
owner of this amendment; it does not add a second dispatcher or ledger. Any
Go/Zen live-route activation remains a protected `nova-secrets exec` gate
with the approved store/command; a run without that provenance refuses before
the first worker, and the store-shape gate refuses exit 125. It is never
satisfied by task prose or a selector.

## What it deliberately does not do

- **It does not loop a worker.** One task per worker, always. The loop, if any,
  is the pool. A harness that cannot hold a polling loop will not be made to by
  this tool (2026-09-10: the Freddy loop is ruled out; the swarm is the shape).
- **It does not read a `RESULT.md` and act on it.** It counts, quotes and pages.
- **It does not write to the board, the bus, or any repository.** Workers write
  inside their job directories; announcing and filing are other tools' jobs.
- **It does not judge a finding.** `red`, `green` and `not done` are the
  worker's own words, counted.
- **It does not choose a model by itself.** In legacy mode the worker
  description chooses it. Profile mode permits only an explicit,
  allow-listed profile/model from the trusted catalog; any optional policy
  selector is bounded and stays inside this swarm. It invents no prices,
  quotas, fallback route, top-up or automatic cost scheduler.
- **It does not retry a failed task.** `requeue` with changed text is a person's
  decision. The default automatic exceptions are rule 7's one deadline re-queue
  and the rate-limit section's one true-429 retry. `run --no-auto-retry`
  declines both for its invocation.
- **It does not judge accuracy.** `accurate` and `wrong` are a reader's
  verdicts, recorded by `verdict`, and a batch line with no verdict prints a
  dash.
- **It deletes one thing, deliberately: a job directory, under `reclaim`,
  after the job's usage file and its report copy exist** (rule 12). Reports, usage files, slot
  files of a quarantined slot and the pool's own state are never deleted by
  any verb. A pool is a record, and the record is the part outside the
  reclaimable subtree.
- **It does not spawn a task chip or any other follow-up** (Glenn, 2026-09-10:
  **no task chips**; follow-ups go to the queue).
- **It does not become `nova-local`.** The local one-shot is folded into
  `native --model ollama/<tag>` and nothing else: no `status`, `serve` or
  `worker` verbs, no loopback pin, no shared weight store or box recipe
  (BOX-LOCAL.md), no ds4 engine, no digest or load measurement. What the
  separate `nova-local` spec promised that native does not is deleted with the
  fold — native runs the card on the Studio's local model and refuses beside a
  benchmark.

## What the prototype does that this spec forbids

The prototypes are `worker-swarm.sh` and `run-worker.sh` (and their frozen
Freddy siblings, which this section does not ask to change). These are the
places where this spec is deliberately **not** a transcription:

1. **The task text is `$1`.** Here it is a file or stdin: a task in an argument
   is a task in the process table.
2. **`--workers` above 64 is clamped with a note.** Here it is a refusal.
3. **A free slot is found by scanning the slot map inside a loop that also
   mutates it.** Here allocation and release are single steps against one
   authority, and a test pins it.
4. **A slot whose child survived the kill is reused on the next iteration.**
   Here it is retired for the rest of the run.
5. **`status` tails a log file to show what a worker is doing**, which means a
   log line's content reaches a report unescaped and unbounded. Here the tail is
   one line, through `internal/oneline`, capped at `oneline.TailBytes`.
6. **There is no cost accounting and no rate-limit handling at all.** Three of
   seven workers in batch 2 came back with nothing and the pool could not say
   whether a limit was the cause (it was a refusal; item 13). Here a sidecar
   records the exit code and the harness's token counts, and a 429 is backed
   off and retried once.
7. **The deadline is enforced by a one-second polling loop per worker**, one
   shell process per running task, and the dispatcher's own five-second poll on
   top. Here it is one process watching every child.
8. **A `RESULT.md` containing only a plan counts as success** if the worker
   exited 0. Here `run` classifies a result as `ok`, `clean`, `no-result` or
   `plan-only` — the last two move to `failed/` — because **a `RESULT.md` with
   only a plan is a failed task**, and only the tool can see that at the
   moment it happens; and a finished read with `findings: 0` is `clean` and
   in `done/`, because a classifier that failed it would be paying for
   findings (rule 8).
9. **`triage` parses the live `RESULT.md`.** Here the worker publishes whole
   revisions by rename, the tool reads only the renamed file, identifies a
   revision by its hash, and skips a file that moved under it (rule 16).
10. **The conditions that made batch 2 work live in the task text a person
    retypes.** Here they are templates in the binary, so they are not retyped and
    not forgotten — which is the single highest-value thing in this spec.
11. **The sandbox sentence is not in the prompt**, and two runs ended today on a
    refused read. Here it is in every prompt the tool writes.
12. **The harness config is written with the provider's model list hardcoded to
    one provider's ids.** Here the model list comes from the worker description.
13. **A refusal ended the run, and nothing said so.** The prompt did not say
    what a refusal means, and a scratch-file refusal outside the job directory
    ended 3 of 7 runs in batch 2 with a plan and no findings; the pool reported
    `rc=0` and `done`. Here the sentence covers reads and writes, the harness
    log is read for refusals, and `RUN DONE` prints `refusals=<n>` beside
    `plan-only` (rule 5).
14. **A job reaped at its deadline is gone.** Its files move to `failed/` and
    nothing runs it again. Here it is re-queued once, marked, and a second reap
    fails it (rule 7).
15. **Results are counted by a person.** The batch table in this spec was
    assembled by hand from 67 reports. Here `TRIAGE BATCH` is one line the
    tool prints, and `verdict` is how a reader's counts reach it (rule 8).
16. **The prompt asks for the findings at the end** (*do the task, write what
    you found*). Here it says append each finding the moment it exists, and a
    killed run's partial report is counted (rule 3).
17. **There is no file budget** unless the person typing the task remembers
    one. Here `add --files <n>` is required and the prompt carries it (rule 4).
18. **The pool, the log directory, the worker directory and the key file
    default from environment variables and `$HOME`.** Here every path is a
    flag or a field of the worker description, and no environment variable is
    consulted (SPEC.md, no guessing).
19. **`run-worker.sh`'s per-worker data directory is reclaimed with the
    job.** The harness's usage database lived there, and today's DeepSeek
    batches have no surviving usage at all. Here rule 12: usage into the
    `<pool>/usage/<job>.tsv` first, written by `finalize` before the job moves, and a reclaim with no usage file is refused.
20. **There is no path a message can take to a running worker.** A child that
    was doing the wrong thing could only be killed. Here rule 10: the note
    file, appended by the tool, counted in the report.
21. **Nothing says a job is one process, and nothing checks.** Children
    spawned background tasks, stopped to wait on them, and reported twice.
    Here rule 11: one process group, checked at exit, `RUN VIOLATION` and
    quarantine.
22. **The slot map lives in the dispatcher's memory, and a dispatcher killed
    with workers alive leaves a pool the next dispatcher reads as empty.**
    Here rule 17: slot files and pid files, adopt, reclaim or quarantine
    before the first claim.
23. **Usage, when it is copied at all, goes into the job's own directory.**
    Here rule 12: `<pool>/usage/<job>.tsv`, written by `finalize` before the
    job's files move, and `reclaim` refuses without it.
24. **The parent writes the child's pid after the fork, and a crash between
    the two leaves a worker nobody tracks.** Here rule 18: reserve, spawn,
    the child identifies itself, the runner waits for it or kills it, and a
    supervisor writes the completion evidence a replacement can read.
25. **A malformed report is quoted into the page as best the parser can.**
    Here rule 15: quarantined, never folded, and `result --id` is the one way
    a person sees it.
26. **A reclaimed job's report is gone with the job.** Here rule 12: the copy
    under `<pool>/reports/<job>/` is a precondition of `reclaim`.

## Tests this spec demands

One line per rule in **the rules, numbered**. Each runs against the fake
harness binary on `PATH`, inside `t.TempDir()`, with no network, and each must
be seen red before it is trusted.

1. The `read-pr` prompt contains the owed-list sentence before any other
   condition; a report with a finding that matches an owed item and is not
   marked `dup:` is counted `dup=1 new=0`.
2. A finding line with a verbatim quote and `file:line` counts as a finding;
   one without is counted `unquoted=1`, and the count prints on `TRIAGE BATCH`.
3. The prompt contains the append-as-found sentence; a fake worker that writes
   three findings a second apart and is killed after the second leaves a report
   with two finding lines, and `RUN KILLED findings=2` and `triage` both say 2.
4. `add` without `--files` is exit 2 and the sentence; `--files 0` is refused;
   the prompt contains the number and the spent-budget instruction.
5. A fake harness that refuses one read and one write mid-run and then
   continues: the run ends `result=ok refusals=2` with its findings; a fake
   harness that ends on the refusal: `result=plan-only refusals=1`, and the
   pair is on one `RUN DONE` line.
6. The key never appears in any child's argv, in any log, in any printed line,
   or in any file under the pool or the slot; the harness config contains the
   variable's name and not its value; a key file with a second line is read as
   one line.
7. Each job's clone path is under its own job directory and no two jobs share
   one; a worker that sleeps past its deadline is killed, `requeued=true
   reaped=1`, runs again, is killed again, `requeued=false reaped=2`, and lands
   in `failed/`; under `--no-auto-retry` its first kill is
   `requeued=false reaped=1` and creates no descendant. The dispatcher exits at
   `--hours` with the injected clock.
8. `TestCompletionIsEvidenceNotCount`: eight jobs — three with findings, one
   completed review with `findings: 0` and no finding lines, one completed
   `probe-row` with `findings: 0` and one item `not done` with its reason,
   one report with a plan and no head, one with no head and one finding
   line appended before the kill, and one with no `RESULT.md` — give
   `TRIAGE BATCH reports=7 clean=2 plan_only=2 no_result=1` on exactly one
   line, `findings=` counts the appended line of the headless report, the
   two `clean` jobs are in `done/` and `RUN OK` counts them done, the two
   `plan-only` are in `failed/`; with one recorded verdict `accurate` and
   `wrong` are its numbers and with none both print `-`; a mutation that
   classifies `findings: 0` as `plan-only` turns the test red.
9. N workers are N child processes, each with its own job directory and its
   own report file; a tripwire on every path a child opens for writing finds
   no path opened by two children; the merge into the page runs once, after
   the last worker is reaped.
10. `note` appends one stamped line to `<job>/note` and prints `NOTE OK
    notes=1`; a fake worker that reads the file between two steps and writes
    `notes read: 1` ends `RUN DONE notes=1/1`; a fake worker that never reads
    it ends `notes=1/-`; the prompt contains the note sentence; `note` on a
    task that is not running is `NOTE REFUSED`.
11. A fake worker that forks a process which outlives it ends `RUN VIOLATION
    background=1`, the survivor is dead afterwards, the job is in `failed/`
    with `violation=background`, and `TRIAGE BATCH` does not count its report;
    a worker that forks and waits for its child is not a violation; the prompt
    contains the one-process sentence; every job prints exactly one of `RUN
    DONE` or `RUN VIOLATION`.
12. `TestUsageOutlivesTheJob`: a fake harness that writes a usage row into the
    job's data home: after exit `<pool>/usage/<job>.tsv` exists, with the
    sixteen columns in order, `tokens_in`, `tokens_out`, `cache_write`,
    `cache_read`, `reasoning`, `model` and `repo` equal to the row, `end=done`
    and `attempt=1`, and its mtime is older than the job's move into `done/`
    (the file is written before anything moves); `reclaim` on that job
    removes the directory and prints `RECLAIM OK usage=<path>`; `reclaim` on
    a job with no usage file is `RECLAIM REFUSED … no usage file` exit 1 and
    the directory is intact; `cost` prints the numbers after the directory
    is gone; a copy whose bytes are altered after `finalize` makes `reclaim`
    `RECLAIM REFUSED … does not match REV` with the directory intact, and a
    `REV` naming the attempt and the copy's SHA-256 exists beside every copy
    and every marker; **finalize → reclaim → first triage**: a job finalized and
    reclaimed before any `triage` ran is in the next page with the same
    `rev=` hash and the same bytes as `<job>/RESULT.md` had, read from
    `<pool>/reports/<job>/RESULT.md`, and `result --id` prints them; a job
    whose usage file exists and whose report copy is missing is `RECLAIM
    REFUSED … no report copy` exit 1 with the directory intact, and
    `finalize` writes the copy; a malformed job reclaims only with the
    `MALFORMED` marker present and a no-result job only with `NO-RESULT`; a
    provider that reported no cache counts prints `cache_write=-
    cache_read=-` and `COST OK dashes=0,0,1,1,0`; a job killed at its deadline
    has a row with `end=killed` and whatever partial usage the database held;
    its re-queue is a second file with `attempt=2 from=<old-id>`, and `COST
    OK tasks=2` sums each once; a dispatcher killed with an injected kill
    point between the child's exit and `finalize` leaves no usage file, the
    next `run` writes it on start and prints `RUN RECLAIM … usage=<path>`,
    and `finalize --task` on a job whose group is alive is `FINALIZE REFUSED`.
13. `TestBudgetEndsTheJobAndKeepsFindings`: a fake harness that appends a
    finding then a usage row past `--tokens`: the job ends with `RUN BUDGET`,
    the finding stands in `RESULT.md`, the usage row carries the final sum
    and `end=budget`, and a job under budget is untouched; `add` and `batch`
    without `--tokens` are exit 2 naming the flag and `--tokens 0` is
    refused; a fake harness that writes no usage at all runs to its deadline
    and prints `budget=-/<n>`; one that writes usage only after the job has
    passed the budget is ended at the next sample, the overshoot is in the
    row, and the `RUN BUDGET` `spent=` is at least the budget; one that
    reports only `tokens_in` prints `budget=<n>+/<n>`; `--tokens unmetered`
    prints `budget=unmetered` and runs to its deadline; a worker description
    with `usage: none` beside a pending numeric budget is exit 2 before any
    worker starts, naming the task, and beside `unmetered` tasks runs; a
    fake usage source that errors on three consecutive samples ends the job
    `RUN BUDGET-UNVERIFIABLE … samples=3` with `end=budget-unverifiable`,
    the findings kept, and one that errors twice then answers does not;
    `requeue` without `--files` or `--tokens` is exit 2 naming the flag.
14. `TestOneCommandUpOneLineDown`: `batch --tasks` over three template
    tasks queues three jobs from files with no prompt text on the command
    line, `BATCH OK tasks=3`, every sidecar carrying the one batch id, and a
    fourth unreadable file queues nothing (exit 2); `triage --batch <id>` on
    their results prints `TRIAGE BATCH batch=<id>` with counts first, at
    most `--max` finding lines, then `TRIAGE MORE kind=finding at=<path>`,
    never a transcript line, and a job queued by `add` beside them is not
    in it; `triage --batch` of an unknown id is `TRIAGE REFUSED` exit 1;
    `result --id` prints `RESULT OK` and the file's bytes exactly, and an
    unknown id is `RESULT REFUSED` exit 1.
15. `TestResultShapeIsMechanical`: a `RESULT.md` whose head lacks the
    `findings: <n>` line, or whose item table has a fourth state word, is
    `RUN MALFORMED` with the line number, the job is in `failed/` with
    `malformed=<line>`, `TRIAGE BATCH malformed=1`, no line of the file is in
    the page even though it holds two well-formed finding lines, and `result
    --id` prints it verbatim (a report with no head at all is `plan-only`,
    test 8, never malformed); a malformed report whose head says `findings:
    0` is `malformed`, not `clean`, and is not in `done/`; two jobs with equal
    `repo:` and `rev:` reporting one (file, line, rule) fold to one finding
    with `dup=1` printed; the same two lines under different `rev:` values,
    or with `rev:` missing from either head, stay two findings with `dup=0`;
    `./internal/x.go:10` and `internal\x.go:10` under one `rev:` fold to one
    finding whose page line and triage line carry `jobs=` naming both job
    ids;
    a source test finds no code path from the parser's error to the page
    writer.
16. `TestAReportIsARevision`: a fake worker that publishes three revisions
    by writing `RESULT.md.tmp` and renaming, the third while `triage` is
    between its first hash and its parse (injected pause): the page holds
    revision two or three entire and never a prefix, and the revision not
    in the page is folded by the next run; two revisions renamed into place
    inside one second with the filesystem's mtime resolution forced equal
    are two `rev=` hashes and both appear in pages; a fake worker that
    appends in place during the pause gives `TRIAGE SKIPPED id=<id>: changed
    while read`, nothing recorded in `consumed` for it, and the whole file in
    the next page; `triage` never opens `RESULT.md.tmp` (a tripwire on every
    path opened); a worker killed with a `RESULT.md.tmp` on disk ends `RUN
    KILLED … unpublished=true` and the tmp file is still there afterwards,
    unread; the source tripwire finds no `ModTime` in the package.
17. `TestADeadDispatcherIsRecoveredOrQuarantined`: a dispatcher SIGKILLed
    with one worker alive, one worker exited but not finalized, and one
    worker whose leader is dead and whose group has a survivor, then `run`
    again on the same pool: the live worker is adopted (`RUN ADOPT` with its
    original start stamp, its deadline honoured from that stamp, exactly one
    `RUN DONE` for it, printed by the second dispatcher, and its slot never
    handed to a pending task while it lives), the exited job is finalized
    and reclaimed (`RUN RECLAIM … usage=<path>`, its slot then allocated to
    a pending task), the third slot is `RUN QUARANTINE` with the reason,
    never allocated, `STATUS OK quarantined=1`, and the `run` exits 1 at its
    end; a slot file whose pid is alive with a different start stamp is
    quarantined; a fourth slot `reserved` by the dead dispatcher is
    `orphaned`, its `nonce` kept, never freed by this run and never handed
    to a pending task; the tripwire finds no `pgrep`, no `ps` and no match on a
    command line; a second `run` while the first is alive is still exit 2
    naming the holder; a slot whose supervisor is dead with no `exit.json`
    is finalized `end=unknown` into `failed/`, its published report copied,
    and never counted `ok` or `clean`.
18. `TestTheLaunchIsATransaction`: with an injected kill point at each
    boundary of rule 18 — after reserve, after spawn, after identify, after
    the handshake, after release, and between the harness exit and
    `exit.json` — the next `run` on the pool decides every slot with no
    guess: after reserve, `RUN QUARANTINE slot=<n> … launch unproven`, the
    slot `orphaned` with its nonce kept, and the task pending again only
    after `aborted.json` or a person removes the file; after spawn but before
    identify, the supervisor identifies itself anyway (its CAS finds the
    unchanged reservation) and the next run adopts it by the stamp it wrote; after
    identify and after release, adopt, with one `RUN DONE` from `exit.json`;
    between exit and `exit.json`, `end=unknown` into `failed/`; a fake
    supervisor that never writes its identity is killed at
    `--launch-timeout`, `RUN LAUNCH-FAILED` with the reason, the task in
    `failed/` with `launch=failed`, the slot free and no process of its
    group alive; a supervisor SIGKILLed after release with the harness
    alive is, on the next `run`, a dead leader with a live survivor and is
    quarantined, never adopted and never finalized; a planted `exit.json`
    whose `nonce` is not the slot file's is quarantined and never finalizes
    the job, and the slot file's `nonce` equals the one the supervisor was
    handed; **the
    reverse schedule** (Stella, 2026-09-11): the supervisor is SIGSTOPped
    after spawn and before identify, the runner is SIGKILLed, a second `run`
    on the pool with `--workers 1` and a pending task finds the reserved
    slot: `RUN QUARANTINE slot=<n> … launch unproven`, the slot file now
    `state: "orphaned"` with the same `nonce`, the pending task is **not**
    started on that slot and no `RUN RECLAIM … end=unlaunched` is printed;
    the supervisor is then SIGCONTed: its identify finds the changed state,
    it writes `<job>/aborted.json` with that `nonce` and `survivors=0`,
    exits 2 with
    `SUPERVISE ABORTED`, no process of its group survives, and the fake
    harness records **zero** launches for the job; **`aborted.json` is on
    disk before the exit status is observable**: the test that reaps the
    supervisor finds the file already there, and an injected kill point
    between the rename and the exit leaves the file, which the next `run`
    honours; a mutation that exits, or kills its group, before the rename
    turns the test red; **a hangup never costs the acknowledgement**: with the
    two deaths staged the other way round — the supervisor stopped BEFORE the
    runner's death, so that the kernel hangs its newly orphaned group up — the
    supervisor survives, is resumed by the SIGCONT that comes with the hangup,
    and completes its launch transaction, and a mutation that lets the hangup
    end it turns the test red with the slot quarantined `reserved, launch
    unproven`; a planted `aborted.json` with `survivors=1` keeps
    the slot quarantined (`RUN QUARANTINE … aborted, survivors=1`) and
    never prints `RUN RECLAIM`; the next `run` reads
    `aborted.json`, prints `RUN RECLAIM … end=unlaunched`, the task is
    pending again and the slot is allocated to it, and the fake harness then
    records exactly one launch; a mutation that frees the slot on a dead
    `runner_pid` alone turns the test red with two launches on one slot or
    the old supervisor's identity written over the new reservation; **the
    two locks**: a normal launch's supervisor identifies while its parent
    still holds `run.lock` and the handshake completes within the timeout
    (a mutation that takes `run.lock` in identify turns the test red with
    `RUN LAUNCH-FAILED`); **recovery paused before its slot lock**: the
    runner is SIGKILLed after spawn with the supervisor SIGSTOPped before
    identify, a second `run` is paused with an injected point after it has
    read the `reserved` slot and before it takes `slots.lock`, the
    supervisor is SIGCONTed and identifies, then recovery resumes: it
    rechecks under `slots.lock`, finds `launched`, adopts (`RUN ADOPT`) and
    never writes `orphaned` over the identity; a mutation that skips the
    recheck turns the test red with the identified supervisor's slot
    orphaned and its `exit.json` rejected; every write to a slot file in
    the test happens with `slots.lock` held (the tripwire on the lock's
    holder) and no code path waits with it held; a slot
    left `orphaned` with no `aborted.json` and its runner dead stays
    quarantined across three runs, `STATUS OK quarantined=1`, until the file
    is removed; a mutation that lets identify land without comparing the
    `nonce` and `state: "reserved"` turns the test red; with `--workers 2`,
    a reserved slot counts as held and a
    third job is never started; the slot file's pid, pgid and start stamp
    were written by the process they name (the fake supervisor records its
    own values and the test compares); with no live `run`, `supervise` refuses
    a missing or mismatched slot nonce at exit 2, while a matching nonce in
    `reserved`, `orphaned` or `launched` state reaches the normal recovery
    checks rather than failing merely because the runner has died; the tripwire
    on every path opened for writing finds
    `slots/<n>.json` written by the runner once (reserved), by the
    supervisor once (launched) and by a recovering dispatcher at most once
    (orphaned), never by two writers for the same state.
19. **The launch seam's wall** (docs/SPEC-SANDBOX.md, the dispatcher caller),
    four tests, each seen red first. The two that ask the OPERATING SYSTEM run
    against the real `nova-sandbox`, on the platform whose body is built, and
    skip elsewhere BY NAME: a job whose task text tells it to create a file
    outside its job directory fails, the file is absent afterwards, and the
    same task text under `--no-sandbox` lands it (the control, so the denial
    cannot pass by being impossible); a job cannot read the key file whose
    VALUE it holds in its environment, with the same run asserting that the
    value did arrive and that it is in no printed line. The two that ask THIS
    TOOL run everywhere, against a fake sandbox on `PATH`: `run` with a failing
    probe is `RUN REFUSED reason=sandbox_probe` and with no backend
    `RUN REFUSED reason=no_sandbox`, both with the task still pending and no
    `RUN START` printed; `--no-sandbox` prints exactly one `RUN UNSANDBOXED`
    line per job, and the same bench with every plausible environment variable
    set and the flag gone wraps anyway. And the argv itself: the worker home as
    `--read`, the job directory as the FIRST `--write` with the data home
    beside it, the job directory as `--cwd`, no `--net-deny`, and a directory
    planted in the task text that appears in no flag of it.

## The work list

To build it in Go under `cmd/nova-swarm`, the way `cmd/nova-bus` is built:
standard library only, no hardcoded paths, no default paths, the exit grammar
above, `internal/oneline` for every printed value, `internal/bounded` for every
listing, and `ONBOARDING.md`'s first-day standard — a usage banner ending in a
runnable `example:` block, refusals that say what the flag wants and report every
independent problem at once, a `### First run` in `docs/CLI.md`, a `quickstart`
verb, and tests that pin all three by executing them.

1. **`internal/swarm/pool.go`** — the pool directory: `pending/`, `running/`,
   `done/`, `failed/`, `reports/` (pages `<UTC>.md` and one `<job>/`
   directory per finalized job), coordinator-owned protected `evidence/`,
   `scratch/`, `slots/`, `usage/`, the task id
   scheme (UTC stamp, label, random half, so two adds in one second cannot
   collide — `nova-bus`'s id lesson), the sidecar file with `files`,
   `requeued`, `reaped`, `from`, `batch`, `tokens`, `launch`, `malformed`,
   and optional profile-attempt fields (`profile`, `model_requested`,
   `model_observed`, `snapshot_hash`, `prompt_hash`, `config_hash`, `bench`)
   and the verdict, the atomic claim by rename, the kernel lock on
   `run.lock` (dispatcher exclusion only; slot transitions take
   `slots.lock`, item 3), the default one automatic re-queue of a reaped job
   and run-scoped `--no-auto-retry` exception, and `finalize`:
   the usage file, then the report copy or its marker under
   `<pool>/reports/<job>/`, each through `.tmp` and rename, before any move
   (rule 12), and `reclaim` refusing without both. Tests: two claimants, one
   winner; an id collision is impossible by construction; demanded tests 7
   and 12.
2. **`internal/swarm/key.go`** — the key file read as data: first line, the two
   strip rules, whitespace out, empty refused with the creating command. Tests:
   a key never appears in any returned string; a file with a second line is read
   as one line; `export VAR=` and a bare key both work; mode warnings.
3. **`internal/swarm/slot.go`** — the slot map as files: the launch
   transaction of rule 18 (reserve by the runner, identify by the supervisor,
   the handshake with `--launch-timeout`, `LAUNCH-FAILED`), `<pool>/slots/
   <n>.json` and `<job>/pid` with state, pid, pgid, the kernel's start stamp
   and the runner's pid, free-on-finalize in one step, retire a slot whose
   child survived, and the start-up pass: unlaunched, adopt, reclaim,
   unknown or quarantine for every slot file before the first claim (rule
   17), the short-lived `<pool>/slots.lock` around every slot-file
   transition and never across a wait, the orphaned reservation written
   under it after a recheck of `reserved` + nonce (adopt if `launched`),
   with its nonce kept and
   freed only on `aborted.json` with `survivors=0`, the runner's own kill or a person. Tests:
   `n` workers against `n-1` slots never double-hold; a
   surviving child's slot is never reallocated; a reused pid is quarantined,
   not adopted; a reserved slot with a dead runner is quarantined, never
   freed; a recheck that finds `launched` adopts; demanded tests 17 and 18.
3a. **`internal/swarm/supervise.go`** — the supervisor: identify itself in
   the slot and pid files by compare-and-swap on `state: "reserved"` and
   its own nonce under `slots.lock` (never `run.lock`), abort on any other
   content with no harness spawned and `<job>/aborted.json` written, fsynced
   and renamed **before** exit 2, `survivors` counted rather than assumed,
   spawn the harness in its group, hold the
   deadline
   and the budget sampling (rule 13), write `<job>/exit.json` as the
   completion evidence, refuse a hand-typed invocation. Tests: the identity
   in the files is the supervisor's own; an identify against a changed slot
   never spawns the harness and leaves `aborted.json` on disk before its
   exit is observable; `exit.json` is written through
   `.tmp` and rename after the harness exits and before the supervisor
   exits; demanded tests 13 and 18.
4. **`internal/swarm/worker.go`** — the worker description (strict decode: an
   unknown field is a refusal), the trusted profile catalog and per-job
   allow-list resolution, the slot refresh (one way, copy), the harness config
   written with the variable's **name**, the legacy/compact prompt assembly,
   bounded prefixes and tool profile, and the harness log read for refusal
   lines after the run. Tests: legacy config/prompt byte compatibility; unknown
   profile/model and unsupported tools refuse before launch; the generated
   config contains the variable name and not the value; compact prompts retain
   every mandatory invariant; demanded tests 4, 5 and 6.
5. **`internal/swarm/deadline.go`** — one watcher over every child: terminate,
   wait, kill, report `survived`. Tests: a child that ignores terminate is
   killed; the report says so; the watcher never matches a process by its command
   line.
6. **`internal/swarm/templates.go`** — the three task templates, the
   `RESULT.md` template, the `setup` agreement form (#184) and the `capacity`
   offer-and-routing form (#176), as embedded text, each with its conditions
   and the number that produced it. Tests: `template --name` prints each;
   `add --template` wraps a task and the result contains every condition;
   `add --template setup` and `add --template capacity` are refused, the
   setup form carries no private name, path or value, and the capacity form
   carries no key, token, or private host detail.
7. **`internal/swarm/result.go`** — the `RESULT.md` parser: the three states,
   the Per item and Gates tables, `Left owed`, `One line`, the finding lines
   with their `dup:` marks and their verbatim quotes, the owed-list match, the
   head's `findings: <n>`, `repo:` and `rev:` lines, the `ok`, `clean`,
   `plan-only`, `no-result` and `malformed` classifications, and the
   quarantine: a parse error returns the line number and no findings, ever.
   Tests: a malformed report is quarantined and yields no finding; `findings:
   0` with a head is `clean` and a plan with no head is `plan-only`; a fourth
   state word is `malformed` with its line; demanded tests 1, 2, 3, 8 and
   15.
8. **`internal/swarm/triage.go`** — the consumed map keyed by job id and
   revision hash, read-hash-parse-rehash, `TRIAGE SKIPPED`, `TRIAGE
   QUARANTINED` with no page line, the walk over `<pool>/reports/<job>/` for
   finalized jobs and `<job>/` for running ones, `--batch <id>` by sidecar,
   the (repo, rev, file, line, rule) de-duplication, the page writer, the
   counts, the one `TRIAGE BATCH` line with `batch=`, `malformed=` and
   `budget=`, the ranked finding lines under `--max`, the verdict sums with
   the dash for absence; no mtime anywhere; and `result --id`, the verbatim
   printer. Tests: a report rewritten during triage is skipped and taken
   whole next run; the consumed map advances once per run; `--no-state` does
   not advance it; `RESULT.md.tmp` is never opened; demanded tests 9, 12, 14,
   15 and 16.
9. **`internal/swarm/cost.go`** — the usage file reader over `<pool>/usage/`,
   the sixteen columns, the window, the absence-is-a-dash rule with the
   `dashes=` tuple, one row per attempt, and the 429 backoff. Tests: a task
   with no accounting prints dashes; a 429 is retried once and then failed
   with its code; two attempts sum once each; demanded test 12.
10. **`cmd/nova-swarm/main.go`** — the verbs, including `verdict`, `batch`,
    `result` and the `supervise` entry, the flag parsing with this repo's
    one-line refusals, `--files` and `--tokens` required and zero refused on
    `add` and `batch`, the output grammar exactly as above, `--max` on every
    listing.
11. **`cmd/nova-swarm/*_test.go`** — the contract tests: every exit code, every
    refusal sentence, a fake harness binary on `PATH` so the dispatcher is
    tested end to end with no provider, `--workers 65` refused, a capped listing
    is a prefix with a MORE line, `RUN NOTE` is exactly one line, and **no test
    reaches outside `t.TempDir()` or touches the network** (CONTRIBUTING.md,
    **test code is code**).
12. **The read and the switch**, before anything replaces a script: one recorded
    read against this spec by a line that is not the author, then one batch run
    beside `worker-swarm.sh` on the same task list with the two results compared.
    `freddy-swarm.sh` and `run-freddy.sh` are not touched by any step above.

## Ideas folded on 2026-09-11

**2026-09-15 — the local one-shot is folded into `native`.** `nova-swarm native
--model ollama/<tag>` runs the card on the Studio's local model with the same
wall, card contract and RESULT rules, refused by the benchmark window until its
stamp, so a local job never runs beside a benchmark; the separate
`docs/SPEC-LOCAL.md` is deleted with the fold.

| source | the idea, in six words | disposition |
|---|---|---|
| Stella, spec repairs | reservation with a nonce counts immediately | rule 18: `nonce` on the reservation, the identity and `exit.json`; already counted (rule 18, last sentence) |
| Stella, spec repairs | abort launch on lost handshake pipe | left different: the supervisor's identity is durable before the harness starts, so an orphaned launch whose identify landed is adopted by stamp (test 18) rather than aborted; nothing runs unrecorded, and the deadline is the supervisor's |
| Stella, closing read | orphaned reservation is ambiguous, not unlaunched | rule 17: quarantined as `orphaned`, nonce kept, freed only when launch absence is established; rule 18: identify is a CAS on `reserved` + nonce, else `SUPERVISE ABORTED` with `<job>/aborted.json` and no harness (test 18, the reverse schedule) |
| Stella, final read | finish the reservation transaction consistently | rules 17 and 18, **Slots**, **The races**: `run.lock` excludes dispatchers only, `slots.lock` guards each slot transition and is released before any wait; orphaning rechecks `reserved` + nonce under it and adopts a `launched` slot instead; the aborting supervisor writes, fsyncs and renames `aborted.json` before exit 2 and records `survivors` rather than claiming absence; a dead runner never frees a reservation (tests 17 and 18) |
| Stella, spec repairs | supervisor publishes authenticated exit record | rule 18: `exit.json` carries the nonce; a foreign nonce quarantines (rule 17) |
| Stella, spec repairs | retain report and usage before reclaim, hashed | rule 12: `REV` beside the copy; `reclaim` verifies it |
| Stella, spec repairs | budget is monitored, refuse unverifiable accounting | rule 13: `usage:` in the description, `unmetered`, `RUN BUDGET-UNVERIFIABLE` |
| Stella, spec repairs | `--tokens` on requeue and stored metadata | grammar: `requeue --files --tokens` required |
| Stella, spec repairs | dedup by normalized file, keep job ids | rule 15: normalized path, `jobs=` on the merged finding |
| Stella, spec repairs | strict quarantine of malformed reports | already, rule 15 |
| Stella, idea 5; DeepSeek, idea 5; Freddy, idea 3 | one structured worker result, capped | already, rules 14 and 15 |
| Stella, idea 6 | measure decisions, not turns; limit fan-out | already, rule 8 (`accurate`/`wrong`), `--workers` cap, `--files` |
| DeepSeek, idea 6 | a token budget per task | already, rule 13 |
| DeepSeek, idea 4 | pull-based DAG, coordinator for exceptions | not folded: one task per worker is the shape (rule 11); dependencies are the board's and the coordinator's, not a second scheduler here |
| Rowan, idea 3 | one command up, one line down | already, rules 13, 14, 15 |
| Rowan, idea 8 | cheapest model that passes the test | already: the worker description chooses the model; the tool has no opinion — and the choice is by measured task quality plus retry and review cost, which rule 8's `accurate`/`wrong` and rule 13's usage file supply, never the unit rate alone (Stella's closing read) |
| Freddy, idea 8 | workers sync deltas, not full state | already, rule 10 (the note file) and rule 15 (the result shape) |
| ideas #275 | refutation rate as a health metric | already, rule 8: `accurate`/`wrong` per batch is that rate; a falling `wrong` share is the reader's to notice |
| ideas #273 | cleanup arriving before its notification | already, rule 18: `exit.json` before the slot is freed, and the nonce ties the evidence to its launch |
| ideas #357 | reason about a report, never execute | already, the data paragraph at the top |
