# nova-swarm — specification

A pool of one-task workers — any provider, any model, through one
harness — each with its own working directory, its own data home, its own job directory
and its own deadline held by the machinery rather than by the worker.

Every rule it keeps is a failure from the record:

| the failure, from the record | the rule that closes it |
|---|---|
| six workers on one data home: `database is locked`, and five of six did nothing (**2026-09-10**) | a **slot**: its own working directory and its own data home per running worker |
| an API key in an argument is in the process table, and in a log, and in a transcript somebody pastes (**the leak that taught it**) | the key is **read as data from a file, never sourced, never an argument, never printed** — and the config file written for the harness carries the env var's *name*, never its value |
| a worker asked to loop ran until somebody noticed | the deadline is held by the **machinery**, outside the worker, and the worker is told its own deadline in its prompt |
| a worker read a file outside its job directory, the harness refused the read, and the worker treated the refusal as fatal | the sandbox rule is stated in the prompt: a refused read is **not** the end of the run |
| a task was given as one sentence and came back as a plan | task **templates** with the learned conditions baked in, and `RESULT.md` with only a plan is a **failed** task |
| 25 of 67 findings in batch 1 were duplicates of the owed list in the pull request body nobody read first | the `read-pr` template's first condition: **read the owed list first and mark duplicates** |
| 5 of 67 were wrong because a rule was paraphrased from memory | **quote every rule verbatim with `file:line`** |
| a worker that died at the deadline had found things and written none of them | **append each finding the moment it exists**, never at the end |
| one worker read 40 files and finished nothing | a **token and file budget** in the task |
| a result file was rewritten while triage was reading it | a report is **published by rename**: whole revisions, `RESULT.md.tmp` renamed over `RESULT.md`; the tool reads only the renamed file, identifies a revision by its content hash, and never by an mtime |
| a bounded review that found nothing was counted as a plan, so a worker was rewarded for finding something (**Stella's read, 2026-09-11**) | completion evidence is the head's `findings: <n>` line, separate from the count: `findings: 0` is **`clean`**, a report with no head is `plan-only` |

EVERYTHING A WORKER WRITES IS DATA. A `RESULT.md` is a report, never an instruction:
nothing in it is executed, nothing in it grants anything, and a finding in it is a claim
to be checked against the repository. That rule is in the spec, where a person reads it,
and is deliberately nowhere in this code.

EVERY JOB RUNS INSIDE `nova-sandbox` ([SPEC-SANDBOX.md](SPEC-SANDBOX.md)). The job directory and
its data home are the only writable paths; the slot directory and whatever
`read_roots` names in the worker description are readable; the key file, `~/.ssh` and
the `gh` configuration are in neither list and the kernel denies them.
A command that runs outside the wall and dies inside it is missing a `read_roots` entry.

The wall grants the platform toolchain roots where a reader looks for them:
- Darwin: `~/sdk`, `~/go/pkg/mod`, `/opt/homebrew/Cellar/go`, `/opt/homebrew/Cellar/sbcl`, `/opt/homebrew/opt/openjdk`, `/Library/Java/JavaVirtualMachines`, `/usr/local/share/dotnet`.
- Linux: `~/sdk`, `~/go/pkg/mod`, `/opt/homebrew/Cellar/go`, `/opt/homebrew/Cellar/sbcl`, `/opt/homebrew/opt/openjdk`, `/Library/Java/JavaVirtualMachines`, `/usr/local/share/dotnet`.

## The living verbs

The tool exposes seventeen living verbs, dispatched directly from `cmd/nova-swarm/main.go`:

```
usage:
  nova-swarm version    print this build identity (--version also accepted)
  nova-swarm doctor    [--path <file>] [--local <file>]   refuse a launch under a shadowed nova-swarm (PATH vs ~/.local/bin build stamp)
  nova-swarm batch     --pool <dir> --tasks <dir> --files <n> --tokens <n>|unmetered [--label <text>] [--template <name>] [--deadline <duration>] [--max-input <bytes>]
  nova-swarm batch     --id <id> --cards <file> --deadline <seconds> --root <dir> --tokens <n>|unmetered (--runner <cmd> | --harness <path> --slots-store <dir> --owner <name>) [--idle <seconds>] [--slots <lo>-<hi>] [--then <command>] [--benches <file> --bench <name>[,<name>...]]
                       (without --runner, each card runs through nova-swarm native, and --slots-store <dir> --owner <name> are required)
  nova-swarm status    --pool <dir> [--slots-store <dir> --owner <name>] [--max <n>]
  nova-swarm stop      --pool <dir>
  nova-swarm triage    --pool <dir> [--batch <id>] [--since <stamp>] [--all] [--no-state] [--max <n>] [--owed <file>] [--usage <file>] [--decide [--floor <f>] [--key-env <var>] [--base-url <url>]]
  nova-swarm result    --pool <dir> --id <job>
  nova-swarm verify    --result <file> --contract <line> --label <text> [--card <file>] [--max <n>] [--run-record <file>] [--usage <file>]
  nova-swarm lint      --card <file> [--typed] [--trust <file>] [--lineup <file>] [--max <n>] | --fleet <file> [--max <n>] | --rules
                       (--fleet lints a launcher script against the coordinator's /bin/bash 3.2: shebang, bash-4 builtins, unquoted expansions)
  nova-swarm template  --name read-pr|probe-row|fix-card|result|worker|setup|capacity|read|fix|text|replay|drift|tone|models.tsv
  nova-swarm finalize  --pool <dir> --task <id>
  nova-swarm quickstart --pool <dir>
  nova-swarm profile   --jobs <glob>   (one PROFILE line per job's timeline.tsv and one mean summary)
  nova-swarm native    --harness <path> --model <provider/model> --card <file> --slot <dir> --root <dir> --deadline <duration> --tokens <n>|unmetered [--label <text>] [--idle <duration>] [--auth <file>] [--config <file>] [--worker <file>] [--results-root <dir>] [--sweep-now] [--events-store <host:port>]
  nova-swarm route     --card <file> --routes <routes.tsv> [--floor 0.9] [--default <worker json>] [--key-env <name>] [--base-url <url>]
  nova-swarm slots init --store <dir> --owner <name> --capacity <n> --share <n>
  nova-swarm slots take --store <dir> --owner <o> --n <k> --for <duration> [--label <text>] [--kind <kind>]
  nova-swarm slots release --store <dir> --owner <o> (--label <text> | --all) [--force]
                       (a lease whose holder is still RUNNING is KEPT: SLOTS KEPT, live=<n>, exit 2.
                        --force frees it anyway and can oversubscribe the bench: an operator's act,
                        never a card's and never a manager's default)
  nova-swarm slots list --store <dir>
  nova-swarm worker    check <description.json> [--env] [--max <n>]
```

### Verb behaviours

1. **`version`**: Prints build stamp (`VERSION <stamp>`). Accepts `--version`.
2. **`doctor`**: Compares `PATH` binary stamp against local build stamp (`~/.local/bin/nova-swarm`). Refuses launch if shadowed.
3. **`batch`**:
   - In directory mode (`--pool --tasks ...`): queues one task per regular file under `--tasks`.
   - In cards mode (`--id --cards ...`): executes a batch of cards across slots, enforcing token budgets and deadlines. Without `--runner`, runs through `native` requiring `--slots-store` and `--owner`.
4. **`status`**: Reports tasks, states, slot holdings, and quarantine counts.
5. **`stop`**: Writes `<pool>/stop` to halt new task admissions while letting running workers complete.
6. **`triage`**: Reads published reports, checks revision hashes, folds findings into a single Markdown summary page (`<pool>/reports/<stamp>.md`), and prints one bounded summary line.
7. **`result`**: Emits `RESULT OK` and prints the published `RESULT.md` verbatim for a specified job id.
8. **`verify`**: Mechanically verifies `RESULT.md` line 1 against the contract line, checks against failure signatures, and writes a `.receipt` file.
9. **`lint`**: Validates card mechanical structure before any spend, validates fleet scripts against bash 3.2, or displays linting rules.
10. **`template`**: Prints standard templates (`read-pr`, `probe-row`, `fix-card`, `worker`, etc.) verbatim without escaping.
11. **`finalize`**: Durable usage writer for an ended job whose runner died before finalizing.
12. **`quickstart`**: Creates pool directory structure (`tasks/`, `running/`, `done/`, `failed/`) and outputs onboarding hints.
13. **`profile`**: Aggregates per-turn timeline TSV files into execution phase durations.
14. **`native`**: Executes a single card through the harness under sandbox containment with external deadline, idle timer, and token tracking. Stated once: `native takes no file lease since #3877` (and native takes no bench slot lease since #3877).
15. **`route`**: Classifies card complexity and kind against a routes table to select an appropriate worker description.
16. **`slots`**: Bench slot lease broker (`init`, `take`, `release`, `list`) managing shared bench capacity.
17. **`worker`**: Validates worker description JSON structure, environment variables, and readable roots.

## The pool verbs on an empty pool

The six pool verbs (`status`, `stop`, `triage`, `result`, `finalize`, `quickstart`) read a pool directory. Nothing fills the pool since the dispatcher was removed; each behaves deterministically when pointed at an empty pool directory:

- **`status`**: Scans the pool states (`running`, `pending`, `done`, `failed`) and reports zero for all counts:
  `STATUS OK pending=0 running=0 done=0 failed=0 slots=0/0 quarantined=0` (exit code 0).
- **`stop`**: Writes `<pool>/stop` marker file and reports zero active running tasks:
  `STOP OK pool=<dir> running=0` (exit code 0).
- **`triage`**: Generates an empty reports page (`<pool>/reports/<stamp>.md`), reports zero folded items, and exits cleanly:
  `TRIAGE BATCH batch=- reports=0 findings=0 new=0 dup=0 unquoted=0 clean=0 plan_only=0 no_result=0 malformed=0 budget=0 accurate=- wrong=-`
  `TRIAGE OK folded=0 template=0 malformed=0 skipped=0 items=0 red=0 green=0 notdone=0 page=<path>` (exit code 0).
- **`result`**: Looks for `<id>` in `tasks/`, `done/`, or `failed/`. When the pool is empty, the task cannot exist, and it refuses:
  `RESULT REFUSED: no task <id> in <pool>; nova-swarm status --pool <pool> lists what is here` (exit code 1).
- **`finalize`**: Looks for `<pool>/tasks/<id>` to finalize usage. When the pool is empty, the task is absent, and it refuses:
  `FINALIZE REFUSED id=<id>: no such task in <pool>` (exit code 1).
- **`quickstart`**: Idempotently creates the pool directory and standard subdirectories (`tasks/`, `running/`, `done/`, `failed/`), verifies that the pending queue is empty, and prints next-step hints:
  `QUICKSTART OK pool=<dir> pending=0 next=add,run,triage`
  followed by `QUICKSTART NOTE` guidance lines (exit code 0).

## Exit codes

| code | meaning |
|---|---|
| 0 | the verb ran and passed: a batch queued, a page written, a report printed |
| 1 | the verb ran and said **NO**: a `finalize` of a job whose process group is alive, a `triage --batch` of an id no sidecar carries, a `result --id` of an id not in the pool or with no published report, a `verify` whose contract line mismatched or whose run carries a failure signature, a `native` whose card was ended by its token budget or by a budget it could no longer verify |
| 2 | could not run: missing flag (`--tokens` on `native` and on `batch --cards`), a numeric `--tokens` on a `native` whose usage source is `none` or whose bench has no `sqlite3` on `PATH`, unreadable pool, unreadable worker description, a key file that is absent or empty, bad invocation |

## Output grammar

```
RUN POOL workers=<n> hours=<h> worker=<name> model=<model> auto_retry=<true|false> pool=<dir>
RUN START id=<id> slot=<n> pid=<n> pgid=<n> started=<stamp> deadline=<d> tokens=<n> job=<path> [profile=<id> model_requested=<id> model_observed=<id>]
RUN LAUNCH-FAILED id=<id> slot=<n> after=<d>: <reason>
RUN ADOPT id=<id> slot=<n> pid=<n> started=<stamp> remaining=<d>
RUN RECLAIM slot=<n> id=<id> end=<done|killed|failed|budget|budget-unverifiable|violation|input-limit|provider|wall|unknown|unlaunched> dest=<done|failed|-> usage=<path|-> requeued=<true|false> [profile=<id> model_requested=<id> model_observed=<id>]
RUN WAIT slots owner=<owner> holders=<owner:count,...>
RUN ROUTED-OUT id=<id> dest=<routed-out> rung=<name> why=<rung-is-asked-not-run>
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
RUN REFUSED: <reason>
RUN REFUSED reason=<sandbox_probe|no_sandbox>: <reason>
BATCH OK id=<id> tasks=<n> pending=<n>
BATCH REFUSED: <reason>
BATCH <id> n=<n> done=<n> abstain=<n> in=<n> out=<n> usd=<sum> idle=<n> stalled=<n> [partial=<n>] [benches=<n>] [uniform-abstain=<reason>]
BATCH THEN rc=<n>
BATCH THEN SKIPPED done=<d> n=<n> abstain=<a> stalled=<s> stopped=<b>
BATCH NOTE slot=<n> stale-lock id=<id> taken
BATCH NOTE <label> RESULT.md copied up from <path>
NATIVE OK label=<id> job=<id> tmp=<path> rc=<n> wall=<n>s sandbox=<path|-> card_sha256=<sha> binary_sha256=<sha> config=<sha8|-> harness=<ok|silent> budget=<spent|n+|->/<n>|unmetered [fence=rejected path=<p>] [usage=none reason=<r> path=<p>] [reason=terminated] [stopped=<tokens|max_turns|max_cache_read|unverifiable>]
NATIVE INCOMPLETE label=<id> job=<id> tmp=<path> rc=<n> wall=<n>s sandbox=<path|-> card_sha256=<sha> binary_sha256=<sha> config=<sha8|-> harness=<ok|silent> budget=<spent|n+|->/<n>|unmetered [fence=rejected path=<p>] [usage=none reason=<r> path=<p>] [reason=terminated] [stopped=<tokens|max_turns|max_cache_read|unverifiable>] why=<harness-silent|no-result|rc>
NATIVE REFUSED: <reason>
STATUS TASK id=<id> state=<pending|running|done|failed> slot=<n|-> for=<d|-> tail=<one line>
STATUS OK pending=<n> running=<n> done=<n> failed=<n> slots=<n>/<n> quarantined=<n>
STATUS MORE kind=<task> shown=<n> total=<t> nova-swarm status --pool <dir> --max 0
STATUS SLOTS owner=<owner> held=<n> share=<n>
STOP OK pool=<dir> running=<n>
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
FINALIZE OK id=<id> usage=<path> existed=<true|false>
FINALIZE REFUSED id=<id>: <reason>
QUICKSTART OK pool=<dir> pending=<n> next=add,run,triage
QUICKSTART NOTE <one remedy line>
```

Every listing is a cap and a count: `--max`, default 20, `0` for all, one MORE line
naming the remedy. The counts are the truth about the pool, never about the output.

## The key, read as data

The key is read as data from a file or environment variable, never sourced, never an argument, and never printed.
It lives in one file the worker description names — mode `0600` — and is never logged.
The harness configuration written by the machinery carries the variable's NAME, never its value.

## Bench slot leases

(Glenn and Stella, 2026-09-17.) A bench is bigger than its owners: one bench,
many owners, and the slots on it are one shared pool, not one pool per owner.
A bench carries ONE slot store shared by every owner, at <bench store>/slots,
and every launcher takes a lease per card before it runs and releases it after.
The seven rules:

1. A bench carries ONE slot store shared by every owner, at <bench store>/slots,
   a directory of atomic mkdir leases each holding owner, pid, card label, until=.
2. Every launcher (a batch runner, a hand launch; native takes no file lease since #3877)
   takes a lease per card before it runs and releases it after; a launch
   without a lease is refused by the launcher.
3. The broker verbs are the only way to hold a slot:

   ```
   nova-swarm slots init --store <dir> --owner <name> --capacity <n> --share <n>
   nova-swarm slots take --store <dir> --owner <o> --n <k> --for <duration> [--kind <kind>]
   nova-swarm slots release --store <dir> --owner <o> (--label <text> | --all) [--force]
   nova-swarm slots list --store <dir>
   ```

   `init` makes a store: the directory, its `slots/` and one `shares.tsv` with the
   given capacity, a reserve of 0 and one owner's share. It creates and never updates.

   `take` grants by the owner's share from the registry file `<store>/shares.tsv`
   (columns bench, owner, share). It refuses with the holder list when the share is spent,
   and never grants past capacity minus reserve.
   Card kinds carry a weight charged at take, before any child starts: a schema or
   fix-red card weighs 4 because it spawns build chains; a read card weighs 1.
   A schema card is refused at take when the remaining share fits only a read.
4. `nova-swarm slots list --store <dir>` prints who holds what, one line per lease.
5. Reaping: a lease past until= whose pid is gone is reaped by the next take;
   drift: a pid alive past until= is DRIFT, printed by name, never reaped and never regranted.
6. In the survey, a card found running under a root with no matching lease is DRIFT.
7. Registry: shares change only by a PR to the registry, never by a note.

Red tests (each seen red before it is trusted):

- two owners at their shares cannot exceed capacity;
- an expired lease with a dead pid frees its slot;
- an expired lease with a live pid is DRIFT and stays;
- a launch without a lease is refused by the launcher;
- a schema card is refused at take when the remaining share fits only a read;
- a live-until lease whose pid is gone is stranded with its label.

A bench holds slot leases: the store is `<store>/slots` with one directory per lease made by `os.Mkdir` (atomic), each holding a file `lease` with lines `owner=`, `pid=`, `label=`, `until=<RFC3339>`, beside `<store>/shares.tsv` rows `capacity\t<n>`, `reserve\t<n>`, `<owner>\t<n>`. `nova-swarm slots take --store <dir> --owner <o> --n <k> --for <duration>` first reaps every lease whose `until=` is past AND whose pid is not alive — a lease past `until=` with a live pid is `DRIFT`, stays, and counts as held — then grants `k` leases iff the owner's held+demand stays within its share and the total held+demand stays within `capacity` minus `reserve`.

`nova-swarm slots list --store <dir>` prints one line per lease.

`native takes no bench slot lease since #3877`.

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
    the sum of the steps' inputs and the sum of nothing else.

P2. **The harness runs the tools, with no model call.** In the pipeline, the
    harness runs the tools, with no model call: clone, checkout, test run,
    commit and the RESULT copy are the machinery's, never a model turn, and the
    harness log shows each call's input size so the cost is a fact.

P3. **No memory between calls.** A step never sees a transcript, a prior turn or
    a running agent; it sees the named inputs and nothing else.

P4. **`MODE: explore` is the one place the loop stays.** A read that must find
    where a rule lives may say `MODE: explore` on its own line; only then is the
    agentic loop admitted, because a search the card cannot name in advance is
    the one step whose next input is not known when the card is written. A card
    that does not say it is a pipeline.

P5. **A fourth call without `MODE: explore` is refused, with the remedy line.**
    A pipeline card names at most three model calls. A card that asks for a
    fourth without the mode word is refused at admission, before any worker
    starts, and the refusal names the keyword, `MODE: explore`, and the rule it
    serves, because a refusal a caller cannot act on is a refusal wasted.

P6. **A `MODE: explore` card carries a turn budget the harness enforces.** The
    card names its budget on a `TURNS: <n>` line; the harness stops the card at
    that turn count and the partial RESULT names the budget, so an explore read
    that wanders ends on a number the card chose rather than on the deadline it
    was given.

P7. **The fix-card shape is three calls, not thirty turns.** Step 1 (model):
    inputs are the issue text plus the named test file and the named source
    file; output is the red test as a patch. The harness applies it, runs the
    test and captures the failing lines. Step 2 (model): inputs are the failing
    lines and the source file; output is the fix as a patch. The harness applies
    it and runs the package tests. Step 3 (model, tiny): inputs are the two
    patches' stat and the test tail; output is the RESULT lines. The harness
    commits and writes `RESULT.md`. Three calls of 10-60k tokens each instead of
    30 turns x 66k.

**Red tests for this section.** One line per rule, seen red first:

- P1, P2, P3, P7: `TestTheFixCardRunsInThreeModelCalls` -- the fixture fix card
  runs in exactly three model calls and the harness log shows their input sizes;
  the fix-card shape is three calls, not thirty turns, and no memory between
  calls and the harness runs the tools, with no model call are the assertions
  the fixture makes on the log.
- P4, P5: `TestAdmissionRefusesAFourthCallWithoutExplore` -- a fourth call without `MODE: explore` is refused, with the remedy line, and the same card with `MODE: explore` is admitted.
- P6: `TestExploreOverTurnBudgetIsStoppedWithTheBudgetNamed` -- a `MODE: explore` card carries a turn budget the harness enforces: over its turn budget it is stopped and the partial RESULT names the budget.

## Exact-tip bench prewarm (#2498 S3)

`nova-swarm bench prewarm --root <dir> --source <checkout> --repo <owner/name> --tip <full-sha>` prepares the inputs a new job would otherwise compile cold.
The source is an existing local checkout that already holds the full 40-character
commit; the verb never fetches, guesses a branch or contacts a fleet host. It makes
`<root>/ref/<owner>/<name>@<tip>` as the reference checkout already consumed by
staging and runs four phases against that exact detached tree: modules, ordinary builds, compiled Go test binaries and ASDF FASLs. The phases use the same
`<root>/cache/go-mod`, `<root>/cache/go-build` and
`<root>/cache/common-lisp/<tip>` seed. Before a card starts, that exact-tip seed
is copied into a private overlay under the card's job directory.

A new reference checkout stays under an owned temporary directory and is hidden until all four phases succeed. A failed phase names itself and publishes neither
the reference checkout nor `<root>/prewarm/<owner>/<name>@<tip>.receipt`.

The success line and durable receipt are `PREWARM OK repo=<owner/name> tip=<full-sha> phases=modules,build,test-binaries,lisp`. A PREWARM receipt proves preparation, not fleet adoption. S3 is adopted only when this command has run at
the current tip on every intended bench and `make test` in a fresh job on each adopted bench finishes in under 60 seconds.

## The task templates

### `read-pr`

```
1. READ THE PR BODY'S OWED LIST FIRST, before reading any code, and for every
   finding you report, say whether it is already on that list. A finding that
   is already owed is marked `dup:` and is not a new finding.
2. QUOTE EVERY RULE VERBATIM, with `file:line`. Never paraphrase a rule from
   memory, and never assert a rule you did not open.
3. APPEND EACH FINDING TO RESULT.md THE MOMENT IT EXISTS. Not at the end.
4. A FILE BUDGET: read at most <n> files. When the budget is spent, write what you have and stop.
5. A RESULT.md CONTAINING ONLY A PLAN IS A FAILED TASK.
```

BOUND THE REPORT (issue #74): findings only. No narration of the clone, no
restated task, no praise, no summary. One line per finding: `file:line`, the
rule in twelve words, the severity, and the fix in one clause. Keep RESULT.md
under 40 lines and every line under 300 characters, and no pipe inside backticks:
a `|` in a quote broke the table grammar twice (D12), so quote the rule without
it. Put the verdict line last. When there is nothing to report, write `findings: 0`.

### `probe-row`

A bounded probe asserting or denying one concrete claim about code or configuration.

### `fix-card`

Carries three steps: write the red test, implement the fix, and report the verdict.

## The `RESULT.md` template and failure signatures

A `RESULT.md` report is emitted with line 1 matching the task's contract line, followed by disposition and evidence.

Known mechanical failure signatures are classified deterministically by `verify`:

| signature | class | remedy |
|---|---|---|
| `toolchain not available` | `toolchain` | `pin the Go toolchain in go.mod, or install it` |
| `no packages to test` | `packages` | `name the packages to test; an empty list proves nothing` |
| `auto-rejecting` | `fence` | `the harness fence auto-rejected a path; re-run walled` |
| `permission denied` | `permission` | `the sandbox refused a read or write; keep the work inside the job directory` |
| `command not found: go` | `toolchain` | `install Go and put it on PATH before the run` |
| `cannot find package` | `packages` | `the package path is wrong, or its module is not in go.mod` |

## Typed records

The typed records contract governs machine-verified evidence:

<!-- typedrec:begin -->
| field | type | fix | recut | port | docs-guard | report | read |
|---|---|---|---|---|---|---|---|
| line 1 | card line 1 verbatim (else contradictory) | R | R | R | R | R | R |
| line 2 | `DONE` \| `ABSTAIN <why>` \| `BLOCKED <why>`; why is 1-512 B | R | R | R | R | R | R |
| SCHEMA | literal `v2` | R | R | R | R | R | R |
| KIND | enum of the 6; must equal the card's KIND | R | R | R | R | R | R |
| ATTEMPT | int 1-99; must equal the card's attempt | R | R | R | R | R | R |
| CHECK | `pass` \| `fail` \| `not-run` | R | R | R | R | R | R |
| REPO | `owner/name`, `^[a-z0-9-]+/[a-z0-9._-]+$`; must equal the card's repo | R | R | R | R | R | R |
| BRANCH | git ref (check-ref-format), ≤200 B; must equal the card's branch when it has one | D | D | D | D | O | - |
| PATHS | 1-256 space-separated repo-relative paths; no `..`, no leading `/`, no duplicates | D | D | D | D | O | - |
| RED | text 1-4096 B | D | D | D | - | - | - |
| GREEN | text 1-4096 B | P | P | P | - | - | - |
| PRIOR | `#<int> @<hex12>` | - | D | - | - | - | - |
| PR | int | - | - | - | - | - | D |
| HEAD | hex40; must equal the card's `pr_head` | - | - | - | - | - | D |
| FINDINGS | int 0-999; must equal the number of `## Findings` rows | - | - | - | - | - | D |
| FLOOR | `HIGH` \| `MEDIUM` \| `LOW` \| `NONE`; `NONE` iff FINDINGS=0 | - | - | - | - | - | D |
| SUGGEST | `APPROVE` \| `HOLD`: a suggestion, never a disposition | - | - | - | - | - | D |
| PROBES | int ≥1; must equal the number of `## Probes` rows | - | - | - | - | D | - |
| sections | required `## ` headings, each with at least one row | Gates, Left owed | Gates, Left owed | Gates, Left owed | Verification, Gates | Probes, Summary | Findings (rows = FINDINGS, the one zero-row case) |

Key: R = required. D = required when the status is DONE (and the `sections` row applies only on DONE). P = required when DONE and CHECK=pass, optional otherwise. O = optional, and type-checked when present. `-` = unknown for this kind, so the file is refused. On ABSTAIN or BLOCKED only the R rows are required; any other field present is still type-checked. A DONE with CHECK=fail is a valid Returned attempt that stays unverified.

### Evidence rows

This one grammar covers every section in every kind.
- **Lines.** The evidence region is split on `\n`.
- **Fences.** A fence is a line that starts with three backticks, and each one toggles the fenced state. Fence lines and every line inside a fence are neither rows nor headings.
- **Headings.** A heading is any line starting `## ` outside a fence.
- **Sections.** A section is the lines after its heading, up to the next heading or the end of the file. A heading is a contract section only when the whole line is exactly `## <Name>`, byte for byte. So `## Findings` counts, while `## findings`, `##Findings`, `## Findings:` and `## Findings ` (trailing space) do not.
- **Other headings.** Any other heading, such as `## Notes`, is evidence. It ends the section above it and is otherwise ignored. A `### ` line does not start with `## `, so it neither ends a section nor counts as a row.
- **Rows.** A row is a section line that starts at column 0 with `- ` (hyphen, space) and then has at least one byte that is not a space or tab. Nothing else is a row: blank lines, prose, indented lines (nested bullets, continuations), `* ` and `+ ` bullets, numbered items, table lines, `### ` subheadings, `-x` and a bare `- `. They all stay in the file as evidence and are never counted.
- **General rule.** Every section named in the kind's `sections` cell must be present exactly once and must have at least one row. There is exactly one exception. On kind=read, `## Findings` must have exactly FINDINGS rows, so FINDINGS=0 means the heading is present with zero rows. Prose such as "none" is allowed there, and any row is `contradictory`. FINDINGS and PROBES each equal the row count of their section.
- **Section defects,** each named by field:
  - A heading that is absent gives `field=## <Name> defect=missing line=0`.
  - A heading with zero rows, outside the exception, gives `field=## <Name> defect=missing line=<heading line>`.
  - A second identical heading gives `defect=duplicate line=<second heading line>`.
  - A count that differs from its rows gives `field=FINDINGS|PROBES defect=contradictory line=<field line>`.
<!-- typedrec:end -->

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
job directory passes `--reference` off the batch's reference checkout and then `--dissociate`, so the object graph
is read once and the per-job clone is small.

### The prompt text is the tool's

Five shell scripts duplicated five of the seven tools on the measured bench,
and for `nova-swarm` the live text was `run-worker-v2.sh:120` — a shell
script's private variable, not a template. That is the inverse of this spec:
the prompts and their conditions are `internal/swarm/templates.go` in the
binary, printable, and versioned with the tool. So **the prompt text the workers run is the
tool's**: `Prompt` and `WrapTemplate` assemble it from the named template, the
shell scripts are prototypes, the shell scripts are prototypes. No tool's live state is a shell script's private variable.

### Red tests

The card earns the same red-first bar as every rule here: seen red before it
is trusted.

- a per-job clone built with `--reference` and `--dissociate` shares the reference checkout's object graph and still has its own working tree;
- the worker prompt carries the named template's conditions from the tool, with no shell script in the path.

## Efficiency: lessons absorbed 2026-09-12

Measured 2026-09-12 on the live pool and the worker homes. Correctness is the
rest of this spec; this section records what `nova-swarm` costs the coordinator
and the bench, and the rules that bound that cost. Three operations are the
widest, and each has one rule.

| the operation, measured | the measurement | the rule that bounds it |
|---|---|---|
| **REPEATS: one full clone of the repository per job** | 21 clones, 307 MB and 36 s of wall clock for one object graph; 1,128,320 cache-read tokens over 13 tool calls, 86,794 cache-read tokens per tool call, with the clone and the `gh pr checkout` two of them — about 174K per job and 3.6M across the 21 | the reference clone |
| **COORDINATOR READ: `triage` is the widest listing of the seven** | `status` 22 lines/3,174 B; `cost --max 0` 22/3,698 B; `triage` 45 lines/15,490 B with 20 of 47 at the default; `triage --all --max 0` 71/24,810 B — the counts on `TRIAGE OK` are 27 B of it | counts first, findings capped |
| **WAITS ON: a deadline, a sampler, and a person** | a job's own clock is `--deadline` (40m in `deepseek.json`); the usage budget is read by the sampler at `--usage-interval` (default 5 s); a verdict waits on a person | one owner per wait, one line back |

1. **The reference clone.** A job's clone is its own and is taken with
   `git clone --reference-if-able <the shared on-disk checkout> --dissociate`,
   so the object graph is shared with a checkout already on the bench and the
   job directory holds only its own objects; `--dissociate` keeps the job's
   clone independent of a reference it does not own.
2. **Counts first; a finding's prose is the second read.** The narrow return of
   `triage` is the counts: `TRIAGE BATCH` and `TRIAGE OK` are the lines a
   coordinator reads, and a `TRIAGE FINDING` line carries the finding's own
   prose, bounded by `--max` and capped by `internal/oneline`, with `TRIAGE
   MORE kind=finding shown=<n> total=<t> at=<path>` naming the page that holds
   the rest.
3. **One owner per wait, and one line back.** A job's own clock is the deadline
   held by the machinery; the usage budget is read by the sampler at
   `--usage-interval` and never by a second poll; and a verdict waits on a
   person, never on a scan.

## Sparse checkout of PATHS packages (#2498 S10)

Staging for a card that declares `PATHS:` checks out the **minimal tree**:
those packages and their in-module dependencies only. A package the card did
not name is not materialized. The named package's tests still run. `PATHS:
none`, or no `PATHS:` line, stays a full checkout. `prepare` does this into
`<job>/repo` when it is given the reference checkout.

A lookup that finds no in-module directories is a valid empty set: the PATHS
and TEST cones are still checked out. An import that cannot be resolved is
not empty. Staging refuses, and does not hand the worker a sparse tree that
omits that dependency.

**Red tests.** `TestSparseCheckoutDoesNotMaterializeAnUnrelatedPackage`: a
fixture PATHS list does not materialize an unrelated package; the named
package's tests still run. `TestPrepareStagesASparseJobClone`: prepare with
`CloneFrom` stages that sparse tree under the job root.
`TestSparseCheckoutRefusesAMissingInModuleImport`: a named package that
imports an in-module package that is not there makes staging refuse.
`TestSparseCheckoutEmptyInModuleSetStillChecksOutPATHS`: a PATHS list that
names no Go package still checks out that path.

## The rules, numbered

Every rule here is normative. Only living verbs are retained.

1. **The owed list first.** A `read-pr` task's prompt says: read the pull
   request body's owed list before any code, and skip what is owed. A finding
   already on that list is marked `dup:` and is not a new finding. `triage`
   counts a finding that matches an owed item and is not marked `dup:` as
   `duplicate`.
2. **Every claim quotes its rule verbatim, beside the line.** A finding line
   carries the rule it rests on, quoted word for word, with `file:line`, on the
   same line or the next. A finding with no quote is counted `unquoted`.
3. **Append as found, never at the end.** The prompt says: append each finding
   to the report file the moment it exists. A run killed at its deadline keeps
   its partial report, and `triage` counts every finding line in it.
4. **A refused read does not end the run.** The worker's prompt says so in one
   sentence. The harness refuses a read or a scratch write outside the job
   directory, and the worker continues with what is inside. `nova-swarm`
   reads the harness log for refusals and prints `refusals=<n>` on `RUN DONE`.
5. **The key file is data.** The key lives in one file, `~/.config/<provider>/env`
   or the path the worker description names. It is read as data, never sourced,
   never on a command line, never in a log, never in a file this tool writes.
6. **One clone per job; a written deadline.** Each job has its own clone under
   its own job directory, never shared. Each job carries a deadline held outside the worker.
7. **Results are counted per batch, on one line, and completion is evidence
   separate from the finding count.** `triage` classifies every report as
   `ok`, `clean`, `plan-only` or `no-result`, and prints one `TRIAGE BATCH`
   line. Evidence of completion is the report's `## Head` with `findings: <n>`;
   `findings: 0` is `clean` (a complete review that found nothing); `plan-only`
   is a `RESULT.md` with no head; `no-result` is no report at all.
8. **N workers are N processes.** Each worker has its own job directory and its
   own report file. No file is written by two workers.
9. **A job is one blocking process group, reported once.** The worker runs in its
   own process group. If the group leaves background survivors, the result is quarantined.
10. **Usage is written to a file by finalize.** The usage file is `<pool>/usage/<job>.tsv`.
    `finalize` writes usage for an ended job whose runner died before doing so.
11. **The swarm's own tokens are budgeted per job.** Every job carries `--tokens <n>`
    or `--tokens unmetered`. A worker running under a budget is stopped when the budget is spent.
12. **Malformed reports are quarantined.** `result --id <job>` prints a report verbatim,
    and malformed reports are not folded into the main triage page.
13. **Publication by rename.** Reports are published whole by renaming `.tmp` over `RESULT.md`.
    `triage` hashes the file before and after reading to ensure no partially written revision is folded.

## Test inventory

The 223 tests covering the seventeen living verbs in `cmd/nova-swarm/`, verified by `go test -list . ./cmd/nova-swarm/`:

| Test | Source | Living Verb | Purpose |
|---|---|---|---|
| `TestAMissingPoolNamesTheVerbThatMakesOne` | `audit_lessons_test.go` | `general` | S5: `--pool` on a missing directory named no remedy, and `quickstart` is exactly t... |
| `TestTemplatePrintsThePulseCardTemplates` | `audit_lessons_test.go` | `template` | #632: `template` must print the six typed card templates nova-pulse `cut` reads fr... |
| `TestTheCommandReferenceCarriesTheHarnessContract` | `audit_lessons_test.go` | `general` | S3: the harness contract was undocumented -- cwd, argv, NOVA_SWARM_JOB, RESULT.md ... |
| `TestEveryPrintedArgumentIsLiteralQuotedOrEscaped` | `audit_test.go` | `general` | The source-level tripwire behind the one-line guarantee: every argument this binar... |
| `TestNoOtherWriterOrShadowCanBypassTheEscape` | `audit_test.go` | `general` | Verifies general behavior and error contracts |
| `TestBatchCardsHelpNamesTokens` | `batch_cards_tokens_help_test.go` | `batch` | THE BATCH CONTRACT A CALLER READS (#3202, found dogfooding #1615).  |
| `TestBatchCardsWithoutTokensPrintsTheBatchRefusal` | `batch_cards_tokens_help_test.go` | `batch` | Verifies batch behavior and error contracts |
| `TestBatchAcceptsTheGatherFlagsThatLandedUnguarded` | `batch_flag_wiring_test.go` | `batch` | 2d99edbf wired --max-inflight and --stall-after on batch; 021e9e4b wired --harness... |
| `TestNativeDrainDeliversPoolIdentityToChild` | `boundary_identity_test.go` | `native` | Verifies native behavior and error contracts |
| `TestBoundaryIdentityNegativeControlFallsBackToBenchConfigOrFails` | `boundary_identity_test.go` | `general` | Verifies general behavior and error contracts |
| `TestNativeRefusesMissingPoolIdentityBeforeHarness` | `boundary_identity_test.go` | `native` | Verifies native behavior and error contracts |
| `TestNativeRefusesMalformedPoolIdentityBeforeHarness` | `boundary_identity_test.go` | `native` | Verifies native behavior and error contracts |
| `TestDoctorOKWhenBothStampsMatch` | `doctor_test.go` | `doctor` | MATCHING STAMPS ARE OK, one line, exit 0. This is the answer on a healthy bench an... |
| `TestDoctorRefusesWhenThePATHBinaryIsShadowed` | `doctor_test.go` | `doctor` | A DIFFERENT STAMP IS A REFUSAL, exit 2, with BOTH full version lines printed and o... |
| `TestDoctorResolvesNovaSwarmOnPATH` | `doctor_test.go` | `doctor` | THE PATH RESOLVER IS THE SEAM, and this test drives it: `--path` is left off, the ... |
| `TestDoctorOKWhenPATHResolvesToTheLocalBinary` | `doctor_test.go` | `doctor` | PATH's nova-swarm IS ~/.local/bin/nova-swarm: there is no second binary and so not... |
| `TestDoctorOKWhenTheLocalBinaryIsAbsent` | `doctor_test.go` | `doctor` | NO ~/.local/bin COPY means there is nothing that could be shadowed: the guard cann... |
| `TestPreflightRefusesALaunchUnderAShadowedBinary` | `doctor_test.go` | `doctor` | THE LAUNCH SEAM. `run` and `native` are the verbs that start a card, and the prefl... |
| `TestPreflightLeavesNonLaunchVerbsAlone` | `doctor_test.go` | `doctor` | Verifies doctor behavior and error contracts |
| `TestDoctorVerbIsReachableFromTheDispatch` | `doctor_test.go` | `doctor` | The verb is reachable from the dispatcher, and `doctor` is not a launch verb itsel... |
| `TestDoctorSurvivesAnUnreadablePATHBinary` | `doctor_test.go` | `doctor` | An unreadable override is not silently ignored: `--path`/`--local` name binaries a... |
| `TestIsExecutableAsksThePlatformsOwnRule` | `executable_test.go` | `general` | THE EXECUTE QUESTION IS ASKED OF THE PLATFORM, NOT OF THE UNIX BIT (windows leg, 2... |
| `TestExecutableByExtensionIsTheWindowsRule` | `executable_test.go` | `general` | THE WINDOWS RULE, HELD TO ITS CONTRACT ON EVERY PLATFORM. executableByExtension is... |
| `TestIsExecutableUnixBits` | `executable_test.go` | `general` | The unix body is the one darwin and linux can exercise directly: the three execute... |
| `TestNativeConfigNamesTheWorkerReadRootsOnAWalledRun` | `fence_readroots_test.go` | `native` | TestNativeConfigNamesTheWorkerReadRootsOnAWalledRun is the issue, at the fence. RE... |
| `TestNativeWallReadsTheWorkerReadRoots` | `fence_readroots_test.go` | `native` | TestNativeWallReadsTheWorkerReadRoots is the same declaration at the OTHER fence. ... |
| `TestNativeConfigStillWithholdsTheCardsReadPathsOnAWalledRun` | `fence_readroots_test.go` | `native` | TestNativeConfigStillWithholdsTheCardsReadPathsOnAWalledRun is the line this fix d... |
| `TestNativeConfigNamesTheJobDirectory` | `fence_test.go` | `native` | TestNativeConfigNamesTheJobDirectory: every native run writes a harness config, wh... |
| `TestNativeConfigDeniesExternalPaths` | `fence_test.go` | `native` | TestNativeConfigDeniesExternalPaths: the generated config the child reads DENIES a... |
| `TestNativeConfigNamesTheCardsReadPaths` | `fence_test.go` | `native` | TestNativeConfigNamesTheCardsReadPaths: on a bench with NO OS WALL, a card that na... |
| `TestNativeReportsAFenceRejection` | `fence_test.go` | `native` | TestNativeReportsAFenceRejection: a harness that prints its own rejection line and... |
| `TestUsageBannerExamplesRun` | `firstrun_test.go` | `general` | (a) The usage banner ends in an `example:` block of lines that actually run. They ... |
| `TestUsageBannerExamplesMakeThePoolBeforeReadingIt` | `firstrun_test.go` | `general` | (a2) The `example:` block is pasted top to bottom by a stranger in an empty direct... |
| `TestABareInvocationCostsOneLineAndNamesTheDoor` | `firstrun_test.go` | `general` | (b) A bare invocation costs ONE line and names the door, rather than 60 lines of b... |
| `TestTheReadmeTranscriptIsWhatTheToolPrints` | `firstrun_test.go` | `general` | (c) The README transcript is compared against what the tool prints -- the event pr... |
| `TestFirstRunTranscriptIsWhatTheToolPrintsLineForLine` | `firstrun_test.go` | `general` | (d) The `### First run` block of docs/TESTS.md is EXECUTED: every command in it is... |
| `TestTheCommandReferenceFirstRunIsWhatTheToolPrints` | `firstrun_test.go` | `general` | TestTheCommandReferenceFirstRunIsWhatTheToolPrints executes docs/CLI.md's `### Fir... |
| `TestUsageBannerExamplesRunThroughTheComparator` | `firstrun_test.go` | `general` | TestUsageBannerExamplesRunThroughTheComparator: the banner's `example:` block is p... |
| `TestTheBenchRefusesAWaitThatReachesTheJobsOwnDeadline` | `fixture_guards_test.go` | `general` | Verifies general behavior and error contracts |
| `TestTheDeadlineACappedJobIsMeasuredAgainstIsTheOneItRunsUnder` | `fixture_guards_test.go` | `general` | The other half: the deadline the cap is measured against is the one the job RUNS u... |
| `TestThePendingPoolIsCappedAgainstTheWorkerTheRunWillUse` | `fixture_guards_test.go` | `worker` | THE SECOND POINT THE CAP IS APPLIED (the #140 read, finding 2): the pending pool, ... |
| `TestDeniedPathWithASpaceStillRefuses` | `gate_hold_test.go` | `general` | TestDeniedPathWithASpaceStillRefuses is P1 as a CLI regression, at the full path. ... |
| `TestRefusalClaimsNoCauseItCannotProve` | `gate_hold_test.go` | `general` | TestRefusalClaimsNoCauseItCannotProve is P2. A shell's `Permission denied` on a pa... |
| `TestWalledRefusalOffersTheReadRootsAsOnePossibility` | `gate_hold_test.go` | `general` | TestWalledRefusalOffersTheReadRootsAsOnePossibility: on a WALLED run the read set ... |
| `TestADenialTheCardRewroteStillRefuses` | `gate_hold_test.go` | `general` | TestADenialTheCardRewroteStillRefuses is Johnny's hold on #1478 at 29047871, the #... |
| `TestNativeGateThatCouldNotRunIsNeverOK` | `gate_test.go` | `native` | AN UNREAD DENIAL CANNOT RETURN OK (issue #1465, as Stella's HOLD on #1478 reshaped... |
| `TestNativeOrdinaryRunIsStillOK` | `gate_test.go` | `native` | TestNativeOrdinaryRunIsStillOK: the guard above fires on the class and on nothing ... |
| `TestNativeWalledRunOpensTheWorkersReadRoots` | `gate_test.go` | `native` | THE READ ROOTS THE DESK NAMED REACH BOTH FENCES (issue #1463).  |
| `TestIssue2012` | `issue2012_test.go` | `general` | Verifies general behavior and error contracts |
| `TestIssue2328` | `issue2328_test.go` | `general` | TestIssue2328 asserts that OPENCODE_EXPERIMENTAL_DISABLE_FILEWATCHER survives nati... |
| `TestLintGoodCardPasses` | `lint_test.go` | `lint` | Verifies lint behavior and error contracts |
| `TestLintNamesTheParentPathLine` | `lint_test.go` | `lint` | A card that writes its scratch above the job is the defect that killed cards tonig... |
| `TestLintNamesTheMissingRedTest` | `lint_test.go` | `lint` | A card that names no red test is the defect the work order says to catch: the chec... |
| `TestLintRefusesNovaSandbox` | `lint_test.go` | `lint` | A card that reaches for the sandbox is a probe this tool does not run, and its def... |
| `TestTemplateThenLintPasses` | `lint_test.go` | `lint` | issue #1471: a new friend who follows the help (`nova-swarm template --name <t>` t... |
| `TestLintAdvisesAnOversizeCardAndDoesNotRefuseIt` | `lint_test.go` | `lint` | A card over the ceiling is ADVISED and never refused (issues #1494, #1527). This t... |
| `TestAnOversizeDriftingCardIsRefusedForTheDriftAndSaysTheCeilingIsAdvisory` | `lint_test.go` | `lint` | A card that is BOTH over the ceiling and drifting is refused for the drift alone, ... |
| `TestTheShiftsOwnCardsLintClean` | `lintcards_test.go` | `lint` | The quoted `../` on the shift's own cards is not a walk, so it is not a drift. The... |
| `TestACardOverTheCeilingIsAdvisedNotRefused` | `lintcards_test.go` | `lint` | The card that is over the ceiling says so on a NOTE, never a DRIFT: the ceiling is... |
| `TestLintAcceptsAMakeTestCommand` | `lintcmd_test.go` | `lint` | The make gate the polyglot lane actually ran. A `make <target>` line was the gate ... |
| `TestLintAcceptsAGmakeTestCommand` | `lintcmd_test.go` | `lint` | `gmake` is the BSD make on macOS benches; AGENTS.md says benches run the card, and... |
| `TestLintAcceptsCommonPolyglotRunners` | `lintcmd_test.go` | `lint` | The other runners the polyglot lane measured. `dotnet test`, `ctest`, `mvn test` a... |
| `TestLintAcceptsScriptStyleTestCommands` | `lintcmd_test.go` | `lint` | `bash <script>` and a bare `./<script>` are how a script-driven gate looks in a ca... |
| `TestLintStillRefusesCargoRunNotCargoTest` | `lintcmd_test.go` | `lint` | A `cargo run` is not a test command -- it is what the recipe does AFTER the tests ... |
| `TestLintTestCommandRemedyNamesTheAcceptedSet` | `lintcmd_test.go` | `lint` | The drift's remedy names the wider set so a card writer on a bench with a stale cl... |
| `TestLintTestCommandRefusesSubstringsAndFalsePositives` | `lintcmd_test.go` | `lint` | EDGE CASES: the widen-the-whitelist fix must NOT widen the false-positive set. A c... |
| `TestLintAcceptsHyphenatedMakeTargets` | `lintcmd_test.go` | `lint` | `make <target>` where the target contains a hyphen must match. The polyglot lane's... |
| `TestLintAcceptsMakeWithPathTarget` | `lintcmd_test.go` | `lint` | `make` with a path-shaped target (`make ./scripts/test.sh`) must also match; the h... |
| `TestLintTypedRefusesACardWithNoDependsOn` | `lintdepends_test.go` | `lint` | Verifies lint behavior and error contracts |
| `TestLintTypedRefusesASelfDependency` | `lintdepends_test.go` | `lint` | Verifies lint behavior and error contracts |
| `TestLintTypedRefusesAnUnknownDependsOnID` | `lintdepends_test.go` | `lint` | Verifies lint behavior and error contracts |
| `TestLintTypedDependsOnDashPasses` | `lintdepends_test.go` | `lint` | Verifies lint behavior and error contracts |
| `TestLintTypedDependsOnReferencePasses` | `lintdepends_test.go` | `lint` | Verifies lint behavior and error contracts |
| `TestLintTypedRefusesASpaceAndDogfood` | `lintdepends_test.go` | `lint` | Verifies lint behavior and error contracts |
| `TestLintTypedDependsOnKnownIDPasses` | `lintdepends_test.go` | `lint` | Verifies lint behavior and error contracts |
| `TestLintCardRefusesAWindowsDriveLetterPath` | `lintescapes_test.go` | `lint` | #1853.1 A WINDOWS DRIVE LETTER IS AN ABSOLUTE PATH. `PATHS: C:/foo/bar` used to li... |
| `TestLintCardRefusesMoreThanEightPathGlobs` | `lintescapes_test.go` | `lint` | #1853.2 THE CAP ON PATHS: IS EIGHT (SPEC-TOOLWORK.md:579-580). Nine globs used to ... |
| `TestLintCardRefusesACommaOnlyPathsLine` | `lintescapes_test.go` | `lint` | #1853.3 A COMMA-ONLY PATHS: LINE DECLARES NOTHING. `PATHS: , , ` is not `PATHS: no... |
| `TestLintCardRefusesAnUnknownKind` | `lintescapes_test.go` | `lint` | #1853.4 AN UNKNOWN KIND IS NOT A KIND. `KIND: completely-unknown-kind` used to lin... |
| `TestLintCardMissingKindDrawsKindDeclared` | `lintheader_test.go` | `lint` | A card missing KIND: draws kind-declared, and the drift line carries the remedy th... |
| `TestLintCardDrawsTestNamedAndPathsDeclared` | `lintheader_test.go` | `lint` | A card with no TEST: draws test-named; a card whose PATHS: climbs or names everywh... |
| `TestLintCardAcceptsKindReportAndRefusesANonsenseKind` | `lintheader_test.go` | `lint` | KIND: report is a card kind. The wake-chain card carries it, and lint accepts that... |
| `TestLintCardCompleteHeaderPasses` | `lintheader_test.go` | `lint` | NEGATIVE CONTROL: the same card, header complete and every value one the gate read... |
| `TestLintCardPausedKindNamesTheTrialRemedy` | `lintheader_test.go` | `lint` | The paused token names the kind AND carries the `trust --set trial` command, becau... |
| `TestLintCardTrialKindAndNoFixtureAreClean` | `lintheader_test.go` | `lint` | NEGATIVE CONTROL for paused: the same card against a fixture that has the kind on ... |
| `TestLintCardTypedRequiresTheHeader` | `lintheader_test.go` | `lint` | `--typed` requires the header of a card that carries none, which is what a card cu... |
| `TestLintTrustFixtureMustBeReadable` | `lintheader_test.go` | `lint` | An unreadable fixture is a refusal that names the flag, not a silent pass: a lint ... |
| `TestLintRulesNamesTheFourNewTokens` | `lintheader_test.go` | `lint` | `lint --rules` is the listing a bench with a stale clone reads (#1464), so the fou... |
| `TestLintCloneStepReadsTheWholeStepNotOnlyItsLine` | `lintheader_test.go` | `lint` | `clone-step` READS THE STEP, NOT ONLY ITS FIRST LINE. The check matched the STEP 1... |
| `TestLintCloneStepStillDraftsAStepThatEntersNothing` | `lintheader_test.go` | `lint` | NEGATIVE CONTROL for clone-step: a STEP 1 that neither clones nor cds anywhere in ... |
| `TestLintAlwaysNamesTheSizeAndTheCap` | `lintheader_test.go` | `lint` | THE SIZE CEILING IS NOT A SILENT BOUND. A card writer learns of the 12000-byte cap... |
| `TestEveryDriftCarriesItsRemedy` | `lintremedy_test.go` | `lint` | TestEveryDriftCarriesItsRemedy: the card of #1464, in its own words, and every dri... |
| `TestLintRulesPrintsEveryRuleAndItsRemedy` | `lintremedy_test.go` | `lint` | TestLintRulesPrintsEveryRuleAndItsRemedy: the second half of #1464. The rules are ... |
| `TestLintRulesNeedsNoCard` | `lintremedy_test.go` | `lint` | TestLintRulesNeedsNoCard: a card writer asking what the rules want has no card yet... |
| `TestNativeWritesAnAskedResultForACardThatEndedWithAQuestion` | `native_asked_test.go` | `result` | THE SHAPE ITSELF: the harness says its last word, it is a question, and the proces... |
| `TestNativeLeavesAFinishedCardsReportAlone` | `native_asked_test.go` | `native` | A CARD THAT PUBLISHED IS DONE, whatever its prose said. The fake harness publishes... |
| `TestNativeDoesNotCallACrashAnAskedCard` | `native_asked_test.go` | `native` | A CRASH IS A CRASH. The harness asks, then exits non-zero: that is `rc=<n>`, an en... |
| `TestNativeRefusesABudgetNothingCanObserve` | `native_budget_source_test.go` | `native` | TestNativeRefusesABudgetNothingCanObserve is the heart of slice 2. Each case names... |
| `TestNativeUsageIntervalFloorAndCeiling` | `native_budget_source_test.go` | `native` | TestNativeUsageIntervalFloorAndCeiling: rule 13d, "On `native` an interval under o... |
| `TestNativeRefusesWithoutTheBudgetWord` | `native_budget_test.go` | `native` | TestNativeRefusesWithoutTheBudgetWord: rule 13d, "The word is required on every la... |
| `TestNativeUnmeteredPrintsTheWordOnTheLine` | `native_budget_test.go` | `native` | TestNativeUnmeteredPrintsTheWordOnTheLine: rule 13d, "`NATIVE OK` always carries `... |
| `TestNativeNumericBudgetPrintsAgainstTheNumber` | `native_budget_test.go` | `native` | TestNativeNumericBudgetPrintsAgainstTheNumber: the same line under a number. Until... |
| `TestNativeBudgetSitsWhereTheGrammarPutsIt` | `native_budget_test.go` | `native` | TestNativeBudgetSitsWhereTheGrammarPutsIt: the output grammar (SPEC-SWARM.md:1946)... |
| `TestNativeHandsCardRepoToCardOut` | `native_cardout_test.go` | `native` | TestNativeHandsCardRepoToCardOut is the quack-test defect of 2026-09-24: under the... |
| `TestHandOffCardOutRefusals` | `native_cardout_test.go` | `native` | TestHandOffCardOutRefusals: no NOVA_CARD_OUT is no hand-off and leaves the job as ... |
| `TestNativeRefusesALabelThatWalksOutOfTheSwarmRoot` | `native_label_path_test.go` | `native` | ISSUE #1923. `native` checks that the SLOT is under the root and then joins the ca... |
| `TestNativeStillAcceptsAnOrdinaryLabel` | `native_label_path_test.go` | `native` | The same admission with an honest label still makes the job directory, so the refu... |
| `TestNativeLaunchGoesThroughTheOneLauncher` | `native_launcher_test.go` | `native` | TestNativeLaunchGoesThroughTheOneLauncher is the call site #2646 left open (stella... |
| `TestNativeLaunchCarriesTheResultFormat` | `native_launcher_test.go` | `result` | TestNativeLaunchCarriesTheResultFormat is nova-tools#3651, and #3689: a typed card... |
| `TestNativeProbeCardRunsNovaCheckByName` | `native_probe_test.go` | `native` | SWARM GATE #3501: inside card sandbox ~/.local/bin is on PATH but its files cannot... |
| `TestControlCardResultsSurviveSweep` | `native_results_test.go` | `result` | TestControlCardResultsSurviveSweep is issue #2632. A control card's RESULT.md, usa... |
| `TestTwoInvocationsPreserveResults` | `native_results_test.go` | `result` | TestTwoInvocationsPreserveResults is the preservation control for a repeated label... |
| `TestPublicationFailureKeepsTheJob` | `native_results_test.go` | `result` | TestPublicationFailureKeepsTheJob is the capture-failure control. The harness unli... |
| `TestASecondNativeRunInOneJobDirectoryIsRefusedAndTheFirstIsUntouched` | `native_same_job_test.go` | `native` | ISSUE #1585, Stella's finding, as two `native` runs in one physical job directory.  |
| `TestANativeRunThatCannotEstablishOwnershipRefusesBeforeItWritesAnything` | `native_same_job_test.go` | `native` | STELLA'S SECOND P1 AT THE VERB (#1585, her HOLD on 6146897a). With `.lease` a path... |
| `TestNativeRunsWithNoSlotsStore` | `native_slot_lease_test.go` | `native` | TestNativeRunsWithNoSlotsStore: no --slots-store, no --owner, and a home with no n... |
| `TestNativeIgnoresTheRetiredSlotsStore` | `native_slot_lease_test.go` | `native` | TestNativeIgnoresTheRetiredSlotsStore: the batman shape, a store whose one owner h... |
| `TestNativeArgvReadsHarnessDir` | `native_test.go` | `native` | TestNativeArgvReadsHarnessDir: the wall's argv reads the harness binary's own dire... |
| `TestNativeArgvReadsTheBenchToolchainRoots` | `native_test.go` | `native` | TestNativeArgvReadsTheBenchToolchainRoots is the edge the schema dogfood loop foun... |
| `TestNativeArgvReadsTheDarwinToolchainRoots` | `native_test.go` | `native` | TestNativeArgvSkipsAToolchainRootThatIsNotThere: rule 5 of the wall REFUSES a --re... |
| `TestNativeArgvSkipsAToolchainRootThatIsNotThere` | `native_test.go` | `native` | Verifies native behavior and error contracts |
| `TestNativeRunRefusesMissingBinary` | `native_test.go` | `native` | TestNativeRunRefusesMissingBinary: a binary that does not exist, and one that exis... |
| `TestNativeRunRecordsCardAndBinaryHashes` | `native_test.go` | `native` | TestNativeRunRecordsCardAndBinaryHashes: a run that finishes records the child's e... |
| `TestNativeRunAuthCopyIs0600` | `native_test.go` | `native` | TestNativeRunAuthCopyIs0600: the named provider's entry is copied from the auth fi... |
| `TestNativeCarriesProviderConfig` | `native_test.go` | `native` | TestNativeCarriesProviderConfig: a `--config` opencode.json is carried beside the ... |
| `TestNativeRefusesConfigProviderWithoutKey` | `native_test.go` | `native` | TestNativeRefusesConfigProviderWithoutKey: a --config whose entry for THE MODEL'S ... |
| `TestNativeConfigChecksOnlyTheModelsProvider` | `native_test.go` | `native` | TestNativeConfigChecksOnlyTheModelsProvider: a --config may name every provider a ... |
| `TestNativeConfigKeylessProviderAdmitted` | `native_test.go` | `native` | TestNativeConfigKeylessProviderAdmitted: a --config that names a provider whose en... |
| `TestNativeOKNamesTheCarriedConfig` | `native_test.go` | `native` | TestNativeOKNamesTheCarriedConfig: the NATIVE OK line itself names the config the ... |
| `TestFriendSequenceLocalModelCard` | `native_test.go` | `native` | TestFriendSequenceLocalModelCard runs one known-answer card on a fake local provid... |
| `TestNativeAllowsProviderLoopback` | `native_test.go` | `native` | TestNativeAllowsProviderLoopback: a keyless provider (baseURL, no apiKey) whose ba... |
| `TestNativeRunRefusalsNameTheirReason` | `native_test.go` | `native` | TestNativeRunRefusalsNameTheirReason drives the remaining three refusals -- a mode... |
| `TestCmdNativeCLI` | `native_test.go` | `native` | Verifies native behavior and error contracts |
| `TestNativeRunChildDirIsJobDir` | `native_test.go` | `native` | TestNativeRunChildDirIsJobDir: the child runs in its job directory <slot>/jobs/<la... |
| `TestNativeChildCwdIsJobDirFromForeignCwd` | `native_test.go` | `native` | TestNativeChildCwdIsJobDirFromForeignCwd: the walled child also runs in the job di... |
| `TestWallNamedDecodesTheProducersEscapedCwd` | `native_test.go` | `native` | TestWallNamedDecodesTheProducersEscapedCwd is the unit half of issue #572: the wal... |
| `TestNativeWalledJobPathWithSpacesCompletes` | `native_test.go` | `native` | TestNativeWalledJobPathWithSpacesCompletes is the regression for issue #572: a job... |
| `TestNativeOKNamesTheWall` | `native_test.go` | `native` | TestNativeOKNamesTheWall: NATIVE OK names the wall it ran inside, copied from the ... |
| `TestNativeRunPassesRepoAllowRule` | `native_test.go` | `native` | TestNativeRunPassesRepoAllowRule: when the wall can express a hash host rule, the ... |
| `TestNativeRunDeniesBusInsideWall` | `native_test.go` | `native` | TestNativeRunDeniesBusInsideWall: recipients are never turned into an allow rule. ... |
| `TestNativeRefusesWhenWallCannotExpressRule` | `native_test.go` | `native` | TestNativeRefusesWhenWallCannotExpressRule: a card that names repos but no wall, o... |
| `TestNativeRefusesWithoutWallUnlessFlagged` | `native_test.go` | `native` | TestNativeRefusesWithoutWallUnlessFlagged: the wall is never implied away (SPEC-SA... |
| `TestNativeRunsWalledWithoutHostRulesWhenNoRepos` | `native_test.go` | `native` | TestNativeRunsWalledWithoutHostRulesWhenNoRepos: a wall that cannot express a host... |
| `TestNativeEnvIsCleanAndInsideTheWall` | `native_test.go` | `native` | TestNativeEnvIsCleanAndInsideTheWall: the walled child is handed a clean environme... |
| `TestNativeSharedGoCaches` | `native_test.go` | `native` | TestNativeSharedGoCaches: the Go module and build caches are bench-shared under <r... |
| `TestNativeNoSharedCachesRestoresHomeCaches` | `native_test.go` | `native` | TestNativeNoSharedCachesRestoresHomeCaches: --no-shared-caches restores today's be... |
| `TestNativeChildCwdIsJobDirUnwalled` | `native_test.go` | `native` | TestNativeChildCwdIsJobDirUnwalled: the child runs in its job directory on BOTH pa... |
| `TestNativeRunWritesUsageInJobDirectory` | `native_test.go` | `native` | TestNativeRunWritesUsageInJobDirectory: the native run writes usage.tsv beside RES... |
| `TestNativeRelativeSlotIsAbsolutized` | `native_test.go` | `native` | TestNativeRelativeSlotIsAbsolutized: the native run absolutizes --slot and --root ... |
| `TestNativeTmpDirIsOutsideAnyRepo` | `native_test.go` | `native` | TestNativeTmpDirIsOutsideAnyRepo: the native run hands the child a TMPDIR that is ... |
| `TestNativeNoWallWritesHarnessLog` | `native_test.go` | `native` | TestNativeNoWallWritesHarnessLog: the UNWALLED run captures the harness's output t... |
| `TestNativeSilentHarnessIsNotOK` | `native_test.go` | `native` | TestNativeSilentHarnessIsNotOK pins THE ONE DEFINITION of the token (issues #591, ... |
| `TestNativeCaptureRefusesSymlink` | `native_test.go` | `native` | TestNativeCaptureRefusesSymlink: the capture is the first file this process opens ... |
| `TestNativeRefusesAModelThatDiffersFromTheWorkerDescription` | `native_test.go` | `native` | ISSUE #881 (a): a key is authorized for one model only, and the worker description... |
| `TestNativeSecretWorkerWritesNoAuthFileAndTheHarnessSeesName` | `native_test.go` | `native` | ISSUE #881 (b): a description naming "secret": "<NAME>" takes the key from the ENV... |
| `TestNativeAuthWithAWorkerNamesItsLegacyCopy` | `native_test.go` | `native` | ISSUE #881 (b), the legacy half: `--auth` with a `--worker` description whose key ... |
| `TestNativeAuthCopyIsGoneAfterTheRun` | `native_test.go` | `native` | The legacy --auth copy is the child's for the length of the run and no longer: the... |
| `TestAuthModeRulesAskThePlatform` | `native_test.go` | `native` | ISSUE #915 (windows leg): the legacy --auth native path refuses an auth source loo... |
| `TestNativeWorkerModelGateComparesQualifiedName` | `native_test.go` | `native` | ISSUE #881: secret implies env_var, and the model gate compares provider/model as ... |
| `TestNativeWalledJobPathWithSpace` | `native_test.go` | `native` | TestNativeWalledJobPathWithSpace: a walled native run whose root, slot and job dir... |
| `TestNativeHoldsAJobLease` | `native_test.go` | `native` | TestNativeHoldsAJobLease: the launcher takes <job>/.lease BEFORE the child starts ... |
| `TestRemoveAuthCopySurvivesAReadOnlyDataHome` | `native_test.go` | `native` | codex-review's hold on #2806: the card owns the data home while it runs, so it can... |
| `TestRemoveAuthCopyNamesACopyItCannotRemove` | `native_test.go` | `native` | A copy the cleanup cannot remove at all is named, never swallowed: here the card r... |
| `TestNativeRefusesToSayOKForAProviderFailureThatProducedNothing` | `native_verdict_test.go` | `native` | THE C18 SHAPE: the provider answers 5xx at request start, the harness exits 1, and... |
| `TestNativeRefusesToSayOKWhenTheHarnessSaidNothing` | `native_verdict_test.go` | `native` | A harness that exits 0, says nothing and writes nothing is the same class: the exi... |
| `TestNativeStillSaysOKForARunThatProducedItsResult` | `native_verdict_test.go` | `result` | AND THE OTHER DIRECTION, so the fix is not "never say OK": a card that ran, answer... |
| `TestNativeHarnessExit255PrintsAVerdictAndDoesNotExit255` | `native_verdict_test.go` | `native` | THE SUPERMAN SHAPE (nova-tools #2058). Darwin, harness v1.18.20, 24 cards at once:... |
| `TestNativeOrdinaryCardsStillPrintOKAndIncomplete` | `native_verdict_test.go` | `native` | THE NEGATIVE, so the 255 clamp is not "never say OK/INCOMPLETE": a card that produ... |
| `TestCardEndEventIsOKOnlyWhenTheCardEarnedIt` | `nativeevent_test.go` | `native` | TestCardEndEventIsOKOnlyWhenTheCardEarnedIt: the entry's kind is the NATIVE line's... |
| `TestCardEndEventCarriesTheUsageRow` | `nativeevent_test.go` | `native` | TestCardEndEventCarriesTheUsageRow: the numbers in the stream are the numbers in u... |
| `TestCardEndEventKeepsDashesOutOfTheStream` | `nativeevent_test.go` | `native` | TestCardEndEventKeepsDashesOutOfTheStream: a fast failure whose provider reported ... |
| `TestCardEndEmitCannotFailTheCard` | `nativeevent_test.go` | `native` | TestCardEndEmitCannotFailTheCard IS THE CONTRACT. The store errors on every entry;... |
| `TestCardEndEmitIsSkippedWithoutAPassword` | `nativeevent_test.go` | `native` | TestCardEndEmitIsSkippedWithoutAPassword: a bench that has not been given NOVA_RED... |
| `TestCardEndEmitWritesTheEntry` | `nativeevent_test.go` | `native` | TestCardEndEmitWritesTheEntry is the positive control: without it every assertion ... |
| `TestCardEndEmitWritesTheCardsDoneKey` | `nativeevent_test.go` | `native` | TestCardEndEmitWritesTheCardsDoneKey locks in the KEY, not just the entry. cmdNati... |
| `TestCardEndEmitBoundsTheWriteAndNotOnlyTheDial` | `nativeevent_test.go` | `native` | TestCardEndEmitBoundsTheWriteAndNotOnlyTheDial: `WriterOptions.Timeout` bounds the... |
| `TestNativeDoesNotTreatAPlantedSymlinkAsAPublishedResult` | `planted_result_test.go` | `result` | native's result lookup must not follow a symlink planted at RESULT.md and call it ... |
| `TestNativeHarnessStateDoesNotFollowAPlantedSymlinkAtResult` | `planted_result_test.go` | `result` | The lookup harnessState uses is the same question native asks of RESULT.md. |
| `TestNativeRunWritesTimeline` | `profile_test.go` | `profile` | TestNativeRunWritesTimeline: a native run timestamps the harness's own per-turn an... |
| `TestProfilePrintsPhases` | `profile_test.go` | `profile` | TestProfilePrintsPhases: `nova-swarm profile --jobs <glob>` prints one PROFILE lin... |
| `TestNativeRetriesAProvider5xxLaunch` | `provider_retry_test.go` | `native` | TestNativeRetriesAProvider5xxLaunch: the native path retries a launch that dies in... |
| `TestNativeLostResponseStaysUnknownAndLaunchesOnce` | `provider_retry_test.go` | `native` | A lost response is one launch, and the usage row stays unknown. The fake harness p... |
| `TestPersistUnknownFallsBackWhenTheMarkerCannotBeWritten` | `provider_retry_test.go` | `general` | Verifies general behavior and error contracts |
| `TestUnrecordedUnknownIsStillAHarvestHold` | `provider_retry_test.go` | `general` | Verifies general behavior and error contracts |
| `TestPersistUnknownFailsWhenNothingCanBeWritten` | `provider_retry_test.go` | `general` | Verifies general behavior and error contracts |
| `TestNativeLostResponseLineSaysUnknownAcceptance` | `provider_retry_test.go` | `native` | Verifies native behavior and error contracts |
| `TestSuperviseTypedByHandIsRefused` | `refusal_test.go` | `general` | `supervise` is run's child and nobody's verb. |
| `TestAnUnusableInvocationCostsOneLine` | `refusal_test.go` | `general` | An unknown verb and a flag typo cost ONE line each, never the banner. |
| `TestABatchIsAllOfItsTasksOrNone` | `refusal_test.go` | `batch` | An empty task directory is a BATCH REFUSED at exit 1, and one unreadable file queu... |
| `TestLaunchCannotSkipTheRouteWithoutALoggedReason` | `route_default_test.go` | `route` | H4 (SPEC-DECIDE, issue #1625): routing is the launcher's default and the one way o... |
| `TestRouteFixComplexity1PicksMuse` | `route_test.go` | `route` | A fix/complexity-1 card routes to the muse row. |
| `TestRoutePrivateSkipsPublic` | `route_test.go` | `route` | The same card with touches_private 0.9 skips the public row and lands on the paid ... |
| `TestRouteBelowFloorExits3` | `route_test.go` | `route` | Kind confidence below the floor yields worker=default and exit 3. |
| `TestRouteFallsToLowerComplexity` | `route_test.go` | `route` | A missing routes row falls to the nearest lower complexity for that kind. |
| `TestRouteTablePicksExactAndFallsBack` | `route_test.go` | `route` | Socket-free: the routes table picks exact, skips public for private material, fall... |
| `TestRouteQuestionsHaveFourKinds` | `route_test.go` | `route` | Socket-free: the verb asks the four documented questions. |
| `TestRouteRefusesWithoutRoutes` | `route_test.go` | `route` | Verifies route behavior and error contracts |
| `TestSeatGivesTheCardEndWriterItsLogin` | `seat_event_test.go` | `general` | TestSeatGivesTheCardEndWriterItsLogin is #4052 on nova-swarm: with --seat the card... |
| `TestTheCardsShellNeverSeesASecret` | `shellshim_test.go` | `general` | TestTheCardsShellNeverSeesASecret is the red team's probe, in Go: a shell started ... |
| `TestTheShimNeverPrintsAValue` | `shellshim_test.go` | `general` | TestTheShimNeverPrintsAValue reads the wrapper's own text: no value, of the fixtur... |
| `TestTheShimIsInTheWallsReadSetAndNotItsWriteSet` | `shellshim_test.go` | `general` | TestTheShimIsInTheWallsReadSetAndNotItsWriteSet pins where the wrappers live: unde... |
| `TestTheChildEnvPutsTheShimFirstAndPinsShell` | `shellshim_test.go` | `general` | TestTheChildEnvPutsTheShimFirstAndPinsShell asserts the two names the harness reso... |
| `TestTheChildEnvIsUnchangedWithoutAShim` | `shellshim_test.go` | `general` | TestTheChildEnvIsUnchangedWithoutAShim keeps the windows path and the argv-builder... |
| `TestTheCardsShellReachesNoGh` | `shellshim_test.go` | `general` | TestTheCardsShellReachesNoGh is #3600's card half: a shell the harness starts with... |
| `TestSlotsTakeRefusesASchemaKindWhenTheShareFitsOnlyARead` | `slots_admission_2033_test.go` | `slots` | Verifies slots behavior and error contracts |
| `TestSlotsListMarksADeadHolderStrandedWithItsLabel` | `slots_admission_2033_test.go` | `slots` | Verifies slots behavior and error contracts |
| `TestSlotsInitMakesAStoreTheLeaseCodeCanRead` | `slots_init_test.go` | `slots` | Verifies slots behavior and error contracts |
| `TestTheStoreSlotsInitWritesGrantsOneLeaseAndRefusesTheSecond` | `slots_init_test.go` | `slots` | The store init writes is honoured by the lease code: one seat means one lease, and... |
| `TestSlotsInitRefusesToOverwriteAStore` | `slots_init_test.go` | `slots` | init creates and never updates: the leases under a live store belong to processes ... |
| `TestSlotsInitWantsAllFourFlags` | `slots_init_test.go` | `slots` | Every flag is required, and one run names every one that is missing: the onboardin... |
| `TestSlotsInitRefusesAReservedOwnerName` | `slots_init_test.go` | `slots` | An owner spelled as one of shares.tsv's two reserved keys would be read back as th... |
| `TestSlotsInitRefusesAShareWiderThanTheCapacity` | `slots_init_test.go` | `slots` | A share wider than the bench can never be met. |
| `TestUsageNamesForceAndTheKeepOnSlotsRelease` | `slots_release_force_usage_test.go` | `slots` | THE FLAG THE HELP DID NOT HAVE (#1902, Johnny's hold on PR #1943).  |
| `TestTriageAcceptsUsageFlag` | `swarm_test.go` | `triage` | triage accepts --usage and exits 0. |
| `TestVersionLineShape` | `version_test.go` | `version` | The SHAPE, asserted field by field. `nova-swarm <identity> <goos>/<goarch> <go ver... |
| `TestVersionLineHoldsWhateverTheStampContains` | `version_test.go` | `version` | The stamp is the ONE field of this line that comes from outside the toolchain, and... |
| `TestVersionIdentityIsTheStampWhenThereIsOne` | `version_test.go` | `version` | A stamped build says the tag and an unstamped one says what the toolchain recorded... |
| `TestVersionRefusesFlagsAndArguments` | `version_test.go` | `version` | Verifies version behavior and error contracts |
| `TestVersionVerbIsReachableFromTheDispatch` | `version_test.go` | `version` | The wiring, which lives in main.go's dispatch and is one line there: this test is ... |
| `TestWorkerCheckAGoodDescriptionPasses` | `workercheck_test.go` | `worker` | A description that names an executable harness, both placeholders, an existing wor... |
| `TestWorkerCheckAMissingHarnessNamesTheField` | `workercheck_test.go` | `worker` | A harness that is not there is one drift naming the harness field, exit 2. |
| `TestWorkerHelpMatchesOtherVerbs` | `workercheck_test.go` | `worker` | `worker check --help` is a help request, not an unknown flag: it answers the same ... |
| `TestWorkerCheckAnAbsentSecretWithEnvNamesTheVariableNotTheValue` | `workercheck_test.go` | `worker` | A secret the environment does not hold is named by its variable, and no value leaks. |
