# nova-merge — specification

`nova-merge` is one binary at the **merge layer**. It lands an **ordered lane**
of entries — pull requests, or branches with no pull request at all — onto one
base branch, one at a time, and it refuses to land anything whose evidence it
cannot name.

This spec is normative. If the code and this document disagree, one of them has
a bug, and the tests decide which. It stands beside
[SPEC.md](SPEC.md), whose **Conventions** section — exit codes, no guessed
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
| a force-push would have made the base unreproducible | `--force`, `-f` and a bare `--force-with-lease` are refused in the same place as `--auto`; the one push the lane makes is a compare-and-swap, `--force-with-lease=<base ref>:<expected sha>`, which cannot rewrite anything (rule 4) |
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
| a base re-read "immediately before the merge" still leaves the window between the read and the host's write, and a hand at another keyboard fits in it | the lane builds the **integration commit** itself, the gate proves **that object**, and publication is one compare-and-swap on the base with the expected sha as a **precondition**: a moved base is `MERGE RACED` **before** anything lands, and nothing but the gated object is ever published (rule 21) |
| a reader finished reading H1, the author pushed H2, and the approve recorded a minute later was stamped H2 | `read --head <sha>` is required and the verdict binds to the sha the reader supplied, never to whatever the entry's head is at record time |
| a lane's repository and base were "written by the first `add`", so the first `add` was also a creation with two unstated arguments | `init --lane --repo --base --lane-branch` creates a lane, once; every other verb refuses a lane `init` has not made |
| a reader on another machine had nowhere to put a verdict but a bus note the coordinator transcribed, and "the tool pushes the state" named no place | a read and a gate are each **one immutable file** under the lane, committed and pushed by the tool to the **lane's own branch**; the coordinator's lane pulls them, and the state lock protects only the local fold (rule 22) |
| an older green gate could outlive a newer red for the same `(entry, head, base)` | the **newest** record for the pair decides, whatever its colour; an older green never survives a newer red (rule 18) |

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
   and there is no other path to a mutation. `--force`, `-f`, a bare
   `--force-with-lease` and `--force-with-lease=<ref>` with no sha are all
   refused. **The one spelling allowed is `--force-with-lease=<base ref>:<sha>`
   with a full ref and a full 40-character sha, used by the publication step of
   rule 21 and nowhere else**: it is a compare-and-swap, the push lands only if
   the remote ref is exactly that sha, and the object pushed has that sha as
   its first parent, so the ref moves forward by one gated commit and no history
   is rewritten. A test asserts the fake remote never sees a non-fast-forward
   push. (2026-09-11: `gh pr merge --auto` merges at once on this host, and
   #922 went in red.)
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
11. **Reads live in the lane's branch as one file each, and the state says how
    many.** `needs_read` is per entry. A read is an approve or a hold, recorded
    by name, as one immutable file under `<lane>/reads/` committed and pushed to
    the lane's branch (rule 22); `state.json` holds the **fold** of those files
    and the entry order, under rule 1's lock. A hold blocks. Losing `state.json`
    loses the order and nothing else: the next fold rebuilds every read from
    the branch. `STATUS ENTRY` prints the count of approves and holds each entry
    holds, and `STATUS OK` prints the total.
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
    default, so one gate cannot spend the whole budget on its own. Both are
    bounded: `--slots` and `--legs` are refused below 1 and above **64** (the
    cap `nova-swarm --workers` carries, Glenn 2026-09-10), on one line naming
    the flag and the cap, because a caller who typed 500 has a belief about
    the machine that a clamp would not correct. **No leg process starts
    before the slot is held**: the slot lock is taken in the gate runner's
    entry, before the first leg is spawned, and there is no code path from a
    `RUN NOTE` command or a hand-typed gate to a leg that skips it; a gate
    runner is the only thing that runs legs, and it holds a slot or it
    refuses. The machine's budget is the tool's to hold, because no caller
    can see the other callers. (2026-09-11: every caller fanned out, load
    reached 235 and
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
          the NEWEST gate record for (entry, entry.oid, base sha read this pass)
              has verdict=green, whatever any older record for the pair says,
          and its merge = an object in the lane's clone whose first parent is
              that base sha and whose second parent is entry.oid
      and the read condition (below)
      and the four placement conditions (in the lane; base is the lane's;
          not a fork; not CONFLICTING)
      and not (base is main and a hosted check for entry.oid is red)
    ```

    The gate is for the **integration commit**, and the integration commit is
    an object `nova-merge` builds once and publishes unchanged (rule 21):
    `run` merges `entry.oid` onto the base sha in the lane's own clone, keeps
    the result at `refs/nova-merge/integration/<entry>/<merge sha>`, prints
    `RUN BUILT` with all three shas, and names the gate command on `RUN NOTE`
    with `--head <oid> --base-sha <sha> --merge <merge sha> --from
    <lane>/repo`. `nova-merge` does not run the gate (see **the local gate**):
    the gate runner fetches **that object** by sha from the lane's clone into a
    clone of its own, refuses if its parents are not `(base, head)` (an
    integration gate; the base gate of the **output grammar** is the one
    exception, and it is checked by exact sha instead), runs the
    fast lane's steps on it, and records `gate --head <oid> --base-sha <sha>
    --merge <merge sha>`. Hosted green and a green gate for the head alone are
    **candidates** (rule 5): they earn the entry `NEEDS-GATE`, the build, and
    the `RUN NOTE`, and nothing else. When the base has not moved since a
    green record (its `base` equals the current base sha), that record **is**
    the predicate and no second gate runs. **The newest record per `(entry,
    head, base)` wins, whatever its colour**: records are ordered by `at`; two records with one `at` to the
    second fold with the red last, so **a red never loses a tie**; a
    red recorded after a green makes the entry `RED` with both shas in
    `detail`, and an older green never survives a newer red; a green after a
    red is a re-run that passed and merges. Every gate record carries `base`
    and `merge`; `gate` without `--base-sha` or without `--merge` is refused,
    and a record without either does not decode. (2026-09-11: #956
    ruled the compressed float's step into the digest and #942, gated a
    minute earlier against the base without it, merged one minute later and
    turned the tip red on four tests; three more entries were then gated red
    against that tip.)

19. **A read is recorded by the reader, with the verb, for the head the
    reader read, and the bus carries only findings.** A reader records a
    verdict with `read --who <name> --head <sha> --verdict approve|hold
    [--note <text>]` on their own machine — the tool writes the record as one
    file under `<lane>/reads/` and pushes it to the lane's branch (rule 22),
    never by writing a note the coordinator then reads and transcribes. `--head` is
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
20. **A lane is created once, by `init`, with its repository, its base and
    its branch.** `init --lane <dir> --repo <owner>/<name> --base <branch>
    --lane-branch <name>` is the only verb that creates a lane: it makes the
    directory (or takes an empty one), checks out the lane branch
    `refs/heads/<name>` of `repo` into it — creating that branch with one
    commit holding `.gitignore` when the host has none, `INIT OK joined=false`,
    and taking the existing one otherwise, `INIT OK joined=true`, which is how
    a reader on another machine gets a lane for the same records (rule 22) —
    writes `state.json` with `version`, `repo`, `base`, `lane_branch` and
    three empty lists, and clones `repo/`. It is creation-only: a lane whose
    `state.json` exists is `INIT REFUSED` at exit 1. **No other verb takes
    `--repo`, `--base` or `--lane-branch`**, and the flag on `gate` that names
    the base **sha** of rule 18 is spelled `--base-sha` so the two are never
    one word: `gate --base <x>` is refused naming `--base-sha`, exactly as
    `add --base <x>` is refused naming `init`. Every other verb on a directory
    with no `state.json` is exit 2, `refusing to guess: this is not a lane;
    nova-merge init --lane <dir> --repo <owner>/<name> --base <branch>
    --lane-branch <name>`, never a state file written on the way past. The
    state file is versioned, `"version": 1`, decoded strictly, and a version
    this binary does not know is refused by number. (2026-09-11: "written by
    the first `add`" was a creation verb hiding inside a queueing one, with
    two arguments nothing asked for.)
21. **The safety check precedes publication: the object gated is the object
    published, under a base precondition, and a race is a refusal before the
    write.** `run` builds the integration commit in the lane's clone (rule
    18); the gate proves that commit by sha; and the merge is the
    **publication** of that same object onto the base, in one of two ways, in
    this order. (a) When the host offers a merge primitive that takes **both**
    an expected head and an expected base as preconditions, the tool uses it
    with `--match-head-commit <oid>` and the base sha, and the host's merge
    commit must be the gated object. (b) When it does not — and `gh` today
    does not: `gh pr merge` takes `--match-head-commit` and nothing about the
    base, and a plain push checks fast-forward **ancestry**, not equality with
    the expected sha — the tool pushes the gated object itself to the base
    ref with a compare-and-swap push, `git push <remote> <merge sha>:refs/heads/
    <base> --force-with-lease=refs/heads/<base>:<expected base sha>`, the one
    `--force-with-lease` rule 4 allows. The remote applies the lease
    atomically: the push lands only if the base is exactly the expected sha,
    and then the base is the gated commit, whose first parent is that sha, so
    the ref moves forward by one tested commit. For a pull request, the host
    marks it merged on its own when its head becomes reachable from the base.
    `MERGE RACED entry=… expected=<sha12> found=<sha12> merge=<sha12>` is
    printed when the lease is rejected: **nothing was published**, the
    unpublished object is named, the pass exits 1 and stops, and the next pass
    builds a new integration commit against the moved base and asks for its
    gate. A push the remote refuses for any reason **other** than the lease —
    a protected base that admits no direct push, a missing permission, a
    remote that does not report the ref it compared — is `MERGE BLOCKED
    entry=… missing=atomic_publication: <the remote's first line>`, exit 1,
    no merge, the pass stops, and the remedy names the host primitive or the
    branch setting a person must change; the tool never falls back to `gh pr
    merge` without a base precondition and never to a plain push, so no
    backend turns an unsupported atomic publication into a merge. After a
    lease push lands, the tool reads the base ref back once and prints it on
    `MERGE OK` as `verified=<sha12>`; a read-back that is not `merge` is
    `MERGE FAIL … published object not at base`, exit 1 — additional evidence
    for a person, never the guard, because the guard already ran on the
    remote. There is no after-the-fact check because there is nothing to check
    after: the tool never publishes an object whose sha is not in a green
    record for `(entry, head, base)` with `merge` equal to it, and it never
    rebuilds or re-merges on the way to the push. (Stella, 2026-09-11: two
    matching parents do not establish the tested tree, and a plain push is
    not an equality guard.)
22. **A read and a gate are each one immutable file in the lane's branch,
    pushed by the tool; the lane directory is the durable home; the state
    lock protects only the local fold.** `read` writes
    `<lane>/reads/<entry>/<who>-<head12>-<at>-<rand6>.json` and `gate` writes
    `<lane>/gates/<entry>/<head12>-<base12>-<at>-<rand6>.json` (the summary
    copied beside it as `<same name>.summary`), each holding the whole record
    and nothing else — the gate record carries `run=<rand6>`, the same six
    characters as its filename, so a record quoted from a log names its file,
    and `<at>-<rand6>` is the record's **submission id**, drawn once per verb
    and never reused. The record is first written, byte for byte, to a
    durable **outbox** outside the branch, `<lane>/outbox/<submission
    id>.json` (the gate summary beside it as `.summary`), untracked, through
    `.tmp` and rename; the file is then committed and pushed to the lane
    branch by the tool, in a compare-and-swap loop: fetch, `reset --hard` the
    branch to the fetched tip, **restore** the outbox bytes to the record's
    path, add, commit, push; a rejected push repeats the loop, at most five
    times within `--timeout`. The outbox item is marked delivered — and only
    then removed — after a fetch shows the record's path at the remote tip
    with the outbox's bytes; a push that still fails is `READ FAIL` / `GATE
    FAIL … pushed=false` at exit 1 naming the file, which stays in the outbox
    and is pushed by re-running the same verb, and a kill at any boundary
    (before add, after commit, after a rejected push, after a landed push and
    before the confirming fetch) is repaired by the same re-run, which finds
    the outbox and restarts the loop. Every Git operation on a lane checkout —
    the CAS loop, the pull and the fold, the fetch of `dry-run` — runs under
    one **checkout lock**, `<lane>/checkout.lock`, a kernel lock with the
    shape of rule 1 and the wait of rule 2, held for the whole loop; the
    state lock of rule 1 protects `state.json` and nothing else. Two writers
    never touch one path: a record is **one immutable file per submission**,
    never edited and never replaced, so the reset removes nothing that the
    outbox does not restore; a reader who records again for the same head
    writes a second file, a second gate for the same pair is a second file,
    and the branch's history keeps every earlier one. `run` and `status`
    **pull** the lane branch (`--ff-only`, bounded by `--timeout`) and then
    **fold** every record file into `state.json`'s `reads` and `gates` lists
    under the state lock of rule 1; `dry-run` **fetches** the lane branch
    under the checkout lock and folds the record files of the fetched tip
    **in memory**, writing neither `state.json` nor the checkout, so its
    plan is over a named, refreshed snapshot (`DRY PLAN … lane_tip=<sha12>`)
    and it still cannot write (see **the verbs**). The lists are the fold
    and the files are the truth, so a
    record that is in the branch is in the next fold, on every machine, with
    the sha its reader supplied. (Stella, 2026-09-11: after a rejected push
    the new record was tracked in the local commit, the reset to the
    competing tip removed it, and the next add failed with `pathspec … did
    not match any files`; a fixture that preserved and restored the bytes
    delivered both readers' records.) `add` and `add-branch` write `state.json`
    only: the order of the lane is the coordinator's and is not shared.
    Nothing else in the lane is tracked: `state.json`, `log`, `repo/`, the
    locks, `slots/`, `stop` and every `<step>.log` are in `.gitignore`, which
    `init` writes. (Stella, 2026-09-11: "the tool pushes the state" named no
    place, and a cloned `state.json` plus a local lock defines neither an
    authority nor an immutable verdict.)
23. **A reader is handed the smallest sufficient packet, and it is pointers,
    never the diff.** `packet --lane <dir> --who <name> ((--pr <n>|--branch
    <name>) | --all) [--max <n>]` prints, per entry that needs a read from
    `<name>` — `needs_read=yes` and no approve by that name for the current
    `oid`; `--all` walks the lane in order — one bounded block: `PACKET
    ENTRY entry=<n-or-name> head=<sha12> last_read=<sha12|-> range=<r>
    holds=<n> gate=<merge|head|stale|-> checks=g<n>/p<n>/r<n> url=<url|->`,
    then at most `--max` `PACKET HOLD who=<name> head=<sha12>: <note>` lines
    — the unresolved findings, each a reader's own recorded hold note, with a
    `PACKET MORE` past the cap — and one `PACKET OK entries=<n> holds=<n>`.
    `range=` is `<last read sha>..<head>`, the exact commits since the sha
    this reader last recorded, or `<base>...<head>` when they never have. The
    tool prints the range and never the diff, the summary path and never the
    log, so the reader opens exactly what changed and nothing the coordinator
    retyped. `packet` is derived from the fold and the host, writes nothing
    and takes no lock. (Stella, 2026-09-11: the smallest sufficient review
    packet is the diff since my reviewed sha, the unresolved finding ids with
    their dispositions, and links to the whole; my first pass loaded too much
    history. The same day the window read 21 review notes to record 21
    reads, rule 19.)

## The verbs

```
nova-merge init       --lane <dir> --repo <owner>/<name> --base <branch> --lane-branch <name> [--remote <url>]
nova-merge add        --lane <dir> --pr <n> [--needs-read]
nova-merge add-branch --lane <dir> --branch <name> [--needs-read]
nova-merge read       --lane <dir> (--pr <n>|--branch <name>) --who <name> --head <sha> --verdict approve|hold [--note <text>]
nova-merge gate       --lane <dir> (--pr <n>|--branch <name>) --head <sha> --base-sha <sha> --merge <sha> --verdict green|red --summary <path>
nova-merge run        --lane <dir> (--once | --loop <duration> --hours <h>) [--planned-red <text>] [--max <n>]
nova-merge status     --lane <dir> [--max <n>] [--reads <entry>]
nova-merge stop       --lane <dir>
nova-merge dry-run    --lane <dir> [--max <n>]
nova-merge packet     --lane <dir> --who <name> ((--pr <n>|--branch <name>) | --all) [--max <n>]
nova-merge version

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

**`init --remote <url>` names the URL the lane clones from and pushes to, and a
first run is told to rehearse with it.** (Additive, 2026-09-12, Stella's ruling on
nova-tools #116: the flag is in the tool and was in no verb list here.) Without it
the URL comes from `--repo` through the host, which means nothing about a lane
could be exercised without a live repository, and git's own config was the only
way in — so the *environment* could move where a lane pushes with no flag saying
so. Given a bare repository of the caller's own it is the whole first run with
nothing reaching a forge, which is what `README.md`'s `### First run` shows before
the live form, because `init` creating the lane branch **is a push** (rule 20,
rule 22) and a first run that has not been told so is a first run that mutates a
shared repository to say hello. It wants an absolute path or a URL: git runs in
the lane directory, so a relative one resolves against the lane and the run is
refused. It is `init`'s alone, for rule 20's reason — a lane's remote, like its
repository and its base, is written once and not overridable by a flag afterwards.

The repository and the base are properties of the **lane**, written into its
state once, by `init` (rule 20), and never overridable by a flag afterwards.
`add` and `add-branch` queue entries into a lane that exists; they create
nothing. A `--base` on any verb but `init` would let two invocations disagree
about where the lane lands, which is the same failure the fixed roster path
closes for `nova-bus`. The base **sha** a gate was taken against (rule 18) is
`gate --base-sha`, a different word on purpose: `--base` on `gate` is refused
naming `--base-sha`, and `--base-sha` on any verb but `gate` is refused naming
`gate` (rule 20).

**`dry-run` is a verb, not a flag.** In the prototype it is a global `--dry-run`
that any verb accepts, including the mutating ones, and the cost of that is a
mode a caller can leave on or off by accident on the one command that merges.
Here the survey is its own verb with its own name: it performs every read `run`
performs, prints the whole plan rather than stopping at the first merge, and
**cannot write**: its fold is in memory over the lane tip it fetched (rule
22), and `state.json` and the checkout are byte-identical afterwards. Nothing
in `dry-run`'s code path can reach the mutating
helper at all, which is a property a test can pin and a flag never is.

**`version` is the Conventions' line, plus this binary's own `build=`.** It
prints `nova-merge <build identity> <goos>/<goarch> <go version>
build=<12 hex>`, one line, exit 0. The first four tokens are what every binary
in the set prints, so a release assertion and a person comparing two pastes
read the identity out of field two here as everywhere else. The fifth is the
sha256 of this binary's own file on disk — rule 16's answer to "is the binary
under this run the one it started with", which a stamped identity cannot give,
because two builds of one tag are two files. Before this verb printed the four,
it printed the file hash ALONE, standing where every other binary puts its
identity.

`status`, `dry-run` and `packet` **report** and exit 0 whatever the lane holds. `run` is
the verb that acts, and `run`'s exit code is about the pass, not about the lane:
see **exit codes**.

## Exit codes

| code | meaning |
|------|---------|
| 0 | the verb ran and passed: a lane created, an entry added, a read or gate recorded, a pass completed with nothing refused |
| 1 | the verb ran and said **NO**: a merge that could not be landed, a merge that `RACED`, a publication the remote refused for a missing capability (`MERGE BLOCKED`), an entry STOPPED, a `run` whose pass ended with at least one BLOCKED entry, an `init` of a lane that exists |
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
INIT OK lane=<dir> repo=<owner>/<name> base=<branch> lane_branch=<name> joined=<true|false> version=1
INIT REFUSED: <reason>
ADD OK kind=<pr|branch> entry=<n-or-name> needs_read=<yes|no> lane=<prs>/<branches>
ADD NOTE <entry> is already in the lane (needs_read=<yes|no>)
ADD REFUSED: <reason>
READ OK entry=<n-or-name> who=<name> verdict=<approve|hold> head=<sha12> current=<true|false|-> approvals=<n> holds=<n> stale=<n> file=<path> pushed=true
READ FAIL entry=<n-or-name> who=<name> head=<sha12> file=<path> pushed=false: <reason>; re-run the same verb to push it
READ REFUSED: <reason>
GATE OK entry=<n-or-name> head=<sha12> base=<sha12> merge=<sha12> verdict=<green|red> summary=<path> in_lane=<true|false> newest=<true|false> file=<path> pushed=true
GATE FAIL entry=<n-or-name> head=<sha12> base=<sha12> merge=<sha12> file=<path> pushed=false: <reason>; re-run the same verb to push it
GATE REFUSED: <reason>
RUN PASS n=<k> at=<stamp> build=<id> pulled=<n> planned_red=<text|->
RUN NEWER build=<id> on_disk=<id>: the binary changed; this loop ends after this pass; restart it by hand
RUN BASE base=<branch> head=<sha12> checks=g<n>/p<n>/r<n> gate=<green|-> state=<GREEN|RED|PENDING|PLANNED-RED>
RUN STOPPED base=<branch>: the base is red (<n> failing); nothing merges onto a red base
RUN ENTRY entry=<n-or-name> head=<sha12> checks=g<n>/p<n>/r<n> read=<n>a/<n>h gate=<merge|head|stale|-> state=<STATE>
RUN STOPPED entry=<n-or-name>: <reason>
RUN BLOCKED entry=<n-or-name> head=<sha12> files=<n>: <the hand command, clone to push>
RUN REMERGE entry=<n-or-name> base=<branch> result=<clean|blocked> pushed=<true|false> files=<n>
RUN BUILT entry=<n-or-name> head=<sha12> base=<sha12> merge=<sha12> ref=refs/nova-merge/integration/<entry>/<sha>
MERGE OK entry=<n-or-name> base=<branch> base_sha=<sha12> head=<sha12> merge=<sha12> gate=<path> admitted=<hosted|gate> read=<who,who|none-required> hosted_red=<names|-> published=<host|push> verified=<sha12>
MERGE RACED entry=<n-or-name> base=<branch> expected=<sha12> found=<sha12> merge=<sha12>: the base moved after the gate; nothing was published; this pass stops
MERGE BLOCKED entry=<n-or-name> base=<branch> missing=atomic_publication: <the remote's reason>; nothing was published; this pass stops
MERGE FAIL entry=<n-or-name>: <reason>
RUN OK lane=<n> merged=<n> dropped=<n> blocked=<n> waiting=<n>
RUN MORE kind=<entry> shown=<n> total=<t> nova-merge status --lane <dir> --max 0
RUN NOTE <the one remedy line>
RUN REFUSED: <reason>
STATUS ENTRY kind=<pr|branch> entry=<n-or-name> head=<sha12> checks=g<n>/p<n>/r<n> read=<n>a/<n>h stale=<n> gate=<merge|head|stale|-> state=<STATE> last=<stamp>
STATUS OK prs=<n> branches=<n> base=<branch> base_state=<GREEN|RED|PENDING|PLANNED-RED> ready=<n> blocked=<n> waiting=<n> reads=<n>a/<n>h
DRY PLAN pos=<k> entry=<n-or-name> admitted=<hosted|gate> gate=<merge|head|stale|-> read=<n>a/<n>h
DRY OK surveyed=<n> would_merge=<n-or-name|-> stopped=<n> waiting=<n>
PACKET ENTRY entry=<n-or-name> head=<sha12> last_read=<sha12|-> range=<r> holds=<n> gate=<merge|head|stale|-> checks=g<n>/p<n>/r<n> url=<url|->
PACKET HOLD who=<name> head=<sha12>: <note>
PACKET MORE kind=hold shown=<n> total=<t> nova-merge packet --lane <dir> --who <name> --all --max 0
PACKET OK entries=<n> holds=<n>
STOP OK lane=<dir>
```

`RUN PASS` is the first line of every pass and it says what the pass looked at
before it says what it found: `pulled=<n>` is the number of record files the
pull of the lane branch brought in and folded (rule 22), so a reader of the log
can see that a read recorded on another machine reached this pass. There is no
flag that turns gate records on or off: which candidate evidence admits an entry
is decided by the base (rule 15), and the verdict on every base is rule 18.
`planned_red=<text>` is the one exception to the red rule (rule 6), printed
where a reader of the log sees it on every pass it applied to.

`RUN BASE` is the second line of every pass. It is the base's own evidence,
read the same way an entry's is: the check buckets of the base's head commit,
or a green gate recorded for the base branch at that head. A `RED` base stops
the pass before any entry is read, with `RUN STOPPED base=…`. A `PENDING` base
(no checks and no gate for its head, which is every base below `main` right
after a merge) waits, and `RUN NOTE` names the gate run that proves it.
`STATUS OK` carries the same verdict as `base_state`, so a lane can be read
without a pass. **A gate has one of two kinds, told apart by its shas.** A
**base gate** is recorded with `--head`, `--base-sha` and `--merge` all the
base's own sha — the base merged onto itself is itself — so the record has the
shape of rule 18, and `RUN BASE gate=green` means exactly that record; the
runner, given three equal shas, fetches that one object and verifies its sha
is exactly the one named, and **no parent check applies**, because no commit
is its own parent. An **integration gate** is a record whose three shas
differ, and for it the runner's parent check of rule 21 is the guard. Any
other mix — `merge` equal to `base` or to `head` with the third different —
names no object either kind can validate and is refused by `gate` and by the
runner naming the two kinds (rule 20). A base gate never satisfies an entry's
predicate: rule 18 asks for `(oid, base sha)` and `oid` is never the base.

`gate=` on `RUN ENTRY`, `STATUS ENTRY` and `DRY PLAN` is the rule 18 standing
of the entry's gate records in one word: `merge` is a green record for
`(oid, current base sha)`, the predicate's first line satisfied; `head` is a
green record for `oid` against an older base, a candidate; `stale` is a record
whose head is not the entry's current `oid` (see **the races**); `-` is none.
`MERGE OK` names all three shas the merge is made of — `base_sha`, `head`,
`merge` — and `merge` is the sha in the gate record, byte for byte the object
published (rule 21); `published=` says whether the host's two-precondition
primitive or the compare-and-swap push carried it; `admitted=` says which
candidate evidence let the entry reach the integration gate, because a reader
of the log asks "what was this merged on" and the answer is one gate record
plus one admission.

`STATUS OK reads=<n>a/<n>h` is the total of approves and holds across the lane
(rule 11). It is a count of the fold: every record file in the lane branch for
an entry that is in the lane. A lane whose `state.json` was lost and re-made
by `init` on the same branch shows `reads=0a/0h` until its entries are added
again, and then every read is back, because the files were never in
`state.json` to lose (rule 22).

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
<lane>/.git, .gitignore   the lane is a checkout of its own branch of the repository (rules 20, 22)
<lane>/state.json     the ordered entries and the fold of the reads and gates  (untracked)
<lane>/log            one append-only line per event, UTC-stamped              (untracked)
<lane>/repo/          this lane's own clone, never a working copy of anybody's  (untracked)
<lane>/reads/<entry>/<who>-<head12>-<at>-<rand6>.json         one read, immutable, tracked and pushed (rule 22)
<lane>/outbox/<at>-<rand6>.json                              a record not yet confirmed at the remote tip (rule 22), untracked
<lane>/checkout.lock  the checkout lock: one Git operation on this checkout at a time (rule 22)
<lane>/gates/<entry>/<head12>-<base12>-<at>-<rand6>.json     one gate record, tracked and pushed (rule 22)
<lane>/gates/<entry>/<head12>-<base12>-<at>-<rand6>.summary  its summary, tracked beside it
<lane>/gates/<entry>/<head>/<step>.log   one log per gate step, kept past --gc (rule 17), untracked
<lane>/state.lock     the state lock: a file the kernel locks per read-modify-write (rules 1 and 2)
<lane>/run.lock       the pass lock: one `run` per lane; pid and stamp inside; kernel-released
<lane>/slots/<n>.lock one kernel-locked file per gate slot (rule 14)
<lane>/stop           present means: start nothing new and exit
```

Every one of these lives under `--lane`. Nothing this tool writes goes
anywhere else, and nothing goes under `/tmp` (rule 13). The tracked files are
the records and nothing else; a `git status` in a lane that is not mid-verb is
clean.

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
| `reads` | the fold of `<lane>/reads/<entry>/` (rule 22): `who`, `verdict`, `note`, `at`, `head`, `file` — `head` is the sha the reader supplied with `--head`, never one the tool filled in |
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
      the NEWEST gate record for (entry, entry.oid, base sha read this pass)
          has verdict=green, whatever any older record for the pair says,
      and its merge = an object in the lane's clone whose first parent is
          that base sha and whose second parent is entry.oid
  and the read condition
  and the four placement conditions
  and not (base is main and a hosted check for entry.oid is red)
```

When it holds, the merge is the publication of that object under the base
precondition (rule 21), and nothing else.

The four placement conditions:

- the entry is **in the lane** (never anything else in the repository);
- its base, read back from the host this pass, **is the lane's base**;
- its head branch lives in the lane's own repository, not a fork the lane cannot
  push to;
- it does not currently conflict with the base.

**Candidate evidence is not in the predicate.** Hosted checks (zero fail, zero
pending, at least one pass) and a green gate for the head alone are what admit
an entry to `NEEDS-GATE`, make `run` build the integration commit (`RUN BUILT`,
rule 18) and put the exact gate command on `RUN NOTE`: `<gate runner> --head
<oid> --base-sha <base sha> --merge <merge sha> --from <lane>/repo`. On
`main` hosted green is the admission an entry needs (rule 10); below `main` a
head gate is (rule 15). Neither merges anything. An entry with candidate
evidence and no record for `(oid, base sha)` waits, and the pass says so: `RUN
ENTRY … gate=head state=NEEDS-GATE`. A clean two-parent merge in the lane's
clone writes no resolved file, which is what rule 7 forbids; a build that
would need one is aborted and the entry is `BLOCKED` exactly as a host
`CONFLICTING` is. This is the whole of what the storm of #942 taught: the head
was proven, the merge was not.

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
an approve for the same head, a second file whose `at` is later (rule 22);
per `(who, head)` the read condition takes the record with the newest `at`,
and two with one `at` to the second fold **hold-last**, as rule 18 folds
red-last; the tool deletes no record, and the lane branch's history keeps
the hold.

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
`{entry, head, base, merge, verdict, summary, at, run, file}` and it is recorded by
`nova-merge gate`, which is the only way a gate enters the lane.

```
nova-merge gate --lane <dir> --pr 949 --head <sha> --base-sha <sha> --merge <sha> --verdict green --summary <path>
```

`--head`, `--base-sha` and `--merge` must each be a full 40-character
hexadecimal commit sha; `--summary` must be a file that exists. All four are
refusals, not tolerances: a gate with a truncated sha is a gate that might match
the wrong commit, a gate with no base is a gate for a commit nobody can name
(rule 18), a gate with no `merge` is a gate for an object nobody can publish
(rule 21), and a gate with no summary is a claim with no evidence behind it.

**A gate is for exactly one integration commit: one object, named by sha.** What
the runner gated is the object `merge`, whose parents are `base` and `head`;
the record says all three. **The newest record for `(entry's current oid,
current base sha)` decides, whatever its colour** (rule 18): green satisfies
the predicate, red makes the entry `RED` with both shas in `detail`, and an
older record for the pair is never consulted once a newer one exists, and two
records with one `at` fold red-last (rule 18). A green
record for the current `oid` against another base is a candidate (`gate=head`);
a record for any other head is `gate=stale` or nothing, and `STATUS ENTRY` says
which rather than pretending. A gate may be recorded **before** the entry joins
the lane — the gate list is top-level, keyed by entry — and `GATE OK` says
`in_lane=false` when that is what happened, and `newest=false` when an even
newer record for the pair already exists.

**The gate runner's interface, as far as this spec needs it.** The runner is a
second tool (below), and this spec fixes only the edge the lane sees: it takes
`--head <sha> --base-sha <sha> --merge <sha> --from <path>`, the three shas and
the clone path `RUN NOTE` prints; it fetches the object `merge` by sha from
`--from` into one clone per run (rule 8), **refuses, for an integration gate, if
that object's parents are not exactly `(base, head)`**, and for a base gate
(the three shas equal, **output grammar**) verifies only that the fetched
object's sha is the one named, runs the fast lane's steps on it under one of the
machine's slots with the leg cap (rule 14) and under its own deadline, and
records the verdict with `nova-merge gate` carrying the same three shas and the
summary path. The runner never builds a merge of its own: a merge made twice
has two shas, and only the one in the lane's clone can be published (rule 21).
A runner that gates the head alone and records it as a merge gate is lying to
rule 18, and the record's `merge` is how a reader would catch it: the sha is in
the summary's first line, and a test compares the two.

**There is no flag that turns gate records on or off.** The integration gate
is the verdict on every base (rule 18); which candidate evidence admits an entry
to it is decided by the base (rule 15), never by a flag, so two passes on one
lane cannot disagree about what counts. `RUN PASS` says what the pass pulled
and folded, not what it chose to believe.

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
integration commit's — though it does **build** the integration commit, once,
in its own clone, so that the object gated is the object published (rules 18
and 21). A second tool runs the gate and records the verdict;
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
entry's integration commit is built exactly as a pull request's (rule 18):
`merge --no-ff` of `origin/<branch>` onto the base sha in the lane's clone, with
a message naming the branch and its last commit's subject, kept under
`refs/nova-merge/integration/`; it is gated by sha and published by the
compare-and-swap push of rule 21. A branch that conflicts stops with its reason
and waits for a hand.

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
host's write. So the window is closed **by the remote, as a precondition on
the write** (rule 21):

- **the object published is the object gated.** The tool never merges at the
  host and never re-merges on the way to the push: the gate record names
  `merge`, the lane's clone holds that object, and publication moves the base
  ref to that sha and to nothing else. Two parents equal to `(base, head)` are
  not the test — two merges of the same parents can differ in their trees —
  the sha is.
- **the base is a precondition the remote enforces, atomically.** The push is
  `--force-with-lease=refs/heads/<base>:<expected base sha>`, the one lease
  rule 4 allows: the remote compares its ref with the expected sha and moves
  it only on equality. A plain push would accept any tip that is an ancestor
  of the merge — the head itself, or the head's parent, both of which the
  gate never saw as a base — and that is why the plain push is not used. A
  host primitive that takes both an expected head and an expected base is
  used instead when one exists; `gh` today offers `--match-head-commit` alone,
  so the lease is the path today, and this spec says so.
- **a moved base is a refusal, not a report.** When the lease is rejected the
  tool prints `MERGE RACED entry=… expected=<sha12> found=<sha12>
  merge=<sha12>`, logs it, exits 1, and **the pass stops there**: nothing was
  published, the object stays under `refs/nova-merge/integration/` as
  evidence, and the next pass builds a fresh integration commit on the moved
  base and asks for its gate. There is no after-the-fact check, because the
  only thing that can land is the thing that was tested.

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
A read or a gate is first its own file, never edited, committed and pushed by
the tool (rule 22), so two records cannot collide on the branch; what can
collide is the fold into `state.json`, and every write of that is a
read-modify-write of one JSON file, so every write takes the same lane lock
(rule 1). The write goes to `state.json.tmp`, the fixed name of rule 1, and
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
  "lane_branch": "nova-merge/schema-main",
  "prs": [
    {"pr": 951, "needs_read": "yes",
     "reads": [{"who": "emma", "verdict": "approve", "note": "", "at": "2026-09-11T12:31:07Z",
                "head": "cbde1fc6ba10c1430f9f90615c70706ea7aaa29e",
                "file": "reads/951/emma-cbde1fc6ba10-20260911T123107Z-c4d5e6.json"}],
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
    {"pr": 949, "head": "<sha>", "base": "<sha>", "merge": "<sha>", "verdict": "green",
     "summary": "gates/949/<head12>-<base12>-20260911T130019Z-a1b2c3.summary",
     "at": "2026-09-11T13:00:19Z", "run": "a1b2c3",
     "file": "gates/949/<head12>-<base12>-20260911T130019Z-a1b2c3.json"}
  ]
}
```

`version` is written by `init` and checked first: a number this binary does not
know is exit 2 naming both numbers, before any other field is read. A gate
record's `base` and `merge` and a read's `head` are full 40-character shas and
all three are required; a record missing any does not decode (rules 18, 19 and
21). `reads` and `gates` are the **fold** of the record files in the lane
branch (rule 22): each carries `file`, the path of the record it came from,
relative to the lane, and a fold replaces both lists wholesale from the files;
**a record file that does not decode is never skipped**: the fold refuses it,
`FOLD REFUSED file=<path>: <reason>`, the file is preserved untouched, and the
entry whose directory holds it is `BLOCKED` for the pass — `MERGE BLOCKED
entry=<id> reason=malformed_record file=<path>`, exit 1, no publication of
that entry whatever its other records say, because the unreadable file may be
the hold or the newer red — and a file whose path names no entry (scope
indeterminate) stops the pass, `RUN STOPPED reason=malformed_record
file=<path>`, before any entry is read; the remedy names the file and the
verb that re-records it. (Stella, 2026-09-11: a malformed HOLD beside valid
approvals vanished from the decision, and an older green survived an
unreadable newer red; a printed NOTE does not make that safe.) The empty lane `init` writes is exactly
this shape with the three lists empty.

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
   of the fold takes the lane lock, a pass holds `run.lock` throughout, and a
   record is a file that needs no lock at all (rule 22).
8. **The base is not re-read immediately before the merge.** Here the base is
   a precondition on the publication itself (rule 21), and a base that moved
   is a refusal before anything lands.
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
    fits in the gap. Here the lane builds the integration commit, the gate
    proves that object by sha, and publication is a compare-and-swap push
    with the expected base as the lease (rule 21): `MERGE RACED` is a refusal
    before the write, and a plain push — which checks ancestry, not equality
    — is not used for anything.
27. **`read` stamps the entry's head at record time.** Here `read --head` is
    required and the verdict binds to the sha the reader supplied (rule 19).
28. **The lane is created by whatever verb runs first**, with the repository
    and the base from the environment. Here `init` is the one creation verb,
    the state is versioned, and every other verb refuses a directory that is
    not a lane (rule 20).
29. **A read from another machine is a bus note the coordinator transcribes,
    and a gate record is a line in one machine's state file.** Here each is one
    immutable file in the lane's branch, pushed by the tool and pulled by every
    lane on that branch (rule 22).
30. **The newest green gate is chosen and an older green outlives a newer
    red.** Here the newest record for `(entry, head, base)` decides whatever its
    colour (rule 18).

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
4. `--auto`, `--force`, `-f`, a bare `--force-with-lease` and
   `--force-with-lease=<ref>` with no sha, in any argument to the mutating
   helper: exit 1 and the sentence, before the command is built;
   `--force-with-lease=refs/heads/<base>:<40-char sha>` passes the guard only
   from the publication step of rule 21 (a source test finds one call site),
   and the fake remote records that every push it received was a
   fast-forward by exactly one commit.
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
   test asserts no function in `internal/merge` opens a file in the clone's
   work tree for writing; the integration commit is made by `git merge
   --no-ff` from two parents with no resolved file, and a build that stops on
   a conflict is aborted and asserted clean.
8. Two gate runs for one entry in one process get two clone names, neither is
   `gate-<entry>` alone, and a run's clean-up removes only the tree it made;
   the other run's tree is intact afterwards.
9. The gate runner's step list and per-leg target lists, read as data, equal
   the hosted `ci-fast.yml`'s, read from the workflow; a fixture with the java
   drift makes the test red.
10. A branch entry merges on a green gate plus its read with no hosted checks
    at all; a pull request onto `main` with no hosted checks waits.
11. Reads survive a restart and a lost state: `read` writes, the process
    exits, `status` prints the same approve and hold counts, and `STATUS OK
    reads=` equals their sum; `state.json` is deleted, `init` on the same
    branch and `add` of the same entry give the same counts from the fold;
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
    end; `--slots` or `--legs` missing is exit 2 naming the flag; `--slots 0`,
    `--slots 65`, `--legs 0` and `--legs 65` are each exit 2 naming the flag
    and the cap of 64, before any slot is touched; a fake leg that records
    whether `<slots-dir>/<k>` was locked at the instant it started finds it
    locked on every leg of every gate, and a mutation that spawns the first
    leg before the slot lock turns the test red.
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
    `NEEDS-GATE` with `gate=head`, `RUN BUILT` names a new merge sha M2 with
    parents `(X+A, B)`, `RUN NOTE` names `--head B --base-sha X+A --merge M2`,
    a red record for `(B, X+A, M2)` makes B `RED` with both shas in `detail`
    and A stays merged, a green one merges B and the published object is M2
    by sha; when the base has not moved, no second gate is asked for; `gate`
    without `--base-sha` or `--merge`, or with a 12-character one, is refused
    naming the flag, and `gate --base <sha>` is refused naming `--base-sha`; a
    state file with a gate record lacking `base` or `merge` is exit 2.
    **The two kinds**: a lane with base `rowan/step-2`, not `main`, gated by
    a record with `--head S --base-sha S --merge S` (S the base's own sha):
    the fake runner receives three equal shas, fetches S, verifies its sha,
    applies no parent check, `RUN BASE gate=green`, and the lane's first
    entry A with a green record `(A, S, M)` where M's parents are `(S, A)`
    merges; a mutation that applies the parent check to the base gate turns
    the test red at `RUN BASE`; a record with `--merge` equal to `--base-sha`
    and a different `--head`, or equal to `--head` and a different
    `--base-sha`, is refused by `gate` naming the two kinds, and the base
    gate for S never satisfies A's predicate.
    **Green then red**: a green record for `(B, X+A, M2)` at 13:00 and a red
    one at 13:05 make B `RED` and nothing merges; red then green merges; a
    mutation that picks the newest *green* turns the test red; a green and
    a red with one `at` to the second make B `RED`, and a mutation that
    folds them green-last turns the test red. **The race,
    three ways, and the untested object never lands**: (i) A's record is
    green for `(A, X, M)`, the fake remote moves the base to X+H after the
    pass's last read; the lease `refs/heads/<base>:X` is rejected, `MERGE
    RACED expected=X found=X+H merge=M` is printed, exit 1, the pass stops
    with no further entry touched, the remote's base is still X+H with M
    unreachable from it, and the next pass builds M' on X+H and waits for its
    gate; (ii) the fake remote's base is moved to A's head itself — an
    ancestor of M, which a plain push would accept — and the lease is
    rejected the same way; (iii) the lane's clone holds M2' with the same
    parents `(X, A)` as the record's M but a different tree: publication
    refuses with `MERGE FAIL … gate names M, not in the lane's clone`, nothing
    is pushed, and the tool never builds a replacement on the way to the
    push. A branch entry in race (i): identical, because the path is one. The
    fake remote records that every successful publication was a lease push of
    exactly the record's `merge` sha, and, on a fake host that offers a
    two-precondition merge, that `--match-head-commit <oid>` and the base sha
    were both supplied.
19. `read --who emma --head <sha> --verdict approve` from a second checkout
    of the lane branch on another machine (a second `init --lane-branch` of
    the same name against the fake remote) is one file in the branch,
    `READ OK … pushed=true`; the coordinator's next pass prints `RUN PASS
    pulled=1` and `status` shows `reads=1` with no name; `--reads <pr>` lists
    it with the sha and the file; a `read`
    whose `--who` is empty is refused with the remedy line; a `read` with no
    `--head`, or a `--head` shorter than 40 characters, is refused naming the
    flag; `READ OK` prints `current=false` when the sha is not the last
    pass's `oid`, and the record's `head` is the sha given, never the `oid`.
20. `init --lane <dir> --repo o/n --base main` on an empty directory writes
    `state.json` with `version: 1`, `repo`, `base`, `lane_branch` and three
    empty lists, checks out the lane branch (created with `.gitignore` on the
    fake remote, `joined=false`; a second lane on the same branch prints
    `joined=true` and creates nothing), clones `repo/`, and prints `INIT OK`;
    a second `init` on the same directory is `INIT REFUSED` exit 1 and the
    state is byte-identical afterwards; `add`, `read`, `gate`, `run`, `status`
    and `dry-run` on a directory with no `state.json` are exit 2 with the
    `init` command in the refusal and write nothing; `--repo`, `--base` or
    `--lane-branch` on any verb but `init` is exit 2 naming the flag, `gate
    --base <sha>` is exit 2 naming `--base-sha`, and `--base-sha` on any verb
    but `gate` is exit 2 naming `gate`; a state file with `version: 2` is
    exit 2 naming both numbers; the `### First run` in `docs/CLI.md` starts with
    `init` and a test executes it.
21. `TestOnlyTheGatedObjectIsPublished`: with a green record for `(A, X, M)`
    the fake remote receives exactly one push, a lease on `refs/heads/<base>`
    expecting X, moving it to M; the fake remote advanced to X+H between the
    pass's last read and the push rejects the lease, `MERGE RACED` names
    `expected=X found=X+H merge=M`, nothing reached the remote, and M is
    still under `refs/nova-merge/integration/` in the lane's clone; the
    remote advanced to A's head (an ancestor of M) is rejected the same way;
    a clone holding a different merge of the same parents than the record
    names is `MERGE FAIL` with no push; a source test finds the lease
    spelling at one call site and finds no `gh pr merge` call without a base
    precondition on the fake host that offers one; on the fake host that
    offers none, `gh pr merge` is never called at all; a fake remote that
    refuses the push for a protected branch prints `MERGE BLOCKED …
    missing=atomic_publication` with the remote's line, exit 1, nothing
    reached the remote, no `gh pr merge` and no plain push followed, and a
    mutation that retries with a plain push turns the test red; after a
    landed lease `MERGE OK` carries `verified=M`, and a fake remote that
    reports a different ref on read-back gives `MERGE FAIL … published
    object not at base` with the lease already recorded as sent.
22. `TestEveryVerdictSurvivesTheFold`: two readers on two lane checkouts of
    one branch and a coordinator on a third record, concurrently and from the
    same starting branch, two reads (`--head H1` and `--head H1` by different
    names), one gate, and one `add`; every push lands (the CAS loop retries
    on rejection), the branch holds three record files, the coordinator's
    next pass prints `pulled=3`, `status` shows `reads=2a/0h` and the gate,
    and each fold record's `head` is the sha its reader supplied, byte for
    byte. **Reject, reset, restore, retry**: against a real bare remote and
    two clones, `alice` and `bob` each record for `H` from the same tip;
    the loser's push is rejected, its reset moves to the winner's tip, the
    outbox restores its exact bytes, the second push lands, and the remote
    holds both files with the bytes each writer's outbox held, compared
    byte for byte; a mutation that drops the restore turns the test red
    with `pathspec … did not match`. **Kill at every boundary**: SIGKILL
    injected before add, after commit, after the rejected push, and after
    the landed push but before the confirming fetch; after each, the outbox
    still holds the item, re-running the same verb delivers it exactly once,
    and the remote never holds two files for one submission id. A push
    rejected five times is `READ FAIL … pushed=false` naming the
    file, the file stays in the outbox, and re-running the same verb pushes
    it and empties the outbox only after the confirming fetch; `alice`
    recording twice for `H` yields two files, the newer `at` wins the read
    condition, and one `at` to the second folds hold-last; `git status` in
    every lane is clean after every verb; a tripwire on
    every path opened finds no record file opened for writing twice, and a
    second tripwire finds every Git operation on a checkout — the loop, the
    pull, the fold, `dry-run`'s fetch — inside the checkout lock, with
    `status` and `read` on one checkout never interleaving. **The fold
    refuses**: a lane branch holding two valid approves for A and one
    truncated `reads/A/stella-…json`: `FOLD REFUSED file=…`, `MERGE BLOCKED
    entry=A reason=malformed_record file=…`, the fake remote saw no push,
    the file is byte-identical afterwards, and a mutation that skips the
    file and merges A turns the test red; a valid green gate for `(B, X,
    M)` beside a newer gate file for B that does not decode blocks B the
    same way; a malformed file at `reads/README` (no entry) is `RUN STOPPED
    reason=malformed_record` before any entry line. **`dry-run` is a
    snapshot**: with a record pushed to the remote after the coordinator's
    last pull, `dry-run` prints a plan that counts it and `lane_tip=` names
    the fetched tip, `state.json` and the checkout's tracked tree are
    byte-identical afterwards, and the state lock was never taken.
23. `TestThePacketIsPointersNotDiff`: a lane with three entries — one read
    by `emma` at H1 and now at H2, one never read, one approved current by
    her — `packet --who emma --all` prints two `PACKET ENTRY` blocks in lane
    order and not the third; the first's `last_read=H1 range=H1..H2`, the
    second's `last_read=- range=<base>...<head>`; two holds by `stella` on
    the first print as two `PACKET HOLD` lines with `holds=2`; a fixture
    diff carrying a distinctive token proves no printed line holds a diff
    line or a log line; `--max 1` prints one hold and a `PACKET MORE`; the
    lane's tree is byte-identical afterwards and the state lock was never
    taken.

## The work list

To build it in Go under `cmd/nova-merge`, the way `cmd/nova-bus` is built:
standard library only, no hardcoded paths, no default paths, the exit grammar
above, `internal/oneline` for every printed value, `internal/bounded` for every
listing, and `ONBOARDING.md`'s first-day standard — a usage banner ending in a
runnable `example:` block, refusals that say what the flag wants and report every
independent problem at once, a `### First run` in `docs/CLI.md`, a `quickstart`
verb, and tests that pin all three by executing them.

1. **`internal/merge/state.go`** — the state file: `version` checked first,
   strict decode (unknown field is an error), the two entry lists, the gate
   list with `base` and `merge` required, reads with `head` required, the
   fold of the record files into both lists (rule 22), the write under
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
6. **`internal/merge/gitops.go`** — the clone, fetch, checkout, the
   integration build under `refs/nova-merge/integration/`, the lease push of
   rule 21, all with the timeout, and the **mutation guard** that refuses
   `--auto`, `--force`, `-f`, a bare `--force-with-lease` and a lease with no
   sha before building a command, admitting only
   `--force-with-lease=refs/heads/<base>:<40-char sha>` from one call site.
   Tests: each refused flag, by exit code and text; every push the fake
   remote saw was a fast-forward by one commit (demanded tests 4 and 21).
6a. **`internal/merge/records.go`** — a read or gate record as one file, the
   commit-and-push CAS loop (fetch, reset to the fetched tip, add, commit,
   push, at most five rounds within `--timeout`), the outbox and its
   restore after every reset, the confirming fetch before an item is
   delivered, the checkout lock, the `--ff-only` pull before a fold,
   `dry-run`'s in-memory fold over the fetched tip, `.gitignore` written by
   `init`, and the fold that rebuilds `reads` and `gates` from the files.
   Tests: demanded tests 19 and 22; a record file that does not decode is
   `FOLD REFUSED` and blocks its entry, never skipped and never repaired.
7. **`internal/merge/read.go`** — the read condition: author exclusion, head
   keying to the sha the reader supplied, hold precedence, the stale count,
   the lane-wide totals. Tests: a hold beats three approves; an author approve
   does not satisfy; a stale approve does not satisfy and is counted
   separately; demanded tests 11 and 19.
8. **`internal/merge/pass.go`** — one pass: the base's evidence first and a
   red base stops (rule 6), walk the order, classify each entry into the
   closed state set, re-merge only what conflicts, build the integration
   commit for an admitted entry (`RUN BUILT`), evaluate `MERGE(entry)` as one
   function that is the only caller of the publication helper, publish the
   record's `merge` object under the base lease (or the host's
   two-precondition primitive), stop the pass on `MERGE RACED` with nothing
   published, merge at most one, stop. Tests: the storm does not happen (a
   clean entry is not touched after a merge, demanded test 3); a raced base
   is a refusal before the write; one merge per pass; a source test finds
   exactly one call site of the publication helper and it is inside the
   predicate's function; demanded tests 5, 6, 10, 15, 18 and 21.
9. **`cmd/nova-merge/main.go`** — the verbs, `init` first and creation-only
   with `--lane-branch`, `read --head`, `gate --base-sha` and `gate --merge`
   required, `--base` refused off `init` and `--base-sha` refused off `gate`,
   the flag parsing with this
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
12. **`internal/merge/packet.go`** — the reader's packet: entries needing
    `--who`'s read, `last_read` from the fold, the range, holds as pointers,
    `--max` through `internal/bounded`, no write and no lock (rule 23).
    Tests: demanded test 23.
13. **`docs/CLI.md`'s `### First run`** and the `quickstart` verb: `init` a
    lane with its repository and base, add one entry, print the status, with
    every path a flag (demanded test 20).

## Ideas folded on 2026-09-11

The table's ideas on coordination spend, and the issues, read against this
spec on 2026-09-11. `rule n` means the idea is now that rule; `already`
names the rule that held it before this pass; `not folded` gives the one
reason.

| source | the idea, in six words | disposition |
|---|---|---|
| Stella, spec repairs | later red invalidates an earlier green | already, rule 18; ties fold red-last (this pass) |
| Stella, spec repairs | publisher conditions on base, else BLOCKED | rule 21: `MERGE BLOCKED missing=atomic_publication`, `verified=` read-back |
| Stella, spec repairs | gate record names tested tree, run | `merge` sha names the tree; `run=` added (rule 22) |
| Stella, spec repairs | direct reads need a specified transport | already, rule 22: one immutable file, lane branch, CAS push |
| Stella, idea 2 | verdict by verb, bus carries findings | already, rule 19 |
| Stella, idea 4 | smallest sufficient review packet, pointers | rule 23, `packet` |
| Stella, idea 5 | one structured result, no receipt chatter | already, rule 19 (an approve is a command, no note) |
| Emma, C3 | batch reviews into one dispatch | rule 23, `packet --all` |
| Emma, A3 | direct read recording, no transcription | already, rule 19 |
| Rowan, idea 2 | reads by verb, findings only | already, rule 19 |
| Rowan, idea 5 | counts by default, lists behind flags | already, rule 12 |
| Freddy, idea 6 | diff-only context for reviews | rule 23: the range, never the diff itself |
| Freddy, idea 4; DeepSeek, idea 8 | cache or memoize routine verdicts | not folded: a verdict is a person's per sha (rule 19); a cache would be a verdict nobody gave |
| DeepSeek, idea 7 | a librarian returning snippets | not folded: a sixth tool, not a merge-lane rule |
| ideas #273 | evidence arriving after the belief | already, rules 18 and 19: every record is keyed to the sha it was made for |
| ideas #357 | reason about a note, never execute | already, the data paragraph at the top |
| nova-tools #35 | a shared branch keyed by the clock races | already, rule 22: records are immutable files, pushed under a CAS loop, ordered by `at` the tool wrote |
| Stella, closing read | durable outbox restored after every reset | rule 22: `<lane>/outbox/<submission id>`, restored after each fetch/reset, delivered only after the confirming fetch; one checkout lock; one immutable file per submission (test 22) |
| Stella, closing read | an unreadable record blocks, never skipped | the state file: `FOLD REFUSED`, `MERGE BLOCKED … reason=malformed_record`, `RUN STOPPED` when the scope is unknown (test 22) |
| Stella, closing read | a base gate is not an integration gate | output grammar: two kinds by shas; the parent check is the integration gate's only (test 18); `dry-run` folds in memory over a fetched tip (rule 22) |
