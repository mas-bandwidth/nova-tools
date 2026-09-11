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
| two honest conflicts, the same two files, every pass, resolved by hand each time | exactly **one mechanical conflict rule**, named, and STOP on anything else |
| the hosted lane took 20 minutes to say what our own hardware says in two (Glenn's **two-minute rule**) | a **local gate** for the entry's current head counts as checks green |
| a branch with no pull request had no way into the lane at all | `add-branch`, and a branch entry merges on a local gate plus its read |

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
gate** below, where a hosted red still stops the entry whatever the gate says.

## The verbs

```
nova-merge add        --lane <dir> --pr <n> [--needs-read]
nova-merge add-branch --lane <dir> --branch <name> [--needs-read]
nova-merge read       --lane <dir> (--pr <n>|--branch <name>) --who <name> --verdict approve|hold [--note <text>]
nova-merge gate       --lane <dir> (--pr <n>|--branch <name>) --head <sha> --verdict green|red --summary <path>
nova-merge run        --lane <dir> (--once | --loop <duration>) [--hours <h>] [--local-gates] [--max <n>]
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
hold, a conflict that is not the mechanical one, a push that would not land. A
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
RUN PASS n=<k> at=<stamp> local_gates=<true|false>
RUN ENTRY entry=<n-or-name> head=<sha12> checks=g<n>/p<n>/r<n> read=<n>a/<n>h gate=<green|-> state=<STATE>
RUN STOPPED entry=<n-or-name>: <reason>
RUN REMERGE entry=<n-or-name> base=<branch> result=<clean|mechanical|stopped> pushed=<true|false>
MERGE OK entry=<n-or-name> base=<branch> basis=<hosted|gate> gate=<path|-> read=<who,who|none-required>
MERGE FAIL entry=<n-or-name>: <reason>
RUN OK lane=<n> merged=<n> dropped=<n> blocked=<n> waiting=<n>
RUN MORE kind=<entry> shown=<n> total=<t> nova-merge status --lane <dir> --max 0
RUN NOTE <the one remedy line>
RUN REFUSED: <reason>
STATUS ENTRY kind=<pr|branch> entry=<n-or-name> head=<sha12> checks=g<n>/p<n>/r<n> read=<n>a/<n>h gate=<green|-> state=<STATE> last=<stamp>
STATUS OK prs=<n> branches=<n> base=<branch> ready=<n> blocked=<n> waiting=<n>
DRY PLAN pos=<k> entry=<n-or-name> basis=<hosted|gate> read=<n>a/<n>h
DRY OK surveyed=<n> would_merge=<n-or-name|-> stopped=<n> waiting=<n>
STOP OK lane=<dir>
```

`RUN PASS` is the first line of every pass and it says what the pass will
**count as green** before it says what it found: `local_gates=true` accepted
local gates, `local_gates=false` read hosted checks only. A listing that does
not say what it looked at is a listing a reader will mistake for everything.

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
<lane>/stop           present means: start nothing new and exit
```

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

**A hosted RED stops the entry whatever else is true.** A local gate never
overrides a failure somebody saw. This is the asymmetry that makes the fast lane
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

`nova-merge` does **not** run the gate. A second tool runs it and records the
verdict; `nova-merge` reads records. The separation is deliberate: the gate's
steps are this repository's `ci-fast.yml`'s, which belong to the repository
being merged and change with it, and a merge tool that embedded them would be a
merge tool that went stale silently.

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
merge the base with conflictstyle=diff3
  clean            -> commit and push the head (never forced)
  mechanical       -> resolve per the rule below, commit, push
  anything else    -> abort the merge, STOP this entry, move on
```

A stopped entry is `BLOCKED` with its reason, and the lane **moves on**: one
entry that needs a hand does not stop the other thirty-two. The reason names
the file, so `docs/FIXED-FORM-VERSIONING-TESTS.md conflicts and is not one of
the two mechanical files` is a sentence a reader can act on without opening
anything.

**A branch entry's merge is never re-merged and never auto-resolved.** A branch
entry merges by checking out the base in the lane's clone, `merge --no-ff` of
`origin/<branch>` with a message naming the branch and its last commit's
subject, and a push of the base (never forced). A branch that conflicts stops
with its reason and waits for a hand, because the mechanical rule below is a
property of the pull-request path's own files and has no business being applied
to a merge nobody reviewed against a pull request.

## The one mechanical conflict rule

Exactly two conflicts are resolved without a person, because both are
mechanical — the resolution is determined by the two sides, with no judgment in
it — and both arose on every second pass of a 30-entry lane.

1. **A sorted-union list.** A conflict whose entire `ours` side and entire
   `theirs` side are each **one line** matching the ship-target declaration — the
   generated list of which legs ship a given form — resolves to that same
   declaration holding the **sorted set union** of both sides' string literals.
   A conflict that includes any other line of that file, or where either side is
   not exactly that one line, is **not** this rule and stops.
2. **Two additions to a workflow.** A conflict in the CI workflow where the
   **base side is empty** — both sides *added* lines, neither changed the same
   ones — resolves to `ours` followed by `theirs`, in that order. A conflict
   where the base side has any content is two sides **changing** the same steps,
   which is a disagreement about the workflow and stops.

**Anything else STOPS that entry**, with one line naming the file and why it was
not mechanical. There is no third rule, no heuristic, and no "take theirs": a
merge tool that resolved a conflict it did not understand would be writing code
nobody read into the base, which is the one thing this whole lane exists to
prevent.

The rule needs `diff3` conflict style — the base side is half the evidence — and
a conflict marker the tool cannot parse to a close is a stop, never a guess.

**Which files those two are is lane configuration, not a constant.** They are
named in the lane's state at creation, as two paths and two rule names
(`sorted-union-list`, `both-added-steps`), because the paths belong to the
repository being merged and a binary that hardcoded one repository's test file
would be this repository's tool carrying another repository's accidents.

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
the lane directory** for the whole of a pass (`O_CREAT|O_EXCL` on
`<lane>/lock`, holding the pid and the stamp, per `internal/bus`'s lock), and a
second `run` on the same lane exits 2 naming the holder. A lock is not a claim
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
run by hand while `run` loops. Every write is a read-modify-write of one JSON
file, so every write takes the same lane lock, and every write is to a temporary
file in the lane directory renamed over the state (the prototype does the
rename; it does not take the lock). A verb that cannot take the lock within its
`--timeout` exits 2 and says who holds it.

## The state file

`<lane>/state.json`, decoded **strictly** — an unknown field is a refusal,
because a state file whose `needs_read` key was typed `needs_reads` is a state
file whose owner believes a read is required.

```json
{
  "repo": "<owner>/<name>",
  "base": "<branch>",
  "mechanical": [
    {"path": "<path>", "rule": "sorted-union-list"},
    {"path": "<path>", "rule": "both-added-steps"}
  ],
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
- **It does not resolve a conflict it does not have a named rule for**, and it
  has exactly two.
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
7. **No lock.** Two `run` loops on one lane directory interleave their
   read-modify-writes. Here every write takes the lane lock, and a pass holds it
   throughout.
8. **The base is not re-read immediately before the merge.** Here it is, and a
   base that moved stops the pass.
9. **The mechanical files are hardcoded to one repository's paths** (a compiler
   test and `ci.yml`). Here they are lane configuration with a named rule each.
10. **The resolver is an embedded Python program**, which puts a second language
    and a second set of parsing bugs inside a shell script. Here it is Go, in
    `internal/merge`, with the conflict parser under test.
11. **`gh pr checks` returning an empty list is treated as zero of everything**
    and then caught only because `C_GREEN == 0` is also a wait. Here "no checks
    at all" is explicitly `PENDING`, named, and tested.
12. **A gate summary path is recorded without being read.** Here it must exist
    at record time and the path is stored absolute.
13. **`status` can resolve a branch head by shelling into the clone when the
    entry has none yet**, which makes a read-only verb depend on a clone's
    freshness. Here `status` prints `head=-` and says the lane has not run yet.

## The work list

To build it in Go under `cmd/nova-merge`, the way `cmd/nova-bus` is built:
standard library only, no hardcoded paths, no default paths, the exit grammar
above, `internal/oneline` for every printed value, `internal/bounded` for every
listing, and `ONBOARDING.md`'s first-day standard — a usage banner ending in a
runnable `example:` block, refusals that say what the flag wants and report every
independent problem at once, a `### First run` in `README.md`, a `quickstart`
verb, and tests that pin all three by executing them.

1. **`internal/merge/state.go`** — the state file: strict decode (unknown field
   is an error), the two entry lists, the gate list, atomic write through a
   temporary file and rename. Tests: an unknown field refuses; a string where a
   number belongs refuses; a round trip preserves order.
2. **`internal/merge/lock.go`** — the lane lock, `O_CREAT|O_EXCL`, holder pid
   and stamp in the file, stale-holder detection, and the Windows variant, on
   `internal/bus/lock*.go`'s shape. Tests: a second holder is refused and the
   refusal names the first.
3. **`internal/merge/conflict.go`** — the diff3 conflict parser and the two
   mechanical rules, as pure functions over text. Tests: the sorted union of
   two lists; a three-line `ours` refuses; a non-empty base side refuses; an
   unterminated marker refuses; a file not in the lane's `mechanical` list
   refuses by name.
4. **`internal/merge/host.go`** — the host interface: entry metadata, check
   buckets, ready, merge. One implementation shelling to `gh` with
   `--timeout`, and a fake for tests. Tests: an empty check list is `PENDING`;
   a `cancel` bucket is red; a fork head is `FORK`.
5. **`internal/merge/gitops.go`** — the clone, fetch, checkout, merge, push, all
   with the timeout, and the **mutation guard** that refuses `--auto` and the
   three force spellings before building a command. Tests: each refused flag,
   by exit code and text; a push is never `--force`.
6. **`internal/merge/read.go`** — the read condition: author exclusion, head
   keying, hold precedence. Tests: a hold beats three approves; an author
   approve does not satisfy; a stale approve does not satisfy and is counted
   separately.
7. **`internal/merge/pass.go`** — one pass: walk the order, classify each entry
   into the closed state set, re-merge only what conflicts, re-read the base and
   the entry `oid` immediately before the merge, merge at most one, stop.
   Tests: the storm does not happen (a clean entry is not touched after a
   merge); a moved base stops the pass; one merge per pass.
8. **`cmd/nova-merge/main.go`** — the verbs, the flag parsing with this repo's
   one-line refusals, the output grammar exactly as above, `--max` on every
   listing, `dry-run` as a verb with no path to the mutation guard.
9. **`cmd/nova-merge/*_test.go`** — the contract tests: every exit code, every
   refusal sentence, the structural refusals, `dry-run` writes nothing (asserted
   by running it against a lane whose clone is read-only), a capped listing is a
   prefix with a MORE line, `RUN NOTE` is exactly one line.
10. **`README.md`'s `### First run`** and the `quickstart` verb: create a lane,
    add one entry, print the status, with every path a flag.
