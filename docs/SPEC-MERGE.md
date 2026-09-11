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
| #942 was gated green against a base that #956 then moved, merged a minute later, and turned the tip red on four tests (**2026-09-11**) | **one merge predicate** (rule 18): a green gate for exactly `(head, base sha)`, and every gate record carries `base` |
| a base re-read "immediately before the merge" still leaves the window between the read and the host's write, and a hand at another keyboard fits in it | the merge carries the head as a **host precondition**, the base is checked **after** the merge against the merge commit's own parents, and a mismatch is `MERGE RACED` and stops the pass |
| a reader finished reading H1, the author pushed H2, and the approve recorded a minute later was stamped H2 | `read --head <sha>` is required and the verdict binds to the sha the reader supplied, never to whatever the entry's head is at record time |
| a lane's repository and base were "written by the first `add`", so the first `add` was also a creation with two unstated arguments | `init --lane --repo --base` creates a lane, once; every other verb refuses a lane `init` has not made |

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
5. **Candidate evidence is one of two things, both keyed to the head, and
   neither is the verdict.** An entry is a **candidate** on zero failing and
   zero pending hosted checks with at least one pass, or on a green local gate
   recorded for exactly the entry's current head sha; a gate for an older head
   is nothing. Candidate evidence admits an entry to the integration gate of
   rule 18, which is the one merge predicate; it never merges an entry by
   itself. Which candidate evidence is required depends on the base (rule 15):
   on `main` a hosted red stops the entry whatever any gate says.
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
15. **Below `main` the local gate is the candidate evidence; on `main` the
    hosted lane is; the verdict on every base is rule 18.** When the lane's
    base is not `main`, a green local gate for the entry's current head is
    what admits it, and a hosted red for that head is printed by name on the
    `MERGE OK` line, `hosted_red=<names>`, and in the log: never blocking,
    never silent. When the base is `main`, the hosted lane admits it, zero
    fail and zero pending, a local gate for the head only lifts `PENDING`, and
    a hosted red stops the entry whatever any gate says. On both bases the
    entry then merges only on the integration gate of rule 18. This is rule
    10 applied to the evidence and not only to placement.
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
18. **The evidence is for the merge, not for the head: one predicate.** A
    green gate for an entry's head proves the head against the base it was
    merged with at gate time; the base moves with every merge, so that
    evidence expires the moment another entry lands. So there is exactly one
    statement of when an entry merges, and every flag, state field, host
    call and test refers to it:

    ```
    MERGE(entry) :=
          a gate record with verdict=green, head = entry.oid,
                             base = the base branch's sha read this pass
      and the read condition (below)
      and the four placement conditions (in the lane; base is the lane's;
          not a fork; not CONFLICTING)
      and not (base is main and a hosted check for entry.oid is red)
    ```

    The gate is for the **integration commit**: `entry.oid` merged onto that
    base sha. `nova-merge` does not run it (see **the local gate**): the gate
    runner builds that commit in a clone of its own, never pushed, runs the
    fast lane's steps on it, and records `gate --head <oid> --base <sha>`.
    Hosted green and a green gate for the head alone are **candidates** (rule
    5): they earn the entry `NEEDS-GATE` and a `RUN NOTE` naming the exact
    gate command with both shas, and nothing else. When the base has not
    moved since a green gate for the head (the record's `base` equals the
    current base sha), that record **is** the predicate and no second gate
    runs. Every gate record carries `base`; `gate` without `--base` is
    refused, and a record without it does not decode. (2026-09-11: #956
    ruled the compressed float's step into the digest and #942, gated a
    minute earlier against the base without it, merged one minute later and
    turned the tip red on four tests; three more entries were then gated red
    against that tip.)

19. **A read is recorded by the reader, with the verb, for the head the
    reader read, and the bus carries only findings.** A reader records a
    verdict with `read --who <name> --head <sha> --verdict approve|hold
    [--note <text>]` on their own machine (the tool pushes the state), never
    by writing a note the coordinator then reads and transcribes. `--head` is
    required and is the full 40-character sha the reader had open; the verdict
    binds to **that** sha, never to whatever the entry's head is when the
    verb runs, so a push between the reading and its recording cannot move
    the approve onto code nobody read. A verdict whose `head` is not the
    entry's current `oid` is kept, counted as `stale`, and authorizes
    nothing; a new `read --head <new sha>` is the only thing that does. Why
    the verb and not the bus: today the coordinator read 26 receipt lines and 21
    review notes to record 21 reads by hand, and mis-attributed all 21 once.
    A bus note is for findings a person must act on (a HOLD's quoted lines);
    an APPROVE with no findings is a command and no note. `status` prints
    reads as counts per entry (`reads=2 holds=0`), never the list, and the
    list is behind `--reads <pr>`. The coordinator's tokens are spent on
    decisions, not on transcription. (2026-09-11)
20. **A lane is created once, by `init`, with its repository and its base.**
    `init --lane <dir> --repo <owner>/<name> --base <branch>` is the only
    verb that creates a lane: it makes the directory (or takes an empty one),
    writes `state.json` with `version`, `repo`, `base` and three empty lists,
    and clones `repo/`. It is creation-only: a lane whose `state.json` exists
    is `INIT REFUSED` at exit 1, and no other verb takes `--repo` or `--base`.
    Every other verb on a directory with no `state.json` is exit 2,
    `refusing to guess: this is not a lane; nova-merge init --lane <dir>
    --repo <owner>/<name> --base <branch>`, never a state file written on the
    way past. The state file is versioned, `"version": 1`, decoded strictly,
    and a version this binary does not know is refused by number. (2026-09-11:
    "written by the first `add`" was a creation verb hiding inside a queueing
    one, with two arguments nothing asked for.)
## The verbs

```
nova-merge init       --lane <dir> --repo <owner>/<name> --base <branch>
nova-merge add        --lane <dir> --pr <n> [--needs-read]
nova-merge add-branch --lane <dir> --branch <name> [--needs-read]
nova-merge read       --lane <dir> (--pr <n>|--branch <name>) --who <name> --head <sha> --verdict approve|hold [--note <text>]
nova-merge gate       --lane <dir> (--pr <n>|--branch <name>) --head <sha> --base <sha> --verdict green|red --summary <path>
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
state once, by `init` (rule 20), and never overridable by a flag afterwards.
`add` and `add-branch` queue entries into a lane that exists; they create
nothing. A `--base` on any verb but `init` would let two invocations disagree
about where the lane lands, which is the same failure the fixed roster path
closes for `nova-bus`. (`gate --base` is not that flag: it names the base
**sha** a gate was taken against, rule 18, and the lane's base **branch** is
where the sha is read from.)

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
| 0 | the verb ran and passed: a lane created, an entry added, a read or gate recorded, a pass completed with nothing refused |
| 1 | the verb ran and said **NO**: a merge that could not be landed, a merge that `RACED`, an entry STOPPED, a `run` whose pass ended with at least one BLOCKED entry, an `init` of a lane that exists |
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
INIT OK lane=<dir> repo=<owner>/<name> base=<branch> version=1
INIT REFUSED: <reason>
ADD OK kind=<pr|branch> entry=<n-or-name> needs_read=<yes|no> lane=<prs>/<branches>
ADD NOTE <entry> is already in the lane (needs_read=<yes|no>)
ADD REFUSED: <reason>
READ OK entry=<n-or-name> who=<name> verdict=<approve|hold> head=<sha12> current=<true|false|-> approvals=<n> holds=<n> stale=<n>
READ REFUSED: <reason>
GATE OK entry=<n-or-name> head=<sha12> base=<sha12> verdict=<green|red> summary=<path> in_lane=<true|false>
GATE REFUSED: <reason>
RUN PASS n=<k> at=<stamp> build=<id> local_gates=<true|false> planned_red=<text|->
RUN NEWER build=<id> on_disk=<id>: the binary changed; this loop ends after this pass; restart it by hand
RUN BASE base=<branch> head=<sha12> checks=g<n>/p<n>/r<n> gate=<green|-> state=<GREEN|RED|PENDING|PLANNED-RED>
RUN STOPPED base=<branch>: the base is red (<n> failing); nothing merges onto a red base
RUN ENTRY entry=<n-or-name> head=<sha12> checks=g<n>/p<n>/r<n> read=<n>a/<n>h gate=<merge|head|stale|-> state=<STATE>
RUN STOPPED entry=<n-or-name>: <reason>
RUN BLOCKED entry=<n-or-name> head=<sha12> files=<n>: <the hand command, clone to push>
RUN REMERGE entry=<n-or-name> base=<branch> result=<clean|blocked> pushed=<true|false> files=<n>
MERGE OK entry=<n-or-name> base=<branch> base_sha=<sha12> head=<sha12> merge=<sha12> gate=<path> admitted=<hosted|gate> read=<who,who|none-required> hosted_red=<names|->
MERGE RACED entry=<n-or-name> base=<branch> expected=<sha12> found=<sha12> merge=<sha12>: the base moved between the gate and the merge; this pass stops
MERGE FAIL entry=<n-or-name>: <reason>
RUN OK lane=<n> merged=<n> dropped=<n> blocked=<n> waiting=<n>
RUN MORE kind=<entry> shown=<n> total=<t> nova-merge status --lane <dir> --max 0
RUN NOTE <the one remedy line>
RUN REFUSED: <reason>
STATUS ENTRY kind=<pr|branch> entry=<n-or-name> head=<sha12> checks=g<n>/p<n>/r<n> read=<n>a/<n>h stale=<n> gate=<merge|head|stale|-> state=<STATE> last=<stamp>
STATUS OK prs=<n> branches=<n> base=<branch> base_state=<GREEN|RED|PENDING|PLANNED-RED> ready=<n> blocked=<n> waiting=<n> reads=<n>a/<n>h
DRY PLAN pos=<k> entry=<n-or-name> admitted=<hosted|gate> gate=<merge|head|stale|-> read=<n>a/<n>h
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
without a pass. A gate for the base branch is recorded with `--head` and
`--base` both the base's own sha — the base merged onto itself is itself — so
the record has the shape of rule 18 and `RUN BASE gate=green` means exactly
that record.

`gate=` on `RUN ENTRY`, `STATUS ENTRY` and `DRY PLAN` is the rule 18 standing
of the entry's gate records in one word: `merge` is a green record for
`(oid, current base sha)`, the predicate's first line satisfied; `head` is a
green record for `oid` against an older base, a candidate; `stale` is a record
whose head is not the entry's current `oid` (see **the races**); `-` is none.
`MERGE OK` names all three shas the merge is made of — `base_sha`, `head`,
`merge` — and `admitted=` says which candidate evidence let the entry reach the
integration gate, because a reader of the log asks "what was this merged on"
and the answer is one gate record plus one admission.

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
<lane>/state.lock     the state lock: a file the kernel locks per read-modify-write (rules 1 and 2)
<lane>/run.lock       the pass lock: one `run` per lane; pid and stamp inside; kernel-released
<lane>/slots/<n>.lock one kernel-locked file per gate slot (rule 14)
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
| `reads` | the recorded verdicts: `who`, `verdict`, `note`, `at`, `head` — `head` is the sha the reader supplied with `--head`, never one the tool filled in |
| `head` | the branch name (a PR) or the resolved `origin/<branch>` sha |
| `oid` | the head **commit**, which is what a read is keyed to and the first half of what a gate is keyed to (the other half is the base sha, rule 18) |
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

There is one merge predicate and it is rule 18's, restated here so the section
a reader opens first says the same thing the rule says (lesson 113: **a
matching rule has exactly one statement**):

```
MERGE(entry) :=
      a gate record with verdict=green, head = entry.oid,
                         base = the base branch's sha read this pass
  and the read condition
  and the four placement conditions
  and not (base is main and a hosted check for entry.oid is red)
```

The four placement conditions:

- the entry is **in the lane** (never anything else in the repository);
- its base, read back from the host this pass, **is the lane's base**;
- its head branch lives in the lane's own repository, not a fork the lane cannot
  push to;
- it does not currently conflict with the base.

**Candidate evidence is not in the predicate.** Hosted checks (zero fail, zero
pending, at least one pass) and a green gate for the head alone (under
`--local-gates`) are what admit an entry to `NEEDS-GATE` and put the exact gate
command on `RUN NOTE`: `<gate runner> --head <oid> --base <base sha>`. On
`main` hosted green is the admission an entry needs (rule 10); below `main` a
head gate is. Neither merges anything. An entry with candidate evidence and no
record for `(oid, base sha)` waits, and the pass says so: `RUN ENTRY …
gate=head state=NEEDS-GATE`. This is the whole of what the storm of #942
taught: the head was proven, the merge was not.

**Zero fail and zero pending, counted separately.** `pass` and `skipping`
buckets count green; `pending` counts pending; `fail` and `cancel` count red. A
cancelled check is a red, not an absence: a run somebody cancelled is a run
nobody read. **Zero checks at all is not green** — it is `PENDING`, because a
pull request whose workflows have not been queued yet reports an empty list, and
accepting that as green is a merge with no evidence behind it.

**On `main`, a hosted RED stops the entry whatever else is true** — it is the
predicate's last line. A local gate never overrides a failure somebody saw.
Below `main` the precedence is the other way (rule 15): the hosted red is
printed by name on the merge line rather than blocking it, because below `main`
the hosted lane is not the evidence the entry is judged on (rule 10). This is
the asymmetry that makes the fast lane safe to trust: a local green is
permission to **stop waiting**, never permission to **ignore**. On both bases
the merge itself rests on the integration gate and on nothing else.

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

**A read is keyed to the head the reader read, and the reader says which.**
`read --head <sha>` is required (rule 19): the full 40-character sha the reader
had open, and the verdict records **that** sha. The tool never fills `head` in
from the entry's current `oid`, because the entry's head at record time is not
the head the reader read — if Emma finishes reading H1 and the author pushes
H2 before she types the verb, a stamp taken at record time would put her H1
judgment on H2. `READ OK` prints `head=<sha12>` and `current=true|false`
(against the `oid` the last pass recorded, `-` if the lane has not run), so a
reader who is already stale hears it at once. A pass ignores an approve whose
`head` is not the entry's current `oid`, counts it as `stale=<n>` on
`STATUS ENTRY`, and prints `read=0a/0h` for the entry; a stale approve is kept
and never counts. This is the fourth race in **the races** below, and it is the
one the prototype leaves open: an approve recorded at 12:31Z counted for a head
pushed at 12:47Z.

## The local gate

The local gate is the fast lane's **exact steps, run on our own hardware**. It
is not a looser version and it is not a subset. One recorded gate is
`{entry, head, base, verdict, summary, at}` and it is recorded by `nova-merge
gate`, which is the only way a gate enters the lane.

```
nova-merge gate --lane <dir> --pr 949 --head <sha> --base <sha> --verdict green --summary <path>
```

`--head` and `--base` must each be a full 40-character hexadecimal commit sha;
`--summary` must be a file that exists. All three are refusals, not tolerances:
a gate with a truncated sha is a gate that might match the wrong commit, a gate
with no base is a gate for a commit nobody can name (rule 18), and a gate with
no summary is a claim with no evidence behind it.

**A gate is for exactly one integration commit: one head onto one base.** What
the runner gated is `head` merged onto `base`, in its own clone, never pushed;
the record says both. The newest green record for `(entry's current oid,
current base sha)` satisfies the predicate; a green record for the current
`oid` against another base is a candidate (`gate=head`); a record for any other
head is `gate=stale` or nothing, and `STATUS ENTRY` says which rather than
pretending. A gate may be recorded **before** the entry joins the lane — the
gate list is top-level, keyed by entry — and `GATE OK` says `in_lane=false`
when that is what happened.

**The gate runner's interface, as far as this spec needs it.** The runner is a
second tool (below), and this spec fixes only the edge the lane sees: it takes
`--head <sha> --base <sha>`, the two shas `RUN NOTE` prints; it builds the
integration commit itself, in one clone per run (rule 8), under one of the
machine's slots with the leg cap (rule 14) and under its own deadline; it
records the verdict with `nova-merge gate` carrying the same two shas and the
summary path. A runner that gates the head alone and records it as a merge
gate is lying to rule 18, and the record's `base` is how a reader would catch
it: the sha is in the summary's first line, and a test compares the two.

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

`nova-merge` does **not** run the gate — not the head's and not the
integration commit's. A second tool runs it and records the verdict;
`nova-merge` reads records, and a pass whose front entry lacks the record rule
18 names waits in `NEEDS-GATE` with the command on `RUN NOTE`. The separation
is deliberate: the gate's steps are this repository's `ci-fast.yml`'s, which
belong to the repository being merged and change with it, and a merge tool that
embedded them would be a merge tool that went stale silently. The cost is one
round trip per base move, and the two-minute rule is what makes that round trip
affordable.

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
the same lane exits 2 naming the holder. A lock is not a claim on the base:
two lanes on **one base** through two lane directories is a configuration this
tool cannot see, and a re-read of the base "immediately before the merge" does
not close it either — another lane or a hand fits between that read and the
host's write. So the window is closed **by the host and after the fact**, with
the limit stated:

- **the head is a precondition the host enforces.** A pull request merges with
  `gh pr merge --match-head-commit <oid>`: the host refuses if the head is not
  the sha the gate record names. A branch entry's merge commit is pushed with
  a plain, non-force push of the base from the lane's clone, which the remote
  rejects if the base is no longer the sha the merge commit's first parent
  names: that push **is** the atomic expected-base guard, and it is exact.
- **the base is checked against the merge commit's own parents.** The host
  offers no expected-base precondition for a pull-request merge, so the tool
  re-reads the base sha after the merge and compares it with the parents of
  the merge commit the host reports: the first parent must be the base sha
  the gate record names, the second the head. When either differs, the tool
  prints `MERGE RACED entry=… expected=<sha12> found=<sha12> merge=<sha12>`,
  logs it, exits 1, and **the pass stops there**: the merge is on the base
  and it is untested, and the next pass gates it as the base before anything
  else (rule 6), which is the red rule doing the job the precondition could
  not.
- **the limit, stated.** For a pull request the host recomputes the merge
  commit; two parents equal to the gate's `(base, head)` is the strongest
  precondition this backend offers, and the tested object is the runner's
  merge of the same two parents. A backend with an expected-base merge
  primitive replaces the after-check with a precondition and `MERGE RACED`
  becomes a refusal before the write. `gh` today is not that backend, and
  this spec says so rather than pretending the window is zero.

**A merge and a re-merge crossing.** A re-merge pushes a new commit onto an
entry's head; a pass that had already read that entry's checks would merge a
head whose checks are for the previous commit. So a pass re-reads the entry's
`oid` immediately before merging and **refuses if it changed** since the pass
read its checks — the same re-read that catches the author pushing mid-pass. A
re-merge of the entry that is next to merge happens **in the pass's own order**,
before its checks are read, never concurrently.

**A read recorded for a stale head.** Closed by `read --head <sha>`: the
verdict carries the sha the reader supplied, and a pass counts it only while
that sha is the entry's `oid`; see **the read condition**. A stamp taken by the
tool at record time would not close it — the push can land between the reading
and the verb. The prototype leaves it open, and today's state file shows four
approves on one entry recorded at 12:31Z and 12:35Z against a head whose `oid`
is now something else.

**A gate for a stale head, or a stale base.** Closed the same way, and half
closed in the prototype: `gate_path` selects on `head == oid` exactly. This
spec keeps it, adds the base half (rule 18: a green record whose `base` is not
the current base sha is `gate=head`, a candidate, and the entry is
`NEEDS-GATE`), and adds the symmetric refusal — a gate recorded for a head that
is not the entry's current head is not merely ignored but reported, on
`STATUS ENTRY`, as `gate=stale`, because a caller who ran the gate and then
pushed should be told the gate was spent rather than left wondering why the lane
is waiting.

**A state file written by two verbs at once.** `add`, `read` and `gate` are
run by hand while `run` loops, and six gate records can arrive in one second.
Every write is a read-modify-write of one JSON file, so every write takes the
same lane lock (rule 1). The write goes to `state.json.tmp`, the fixed name of rule 1, and
lands by rename; nothing is written unless the old state
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
  "version": 1,
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
    {"pr": 949, "head": "<sha>", "base": "<sha>", "verdict": "green",
     "summary": "<path>", "at": "2026-09-11T13:00:19Z"}
  ]
}
```

`version` is written by `init` and checked first: a number this binary does not
know is exit 2 naming both numbers, before any other field is read. A gate
record's `base` and a read's `head` are both full 40-character shas and both
are required; a record missing either does not decode (rules 18 and 19). The
empty lane `init` writes is exactly this shape with the three lists empty.

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
- **It does not run the gate.** It records gate verdicts, for the head and
  for the integration commit alike, and waits for the one rule 18 names. See
  **the local gate**.
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
25. **A green gate for the head is the verdict, and a hosted green is a second
    verdict beside it.** #942 merged on a head gate a minute after the base
    moved. Here rule 18: one predicate, a gate for `(head, base sha)`, and
    hosted green and head gates are admission only.
26. **The base is re-read before the merge and then the merge is run.** A hand
    fits in the gap. Here the head is a host precondition
    (`--match-head-commit`), a branch merge is a plain push the remote rejects
    on a moved base, and a pull-request merge is checked against its own
    parents afterwards: `MERGE RACED` stops the pass.
27. **`read` stamps the entry's head at record time.** Here `read --head` is
    required and the verdict binds to the sha the reader supplied (rule 19).
28. **The lane is created by whatever verb runs first**, with the repository
    and the base from the environment. Here `init` is the one creation verb,
    the state is versioned, and every other verb refuses a directory that is
    not a lane (rule 20).

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
   `PENDING`; a green gate for the current head against the current base sha
   merges; a green gate for the current head against an older base prints
   `gate=head state=NEEDS-GATE`, waits, and `RUN NOTE` carries both shas; a
   green gate for the previous head prints `gate=stale` and waits; on `main`,
   a hosted red with a green integration gate still refuses (below `main`,
   test 15); hosted green alone, on `main`, is `NEEDS-GATE` and never a
   merge.
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
    the same approve and hold counts, and `STATUS OK reads=` equals their sum;
    a read recorded `--head H1`, then the fake host advances the entry to H2:
    the next pass prints `read=0a/0h`, `STATUS ENTRY stale=1`, the entry is
    `NEEDS-READ`, and a second `read --head H2` by the same reader satisfies
    it while the H1 record is still in the file.
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
15. Base is not `main`, hosted red for the head, green gate for `(head, base
    sha)`: the entry merges and `MERGE OK` carries `hosted_red=<names>` with
    the failing check names and `admitted=gate`; base is `main`, same
    evidence: the entry is `RED` and nothing merges; base is `main`, hosted
    pending, green gate for `(head, base sha)`: merges with `admitted=gate`;
    base is `main`, hosted green, green gate for the head against an older
    base: `NEEDS-GATE`, nothing merges.
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
18. Two entries A and B both gated green against base X (`--base X`); A
    merges (base is now X+A); B is not merged on its recorded gate: B is
    `NEEDS-GATE` with `gate=head`, `RUN NOTE` names `--head B --base X+A`, a
    red record for `(B, X+A)` makes B `RED` with both shas in `detail` and A
    stays merged, a green one merges B; when the base has not moved, no
    second gate is asked for; `gate` without `--base`, or with a 12-character
    one, is refused naming the flag; a state file with a gate record lacking
    `base` is exit 2. The race: A's gate record is green for `(A, X)`, the
    fake host moves the base to X+H after the pass's last read and then
    performs the merge; the tool reads the merge commit's parents, finds
    `X+H` where it expected `X`, prints `MERGE RACED expected=X found=X+H`,
    exits 1, and the pass stops with no further entry touched; the next pass
    treats the merged tip as an ungated base (rule 6) and waits. A branch
    entry in the same race: the plain push is rejected by the fake remote,
    nothing lands, `MERGE FAIL` names the rejection, and the branch is
    untouched. A merge is always issued with `--match-head-commit <oid>`, and
    the fake host records it.
19. `read --who emma --head <sha> --verdict approve` from a second checkout
    of the lane lands in the state under the lane lock and `status` shows
    `reads=1` with no name; `--reads <pr>` lists it with the sha; a `read`
    whose `--who` is empty is refused with the remedy line; a `read` with no
    `--head`, or a `--head` shorter than 40 characters, is refused naming the
    flag; `READ OK` prints `current=false` when the sha is not the last
    pass's `oid`, and the record's `head` is the sha given, never the `oid`.
20. `init --lane <dir> --repo o/n --base main` on an empty directory writes
    `state.json` with `version: 1`, `repo`, `base` and three empty lists,
    clones `repo/`, and prints `INIT OK`; a second `init` on the same
    directory is `INIT REFUSED` exit 1 and the state is byte-identical
    afterwards; `add`, `read`, `gate`, `run`, `status` and `dry-run` on a
    directory with no `state.json` are exit 2 with the `init` command in the
    refusal and write nothing; `--repo` or `--base` on any verb but `init` is
    exit 2 naming the flag; a state file with `version: 2` is exit 2 naming
    both numbers; the `### First run` in `README.md` starts with `init` and
    a test executes it.

## The work list

To build it in Go under `cmd/nova-merge`, the way `cmd/nova-bus` is built:
standard library only, no hardcoded paths, no default paths, the exit grammar
above, `internal/oneline` for every printed value, `internal/bounded` for every
listing, and `ONBOARDING.md`'s first-day standard — a usage banner ending in a
runnable `example:` block, refusals that say what the flag wants and report every
independent problem at once, a `### First run` in `README.md`, a `quickstart`
verb, and tests that pin all three by executing them.

1. **`internal/merge/state.go`** — the state file: `version` checked first,
   strict decode (unknown field is an error), the two entry lists, the gate
   list with `base` required, reads with `head` required, the write under
   the lock through `state.json.tmp` (a fixed name, rule 1) and rename, both
   states parsed before the rename; `Init` writes the empty versioned lane
   and refuses an existing one. Tests: an unknown field refuses; a string
   where a number belongs refuses; a gate without `base` refuses; a version
   this binary does not know refuses by number; a round trip preserves order;
   thirty concurrent writers all land and the file parses at every instant
   (demanded tests 1 and 20).
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
   keying to the sha the reader supplied, hold precedence, the stale count,
   the lane-wide totals. Tests: a hold beats three approves; an author approve
   does not satisfy; a stale approve does not satisfy and is counted
   separately; demanded tests 11 and 19.
8. **`internal/merge/pass.go`** — one pass: the base's evidence first and a
   red base stops (rule 6), walk the order, classify each entry into the
   closed state set, re-merge only what conflicts, evaluate `MERGE(entry)`
   as one function that is the only caller of the mutating helper's merge,
   issue the merge with the head precondition, read the merge commit's
   parents back and stop the pass on `MERGE RACED`, merge at most one, stop.
   Tests: the storm does not happen (a clean entry is not touched after a
   merge, demanded test 3); a raced base stops the pass after the fact; one
   merge per pass; a source test finds exactly one call site of the merge
   helper and it is inside the predicate's function; demanded tests 5, 6, 10,
   15 and 18.
9. **`cmd/nova-merge/main.go`** — the verbs, `init` first and creation-only,
   `read --head` and `gate --base` required, the flag parsing with this
   repo's one-line refusals, the output grammar exactly as above, `--max` on
   every listing, `--loop` refusing without `--hours`, `dry-run` as a verb
   with no path to the mutation guard, every verb but `init` refusing a
   directory that is not a lane.
10. **`cmd/nova-merge/*_test.go`** — the contract tests: every exit code, every
    refusal sentence, the structural refusals, `dry-run` writes nothing
    (asserted by running it against a lane whose clone is read-only), a capped
    listing is a prefix with a MORE line, `RUN NOTE` is exactly one line, the
    fifty-entry measurement (demanded test 12), the no-`/tmp` and no-process-
    scan source test (demanded test 13).
11. **The gate runner's clone naming** (demanded test 8) lives with the gate
    runner, and this spec only demands it: `gate-<entry>-<run id>`, and a
    clean-up that removes only its own tree.
12. **`README.md`'s `### First run`** and the `quickstart` verb: `init` a
    lane with its repository and base, add one entry, print the status, with
    every path a flag (demanded test 20).
