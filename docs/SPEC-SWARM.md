# nova-swarm — specification

`nova-swarm` is one binary at the **worker layer**. It runs a pool of **one-task
workers** — any provider, any model, through one harness — each with its own
working directory, its own data home, its own job directory and its own deadline
held by the machinery rather than by the worker.

This spec is normative. If the code and this document disagree, one of them has
a bug, and the tests decide which. It stands beside [SPEC.md](../SPEC.md), whose
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
| "the slot is written before the child starts" named no transaction: a crash between the fork and the write left a running worker nobody tracked (**Stella's second read, 2026-09-11**) | the **launch transaction** (rule 18): the runner reserves the slot with a placeholder, the child writes its own pid, pgid and start stamp into it before doing anything else, the runner waits for that write with a bounded timeout or kills and marks `LAUNCH FAILED`; the child is a **supervisor** that writes durable completion evidence, and an outcome with none is `unknown`, never guessed |
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
   silent past its deadline is reaped and its job is re-queued once, with
   `requeued=1` in the new task's sidecar; a job reaped a second time goes to
   `failed/` with `reaped=2` and is not re-queued again.
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
    `budget`, `violation`, `failed`, `unknown` (no completion evidence, rule
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
    `<pool>/reports/<job>/NO-RESULT` is written instead; and **only then**
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
    evidence would be inside the thing about to be removed; `finalize` on a
    job whose usage file exists but whose copy is missing writes the copy and
    prints `FINALIZE OK … existed=true`. `cost` reads `<pool>/usage/` and nothing else, so it answers
    after the directory is gone; a killed attempt's partial usage stands in
    its own row. (2026-09-11: DeepSeek's usage for two batches lived in
    per-worker data directories that were reclaimed with the jobs, and
    nothing survived.)

13. **The swarm's own tokens are budgeted per job, and the machinery ends the
    job at the budget it can see.** Every job carries `--tokens <n>` (no
    default; `add` and `batch` without it are exit 2 naming the flag, and `0`
    is refused); the supervisor samples the provider's usage as the job runs
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
    has and prints `budget=<n>+/<n>` with the plus. A worker
    is handed a task file and the files the task names, never a conversation
    and never a repository to wander: the task template's file list is the
    reading list, `--files` is its ceiling, and a job that reads past it is
    the refusal rule 1 already names. (2026-09-11: 21 children spent 2.4M
    tokens on this bench, most of it re-reading what the task could have
    handed them.)
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
    are two findings — and the duplicate count is printed, because a
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
    revision last folded into a page. A `RESULT.md` whose hash is not the
    recorded one is new and is folded; a file whose hash **changes between
    the read and the end of the parse** — a writer that ignored the protocol
    and appended in place — is not folded this run, prints `TRIAGE SKIPPED
    id=<id>: changed while read`, and is folded by the next run when it holds
    still; nothing is recorded as consumed unless the bytes recorded are the
    bytes in the page. After the job's process group is dead, a leftover
    `RESULT.md.tmp` is left where it is as data, never renamed by the tool and
    never read, and the job's `RUN` line carries `unpublished=true`; the
    published revision is what is counted. (Stella, 2026-09-11: a copy taken
    during an append is a prefix, and two revisions can share an mtime.)
17. **A dispatcher that dies leaves durable ownership, and the next one
    recovers it or quarantines it.** A slot is a file, `<pool>/slots/<n>.json`,
    written by the launch transaction of rule 18 and holding, once launched,
    `{job, state, pid, pgid, pid_started, runner_pid}` where `pid_started` is
    the process start stamp the kernel reports for that pid; each job also
    holds `<job>/pid` with the same fields. The pool lock is a kernel lock and
    dies with its holder. On start, before it claims any pending task, a
    dispatcher reads every slot file and decides each one: `state=reserved`
    and `runner_pid` dead — **unlaunched**: no child was ever identified, the
    task goes back to `pending/` untouched with `launch=unlaunched` in the
    sidecar, and the slot is freed (`RUN RECLAIM … end=unlaunched`); the pid
    is alive **and** its start stamp matches the file — **adopt**: the job is
    watched from here, its deadline computed from the recorded start, and it
    ends with the same `RUN DONE`, `RUN KILLED` or `RUN VIOLATION` it would
    have had, printed by this dispatcher, from the supervisor's completion
    evidence `<job>/exit.json` (rule 18), because a dispatcher cannot `wait`
    on a process that is not its child and never pretends to; the pid is
    dead, no process in the recorded group is alive, and `<job>/exit.json`
    exists — **reclaim**: `finalize` runs for the job, its files move as rule
    12 says, and the slot is freed; the pid is dead, the group is dead, and
    there is **no** `exit.json` — the outcome is **unknown**: `finalize` runs
    with `end=unknown`, the files go to `failed/` with `end=unknown` in the
    sidecar, the published `RESULT.md`, if any, is kept and copied but the
    job is never `ok` or `clean`, and the slot is freed; anything else — a
    pid alive with a different start stamp (pid reuse), a dead leader with a
    survivor in the group, an unreadable slot file, a slot file with no
    matching `<job>/pid`, a `reserved` slot whose `runner_pid` is alive under
    another start stamp — is **quarantined**: the slot is never allocated for
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
    (1) **reserve** — under the pool lock the runner writes
    `<pool>/slots/<n>.json` with `{job, state: "reserved", runner_pid,
    runner_started, reserved_at}` and no pid, through `.tmp` and rename, and
    moves the task into `running/`; (2) **spawn** — the runner forks the
    **supervisor**, `nova-swarm supervise --pool <dir> --task <id> --slot
    <n>`, this binary again, as the leader of a new process group; (3)
    **identify** — the supervisor, **before doing anything else**, writes its
    own `pid`, `pgid` and kernel start stamp into the slot file and into
    `<job>/pid` with `state: "launched"`, each through `.tmp` and rename;
    (4) **handshake** — the runner waits for `state: "launched"` to appear in
    the slot file, up to `--launch-timeout` seconds (default 10, a tool
    property like a timeout); if it does not appear, the runner kills the
    supervisor's group, writes `launch=failed` into the sidecar, moves the
    task to `failed/`, frees the slot, and prints `RUN LAUNCH-FAILED id=<id>
    slot=<n> after=<d>: no identity within <n>s`; only after the handshake is
    `RUN START` printed, with the identified pid; (5) **release to work** —
    the supervisor spawns the harness as its child in the same group, with
    the key in the child's environment (rule 6), and holds the job's
    deadline and budget (rules 7 and 13); (6) **finalization** — when the
    harness exits, the supervisor writes `<job>/exit.json` with `{rc, signal,
    ended, survivors}` through `.tmp` and rename, **the durable completion
    evidence**, then exits; the runner, or an adopting dispatcher, reads that
    file, runs the group check of rule 11 and `finalize` of rule 12, and
    prints the job's one `RUN` line. The runner never allocates a slot whose
    file exists in any state, so the worker cap `--workers` counts reserved
    slots as held; a `supervise` typed by hand is refused at exit 2 when no
    live `run` holds `<pool>/run.lock`. (Stella, 2026-09-11: two files
    written by the parent are not one atomic step, and a replacement
    dispatcher cannot reap a process that is not its child.)

## The verbs

```
nova-swarm add      --pool <dir> --task <file>|--stdin --files <n> --tokens <n> [--label <text>] [--template <name>] [--deadline <duration>]
nova-swarm batch    --pool <dir> --tasks <dir> --files <n> --tokens <n> [--label <text>] [--template <name>] [--deadline <duration>]
nova-swarm run      --pool <dir> --workers <n> --hours <h> --worker <file> [--max <n>] [--launch-timeout <s>] [--usage-interval <s>]
nova-swarm supervise --pool <dir> --task <id> --slot <n>          (spawned by run; refused by hand, rule 18)
nova-swarm status   --pool <dir> [--max <n>]
nova-swarm stop     --pool <dir>
nova-swarm requeue  --pool <dir> --task <id> --task-file <file>|--stdin [--label <text>]
nova-swarm verdict  --pool <dir> --task <id> --who <name> --accurate <n> --wrong <n>
nova-swarm triage   --pool <dir> (--batch <id> | [--dir <dir>]...) [--since <stamp>] [--all] [--no-state] [--max <n>]
nova-swarm result   --pool <dir> --id <job>
nova-swarm template --name <read-pr|probe-row|fix-card|result>
nova-swarm cost     --pool <dir> [--since <stamp>] [--max <n>]
nova-swarm note     --pool <dir> --task <id> --text <text>
nova-swarm finalize --pool <dir> --task <id>
nova-swarm reclaim  --pool <dir> (--task <id> | --done) [--max <n>]
```

`--tokens <n>` is the token budget (rule 13). It has no default and `0` is
refused, on `add` and on `batch` alike, for the reason `--files` has none.

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

`--files <n>` is the file budget (rule 4). It has no default: a budget this
tool supplied would be a guess about somebody else's task. Zero is refused,
because a worker that may open no file is a worker asked for a plan.

`verdict` records a reader's counts for one task: how many of its findings
were accurate and how many wrong, by name, into the task's sidecar. It is the
only way `accurate` and `wrong` reach a batch line, and it is a person's act.

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

**`--worker <file>`** names the worker description: which provider, which model,
which env var the provider reads, which base URL, and where the key file is. It
is a file because it is configuration a person wrote, and it is **required**
because this tool has no opinion about whose model runs.

`status`, `triage`, `result`, `template` and `cost` **report** and exit 0
(their refusals are exit 1 as the table says). `run`, `add`, `batch`,
`requeue`, `note`, `finalize` and `reclaim` are the verbs that act; `supervise`
is `run`'s child and nobody's verb.

## Exit codes

| code | meaning |
|------|---------|
| 0 | the verb ran and passed: a task queued, a batch queued, a pool drained, a page written, a report printed |
| 1 | the verb ran and said **NO**: a dispatcher that exited with tasks still pending and nothing running, a `requeue` of an id that is not in the pool, a `triage` over a directory that holds no reports when one was named, a `reclaim` of a job with no usage file or no report copy, a `finalize` of a job whose process group is alive, a `run` that ended with a quarantined slot or a `LAUNCH-FAILED` job, a `batch` over a directory with no task file, a `triage --batch` of an id no sidecar carries, a `result --id` of an id not in the pool or with no published report |
| 2 | could not run: missing flag (`--files`, `--tokens` included), unreadable pool, unreadable worker description, a key file that is absent or empty, `--workers` above the cap, a `batch` with an unreadable task file, a `supervise` typed by hand, bad invocation |

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
ADD OK id=<id> label=<label> template=<name|-> deadline=<d> tokens=<n> batch=<id|-> pending=<n>
ADD REFUSED: <reason>
BATCH OK id=<id> tasks=<n> pending=<n>
BATCH REFUSED: <reason>
RUN POOL workers=<n> hours=<h> worker=<name> model=<model> pool=<dir>
RUN START id=<id> slot=<n> pid=<n> pgid=<n> started=<stamp> deadline=<d> tokens=<n> job=<path>
RUN LAUNCH-FAILED id=<id> slot=<n> after=<d>: <reason>
RUN ADOPT id=<id> slot=<n> pid=<n> started=<stamp> remaining=<d>
RUN RECLAIM slot=<n> id=<id> end=<done|killed|failed|budget|unknown|unlaunched> usage=<path|->
RUN QUARANTINE slot=<n> id=<id|->: <reason>
RUN BUDGET id=<id> slot=<n> spent=<n> of=<n> findings=<n>
RUN MALFORMED id=<id> slot=<n> line=<n> dest=failed
RUN DONE id=<id> slot=<n> rc=<n> after=<d> result=<ok|clean|no-result|plan-only|malformed> findings=<n> refusals=<n> notes=<sent>/<read|-> unpublished=<true|false> budget=<spent|n+|->/<n> dest=<done|failed>
RUN VIOLATION id=<id> slot=<n> background=<n> dest=failed: <reason>
RUN KILLED id=<id> slot=<n> after=<d> deadline=<d> findings=<n> unpublished=<true|false> budget=<spent|n+|->/<n> requeued=<true|false> reaped=<1|2>
RUN MORE kind=<task> shown=<n> total=<t> nova-swarm status --pool <dir> --max 0
RUN OK started=<n> done=<n> failed=<n> killed=<n> pending=<n> after=<d>
RUN NOTE <the one remedy line>
RUN REFUSED: <reason>
STATUS TASK id=<id> state=<pending|running|done|failed> slot=<n|-> for=<d|-> tail=<one line>
STATUS OK pending=<n> running=<n> done=<n> failed=<n> slots=<n>/<n> quarantined=<n>
TRIAGE REPORT id=<id> rev=<sha12> job=<name> result=<ok|clean|plan-only> items=<n> red=<n> green=<n> notdone=<n>: <head>
TRIAGE QUARANTINED id=<id> rev=<sha12> line=<n>: not folded; nova-swarm result --pool <dir> --id <id>
TRIAGE SKIPPED id=<id>: changed while read
TRIAGE MORE kind=<report|finding> shown=<n> total=<t> at=<path> --max 0
TRIAGE BATCH batch=<id|-> reports=<n> findings=<n> new=<n> dup=<n> unquoted=<n> clean=<n> plan_only=<n> no_result=<n> malformed=<n> budget=<n> accurate=<n|-> wrong=<n|->
TRIAGE OK reports=<n> template=<n> malformed=<n> skipped=<n> items=<n> red=<n> green=<n> notdone=<n> page=<path>
TRIAGE REFUSED: <reason>
RESULT OK id=<id> rev=<sha12> class=<ok|clean|plan-only|malformed> bytes=<n> from=<path>
RESULT REFUSED: <reason>
VERDICT OK id=<id> who=<name> accurate=<n> wrong=<n>
VERDICT REFUSED: <reason>
COST TASK id=<id> attempt=<n> end=<word> in=<n|-> out=<n|-> cache_write=<n|-> cache_read=<n|-> reasoning=<n|-> usd=<n.nnnn|-> model=<model> repo=<repo|->
COST OK tasks=<n> in=<n> out=<n> cache_write=<n> cache_read=<n> reasoning=<n> dashes=<in>,<out>,<cw>,<cr>,<r> usd=<n.nnnn> window=<stamp>..<stamp>
REQUEUE OK id=<id> from=<old-id> changed=<true>
NOTE OK id=<id> notes=<n>
NOTE REFUSED: <reason>
FINALIZE OK id=<id> usage=<path> existed=<true|false>
FINALIZE REFUSED id=<id>: <reason>
RECLAIM OK id=<id> freed=<bytes> usage=<path>
RECLAIM REFUSED id=<id>: <reason>
STOP OK pool=<dir> running=<n>
```

`RUN POOL` is the first line of every dispatcher run and it says what will run
before anything runs: the provider, the model and the pool. **It never prints
the key, the key file's contents, or the env var's value** — only the variable's
name, where a name is needed at all.

**Every listing is a cap and a count**, per SPEC.md. `run`, `status`, `triage`
and `cost` take `--max <n>`, default 20, `0` for all, one MORE line naming the
remedy. The counts are the truth about the **pool**, never about the output.
A `status` over a 67-task pool printed 67 lines in the prototype; the finding a
reader wanted was one of them.

**`RUN NOTE` is exactly one remedy line.** If anything failed it names
`nova-swarm triage`; if the pool drained it says so; if a worker was killed at
its deadline twice it names `requeue` with a smaller file budget, which is the
remedy that worked in batch 3.

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
a scan of directories. The files
are the ownership, not a cache of it: a dispatcher that starts over a pool with
slot files present adopts, reclaims or quarantines each one before it claims a
task, and a quarantined slot stays out of the map for the whole run. See **the
races**.

**The refresh is one way, and it is a copy rather than a mount or a link.** A
worker that could write back into the home copy could change the next worker's
self, and the next worker would load it without anybody reading the change.

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

**A reaped job runs once more, and only once (rule 7).** The first reap keeps
the partial `RESULT.md`, moves the task's files to `failed/`, and queues the
same task text again with `requeued=1` and `from=<old-id>` in the new sidecar.
`RUN KILLED … requeued=true reaped=1` says so. A second reap of the re-queued
task is `reaped=2`, `requeued=false`, and the job stays in `failed/`; the
remedy line names `requeue` with a smaller budget, which is a person's act.
One automatic retry closes the case where a worker was silent because the
provider was, and never the case where the task was too big, which a second
identical run would only prove twice.

**The worker is told its deadline in its own prompt**, in seconds, with the
sentence that it will be killed by machinery — because a worker that knows it
has twenty minutes writes findings as it goes, and a worker that does not writes
them at the end it never reaches.

**The dispatcher's own deadline is `--hours`**, and it is required. At it, the
dispatcher starts nothing new and exits when the running workers finish or are
killed. Glenn, 2026-09-09: **every ask, child or read has a written deadline and
a default action; never wait forever.** A `stop` file in the pool does the same
thing on demand.

**A wait loop never ends by scanning for its own name.** The dispatcher waits on
pids it started, and it never matches a process by its command line: that is how
19 orphaned shells happened (2026-09-10), a loop having found itself.

## The job directory and the sandbox rule

**The job directory is the only place a worker writes.** It is created before
the worker starts, it is named in the prompt, and everything the worker clones,
scratches or reports goes under it. The worker's **cwd is the slot directory**,
not the job directory, because the harness refuses reads outside its working
directory and the worker's self and its clones live under the slot.

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
```

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
zero, because a zero is a measurement and a dash is an absence, and `COST OK`
carries `dashes=` so a total with an absence in it is never read as complete.
`cost` reads `<pool>/usage/` and nothing under `done/`, `failed/` or
`running/`, which is why it answers after `reclaim`.

This exists because **a fleet can burn a session**: one fleet ran about 9M
tokens, and three parallel workflows hit the limit in 20 minutes (Glenn,
2026-09-10). A pool whose cost is invisible is a pool that is discovered to be
expensive by being cut off.

**Rate limits are the dispatcher's business.** A provider's 429 is not a failed
task: the dispatcher holds the slot, waits the interval the provider names (or
its own `--backoff`, default 30 seconds, doubling to a cap of 5 minutes), and
retries the **same** task once. A second 429 on the same task fails it with
`rc=429` in its sidecar, so `triage` can see that a batch's silence was a limit
rather than a set of bad tasks. Three of seven workers in batch 2 came back
with a plan and no findings and nothing in the pool said why; the cause turned
out to be a refusal, not a limit (rule 5), but a 429 is a second road to the
same silence and the sidecar closes both.

## `requeue` — the same task, changed

```
nova-swarm requeue --pool <dir> --task <id> --task-file <file> [--label <text>]
```

`requeue` takes a finished task and queues a **new** task with **new text**,
recording `from=<old-id>` in the new task's sidecar. The text must be supplied;
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
dispatcher's child, and it does not pretend to), frees every `reserved` slot
whose runner is dead with the task back in `pending/`, reclaims every slot
whose whole process group is gone (running `finalize` for the job first, with
`end=unknown` when there is no `exit.json`), and quarantines everything it
cannot decide: a reused pid, a dead leader with a live survivor, an unreadable
file. A quarantined slot is not in the map, is named on `STATUS OK
quarantined=`, and is a person's to clear.
Nothing about a slot is ever inferred from a directory listing or an age. The
dispatcher is not a lifecycle container for its children — a child in its own
process group outlives a SIGKILL of its parent by design, so that a dispatcher
crash does not throw away twenty minutes of a worker's reading — which is
exactly why the ownership must be durable.

**A task file read after it was moved.** The dispatcher reads the task text
**after** the rename into `running/`, from the path it renamed to, never from the
pending path it no longer owns.

## What it deliberately does not do

- **It does not loop a worker.** One task per worker, always. The loop, if any,
  is the pool. A harness that cannot hold a polling loop will not be made to by
  this tool (2026-09-10: the Freddy loop is ruled out; the swarm is the shape).
- **It does not read a `RESULT.md` and act on it.** It counts, quotes and pages.
- **It does not write to the board, the bus, or any repository.** Workers write
  inside their job directories; announcing and filing are other tools' jobs.
- **It does not judge a finding.** `red`, `green` and `not done` are the
  worker's own words, counted.
- **It does not choose a model.** The worker description does, and it is
  required.
- **It does not retry a failed task.** `requeue` with changed text is a person's
  decision. The one exception is rule 7: a job reaped at its deadline is
  re-queued once by the machinery, because a silent provider and a silent
  worker look the same from outside, and once is enough to tell them apart.
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
   in `failed/`; the dispatcher exits at `--hours` with the injected clock.
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
    is gone; **finalize → reclaim → first triage**: a job finalized and
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
    reports only `tokens_in` prints `budget=<n>+/<n>`.
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
    quarantined; the tripwire finds no `pgrep`, no `ps` and no match on a
    command line; a second `run` while the first is alive is still exit 2
    naming the holder; a slot whose supervisor is dead with no `exit.json`
    is finalized `end=unknown` into `failed/`, its published report copied,
    and never counted `ok` or `clean`.
18. `TestTheLaunchIsATransaction`: with an injected kill point at each
    boundary of rule 18 — after reserve, after spawn, after identify, after
    the handshake, after release, and between the harness exit and
    `exit.json` — the next `run` on the pool decides every slot with no
    guess: after reserve, `RUN RECLAIM … end=unlaunched` and the task is
    pending again; after spawn but before identify, the supervisor identifies
    itself anyway and the next run adopts it by the stamp it wrote; after
    identify and after release, adopt, with one `RUN DONE` from `exit.json`;
    between exit and `exit.json`, `end=unknown` into `failed/`; a fake
    supervisor that never writes its identity is killed at
    `--launch-timeout`, `RUN LAUNCH-FAILED` with the reason, the task in
    `failed/` with `launch=failed`, the slot free and no process of its
    group alive; with `--workers 2`, a reserved slot counts as held and a
    third job is never started; the slot file's pid, pgid and start stamp
    were written by the process they name (the fake supervisor records its
    own values and the test compares); `supervise` typed by hand with no live
    `run` is exit 2; the tripwire on every path opened for writing finds
    `slots/<n>.json` written by the runner once (reserved) and by the
    supervisor once (launched), never by both for the same state.

## The work list

To build it in Go under `cmd/nova-swarm`, the way `cmd/nova-bus` is built:
standard library only, no hardcoded paths, no default paths, the exit grammar
above, `internal/oneline` for every printed value, `internal/bounded` for every
listing, and `ONBOARDING.md`'s first-day standard — a usage banner ending in a
runnable `example:` block, refusals that say what the flag wants and report every
independent problem at once, a `### First run` in `README.md`, a `quickstart`
verb, and tests that pin all three by executing them.

1. **`internal/swarm/pool.go`** — the pool directory: `pending/`, `running/`,
   `done/`, `failed/`, `reports/` (pages `<UTC>.md` and one `<job>/`
   directory per finalized job), `scratch/`, `slots/`, `usage/`, the task id
   scheme (UTC stamp, label, random half, so two adds in one second cannot
   collide — `nova-bus`'s id lesson), the sidecar file with `files`,
   `requeued`, `reaped`, `from`, `batch`, `tokens`, `launch`, `malformed`
   and the verdict, the atomic claim by rename, the kernel lock on
   `run.lock`, the one automatic re-queue of a reaped job, and `finalize`:
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
   17). Tests: `n` workers against `n-1` slots never double-hold; a
   surviving child's slot is never reallocated; a reused pid is quarantined,
   not adopted; demanded tests 17 and 18.
3a. **`internal/swarm/supervise.go`** — the supervisor: identify itself in
   the slot and pid files, spawn the harness in its group, hold the deadline
   and the budget sampling (rule 13), write `<job>/exit.json` as the
   completion evidence, refuse a hand-typed invocation. Tests: the identity
   in the files is the supervisor's own; `exit.json` is written through
   `.tmp` and rename after the harness exits and before the supervisor
   exits; demanded tests 13 and 18.
4. **`internal/swarm/worker.go`** — the worker description (strict decode: an
   unknown field is a refusal), the slot refresh (one way, copy), the harness
   config written with the variable's **name**, the prompt assembly, the
   harness log read for refusal lines after the run. Tests: the generated
   config contains the variable name and not the value; the prompt contains
   the deadline, the job directory, the file budget, the read-or-write sandbox
   sentence, the append-as-found sentence and the no-bus sentence; demanded
   tests 4, 5 and 6.
5. **`internal/swarm/deadline.go`** — one watcher over every child: terminate,
   wait, kill, report `survived`. Tests: a child that ignores terminate is
   killed; the report says so; the watcher never matches a process by its command
   line.
6. **`internal/swarm/templates.go`** — the three task templates and the
   `RESULT.md` template, as embedded text, each with its conditions and the
   number that produced it. Tests: `template --name` prints each; `add
   --template` wraps a task and the result contains every condition.
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
