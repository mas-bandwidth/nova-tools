# nova-merge — specification

`nova-merge` is one binary at the **merge layer**. It lands an **ordered lane**
of entries — pull requests, or branches with no pull request at all — onto one
base branch, one at a time, and it refuses to land anything whose evidence it
cannot name.

This spec is normative. If the code and this document disagree, one of them has
a bug, and the tests decide which. It stands beside
[SPEC.md](../SPEC.md), whose **Conventions** section — exit codes, no guessed
paths, the one-line output grammar, the cap-and-count rule, `internal/oneline`
and `internal/bounded` — applies here unchanged and is not restated. Where this
tool needs something the Conventions do not cover, it is below and it says so.

A lane in this shape landed 30-odd pull requests onto one base in a single
morning. The form works, and it fails in every way a shell loop around
`gh pr merge` fails. This tool is those failures closed, one rule each.

| the failure, from one morning's record | the rule that closes it |
|---|---|
| `gh pr merge --auto` does not queue here — it merges immediately, and #922 went in red (Glenn, 2026-09-11: **never `--auto`**) | `--auto` is **refused structurally**, in the one function that runs a mutating command, before the command is built |
| a merge landed while one check was still pending, so nothing had read the job that later went red | the merge condition counts **fail and pending separately** and requires zero of both |
| a read was recorded and the author had pushed since, so the approve was for code nobody had | a read and a gate are **keyed to the entry's head**, and a stale one does not count |
| a force-push would have made the base unreproducible | `--force`, `-f` and `--force-with-lease` are refused in the same place as `--auto` |
| a pull request whose base was not the lane's base was merged into the wrong branch | the base is **read back from the host** every pass and a mismatch stops that entry |
| re-merging the base into every entry after every merge restarted every CI run: 26 entries × 70 jobs, queued at once (**2026-09-11 12:40Z, the storm**) | the base is re-merged **only into entries that CONFLICT with it** |
| four conflicts in one morning, all real, all on one Java file the tip had changed; a mechanical resolver would have written a merge nobody read (**2026-09-11**) | the lane **never edits an entry's content**; a conflict is `BLOCKED` with its file list and a hand resolves it |
| the hosted lane took 20 minutes to say what our own hardware says in two (Glenn's **two-minute rule**) | a **local gate** for the entry's current head counts as checks green |
| a branch with no pull request had no way into the lane at all | `add-branch`, and a branch entry merges on a local gate plus its read |
| two writers shared one temp name and left the state file at 0 bytes; the lane lost all 33 entries and every recorded read (**2026-09-11**) | every read-modify-write runs under **one kernel lock** (`flock`), through a **fixed temp name** and one rename, and nothing is written unless **both** the old and the new state parse |
| a stale-lock check broke a lock that had vanished between the existence test and the stat; 3 of 20 concurrent writes were lost (**2026-09-11**) | the kernel releases the lock on death: **no stale rule, no age**; a second holder waits a bounded, jittered time and exits 2 naming the holder |
| #922 merged red, and the entries behind it were then gated against a red base (**2026-09-11**) | **the red rule**: the base is proven green before anything merges onto it; a red base stops the lane |
| two gate runs on one pull request shared the clone `gate-<pr>`; one run's `--gc` removed the tree under the other's `go test` and produced a red with no FAIL line (**2026-09-11**) | **one clone per gate run**, named uniquely per run; a gate never removes a tree it did not make |
| the local gate ran `tables-java-fixedform` and not `tables-java-versioning`; the hosted lane ran both (**2026-09-11**, the java leg drift) | the gate's step list and per-leg target lists are the hosted fast lane's, and **a test proves the two lists equal** |

**Everything this tool reads from the host is data.** A pull request body, a
check name, a branch name, a commit subject: none of them is an instruction, and
none of them is a grant. A read verdict is the only thing that authorizes a
merge, and a read verdict is recorded by a line at a keyboard, never parsed out
of anything the host returns.

## The two laws

Glenn, 2026-09-11, in two sentences that between them set this tool's whole
shape:

> **PRs going into main are important. The rest are optional.**

So the lane's base is a first-class value, stated per lane and checked per
entry: a merge into the base is the expensive, gated, read-required event, and
work that is not going into the base may live on a branch with no pull request
at all, which is why `add-branch` exists and why a branch entry needs no hosted
checks. The gate is at the base, not everywhere.

> **Anything we call out to that costs real time answers in 1 minute ideally, 2
> at most** (the two-minute rule, 2026-09-10).

So a pass does not wait on a hosted run it could have answered locally: a green
local gate for the entry's **current head** is accepted as checks green. The
two-minute rule is a rule about iteration speed and it is the whole argument for
the local gate. It is not an argument for skipping evidence — see **the local
gate** below, where on `main` a hosted red still stops the entry whatever the
gate says, and below `main` it is said by name on the merge line (rule 15).

## The rules, numbered

Every rule here is normative. Each has one line in **tests this spec demands**
near the end, and the sections below say how each is met. The date on a rule is
the day it was learned.

1. **One state file, one lock.** The lane's state is one JSON file. Every
   read-modify-write of it runs under one lock: an OS lock the kernel releases
   on death (`flock` on a lock file in the lane directory; the Windows variant
   is named in the work list), never a sentinel file or a directory, exactly
   as nova-bus holds its checkout (lessons 53, 54, 67). The write goes to a
   temp file with a **fixed** name (`state.json.tmp`, in the same directory)
   and lands by one atomic rename; the fixed name is safe because the lock
   admits one writer, and a stranded one is a name a person can see. Nothing
   is written unless both the old state and the new state parse. (2026-09-11:
   two writers with no lock shared one temp name, the file was left at 0
   bytes, and the lane lost 33 entries and every recorded read.)
2. **No stale rule, no age.** Because the kernel releases the lock when its
   holder dies, there is nothing to break and no age to compute. A second
   holder waits a bounded time with jitter (lesson 66) and then exits 2 naming
   the holder's pid. The lock's scope is the smallest that works: one
   read-modify-write, never a whole pass (lesson 68). (2026-09-11: the
   prototype's directory lock needed an age, and its "vanished means stale"
   arithmetic broke another writer's live lock and lost 3 of 20 writes; that
   whole class of bug is what the kernel lock removes.)
3. **Re-merge only what conflicts.** After a merge, the base is re-merged into
   an entry only when the host reports that entry `CONFLICTING`. Never into
   every entry after every merge. (2026-09-11 12:40Z: 26 entries times 70 jobs
   queued at once, and the runner pool was jammed for an hour.)
4. **Never `--auto`, never `--force`, never a force-push.** The refusal lives in
   the one function that runs a mutating command, before the command is built,
   and there is no other path to a mutation. (2026-09-11: `gh pr merge --auto`
   merges at once on this host, and #922 went in red.)
5. **The merge evidence is one of two things, and both are keyed to the head.**
   An entry merges on zero failing and zero pending hosted checks with at least
   one pass, or on a green local gate recorded for exactly the entry's current
   head sha. A gate for an older head is nothing. Which of the two is the
   verdict depends on the base (rule 15): on `main` a hosted red stops the
   entry whatever the gate says.
6. **The red rule.** The base is proven green before anything merges onto it. A
   red base stops the lane: no entry merges onto it, and nothing is piled onto
   a red. The only exception is a temporary red a person has named and planned
   on the way to green, declared with `run --planned-red <text>`, printed on
   every `RUN PASS`, and logged. (Glenn, 2026-09-11.)
7. **The lane never edits an entry's content.** A conflict the host reports
   makes the entry `BLOCKED` with the list of conflicting files. A hand resolves
   it: a child, one clone. The lane does no mechanical resolve of any kind.
   (2026-09-11: four conflicts, all real, all on one Java file the tip had
   changed.)
8. **Gates run wide; the merge is the only serial step.** One clone per entry
   per gate run, named uniquely per run. Two gate runs on one entry never share
   a tree. A gate never removes a tree it did not make. Parallel is the
   default. Serial is only where parallel cannot work, and those places are
   named: one base, one writer of the state, one merge per pass. The merge
   takes seconds. (Amdahl for coordination, Glenn 2026-09-11; the `gate-<pr>`
   false red.)
9. **Local-gate parity.** The local gate runs exactly the hosted fast lane's
   steps and per-leg make targets. A test reads both lists and proves them
   equal. A step that runs a smaller or a different target list is a contract
   violation. (2026-09-11: the java leg ran `tables-java-fixedform` locally and
   not `tables-java-versioning`.)
10. **Placement.** A pull request into `main` gets hosted CI. Below `main`, a
    branch, a local gate and a read are enough. Hosted CI runs only on `main`
    and nightly. The lane supports both pull-request entries and branch
    entries. (Glenn, 2026-09-11.)
11. **Reads live in the state, and the state says how many.** `needs_read` is
    per entry. A read is an approve or a hold, recorded by name. A hold blocks.
    Every read is in the state file, so losing the state loses the reads, and
    rule 1 is what protects them. `STATUS ENTRY` prints the count of approves
    and holds each entry holds, and `STATUS OK` prints the total.
12. **Bounded output.** `status` and `run` print counts and one remedy line.
    No listing grows with the lane: every listing is capped at `--max`,
    default 20. The bound is measured at the largest plausible state, 50
    entries, in lines and bytes.
13. **Every loop ends on its own.** `run --loop` requires `--hours`. The tool
    never matches a process by its own command line. It never touches `/tmp`.
    The state, the clone and the gate summaries live only under paths given by
    flags.
14. **The gate takes one of N machine-wide slots.** The gate runner holds
    `--slots <n>`, the number of gates this machine runs at once, and
    `--slots-dir <dir>`, where the slots live. Neither has a default. A slot
    is one lock file, `<slots-dir>/<k>`, held by a kernel lock exactly as
    rule 1 holds the state: a slot whose holder died is free at once, with no
    age and nothing to break. A gate that finds every slot held waits a
    bounded, jittered time (rule 2), prints one line while it waits, `GATE
    WAIT slots=<n>/<n> waited=<d>`, and exits 2 naming the holders if the wait
    runs out. The leg fan-out inside one gate is capped by `--legs <n>`, no
    default, so one gate cannot spend the whole budget on its own. The
    machine's budget is the tool's to hold, because no caller can see the
    other callers. (2026-09-11: every caller fanned out, load reached 235 and
    then 422 on 32 cores, and green tests became 30 second timeouts.)
15. **Below `main` the local gate is the verdict; on `main` the hosted lane
    is.** When the lane's base is not `main`, a green local gate recorded for
    the entry's current head is the merge evidence, and a hosted red for that
    head is printed by name on the `MERGE OK` line, `hosted_red=<names>`, and
    in the log: never blocking, never silent. When the base is `main`, the
    hosted lane is the verdict, zero fail and zero pending, and a local gate
    only lifts `PENDING`; a hosted red stops the entry whatever the gate says.
    This is rule 10 applied to the verdict and not only to placement.
    (2026-09-11: eleven gate-green pull requests below `main` sat behind a
    hosted red the hosted lane had caused itself.)
16. **`run` prints its build id and steps aside for a newer binary.** Every
    `RUN PASS` line carries `build=<id>`, the id compiled into the running
    binary. At the end of every pass under `--loop`, the tool reads the build
    id of the binary at its own path on disk; when the two differ, it finishes
    the pass, prints one line naming both ids, `RUN NEWER build=<id>
    on_disk=<id>`, and exits 0. It never loads the new code itself and it
    never runs the old code past the pass boundary. A restart is a person's
    explicit act, never a silent one. (2026-09-11: the loop kept running old
    code after the script was fixed, and nobody could tell from the log which
    code a pass had run.)
17. **A stop names its next step, and the evidence outlives the clone.** A
    `BLOCKED` entry prints one `RUN BLOCKED` line carrying the exact hand
    command: the clone, the fetch, the merge, the conflicting files and the
    push, in the order a hand runs them, and the entry stays `BLOCKED` until a
    new head arrives; no pass retries it. Gate logs live under the lane,
    `<lane>/gates/<entry>/<head>/<step>.log`, one file per step, and they
    outlive the gate's clone: `--gc` removes trees and never logs. Every gate
    step declares its failure marker, a regular expression, and the summary
    quotes the first line that matches it; a step with no marker says `no
    marker, see <log path>`. Never a heuristic over free text. (2026-09-11:
    four Java conflicts each cost a child an afternoon working out the next
    step; #948's "first failing line" was a status line, and the real log had
    been removed with the clone.)

## The verbs

```
nova-merge add        --lane <dir> --pr <n> [--needs-read]
nova-merge add-branch --lane <dir> --branch <name> [--needs-read]
nova-merge read       --lane <dir> (--pr <n>|--branch <name>) --who <name> --verdict approve|hold [--note <text>]
nova-merge gate       --lane <dir> (--pr <n>|--branch <name>) --head <sha> --verdict green|red --summary <path>
nova-merge run        --lane <dir> (--once | --loop <duration> --hours <h>) [--local-gates] [--planned-red <text>] [--max <n>]
nova-merge status     --lane <dir> [--max <n>]
nova-merge stop       --lane <dir>
nova-merge dry-run    --lane <dir> [--local-gates] [--max <n>]

every verb takes [--lane <dir>]; every verb that runs git or gh also takes
[--timeout <seconds>], default 120
```

The binary is `nova-merge`, and that is its only name.

**No guessed anything, with one named exception.** There is no default lane
directory, no default repository, no default base. A missing one is exit 2 and
`refusing to guess`. The exception is `--timeout`, which defaults to **120
seconds**, for the reason SPEC.md gives for `nova-bus --git-timeout`: a
subprocess timeout is how long this tool waits before saying so, not a fact
about a lane that only its owner can supply. `run --loop` gets no default
interval and `run --hours` no default deadline for the opposite reason: a loop
with no deadline is a lane that is stuck rather than working, and nobody outside
can tell the two apart (Glenn, 2026-09-09: **every ask, child or read has a
written deadline and a default action; never wait forever**).

The repository and the base are properties of the **lane**, written into its
state at creation by the first `add` or `add-branch` and never overridable by a
flag afterwards. A `--base` flag would let two invocations disagree about where
the lane lands, which is the same failure the fixed roster path closes for
`nova-bus`.

**`dry-run` is a verb, not a flag.** In the prototype it is a global `--dry-run`
that any verb accepts, including the mutating ones, and the cost of that is a
mode a caller can leave on or off by accident on the one command that merges.
Here the survey is its own verb with its own name: it performs every read `run`
performs, prints the whole plan rather than stopping at the first merge, and
**cannot write**. Nothing in `dry-run`'s code path can reach the mutating
helper at all, which is a property a test can pin and a flag never is.

`status` and `dry-run` **report** and exit 0 whatever the lane holds. `run` is
the verb that acts, and `run`'s exit code is about the pass, not about the lane:
see **exit codes**.

## Exit codes

| code | meaning |
|------|---------|
| 0 | the verb ran and passed: an entry added, a read or gate recorded, a pass completed with nothing refused |
| 1 | the verb ran and said **NO**: a merge that could not be landed, an entry STOPPED, a `run` whose pass ended with at least one BLOCKED entry |
| 2 | could not run: missing flag, unreadable lane state, a lane directory that is not one, bad invocation, `gh` or `git` absent |

**An entry that is merely waiting is not a failure.** Zero pending checks is
not the same news as a red check, and a lane full of entries waiting on CI is a
lane working exactly as intended. `run` exits 0 with `waiting=<n>` and exits 1
only when something was **refused, stopped or blocked** — a red, a wrong base, a
hold, a conflict, a red base, a push that would not land. A
lane that exits 1 on every pass while CI runs is a lane whose caller stops
reading the exit code, and the prototype had exactly that shape.

A `run` that exits 1 **after** merging says so on its `MERGE OK` line and in
its refusal: the merge is on the base, and the entry behind it is blocked.

## Output grammar

One machine-scannable line per event; first token names the verb, second token
is `OK`, `FAIL` or one of the informational tokens listed here. `OK` lines and
informational lines go to stdout; `FAIL` lines and refusals go to stderr. Every
path, branch name, check name, commit subject and reason renders through
`internal/oneline`, so a branch named with a U+2028 in it produces one escaped
line rather than two.

```
ADD OK kind=<pr|branch> entry=<n-or-name> needs_read=<yes|no> lane=<prs>/<branches>
ADD NOTE <entry> is already in the lane (needs_read=<yes|no>)
ADD REFUSED: <reason>
READ OK entry=<n-or-name> who=<name> verdict=<approve|hold> head=<sha|-> approvals=<n> holds=<n>
READ REFUSED: <reason>
GATE OK entry=<n-or-name> head=<sha12> verdict=<green|red> summary=<path> in_lane=<true|false>
GATE REFUSED: <reason>
RUN PASS n=<k> at=<stamp> build=<id> local_gates=<true|false> planned_red=<text|->
RUN NEWER build=<id> on_disk=<id>: the binary changed; this loop ends after this pass; restart it by hand
RUN BASE base=<branch> head=<sha12> checks=g<n>/p<n>/r<n> gate=<green|-> state=<GREEN|RED|PENDING|PLANNED-RED>
RUN STOPPED base=<branch>: the base is red (<n> failing); nothing merges onto a red base
RUN ENTRY entry=<n-or-name> head=<sha12> checks=g<n>/p<n>/r<n> read=<n>a/<n>h gate=<green|-> state=<STATE>
RUN STOPPED entry=<n-or-name>: <reason>
RUN BLOCKED entry=<n-or-name> head=<sha12> files=<n>: <the hand command, clone to push>
RUN REMERGE entry=<n-or-name> base=<branch> result=<clean|blocked> pushed=<true|false> files=<n>
MERGE OK entry=<n-or-name> base=<branch> basis=<hosted|gate> gate=<path|-> read=<who,who|none-required> hosted_red=<names|->
MERGE FAIL entry=<n-or-name>: <reason>
RUN OK lane=<n> merged=<n> dropped=<n> blocked=<n> waiting=<n>
RUN MORE kind=<entry> shown=<n> total=<t> nova-merge status --lane <dir> --max 0
RUN NOTE <the one remedy line>
RUN REFUSED: <reason>
STATUS ENTRY kind=<pr|branch> entry=<n-or-name> head=<sha12> checks=g<n>/p<n>/r<n> read=<n>a/<n>h gate=<green|-> state=<STATE> last=<stamp>
STATUS OK prs=<n> branches=<n> base=<branch> base_state=<GREEN|RED|PENDING|PLANNED-RED> ready=<n> blocked=<n> waiting=<n> reads=<n>a/<n>h
DRY PLAN pos=<k> entry=<n-or-name> basis=<hosted|gate> read=<n>a/<n>h
DRY OK surveyed=<n> would_merge=<n-or-name|-> stopped=<n> waiting=<n>
STOP OK lane=<dir>
```

`RUN PASS` is the first line of every pass and it says what the pass will
**count as green** before it says what it found: `local_gates=true` accepted
local gates, `local_gates=false` read hosted checks only. A listing that does
not say what it looked at is a listing a reader will mistake for everything.
`planned_red=<text>` is the one exception to the red rule (rule 6), printed
where a reader of the log sees it on every pass it applied to.

`RUN BASE` is the second line of every pass. It is the base's own evidence,
read the same way an entry's is: the check buckets of the base's head commit,
or a green gate recorded for the base branch at that head. A `RED` base stops
the pass before any entry is read, with `RUN STOPPED base=…`. A `PENDING` base
(no checks and no gate for its head, which is every base below `main` right
after a merge) waits, and `RUN NOTE` names the gate run that proves it.
`STATUS OK` carries the same verdict as `base_state`, so a lane can be read
without a pass.

`STATUS OK reads=<n>a/<n>h` is the total of approves and holds across the lane
(rule 11). It is a count of what the state file holds, so a lane whose state
was lost and rebuilt shows `reads=0a/0h`, and a reader knows the reads are
gone rather than assuming they are somewhere else.

**Every listing is a cap and a count**, per SPEC.md: `run` and `status` take
`--max <n>`, default 20, `0` for all, and print one `RUN MORE` / `STATUS MORE`
line naming the remedy when they stop early. The counts on `RUN OK` and
`STATUS OK` are the truth about the **lane**, never about the output: the
listing is capped, the counting never is. A lane of 33 entries printed 33 lines
per pass in the prototype, every five minutes, into a context window that had no
way to refuse them — and **Glenn, 2026-09-09: bounded output by design; counts
not lists; one remedy line.**

**`RUN NOTE` is exactly one remedy line per pass, and it names the next
command.** Not a paragraph, not one per entry. If anything is blocked it is
`nova-merge status --lane <dir>` with the sentence that a RED entry needs its
failing job read and a BLOCKED one needs a hand; if nothing is, it is the `run
--loop` that keeps watching. A cap with no remedy is censorship; a cap with one
is an index.

**A refusal prints every independent problem in one go**, one line each, per
this repo's guidance law: a `gate` invocation with a bad sha *and* a missing
summary says both, because a caller can fix two things as easily as one.

## The lane

A lane is a directory, named by `--lane`. It holds:

```
<lane>/state.json     the ordered entries, their reads and the recorded gates
<lane>/log            one append-only line per event, UTC-stamped
<lane>/repo/          this lane's own clone, never a working copy of anybody's
<lane>/gates/         the gate summaries, one file per entry and head
<lane>/gates/<entry>/<head>/<step>.log   one log per gate step, kept past --gc (rule 17)
<lane>/lock/          the lock, a directory; holds pid and stamp (rules 1 and 2)
<lane>/stop           present means: start nothing new and exit
```

Every one of these lives under `--lane`. Nothing this tool writes goes
anywhere else, and nothing goes under `/tmp` (rule 13).

The clone is the lane's own and the tool creates it. **It is never a checkout
somebody works in.** A lane that merged into a working copy would rewrite a
line's HEAD under them, and the prototype's clone is already separate for
exactly this reason; here it is a refusal: a `--lane` whose `repo/` is a git
work tree with a dirty index, or whose `repo/` resolves (after following
symlinks, per `nova-bus`'s `--bus` test) to any repository the tool did not
clone itself, is exit 2.

### The entries

An entry is a pull request or a branch, and the two lists are separate in the
state file on purpose — see **the state file**. Every entry carries:

| field | meaning |
|---|---|
| `pr` or `branch` | which kind it is; exactly one is present |
| `needs_read` | `yes` if a recorded read is required before it merges |
| `reads` | the recorded verdicts: `who`, `verdict`, `note`, `at`, `head` |
| `head` | the branch name (a PR) or the resolved `origin/<branch>` sha |
| `oid` | the head **commit**, which is what a read and a gate are keyed to |
| `state` | the last pass's verdict: one of the states below |
| `last` | when that verdict was reached |
| `detail` | the one-line reason, for a state that has one |

The states are `NEW`, `PENDING`, `NEEDS-READ`, `NEEDS-GATE`, `HOLD`, `RED`,
`WRONG-BASE`, `FORK`, `CONFLICTING`, `REMERGED`, `BLOCKED`, `MERGEABLE-GREEN`,
`UNKNOWN`. They are a closed set, and a pass that cannot place an entry in one
of them prints `RUN STOPPED … : <reason>` rather than inventing a fourteenth.

**The lane is ORDERED and the order is the order entries were added.** A pass
walks it from the front and merges **at most one** entry, then stops: the base
moved, so every entry behind it must be read again. A pass that merged two
entries would be merging the second against a base no check had seen.

## The merge condition

An entry merges when **both** halves hold. Neither is sufficient, and the
conjunction is the whole tool:

```
  (  hosted checks show ZERO fail and ZERO pending and at least one pass
  OR a GREEN local gate recorded for this entry's CURRENT head, with --local-gates )
AND the read condition
```

and, independently, all of these are true:

- the entry is **in the lane** (never anything else in the repository);
- its base, read back from the host this pass, **is the lane's base**;
- its head branch lives in the lane's own repository, not a fork the lane cannot
  push to;
- it does not currently conflict with the base.

**Zero fail and zero pending, counted separately.** `pass` and `skipping`
buckets count green; `pending` counts pending; `fail` and `cancel` count red. A
cancelled check is a red, not an absence: a run somebody cancelled is a run
nobody read. **Zero checks at all is not green** — it is `PENDING`, because a
pull request whose workflows have not been queued yet reports an empty list, and
accepting that as green is a merge with no evidence behind it.

**On `main`, a hosted RED stops the entry whatever else is true.** A local gate
never overrides a failure somebody saw. Below `main` the precedence is the other
way (rule 15): the local gate is the verdict, and the hosted red is printed by
name on the merge line rather than blocking it, because below `main` the hosted
lane is not the evidence the entry is judged on (rule 10). This is the asymmetry that makes the fast lane
safe to trust: a local green is permission to **stop waiting**, never permission
to **ignore**.

**A draft is never merged from outside the lane, and never silently.** The
prototype calls `gh pr ready` on a draft whose turn has come. That is kept —
the lane's entries are the lane's business — and it is bounded: readying a pull
request is a mutation, it is logged as one, `dry-run` prints it instead, and an
entry that is a draft *and* is not in the lane cannot be reached at all, which
is the rule `never a draft not in the lane` states.

## The read condition

```
a hold anywhere          -> HOLD, never merges, whatever the checks say
needs_read=no            -> satisfied
needs_read=yes and
  an approve recorded by a line that is NOT the entry's author,
  for this entry's CURRENT head
                         -> satisfied
anything else            -> NEEDS-READ, waits
```

**A hold blocks, and nothing outvotes it.** Not three approves, not a green
gate, not a deadline. A hold is removed by the line that recorded it recording
an approve; the tool does not delete records, and the state file keeps both.

**An approve from the author is not a read.** The prototype counts any recorded
approve, which made a self-approve indistinguishable from a read — and the whole
point of the read condition is that somebody other than the writer looked
(Glenn, 2026-09-09: **shared tools merge only after reads from other lines**).
So `read` records `who`, `run` reads the entry's author from the host, and an
approve whose `who` resolves to the author does not satisfy the condition. It
is still recorded, and `STATUS ENTRY` shows it; it just does not count. The
resolution is by the names in the lane's own state, not by anything the host
says about identity: a host login is evidence about an account, and `who` is a
line at a keyboard.

**A read is keyed to the head the reader read.** `read` stamps the entry's
current `oid` into the verdict, and a pass ignores an approve whose `head` is
not the entry's current `oid`, printing it as `read=0a/0h` with the stale count
on `STATUS ENTRY`. This is the fourth race in **the races** below, and it is the
one the prototype leaves open: an approve recorded at 12:31Z counted for a head
pushed at 12:47Z.

## The local gate

The local gate is the fast lane's **exact steps, run on our own hardware**. It
is not a looser version and it is not a subset. One recorded gate is
`{entry, head, verdict, summary, at}` and it is recorded by `nova-merge gate`,
which is the only way a gate enters the lane.

```
nova-merge gate --lane <dir> --pr 949 --head <sha> --verdict green --summary <path>
```

`--head` must be a full hexadecimal commit sha of at least 7 characters;
`--summary` must be a file that exists. Both are refusals, not tolerances: a
gate with a truncated head is a gate that might match the wrong commit, and a
gate with no summary is a claim with no evidence behind it.

**A gate is for exactly one head.** The newest green gate for the entry's
current `oid` wins; a gate for any other head is ignored, and `STATUS ENTRY`
prints `gate=-` rather than pretending. A gate may be recorded **before** the
entry joins the lane — the gate list is top-level, keyed by entry — and `GATE
OK` says `in_lane=false` when that is what happened.

**Gates are only accepted under `run --local-gates`.** Without the flag the lane
reads hosted checks and nothing else. The flag is the caller saying *our
hardware's green counts today*, and it is printed on `RUN PASS` so a reader of
the log knows which pass merged on what.

**What the gate runs is the hosted fast lane's own commands, byte for byte.**
The prototype copies `ci-fast.yml`'s `plan` script verbatim, dedented the way a
YAML block scalar dedents it, with `GITHUB_OUTPUT` pointed at a scratch file;
then the lint steps, the block gate, the touched-package tests and one run per
selected leg, with the workflow's own target lists unchanged. **The one thing
that is not verbatim is the toolchain addresses** — the hosted job installs
toolchains on a clean runner and hands each leg their paths; locally those paths
are this bench's. Addresses are not gates. A step that ran a different or a
smaller set of targets than the hosted job is a bug, and this spec says so here
so that a future looser version is a contract violation rather than a
convenience.

**Parity is proven, not promised (rule 9).** The gate runner keeps its step
list and its per-leg target lists as data, and a test reads them beside the
hosted `ci-fast.yml` and asserts the two are equal, step for step and target
for target. Today the java leg had drifted: the local gate ran
`tables-java-fixedform` and the hosted job ran `tables-java-fixedform
tables-java-versioning`. A green from a gate missing a target is a green that
answered a smaller question, and nothing but a test notices.

**Gates run wide (rule 8).** Every entry that needs a gate gets its own clone,
and the clone's name is unique per run: `gate-<entry>-<run id>`, never
`gate-<entry>` alone. Two gate runs on one entry never share a tree. Today two
runs shared `gate-<pr>`; one run's clean-up removed the tree under the other
run's `go test`, and the second run reported red with no FAIL line in it. A
gate run removes only the tree it made, and only when asked. The only serial
steps in the whole lane are the ones parallel cannot do: one base, one writer
of the state, one merge per pass. The merge itself takes seconds.

`nova-merge` does **not** run the gate. A second tool runs it and records the
verdict; `nova-merge` reads records. The separation is deliberate: the gate's
steps are this repository's `ci-fast.yml`'s, which belong to the repository
being merged and change with it, and a merge tool that embedded them would be a
merge tool that went stale silently.

**Placement (rule 10).** A pull request into `main` merges on hosted checks.
A branch entry, or a pull request whose base is below `main`, merges on a
green local gate plus its read, and needs no hosted run at all. Hosted CI runs
only on `main` and nightly. This is Glenn's law from the top of the page, made
into a rule the lane can apply per entry.

## The re-merge rule — only what conflicts

After a merge, the base has moved. Every entry behind it is now behind, and an
entry that **conflicts** with the base gets no CI at all — the host will not run
checks on a pull request it cannot merge — so it is stuck until somebody moves
it.

**So the base is re-merged into exactly the entries that CONFLICT with it, and
into nothing else.**

The prototype's first shape re-merged the base into **every** entry after every
merge. That is the storm of **2026-09-11 12:40Z**: 26 entries each got a new
commit, each new commit cancelled and restarted that entry's whole check matrix,
and about 70 jobs per entry were queued at once. The lane spent the next
half-hour watching runs it had itself invalidated, nothing went green, and the
two-minute rule was violated by the tool whose job is to honour it. An entry
that still merges cleanly needs nothing: its checks are valid, its merge commit
will be computed by the host at merge time, and touching it throws away evidence
that already exists.

The re-merge of one entry:

```
fetch the base; fetch the entry's head
check out the head in the lane's clone
if the base is already an ancestor -> nothing to do
merge the base
  clean            -> commit and push the head (never forced)
  any conflict     -> abort the merge; BLOCKED with the conflicting file list; move on
```

A blocked entry is `BLOCKED` with its reason, and the lane **moves on**: one
entry that needs a hand does not stop the other thirty-two. The reason names
every conflicting file, so `conflicts with fixed-table-form in
test/java-tables/Versioning.java` is a sentence a reader can act on without
opening anything, and `files=<n>` on `RUN REMERGE` is the count.

**A branch entry's merge is never re-merged and never auto-resolved.** A branch
entry merges by checking out the base in the lane's clone, `merge --no-ff` of
`origin/<branch>` with a message naming the branch and its last commit's
subject, and a push of the base (never forced). A branch that conflicts stops
with its reason and waits for a hand.

## The one mechanical conflict rule, withdrawn

An earlier draft of this spec kept one mechanical conflict rule in two halves:
a sorted union of a ship-target list, and a workflow hunk where both sides had
only added steps. The prototype carries both, as `resolve_mechanical` and an
embedded Python resolver. **This spec withdraws the rule. There is no
mechanical resolve (rule 7).**

The reason is the record. On 2026-09-11 the lane met four conflicts. All four
were real: all on one Java file the tip had changed, none on either of the two
files the rule named. A resolver that had matched would have been a resolver
writing code nobody read into an entry. A resolver that did not match still
had to be read, tested and kept parity with a file that belongs to another
repository. The rule earned nothing on the day it was built for, and it is
the one place in this tool where a mistake writes content.

**So the lane never edits an entry's content.** A conflict is the host's word
(`mergeable: CONFLICTING`), the entry becomes `BLOCKED`, `detail` names every
conflicting file, and a hand resolves it: a child, in one clone of its own,
never the lane's. The lane does not parse conflict markers, does not need
`diff3`, and has no `mechanical` list in its state. There is no code path in
`nova-merge` that writes a resolved file, and a test asserts it.

## The refusals that are structural

Four of this tool's rules are enforced in the **one function that runs a
mutating command**, before the command is built, rather than by the callers
remembering:

| refused | because |
|---|---|
| `--auto` in any argument | `gh pr merge --auto` merges immediately here rather than queueing; #922 went in red that way on 2026-09-11 |
| `--force`, `-f`, `--force-with-lease` | the lane never force-pushes; history the lane rewrote is history nobody can reproduce |
| a merge of an entry not in the lane | the lane's authority is exactly its own list |
| a merge of an entry whose base is not the lane's base | a merge into the wrong branch is not recoverable by a revert |

Each is a refusal with its reason and the rule's date attached, at exit 1, and
each is pinned by a test that asserts **the exit code and the text**, per this
repository's CI doctrine: a check that cannot tell a failure from a refusal is
not checking the contract it claims to.

## The races, taken out

**Two lanes on one base.** Two `run` processes, or one `run` and one hand
`gh pr merge`, both merging into one base: each merges an entry its own checks
verified against a base the other had already moved. The tool takes a **lock on
the lane directory** for the whole of a pass (`flock` on `<lane>/run.lock`, a
second, longer-lived lock than the state lock of rule 1; the pid and the stamp
written inside; released by the kernel on death, rule 2), and a second `run` on
the same lane exits 2 naming the holder. A lock is not a claim
on the base: two lanes on **one base** through two lane directories is a
configuration this tool cannot see, so `run` records the base's sha at the start
of a pass and **re-reads it immediately before the merge**; if it moved, the
pass prints `RUN STOPPED … : the base moved under this pass` and starts over
rather than merging. That check is cheap and it is the only defence against a
hand at another keyboard.

**A merge and a re-merge crossing.** A re-merge pushes a new commit onto an
entry's head; a pass that had already read that entry's checks would merge a
head whose checks are for the previous commit. So a pass re-reads the entry's
`oid` immediately before merging and **refuses if it changed** since the pass
read its checks — the same re-read that catches the author pushing mid-pass. A
re-merge of the entry that is next to merge happens **in the pass's own order**,
before its checks are read, never concurrently.

**A read recorded for a stale head.** Closed by keying the verdict to the
`oid` it was recorded against; see **the read condition**. The prototype leaves
it open, and today's state file shows four approves on one entry recorded at
12:31Z and 12:35Z against a head whose `oid` is now something else.

**A gate for a stale head.** Closed the same way, and already closed in the
prototype: `gate_path` selects on `head == oid` exactly. This spec keeps it and
adds the symmetric refusal — a gate recorded for a head that is not an ancestor
of the entry's current head is not merely ignored but reported, on
`STATUS ENTRY`, as `gate=stale`, because a caller who ran the gate and then
pushed should be told the gate was spent rather than left wondering why the lane
is waiting.

**A state file written by two verbs at once.** `add`, `read` and `gate` are
run by hand while `run` loops, and six gate records can arrive in one second.
Every write is a read-modify-write of one JSON file, so every write takes the
same lane lock (rule 1). The write goes to `state.json.tmp.<pid>`, a name per
writing process, and lands by rename; nothing is written unless the old state
parsed and the new state parses. Today's prototype, before its lock, had two
writers on one shared temp name: the state was left at 0 bytes and the lane
lost all 33 entries and every read. A verb that cannot take the lock within
its `--timeout` exits 2 and says who holds it.

**A lock broken under a live holder.** This tool has no stale-lock check to
get wrong (rule 2): the lock is the kernel's, released when the holder dies,
so a dead holder holds nothing and a live holder cannot be broken. The
prototype's directory lock needed an age; its check read a vanished lock's
age as infinite, called it stale, and broke the lock the next writer had just
taken; 3 of 20 concurrent writes were lost that way. That is the argument for
the kernel lock, not for a better age.

**A red base under a green entry.** An entry's checks are evidence about the
entry merged with the base the host computed at check time. If the base is
red, that merge is red too, whatever the entry's own checks say, and the entry
behind it is worse. So a pass reads the base's evidence first (rule 6) and
stops before touching any entry when the base is red. The only way through is
`--planned-red`, a person's name on the exception, printed on every pass.

## The state file

`<lane>/state.json`, decoded **strictly** — an unknown field is a refusal,
because a state file whose `needs_read` key was typed `needs_reads` is a state
file whose owner believes a read is required.

```json
{
  "repo": "<owner>/<name>",
  "base": "<branch>",
  "prs": [
    {"pr": 951, "needs_read": "yes",
     "reads": [{"who": "emma", "verdict": "approve", "note": "", "at": "2026-09-11T12:31:07Z",
                "head": "cbde1fc6ba10c1430f9f90615c70706ea7aaa29e"}],
     "head": "rowan/twin-full-width-lanes",
     "oid": "cbde1fc6ba10c1430f9f90615c70706ea7aaa29e",
     "state": "RED", "last": "2026-09-11T13:17:00Z",
     "detail": "fixed form + versioning (java)",
     "green": 24, "pending": 0, "red": 5}
  ],
  "branches": [
    {"branch": "rowan/wire-probe", "needs_read": "no", "reads": [],
     "head": "", "oid": "", "state": "NEEDS-GATE", "last": "", "detail": ""}
  ],
  "gates": [
    {"pr": 949, "head": "<sha>", "verdict": "green",
     "summary": "<path>", "at": "2026-09-11T13:00:19Z"}
  ]
}
```

**The two entry lists are separate, and a gate carries `pr` or `branch` and
never both.** A branch entry in the `prs` list would be parsed as a pull request
by any older reader of this file, and a gate with both keys would match both
selections. The prototype arrived at the same shape for the same reason, and it
is worth stating as a rule: **a list whose items have two shapes is two lists.**

The counts (`green`, `pending`, `red`) are numbers here and strings in the
prototype, because `jq --arg` writes strings. A count that is a string compares
as a string, and `"10" < "9"`. This spec makes them numbers.

## The log

`<lane>/log`, append-only, one line per event, `<UTC stamp> <text>`, never
rotated by the tool and never rewritten. Every mutation is logged **before it
runs** (`RUN <command>`) and its result after. A refusal is logged. A survey
under `dry-run` logs nothing, which is what makes it a survey.

The log is the lane's record of what it did and it is read by a person the
morning after a storm. That is its whole specification: **one event per line,
the stamp first, no filtering.** Glenn, 2026-09-10, after grep kept only the
`NOTE` lines and lost the `REFUSED` one for thirty minutes: **never filter the
status line.**

## What it deliberately does not do

- **It does not review code.** A read verdict is recorded by a line; nothing in
  this tool reads a diff and forms an opinion.
- **It does not run the gate.** It records gate verdicts. See **the local
  gate**.
- **It does not queue with the host's merge queue**, and it does not use
  `--auto`, which on this host is not a queue.
- **It does not open, close, comment on, label or approve a pull request.** It
  readies a draft that is in the lane and it merges. A tool that could comment
  could be made to argue.
- **It does not resolve a conflict.** Not one. A conflict is `BLOCKED` with
  its file list, and a hand resolves it in a clone of its own (rule 7).
- **It does not run in the dark.** Every loop has `--hours`, every wait has a
  timeout, and a pass says on its second line whether the base is green.
- **It does not rebase or squash.** A merge commit, or a stop.
- **It does not decide what goes in the lane.** `add` is a person's decision,
  every time.
- **It does not notify anybody.** Announcing is `nova-bus`'s job, and a merge
  tool that also wrote to the bus would be two tools in a bug report.

## What the prototype does that this spec forbids

The prototype is `merge-lane.sh`, 700-odd lines of zsh, and it landed the work.
These are the places where this spec is deliberately **not** a transcription of
it:

1. **`--dry-run` is a global flag on every verb, including `run`.** Here the
   survey is the `dry-run` verb and cannot reach the mutating helper at all.
2. **An approve from the author counts.** Here it is recorded and does not
   satisfy the read condition.
3. **A read is not keyed to a head.** Here a verdict carries the `oid` it was
   recorded against and a stale one does not count.
4. **A pass prints one line per entry, uncapped, every loop.** Here every
   listing is capped at `--max`, default 20, with one MORE line and one remedy
   line.
5. **A pass exits 0 whatever happened, and `die` exits 1 for invocation
   errors.** Here the exit table is this repository's: 0 pass, 1 said NO, 2
   could not run — so a flag typo is 2, and a blocked entry is 1.
6. **The counts in the state file are strings.** Here they are numbers.
7. **The lock covers one write and nothing holds the lane for a pass.** Two
   `run` loops on one lane directory interleave their passes. Here every write
   takes the lane lock, and a pass holds it throughout.
8. **The base is not re-read immediately before the merge.** Here it is, and a
   base that moved stops the pass.
9. **`resolve_mechanical`, and the embedded Python resolver behind it.** The
   prototype rewrites two named files in place when every conflict matches one
   of two rules. Here there is no resolver at all: a conflict is `BLOCKED`
   with its file list, and no code path writes a resolved file (rule 7).
   Today's four conflicts were all real and all on one Java file the rule did
   not name.
10. **The first shape re-merged the base into every entry after every merge.**
    That is the storm of 12:40Z: 26 entries times 70 jobs, and the runner pool
    jammed for an hour. Here the base is re-merged only into an entry the host
    reports `CONFLICTING` (rule 3).
11. **`gh pr checks` returning an empty list is treated as zero of everything**
    and then caught only because `C_GREEN == 0` is also a wait. Here "no checks
    at all" is explicitly `PENDING`, named, and tested.
12. **A gate summary path is recorded without being read.** Here it must exist
    at record time and the path is stored absolute.
13. **`status` can resolve a branch head by shelling into the clone when the
    entry has none yet**, which makes a read-only verb depend on a clone's
    freshness. Here `status` prints `head=-` and says the lane has not run yet.
14. **The `--auto` refusal lives in one shell function, `mut`, and every other
    call site in the file runs `git` or `gh` directly.** A caller who reaches
    past `mut` has no guard, and `gh pr merge --auto` by hand is what merged
    #922 red. Here the guard is the only path to a mutation (rule 4).
15. **One shared temp name for the state write** (`lane.json.tmp`, in the
    revision that lost the state), then `.tmp.$$`, with no lock around either.
    Here rule 1: one kernel lock, a fixed temp name, a rename, and both states
    must parse.
16. **A directory lock with an age, and "vanished means stale".** The
    prototype's stale check computed an age for a lock that was gone between
    its existence test and its stat, called it stale, and broke the lock a live
    writer had just taken. Here rule 2: a kernel lock, no age, nothing to
    break.
17. **`local-gate.sh` names the clone `gate-<pr>`, reclaims it with `--gc`
    (an `rm -rf`), and two runs on one pull request share it.** Here rule 8:
    one clone per gate run, named uniquely per run, and a run removes only the
    tree it made.
18. **The java leg runs `tables-java-fixedform` only**; the hosted job runs
    `tables-java-fixedform tables-java-versioning`. Here rule 9, and the parity
    test that would have gone red on the drift.
19. **`run` with no `--hours` loops forever** (`hours=0` means no deadline),
    and the lane directory, the repository and the base default from
    environment variables. Here `--loop` requires `--hours` (rule 13), and
    every path is a flag (SPEC.md, no guessing).
20. **Nothing reads the base's own checks.** A pass starts reading entries with
    no word on whether the base is green. Here `RUN BASE` is the second line
    of every pass and a red base stops it (rule 6).
21. **`local-gate.sh`'s slots are directories holding a pid, with a stale
    check.** The stale check is the same age arithmetic that broke the lane
    lock (item 16), and a slot whose holder died is held until somebody's
    computation agrees it is dead. Here rule 14: one lock file per slot, a
    kernel lock, no age, and the wait says so on one line.
22. **A zsh loop runs old code after the file changed.** `merge-lane.sh run`
    is a shell loop over a script that was edited under it, so a fixed pass
    ran the old code and nobody could tell which. Here rule 16: the build id
    is on every pass, and the loop ends at the pass boundary when the binary
    on disk differs.
23. **`BLOCKED` with a file name and no next step.** The prototype names the
    conflicting file and stops; the clone, the merge and the push are the
    reader's to work out, and today that was four afternoons. Here rule 17:
    the exact hand command is on the line.
24. **The "first failing line" is a search over free text, and the gate log
    lives under the clone.** #948's first failing line was a status line, and
    the real log went with `--gc`. Here rule 17: a declared marker or `no
    marker, see <path>`, and logs under the lane that outlive the clone.

## Tests this spec demands

One line per rule in **the rules, numbered**. Each is a test the work list
builds, and each must be seen red before it is trusted (CONTRIBUTING.md: a
check never seen failing is not a check).

1. Thirty concurrent writers (`add`, `read`, `gate`, in any mix) on one lane:
   every write lands in the final state, and a reader polling the file in a
   tight loop parses it at every read, never 0 bytes, never a partial file.
2. A holder killed with SIGKILL mid-write leaves the old state entire and
   the next writer takes the lock at once, with no age and no break; a second
   holder against a live one waits the bounded, jittered time and exits 2
   naming the live holder's pid; the fixed temp name left by the kill is
   stepped over, not reported as a stray.
3. After a merge, an entry the host reports `MERGEABLE` is not touched: no
   fetch of its head, no commit, no push. An entry reported `CONFLICTING` gets
   exactly one re-merge attempt.
4. `--auto`, `--force`, `-f`, `--force-with-lease` in any argument to the
   mutating helper: exit 1 and the sentence, before the command is built; the
   fake host records that no push was ever forced.
5. One failing check refuses; one pending check waits; zero checks is
   `PENDING`; a green gate for the current head merges; a green gate for the
   previous head prints `gate=-` and waits; on `main`, a hosted red with a
   green gate still refuses (below `main`, test 15).
6. A red base: the pass prints `RUN STOPPED base=…` as its third line and
   merges nothing, whatever the entries show; with `--planned-red <text>` the
   text is on `RUN PASS` and in the log and the pass proceeds; a `PENDING`
   base waits and `RUN NOTE` names the gate command.
7. A conflicting entry becomes `BLOCKED` with every conflicting file named in
   `detail`, its head sha is unchanged, and the fake host saw no push. A source
   test asserts no function in `internal/merge` opens a file in the clone for
   writing.
8. Two gate runs for one entry in one process get two clone names, neither is
   `gate-<entry>` alone, and a run's clean-up removes only the tree it made;
   the other run's tree is intact afterwards.
9. The gate runner's step list and per-leg target lists, read as data, equal
   the hosted `ci-fast.yml`'s, read from the workflow; a fixture with the java
   drift makes the test red.
10. A branch entry merges on a green gate plus its read with no hosted checks
    at all; a pull request onto `main` with no hosted checks waits.
11. Reads survive a restart: `read` writes, the process exits, `status` prints
    the same approve and hold counts, and `STATUS OK reads=` equals their sum.
12. Fifty entries: `status` and `run` output measured in lines and bytes and
    the numbers written into the commit; the listing is a prefix of 20 with
    one MORE line; `RUN NOTE` is one line; the counts say 50.
13. `run --loop` without `--hours` is exit 2 and the sentence; a loop with
    `--hours` ends on its own with the injected clock; nothing under `/tmp`
    is created; a source test finds no `pgrep`, no `ps`, and no read of the
    process table.
14. With `--slots 3`, four gates started together: three run, the fourth
    prints one `GATE WAIT` line and runs when a slot frees; a holder killed
    with SIGKILL mid-gate frees its slot at once and the waiter takes it with
    no age computed; a gate with `--legs 2` over nine legs never has more than
    two leg processes alive, checked by a fake leg that records its start and
    end; `--slots` or `--legs` missing is exit 2 naming the flag.
15. Base is not `main`, hosted red for the head, green gate for the head: the
    entry merges and `MERGE OK` carries `hosted_red=<names>` with the failing
    check names; base is `main`, same evidence: the entry is `RED` and nothing
    merges; base is `main`, hosted pending, green gate: merges with
    `basis=gate`.
16. `RUN PASS` carries `build=<id>` equal to the id compiled in; with the
    binary at the tool's own path replaced mid-pass by one with a different
    id, the loop finishes that pass, prints `RUN NEWER` naming both ids, and
    exits 0 before the next pass; an unchanged binary loops to `--hours`.
17. A conflicting entry prints `RUN BLOCKED` whose tail parses as the clone,
    fetch, merge and push commands with every conflicting file named, and a
    second pass with the same head prints nothing new for it; a new head
    clears it; after the gate runner's `--gc` every `<step>.log` under
    `<lane>/gates/` still exists; a step with a marker quotes the first
    matching line in the summary, a step without prints `no marker, see
    <path>` with an existing path, and a source test finds no free-text search
    for `FAIL` or `error` in the summariser.

## The work list

To build it in Go under `cmd/nova-merge`, the way `cmd/nova-bus` is built:
standard library only, no hardcoded paths, no default paths, the exit grammar
above, `internal/oneline` for every printed value, `internal/bounded` for every
listing, and `ONBOARDING.md`'s first-day standard — a usage banner ending in a
runnable `example:` block, refusals that say what the flag wants and report every
independent problem at once, a `### First run` in `README.md`, a `quickstart`
verb, and tests that pin all three by executing them.

1. **`internal/merge/state.go`** — the state file: strict decode (unknown field
   is an error), the two entry lists, the gate list, the write under the lock
   through `state.json.tmp.<pid>` and rename, both states parsed before the
   rename. Tests: an unknown field refuses; a string where a number belongs
   refuses; a round trip preserves order; thirty concurrent writers all land
   and the file parses at every instant (demanded test 1).
2. **`internal/merge/lock.go`** — the lane lock as `flock` on a lock file
   in the lane directory (LockFileEx on Windows), held for one
   read-modify-write, a bounded jittered wait, exit 2 naming the holder's pid;
   no stale rule, no age. Tests: a second holder
   is refused and the refusal names the first; demanded test 2.
3. **`internal/merge/blocked.go`** — the conflicting-file list from
   `git diff --name-only --diff-filter=U`, the abort, the abort checked
   afterwards, the `BLOCKED` detail. Tests: every file is named; the abort is
   verified; a source test asserts nothing in the package writes into the
   clone (demanded test 7).
4. **`internal/merge/parity_test.go`** — the gate runner's step and target
   lists against `ci-fast.yml` (demanded test 9). The lists are data in the
   gate runner, never a second copy in this package.
5. **`internal/merge/host.go`** — the host interface: entry metadata, check
   buckets, the base's own check buckets, ready, merge. One implementation
   shelling to `gh` with `--timeout`, and a fake for tests. Tests: an empty
   check list is `PENDING`; a `cancel` bucket is red; a fork head is `FORK`.
6. **`internal/merge/gitops.go`** — the clone, fetch, checkout, merge, push, all
   with the timeout, and the **mutation guard** that refuses `--auto` and the
   three force spellings before building a command. Tests: each refused flag,
   by exit code and text; a push is never `--force` (demanded test 4).
7. **`internal/merge/read.go`** — the read condition: author exclusion, head
   keying, hold precedence, the lane-wide totals. Tests: a hold beats three
   approves; an author approve does not satisfy; a stale approve does not
   satisfy and is counted separately; demanded test 11.
8. **`internal/merge/pass.go`** — one pass: the base's evidence first and a
   red base stops (rule 6), walk the order, classify each entry into the
   closed state set, re-merge only what conflicts, re-read the base and the
   entry `oid` immediately before the merge, merge at most one, stop. Tests:
   the storm does not happen (a clean entry is not touched after a merge,
   demanded test 3); a moved base stops the pass; one merge per pass; demanded
   tests 5, 6 and 10.
9. **`cmd/nova-merge/main.go`** — the verbs, the flag parsing with this repo's
   one-line refusals, the output grammar exactly as above, `--max` on every
   listing, `--loop` refusing without `--hours`, `dry-run` as a verb with no
   path to the mutation guard.
10. **`cmd/nova-merge/*_test.go`** — the contract tests: every exit code, every
    refusal sentence, the structural refusals, `dry-run` writes nothing
    (asserted by running it against a lane whose clone is read-only), a capped
    listing is a prefix with a MORE line, `RUN NOTE` is exactly one line, the
    fifty-entry measurement (demanded test 12), the no-`/tmp` and no-process-
    scan source test (demanded test 13).
11. **The gate runner's clone naming** (demanded test 8) lives with the gate
    runner, and this spec only demands it: `gate-<entry>-<run id>`, and a
    clean-up that removes only its own tree.
12. **`README.md`'s `### First run`** and the `quickstart` verb: create a lane,
    add one entry, print the status, with every path a flag.
