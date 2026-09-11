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
| a result file was rewritten while triage was reading it | triage reads by **mtime watermark** and copies before parsing |
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
8. **Results are counted per batch, on one line.** `triage` classifies every
   report as `ok`, `no-result` or `plan-only`, and prints one `TRIAGE BATCH`
   line: `findings=<n> new=<n> dup=<n> unquoted=<n> plan_only=<n>
   no_result=<n> accurate=<n|-> wrong=<n|->`. `plan-only` is a report with no
   finding lines, and the tool detects it. `accurate` and `wrong` are a
   reader's verdicts recorded by `nova-swarm verdict`; a dash means nobody has
   recorded one, and a dash is never a zero.
9. **N workers are N processes.** Each worker has its own job directory and its
   own report file. No file is written by two workers. The coordinator merges
   the reports once, at the end: scatter, then merge, and the merge is the
   only serial step.

## The verbs

```
nova-swarm add      --pool <dir> --task <file>|--stdin --files <n> [--label <text>] [--template <name>] [--deadline <duration>]
nova-swarm run      --pool <dir> --workers <n> --hours <h> --worker <file> [--max <n>]
nova-swarm status   --pool <dir> [--max <n>]
nova-swarm stop     --pool <dir>
nova-swarm requeue  --pool <dir> --task <id> --task-file <file>|--stdin [--label <text>]
nova-swarm verdict  --pool <dir> --task <id> --who <name> --accurate <n> --wrong <n>
nova-swarm triage   --pool <dir> [--dir <dir>]... [--since <stamp>] [--all] [--no-state] [--max <n>]
nova-swarm template --name <read-pr|probe-row|fix-card>
nova-swarm cost     --pool <dir> [--since <stamp>] [--max <n>]
```

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

`status`, `triage`, `template` and `cost` **report** and exit 0. `run` is the
verb that acts.

## Exit codes

| code | meaning |
|------|---------|
| 0 | the verb ran and passed: a task queued, a pool drained, a page written |
| 1 | the verb ran and said **NO**: a dispatcher that exited with tasks still pending and nothing running, a `requeue` of an id that is not in the pool, a `triage` over a directory that holds no reports when one was named |
| 2 | could not run: missing flag, unreadable pool, unreadable worker description, a key file that is absent or empty, `--workers` above the cap, bad invocation |

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
ADD OK id=<id> label=<label> template=<name|-> deadline=<d> pending=<n>
ADD REFUSED: <reason>
RUN POOL workers=<n> hours=<h> worker=<name> model=<model> pool=<dir>
RUN START id=<id> slot=<n> pid=<n> deadline=<d> job=<path>
RUN DONE id=<id> slot=<n> rc=<n> after=<d> result=<ok|no-result|plan-only> findings=<n> refusals=<n> dest=<done|failed>
RUN KILLED id=<id> slot=<n> after=<d> deadline=<d> findings=<n> requeued=<true|false> reaped=<1|2>
RUN MORE kind=<task> shown=<n> total=<t> nova-swarm status --pool <dir> --max 0
RUN OK started=<n> done=<n> failed=<n> killed=<n> pending=<n> after=<d>
RUN NOTE <the one remedy line>
RUN REFUSED: <reason>
STATUS TASK id=<id> state=<pending|running|done|failed> slot=<n|-> for=<d|-> tail=<one line>
STATUS OK pending=<n> running=<n> done=<n> failed=<n> slots=<n>/<n>
TRIAGE REPORT at=<stamp> job=<name> items=<n> red=<n> green=<n> notdone=<n>: <head>
TRIAGE DEGRADED at=<stamp> job=<name> lines=<n>: <first heading>
TRIAGE MORE kind=<report> shown=<n> total=<t> --max 0
TRIAGE BATCH reports=<n> findings=<n> new=<n> dup=<n> unquoted=<n> plan_only=<n> no_result=<n> accurate=<n|-> wrong=<n|->
TRIAGE OK reports=<n> template=<n> degraded=<n> items=<n> red=<n> green=<n> notdone=<n> page=<path>
TRIAGE REFUSED: <reason>
VERDICT OK id=<id> who=<name> accurate=<n> wrong=<n>
VERDICT REFUSED: <reason>
COST TASK id=<id> in=<n> out=<n> usd=<n.nnnn> model=<model>
COST OK tasks=<n> in=<n> out=<n> usd=<n.nnnn> window=<stamp>..<stamp>
REQUEUE OK id=<id> from=<old-id> changed=<true>
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
rule beside them; `plan_only` and `no_result` are reports. `accurate` and
`wrong` sum the recorded verdicts, and print a dash when no verdict exists for
any report in the batch. The line never grows with the batch.

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
- its **own data home**, `<slot-dir>/data`, exported so the harness keeps its
  own database there;
- its **own job directory**, `<slot-dir>/jobs/<label>`.

**The data home is the whole reason slots exist.** The harness keeps one SQLite
database per data home, and concurrent runs against one database lock each other
out: measured **2026-09-10** with six workers, `database is locked`, five of the
six doing nothing while the pool reported six running. A pool that reports six
running and does one task's work is worse than a pool of one, because the number
is believed.

**A slot is held by exactly one worker and released only when that worker's
process is reaped.** The dispatcher keeps the slot → task map and the task →
pid map; a slot is freed in the same step that reaps the pid, never on a timer
and never by a scan of directories. See **the races**.

**The refresh is one way, and it is a copy rather than a mount or a link.** A
worker that could write back into the home copy could change the next worker's
self, and the next worker would load it without anybody reading the change.

## The deadline, held by the machinery

Every task carries a deadline. The default is the worker description's, and
`add --deadline` overrides it per task.

**The machinery holds it, not the worker.** The dispatcher starts the worker as
a child, watches it, and at the deadline sends a terminate, waits, then a kill.
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
and stop.

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
   the top, before the work; the findings are the work.
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

**`state` is one of exactly three words**: `red`, `green`, `not done`. A
fourth word is a report a coordinator has to interpret, and a coordinator reading
forty reports interprets nothing.

**A report that does not follow the template degrades rather than failing.** It
is counted as `TRIAGE DEGRADED` with its first heading and its line count, and it
is still quoted into the page. A triage that refused a malformed report would
lose the one finding the worker got out before it died.

## `triage` — one page

`triage` walks the job directories, takes every `RESULT.md` **newer than the
watermark**, prints one line each, and writes **one page**:

```
<pool>/reports/<UTC>.md
```

The page carries, per report: its heading, its path, its mtime, its **Per item**
table and its **Left owed** list, in mtime order, oldest first. The terminal
gets one line per report and then the counts — `reports`, `template`,
`degraded`, `items`, `red`, `green`, `notdone` — and the page's path. That is
the whole of the terminal output, capped at `--max`, because the page is the
artifact and the terminal is an index to it.

**The watermark is state**, `<pool>/triage.json`, holding `last`, `last_iso`,
`runs` and `last_count`, written atomically. `--all` ignores it, `--since`
overrides it, `--no-state` does not advance it. A triage with no watermark
reprints the night.

**A report is copied before it is parsed.** This closes the third race below: a
worker appending to `RESULT.md` while triage reads it produced a page with half
a table in it. The copy is into the pool's own scratch, it is the copy that is
parsed, and the mtime recorded is the copy's source mtime.

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

`cost` reports, per task and per window: **input tokens, output tokens, and
dollars**, with the model named. The numbers come from the harness's own
accounting, recorded into the task's sidecar file when the worker exits; a task
whose harness reported nothing prints `in=- out=- usd=-` rather than a zero,
because a zero is a measurement and a dash is an absence.

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

The old task's files stay where they are. Nothing is deleted, ever: a pool is a
record.

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
the **only** authority on what is free, a slot is allocated and recorded in one
step before the child is started, and it is freed in the **same step that reaps
the pid**. Never a directory scan, never a lock file in the slot, never a
timer. Pinned by a test that starts `n` instant-exit workers against `n-1` slots
and asserts no slot is ever held twice.

**A worker still running after its deadline.** The dispatcher terminates, waits,
kills. A child that survives the kill (a process group that outlived its leader)
is reported as `RUN KILLED … survived=true` and its **slot is not reused for the
rest of the run**: a slot whose data home may still have a writer in it is not a
free slot. A pool that re-used it would reproduce the lock failure with a
corpse. The dispatcher's own exit does not wait forever on it — `--hours` is a
deadline for the dispatcher too, and a surviving child is named in `RUN OK` so a
person can end it.

**A result file rewritten mid-triage.** Closed by copying before parsing; see
**triage**. The copy also makes the watermark honest: a report appended to after
triage read it has a newer mtime than the watermark and is picked up by the next
triage, which is correct — it changed.

**Two dispatchers on one pool.** Both would move the same pending task into
`running/`. The move is the claim (`rename` is atomic within a directory, and a
dispatcher that loses the rename simply takes the next task), and on top of that
`run` holds a lock on the pool directory for its whole life and a second `run`
exits 2 naming the holder.

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
- **It does not delete anything.** A pool is a record.
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
   exited 0. Here `run` classifies a result as `ok`, `no-result` or `plan-only`
   — the last two move to `failed/` — because **a `RESULT.md` with only a plan is
   a failed task**, and only the tool can see that at the moment it happens.
9. **`triage` parses the live `RESULT.md`.** Here it copies first.
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
8. Seven reports, three of them plan-only, one with a recorded verdict:
   `TRIAGE BATCH` is exactly one line, its counts are the seven reports'
   counts, `accurate` and `wrong` are the verdict's numbers; with no verdict
   both print `-`; a report with no finding lines is `plan_only`.
9. N workers are N child processes, each with its own job directory and its
   own report file; a tripwire on every path a child opens for writing finds
   no path opened by two children; the merge into the page runs once, after
   the last worker is reaped.

## The work list

To build it in Go under `cmd/nova-swarm`, the way `cmd/nova-bus` is built:
standard library only, no hardcoded paths, no default paths, the exit grammar
above, `internal/oneline` for every printed value, `internal/bounded` for every
listing, and `ONBOARDING.md`'s first-day standard — a usage banner ending in a
runnable `example:` block, refusals that say what the flag wants and report every
independent problem at once, a `### First run` in `README.md`, a `quickstart`
verb, and tests that pin all three by executing them.

1. **`internal/swarm/pool.go`** — the pool directory: `pending/`, `running/`,
   `done/`, `failed/`, `reports/`, `scratch/`, the task id scheme (UTC stamp,
   label, random half, so two adds in one second cannot collide — `nova-bus`'s id
   lesson), the sidecar file with `files`, `requeued`, `reaped`, `from` and the
   verdict, the atomic claim by rename, the pool lock, the one automatic
   re-queue of a reaped job. Tests: two claimants, one winner; an id collision
   is impossible by construction; demanded test 7.
2. **`internal/swarm/key.go`** — the key file read as data: first line, the two
   strip rules, whitespace out, empty refused with the creating command. Tests:
   a key never appears in any returned string; a file with a second line is read
   as one line; `export VAR=` and a bare key both work; mode warnings.
3. **`internal/swarm/slot.go`** — the slot map: allocate-and-record in one step,
   free-on-reap in one step, retire a slot whose child survived. Tests: `n`
   workers against `n-1` slots never double-hold; a surviving child's slot is
   never reallocated.
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
   `plan-only` and `no-result` classifications, and the degrade path. Tests: a
   malformed report degrades rather than failing; a plan-only report is
   classified as one; a fourth state word is a parse finding, not a silent
   fifth bucket; demanded tests 1, 2 and 3.
8. **`internal/swarm/triage.go`** — the watermark state, copy-before-parse, the
   page writer, the counts, the one `TRIAGE BATCH` line, the verdict sums with
   the dash for absence. Tests: a report appended to during triage does not
   produce half a table; the watermark advances once per run; `--no-state`
   does not advance it; demanded tests 8 and 9.
9. **`internal/swarm/cost.go`** — the sidecar's token and dollar accounting, the
   window, the absence-is-a-dash rule, and the 429 backoff. Tests: a task with
   no accounting prints dashes; a 429 is retried once and then failed with its
   code.
10. **`cmd/nova-swarm/main.go`** — the verbs, including `verdict`, the flag
    parsing with this repo's one-line refusals, `--files` required and zero
    refused, the output grammar exactly as above, `--max` on every listing.
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
