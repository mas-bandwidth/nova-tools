# nova-swarm — specification

A pool of one-task workers — any provider, any model, through one
harness — each with its own working directory, its own data home, its own job directory
and its own deadline held by the machinery rather than by the worker.

Every rule it keeps is a failure from the record:

| the failure, from the record | the rule that closes it |
|---|---|
| six workers on one data home: `database is locked`, and five of six do nothing | a **slot**: its own working directory and its own data home per running worker |
| an API key in an argument is in the process table, and in a log, and in a transcript somebody pastes (**the leak that taught it**) | the key is **read as data from a file, never sourced, never an argument, never printed** — and the config file written for the harness carries the env var's *name*, never its value |
| a worker asked to loop ran until somebody noticed | the deadline is held by the **machinery**, outside the worker, and the worker is told its own deadline in its prompt |
| a worker read a file outside its job directory, the harness refused the read, and the worker treated the refusal as fatal | the sandbox rule is stated in the prompt: a refused read is **not** the end of the run |
| a task is given as one sentence and returns as a plan | task **templates** with the learned conditions baked in, and `RESULT.md` with only a plan is a **failed** task |
| 25 of 67 findings in batch 1 were duplicates of the owed list in the pull request body nobody read first | the `read-pr` template's first condition: **read the owed list first and mark duplicates** |
| findings fail when a rule is paraphrased from memory | **quote every rule verbatim with `file:line`** |
| a worker that died at the deadline had found things and written none of them | **append each finding the moment it exists**, never at the end |
| one worker read 40 files and finished nothing | a **token and file budget** in the task |
| a result file is rewritten while a reader inspects it | a report is **published by rename**: whole revisions, `RESULT.md.tmp` renamed over `RESULT.md`; the tool reads only the renamed file, identifies a revision by its content hash, and never by an mtime |
| a bounded review that finds nothing counts as a plan, so a worker is rewarded for finding something | completion evidence is the head's `findings: <n>` line, separate from the count: `findings: 0` is **`clean`**, a report with no head is `plan-only` |

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
- Linux: `~/sdk`, `~/go/pkg/mod`.

## The living verbs

The tool exposes ten living verbs, dispatched directly from `cmd/nova-swarm/main.go`:

```
usage:
  nova-swarm version    print this build identity (--version also accepted)
  nova-swarm doctor    [--path <file>] [--local <file>]   refuse a launch under a shadowed nova-swarm (PATH vs ~/.local/bin build stamp)
  nova-swarm verify    --result <file> --contract <line> --label <text> [--card <file>] [--max <n>] [--run-record <file>] [--usage <file>]
  nova-swarm lint      --card <file> [--typed] [--child-rules] [--trust <file>] [--lineup <file>] [--max <n>] | --fleet <file> [--max <n>] | --rules
                       (--fleet lints a launcher script against the coordinator's /bin/bash 3.2: shebang, bash-4 builtins, unquoted expansions)
                       (--child-rules holds the card to every rule the coordinator gives a child: one rule-<name> per required sentence, one step-<what> per forbidden command; template --name card prints a card that passes)
  nova-swarm template  --name read-pr|probe-row|fix-card|result|worker|setup|capacity|card|read|fix|text|replay|drift|tone|models.tsv
  nova-swarm profile   --jobs <glob>   (one PROFILE line per job's timeline.tsv and one mean summary)
  nova-swarm native    --harness <path> --model <provider/model> --card <file> --slot <dir> --root <dir> --deadline <duration> --tokens <n>|unmetered [--label <text>] [--idle <duration>] [--auth <file>] [--config <file>] [--worker <file>] [--results-root <dir>] [--sweep-now] [--frame <file>]
  nova-swarm member    --as <name> --harness <path> --root <dir> [--width <n>] [--model <provider/model>] [--deadline <duration>] [--tokens <n>|unmetered] [--sprint <nova-sprint>] [--reader] [--every <duration>] [--once | --ticks <n>] [--auth <file>] [--config <file>] [--worker <file>] [--no-wall] [--gh <path>] [--pass <NAME,...>]
                       (this machine as one member of a sprint's fleet: beat, queue, push and finish what ended (the child's commit to origin's sprint branch, from outside the wall, never forced; the pull request the child's gh pr create asked for, opened with --gh), each finish judged ok, failed or reaped (docs/SPEC-CARD-CONTRACT.md), take to the width its fleet row names (read with its queue every tick; --width is a reader's, or a twin's override), each card one native child with its frame and an allowlist environment, on the model, budget and deadline its packet's route names (the card decides: the deal draws a route of its tier, or its model: pin; --model, --tokens and --deadline are the override a card with no route runs on); --pass names the secrets a child is handed, the loop record's nova-secrets keys: a loop whose harness reads its provider key from the environment carries --pass <KEY>, else its children start without it and fail at the provider; --reader runs the readers-table loop; the store is nova-sprint's, from NOVA_SPRINT_REDIS)
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

1. **`version`**: Prints build identity: `nova-swarm <identity> <os>/<arch> <go-version>`. Accepts `--version`.
2. **`doctor`**: Compares `PATH` binary stamp against local build stamp (`~/.local/bin/nova-swarm`). Refuses launch if shadowed or if a compared binary cannot be read; see "The doctor".
3. **`verify`**: Mechanically verifies `RESULT.md` line 1 against the contract line, checks against failure signatures, and writes a `.receipt` file.
4. **`lint`**: Validates card mechanical structure before any spend, validates fleet scripts against bash 3.2, or displays linting rules. Under `--child-rules` the card is also held to every rule the coordinator gives a child: one `rule-<name>` per required sentence and one `step-<what>` per forbidden command (`internal/swarm/lintchild.go`), the rules `nova-sprint add` holds every brief to.
5. **`template`**: Prints standard templates (`read-pr`, `probe-row`, `fix-card`, `worker`, etc.) verbatim without escaping. `card` is a whole card: the contract line, the RULES paragraph with every child rule quoted, and the steps; it lints clean as printed.
6. **`profile`**: Aggregates per-turn timeline TSV files into execution phase durations.
7. **`native`**: Executes a single card through the harness under sandbox containment with external deadline, idle timer, and token tracking. Takes job and slot directory leases (`.lease`, `.slot-lease`). The harness leads its own process group from its start. When it exits on its own, native ends whatever it left in that group (a language server, a shell's `&`) before it prints its line: a terminate, `swarm.TerminateGrace` (3 s), then a kill; the line names the group, `survivors=<pgid>:reaped`, or `survivors=<pgid>:alive` when something outlived the kill. A leftover holding the harness's pipes costs at most `subproc.WaitDelay` (5 s): a harness that exited 0 while its pipes stayed open keeps `rc=0`.
8. **`member`**: This machine as one member of a sprint's fleet: it beats, pushes and finishes the cards whose children ended, takes from the fleet table to its width, and runs each card as one `native` child. The width is the member's fleet row's (`fleet up <m> --width n`, `fleet sync` from nova-config's slots), read with its queue every tick (`queue --json` carries `width`), so a row lowered mid-run takes nothing new until the running fall under it; `--width` is an override, a reader's or a twin's, and the start line says `width=row` or `width=override:<n>`. The card decides its model (docs/SPEC-SPRINT.md, the deal's route): each work packet carries the route the deal drew for it, `route`, `model` (provider/model), `tokens` and `deadline`, and the member launches native with exactly those, the profile chosen from that model id as before; `--model`, `--tokens` and `--deadline` are the override a card with no route runs on (a store with no route: a twin, one machine), printed `model=card,override:<id>` on the start line, and a card with neither is refused at its launch naming both ways to give it, and reported at once as a failed finish (`launch refused: <why>`) so the store never holds it working with nothing running. The `start` and `finish` lines name `route=<r> model=<m>`; a finish carries `--usage` (the child's wall and budget word from its NATIVE line), and a failed finish's reason begins with how the run ended when native says so: `provider failure`, `budget`, `deadline`. `--reader` runs the readers-table loop; `--reader` runs the readers-table loop (a read pushes nothing). Each launch is framed (docs/SPEC-CARD-CONTRACT.md): the member writes `<slot>.frame.json` from the packet and hands it to `native --frame`, which stages the frame's commit on the frame's branch, writes `JOB.md` into the job directory and puts the shims of the model family's profile first on the child's `PATH` (a `git push` is recorded in the job, a clone of the card's repository is the staged checkout, `gh pr create` records the finish in `<job>/.sprint/finish.md` and `gh pr review` a read's verdict); native records the commit it staged in `<slot>/staged`, and the member starts it with an allowlist environment: the names native, git and the harness need, and of the secrets only those `--pass` names (the loop record's nova-secrets keys) and the worker description's (docs/SPEC-CARD-CONTRACT.md); the prompt begins `Read <job>/JOB.md first.` The push at a work card's finish: when the child's result names a commit (`head: <sha>`, else the last push the git shim recorded) that has a commit the staged commit does not (counted in the member's own repository, never from the checkout's refs), the member, outside the wall and with this machine's own credential (its ssh key or git credential helper; never a value read from a seat), pushes that commit to origin's branch the packet names (`sprint/<work card id>`), at the repository the card names, never forced and with no terminal prompt. It never runs git with the checkout's own configuration or hooks, which the child wrote: it fetches the checkout's branches into its own bare repository `<root>/push.git` and pushes from there. It fetches the checkout's `HEAD` and every branch, so a commit on any branch the child made is found. When the result carries a `title:` (the child ran `gh pr create`), the member then opens the pull request as itself with `--gh`, from that branch into the card's base, with the result's title and body. The finish is judged in one place, `member.Judge` (tla/CardContract.tla): ok only when the result has the contract's shape, its verdict is `ok` and the push landed; it then carries `--head <pushed sha> --branch <branch>` and a report that starts `pushed=<sha> to <branch>[ pr=<address>]: `. Anything else is a `--failed` finish naming no head and no branch, its report starting with the reason (`no RESULT.md shape`, `nothing to do: <why>`, `verdict <word>`, `no commit: <why>`, `push refused: <git's line>`), with a `NOTE finish <card> failed: <reason>` on the member's output; a push git refuses is also a `NOTE push <card> refused: <git's line>`. A launch whose claim moved, or whose card left the queue, is `reaped`: its end is printed and nothing is reported, whatever column the queue lists the card in (a redeal, and a withdrawn card dealt again, list it in the member's ready column at a later generation), and its place of the width is taken again in the same tick. The member waits on native's process alone: native's output goes to a file in the slot, never a pipe back to the member, so a native that has exited is finished within one tick whatever a process it left still holds. The merge finds the work as the card's head on origin's branch; the member opens no pull request.
9. **`slots`**: Bench slot lease broker (`init`, `take`, `release`, `list`) managing shared bench capacity.
10. **`worker`**: Validates worker description JSON structure, environment variables, and readable roots.

## Exit codes

| code | meaning |
|---|---|
| 0 | the verb ran and passed: a card executed, a receipt written |
| 1 | the verb ran and said **NO**: a `verify` whose contract line mismatched or whose run carries a failure signature, a `native` whose card was ended by its token budget or by a budget it could no longer verify |
| 2 | could not run: missing flag (`--tokens` on `native` and on `member`), a numeric `--tokens` on a `native` whose usage source is `none` or whose bench has no `sqlite3` on `PATH`, unreadable worker description, a key file that is absent or empty, bad invocation |

## Output grammar

```
NATIVE OK label=<id> job=<id> tmp=<path> rc=<n> wall=<n>s sandbox=<path|-> card_sha256=<sha> binary_sha256=<sha> config=<sha8|-> harness=<ok|silent> budget=<spent|n+|->/<n>|unmetered [fence=rejected path=<p>] [usage=none reason=<r> path=<p>] [reason=terminated] [stopped=<tokens|max_turns|max_cache_read|unverifiable>] [survivors=<pgid>:<reaped|alive>]
NATIVE INCOMPLETE label=<id> job=<id> tmp=<path> rc=<n> wall=<n>s sandbox=<path|-> card_sha256=<sha> binary_sha256=<sha> config=<sha8|-> harness=<ok|silent> budget=<spent|n+|->/<n>|unmetered [fence=rejected path=<p>] [usage=none reason=<r> path=<p>] [reason=terminated] [stopped=<tokens|max_turns|max_cache_read|unverifiable>] [survivors=<pgid>:<reaped|alive>] why=<harness-silent|no-result|rc>
NATIVE REFUSED: <reason>
SLOTS OK store=<dir> capacity=<n> share=<n>
SLOTS GRANTED store=<dir> owner=<owner> n=<n> ...
SLOTS REFUSED store=<dir> owner=<owner> ...
SLOTS RELEASED store=<dir> owner=<owner> ...
SLOTS KEPT store=<dir> owner=<owner> live=<n>
SLOTS LEASE store=<dir> slot=<n> owner=<owner> ...
DOCTOR OK stamp=<stamp>
DOCTOR OK nothing to compare: no nova-swarm on PATH and none under the local directory
DOCTOR DRIFT path=<binary> stamp=<stamp>
DOCTOR DRIFT local=<binary> stamp=<stamp>
DOCTOR REFUSED <path binary> shadows <local binary>; copy the ~/.local/bin binary over the PATH one, or fix PATH so ~/.local/bin comes first
DOCTOR UNREADABLE reading the version of <path|local>=<binary>: <cause>; <the other binary>; run `<binary> version` by hand and rebuild or remove the binary that does not answer, then launch again
PROFILE job=<path> ...
```

Every listing is a cap and a count: `--max`, default 20, `0` for all, one MORE line
naming the remedy. The counts describe the complete result set, never just the printed rows.

## The key, read as data

The key is read as data from a file or environment variable, never sourced, never an argument, and never printed.
It lives in one file the worker description names — mode `0600` — and is never logged.
The harness configuration written by the machinery carries the variable's NAME, never its value.

## The doctor

The doctor compares the `version` line of the `nova-swarm` first on PATH with the one at
`~/.local/bin/nova-swarm`, and `native` runs the same check before it starts
anything (`-h` never does). Each binary is asked for `version` under a 5-second deadline,
both at the same time; the first line it prints, at most 4096 bytes, is its stamp, and a
stamp is printed as a bounded, escaped excerpt.

| line | meaning | exit | next action |
|---|---|---|---|
| `DOCTOR OK stamp=<stamp>` | the two agree, or there is one binary to read | 0 | none |
| `DOCTOR OK nothing to compare: no nova-swarm on PATH and none under the local directory` | no binary was read | 0 | none |
| `DOCTOR DRIFT path=<binary> stamp=<stamp>` and `DOCTOR DRIFT local=<binary> stamp=<stamp>` | the two stamps differ; both are printed | 2 | see the next line |
| `DOCTOR REFUSED <path binary> shadows <local binary>; ...` | the PATH binary shadows the local one; the launch does not start | 2 | copy the `~/.local/bin` binary over the PATH one, or fix PATH so `~/.local/bin` comes first |
| `DOCTOR UNREADABLE reading the version of <path or local>=<binary>: <cause>; <the other binary>; ...` | a binary the check compares could not be read; the launch does not start | 2 | run `<binary> version` by hand, then rebuild or remove that binary, then launch again |

The cause is one of `timed out after <deadline>`, `exited <n>`, `was killed (<signal>)`
(a run ended by a signal), `printed nothing`, `printed a line longer than <n> bytes`,
`not found`, or the system's own words when the binary cannot be started, such as
`fork/exec <path>: permission denied` for a file that is not executable. The other binary
is described in one sentence: it reported a stamp, it could not be read either, it is not
installed, or there is no other binary. A stamp printed before a failure is still compared, so a stale binary that then
hangs is refused as shadowing and as unreadable.

When the check itself is the problem, the refusal's own next action is the way out: run the
named binary's `version` by hand to see what it does, then rebuild it or remove it.
Removing the copy under `~/.local/bin` is tolerated: with no local copy there is nothing to
shadow with, and the check passes on the PATH binary alone. No flag skips the check.

## Bench slot leases

A bench is bigger than its owners: one bench,
many owners, and the slots on it are one shared pool, not one pool per owner.
A bench carries ONE slot store shared by every owner, at <bench store>/slots,
and a lease on it is held through the broker verbs below.
The seven rules:

1. A bench carries ONE slot store shared by every owner, at <bench store>/slots,
   a directory of atomic mkdir leases each holding owner, pid, card label, until=.
2. `native` takes no bench capacity lease: it takes only job and slot directory
   leases (.lease, .slot-lease) and reads and writes no bench capacity store, and a
   lease on the store is held only through the broker verbs (rule 3).
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
- a schema card is refused at take when the remaining share fits only a read;
- a live-until lease whose pid is gone is stranded with its label.

A bench holds slot leases: the store is `<store>/slots` with one directory per lease made by `os.Mkdir` (atomic), each holding a file `lease` with lines `owner=`, `pid=`, `label=`, `until=<RFC3339>`, beside `<store>/shares.tsv` rows `capacity\t<n>`, `reserve\t<n>`, `<owner>\t<n>`. `nova-swarm slots take --store <dir> --owner <o> --n <k> --for <duration>` first reaps every lease whose `until=` is past AND whose pid is not alive — a lease past `until=` with a live pid is `DRIFT`, stays, and counts as held — then grants `k` leases iff the owner's held+demand stays within its share and the total held+demand stays within `capacity` minus `reserve`.

`nova-swarm slots list --store <dir>` prints one line per lease.

native takes directory leases (.lease, .slot-lease).

## The card is a pipeline, not a loop (issue #856)

Each model call receives its required context explicitly. In this repository
a card is a pipeline of stateless model calls, not an agent loop: there is
no memory between calls.
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
    receives.

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

The card is a measurement of the same
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
per bench**, and every job's clone is built from it: a per-job clone under the
job directory passes `--reference` off the bench's mirror (`~/nova-bench/mirror/<name>.git`) and then `--dissociate`, so the object graph
is read once and the per-job clone is small.

### The prompt text is the tool's

The prompts and their conditions are `internal/swarm/templates.go` in the
binary, printable, and versioned with the tool. So **the prompt text the workers run is the
tool's**: `Prompt` and `WrapTemplate` assemble it from the named template; the
shell scripts are prototypes. No tool's live state is a shell script's private variable.

### Red tests

The card earns the same red-first bar as every rule here: seen red before it
is trusted.

- a per-job clone built with `--reference` and `--dissociate` shares the reference checkout's object graph and still has its own working tree;
- the worker prompt carries the named template's conditions from the tool, with no shell script in the path.

## Efficiency: lessons absorbed

Measured on the live pool and the worker homes. Correctness is the
rest of this spec; this section records what `nova-swarm` costs the coordinator
and the bench, and the rules that bound that cost. Two operations are the
widest, and each has one rule.

| the operation, measured | the measurement | the rule that bounds it |
|---|---|---|
| **REPEATS: one full clone of the repository per job** | 21 clones, 307 MB and 36 s of wall clock for one object graph; 1,128,320 cache-read tokens over 13 tool calls, 86,794 cache-read tokens per tool call, with the clone and the `gh pr checkout` two of them — about 174K per job and 3.6M across the 21 | the reference clone |
| **WAITS ON: a deadline, a sampler, and a person** | a job's own clock is `--deadline` (40m in `deepseek.json`); the usage budget is read by the sampler at `--usage-interval` (default 5 s); a verdict waits on a person | one owner per wait, one line back |

1. **The reference clone.** A job's clone is its own and is taken with
   `git clone --reference-if-able <the shared on-disk checkout> --dissociate`,
   so the object graph is shared with a checkout already on the bench and the
   job directory holds only its own objects; `--dissociate` keeps the job's
   clone independent of a reference it does not own.
2. **One owner per wait, and one line back.** A job's own clock is the deadline
   held by the machinery; the usage budget is read by the sampler at
   `--usage-interval` and never by a second poll; and a verdict waits on a
   person, never on a scan.

## The rules, numbered

Every rule here is normative. Only living verbs are retained.

1. **The owed list first.** A `read-pr` task's prompt says: read the pull
   request body's owed list before any code, and skip what is owed. A finding
   already on that list is marked `dup:` and is not a new finding.
2. **Every claim quotes its rule verbatim, beside the line.** A finding line
   carries the rule it rests on, quoted word for word, with `file:line`, on the
   same line or the next. A finding with no quote is counted `unquoted`.
3. **Append as found, never at the end.** The prompt says: append each finding
   to the report file the moment it exists. A run killed at its deadline keeps
   its partial report on disk.
4. **A refused read does not end the run.** The worker's prompt says so in one
   sentence. The harness refuses a read or a scratch write outside the job
   directory, and the worker continues with what is inside. `native` runs
   inside the sandbox wall and logs refusals to `harness.log`.
5. **The key file is data.** The key lives in one file, `~/.config/<provider>/env`
   or the path the worker description names. It is read as data, never sourced,
   never on a command line, never in a log, never in a file this tool writes.
6. **One clone per job; a written deadline.** Each job has its own clone under
   its own job directory, never shared. Each job carries a deadline held outside the worker.
7. **Results are verified per card, and completion is evidence separate from the finding count.**
   `verify` checks each `RESULT.md` against its contract line and writes a `.receipt`.
   Evidence of completion is the report's `## Head` with `findings: <n>`; `findings: 0`
   is `clean` (a complete review that found nothing); `plan-only` is a `RESULT.md` with no head;
   `no-result` is no report at all.
8. **N workers are N processes.** Each worker has its own job directory and its
   own report file. No file is written by two workers.
9. **A job is one blocking process group, reported once.** The worker runs in its
   own process group. If the group leaves background survivors, the result is quarantined.
10. **Usage is written to a file.** Each job writes its usage record with timestamps,
    token counts, and exit code. `native` records usage in the slot and passes it to verification.
11. **The swarm's own tokens are budgeted per job.** Every job carries `--tokens <n>`
    or `--tokens unmetered`. A worker running under a budget is stopped when the budget is spent.
12. **Malformed reports are refused.** `verify` checks `RESULT.md` against its contract line,
    and malformed reports or missing lines are refused rather than accepted as verified evidence.
13. **Publication by rename.** Reports are published whole by renaming `.tmp` over `RESULT.md`.
    The runner and verifier read only published reports to ensure no partially written revision is processed.

## Card lint

`nova-swarm lint --card <file>` checks a card's shape before any spend. `--child-rules` also
holds the card to the rules the coordinator gives a child, and `nova-sprint add` holds every
brief (`--brief` and `--brief-file`) to them before it writes anything, exit 2; a card with
no brief (a `--count` card, an id card) and a sentinel carry none to check. `nova-swarm
template --name card` prints a card with every general rule in place, which lints clean as
printed. `nova-swarm lint --rules` prints every token below with its remedy. A card written
for a bench worker under the shape rules carries none of the child rules and is linted without
the flag exactly as before.

**The rule set is the coordinator's, and the tool is general.** The required sentences are not
one repository's. They come from a rules file, one required sentence per line:

- `nova-sprint add --rules <file>` holds that add's brief to the file;
- `nova-sprint init --rules <file>` records the file's absolute path for the sprint (the key
  `sprint:rules`, kept by a clear and removed by teardown), and every later `add` without
  `--rules` reads it;
- `nova-swarm lint --card <card> --child-rules-file <file>` holds a card to the file, and
  implies `--child-rules`;
- with no file the set is the built-in general rules, below.

A rules file line is a sentence; a blank line and a line starting `#` are skipped. A line may
open with `[name]`, the token its finding carries (`rule-<name>`, kebab case); a line with no
name is `line<n>` after its line number. A repeated name, a repeated sentence, a bad name, a
`[name]` with no sentence, and a file with no rule at all are each refused with the line, and
every problem of a file is reported in one run. A card quotes each sentence verbatim (blanks and
line breaks folded), conventionally in a `RULES.` paragraph; a card without one draws
`rule-<name>` at line 1 with the sentence in the excerpt, and the remedy quotes the sentence and
the file and line it came from. This repository's own file is `fleet/child-rules.txt`, cited by
its sprint's init: `nova-sprint init --rules fleet/child-rules.txt`; it holds the repository's
rules (its Go caches, its `-timeout`, `t.Parallel()`, the class tests, the commit trailer, the
container tier) and is configuration, not the tool.

The built-in general rules are the rows of `DefaultChildRules` in
`internal/swarm/lintchild.go`, and only the rules that hold for any project:

| rule token | the sentence the card quotes |
| --- | --- |
| `rule-worktree` | Work only in the job directory this card names. |
| `rule-no-force-push` | Never force-push or rebase a shared branch. |
| `rule-no-kill` | Never kill a process you did not start. |
| `rule-no-server` | Never start a server on this machine. |
| `rule-no-rm-rf` | No `rm -rf` outside the job directory. |
| `rule-report-not-done` | Report what was not done. |

Two kinds of check read the card's text:

- **Presence.** One `rule-<name>` token per required sentence, as above.
- **Scan.** One `step-<what>` token per forbidden command, read off every line outside the
  `RULES.` paragraph, which runs from its `RULES` line to the first blank line. A clause (the
  text back to the last `;`, `,`, `&&`, `||` or `. `) that says `never`, `not`, `no` or
  `without` before the command is prose about the rule and is not read as a command. The
  scans for a redis-server, `kill`, a recursive `rm`, a force-push, a rebase, `git stash` and
  `gh pr merge` are built in and run over every card. The scans for `go clean` and for a `go
  test` with no `-timeout` of a positive duration are a Go project's and run only where the
  rule set carries a rule named `no-go-clean` or `go-test-timeout`.

| scan token | what it wants |
| --- | --- |
| `step-redis-server` | no line starts a redis-server: a server a child starts belongs to nobody who will stop it, and the machine's own servers belong to whoever runs them; a test that needs one runs where the card says, never by starting it here |
| `step-go-clean` | no line runs `go clean`: a cache clean breaks every build that shares the cache; give the child a private GOCACHE (a path of its own) and let it be (runs with a rule named `no-go-clean`) |
| `step-kill` | no line kills a process: a child stops only a process it started itself, and says so as `kill $!` or `kill %<n>`; `pkill` and `killall` name processes by pattern and reach another child's |
| `step-rm-rf` | a recursive `rm` names a path inside the job: a relative path without `..`, or one under `$PWD` or `<job>`; never `/`, `~`, `$HOME`, `.`, `*` or a path above the job, and never a variable the card cannot show the value of |
| `step-force-push` | no line force-pushes (`--force`, `--force-with-lease`, `-f`, a `+` refspec): a child pushes its own branch with a plain `git push`, and a rewrite of a shared branch is the coordinator's act alone |
| `step-rebase` | no line rebases: merge the base forward with `git merge --no-edit`; a rebase rewrites the history another worktree shares |
| `step-stash` | no line stashes: the stash list is shared by every worktree of the repository, so a stash taken here is popped there; commit to the child's own branch instead |
| `step-merge` | no line merges a pull request: the child opens it against the base the card names and stops; the coordinator lands it |
| `step-go-test-timeout` | every `go test` carries `-timeout 600s` on the same command, so a hung test ends at ten minutes and not at the card's deadline (runs with a rule named `go-test-timeout`) |

## Test inventory

List the current unit tests with `go test -list . ./cmd/nova-swarm/`.
