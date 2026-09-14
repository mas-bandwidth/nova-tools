# nova-work — specification (DRAFT 26, 2026-09-14)

**Status: a draft under joint authorship, Rowan and Stella, on Glenn's word of 2026-09-13.**
Nothing here is built. The Schema NEW Fixed Tables roadmap is the pilot, and the pilot decides
what this document keeps. Sections marked *(Stella)* are hers; sections marked *(Rowan)* are
mine; the rest is shared. Every requirement that is Glenn's cites its source so a reader can check the words: a
nova-tools#177 comment by id, or one of the five bus notes in which Stella reports his live
words of 2026-09-13 — stella-5adca9a1f09d (resident O, periodic clips, verbs for structure),
stella-461d99ec092d (the root is COW: closed, open, working; C is history, W is a view in O),
stella-deed78e54cb4 (link versus absorb, the initial import that loses no data),
stella-0ace603bdc22 (one coordinator, one live reader/writer), stella-fe600103fa2e (*"the
work set is the PRIMARY FORM"*, superseding 5653982211's hesitation), and Stella's two reviews, stella-ff217e98685c (five
points on draft 1) and stella-d205f6120ee7 (two findings on draft 5). Where a later word
supersedes an earlier comment, the later word governs and the earlier is named. **Two things in this document are the authors' proposals and not Glenn's requirements,
and they are marked where they stand: the lease model (Rowan's, from #177 comment 5654176537)
and everything in the section *Additions of the authors'*.** Draft 5 integrates Stella's
resident-session amendment (her 9757de0, on Glenn's clarification after draft 3: *the
coordinator loads S from the work repository, manipulates it in memory through nova-work
verbs, and periodically clips to Git; reloading and reparsing on every verb defeats the
purpose; both structure and state need verbs*) with the data, counting, query and validation
contracts of drafts 1 to 4, which the recorded Fable cold reads shaped. **The reads are the
record, and no count of them is kept in this sentence**: every whole read is a comment on PR
#231 with its repairs beneath it, and each draft's own comment names by id the read it folds,
so a number here can never go stale (drafts 1 to 4 were shaped by the HOLDs of 15:34Z, 15:47Z
and 15:49Z; drafts 20 and 21 folded 5655371246, 5655806486, 5655806825, 5655903904 and
5655905248; draft 22 folded the two whole reads at efc26a2e, 5655987384 and 5655988102; and this
draft folds Glenn's root refinement of 23:36Z, stella-461d99ec092d, and his live word on the
words and the letters that followed it).

**Draft 25 bounds the closed history, and the bound is Glenn's.** His words, live on 2026-09-13
in Stella's report of them: *"we only load the last 24 hours of closed history by default"*;
*"Across the last 2 days at most"*; *"This way it is bounded."* So the default window for loading
closed history is `[now - 24h, now)` in UTC; **at most today's and yesterday's day partitions are
resident**; pages and bytes are bounded even inside a busy day; older history is reached only by
an explicit historical query or a required indexed dependency lookup; and **no history is ever
deleted automatically**. The mechanism that carries those five sentences is **Stella's**, from her
additive amendment `docs/SPEC-WORK-CLOSED.md` at `4978e8e` on `codex/work-closed-day-partitions`,
which her disposition on draft 24 asked be integrated here rather than circulated as a competing
spec. **It is integrated into this file and not kept as a companion**, because this document's own
convention is one file whose authors' sections are named inside it — her resident-session, intake
and roadmap sections already live here, and the sibling `SPEC-*.md` files are sibling *tools*, not
amendments to one — and because her own rule is *do not create a second work-set owner*: two files
describing one C is exactly the second owner. Her **W1** (the whole-history loads) and **W2** (a
latest-row-only index promising state as of an earlier window end) are answered in *The execution
model — retention*, *The data — The root is COW*, *Counting*, *Queries — the contract*, *The
validator* and *Cost*; her scoped clearances at draft 24 are folded as cleared where each stands;
and her four inter-section questions are closed under *What this draft does not do*. Nothing of the
COW root, the remove-settles-only-open rule, the revive-appends rule or the worked acceptance
changes except where the bound changes its text, and each such place says so.

**Draft 26 integrates Stella's two further companions, and it is the same integration as draft
25's.** `docs/SPEC-WORK-PILOT.md` and `docs/SPEC-WORK-VALIDATION.md` at `81c2885` on
`codex/work-closed-day-partitions` — the resident engine and roadmap parity, and the preservation
and recovery acceptance — are folded **into this file**, for the reasons draft 25 gives and her own
lock gate asks for: *integrate this companion with the main spec into one unambiguous revision
before implementation approval*. Neither companion lands as a file; what each carries is below,
her sections marked hers, and **where a companion and this document disagreed the older sentence is
deleted rather than kept beside the newer one** — the six places are named in *What draft 26
changed in the older text*. **Where a companion left a syntax or a protocol open, this draft closes
it and says so**: every such decision is Rowan's, marked **(Rowan's decision, for review)** at the
sentence that makes it, and listed together in *Additions of the authors'*. **Three words are
fixed here and used nowhere else**: the letters are **C**, **O** and **W**; the set **O** is
*open* and is never called *active*; and **ACTIVE**, in capitals, is Glenn's per-friend live-data
node of *CONFIG and ACTIVE* below and is the only thing that word names in this document.

**One recursive work set, and its root is COW: closed, open, working.** The root is `(root C
O)` — **C** the closed work, **O** the open work — and **W**, the working view, is a predicate
within O and never a third branch, so `|W| ≤ |O|` always; *The root is COW* below is the whole
of it. Restricted Lisp data holds what is **desired** (the work:
repositories, streams, features, tasks, down to whatever depth is useful) and what is
**observed** (events: structure changes, transitions, evidence, attempts, leases, heartbeats,
scope changes). Everything **derived** — a task's current state, counts, percentages, views,
plans — is computed and cached in memory and never written as authority; a roadmap table, an
owner queue, a stream report and a percentage are each a **projection** of O at a named scope
revision, and none of them is a second store. `ROADMAP.md` is regenerated, never edited; a
hand-typed percentage is a bug (5653970526). **O is the primary form, in a persistent versioned repository** (Glenn, 2026-09-13, via
stella-fe600103fa2e: *"the work set is the PRIMARY FORM"*, which supersedes #177 comment
5653982211's *evaluate … only after … demonstrated*). What 5653982211 listed — lossless
import/export, stable identity mapping, provenance retention, restart/replay and
reconstruction — is kept as the gate on **migration**, not on the requirement: his own rule for
the initial import is *"We must not lose data"* (stella-deed78e54cb4), and Stella's migration
section below carries it. Nothing in this draft migrates or imports anything.

This spec is normative once it leaves draft. It is a sibling of [SPEC.md](SPEC.md), whose
**Conventions** — exit codes, no guessed paths, the one-line guarantee, the field escape, the
cap-and-count law, the version line — govern here unchanged. If the code and this document
disagree, one of them has a bug, and the tests decide which.

## The failures it closes

| the failure, from the record | what closes it |
|---|---|
| a task disappears after a context loss, a priority change or a handoff (#177) | O is resident, journaled locally on every accepted mutation, and clipped to the repository; a node's `:id` is stable through every rename; every change is an event with an author and a stamp |
| the work set reparsed on every command, so studying O cost a parse per question (Glenn, via Stella 15:49Z) | one load per session; verbs act on resident objects; an unchanged indexed query costs zero parses and zero replays |
| two workers on one task, neither aware of the other (nova-board's 2026-09-10 morning) | one live **lease** per node; a second `take` is refused and names the holder; there is one coordinator and one live O, so the refusal is authoritative (Glenn, via Stella 15:53Z) |
| a stored owner read as "working on it" while nothing moves | *responsible* and *working* are two facts: `:responsible` is durable accountability set by a person's word; *working-now* is a heartbeat inside a window (5654012267) |
| a percentage no evidence supports; an average of fractions rounded to green (5653970526) | done needs evidence bound to the task's acceptance criteria and verified; language completion is green cells / applicable rows; cell progress shows its numerator and denominator |
| the denominator moved and nobody saw it | every event that changes a required set increments the scope revision; a baseline is an event; additions append at the bottom; removals carry a reason; `percent` prints the baseline row count beside the current one (5654160320, 5653990830, 5649089106) |
| an old attempt's result quietly satisfying a corrected task (5653982211) | a task carries a `:generation`; a `correct` event bumps it; evidence of an older generation cannot close the task |
| studying O became pairwise work (5653973972, 5654049969) | counted containment is a forest, references are a graph; a fold visits each node and edge once over indexes built at load and updated incrementally; no transitive descendant sets are materialised; evidence fetching is a separate, bounded pass |
| a deferral or a cancellation counted as progress | `:deferred`, `:cancelled` and `:superseded` are scope events; they never enter the done count and the baseline denominator stays printed |
| the roadmap table edited by hand and the data left behind | `render --check` fails on drift; the table lives between two markers and O owns it |
| a hand-written change to structure with no record of who made it or why | structure is changed only by verbs, each an event with an author, a stamp and a reason |

## The execution model *(Rowan, on Stella's amendment; her sections below govern where they say more)*

**There is exactly one owning coordinator and one live reader/writer of O** (Glenn, via Stella
15:53Z: *"there must be one coordinator at a time. One reader/writer on the work set S in
memory via nova-work"; "Otherwise, we have races"*). Friends and workers submit results, evidence
and requested changes to that coordinator; they never open a second live O; other readers read
published, revision-labelled snapshots. **The mechanism that makes the rule hold across benches is proposed for the pilot, on the
substrate we already trust, and it fences MUTATION, not only publication.** The branch that
holds O carries an ownership record (`OWNER`: the coordinator's name, a **generation**, a
**token** drawn at random when the generation was taken, the stamp it was taken, and
**`until`**, the stamp the ownership lease expires). The token is written in two places and
nowhere else: the `OWNER` record on the branch and the taking session's own journal, so
**only a process holding that journal can resume the generation**; a second session under
the same name on another bench holds no journal with the token and is a taker, not a resumer.
**Two processes cannot hold one journal**, and the lock that says so is the journal's, not
the socket's (Stella's narrowed finding, comment 5654780545: `--session` and `--journal` are
independent arguments, so two sessions on different socket paths could otherwise share one
journal and both satisfy the resume predicate). At start the session takes an OS-held
exclusive lock (`flock`) on `<journal>.lock`, keyed by the journal's canonical path (realpath,
so a symlink or a relative spelling is the same journal), and holds it for its whole life; a
start that cannot take it refuses (exit 1, `journal held`, naming the holder's pid and socket
from the lock file's contents) and **never unlinks another process's lock**: a socket that
does not answer is an availability signal, not a death certificate, and a stopped or wedged
owner still owns its journal until its lease fences it. The kernel releases the lock when the
holder dies, so a crashed owner needs no cleanup. **The endpoint is locked too, by its own
lock and never by the journal's**: the session also takes an exclusive lock on
`<session>.lock`, keyed by the socket path's canonical spelling, and holds it for its whole
life; a start whose `--session` path carries a held lock refuses (exit 1, `socket held`,
naming the holder's pid and journal from that lock file) whatever journal that holder is on,
and **only a socket whose own lock is free or absent, and which answers nothing, is
unlinked** — so a live session on another journal never has its endpoint removed under it,
which was the second two-writer path. **The Unix order is stated, because two starters must
not both unlink one dead socket**: take `<session>.lock`, then probe the socket, then unlink
it only if it answers nothing, then bind — the lock decides who unlinks, never the probe. **The two locks are one predicate on two paths, and the
platform spelling is named rather than assumed** — and **what each platform is asserted to do
here is that platform's own documented contract, cited as such and not measured by this
document**: `flock(LOCK_EX|LOCK_NB)` on Unix,
`LockFileEx` with `LOCKFILE_EXCLUSIVE_LOCK|LOCKFILE_FAIL_IMMEDIATELY` on Windows
(SPEC-MERGE's spelling) on `<journal>.lock`, both released by the OS when the holder dies.
**`<session>.lock` is a file only where `--session` is a filesystem path**: it is that
socket's canonical spelling with `.lock` appended, in the socket's own directory. On Windows
the endpoint `--session` names is a named pipe (`\\.\pipe\<name>`), which is **not exclusive
by default** — two servers may otherwise open one name, which is the two-writer hole — so the
session creates its first instance with `FILE_FLAG_FIRST_PIPE_INSTANCE`, a second create of
the same name then fails with `ERROR_ACCESS_DENIED`, and **that first-instance creation IS
the endpoint lock**: the failure is the `socket held` refusal, there is no `<session>.lock`
file on Windows because a name under `\\.\pipe\` is not a path a lock file can be created
at, and nothing is ever unlinked, a pipe ending with its server. A journal copied to another bench carries the token but not
the lock, and the original process may still be alive: **a copied journal is never resumed**.
The journal records the bench (hostname and the lock's identity: its device and inode on Unix, its volume serial number and file index from `BY_HANDLE_FILE_INFORMATION` on Windows — again the platform's documented contract, and a pilot that finds either spelling behaving otherwise changes this paragraph rather than the code around it) that wrote it; a start on
a different bench, or with a different lock identity, is a taker (fresh generation after
`until` plus `--skew`, and its own new journal), and the copy's unclipped events go through
`session export` / `session replay` like any fenced owner's, never through a resume. So the
resume predicate is: the record names me, my journal holds its token, my bench wrote that
journal, and I hold the journal's lock (Stella's finding 1, comment 5654659093, narrowed in
5654780545). Three rules:

1. **Taking.** `session start` fetches the tip, reads `OWNER`, and takes ownership only if the
   record names nobody, or its `until` plus `--skew` is in the past, or it names this session
   and this session's journal holds the record's token (that is a resume: same generation, no
   bump, `until` advanced), or **it names this session as `successor`** — the handoff case of
   the handoff paragraph below, which takes the next generation at once and waits neither
   `until` nor `--skew`, because the old owner fenced itself before it wrote the record; it then pushes one
   fast-forward commit — **bumping the generation for a take or a handoff and keeping it for a
   resume**, which is all the resume parenthetical above means — that sets `until = now + 2 ×
   --every`, using
   a compare-and-swap push (`--force-with-lease=<branch>:<tip read>`: git refuses the push if
   the tip moved; **no history is ever rewritten** — the flag is the CAS, not a force). A
   refused push, or an `OWNER` whose lease is live and names another, is exit 1 naming the
   owner, generation and `until`, and the session never activates. A restart without the token waits like anyone else.
2. **Holding.** Every `--every`, the owner fetches the tip and **reconfirms**, in this order:
   first that the fetched tip **is the session's base**, then that `OWNER` on it still
   carries its generation and token; only then does it push a fast-forward commit advancing
   `until` the same way. **The session's base is the sha of the last commit it pushed**, a
   reconfirm as much as a clip, so its own reconfirms never read as divergence. **Divergence
   is a tip this session did not write, and the check is one predicate, `tip == base`, made
   before the CAS of every push the session makes** — a reconfirm, a clip, a handoff — stated
   here once and applied the same way everywhere (the CAS push guards the remote against a
   race with the push itself; the base check guards the session against a tip it did not
   write, which the CAS alone would accept and the next clip would then overwrite). A tip that
   is not the base is **raced**: the push is not made, the line is `<TOKEN> RACED …
   expected=<base sha> found=<tip sha>` (the shape of SPEC-MERGE rule 21, `SESSION` for a
   reconfirm, `CLIP` for a clip, `HANDOFF` for a handoff), and the session fences. A hand edit
   on the branch under a live session is therefore raced by the next reconfirm, never adopted
   as a base and never overwritten by a later clip. The raced session's accepted-but-unclipped
   events go where every fenced session's go: `session export --into <path>` writes its
   request bundle, and the next owner — a `session start` that loads the edited snapshot whole
   and validates it, after `until` plus `--skew` or by handoff — applies it with `session
   replay --from <path>`, each request validated fresh against the live O. **An owner that
   cannot reconfirm before its `until` fences itself**: it refuses every write AND every read
   except `session status` and `session export`, which are how a fenced session is inspected
   and recovered and which **themselves exit 0 on a fenced session**, answering about the
   fence rather than suffering it (every other verb: exit 1, `fenced`, naming its generation
   and the tip's if known),
   because a fenced session's resident O may be behind a new owner's and an answer from it
   would be a stale answer wearing a live one's clothes; it keeps its journal. Offline or
   partitioned, it fences at `until` without any network at all. So at no instant do two
   sessions accept mutations: the old owner is fenced by its clock at `until`, and a takeover
   is refused until `until` plus `--skew` has passed on the taker's clock. The bound this
   rests on is stated: the benches' clocks agree to within `--skew <duration>`, and the
   reconfirm cadence is `--every`. A fenced owner that later reconfirms successfully (its
   generation and token still on the tip, nobody took) unfences and continues; one that finds
   another generation stays fenced and exports. **Admission is checked per request, not per
   reconfirm**: every read and every write compares the session's clock to `until` at the
   moment it is admitted, and a request that arrives after `until` is refused `fenced` even if
   the reconfirm that would have advanced `until` is in flight; **a reconfirm's completion
   deadline is `until` itself**: its result is applied only if it began before `until`, its
   pushed commit is the tip, AND it completed before `until` on the session's clock; a
   reconfirm that completes after `until`, however early it began, is discarded and the
   session stays fenced until a later reconfirm succeeds whole (Stella, 5654780545, and the
   tightening in stella-6438e5513f41: the prohibition is on completion after expiry).
3. **Publishing.** Every clip carries the generation and is pushed the same CAS way; a late
   clip from a fenced owner is refused by the moved tip. A friend's request that reaches a
   fenced session is refused, not queued.

What this does not do, said plainly: it cannot make a fenced session's accepted-but-unclipped
events shared; they are recovered by the export path below, never lost and never merged. What
it does do is what Glenn's rule requires: one live reader/writer at a time, by the clock and
the branch together, on no backend but git.

A **session** is that coordinator's supervised, long-lived process, and it owns the one O.
**`session start` is the launcher and not that process**: it starts the session process, waits
for its `SESSION OK` line, prints it, and exits with that line's exit — 0 when `findings=0`, 1
for a red load — while the process it started stays up and serves reads, which is how a red
load's exit of 1 and its still-serving session are one sentence and not two. **The process the
launcher starts inherits the launcher's standard output and standard error until it has printed
that line**, so a red load's `WORK FAIL` findings and the `SESSION OK` under them are one
caller's output in order and not two processes' interleaved. **A supervisor that must itself be
the parent of the process it watches starts the session with `--foreground`**, where `session
start` *is* the session process: it prints the same line on its own stdout and then serves, and
its exit is the session's (Fable at d1b20f42, 2026-09-13: a launcher that exits and leaves a
child is what an init system refuses to supervise). What a supervisor holds is the session
process either way. That process loads the snapshot at the
fetched tip once (`--file` is the snapshot's path inside `--repo`, read at that tip, never a
free file), **opens — and does not load — the two index roots that same commit names**, the closed
index's and the dedup index's, which are read as bounded pages into a resident cache of at most
`--index-cache` pages and never whole (*Retention* below, on Stella's W1), replays its own journal
from that journal's newest boundary record beyond that snapshot once
(recovery, and the only replay),
validates the set whole, builds the indexes over O, and answers verbs against its resident objects
thereafter; `--at` answers from the retained history in memory, by a replay counted in `replays=`. **The CLI is a thin client**: `nova-work <verb> --session <path>` sends the verb to the
session listening at that path (a Unix socket the session creates at start; no default path)
and prints its one-line answer; a fresh CLI process is never a fresh parse. A reader who is
not the coordinator reads a published snapshot with `--snapshot <path>` in place of
`--session`, read-only, and its answers carry the snapshot's revision. The client is Go under
this repository's conventions; **the session's own language is Common Lisp** — Stella,
`docs/SPEC-WORK-PILOT.md` at `81c2885`, which supersedes *the pilot's decision* this sentence
carried through draft 25: typed data, stable-id indexes, recursive policies and incremental
updates live in one process — **and Lisp is not itself the complexity guarantee**: the maintained
indexes and the bounded access of *Cost* below are, imported s-expressions stay restricted data
and never reach `eval` or reader evaluation by *The data* above, and the engine's runtime
packaging and its supported platforms are pinned and tested before any release. The version line
is the client's; a session in another language answers `session
status` with its own `SESSION OK … build=<identity>` field, so every running binary says
which build it is. A session's identity, bounds, state, journal path, base revision and clip cadence are
explicit at start and readable at any time (`session status`, whose `SESSION OK` carries
`state=`, `every=`, `skew=`, `max-bytes=`, `max-depth=`, `max-nodes=`, `index-cache=`,
`page-bytes=`, `page-records=` and `closed-window=` beside the clip
cadence, so every bound is read rather than remembered), and it is stopped explicitly;
no always-on daemon is required, and a supervised session that a coordinator starts for a
sitting and stops at its end is enough (Stella, *Keep the work set alive*).

Every mutation is one typed event and carries a **request id**: `--request <id>` on every
mutation verb, drawn by the caller (a friend's request arrives with one), or drawn by the tool
and printed on the `OK` line when absent. The session validates the event against the current
local revision over the resident O as it would be with the event applied, appends it with its
request id to the **local recovery journal**, acknowledges only after the journal is durable,
then applies it to the resident objects, updates the affected indexes and invalidates the
affected derived values. A failed validation changes neither O nor the journal. **A retry with a
request id the journal already holds is answered by the same two-part test the dedup index
below makes, never by the id alone**: where the retry's payload digest equals the digest the
journal recorded for that id, it is answered with the original `OK` line and applies nothing,
which is how a crash between durability and acknowledgement yields one event (Stella, *Accept
locally, then clip into Git*); where the digest differs, it is refused at exit 1, `<MUTATION>
FAIL request=<id>: reused with a different payload`, because a requester that changed its
payload under an id this session has already applied must re-read rather than be answered about
the other one (Fable at 7472e545, 2026-09-13: collision detection was kept beyond the retention
boundary and dropped inside the journal, where the newest — and so the most retried — events
live). **The digest is named here, because a successor after a handoff may be another build or
another language** and two serializations of one request would read as a reuse on a legitimate
retry: it is SHA-256, printed as lowercase hex, over the **canonical serialization of the
request's payload**, and that serialization is stated whole here rather than left to a reader.
Each event is written as one parenthesized list: `:kind` and its value, then `:node` and its
value, then `:by` and its value, then the kind's own fields in the order the `:event` kinds
below list them for that kind — **every field written whether or not the caller gave one**, a
field the caller did not give written as `()`, so the body has a constant shape and two builds
cannot disagree about a default — each element printed by the same deterministic printer a clip
writes its snapshot with, one space between elements, no comments and no other whitespace.
**The digest covers what the requester sent and nothing the session assigns**: `:kind`, `:node`,
`:by` and the kind's own fields are in it; `:stamp`, `:clock`, `:request` and `:generation-owner`
are not, because a `:stamp` the session reads from its own clock under `:clock :tool` differs on
every retry and would refuse every one of them as a reuse. **A request whose envelope holds two
events digests both, the structure event first and the scope event second**, one space between
the two lists, as one serialization. So two implementations digest one request to one value
(Fable at 7472e545 and at efc26a2e, 2026-09-13: the definition named an ordered field list that
the `:event` kinds gave for no structure verb and for no scope kind, so one `node add` had two
serializations, and a legitimate retry against a successor of another build was refused
`reused with a different payload` — the exact hurt this paragraph exists to close). **Beyond the journal the promise is kept by the
dedup index of the retention boundary above, and it is kept as a refusal rather than as a
replayed answer**: a retry whose id the index holds is refused `already applied`, naming the
revision it was applied at, so the requester re-reads rather than acting twice, and no session
— a successor after a handoff among them — ever applies one request id twice.

**The repository branch that holds O has one writer too: the owning coordinator.** A **clip**,
the ownership commits of rules 1 and 2 above, the handoff commit below and `session stop`'s release commit are the only writes to it, and a clip: it names a local event boundary, fetches the upstream revision, and
**refuses, `CLIP RACED`, by the one base predicate of rule 2 (`tip == base`, the check the
reconfirm makes, not a second one)** — a moved upstream means a hand edit or a new
owner's take, and either is a handoff or a reload, never a merge — then validates
the resident O whole, writes one deterministic snapshot carrying the structure and the
retained event history, commits and pushes under `--git-timeout <seconds>` with `--attempts
<n>` (default 25 as the bus's; what an attempt retries here is the CAS push after a fetch
shows the tip unchanged but the push raced the same owner's own reconfirm commit, the one
moving-remote case this design allows), and records the shared revision and which local events it contains. **A transport retry and a
durable request-id retry are two different things, and `--attempts` is only the first** (Stella's
third inter-section question, closed here): `--attempts` with bounded exponential backoff inside
`--git-timeout` is the *network's* budget for one push, retrying a request the remote never
accepted; the dedup predicate above is the *request's* durability, retained across every retry, a
clip, a rotation and a handoff alike; and **a semantic conflict is never retried blindly** — a
`RACED` push, a `stale` expectation and a `reused with a different payload` each end their
attempt and are reported, because retrying a refusal is how one request becomes two events.
**A clip also appends one
boundary record to the journal**: the record carries the clip's commit sha, the base sha, the
clipped revision and the local event boundary the clip named, so the journal alone says which of
its events a clip has carried and at what clipped revision. That record is what lets the offline
export below write a bundle with no repository read, and it is where the *within the journal*
boundary of the dedup rule above is read. **That record is also where a read of the journal
starts, so the journal is not read from its beginning and need not grow forever**: a session
start, a `session export --journal` and every other read of a journal begin at its newest
boundary record and read forward, and **a clip may rotate the journal** — closing the file it
has just written that record into and opening a fresh one whose first record is a copy of it —
so a long-lived set's journal is bounded by the events since its last clip and not by the set's
age. The request ids before that record are the dedup index's, which is already the rule
*beyond the journal* below. A journal is read under the three bounds its reader named like every
other file, and one past `--max-bytes` is refused at exit 2 naming the bound and the file
(Fable at efc26a2e, 2026-09-13: nothing rotated and every start read the whole of it, so the
coordinator of a long-lived set reached a start refused at exit 2 for its own journal, with no
remedy but raising the bound every file is read under, while the retention paragraph promised
load bounded by the retention window) (Fable at 7472e545,
2026-09-13: the offline form *reads no repository*, and nothing said the journal held the base,
the clipped revision or the clip boundaries every bundle request requires). A failed push leaves
accepted local work and the pending clip intact and reports *locally durable, not shared*; a
divergence prints the upstream sha and the session's base and stops; no history is ever
rewritten (the CAS push above only fast-forwards) and nothing is reconciled. A session stop and a coordinator handoff request a clip under the
same flags, so a stop is bounded by the same timeout and budget. **A session whose clip is
refused by divergence is fenced**: it is fenced exactly as in rule 2 — every write and every
read refused `fenced` at exit 1, except the `session status` and `session export` that rule
names — keeps
its journal, and `session export --session <path> --into <path>` writes its accepted events since its base as a
request bundle — each event with its request id, expected revision and payload — which the
owning coordinator applies with `session replay --from <path>`, one request at a time,
validated fresh against the live O, refusing the stale ones by `--expect` and reporting each
verdict on its own line. Nothing is lost; nothing is merged without validation; the fenced
session's unshared work is a file, not a claim. **A fenced session that dies before it exports
leaves no journal a verb cannot read**, which is what *nothing is lost* would otherwise not
cover: `session export --journal <path> --into <path> --max-bytes <n> --max-depth <n>
--max-nodes <n>` is the offline form. It reads that journal under the caller's own three bounds
while holding the journal's own lock and writes the same bundle — **each request's required
`--expect` taken from the newest clip boundary record in that journal, and the bundle's `base=`
from that record's commit sha**, the commit that clip pushed, which is how the offline bundle is
whole without reading a repository. **It is the record's commit sha and never its base sha**,
because a live `session export` writes `base=` from the session's base, *the sha of the last
commit it pushed* (rule 2 above), which after a clip is that clip's own commit; taking the
record's base sha would make the offline bundle of a journal one commit behind the live bundle
of the same journal (Fable at efc26a2e, 2026-09-13) — and **starts no session, takes no ownership, reads no repository and validates
nothing** — a bundle is requests, and `session
replay` is where they are validated. A journal whose lock is held is refused, exit 1, `journal
held`, naming the holder, because a live owner's journal is not a file to be read out from under
it (Fable at d1b20f42, 2026-09-13: a fenced owner killed by its supervisor at the end of a
sitting stranded its accepted events in a journal no verb read). `render` writes a working-tree
file that the next clip commits; it is not a second write path to the branch.

**The periodic clip is the same clip, run by the session on two triggers named at start.**
`session start` takes `--clip-every <duration>` and `--clip-after <n>`, both required, both
distinct from the reconfirm's `--every`: the session clips when its pending accepted events
reach `--clip-after`, or when `--clip-every` has elapsed since the last clip with at least one
event pending, whichever comes first; a clip with nothing pending is not run. The periodic
clip uses the same guard (`tip == base`), the same `--git-timeout` and `--attempts` given at
start, and prints the same `CLIP OK` / `CLIP RACED` / `CLIP FAIL` line; a write admitted while
a clip is running is accepted, journaled and pending for the next one. Both values print on
`SESSION OK` at start (`clip-every=<duration> clip-after=<n>`) and on `session status`, so the
cadence is readable, never assumed. Glenn's *periodically clips to Git* (stella-5adca9a1f09d)
is this paragraph, and it meets Stella's *Clip cadence is configurable* — her sentence names
no flags; `--clip-every` and `--clip-after` are Rowan's spelling of her requirement, and the
claim here is only that they satisfy it.

**Retention is explicit, and the snapshot a session loads is bounded by `--retain` whole.** The
structure and the retained events are the snapshot; **the two indexes that may not forget — the
dedup index of request ids and the closed index of C — are published beside it, on disk, in
bounded pages, and are never loaded whole.** An earlier draft's
heading called the whole snapshot *bounded by construction* while its own index bullet said the
index grows with history, and a reader who believed the heading would size a bench by `--retain`
alone (Fable at 7472e545, 2026-09-13); draft 24 corrected that heading and left both indexes
*inside* the snapshot file, so the sentence was honest and the load was still one row per request
id and one row per settled item of the whole history at every start — **calling a thing an index
does not bound the memory it is read into** (Stella at e79847fb, 2026-09-13, W1, and her amendment
`docs/SPEC-WORK-CLOSED.md` at `4978e8e`, whose layout, recovery and gap rules the five paragraphs
after this list are). **The bound is Glenn's, and it is the header's**: *"we only load the last 24
hours of closed history by default"*; *"Across the last 2 days at most"*; *"This way it is
bounded."* `session start --retain
<duration>` is required and names how much history a snapshot carries: **the revision it names
is the newest clipped revision whose commit stamp is older than the clip's own stamp less
`--retain`**, so the boundary moves forward at a clip and never backward (Fable at d1b20f42,
2026-09-13: how a duration named a revision was unstated). Every clip writes three
things into its one deterministic snapshot: the structure; the **retention boundary**, the
derived state as the tool computed it at the revision `--retain` names; and every event after
that boundary. The events before it are written **unchanged and in order** into a sibling
**retention archive** file the same clip commits, named in the snapshot's header with its
revision range — a different file, and a different purpose, from the absorb archive of the
intake sections below, which preserves a source issue's content before a deletion. Nothing is
deleted: a retention archive is provenance, retrievable, and never read on the normal path.
**This document calls neither of them a checkpoint**: *checkpoint* is Stella's word below for
the clip commit her sections publish, and one word for two things is how a reader learns the
wrong one. **So the retention boundary and the two published indexes together carry everything the normal
path reads, or that sentence is false. It is a closed list, and it is the baseline of record every
rule compares forward from** — the first two bullets and the last are in the snapshot, and the two
index bullets name what the same clip publishes beside it, each bullet saying which:

- **per node** — its derived state, generation, scope revision, source revision, lease state,
  and, **for every event of its evidence set, that event's five fields: `:pointer`,
  `:criterion`, `:against`, `:stamp` and `:generation`** — for every event, not only for the
  ones a live `:to :done` names — because `stale=` is a fact of *every* evidence event (an
  `:against` that is not the node's current source revision), `freshest=` is the newest
  evidence stamp under the scope, `verify` prints one `VERIFY ROW` per evidence event and
  derives each verdict from the criterion's kind and subject and the node's generation at the
  event, and rules 5 and 17 and every rollup's `done-unverified=` read the same fields; a list
  that kept them only for the events a done already names would send `verify` to the archive
  for evidence gathered on a task still in `:review`, or cited later by a `state --to done
  --evidence <old-id>`, and the sentence above would be false (Opus at d1b20f42, 2026-09-13);
- **per container** (a `:work-set`, a `:feature`, a `:roadmap`) — its required set at the
  boundary **and its baseline members**, the membership its last `:baseline` recorded, because
  rule 11 compares against that baseline plus the scope events since and rule 14 against the
  set derived at the point of a re-baseline; with them the two counts a reader is promised,
  the baseline row count `baseline-rows=` prints and the `since-baseline=` of every ask;
- **the request id of every accepted event before the boundary, with the revision it was
  applied at and the digest of its payload** — the dedup index, three fields per event. It grows
  with the set's whole history rather than with `--retain`,
  and it is kept because **an index that forgets is not one** — **so it is not in the snapshot**:
  the clip publishes it under its own versioned index root, in pages bounded by `--page-bytes` and
  `--page-records`, and a session holds only the pages its bounded cache is holding, by the
  paragraphs below. **Retaining every past request in one resident map is the same failure under
  another name** and is refused here in words, since no test can see it on a small fixture
  (Stella, W1). **The predicate it serves is the
  index together with the request ids and payload digests of the retained events**, and that
  pairing is what makes the covered range the whole history up to the tip with no gap: the index
  holds every event before the boundary, the retained events the snapshot carries hold their own
  `:request` and payload from the boundary to the tip, and the window between the two — the
  newest events, which are exactly the ones a friend retries — belonged to neither before this
  sentence, so a retry inside `--retain` applied twice against a successor (Opus at 7472e545,
  2026-09-13). A retry of an id the predicate holds is refused at exit 1, `<MUTATION> FAIL
  request=<id> applied=<rev>: already applied`, and a retry of an id it holds whose payload
  digest differs is refused `<MUTATION> FAIL request=<id>: reused with a different payload`,
  which is how a reused id is caught rather than obeyed; it is a refusal and not a replayed
  answer because the original `OK` line is not retained and an invented one would be a worse
  answer than none. The
  index is published by the clip and named by the commit the clip pushes, so it survives a clip and
  crosses a handoff with the clip the handoff makes (Stella 5655371246 item 2, Fable and Opus at
  d1b20f42, 2026-09-13: a successor starts with *its own journal*, so before this bullet the
  once-only promise ended at the handoff and a friend's retry applied a second time). **A dedup
  page the predicate needs and cannot read is a refusal to admit that request and never evidence
  that it is new**: exit 1, `<MUTATION> FAIL request=<id> page=<name>: dedup unavailable`, because
  a retry answered as new is the once-only promise broken in the one place a friend can feel it
  (Stella, W1) (replay `dedup-page-unavailable-refuses`);
- **one closed-index row per `:settle` and per `:revive` of C** — the row the root section below
  enumerates: its `:id`, `:under`, repository, kind, category, disposition, the state and
  generation it settled at, its scope and source revisions, its required-leaf count at settle, the
  event's revision, stamp, author and request id, and its evidence events' five fields. **The rows
  are append-only, one per transition and never one per item**, which is Stella's W2 and the
  paragraphs below. It grows with the set's whole history rather than with
  `--retain`, for the same reason the dedup index does — C is append-only and what it holds is
  read from these rows, never from a body, so an ask over C costs no archive read and a rollup
  over a container whose members have long since finished costs no more than one whose members
  have not — and, for that same reason, **it is published beside the snapshot and not inside it**,
  under its own versioned index root, in the same bounded pages;
- **the lease log from each node's newest lease with no release and no handoff onward** —
  *live* would drop a lease past its deadline, and an expired unreleased lease is exactly what
  `stale` and `expired=` must still see — which is what `handoffs` answers from.

**C is partitioned by day, and the day is the event's own.** A closure record is written into the
UTC day of the event's recorded stamp — `closed/<yyyy>/<mm>/<dd>/`, an illustrative layout and not
a second public interface — in **bounded immutable segments**, each carrying a stable event id, the
item id, the event revision and the recorded stamp. **A reopen or a correction is appended at its
own event time and references the original identity**: no old closure event is ever rewritten,
migrated into today or deleted because the item is open again, which is what makes a past answer
stay past. **A dated manifest names that day's segments with their revision and stamp bounds,
their hashes and their record counts**, so a reader knows what the day is supposed to hold before
it opens anything, and **a busy day has many segments**: *one file per day* is not permission for
one unbounded read. **Recorded event time chooses the partition and the revision remains the
authoritative ordering** — a backdated `--now` or a skewed clock does not let an index assume time
is monotonic with revision — so a selection reads the intersecting days by the date index and then
applies the exact half-open `[from, to)` bounds inside them, and never lists every historical file
and filters afterwards (Stella, `SPEC-WORK-CLOSED.md`).

**Both indexes are versioned roots of bounded pages, and the clip's small root names them.** An
index root names its pages; a page locates partitions by day and records by stable item and event
id and by repository; **every page is bounded by `--page-bytes <n>` and `--page-records <n>`, both
named at `session start`**, and a page that would pass either is split into another page by the
clip that writes it rather than written past the bound. A lookup is **bounded indexed access and
never a promise of O(1)**: a cold lookup may read as many pages as the index is deep, and what
this document promises is that the number is bounded by that depth and by nothing about how much
work the team has finished. **The indexes are rebuildable from the retained canonical events and
the closure records**, which is what makes them an index and not a second store; a key-value cache
in front of them, if a pilot wants one, is a cache and nothing more.

**The resident cache is bounded, and the default window is Glenn's two days.** `session start
--index-cache <n>` is required and names the greatest number of index pages the session holds
resident across both roots and the day manifests together; it prints on `SESSION OK` and on
`session status`, like every other bound. **The default closed-history window is the rolling
`[now - 24h, now)` in UTC** — not the last 24 calendar dates and not all of yesterday plus today
— named by `session start --closed-window <duration>`, default `24h`, **refused at exit 2 above
`48h`** because the resident bound is two day partitions and a flag that could ask for three would
be the bound removed by a number. At startup and on a default closed-history query the session
**opens at most the two UTC day partitions that interval intersects**, today's and yesterday's; at
exactly `00:00:00Z` the interval is yesterday's whole day and only yesterday intersects it, which
is one partition and not two. **The time range is bounded and the volume inside it is not**, so the byte, record and page
bounds hold inside the window too: the default window is read in bounded pages, `--max` caps the
rows, and truncation and continuation are printed rather than assumed. **An ongoing session
advances the rolling window and evicts expired pages from the cache, least recently used first,
and evicting a page deletes nothing** — `--retain`, the archive and the partitions are untouched by
a cache. **Older history is reached only two ways**: an explicit historical query, which is an
`--ask` whose `--from` reaches before the window, and a **required indexed dependency lookup** —
rule 2 resolving a name that is in C, `released=` reading a settled release task through the
reverse-dependency index, a rollup reading a settled member's row — each of which is one bounded
indexed access for the one id it needs and never a day opened whole. **No retention cutoff and no
automatic deletion is introduced by any of this** (Glenn; Stella, W1) (replays
`default-window-opens-two-days`, `busy-day-many-segments`, `history-grows-startup-does-not`).

**One revision names them all, and one recovery brings them back.** A clip publishes **one**
revision naming together: the snapshot of O, the immutable closure segments it has written, the
closed index root, the dedup index root and the day manifests. It **stages the new immutable
files, verifies the hashes its manifests name, then commits the root and every file it references
in that same commit** — so a reader never sees a root pointing at a file that is not there, and a
failed push leaves the work locally durable and unshared exactly as the clip paragraph above
already says, reconciled against the exact remote commit before the publication is retried and
never by advancing a shared receipt because the commit exists locally. **Recovery is the one
replay this document already has**: the session loads the snapshot, opens the two roots at that
same commit, and replays its journal from the journal's newest boundary record, **applying every
`:settle` and every `:revive` it passes as an overlay on the index it has opened**, before the
whole validation and before any ask is answered — the overlay is what the next clip writes into
the pages, and until it does, a query reads the pages and the overlay as one. So a crash between a
settle and the next clip leaves no id in both branches and none in neither, whether the crash fell
before the acknowledgement, after it and before the clip, or inside the publication (replay
`index-replayed-after-crash`).

**An absent day and a missing segment are two different answers, and the manifest is what tells
them apart.** A day with no manifest inside a complete manifested range **means no events that
day**: the answer is the rows there are, `gap=0`, and no note. **A manifest or a segment the
committed root names that is missing or corrupt is a coverage gap**: the listing prints the rows
it can answer, `gap=<n>` and one `QUERY NOTE coverage-gap file=<name> range=<rev>-<rev>`, and
never an empty closed set, because *a missing archive produces an honest coverage gap, not an
empty completed set* (Glenn, 23:34Z). **A query that asks what an item's state was, as of a window
end whose partition it cannot read, refuses rather than answering from a newer row**: exit 1,
`QUERY FAIL ask=<kind> as-of=<stamp> partition=<yyyy-mm-dd>: historical window unavailable`,
naming the one partition it would need, so the caller reads a refusal it can act on instead of a
number it cannot check. **A cached summary may answer an independent query with its provenance and
can never make missing evidence read as verified.** These three are one rule stated three ways: an
answer says which question it answered and over what it could actually read (replays
`absent-day-is-not-a-gap`, `missing-segment-is-a-gap`, `as-of-refuses-unavailable-partition`).

A `handoffs --since <revision>` whose revision is before the loaded snapshot's boundary is
refused exactly as `--at` is (exit 1, `QUERY FAIL … : since=<rev> boundary=<rev> before the
retained history`, naming the retention archive that holds it). A session start loads the
snapshot alone — the retention boundary, then the retained events — and opens the two index roots
without loading them, so **load cost is bounded by the retention window and by `--index-cache`,
and by neither the set's age nor how much work the team has finished**; `--at` reaches the
boundary and no further; and **a session whose archive file is absent answers every ask and runs
every rule**, which is the test that keeps this paragraph honest. **A clip whose snapshot would
exceed the session's own `--max-bytes` refuses**, and **the refusal prints all four numbers and
names the remedy that can actually work**: `CLIP FAIL … : snapshot=<bytes> retained=<bytes>
index=<bytes> closed-index=<bytes> past
--max-bytes=<n>, <remedy>`, where `snapshot=` is the whole file the clip would write — the word
means the whole file everywhere else in this document and is not narrowed here — `retained=` is
the structure and the retained events, and `index=` and `closed-index=` are the bytes this clip
would publish into the dedup and closed index pages **beside** the snapshot, printed so an
operator sizes the bench by every file the clip writes and not by the one `--max-bytes` governs
(Fable and Opus at efc26a2e, 2026-09-13: two numbers were printed and the first was the whole
file's name over a part's value; and a fourth number is printed because a second index that may
not forget arrived with the root's C branch). **The remedy no longer branches, and the reason it
does not is this draft's change**: every part of the snapshot is now bounded by `--retain`, so the
remedy is always `lower --retain or raise --max-bytes` and there is no longer a part no flag can
lower. **An index page never refuses a clip at all**: a page that would pass `--page-bytes` or
`--page-records` is split into another page, which is what a paged index on disk is for, and the
bound a growing history meets is the bench's own disk rather than a session's resident memory.
The one file a
tool must never write is one it cannot read back, and a remedy that cannot move the number it
names is worse than no remedy at all (Fable at 7472e545, 2026-09-13: the refusal named
`--retain` for an overflow `--retain` cannot shrink; Opus at efc26a2e, 2026-09-13: branching on
the larger part told an operator with `index=55 retained=50` under a bound of 100 to find a
bigger bench, when lowering `--retain` on his own keyboard would have done; Stella at e79847fb,
2026-09-13: while the indexes were in the file, an operator could reach a clip no flag of his
could pass) (replay `clip-names-the-index-that-overflowed`). **A removed subtree leaves the live snapshot the same way**:
the snapshot's structure is O's tree, and a node removed by `node remove`, its subtree
and their events are written into the archive by the first clip after their `:remove` event
passes the retention boundary and into the snapshot's structure never again — **their
closed-index rows staying in C's index**, which is how a removal is still answerable, by id
and by repository, after the archive is gone — so `--retain`
bounds the live structure whatever has been removed from it, and the refusal above names a flag
that moves the part that overflowed.

**Handoff is a verb, and it is the one way an owner ends without a successor's wait.**
`session handoff --session <path> --to <name> --git-timeout <seconds> [--attempts <n>]`: from
the moment it is admitted the session refuses every further write (`fenced`); it clips under
the same guard, so a hand edit under it races the handoff too; it then pushes one CAS commit
that writes `OWNER` with the same generation, `until` set to the handoff's stamp (released
now) and `successor=<name>`; prints `HANDOFF OK … generation=<n> to=<name> commit=<sha>`; and
exits fenced, its journal kept, nothing pending. The successor's `session start --as <name>`
reads an `OWNER` that names it as successor and takes the next generation at once — no `until`
plus `--skew` wait, because the old owner fenced itself before it wrote the record — with a
fresh token and its own journal, loading the handoff's clip. A start by anyone else sees a
released record and waits `--skew`, as after a stop. **`session stop` is the same sequence
without a successor**: clip (unless `--no-clip`), then `OWNER` released with `until` at the
stop's stamp, so a taker after a planned stop waits `--skew`, not `until` plus `--skew`. A
handoff whose clip is raced changes no `OWNER` and exits fenced like any raced session; its
bundle goes by export and replay.

## The data *(Rowan)*

**Restricted Lisp, read as data.** A work file is a sequence of s-expressions made only of
lists, keywords, strings and integers, with `;` line comments, which the reader discards. The
reader refuses, at exit 2 with one line naming the byte offset, every dispatch macro (`#.`
first among them) and every other form the source forbids as evaluation (5653982211); the
full list of refused syntax is an authors' addition, listed at the end. Nothing read is ever
evaluated. **Every verb that reads a file takes the same three bounds**, `--max-bytes <n>
--max-depth <n> --max-nodes <n>`, none defaulted: a file past any of them is refused at exit 2
before parsing finishes, and a missing bound is `refusing to guess`. **Every file a session
reads is read under the three bounds its `session start` named** — its snapshot, its archives
when a maintenance read asks for them, its journal, its verification cache and every bundle
`session replay --from` reads — so a client verb addressed to a session names no bounds of
its own and no file is ever read unbounded; under `--snapshot` the reader's own three bounds
govern the snapshot and the cache alike. A file past a bound is refused at exit 2 naming
which bound and which file, never truncated. Unknown keys on a node
are preserved and ignored, so a team may carry its own fields; an unknown `:type` is a
refusal, because a type names the rules a node is checked by (5653990830).

**One resident set, one journal, one snapshot.** O lives in memory in a supervised session (the
execution model, below): its **structure** — nodes, `:id`, `:type`, `:children`, `:deps`,
`:acceptance`, `:responsible`, axes and cells — and its **log** — every event below — are both
held there, both changed only through typed verbs, and both written to the repository only by a
**clip**, as one deterministic snapshot carrying the structure and the retained history. Nothing
in the normal path is hand-written; the snapshot is data a person may read, and a hand edit
to it is a maintenance act outside the verbs, loaded and validated whole by the next session
start, never the way structure changes in use. Every mutation is an event: a structural
verb appends a structure event and a scope event where a required set changes, so every
transition is kept rather than overwritten (#177: *preserve transitions and corrections rather
than overwriting the history*).

**A node.**

```lisp
(:id "schema/fixed-tables/versioning/cpp"   ; stable, never reused, never carries display text
 :type :work-set                             ; the kinds are listed below
 :title "C++ versioning"                     ; display only; may change freely
 :children ("schema/cpp/read-older"          ; COUNTED CONTAINMENT: this node owns these
            "schema/cpp/refuse-newer")
 :deps ("schema/shared/lock-rules")          ; REFERENCE: needed, not owned, not counted here
 :responsible "emma"                         ; durable accountability, set by a person's word (an event records who set it)
 :category "feature"                         ; free label; taxonomy TBD (5654164074)
 :links ("https://github.com/mas-bandwidth/schema/issues/898"))   ; an issue is a link, never a type
```

**Containment and reference are two different edges, and the difference is the whole cost
model** (5654049969). `:children` is canonical containment: every node has at most one
containment parent, the containment edges form a forest, and a node is **counted once**, under
that parent, wherever else it is referenced. `:deps`, a roadmap cell's `:ref`, a view's
membership and any other pointer are references: they form a graph, they carry no count and no
cost, and they are validated for existence and for dependency state. Shared compiler and lock
work in Schema is owned once, under an explicit owning work set that every repository carries
(`<repo>/shared`, visible in every listing, 5654164074, and created `:required false` by the
validator's rule-7 paragraph below, which also names the verb that makes it count), and referenced from nine cells; it is
one task with one cost. Stella's *A cell is a reference, not another state store* below says
the same rule from the roadmap's side and is not restated here.

**Kinds.** Features, tasks, attempts and leaf subtasks are distinct units (5654012267), so
they are distinct kinds:

- `:work-set` — a container. Its completion is its required children's completion; an empty
  required set is **never** done.
- `:epic` — a container above features, one level of **Glenn's agreed hierarchy**
  (repository → roadmap → epic → feature → subtasks, with sub-features and subtasks recursive
  beneath). It is a `:work-set` by every rule; it is a kind of its own because **a row kind must be
  queryable and never inferred from a title word** (Stella, `docs/SPEC-WORK-PILOT.md`).
- `:feature` — a work-set whose completion is what a roadmap row counts; the unit of "green
  feature cells / applicable rows". **A feature decomposes into sub-features recursively**, each a
  `:feature` under it, and the counting unit of every rollup stays the one `unit=` names.
- `:roadmap` — a typed view over its cells: `:axes` (ordered, named members), `:cells` mapping
  a coordinate to a `:ref`, `:scope-revision`, `:completion-policy` (`:all-required-features` is the only policy in this
  draft). A cell references a node; it never contains state of its own. An unknown axis member and a
  duplicate coordinate are refusals; **a missing cell is not**, and an omitted cell is never
  complete (5653990830). A cell may be marked `:out-of-scope` by a recorded scope event, which
  is distinct from unstarted and from unknown, and **an out-of-scope cell leaves that axis
  member's applicable rows** (5654160320: *fully green features / applicable features*).
  **The members of a roadmap's first axis are its rows, and a row is a node of the roadmap's
  declared `:row-kind`** — `:feature` by default, and `:epic` or `:work-set` where the roadmap
  declares it, each with the `:aggregation` policy that says how a row of that kind rolls its
  members up. **This amends draft 24's feature-only rule** (Stella,
  `docs/SPEC-WORK-PILOT.md`: *amend draft24's feature-only first axis rule for explicitly declared
  row kinds and their aggregation policies*); the kind is declared on the roadmap, read by rule 2,
  and **never inferred from a node's title**. The
  members of every other axis are columns. **The axis and cell layer is optional, and it is
  optional whole**: a roadmap may declare `:axes ()`, in which case its rows are its declared
  members in order, there are no coordinates and no cells, and **no synthetic cell is interposed
  between a feature and its subtasks** — the optional split on a second dimension (a language, a
  platform, a backend) is what introduces cells at all, and a roadmap that does not split has
  none. A projection to a table needs an explicit two-axis selection, or fixed selections for the
  further axes, and **a matrix is never silently flattened**. Adding a column is an `:axis` scope event of the
  roadmap and adding a row a `:discovery` one, each a scope revision by the delta table below;
  removing one is not
  completion.
- `:task` — work with `:acceptance`, a list of the criteria that close it, **one schema**:
  `(:id "c1" :kind :test :subject "test:internal/lockfile/TestLockRule1@<rev>" :predicate
  :passes)`, where `:kind` is `:test`, `:job`, `:merged` or `:attested`, `:subject` names the
  exact thing the evidence must be about (a test name, a job name, a PR number, or for
  `:attested` the criterion text a reviewer signs), and `:predicate` is what must be true of it
  (`:passes`, `:succeeds`, `:merged-at`, `:attested-by`); `node add --acceptance` takes exactly
  this form, and an evidence pointer qualifies a criterion only when its kind matches, **its
  subject is the criterion's subject** (a passing test of another name qualifies nothing) —
  and **where a scheme's pointer names no subject of its own, the subject the resolver was
  passed and the cache was keyed on is that subject**, so the comparison is made on a fact
  established about the right thing and never on a boolean about some other one — its
  predicate holds at the named revision, and its `:generation` is the task's current one, so a
  `correct` event makes every earlier qualification void for the done claim (Stella's finding
  2, comment 5654659093); `:required` (default true), and a current state, generation,
  evidence set and blocked reason **all derived from its events**. A task with no `:children`
  is a **leaf subtask**, the unit `unit=leaves` counts; a task with children is counted by its
  leaves, never itself; so the four units of 5654012267 are `:feature`, `:task`, the leaf
  `:task`, and the `:attempt` event. A node may carry `:private true`; `render` never writes
  a private node or its descendants into an output file, and a public view that reaches one
  through a parent prints `private=<n>` and nothing of it (5653982211). A task has no stored worker; who is working
  on it is answered from leases only; who is responsible is `:responsible`, inherited down the
  containment forest until overridden. A task may carry `:version "<text>"`, the name of the
  release it ships, **read by the `released=` field of the disposition row below**, so *what
  release contains this fix* is answered from the release task that tracks it and never from a
  second store.
- `:lease` — the ownership-of-execution record, below. *(The authors' proposal, not Glenn's.)*
- `:event` — the log. **`:by` is the event's author on every kind and never anything else**;
  where an event names another node — a supersede's replacement — the field is
  `:superseded-by`, and the verb spells it the same way. Every event carries
  `:kind`, `:node`, `:by`, `:stamp`, `:clock` (`:tool`
  or `:given`), `:request` (the request id), `:generation-owner` (the coordinator generation
  that accepted it), and the fields its kind needs:
  - `:structure` — the structural half of a structure verb's envelope. Its first field is
    `:verb`, the verb that wrote it, and the rest are the fields that verb changed, **in this
    order per verb, which is also the order the payload digest above serializes them in**:
    `node add` — `:verb`, `:node-type`, `:title`, `:under`, `:category`, `:required`,
    `:acceptance` (the criteria in the order given), `:reason`;
    `node remove` — `:verb`, `:under` (the parent it detaches from), `:reason`;
    `node require` — `:verb`, `:to` (`true` or `false`), `:reason`;
    `decompose` — `:verb`, `:children` (in the order `--into` gave them), `:acceptance` (per
    child, in that child order), `:reason`;
    `accept` — `:verb`, `:add`, `:remove`, `:reason` (the one of `:add` and `:remove` the call
    did not give is written `()`, as every absent field is);
    `dep` — `:verb`, `:add`, `:remove`, `:reason`;
    `axis` — `:verb`, `:roadmap`, `:axis`, `:add`, `:reason`;
    `cell` — `:verb`, `:roadmap`, `:coord`, `:ref`, `:out-of-scope`, `:in-scope`, `:reason`.
    `responsible` and `source` write no `:structure` event: their own kinds below are their
    whole record.
  - `:transition` — `:to <state>`, `:reason`, and for `:blocked` a `:blocked-by` reference; a
    `:to :done` names the evidence event ids it stands on.
  - `:evidence` — `:pointer`, `:criterion` (an `:acceptance` id on the node), `:against <sha>`
    (the tree it was read against), `:generation` (the node's, at the time of writing),
    `:attempt` (optional).
  - `:attempt` — `:model`, `:bench`, `:started`, `:ended`, `:result` (a pointer), `:usage` (a
    pointer to a token record, #181), `:generation` (the task generation it answered).
  - `:correct` — a correction to a task: `:reason`; bumps the task's `:generation`
    (5653982211).
  - `:review-attest` — a reviewer's attestation that a result satisfies an `:attested`
    criterion: `:criterion`, `:result` (a pointer), `:against <sha>`, `:generation` (the
    node's, at the time of writing), `:by` the reviewer. **It is an evidence event**: it
    carries `:pointer` = its `:result` and is what a `:to :done` names for an `:attested`
    criterion, under the same generation rule as every other evidence event.
  - `:responsible` — `:to <name>` on a work-set or feature, on a person's word, `:reason`.
  - `:lease`, `:heartbeat`, `:release`, `:handoff` (carrying the new holder's `:deadline` and
    `:default`, so a handed lease is a whole lease) — the lease log, below.
  - `:baseline`, `:discovery`, `:remove`, `:require`, `:defer`, `:cancel` (carrying
    `:evidence` that the worker stopped), `:reopen`, `:split`, `:supersede`, `:scope`, `:axis`,
    `:source`, `:settle`, `:revive` — the scope log: a baseline records the required set of its node **as the tool
    computed it at that moment**, member by member; a split names the new children; a
    supersede names `:superseded-by`; every one of them increments the node's scope revision,
    because the revision is the count of scope events, and each changes the required set by
    **its own stated delta, in the table below, which is what rule 11 compares against**.
    Splitting is decomposition, not discovery or completion (5653970526). **Each scope kind's
    own fields, in the order the payload digest above serializes them**: `:baseline` —
    `:members`, `:reason`; `:discovery` — `:members`, `:reason`; `:remove` — `:reason`;
    `:require` — `:to`, `:reason`; `:defer` — `:reason`; `:cancel` — `:evidence`, `:reason`;
    `:reopen` — `:reason`; `:split` — `:children`, `:reason`; `:supersede` — `:superseded-by`,
    `:reason`; `:scope` — `:coord`, `:in-scope` (`true` or `false`), `:reason`; `:axis` —
    `:axis`, `:member`, `:reason`; `:source` — `:to`, `:reason`; `:settle` — `:disposition`
    (`done`, `cancelled`, `superseded` or `removed`), `:reason`, `:already-closed` (the ids
    beneath a removed node that were already in C, empty on every other settle, by the removal
    paragraph below); `:revive` — `:reason`.
    **`:settle` and `:revive` are the session's own half of another verb's envelope and are
    outside the payload digest**, with `:stamp`, `:clock`, `:request` and `:generation-owner`:
    the requester never sent one, the session derives it from the transition it accompanies, and
    a digest that covered it would make two builds disagree about a field neither was given (the
    root section below). **The session-written events of an envelope are these two and the
    `:release` a settle writes for a live lease, and there are no others**: all three are outside
    the payload digest and every other event of an envelope is the requester's and is digested,
    so two builds digest one request to one value whether or not the item it settles was held,
    and a retry of that request is answered by its original `OK` line (replay
    `settle-outside-the-digest`).

**A revision moves on every scope event; a denominator moves only where the delta says.**
This table is the per-kind required-set delta rule 11 checks, and the verb that may write
each kind:

**A required set is a container's DIRECT required members, never its leaves**, and **a scope
event on a member is a scope event of its containment parent and of every roadmap that has it
as a row**: it counts in each of those nodes' scope revisions as well as its own node's,
and rule 11 folds it into each of their sets by the row below. That is why the third column
says whose set each kind moves; without it the first `:cancel` anywhere would leave a parent's
set changed with no event of the parent's to explain it, and rule 11 would go red on the walk
after it — the silent denominator of 5654160320 wearing a green shirt. **There is one statement
of a roadmap's membership and the other is deleted**: this sentence said *every roadmap whose
cell references it*, draft 19's membership, while the table's `:cancel` row and the replay list
said *every roadmap that has it as a row*, so one implementer reddened where the other did not,
and a row whose cells are not yet mapped — legal by the `:roadmap` kind above — is referenced by
no cell while its cancel is still the roadmap's event (Fable and Opus at 7472e545, 2026-09-13).

| kind | required-set delta | the set it moves | written by |
|---|---|---|---|
| `:baseline` | sets the set to the members it records | the event's node's own | `event` |
| `:discovery` | adds the named member at the bottom of the listing | the event's node's own | `event`, **refused at exit 2 naming `axis --add` where `--node` is a `:roadmap`**, because a roadmap's set is its first axis's live members and a discovery through `event` would seat a member on no axis; `node add` inside its envelope, **refused at exit 2 naming `axis --add` where `--under` is a `:roadmap`**, for that same reason and by that same sentence (Opus at efc26a2e, 2026-09-13: the hole was closed in `event` and left open in its sibling verb, which seats a member in the set and on no axis); `node require --to true` inside its envelope; **`axis` inside its envelope, where `--add` names the roadmap's first axis** — the member is the row it adds, the node is the roadmap |
| `:defer`, `:reopen` | **none** — the node stays required and stays in every denominator | none | `event` |
| `:cancel`, `:supersede` | removes the event's node from the set | its containment parent's, and every roadmap that has it as a row | `event` |
| `:split` | **sets the split node's own required set to the children it names**; its parent's set is unchanged, because the split node is still one required member of it | the event's node's own | `decompose` only |
| `:remove` | removes the event's node from the set, and detaches it from its parent's `:children` | its containment parent's, and every roadmap that has it as a row | `node remove` only |
| `:require` | with `:to false` removes the event's node from the set; with `:to true` adds it at the bottom of the listing, as a `:discovery` does. **It detaches nothing**: the node stays in its parent's `:children` either way, which is the whole difference from `:remove` | its containment parent's own | `node require` only |
| `:scope` | **none** — it takes a cell's coordinate out of (`--out-of-scope`) or back into (`--in-scope`) the named axis member's applicable rows, which is `percent`'s denominator and not a required set | none — the roadmap's revision moves, its set does not | `cell` only |
| `:axis` | **none** — a member of an axis that is not the first is a column, and its applicable rows begin at its cells | none — the roadmap's revision moves, its set does not | `axis` only |
| `:source` | **none** — it moves the node's source revision, which stales evidence, not membership | none | `source` only |
| `:settle` | **none** — it moves the item from O to C, and a member that has finished is still a member: what takes one out of a set is the `:remove`, `:cancel` or `:supersede` in the same envelope, by its own row above | none — the node's revision moves, no set does | `state --to done`, `event --kind cancel`, `event --kind supersede` and `node remove`, each inside its envelope, and never `event --kind settle`, which is exit 2 |
| `:revive` | **none** — it moves the item from C back to O, at `:todo` by the transition table | none — the node's revision moves, no set does | `event --kind reopen` inside its envelope, and never `event --kind revive`, which is exit 2 |

**A roadmap's required set is the members of its first axis whose node is live** — live
meaning not removed, not cancelled and not superseded — **and the axis list is not the set**.
Each row is the node of the roadmap's declared `:row-kind` whose completion that row counts; a row enters the set with the
`:discovery` an `axis --add` on the first axis writes; and it leaves the set, **while staying on
the axis**, with the `:remove`, `:cancel` or `:supersede` of its feature, by the delta table
above. **The two differ by exactly the rows that have left, and that is the design**: `axis`
has only `--add`, nothing takes a member off an axis, and `node remove` keeps the member on the
first axis (the removal paragraph below says so). **And the set is derivable with the retention
archive absent**, which is what makes rule 2's treatment of a departed member safe: the
retention boundary carries each container's required set as the tool computed it at the
boundary, and the retained scope events move it forward, so a row that left before the boundary
is simply not in the recorded set and no event and no node has to be read for it. A definition
that made the set the axis members outright would have rule 11 derive ten from the axis and nine from the baseline plus its
deltas after the first `event --kind cancel`, and go red on every walk thereafter — the
silent-denominator hurt of 5654160320 inverted, a red on an honest cancel (Fable and Opus at
efc26a2e, 2026-09-13: the definition and the delta table were both live and said two different
things). **`rows=` is the cardinality of the set and never of the axis**, on every line, and is
never the language-completion denominator: the denominator of `percent --axis <member>` is the
**applicable rows for that member** — the live rows less those with a recorded out-of-scope
cell for it — and it prints under its own name, `applicable=<n>`, beside `rows=`, so both
numbers are on the line and neither stands for the other (Opus at 7472e545, 2026-09-13: `rows=`
carried two meanings, and on a roadmap with one out-of-scope cell the printed pair gave a
percentage the counting rule did not name, while the number that rule divides by was on no
line). **`rows=` and `baseline-rows=` are the roadmap's own and are not per axis member**;
`applicable=` is the only per-member count on the line; and `since-baseline=` is the scope's own
count in members, by its own bullet in *Counting* below. **A cell is a reference and moves no required set**: mapping,
re-pointing or clearing a coordinate changes the roadmap's projection and not its denominator,
which is why `cell` writes no scope event but the `:scope` of `--out-of-scope` and `--in-scope`;
an `:out-of-scope` cell moves that axis member's applicable rows and never this set; a member of
any other axis is a column, which is why `:axis` moves nothing. (Opus at d1b20f42, 2026-09-13:
draft 19 made this set the cell *targets*, so one feature row over nine language members put
nine members in the set while `rows=` counted one — a denominator off by the size of the axis,
and the exact number 5649089106 asked to be honest.)

So a `:defer` that moved a denominator would be a deferral counted as progress, and the table
is why it cannot be; and rule 11 has the per-kind arithmetic it needs rather than the claim
that every scope event changes a required set. **`event --kind` carries only the kinds whose
delta needs no structure change** — `baseline`, `discovery`, `defer`, `cancel`, `reopen`,
`supersede` — and refuses `split`, `remove`, `require`, `scope`, `axis`, `source`, `settle` and `revive` at
exit 2 naming the verb that writes each — `settle` and `revive` each naming the verbs whose
envelopes carry them — the four that write a `:settle` and the one `event --kind reopen` that
writes a `:revive` — because a branch move with no work behind it would be a ledger entry
nobody earned — because structure changes only by verbs: a scope-only `remove` would leave
the node in its parent's `:children`, and rule 11 would go red on the next walk.

**States and transitions, derived.** A task's current state is the `:to` of its newest
transition, where **a scope event of kind `:defer`, `:cancel`, `:reopen` or `:supersede` is
also a transition for its node** (to `:deferred`, `:cancelled`, `:todo` for a `:reopen`, or
`:superseded`), or `:unknown` when it has none. **A `:reopen`'s target state is `:todo`** and is
named here rather than inferred: `:todo` and `:doing` differ for `who`, for `stale` and for
every `state` that may follow, so a reopened node that landed in a state an implementer chose
would be a different set for every implementation (Fable and Opus at d1b20f42, 2026-09-13). **A `:cancel` is admitted only from
`:cancel-requested`** — the one edge the table below gives it — and an `event --kind cancel`
on a node in any other state is refused by rule 10 as an invalid transition, so the scope log
is never a way around the transition table. `:unknown` is explicit and is neither
zero nor not-started (5653970526). Its generation is the count of its `:correct` events. The
allowed transitions are a table the validator holds: from `:todo` to `:doing`, `:blocked`,
`:cancel-requested`; from `:doing` to `:blocked`, `:review`, `:done`, `:cancel-requested`;
from `:blocked` to `:doing`, `:cancel-requested`; from `:review` to `:doing`, `:done`;
from `:cancel-requested` to `:doing` (withdrawn) or, by a `:cancel` scope event carrying
evidence that the worker stopped, to `:cancelled` (5653982211: *cancellation requests are
distinct from a confirmed stopped worker*); from `:unknown` to any non-terminal state by a
transition that carries evidence or a reason. **The four scope transitions are in the same
table and have the same edges, so rule 10 checks them as it checks the rest** (Fable and Opus
at d1b20f42, 2026-09-13: three of the four had none, and a gate with no edge guesses): a
`:defer` from `:todo`, `:doing`, `:blocked`, `:review`, `:cancel-requested` or `:unknown` to
`:deferred`; a `:supersede` from the same six to `:superseded`; a `:cancel` from
`:cancel-requested` only, to `:cancelled`; a `:reopen` from `:done` or `:deferred` to `:todo`.
So a defer of a done node is refused by its missing edge rather than by a second sentence, and
`:done` and `:deferred` are still left only by a `:reopen`. **`:deferred`, `:cancelled` and `:superseded`
are never targets of `state`**: they are entered only by their scope events (`defer`,
`cancel`, `supersede`), which record the transition and move the revision; `:done` and
`:deferred` are left only by a `:reopen` scope event, which is itself the transition;
`:cancelled` and `:superseded` are terminal. A
`:to :done` transition must name evidence events whose criteria cover every `:acceptance`
entry of the task, and every evidence event carries the task's `:generation` at the time it
was written; a `:to :done` naming evidence of an older generation is refused, whether or not
an attempt is named (5653982211). A required task with no `:acceptance` entry can never be
done, and rule 15 names it. A transition to `:blocked` without `:blocked-by` is
refused.

**Evidence is a pointer the validator can fetch, bound to a criterion.** Resolving a pointer
proves that the thing exists; **the criterion it names is what it proves** (Stella, 15:38Z,
point 1), so an evidence event carries both, and an evidence event whose criterion is not an
`:acceptance` entry of its node is refused. This draft ships five schemes — `commit:<sha>`,
`run:<owner/repo>#<id>@<sha>`, `pr:<owner/repo>#<n>@<sha>`, `file:<path>@<sha>`,
`test:<package>/<name>@<sha>` — and a sixth, `note:<scheme>:<id>`, for any team's message
store, so that no family's bus is named in the tool (5653970526).

**Every scheme names one fact, and a resolver the operator names is what establishes it; the
tool itself knows no forge.** `session start` takes `--resolver
<scheme>=<command>`, repeatable, one per scheme this session may fetch; **a scheme with no
resolver is unreachable**, counts as unverified, and is never guessed at and never assumed to
be some house's forge. `verify` runs a scheme's resolver once per cache key, inside
`--max-fetch <n>` and under `--fetch-timeout <seconds>`, **executing the command directly and
never through a shell** — the pointer is caller text, and an `sh -c` here is an injection —
passing **two arguments, the pointer and the criterion's subject**, and reading one line on
stdout: `<fact> <stamp>`,
where `<fact>` is the token `holds` or `absent` and must agree with the exit, and `<stamp>` is
an RFC 3339 UTC stamp naming when the resolver established it. Exit 0 the fact holds, 1 it
does not — **a negative is a fact and is cached like any other**, so a failed run is answered
offline — 2 it could not be established; any other exit, a `<fact>` disagreeing with the exit,
no line, or a line past `--max-bytes` is *unreachable*, and an unreachable answer is never
cached. The fact each resolver must establish, and nothing wider:
`commit:<sha>` that commit exists; `file:<path>@<sha>` that file exists at that sha;
`test:<package>/<name>@<sha>` **that named test ran at that revision and passed** (a recorded
result of that run, read from wherever the bench keeps it — the tool never re-runs a test and
never infers one test's result from another's); `run:<owner/repo>#<id>@<sha>` **that the job
the second argument names succeeded in that run at that sha**; `pr:<owner/repo>#<n>@<sha>` that
the pull request is merged at that sha; `note:` has no resolver and qualifies nothing. What a resolver
is — a forge API call, a log read, a stored artifact — is the operator's, per bench, so no
forge, no CI product and no family's bus is named anywhere in this tool.

**A `note:` pointer is
accepted on a heartbeat and on an attempt's `:usage`, never as evidence for `:done`**
(5649089106: *status messages are not completion evidence*); rule 5 refuses it. **Resolution is a separate
pass from validation, and resolving is not qualifying** (Stella, stella-ff217e98685c points 1
and 2, and stella-d205f6120ee7 finding 1): `verify` marks a pointer *verified* only when the
thing it names **qualifies for the criterion's kind** — a `:test` criterion by a `test:` pointer
that passed at the named revision; a `:job` criterion by a `run:` pointer whose `@<sha>` is
the revision the evidence event was written against (`:against`) and for whose run the resolver
established that the criterion's subject — the job name — succeeded; a `:merged` criterion by a `pr:` pointer merged at the named
sha; an `:attested` criterion only by a `:review-attest` event naming the reviewer, the
criterion, the result pointer and the revision (a `note:` or a bare `commit:`/`file:` never
qualifies anything by itself, and a green aggregate run never qualifies a whole feature:
5649089106, *CI activity and status messages are not completion evidence*). Any other pairing
is *found-not-qualifying* and counts as unverified; a pointer the fetch cannot reach is
*unreachable* and counts the same. `check` never fetches;
`verify` fetches, under
`--max-fetch <n>` and `--fetch-timeout <seconds>` (both required where it fetches, as
SPEC-BOARD requires `--gh-timeout`, and **both refused under `--offline`**, which fetches
nothing and so budgets nothing), **through the session's one cache and through no flag of its
own**.
**The cache holds raw resolutions, never verdicts.** Its key is **the pointer, the subject the
resolver was passed, and the identity of the resolver that answered it**. **A resolver's identity
is the command string `session start` was given after the `=` in `--resolver <scheme>=<command>`,
verbatim and unnormalized** — not a digest of a binary the session never reads, and not an
operator label an implementer would have to invent — so two sessions given one command line agree
on it with no fetch and no comparison of benches (Fable and Opus at 7472e545, 2026-09-13: this
string is the cache key's third field and the snapshot reader's whole guard, and what it was was
never said) — the revision is
inside the pointer, in every scheme but `commit:`, whose sha *is* the pointer — and its value
is the fact that resolver established and the stamp it was established at. **This is the repair
of a pointer that named half a proposition** (Stella 5655371246 item 1, Fable and Opus at
d1b20f42, 2026-09-13): draft 19's `run:<owner/repo>#<id>` carried no revision and no job, the
resolver was passed the pointer alone and was asked to prove *that the named job succeeded at
the revision the criterion names*, and the cache was keyed on the pointer alone — so one cached
`holds` qualified every `:job` criterion at every head, which is the green-aggregate-run hurt of
5649089106, and an honest resolver could not answer at all. The fact is now whole in the request
and whole in the key, and it is still a raw fact and never a verdict: what a criterion makes of
it is derived locally, below. **A reader under `--snapshot` names no resolvers and fetches
nothing**: it accepts a fact in the cache it was given **only where that fact's resolver
identity is one the snapshot's header names** — every clip writes the identities of the
resolvers its session was started with — counts every other fact as unverified, and prints
`fetched=0`, so its verdicts are the coordinator's last fetch and never an identity a handed
cache invented (Stella 5655371246 item 1). **What that pin protects is said plainly, so nobody
reads more into it**: it refuses a fact attributed to a resolver the coordinator never named, and
it does not and cannot refuse a different binary standing behind the same command string on
another bench — what a resolver is remains the operator's, per bench, exactly as the resolver
itself is (Fable at 7472e545, 2026-09-13). **One session has one cache, and this is the one sentence that says where a
cache is named**: the path is `session start --cache`; a `--cache` on `check`, `verify` or
`query` addressed by `--session` is refused at exit 2; under `--snapshot` the reader's own
`--cache` is required and is the only one; and **`verify` has no `--snapshot` form at all**,
because a snapshot reader fetches nothing and the verbs that read a snapshot are `check` and
`query`. So `verify` names a cache on no path, the grammar below gives it none, and two paths
can never be named for one session's facts. **Qualification is per evidence event and is never cached**:
whether an event is *verified* is derived at read time from that raw fact together with the
criterion's kind, subject and predicate and the node's generation at the event, so a
`correct` event or an `accept` edit changes every affected verdict with no fetch at all, and
two events naming one pointer for two criteria are two verdicts over one cached fact.
`VERIFY ROW` therefore prints one verdict per `<event-id>`, not one per pointer (Stella's
finding 2). `verify --offline` derives verdicts from the cached facts and fetches nothing. **A pointer that
does not resolve marks that evidence event `unverified` and changes no task's recorded
state** — the log is never rewritten by a fetch — **but no count is ever green on unverified
evidence**: in every rollup a `:done` whose evidence is not all verified (by `verify`, through
the cache named on the read) counts as `unknown`, never as done, and the answer prints
`done-unverified=<n>` beside `done=<n>` (5653970526: *report unknown or a clearly labelled
verified lower bound*; 5654176537 rule 4). The same holds for **stale** evidence, which is
local and needs no fetch. **A source revision has one home and one verb.** It is a field of a
repository work set — `(:type :work-set :repo "<owner>/<name>" :source-revision "<sha>")` —
and it is inherited down the containment forest exactly as `:responsible` is, overridden where
a node carries its own, so **every node has exactly one**, and **a scope that is not one node's
subtree has none**: a line whose scope spans more than one repository work set prints
`source=-`, never one repository's sha standing for the rest. A task in no roadmap has one, and a
task referenced by nine cells still has one, because a cell is a reference and carries no
state. A roadmap carries none. `nova-work source --node <id> --to <sha> --reason <text>` is
the one way it moves: one `:source` event, a scope event whose required-set delta is none, so
staleness both arrives and clears by a recorded act with an author and a reason, and the first
source bump is not a state nothing can leave. An evidence event whose `:against` is not its
node's current source revision is stale, `check` counts it (`stale=<n>`), and a `:done`
standing on it counts as `unknown` in every rollup (5653970526: *source changes can
invalidate old proof*).

**The lease** *(Rowan's proposal, #177 comment 5654176537, changed here from that comment in
three ways: expiry is derived and never stored, so `:expired` is not a state; an expired lease
is a count and never a finding; `:extend-once` is defined)*. A lease is an event; its current
state is derived from its heartbeat, release and handoff events.

```lisp
(:kind :lease :id "l-3f9a1c0e7b2d"            ; the tool's own id, random hex, never a count
 :node "schema/fixed-tables/versioning/cpp"
 :by "emma" :stamp "2026-09-13T14:41:11Z" :clock :tool
 :deadline "2026-09-13T21:00:00Z"
 :default :release)                  ; :release | :extend-once | (:escalate "<name>")
(:kind :heartbeat :lease "l-3f9a1c0e7b2d"
 :by "emma" :stamp "2026-09-13T15:02:00Z" :clock :tool
 :evidence "note:bus:emma-841138a3b056")
```

A lease never expires into done. **Expiry is derived**: a lease whose deadline is behind the
read's clock, with no release or handoff, reads as expired in every answer, the node reads as
unowned for execution, `check` counts it (`expired=<n>`), and the responsibility on the node
is untouched (Stella, point 4: *expiry ends the claim, not accountability or evidence*).
`:extend-once` means: at the first expiry the lease reads as extended by the same length,
once, and a `stale` answer says so; at the second it reads as expired. **`(:escalate
"<name>")` means: at expiry the claim ends exactly as `:release` ends it** — the lease reads
expired, the node reads unowned for execution, the responsibility on the node is untouched —
**and the node additionally reads `escalated-to=<name>` in every answer until a new lease or
a release**; `check` counts it (`escalated=<n>`) and `who` and `stale` print it on the row.
Escalation is a reading, not an act: it assigns nobody, sends nothing and names no transport,
because a tool that messaged a person here would be naming one house's bus. Working-now means a
heartbeat inside `--window`; a live lease with no heartbeat in the window is *held, not
worked*, and the answer says so. One live lease per node; a handoff is a `:handoff` event
naming the new holder, so the transition is a record and not an overwrite. **A `release` whose
`--as` is not the lease's holder is refused**, exit 1, `LEASE FAIL node=<id> holder=<name>
since=<stamp> deadline=<stamp> live=<n>: held`, naming the holder: a claim is ended by the one
who made it, by its deadline, or by a `release --handed` from that holder, and never by a third
name reaching in. A lease with no
`:deadline` or no `:default` is refused at write time: a deadline with no default is a wait
with no end.

**The root is COW: closed, open, working.** The root is `(root C O)`, and its two branches are
**C**, the closed work — everything that has settled, kept for good and never thrown away —
and **O**, the open work — everything still to be done. **W**, the working view, is
`(working O)`: a predicate over O, never a third branch, so `|W| ≤ |O|` always and nothing is
in W that is not in O (Glenn, 2026-09-13 23:36Z, via stella-461d99ec092d, which supersedes the
three-independent-state-branches shape of the note before it, and his live word the same night
that fixes the letters and the words: the set this document called S is **O**, *open*, never
*active*, because active is what W means; the done history is **C**, *closed*; W stays W).
**`(root C O)` and `(working O)` are this section's notation for the shape and are never a
file's text**: no work file holds either form — a work file is made only of lists, keywords,
strings and integers, by the reader's paragraph above — and C, O and W are derived from the
events the file already carries, which the `:settle` and the `:revive` below are.
**Where the record quoted in this document says S it means O**; the quotations keep the word
the record used, and *live rows* in *Counting* below is the roadmap denominator's own term
and names no branch of the root; the word *active* names W's predicate and no set of rows. **And `ACTIVE` in capitals is a third
thing and the last one this document spells with those letters**: it is the per-friend live-data
node of *CONFIG and ACTIVE* below, Glenn's own word for it, defined there once and never used of a
branch, a row, a session state or a lease.
**This is a partition and not a second ledger**: C and O hold
the same nodes, the same events, the same evidence and the same ids this document already
defines, told apart by one derived fact, and no verb, count, rule or file below is duplicated
for C (Glenn, 23:36Z: *reconcile this with the existing lease/state/event semantics, not a
duplicate ledger*).

**Every top-level child of O is a repository work set**, `(:type :work-set :repo
"<owner>/<name>")`, including repositories that hold research, planning or a friend's own
work; a cross-repository goal is a view over canonical owned nodes, never a copy
(5654164074, Glenn's preferred simplification, *to be prototyped before it is an invariant*).
O is the team's authorized known work, never a scan of every reachable repository. A GitHub
issue or PR is a `:links` entry on a node, not the node's type; intake from issues is in
Stella's section.

**The move from O to C is an event, and the item's id, its history and its evidence move with
it unchanged.** An item settles when its work has ended: a `:to :done` transition, a `:cancel`,
a `:supersede` or the `:remove` of a `node remove`. The verb that writes that event writes one
more in the same envelope — a **`:settle`** event carrying `:disposition` (`done`, `cancelled`,
`superseded` or `removed`) and `:reason` — one journal record, all-or-none, one `OK` line, one
request id, exactly as a structure verb's envelope already is. The `:settle` is the session's
own half and **is not in the request's payload digest**, with `:stamp`, `:clock`, `:request`
and `:generation-owner`, because the requester never sent it and two builds must digest one
request to one value. The reverse is the same shape: a `:reopen` of an item in C writes a
**`:revive`** event in its envelope and the item is in O again, at `:todo`, by the transition
table (a `:reopen` of a `:deferred` item writes none: a deferred item never left O).
**A `:revive` takes nothing out of C, because C is append-only.** The item's closed record and
every event under it stay exactly where they are; the `:revive` is appended to that history, and
the closed record reads as revived by it, naming that event's revision, stamp and author. **The
closed index's rows are append-only, one per transition, and that is what makes a past answer stay
past.** Each `:settle` and each `:revive` writes **its own immutable row**, keyed
`<event-rev>:<id>` and written into the day partition of that event's stamp; a row is never
rewritten and never replaced. An id's rows are its **branch history** in revision order, reached
through the id index in one bounded lookup, and the row a reader wants for a given moment is **the
newest row at or before that moment** — so a settle, a revive and a second settle reconstruct as
the three things they were, and an ask whose window ends between the first two answers the first.
The **newest** row of an id carries `revived=<rev|->` and `settles=<n>`, so every field draft 24
printed still prints; what has changed is that the rows behind it are still there. **Two bounds
make this affordable, and they are why this is the shape chosen over refusing every historical
ask**: a row is written per *transition*, and a transition is a real settle or a real reopen, so
the index grows with the work that happened and never with the questions asked; and an as-of
lookup for one id reads that id's chain in pages bounded by `--page-records`, the chain itself
bounded by its own `settles=`, and never scans a day. **The refusal is the floor under it and not
the design**: where a partition such a lookup needs is missing or corrupt, the ask refuses by the
retention section's one line naming that partition, and never answers an earlier moment from a
later row. **The cursor is `<event-rev>:<id>`**, stable for the same reason the rows are — a
cursor keyed on an item's *latest* settle moves between pages the moment that item settles again
(Stella at e79847fb, W2) (replays `as-of-reconstructs-settle-revive-settle`,
`cursor-pinned-across-a-new-settle`).
**An id is counted and printed by its latest state at the query's window end**: an id whose
newest branch event by then is a `:settle` is in `closed=` and prints the disposition row of C,
one whose newest is a `:revive` is in `open=` and prints O's, so an item that settled and was
revived inside one window is counted once, as open, and `closed-in=` counts only the ids whose
latest state at the window's end is settled.
**Closure activity and item state are two questions, and this document answers both rather than
letting one stand for the other**: `closed=` and `closed-in=` are item state at the window's end,
each id once; `settles-in=<n>` and `revives-in=<n>` are the *events* inside the window, one per
transition; and `items-in=<n>` is the distinct ids those events touched. So an item that settled
and was reopened inside one window still shows the settle that happened — `settles-in=` counts it
— while `closed=` counts that id once and as open, which is the reading rule 18 rests on (Stella
at e79847fb, W2: a reopen must not erase work that actually happened during an interval) (replay
`activity-and-state-are-two-counts`). Rule 18 below reads the same way: the finding is an
id whose *latest* state puts it in both branches, never the history of an id that has honestly
moved and kept its record (replay `revive-appends-and-counts-latest`). `:cancelled`,
`:superseded` and `:removed` are terminal and no `:reopen` reaches them, as the transition table
already says. **Settling changes no `:id`, no `:children`, no `:acceptance`, no evidence event
and no count**: the containment forest spans both branches, a settled item keeps its place under
its parent, and `node remove` is still the only thing that detaches one. **Settling moves no
required set**: `:settle` and `:revive` are scope events whose delta is none, in the table above,
so a member that is done is still a member and `rows=` does not move when it finishes. What moves
a set is the delta table's own kinds, and a `:remove`, a `:cancel` and a `:supersede` move theirs
by that table exactly as before — in the same envelope as the settle they carry (replays
`settle-keeps-id-and-evidence`, `settle-moves-no-required-set`, `reopen-revives`).

**A container settles with its members, in the same envelope, and revives with them.** A
`:work-set`, a `:feature` and a `:roadmap` have no work of their own: their completion is their
required members' completion, by the kinds above. So the settle that closes the last open member
of a container carries the container's own `:settle` too — `:by` the author of that settle,
`:reason` naming the member that finished it — and so on up the containment path while each
parent's required set is wholly settled, all in the one envelope, all-or-none, one `OK` line.
The cascade is bounded by the path from the node to the root and never by the size of the set,
and it stops at the first container that still holds an open member. **An empty required set is
never done** by the kinds above, so an empty container never settles by cascade and `<repo>/shared`
does not close a repository behind the team's back. A `:revive` cascades the same way in
reverse: reopening one item returns its settled ancestors to O in the same envelope, because a
container over open work is open (replay `containers-settle-with-their-members`).

**A roadmap outlives the work that built it, and settling one closes no view.** A `:roadmap`
settles with its members like any other container — the cascade above is not amended — but **a
roadmap is a durable named view and a capability inventory, never a queue item that vanishes when
its last task closes** (Stella, `docs/SPEC-WORK-PILOT.md` at `81c2885`). So **a roadmap's view
record stays in the live snapshot's structure and never passes into the retention archive**: its
stable id, its ordered axes and their members, its `:row-kind` and `:aggregation`, its cell
mapping, its baseline and scope history, its projection targets and its publication receipts are
retained whatever branch the roadmap is in and however old its work is **(Rowan's decision, for
review** — the alternative was exempting `:roadmap` from the settle cascade, which would leave a
view standing in O for ever and make `remaining` answer with work nobody has**)**. Opening a named
roadmap is therefore an **explicit scoped query**, answered from that retained record plus bounded
indexed reads of its members' closed rows — **never a load of C, and never narrowed by the default
`[now - 24h, now)` window**, which bounds what a *closed-activity listing* opens and not what a
named view may reach. **Completed rows stay in the table**: *remaining only* is an explicit filter
and never a pruning, and retiring a row is a recorded scope decision that leaves every older
revision's view reproducible (replays `roadmap-outlives-its-work`,
`roadmap-opened-after-the-window`).

**Settling is the record that the work ended and never a claim that it was verified.** A `:to
:done` standing on unverified or stale evidence settles its item and still counts `unknown` with
`done-unverified=` beside it, exactly as it does today: C holds the disposition, the transition
and the evidence events, and every verdict stays derived at read time from the criterion, the
generation and the verification cache, so a `correct`, an `accept` or a source bump changes what
a settled item counts as with no fetch and no second record — which is what *not a duplicate
ledger* has to mean in the one place it is tempting to forget.

**Settling ends the claim, because the work has ended.** The settle envelope carries a
`:release` for a live lease on the node, written by the settling author and naming the holder it
ended — the session's own, outside the payload digest with the `:settle` beside it, by the event
kinds above; it is not refused by the holder rule of `release`, which guards a third name reaching in
and not the end of the work itself. So **no item of C holds a live lease**, W and C are
disjoint by construction, and a settled item reads `holder=unowned` in every answer while its
lease log stays whole for `handoffs` (replay `settle-releases-the-lease`).

**W is a view, and no verb writes it.** `(working O)` is the items of O that hold a live lease —
a lease whose deadline is ahead of the read's clock with no release and no handoff — which is
the lease section's own fact and not a new one. `take` and `release` are the only things that
change W, exactly as they do today; nothing is settled by being in W and nothing is in W by
being `:doing`, because *responsible*, *doing* and *working* are three facts and this document
has kept them apart since its first draft. `--window` splits W the way `who` and `stale` already
print it: worked-now (a heartbeat inside the window) and `held-not-worked=`. `|W| ≤ |O|` is
rule 18's business below (replay `working-is-a-view`).

**C is append-only, and it is read through its index, in pages, and never by loading it.** Every
clip writes one **closed-index** row per `:settle` and per `:revive` into the day partition of
that event and into the closed index root the clip's commit names — **beside the snapshot and
never inside it**, in pages bounded by `--page-bytes` and `--page-records`, as the retention
section above sets out on Stella's W1. **A session start loads no row of it**: it opens the root,
reads the manifests and the pages the default window needs, and holds at most `--index-cache`
pages resident, so **startup cost is flat in how much work the team has ever finished** and an ask
that reaches further back pays for exactly what it reaches. **On recovery the same replay that
rebuilds O overlays the index.** A session opens the closed index root as the last clip published
it and replays its own journal from that journal's newest boundary record, which is the execution
model's one replay above; **that replay applies every `:settle` and every `:revive` it passes as
an overlay on that index exactly as a clip would write them** — each appending its own row, the
overlay read together with the pages until the next clip writes it down — and it does so before
the whole validation runs and before any ask is answered. So a crash between a settle and the next clip leaves no id in both branches and no id
in neither: one pass over one journal recovers the tree and the index together (replay
`index-replayed-after-crash`). Each row carries what the normal path reads about a settled item —
its `:id`, its `:under`, its repository, its kind and category, its disposition, the state and
generation it settled at, its scope and source revisions, its required-leaf count at settle, the
settle event's revision, stamp, author and request id, and, **for every event of its evidence
set, that event's five fields** (`:pointer`, `:criterion`, `:against`, `:stamp`, `:generation`) —
so **evidence survives the move** and no ask and no rule loads a body to answer about a settled
item. **C is not a second ledger**: a settled item's node and its events live exactly where every
item's do, in the snapshot's retained part while they are newer than the retention boundary and
in the retention archive after it, written by the same clip; what C adds is rows *about* them.
**Three things are bounded by three different axes and this document names them together once, so
no reader takes one for another**: **C** is a branch by *disposition*; the **day partitions** of
the retention section above hold C's rows by the *event day* of the settle or the revive that
wrote each, and are what the default window opens two of; and the **retention archive** is a file
bounded by *age* that holds bodies. An item may be in C, its rows in two day partitions and its
oldest events in the archive, and no one of the three implies another. **So a session whose archive file is absent still answers every ask over
C and runs every rule over it**, which is the same test the retention paragraph already stands
on; an ask that would need a body the archive holds — a `--at` replay or a `handoffs --since`
before the boundary — is refused by the rules that already refuse it, and a listing whose rows
the index answers but whose bodies are gone prints **`gap=<n>`** and one `QUERY NOTE
coverage-gap file=<name> range=<rev>-<rev>` and never an empty closed set: a missing archive is
an honest coverage gap, not a completed set of nothing (Glenn, 23:34Z) (replays
`closed-row-with-archive-absent`, `closed-paged-without-full-load`).

**Root-wide queries name their branch and their window, and count each id once.** `query --ask`
takes **`--branch <open|closed|root>`, required**, refused at exit 2 naming the flag when absent,
because a count whose branch a reader has to infer is the silent denominator of 5654160320 in
another coat; the flag takes the words and the data carries the letters. `--branch closed` and
`--branch root` **require `--from <stamp>` and `--to <stamp>`**, the time window over settle
stamps, and `--branch open` refuses them at exit 2 — O is read in the present and `--at
<revision>` is how it is read in the past. `who`, `stale` and `handoffs` refuse `--branch closed`
and `--branch root` at exit 2 naming the flag, because a live lease is a fact of O alone
(replay `branch-and-window-required`). **`done --branch open` is empty by construction and is
not refused**: every `:to :done` settles in the same envelope, so no done item is left in O, and
the ask prints its `QUERY OK` line with `shown=0` and no rows — an honest answer about a set
with no members, never an error.
**`--branch` selects the rows a listing prints and never what the counts fold**: the counts on a
`QUERY OK` line are the scope's and have always folded both branches — a done leaf is counted by
its container today and is counted the same way from C — so no percentage, no `rows=`, no
`required=` and no rollup changes its value because an item settled. **Each id is counted once
and printed once**: `open=<n>` and `closed=<n>` partition the scope's counted ids, their sum is
the scope's counted total, `closed-in=<n>` is the part of `closed=` whose settle stamp falls
inside the window, and an id in both branches is a finding by rule 18 and not an arithmetic to
be tidied at read time (replay `cow-root-partition`). **A closed listing never loads all of
history**: its rows come from the closed index in event-revision order, the window bounds the
range and selects the day partitions through the date index — **at most two of them where the
window is the default `[now - 24h, now)`** — `--max` caps the page under the cap-and-count law,
and the `MORE` line's remedy names **`--after <cursor>`**, the cursor being the last printed row's
`<event-rev>:<id>` (an open listing's is its `<id>`), so the next page is a read of the next rows
and never of the whole. **A page is pinned to the revision, the filter and the ordering its first
page captured**, which the cursor carries: `now` and the query revision are read once at the first
page, every later page answers as of them, and **a continuation whose pinned revision the session
can no longer serve is refused rather than drifted** — `QUERY FAIL ask=<kind> after=<cursor>
pinned=<rev> current=<rev>: page expired`, exit 1 — because a continuation that silently skipped
or repeated a row because another item settled between two pages would be a report nobody could
check (Stella at e79847fb, W2) (replay `cursor-pinned-across-a-new-settle`).

**The three questions Glenn asked are three asks over the same identities** (23:34Z: *answer
"what remains?", "what is in flight?", and "what did we complete during this interval?" from the
same identities and evidence*): `remaining --branch open` is what remains, `who --branch open --window <dur>` over
`(working O)` is what is in flight — its answer prints `membership=working` — and `done --branch
closed --from <stamp> --to <stamp>` is what was completed in the interval. None of them reads a
message bus and none of them reconstructs a closed pull request by hand: the identities, the
evidence and the settle events are already in the root.

**Which branch a sentence of this document is about, said once.** Every sentence about loading,
validating, journaling, clipping, ownership, fencing, the one coordinator and the one live
reader/writer is about **O** and the closed index the same session opens: the session loads O
whole and reads C's index in bounded pages, never C's history. Every sentence about membership, required sets, counting,
states, transitions and leases is about **O** unless it names C. Stella's sections below keep her
spelling, and where they say S they mean O.

**Scope, baseline, focus** (5654160320). A roadmap or work set carries a scope revision,
derived: the count of its scope events. The first `:baseline` event records the initial
membership before execution; later discoveries **append at the bottom of every rendered
listing in discovery order**, reported as *added since baseline*, and reprioritising execution
never reorders the baseline rows; a removal carries a reason and never lowers a count
silently; a split that changes the leaf count prints both units (Stella, point 5). **Focus**
is a query (a node id, a repo, a category, an owner, a revision), not a copy: it selects a
subtree or a membership view over the same nodes, so identity, dependencies, responsibility
and evidence are the same in every view; a dependency outside the focus that blocks it is
reported on the row (`blocked-by=`), and **`--at <revision>` answers as of a past scope revision by
replaying the retained log in memory to it** — no parse and no file read, but it is a replay
and it counts in the answer's `replays=` — and a revision before the loaded snapshot's
retention boundary is refused at exit 1, `QUERY FAIL … : at=<rev> boundary=<rev> before the
retained history`, naming the archive that holds it (5653982211: *views filter by … scope
revision*).

**Clocks.** Every event carries `:stamp` and `:clock`. By default the tool reads its own clock
and records `:clock :tool`; `--now <stamp>` is optional and records `:clock :given`, for
replay and tests (Stella, point 3). Reads take the same `--now` for the same reason.

## Counting *(shared; Glenn's rules verbatim where they are his)*

- **Language completion** on a roadmap = `100 * green feature cells / applicable feature
  rows` for that axis member, where applicable rows are the live rows less those the axis
  member has a recorded out-of-scope event for. *Partial cells do not contribute fractions of
  a completed feature to this number* (5653970526). **The divisor of that division prints under
  its own name**: `percent` prints `green=<k> applicable=<n> rows=<n> baseline-rows=<n0>`, where
  `applicable=` is the divisor above, `rows=` is the roadmap's required-set cardinality and
  `baseline-rows=` is the membership its last `:baseline` recorded, so a new denominator is
  visible beside the old (5649089106) and the divisor beside both. **`applicable=` is per axis
  member; `rows=` and `baseline-rows=` are the roadmap's and are the same under every `--axis`**,
  so nine beside ten reads as one row out of scope for one member and never as a row removed.
  **`percent` over zero applicable rows prints `green=0 applicable=0` and no percentage**,
  because a percentage of nothing is not zero, and exits 0. **`--axis <member>` is required by
  `percent`**, and a `percent` without it is refused at exit 2 naming the flag: `applicable=`,
  `green=` and the percentage are each defined for one axis member and for nothing wider, and a
  roadmap-wide percentage over the cells of every member would be the average of fractions
  5653970526 forbids (Opus at efc26a2e, 2026-09-13: the flag was optional in the grammar while
  every field of the answer was defined per member).
- **Cell progress** = completed required leaves / required leaves, printed as `k/n`, never as
  a lone percentage. A parent is green only when every required child and every dependency
  gate is satisfied. *Do not average nested percentages, round 99.9 to green, treat an empty
  checklist as done, or count one shared leaf repeatedly within a cell* (5653970526).
- **Completion of a focus** = completed required work / current required work, with the
  baseline denominator kept beside it for expansion and contraction (5654160320). **Current
  required work keeps `:deferred` leaves (deferred is not done) and excludes only
  `:cancelled` and `:superseded` leaves**, each removed by a scope event with an author and a
  reason; `remaining` prints `deferred=<n>`, `cancelled=<n>` and `superseded=<n>` kept apart,
  so what is in and what left the denominator is visible; the baseline denominator still
  counts all three (Johnny Grok, johnny-afcd1cdafa74: a deferral must not raise
  completed / current required, at leaf grain as at row grain). **Live rows** of a roadmap are its baseline rows plus discovered rows,
  less rows removed, cancelled or superseded by a scope event — *live* is the word the
  required-set definition above already uses of a row's feature, and the two name one set under
  one name; **a deferred row stays live and stays
  in the denominator, because it is not done** (Johnny Grok's HOLD on draft 9, bus note
  johnny-e51960925453: deferring an applicable row must not raise green/rows); **applicable
  rows** for an axis member are the live rows less those with an out-of-scope cell for that
  member, and every change to `rows=` is a scope event with an author and a reason.
- **`since-baseline=` has one definition and it is gross**: the count of members a `:discovery`
  added to the node's required set since that node's last `:baseline`, counted whether or not a
  later `:cancel`, `:supersede` or `:remove` took one of them out again — because Glenn's word is
  *added since baseline* (5654160320), and what a baseline failed to foresee is not unlearned by
  a cancel. **The net movement is already readable** as `rows=` against `baseline-rows=` at row
  grain, as `rows=` against `baseline-rows=`. **At leaf grain no baseline denominator is on any
  line**, so the net movement there is not readable and this choice does lose that; the honest
  statement is that the gross number is Glenn's word and the net is readable at row grain, not
  that nothing is lost (Opus at efc26a2e, 2026-09-13: the compensation named `required=` against
  a baseline denominator that appears in no line of the grammar). **It is counted in members —
  the direct required members a `:discovery` added — and never in leaves and never in the line's
  `unit=`**, by the units bullet below. It is one node's count where the scope is one node, and
  **over a scope that covers many containers it is the sum of their counts**, because such a
  scope has a required set in each container and no one of them stands for the rest (Fable at
  efc26a2e, 2026-09-13: `size`, `stream --repo` and `under --category` are scopes of many nodes,
  and `source=` has a rule for that case while this field had none). It is never an axis
  member's (Opus at 7472e545, 2026-09-13: every mention of it was a use and none a definition,
  and its two defensible readings printed two numbers for one set).
- **Units are labelled** on every line: `unit=features` or `unit=leaves`; a comparison never
  changes unit silently. **`unit=` names the grain of the line's state counts, and the fields
  whose grain is fixed are named here so no reader infers them from it**: `done=`,
  `done-unverified=`, `unknown=`, `deferred=`, `cancelled=`, `superseded=`, `stale=`, `open=`,
  `closed=` and `closed-in=` are in
  the line's `unit=`; `since-baseline=` is **always members**, the direct required members a
  `:discovery` added, because the delta table moves a set member by member and a discovery of
  one feature holding twelve leaves is one discovery under either unit; `required=` is **always
  leaves**, the total required leaves under the scope, because it is the denominator of cell
  progress and cell progress has one grain; `green=`, `applicable=`, `rows=` and `baseline-rows=`
  are **always the roadmap's rows**, a row being a node of that roadmap's declared `:row-kind` by
  the kinds above; **the line prints `row-kind=<kind>` beside them** so a reader never has to infer
  what a row was, and `unit=features` is the label where that kind is `:feature`, which is the
  default and was the only kind before draft 26. So one line carries
  four named grains and guesses at none (Opus at 7472e545, 2026-09-13: one `unit=` labelled a
  `percent` line that printed feature counts and a leaf count together; Fable and Opus at
  efc26a2e, 2026-09-13: `since-baseline=` was promised in `unit=` and defined in members, so a
  `unit=leaves` line printed one number and meant the other).
- **The branch counts are the root's own and partition the scope**: `open=<n>` is the scope's
  counted items still in O, `closed=<n>` those in C, and **their sum is the scope's counted
  total with no id in both**, which rule 18 makes a finding rather than an arithmetic tidied at
  read time. `closed-in=<n>`, printed only where a window is given, is the part of `closed=`
  whose settle stamp falls inside it — *what did we complete during this interval*. **Three more
  are printed with it wherever a window is given, and they count events rather than items**:
  `settles-in=<n>` and `revives-in=<n>`, one per `:settle` and one per `:revive` recorded inside
  the window, and `items-in=<n>`, the distinct ids those events touched. **The two grains are
  never added and never substituted**: `closed-in=` answers *which items are finished as of the
  window's end* and `settles-in=` answers *how much finishing happened inside it*, so an item
  settled and reopened in one window is `settles-in=1 revives-in=1 items-in=1 closed-in=0`, and
  the interval's real work is on the line rather than erased by the reopen (Stella at e79847fb,
  W2). `gap=<n>` is
  the rows whose body the retention archive holds, or whose day partition the committed root names
  and the read could not open, printed so a missing file reads as a coverage gap and never as a
  completed set of nothing; **a day with no manifest inside a complete manifested range is no
  events and not a gap**, by the retention section above. **A settled
  item is counted exactly where it was counted before it settled**: `done=`, `required=`,
  `rows=`, `applicable=` and every percentage fold both branches, because a member that has
  finished is still a member and a denominator that dropped it would be the silent denominator
  of 5654160320 arriving by a new road.
- **`|O|` is a counter carried by every accepted mutation envelope and is read, never computed**
  (Stella, `docs/SPEC-WORK-PILOT.md`). The root's open-item count, and the per-repository and
  per-container counts beneath it, are updated by the same envelope that moves an item, including
  the whole of a settle or revive cascade, before its `OK` line is printed; **a resident
  current-revision `|O|` query reads the counter and triggers no rollup, no scan, no parse and no
  replay**, and a test that mutates and then asks repeatedly asserts zero visits, zero parses and
  zero replays and compares against an independent full count after a close, a reopen and an
  import replay (replay `open-count-is-read-not-computed`). **The counters count canonical item ids
  once** and exclude references, attempts and history records; **the count's unit and revision are
  printed with it**; and **an open linked issue count and an open leaf-task count are separate
  counters, neither of which is silently labelled `|O|`**. Startup and recovery may reconstruct the
  counters from the canonical state — an ordinary query may not — and **no promise of constant time
  is made for an arbitrary new filter**, only for the counters this bullet names.
- **Future cannot lower the active percentage; a deferred item cannot raise the completed
  count** (5653970526). Both are tests.
- **Unknown is a count of its own**, printed on every answer; `done-unverified=<n>` is the
  part of `unknown=` that is a recorded done standing on unverified or stale evidence, so the
  two are never added.

## Queries — the contract *(Rowan; Glenn's list from 5654012267)*

Every answer is computed whole before anything is printed, then printed as one `QUERY OK`
scope line and one `QUERY ROW` line per fact, capped and counted. The scope line is the
`QUERY OK` line of the grammar below, and the grammar is the one enumeration, in two parts.
**On every `--ask`**: scope revision, membership rule, **branch**, unit, source sha, freshest evidence
stamp (`-` where no evidence event falls under the scope, as `source=` prints `-`), `done=`, `done-unverified=`, `unknown=`, `deferred=`, `cancelled=`, `superseded=`,
`stale=`, `required=` (the total required leaves under the scope), `since-baseline=`,
`private=` (nodes a view reached through a parent and printed nothing of, 5653982211),
`rows=`, `open=`, `closed=`, `gap=`, `shown=`, `parses=`, `replays=` and `emitted=`; and
`from=`, `to=`, `closed-in=`, `settles-in=`, `revives-in=` and `items-in=` wherever a window was
given, which is every `--branch closed` and every `--branch root`. **The window a closed ask reads
by default is the rolling `[now - 24h, now)` of the retention section above**, and an `--ask`
whose `--from` reaches before it is an **explicit historical query**: it is answered the same way
from the same rows, it opens the day partitions its range intersects and no others, and it prints
`pages=<n>` — the index and segment pages this answer read — so the cost of reaching back is on
the line the answer is printed on and never hidden in it. **Per ask, named in brackets in the
grammar and by the table here, so the table can never promise a field the grammar lacks**:
`percent` adds `green=`, `applicable=`, `baseline-rows=` and `row-kind=`; `who` and `stale` add `held-not-worked=`,
`unowned=` and `responsible=`; `done`, `remaining`, `size`, `stream` and `under` add
`responsible=`; `stream` adds `leases=`, the live lease count its row in the table promises.
**A listing that may print a settled item prints the disposition row**, which is `done`,
`remaining` and `under` under `--branch closed` or `--branch root`: it carries `branch=`,
`disposition=` (`pending`, `working` or `deferred` in O; `done`, `cancelled`, `superseded` or
`removed` in C), the repository, `landed=<sha|->` — the `:against` sha of the qualifying
evidence event whose criterion is `:merged`, which is the revision the fix landed at, and `-`
where none — `released=<version|->` — the `:version` of the **settled** release task that names
this item in its `:deps`, found through the reverse-dependency index, and `-` while that task is
still in O, because **a merged fix is not a distributed one** (Glenn, 23:36Z: *a merged fix can
be C while the separately tracked release task remains O; do not call distributed just because
merged*); **where two settled release tasks name one item, the field prints the version of the
one with the earlier settle stamp**, because the release that first carried the fix is the one
that distributed it, and a later release carrying it again changes nothing about that
(replay `merged-is-not-distributed`) — `holder=<name|unowned>`, the live lease's holder while
the item is in O and `unowned` for every item of C, since a settle releases the lease, **so
*every finding, its disposition and any in-flight fix* is one ask and never a second ask for who
holds it** (Glenn, 5657069874) — `settled=<stamp|->`, and the evidence counts. **Two asks have their own row shape, and it is in the grammar with the others**: `who` and
`stale` print the lease row, which carries the `default=` the table promises beside the
holder, the heartbeat age and the deadline; `handoffs` prints the transition row, one line per
`:lease`, `:heartbeat`, `:release` or `:handoff` event since the named revision, because a
transition log is not a counting row. Every other ask prints the counting row. A field an ask does not carry is absent, never printed empty.

| ask | answers |
|---|---|
| `done --node X` / `remaining --node X` | completed and outstanding required work under X, by kind, capped and counted; `remaining --branch open` is *what remains*, and `done --branch closed --from <stamp> --to <stamp>` is *what did we complete during this interval*, its rows the disposition row, paged by `--after` |
| `who --node X --branch open --window <dur>` | live leases on X and beneath it: holder, heartbeat age, deadline, default; then `held-not-worked` and `unowned` counts; and `responsible=` for X. This is *what is in flight*: its membership rule is `(working O)` and it prints `membership=working` |
| `percent --node R --axis <member>` | the roadmap rollup for one axis member, with `green=<k> applicable=<n> rows=<n> baseline-rows=<n0> done-unverified=<n>` — the percentage is `green` over `applicable` — and every partial cell's `k/n` and `unknown=<u>` |
| `size` / `size --node X` | total required leaves, done, unknown, unverified, deferred, cancelled, superseded, since-baseline |
| `stream --repo <owner/name>` / `--owner <name>` | the same, for one repository or one friend's own selected work, plus `responsible=` and live lease count (5654012267: *ownership for a named stream*) |
| `under --repo <owner/name> --category <label>` | compact listing of nodes by category with state (5654164074; taxonomy TBD); under `--branch root` it spans C and O in one answer, one row per id, which is the worked acceptance below |
| `stale --window <dur>` | leases past deadline or past the heartbeat window, grouped by holder |
| `handoffs --since <revision>` | the lease transition log |
| `roadmap --node R` | one named roadmap opened whole: its axes and ordered members, its `:row-kind` and `:aggregation`, its cell mapping, its baseline and scope history, its projection targets and publication receipts, and one row per member with its current verification state — from the retained view record plus bounded indexed reads, **never a load of C and never narrowed by the default window** |
| `friends` / `friends --owner <name>` | every known friend, idle, resting and unavailable ones included, with status, observation age, working count and the pending and acknowledged assignments; expanding one friend costs `O(k)` in its k tasks and answers *friend, task, executing model* directly |
| `models` / `models --category <task-class>` | the shared model catalog by stable model and version identity, with declared capability, friend assessment and measured result kept apart, each with its observation age, sample count and uncertainty, and bounded drill-down to the receipts |
| `ready --node X` | the work that can actually be started under X, derived from dependencies, agreed scope, acceptance readiness, ownership, availability and resource limits; **every row that cannot proceed prints its exact reason and who can resolve it**, because waiting is not execution (replay `ready-names-the-blocker-and-the-resolver`) |

### The worked acceptance: findings per repository across C and O *(Glenn's own query, 23:36Z)*

This is the query that produced the root refinement, written out whole, and it is the replay
**`findings-across-c-and-o`**: *list all Alex findings per repo, pending/working/done
disposition, landed revisions and the release versions containing the fixes*. Nothing in it is
security-specific — a finding is a `:task` under its repository work set with a category, its fix
lands as evidence, and its release is another task — which is why it is here and not in a section
of its own (Glenn, 23:34Z: *this is a general work-set/history requirement, not a
security-specific data model*).

Four findings of one audit under `mas-bandwidth/nova-tools`, category `security-finding` — **an
example, its ids and its names invented for this document**, as the examples above it are:
`alex-1`, fixed, merged and shipped in `v0.4.2`; `alex-2`, fixed and merged, whose release task
`nova-tools/release/v0.4.3` is still open; `alex-3`, held under a live lease by Freddy; `alex-4`,
read and judged not a defect, closed by a `cancel` carrying the evidence of that reading, which
is a disposition of its own and never a completion.

**Every number on the scope line below follows from those four leaves and from nothing else.**
`unit=leaves`, because the items this scope counts are four leaf `:task`s and `unit=` names the
grain of the state counts; `required=3`, because `required=` is always leaves and `alex-4`'s
`:cancel` took it out of its parent's required set by the delta table above, current required
work excluding exactly the `:cancelled` and `:superseded` leaves by *Counting* below;
`done=2 cancelled=1 unknown=0`, because `alex-1` and `alex-2` are done, `alex-4` is
cancelled, and `alex-3` is `:doing` — a state of its own, where `:unknown` is the state of a node
with no transition at all; `since-baseline=0`, because no node in the fixture carries a
`:baseline` and so no `:discovery` has added a member since one; `rows=0`, because `rows=` is a
roadmap's required-set cardinality and this category scope covers no roadmap; `open=1
closed=3`, which sum to the four ids the scope counts; `settles-in=3 revives-in=0 items-in=3`,
because three `:settle` events fall inside the window, no `:revive` does, and they touched three
distinct ids, which is the activity beside the state; and `pages=4`, the three day partitions the
window intersects that hold closure records — `2026/09/11`, `2026/09/12` and `2026/09/13` — plus
the one date-index page they were reached through, the other ten days of the window holding no
manifest and so being opened not at all. **This `--from` reaches before the default `[now - 24h,
now)`, so it is an explicit historical query**, which is exactly the way a report of a month's
findings is meant to be asked for; what the bound promises is that `pages=` is unchanged when a
year of older history stands behind this window, and that is the assertion the replay makes.

```
nova-work query --session /run/work.sock --ask under --repo mas-bandwidth/nova-tools \
                --category security-finding --branch root \
                --from 2026-09-01T00:00:00Z --to 2026-09-14T00:00:00Z --max 20

QUERY OK ask=under scope=412 membership=category branch=root unit=leaves source=9f2c1a7e freshest=2026-09-13T18:22:41Z done=2 done-unverified=0 unknown=0 deferred=0 cancelled=1 superseded=0 stale=0 required=3 since-baseline=0 private=0 open=1 closed=3 gap=0 from=2026-09-01T00:00:00Z to=2026-09-14T00:00:00Z closed-in=3 settles-in=3 revives-in=0 items-in=3 responsible=rowan pushed=410 rows=0 shown=4 pages=4 parses=0 replays=0 emitted=612
QUERY ROW nova-tools/sec/alex-1 branch=closed disposition=done repo=mas-bandwidth/nova-tools kind=task state=done landed=9f2c1a7e released=v0.4.2 holder=unowned settled=2026-09-12T18:22:41Z evidence=2 verified=2 responsible=emma
QUERY ROW nova-tools/sec/alex-2 branch=closed disposition=done repo=mas-bandwidth/nova-tools kind=task state=done landed=4b81c0d5 released=- holder=unowned settled=2026-09-13T09:14:02Z evidence=2 verified=2 responsible=emma
QUERY ROW nova-tools/sec/alex-3 branch=open disposition=working repo=mas-bandwidth/nova-tools kind=task state=doing landed=- released=- holder=freddy settled=- evidence=0 verified=0 responsible=freddy
QUERY ROW nova-tools/sec/alex-4 branch=closed disposition=cancelled repo=mas-bandwidth/nova-tools kind=task state=cancelled landed=- released=- holder=unowned settled=2026-09-11T22:03:55Z evidence=1 verified=0 responsible=rowan
```

**What the shape is asserted to say**, each of these a line of the replay: four ids, four rows,
`open=1` and `closed=3` summing to the four this scope counts, and no id on two rows;
`unit=leaves` with `required=3` over four leaves one of which was cancelled out of its set,
`since-baseline=0` over a fixture with no
baseline and `rows=0` over a scope with no roadmap, each derived above rather than asserted; `alex-2`
in C with `landed=4b81c0d5` and `released=-`, because **merged is not distributed** and its
release task is open — the same node answering `nova-work query --session /run/work.sock --ask
remaining --branch open --repo mas-bandwidth/nova-tools --category release`, which lists
`nova-tools/release/v0.4.3` as `disposition=pending`, so the two facts are one set of
identities read twice and never two ledgers; `alex-3` in O, `disposition=working` because it
holds a live lease, and W is exactly that view; `alex-4` closed as `cancelled`, distinct from
`done` and counted in neither `done=` nor a percentage; `parses=0 replays=0`, because the
closed rows came from the closed index and the open one from the resident tree, and `pages=4`
because the index was read in pages and never loaded — **the same ask run against a set carrying a
year of older closed history prints the same four rows, the same `pages=4` and the same startup
resident bytes**, which is Stella's W1 written as an assertion (replay
`history-grows-startup-does-not`); and **the same
ask with the retention archive file absent printing the same four rows** — the bodies are the
archive's and the rows are the index's — while an ask that reaches for a body prints `gap=<n>`
and its `QUERY NOTE coverage-gap` and never a shorter list. Running it again with `--max 2`
prints two rows and a `MORE` naming `--after`, and the `--after` page prints the other two, with
no read of anything outside the window (replay `closed-paged-without-full-load`).

## The verbs *(Rowan; a draft shape, to be cut by the pilot)*

Session verbs run the process; every other verb is a client verb addressed to a session by
`--session <path>` (required; no default), or, for `check` and `query` only, to a published
snapshot by `--snapshot <path>` with the three bounds, read-only; under `--snapshot` the
verification cache is named by `--cache <path>`, required there and refused under `--session`
(the session's own, from its start, is the one path), so a snapshot reader sees the same
verdicts the coordinator last fetched. **Every mutation verb takes `<write flags>` = `--as
<name> [--request <id>] [--expect <rev>] [--now <stamp>]`**: the request id is drawn by
the tool and printed when absent. **`--expect` names a revision the requester can read, and
there are exactly two, in one number space.** The session's **local revision** is the count of
accepted events, the snapshot's plus its journal's, printed `rev=<n>` on every mutation's `OK`
line; the **clipped revision** is the local revision at the last clip's boundary, carried
inside the snapshot the clip wrote and printed `pushed=<rev|->` on every answer, so a friend
reading a published snapshot, a fenced session reading its own base and the coordinator all
read the same number for the same clip. Which one `--expect` names is decided by the path,
never by the value: on the coordinator's own verbs (the CLI, `--session`) it is the local
revision, optional; on every request in a `session replay` bundle it is the clipped revision,
**required**, and `session export` writes it from the fenced session's base. A request whose
expectation is not the current value of its kind is refused at exit 1, `<MUTATION> FAIL
node=<id> expect=<rev> current=<rev>: stale`, printing the current value so the requester can
re-read and resubmit (5653982211: *apply rejects stale preconditions*). **On the replay path
the expectation is checked per node as well as per set**: a replayed request is refused the
same way, `<MUTATION> FAIL node=<id> expect=<rev> current=<rev>: stale`, when the node it
addresses has accepted **an event of its own that is not an earlier request of this same bundle
applied by this replay** after the revision its `--expect` names — its own accepted events,
never the sets a `decompose` or an `event --kind cancel` elsewhere moved for it as a parent or
as a roadmap, so a disjoint change does not refuse an unrelated replay (Fable at 7472e545,
2026-09-13), and never the bundle's own earlier requests, because **a bundle is one ordered
sequence from one session and its later requests are exactly what its earlier ones saw**: every
request in a bundle carries the same clipped revision, so without this clause the second request
a fenced session made against one node would be refused `stale` by the first one's replay, and a
session that set a task `:doing` and then wrote its evidence would lose the evidence on every
replay — no bundle touching one node twice could ever replay whole, which is what the replays
below demand (Fable at efc26a2e, 2026-09-13) — even where the
clipped revision has not moved, because the clipped revision moves only at a clip and the
coordinator's own accepted events between two clips would otherwise pass an expectation that
never saw them (Stella 5655371246 item 3, Fable and Opus at d1b20f42, 2026-09-13: a `state --to
doing` replayed over a node the coordinator had since set `:blocked` is a legal edge and a lost
intent, and structural validation cannot see the difference). **A replayed request whose node
has accepted nothing since that revision but this bundle's own earlier requests is admitted**, so
disjoint work still replays, a bundle replays whole, and the refusal is the overwritten intent
alone. How a friend on another bench submits a
request is the bus: a request bundle is a file a note carries, and `session replay --from` is
its intake, so no second transport is invented here. Every client verb that lists takes `--max
<n>`, default 20, `0` means all, negative refused (SPEC.md, the cap-and-count law). Every
duration comes from a flag: `--window` is required by `who` and `stale`, `--by` by `take` and
by `release --handed` — **where it names the lease's `:deadline` and never the event's `:by`**,
the author of every event being `--as` and the stored `:by` being what `--as` wrote —
`--every` and `--clip-every` by `session start`. **`--as <name>` is caller text on every verb**:
the tool authenticates nobody, the name is what the record will say, and the one place it is
checked against anything is `release`, below. On `session replay` it names the coordinator
applying the bundle and is recorded in `:generation-owner`; **the event's `:by` stays the
request's own author, as the bundle carries it**, because a replay moves a request and never
re-authors it. `--now <stamp>` is optional on every verb and records `:clock
:given`; absent, the session's clock is used and recorded as `:clock :tool`.

```
nova-work session start  --session <path> --as <name> --file <path-in-repo> --journal <path> --cache <path> --repo <path> --remote <name> --branch <name>
                         --max-bytes <n> --max-depth <n> --max-nodes <n> --every <duration> --skew <duration> --clip-every <duration> --clip-after <n> --retain <duration>
                         --index-cache <n> --page-bytes <n> --page-records <n> [--closed-window <duration>]
                         [--resolver <scheme>=<command> ...] --git-timeout <seconds> [--attempts <n>] [--repair] [--foreground] [--max <n>] [--now <stamp>]
nova-work session export (--session <path> | --journal <path> --max-bytes <n> --max-depth <n> --max-nodes <n>) --into <path>
nova-work session replay --session <path> --from <path> --as <name> [--max <n>]
nova-work session status --session <path>
nova-work session stop   --session <path> --git-timeout <seconds> [--attempts <n>] [--no-clip]
nova-work session handoff --session <path> --to <name> --git-timeout <seconds> [--attempts <n>]
nova-work operation status  --session <path> --id <id>
nova-work operation list    --session <path> [--max <n>]
nova-work operation wait    --session <path> --id <id> --timeout <duration> [--after <cursor>]
nova-work operation cancel  --session <path> --id <id> --reason <text>
nova-work checkpoint list    --session <path> [--max <n>]
nova-work checkpoint create  --session <path> --as <name> --reason <text>
nova-work checkpoint verify  --session <path> --id <id>
nova-work checkpoint restore --checkpoint <path> --into <path> --max-bytes <n> --max-depth <n> --max-nodes <n>   (isolated and read-only: takes no ownership, dispatches nothing, replays no message)
nova-work checkpoint compare --checkpoint <path> --against (--session <path> | --snapshot <path> --max-bytes <n> --max-depth <n> --max-nodes <n>) [--max <n>]
nova-work undo-plan      --session <path> --request <id> [--max <n>]
nova-work undo           --session <path> <write flags> --request-of <id> --reason <text>
nova-work redo-plan      --session <path> --request <id> [--max <n>]
nova-work redo           --session <path> <write flags> --request-of <id> --reason <text>
nova-work friend         --session <path> <write flags> (--register <name> | --retire <name> | --role <name>=<role>[:<scope>] | --participation <name>=<yes|no|withdrawn> | --capability <name>=<capability-id> --group <child|swarm|local|one-shot> --limit <n> | --limit <name>=<n>) --reason <text>
nova-work config         --session <path> (--request <name> --base <hash|-> | --export <name> --into <path> | --intake --from <path> <write flags>) [--max <n>]
nova-work model          --session <path> <write flags> (--register <id> --provider <name> --route <text> --billing <metered|subscription|local|unknown> | --rate <id>=<pricing-id> --effective <stamp> --source <pointer> | --evidence <id> --task-class <label> --result <pointer> --samples <n>) --reason <text>
nova-work observe        --session <path> <write flags> --friend <name> (--state <awake|resting|unavailable|unconfirmed> --source <pointer> | --attempt <id> --observed-model <id> --bench <name> --usage <pointer>) --reason <text>
nova-work clip           --session <path> --as <name> --git-timeout <seconds> [--attempts <n>] [--max <n>] [--now <stamp>]
nova-work check          (--session <path> | --snapshot <path> --max-bytes <n> --max-depth <n> --max-nodes <n> --cache <path>) [--max <n>]
nova-work verify         --session <path> (--offline | --max-fetch <n> --fetch-timeout <seconds>) [--node <id>] [--max <n>]
nova-work query          (--session <path> | --snapshot <path> --max-bytes <n> --max-depth <n> --max-nodes <n> --cache <path>) --ask <kind> --branch <open|closed|root>
                         (--ask is one of: done, remaining, who, percent, size, stream, under, stale, handoffs, roadmap, friends, models, ready)
                         [--node <id>] [--repo <o/n>] [--owner <name>] [--category <label>] [--axis <member>]
                         [--since <revision>] [--at <revision>] [--from <stamp>] [--to <stamp>] [--after <cursor>] [--max <n>]
                         (who and stale: --window <duration>, required; percent: --axis <member>, required;
                          --branch closed and --branch root: --from and --to, required, and refused under --branch open;
                          who, stale and handoffs: --branch open only, the other two exit 2)
nova-work render         --session <path> --view <roadmap-id> --into <path> --start <marker> --end <marker> [--at <revision>] [--check]
nova-work node add       --session <path> <write flags> --id <id> --type <kind> --under <parent-id> [--title <text>] [--category <label>] [--required <true|false>] [--acceptance <id:kind:subject:predicate> ...] --reason <text>
nova-work node remove    --session <path> <write flags> --node <id> --reason <text>
nova-work node require   --session <path> <write flags> --node <id> --to <true|false> --reason <text>
nova-work decompose      --session <path> <write flags> --node <id> --into <id,...> --acceptance <child-id:id:kind:subject:predicate> ... --reason <text>
nova-work accept         --session <path> <write flags> --node <id> (--add <id:kind:subject:predicate> | --remove <id>) --reason <text>
nova-work source         --session <path> <write flags> --node <id> --to <sha> --reason <text>
nova-work dep            --session <path> <write flags> --node <id> (--add <id> | --remove <id>) --reason <text>
nova-work axis           --session <path> <write flags> --roadmap <id> --axis <id> --add <member> --reason <text>
nova-work cell           --session <path> <write flags> --roadmap <id> --coord <member,member> (--ref <id|-> | --out-of-scope | --in-scope) --reason <text>   (--ref - clears the mapping)
nova-work responsible    --session <path> <write flags> --node <id> --to <name> --reason <text>
nova-work take           --session <path> <write flags> --node <id> --by <duration|stamp> --default <release|extend-once|escalate:<name>>
nova-work heartbeat      --session <path> <write flags> --node <id> --evidence <pointer>
nova-work release        --session <path> <write flags> --node <id> [--handed <name> --by <duration|stamp> --default <release|extend-once|escalate:<name>>]
nova-work attest         --session <path> <write flags> --node <id> --criterion <id> --result <pointer> --against <sha>
nova-work attempt        --session <path> <write flags> --node <id> --model <name> --bench <name> --result <pointer> [--usage <pointer>]
nova-work evidence       --session <path> <write flags> --node <id> --pointer <pointer> --criterion <id> --against <sha> [--attempt <id>]
nova-work state          --session <path> <write flags> --node <id> --to <state> (--evidence <event-id> ... | --reason <text>) [--blocked-by <id>]
nova-work correct        --session <path> <write flags> --node <id> --reason <text>
nova-work event          --session <path> <write flags> --kind <baseline|discovery|defer|cancel|reopen|supersede> --node <id> --reason <text> [--member <id,...>] [--superseded-by <id>] [--evidence <pointer>] (baseline and discovery: --member, required, and --kind discovery on a :roadmap is exit 2 naming `axis --add`; supersede: --superseded-by, required; cancel: --evidence <pointer>, required, and a note: pointer IS admitted here, because it evidences a stopped worker and never a done; --member on any other kind is exit 2)
nova-work version
nova-work help
```

**What a mutation does, stated exactly.** `node add`, `node remove`, `node require`,
`decompose`, `accept`, `dep`, `axis`, `cell`, `responsible` and `source` are structure verbs: each appends one structure event and, where it changes a
required set, one scope event, **as one request envelope**: one journal record holding both,
written all-or-none, replayed all-or-none, answered with one `OK` line, and answered again
with the same line on a retry of the same request id, so a crash between the two can never
leave the tree changed with the denominator and revision unchanged (Stella, 16:00Z, finding
2). `decompose` is a `split` (decomposition, never discovery or completion), and **it carries its
children's acceptance, because rule 15 is not a surprise sprung at the end**: `--into` names
the children it creates under `--node`, `--acceptance` is repeatable and must name at least
one criterion for every child it creates that is `:required`, and a decomposition that would
leave a required leaf unclosable is refused whole at the candidate gate before anything is
written. **`accept` is the verb that adds or removes a criterion after `node add`**, so
acceptance is editable by a recorded act instead of frozen at creation; removing the last
criterion of a required leaf is refused by rule 15 the same way. **An `accept --add` on a node
derived `:done` is refused by rule 5 at the candidate gate**, because its standing `:to :done`
would no longer cover the node's `:acceptance`; the way through is `correct` — which bumps the
generation and takes the node off its done claim — then `accept --add`, then fresh evidence,
so a criterion is never slipped under a green cell without a record. **`node remove` detaches a
node from its parent's `:children` in one envelope with its `:remove` scope event**: the node
and the **open** items of its subtree settle into C with disposition `removed` — **one `:settle`
for the node and one for each item beneath it that is still in O**, each carrying `:reason
removed`, in that same envelope — and every event of theirs is kept as
provenance and counted nowhere —
a removal is never a delete, and by the retention paragraph above they pass into the clip's
archive with the boundary, so provenance never grows the live snapshot — and a node holding a
live lease, or referenced by a cell's `:ref` or another node's `:deps`, is refused, naming the
holder or the referrer. **An item beneath it that is already in C is untouched by the
removal**: no second `:settle` is written for it, because rule 18 below refuses a `:settle` for
an item already in C and because a finished item's own disposition is not overwritten by the
removal of something above it — a leaf that was `done` before the removal reached it stays
`done`, with its evidence and its settle stamp as they were. **The node's own `:settle` names
them instead, in its `:already-closed` field**, the ids beneath it that were already closed, so
the removal's one record accounts for every item of the subtree exactly once and a reader of C
can tell a leaf that was removed from a leaf that had already finished. **A `node remove` whose
own node is already in C writes nothing at all**: it is a no-op at exit 0 with one `NODE NOTE
already-closed node=<id> disposition=<d> settled=<stamp>` line before its `NODE OK`, because
what the caller asked for — that this item is not open work — already holds, because a second
`:settle` for it is rule 18's finding rather than a second history, and because a retry after a
crash lands here and must read the same. Nothing is detached and no scope event is written by
that no-op, so the item stays where the settle that closed it left it: in its parent's
`:children`, and in its parent's required set exactly where its disposition already had it.
**Why a live lease refuses a removal while a `state --to done` ends one**: a settle ends a claim
on work that has ended, written by the author who is ending it, and a removal ends work that
somebody else is still holding — so the first releases and the second refuses and names the
holder, who releases or hands off first. Two settle paths, two lease rules, and the difference
is whose work is ending (replay `remove-settles-only-open-items`). **A reference to any node of its subtree refuses it the same way, naming
that node**, because a cell whose `:ref` named a removed node's child would go into the archive
with the rest of the subtree and leave the cell pointing at a node the live set does not hold
(Fable at efc26a2e, 2026-09-13). A cell-referenced node is removed by re-pointing or clearing
that cell first (`cell --ref -`), which is a structure change and moves no required set, a cell
being a reference. **A roadmap row is not in that refusal list and is not to be added to it**: a
row's removal is a legal move of the roadmap's required set by the delta table above, and the
roadmap keeps the member on its first axis while the member leaves the set — **and because the
member has left the set, rule 2 does not check it**, which is how the clip that later carries
the node into the archive stays green (Opus at 7472e545 and at efc26a2e, 2026-09-13). **An
`<id:kind:subject:predicate>` argument is split by position, not by a quoting rule**: before
the first `:` is the id, between the first and the second is the kind, after the last `:` is
the predicate, and everything between is the subject, so a subject may hold `:` and `@`
(`test:internal/lockfile/TestLockRule1@<rev>`) and needs no escaping;
`decompose --acceptance` takes the same form with the child's id before it and one further
`:`. `take`,
`heartbeat`, `release`, `attempt`, `evidence`, `state`, `correct` and `event` append one event
each. **The gate is one validation, of the resident O as it would be with the event applied**:
the session evaluates the structural rules over the affected nodes and either journals and
applies the event unchanged or refuses at exit 1 with the finding's line and changes nothing.
A red O elsewhere still refuses, because a red set is stopped, not written around; `check`
says where. **A set that is red at load has one defined way out, and it is a verb rather than
a hand edit**: `session start --repair` loads and validates the same way, prints `SESSION OK
… findings=<n>`, and admits mutations under a different gate — the whole validation, O(V+E)
and stated as that cost, run over the candidate, the event accepted only when its finding
count is strictly below the current one and refused otherwise at exit 1, `<MUTATION> FAIL
node=<id> findings=<n> was=<n>: no repair`. It refuses every clip while findings stand, and at
zero it prints `SESSION NOTE repaired findings=0` and admits the ordinary candidate gate
thereafter. Without `--repair`, a red load answers reads and refuses every mutation, so a red
set is never repaired by accident and never left where no verb can reach it. **A red load's
own exit is 1, with or without `--repair`** — the validation ran and said no, which is the
exit table's 1 — while the session stays up and serves reads; the line is the same
`SESSION OK … findings=<n>`, and only `findings=0` exits 0. **The findings themselves have no
second grammar**: they are the whole validation's own `WORK FAIL <id>: rule <n>: <reason>`
lines, printed above that `SESSION OK`, capped per rule by the `--max` `session start` was
given, with a `WORK MORE` line naming the remedy. `plan`, `apply` and `reconcile` (5653982211, the Terraform half) are **not in
this draft**: named here so a reader knows they are deferred, with their own section once the
pilot has shown what a plan must name.

**A lease is authoritative the moment the one coordinator accepts it**, because the ownership
record above admits one live O; `pushed=<rev|->` on its answer says only which clip carried it to the branch, which is
durability, not exclusivity. A request to take a node
arrives at the coordinator from a friend as a mutation request with a stable request id and
the friend's expected revision; the coordinator serializes it like any other (Stella, *One
coordinator, one live reader/writer*).

## The resident session *(Stella, from her amendment at 60b9027; governs the execution model where it says more than the section above)*

### Keep the work set alive

A supervised, long-lived Lisp session owns the parsed S, its stable-ID indexes,
current event position, derived state and cached projections. Load and validate
once at session start; subsequent verbs operate on those resident objects.
A CLI or connector can be a thin client of this session. A fresh CLI process
must not imply a fresh Lisp process or a complete reload. An always-on system
daemon is not required. Session startup, identity, bounds and shutdown must be
explicit and inspectable.

Both structure and state are manipulated through typed verbs. Creating a node,
decomposing a feature, changing a dependency or assigning responsibility cannot
require the coordinator to rewrite Lisp text by hand. The precise public verb
names remain a pilot decision. Trusted implementation code may be Lisp; imported
S and issue text remain bounded data, never executable forms.

After a mutation, update affected indexes and invalidate affected derived values.
An indexed lookup does not traverse S; a changed leaf does not unconditionally
reparse the file or replay the whole event history. Full validation and broad
queries may still require O(V+E) work. No constant-time promise applies to an
arbitrary structural edit or a change affecting most of the graph. Store derived
state in memory; it does not become a second authoritative work set.

### Accept locally, then clip into Git

Proposed durability mechanism: validate each mutation against the current local
revision, append its stable event ID and payload to a local recovery journal,
and acknowledge acceptance only after that journal is durable. Failed validation
changes neither accepted S nor its journal. The same event cannot apply twice.
This is a crash-recovery proposal, distinct from Glenn's required periodic clips.

A clip captures a named local event boundary, fetches the upstream revision,
checks it against the coordinator's expected shared base, validates the candidate,
and writes a deterministic snapshot with the retained event history. An unexpected
upstream work-state edit is an ownership/protocol conflict, not an invitation to
merge concurrent coordinators. Intake requests are applied by the sole owner. Commit and
push the resulting checkpoint. On success, record the new shared revision and
which local events it contains. Later accepted events remain pending for the
next clip. Do not discard recovery records before successful checkpointing.

A failed network request leaves local accepted work and the pending clip intact;
report it as locally durable but not shared. Retry with bounded exponential
backoff. A rejected push preserves the local work and names the upstream and base shas; never
force-push and never merge (the sentence "apply nonconflicting incoming events
incrementally" stood here in 9757de0 and is struck by stella-0ace603bdc22). An explicit
reload/rebuild is a recovery or maintenance operation, not the normal verb path.
Clip cadence is configurable; shutdown and coordinator handoff request a clip.
A Git checkpoint is not required for every small mutation.

Only one coordinator may access the live work set, as specified below. A lease
file plus periodic Git pushes cannot establish that exclusivity across benches.
Local recovery also does not guarantee another bench can recover unclipped events
after total loss of the original bench. The ownership and recovery mechanism must
be decided and tested before automatic takeover is enabled.

### Required measurements and replays

- Start once, run many queries/mutations: instrument actual parse count, full
  replay count, graph visits, journal writes and emitted bytes. Unchanged indexed
  queries cause zero additional parses or full replays.
- Incremental results equal a clean reconstruction of the same accepted revision;
  unrelated cached projections remain reusable after a local leaf mutation.
- Crash after journal durability but before acknowledgement, then retry the same
  request: one accepted event, no lost or duplicated mutation.
- Crash or disconnect during clip: recovery retains all accepted events and
  reports the last confirmed shared checkpoint honestly.
- A second coordinator attempts to open the live S: refuse access. Transfer
  ownership, then resume the former coordinator: reject its reads/writes and
  side-effect requests under the old ownership generation.
- Structural verbs update the tree and its evidence/scope history without manual
  source editing; the resulting ROADMAP remains reproducible.

These amend draft 3's file-per-verb interface, hand-written-only structure, full
checks around every append, and build-indexes-on-every-read wording. Integrate
those contracts together before approval; adding a resident wrapper around an
unchanged reparse-per-command engine does not satisfy the requirement. Dogfood
the workflow on Fixed Tables, record total builder plus review/repair cost, then
refine the production spec. This amendment does not authorize a production build.

### Reusable recursion, not a mandatory combinator

Glenn asks whether Y-combinator ideas can make the Lisp tooling more general.
The proposal is reusable recursive operators over typed nodes: a fold for
summaries, a bounded unfold for decomposition, and incremental propagation for
derived values. A recursive data structure is not itself the Y combinator.
Ordinary named recursion in Lisp suffices; a literal textbook Y combinator does
not terminate under eager evaluation without adaptation.

Separate traversal from per-kind policy. A completion fold and a cost fold share
traversal machinery but not counting rules: attempts contribute incurred cost,
while only acceptance-qualified work contributes completion. References do not
become extra owned children. Cache with revision and policy identity; invalidate
through the retained indexes. A generic operator must preserve these semantics.

For an acyclic dependency graph, use an affected topological pass instead of
repeatedly rescanning all of S until nothing changes. Any future fixed-point
solver over cyclic analysis facts requires a defined domain, convergence argument
or explicit iteration bound, and an honest non-convergence result. It does not
permit cycles in counted containment or declare tasks complete by convergence.
Decomposition is proposed work, with depth/work/cost bounds and stopping criteria;
it never grants itself permission to execute an expanding tree of tasks.

The bounded pilot should demonstrate two different summary policies over the
same tree, shared-reference counting, and equivalent full versus incremental
results after a change. Adopt the abstraction only if it reduces implementation
or coordination cost without obscuring acceptance evidence.


## The engine and its client *(shared; Stella's `docs/SPEC-WORK-PILOT.md` at `81c2885`, integrated; the wire schema is Rowan's)*

**The engine is the resident Common Lisp session of the execution model above; the Go CLI is a
thin client of it over one explicitly named local Unix-domain socket.** The engine owns the
canonical state, the journal, the indexes and the mutation ordering; **starting a CLI process
reloads nothing**; the session and journal locks and the coordinator fencing of the execution
model are unchanged. **The socket and the directory that holds it belong to the account that runs
the session** — the directory created `0700` and the socket `0600`, both owned by that account —
**there is no network listener and no remote evaluation protocol anywhere in this scope**, and
**reaching the socket is not a grant of coordinator authority**: every request still carries its
author, its request id and its expectation, and the fencing rules still decide.

**The wire is a versioned, bounded, length-prefixed UTF-8 JSON protocol, and this paragraph pins
it** (Stella's requirement; **the exact schema below is Rowan's decision, for review**, since her
text names the properties and leaves the spelling open). One message is a **4-byte big-endian
unsigned length** followed by that many bytes of one UTF-8 JSON object; a frame past
`--max-frame-bytes` (defaulting to the session's `--max-bytes`) is refused with one framed error
and the connection is then closed, never truncated and never partially applied. **Every integer
the protocol carries is a JSON string of decimal digits — ids, revisions, counters, byte counts
and token totals alike — and the protocol carries no JSON numbers at all**, because an IEEE-754
double rounds silently above 2^53 and a usage total is exactly where that bites; a reader that
meets a JSON number refuses the frame. **Every timestamp is RFC 3339 in UTC with a trailing `Z`**,
the same spelling `:stamp` uses, never an offset, never a local zone and never an epoch count.
**An absent key and a JSON `null` both mean *not given*, while an empty string and an empty array
are values** — the same distinction the payload digest's `()` makes, so the two serializations
agree. A request is `{"op": "<verb>", "request": "<id>", "as": "<name>", "expect": "<rev>", "now":
"<stamp>", "max": "<n>", "deadline": "<stamp>", "args": {…}}` with `op` a **typed operation name
and never an executable form**, no Lisp, no shell, no path the engine did not resolve itself. A
response is `{"ok": true|false, "exit": "<0|1|2>", "lines": [ … ], "rev": "<n>", "pushed":
"<rev>|-"}`, where **`lines` are exactly the one-line answers of *Output grammar* below,
verbatim** — so the grammar has one definition and the CLI prints what it was handed rather than
formatting a second time. **Versions are negotiated before any request**: the client's first frame
is `{"op": "hello", "protocol": ["1"], "client": "<build identity>"}` and the session answers with
the one version it will speak or refuses, naming what it supports, and closes; an unsupported
version fails clearly and never degrades into a guess. **Restricted s-expressions remain the
durable work-data format** — JSON is the wire and never the store (replays
`wire-integers-are-strings`, `protocol-version-negotiated-or-refused`).

**Durability across the wire is the journal's, not the connection's.** Mutations enter the single
writer's queue; the accepted envelope is journaled and applied before its success response.
**A socket disconnect is never a rollback and never a cancellation**: the work either was accepted
or was not, and the caller finds out by asking. **A retry with the same request id and an identical
body is answered with the recorded disposition; the same id with different arguments is refused**,
by the dedup predicate of *Retention* above and its bounded indexed pages — **never by an unbounded
resident map of request ids**. **The local durable revision and the last shared Git revision stay
two fields in every response**, `rev=` and `pushed=`, because *accepted here* and *shared there*
are two facts and one number for both is how a coordinator loses work (replay
`disconnect-is-not-a-rollback`).

**Slow work returns a durable operation id, and the control plane never waits behind it.** A quick
query or mutation returns its bounded result. A long operation — a source capture, an import
staging, an export, a clip's transport — returns **an operation id and a state at once**, and the
CLI may exit while the work continues: `nova-work operation status --id <id>`, `operation list`,
`operation wait --id <id> --timeout <duration> [--after <cursor>]` and `operation cancel --id
<id>`, each bounded and capped like every other listing. **Waiting is an event cursor and a bounded
block, never a model asking again in a loop** (a poll loop is a whole agent per tick, which the
record already paid for). **A wait that times out leaves the operation running**; a completed
result is retrievable by its id afterwards; **a cancellation is a request with its own
acknowledgement and its own final disposition**, and it can neither erase an accepted mutation nor
undo an external effect that may already have happened. **Slow I/O stages its inputs and results
outside the mutation loop** and only the owning engine admits a validated result at an expected
revision, so a concurrent capture never becomes a second writer; queues, jobs, staged bytes and
retained results are bounded by explicit limits, accepted work stays recoverable, and **recovery
reconciles interrupted operation ids and their external outcomes before anything is retried**.
**No unbounded scan and no network wait may hold the mutation loop**: it is paginated or staged,
and status and cancellation responsiveness are measured under load (replays
`operation-survives-the-client`, `cancel-is-a-request-not-an-erasure`,
`status-answers-while-io-runs`).

**Mistakes are reversible by appending, never by erasing.** `undo-plan` and `undo`, `redo-plan` and
`redo` name **accepted request ids**, and each reversible verb records enough preimage and
provenance for the engine to build a **typed compensating envelope**: the original event stays
exactly where it is, the reversal is appended with its lineage, and **redo reapplies the intent
against current preconditions rather than deleting the undo**. A plan is revision-bound and shows
the nodes, dependencies, counters, verification and assignment effects it would move; **a stale or
conflicting plan refuses atomically**, naming what changed, and is never half-applied. **Accepted
evidence and source and accounting receipts are historical facts**: an undo may supersede what they
currently support and can never erase that they happened. **A sent message, a paid execution, a
publication and a source deletion are not undone by rewinding local state** — they are reported as
external effects with their own compensating workflow, a cancellation stays a request until its
outcome is known, and **a generic undo of an irreversible or uncertain operation is refused**.
**Resetting shared Git history is never the undo mechanism** (replays `undo-appends-and-preserves`,
`redo-refuses-a-stale-plan`, `undo-refuses-an-external-effect`).

**The verb families below are a coverage requirement, and the grammar above is the one spelling.**
Every canonical field maps to an owning typed mutation or is explicitly marked derived or
immutable; **no generic set-field escape hatch may bypass an invariant**; each verb states its
argument types, prerequisites, read and write set, invalidation and counter effects, authority and
fencing checks, all-or-none boundary, idempotency, its success, refusal and unknown outcomes, and
its evidence receipt; a multi-node change is one atomic validated envelope; and **a dry run
produces a revision-bound plan and accepts no mutation**, an applied stale plan revalidating and
refusing its conflicting assumptions. **No parallel aliases**: one name per operation, and nothing
that acquires a different meaning under a second spelling. **Import is not overloaded with
deletion, completion is not overloaded with retirement, and a correction is not proof that a worker
received it.**

| data or action | required coordinator operations |
| --- | --- |
| session and durability | start, status, clip, checkpoint list/create/verify, isolated restore and compare, export/replay, stop, fenced handoff |
| reversible mistakes | undo-plan/undo and redo-plan/redo of named requests; appending compensating history and refusing conflicting or irreversible effects |
| work structure | add, edit permitted metadata, move/reparent, decompose, link/unlink, require, retire; stable ids and historical scope preserved |
| scope and dependencies | baseline, discovery, dependency add/remove, prioritise, defer, cancel, reopen, supersede |
| assignments and execution | offer, acknowledge/decline, assign/responsible, take/heartbeat/release, attempt/result/usage intake, correction, pause/stop/reconcile |
| evidence and completion | criteria add/change/retire, source revision, attest/evidence, review/finding/disposition, verify, settle/revive under their explicit guards |
| roadmaps | create/edit view, axes and members, cells and references, projection targets and policy, query/render/check; no duplicated task state |
| friends and CONFIG | register/retire, role and participation changes, capability and limit changes, config request/export/validated intake |
| ACTIVE | observation and availability intake, current attempts and pending offers, return reconciliation; occupancy derived from execution records |
| models and prices | register/version, evidence and suitability updates, route and rate revisions with provenance; historical receipts immutable |
| issue correspondence | inventory, read-only capture and plan, non-destructive import, reconcile, archive/export/restore, a separately selected absorb or external update |
| queries and operations | indexed counts, focus and subtree, ready and blockers, friend/model/cost/history views, bounded operation status/wait/cancel |

**Every normal coordinator workflow runs through these public verbs** — no hand-edited Lisp, no
hand-edited JSON, no hand-edited journal record and no hand-edited derived cache — and the
implementation plan names the missing verbs and the conflicting existing semantics **before the
lock gate below** (replay `every-field-has-an-owning-verb`).

## Friends, CONFIG and ACTIVE *(Stella, `docs/SPEC-WORK-PILOT.md` at `81c2885`)*

**Nothing in this section names a friend, a bench, a repository or a house.** Every identity below
is configuration a team supplies; **no friend name, no model name and no role is hardcoded into
nova-work**, and a tool that shipped one would be specific to whoever wrote it (Glenn, and Stella's
own rule: *team-specific role records belong in team configuration, never hardcoded into the
generic tool*). The examples in this document are marked as invented where they appear.

**The coordinator's fast in-memory tracker of who has what is nova-work's, and it is indexes, not
a second store.** A stable friend identity index and a reverse assignment index from an identity
to canonical task ids live in the same resident model, under the one writer and the one journal;
**rereading the bus is not the query path**. Configuration, assignments and observation receipts
persist in the checkpoint; time-sensitive availability is revalidated on recovery. A `friends`
section names **every known friend, idle, resting and unavailable ones included**, with the
`working` task references and the pending and acknowledged assignments under each. **Those
references are references**: they never become second containment parents, never duplicate a
completion or a cost record, and never inflate `|O|` or a roadmap's completion. A direct lookup
returns a friend's status and working count in constant time; expanding one friend's list costs
`O(k)` in its k tasks and is capped and counted like every listing.

**CONFIG and ACTIVE are two sections and the difference is how often they change** (Glenn's own
separation). **CONFIG** is each friend's mostly constant capability and configuration: selectable
models, the maximum number of child agents, each swarm with its models and slot limits, local-model
routes, one-shot launchers, agreed roles and budget policies, each with provenance and scope. It
is updated **only on a meaningful configuration change** and is never rewritten by a heartbeat or a
token sample. **ACTIVE = active data per friend** — Glenn's words, and the one thing this document
spells with that word: what each friend is doing now, the tasks and the children, swarms, local
runs and one-shots executing them, the chosen and the observed model, the bench, the handle, the
status, the timestamps, the tokens and the costs. **ACTIVE is variable operational data and is
never called configuration**; it references CONFIG's stable identity and revision rather than
copying a definition into every activity record; and its updates and observations are journaled and
retained under the event and accounting contracts this document already has.

**The coordinator is a friend in `friends` and is tracked by the same indexes as everyone else** —
her or his own tasks, executions, model, bench, usage and availability, through the same queries.
**Coordinator ownership is a role with the single-writer and fencing rules of the execution model,
and never an exemption from attribution or accounting.** A role transfer moves that authority; it
**merges no two friend identities and erases neither friend's historical work**.

**A role is configured, never inferred, and a model's capability never cancels a friend's agreed
limits.** CONFIG's role records carry agreed roles, preferred participation and reserved duties
with their provenance and scope, and the configuration is **expressive enough for the three shapes
the record asks for**: a role reserved for **essential security work only** on a paid plan, so
routine work never spends it; a **specialised different-perspective role on a reserved plan**,
asked at decision points and not for receipts; and **agreed participation**, which is a recorded
consent with a scope and a revision rather than a capacity reading. **Strengths and weaknesses
belong to the model catalog below and never to a second per-friend rating system**; a friend's
agreed limits are never raised silently at any level (replays `roles-are-configured-not-inferred`,
`reserved-role-is-not-spent-on-routine-work`, `no-friend-name-in-the-tool`).

**Configured capability, observed fact and current free capacity are three fields and never one.**
CONFIG holds what a friend *may* do; **observations** hold last contact and its source, explicit
rest or return, rate-limit or credit unavailability, and pending offers, acknowledged assignments
and live leases. **Missing contact is unknown and stale capacity — never proof of failure and never
proof of consent** — no secret is stored, and **willingness is never inferred from configured
capacity**. Each friend may expose four execution capability groups — **child agents, swarms, local
models and one-shots** — each entry with a stable capability id, its source, its last-verified
stamp, its availability and its budget or permission constraints, and with **declared support,
successful runtime verification and current free capacity as three separate fields**: a catalog
entry is not evidence of a live child or of free credits. **Actual children, swarm jobs, local runs
and one-shots are execution instances** linked to their friend, capability, canonical task and
attempt, retaining requested and observed model, bench, status, deadline, provider handle and usage
receipts; **nested delegated executions keep their parent lineage without counting one attempt
twice** in a friend's, a pool's or a task's totals; and **one-shot names a launch shape, not one
model call**.

**Dispatch, delivery, acknowledgement and accepted ownership are four facts.** A dispatch through
the bus or a worker launcher records intent and a stable request identity; **a pending offer
reserves only explicitly declared capacity until it is reconciled**, and **a timeout alone never
blindly launches a duplicate while the old worker may still be running**. A correction or a
reassignment retains lineage and reconciles the cancellation, the lease and the side-effect
authority under the fencing rules above. **An imported message is data**; what changes the set is a
validated coordinator verb applied to it. **The requested model and the observed model are two
fields and unknown stays unknown**: a friend's usual model is not proof of the model that executed
a delegated task, concurrent attempts keep separate model and usage attribution, and **a retry never
overwrites the attempt before it**. The compact view answers *friend, task, executing model*
directly from these records and shows delegated execution as delegated (replays
`dispatch-ack-and-ownership-are-three`, `requested-model-is-not-observed-model`,
`a-retry-does-not-overwrite-its-attempt`).

**Availability is observed, and silence is a question rather than an answer.** ACTIVE records
whether a friend is confirmed awake, explicitly resting, unavailable on a confirmed plan, credit or
provider limit, or unconfirmed and unreachable, each with its source and last-contact stamp;
remaining quota and expected return are kept **only where they were observed**, and neither an old
heartbeat nor an elapsed estimate proves a current one. **Explicit rest is respected.** A
configured silence threshold — `--silence-ping <duration>`, **a team's configuration and not a
number this tool believes in** — triggers **one bounded availability ping** through the existing
wake protocol unless the friend is resting or is reserved from routine wakeups; a configured answer
window then marks that capacity **unavailable for scheduling with reason `unconfirmed`**, which
**asserts neither sleep nor exhausted credit without evidence**. **A failed probe is unresolved
delivery and not a failed friend.** A return reconciles outstanding assignments and observed
capacity before any new dispatch. **The same rules apply to the coordinator**, and an automatic
role transfer still needs fencing and recovery and never a stale-contact test alone (replays
`silence-is-a-ping-not-a-verdict`, `explicit-rest-is-not-pinged`, `return-reconciles-before-dispatch`).

**Configuration is exchanged in bounded pieces and never as prose every poll.** A request names a
friend and the last-known config hash and revision; the answer is **`UNCHANGED` with that
identity** where they are equal, and otherwise a validated manifest or a **bounded delta against
the exact named base**; an unknown base asks for a bounded full manifest. **A changed fragment is
applied atomically after schema, identity and hash validation, by the coordinator, and a message
never executes imported code.** A large manifest uses explicit bounded parts with a completeness
hash, and **a partial config is never admitted as a complete replacement**. Each manifest records
its schema version, its stable friend, capability and route ids, its revision, its content hash and
its observation provenance; each executable model route gives its provider, its exact model or
alias with the resolved identity, its harness or endpoint class, its billing mode (metered,
subscription, local or unknown), its pricing reference and its effective stamps. **No API key, no
credential value and no secret-store content is in a manifest**, and sharing respects the audience
the configuration names (replays `unchanged-config-is-one-bounded-answer`,
`an-invalid-delta-leaves-the-old-config`, `a-partial-manifest-is-refused`).

**What this section does not decide, and must not.** Whether friends participate in swarms at all,
or whether pools hold model-only workers, is **an open design decision to be asked and recorded,
not settled here**: a swarm is an execution capability holding jobs and **is neither a friend nor a
model**; a swarm labelled with a friend's name does not make its workers that friend, carry that
friend's continuity or speak for them; a model-only worker needs no invented friend identity; and
the friend responsible for a pool and the actor executing a job are two references that are never
double-counted. **A friend's own disposition is the only record of that friend's consent** — a
worker's reply and a silence are not it — and the friend whose participation Stella's companion at
`81c2885` names must be asked directly. **Until it is resolved, identity provenance stands as it is
and no historical actor is renamed**; if model-only pools are chosen, the capability carries a
model or route label and every past source label and attribution is preserved; if participation is
supported, it needs an explicit participation record with its scope and revision and a withdrawal
behaviour that reconciles running jobs. This is in *What this draft does not do* as well, so it is
not read as settled.

## Models, prices and what they are evidence of *(Stella, `docs/SPEC-WORK-PILOT.md` at `81c2885`)*

**A shared `models` section is keyed by stable model and version identity, with provider route and
alias mappings where they differ**, and friend CONFIG **references** those records rather than
repeating a description in every pool. Each carries concise strengths, limitations and
task-suitability notes, supported modalities, tool and harness needs, context and output
constraints, and a pricing reference. **Declared vendor capability, a friend's assessment and a
measured result are three separate fields**, and **unknown or outdated evidence never hardens into
an established strength or weakness**.

**An observation carries its workload and revision**, the model, provider and harness
configuration, the accepted quality and verification outcome, the operational tokens, the review,
retry and correction overhead, the cost and the wall time, with its date, source, sample count and
uncertainty. **A failed attempt is evidence and is not a verdict on a model's or a friend's
worth**; a change of version, route or configuration **does not inherit the old conclusions**; and
**no numerical ranking is published from a handful of unmatched tasks**. Compact suitability
summaries are indexed by task class and capability with bounded drill-down to the original
receipts.

**A pricing record is immutable by content identity** and states its currency, its unit scale (a
price per million tokens, say), its separate input, output, cache-write and cache-read rates, and
its treatment of reasoning tokens, saying whether cache and reasoning counters are inside the
parent totals; tiers, long-context thresholds and batch or discount conditions are explicit where
they apply. **A missing or unsupported dimension is unknown and never zero.** **Measured provider
cash charges, estimated marginal cash and virtual reference token cost are three separately
labelled values**: a subscription does not make reference cost zero, exhausting an allowance or
paying overflow changes whether a route is usable under its policy, and local inference counts its
tokens with a **declared API charge of zero** while any hardware or energy cost is a separate model.
**Live remaining quota is an observation and not a CONFIG edit.** The coordinator resolves a rate
reference once, caches it by immutable identity, and computes comparable costs mechanically from
retained source-normalised usage; **every execution and every estimate pins its configuration and
pricing revision**, so an old estimate stays reproducible when a rate changes, and **a price
refresh is a meaningful config change with its source and effective time and never a silent edit to
a historical receipt** (replays `pricing-is-pinned-by-revision`, `unknown-price-is-not-zero`,
`subscription-is-not-free-reference-cost`, `local-tokens-cost-zero-api`).

**These facts inform a coordinator and grant nothing.** They combine with availability, actual free
capacity, prerequisites, agreed limits and cost policy to prefer a capable economical route by
**total operational cost for accepted work, reviews and rework included, never token price alone**,
while keeping specialised perspectives and security roles available where they are needed.
**Routine scheduling is automated only under an explicit policy and measured real-work results**,
and **no model profile grants access, execution authority or capacity by itself.**

## The hierarchy, the table and the roadmap's own record *(Stella, `docs/SPEC-WORK-PILOT.md` at `81c2885`)*

**Glenn's hierarchy is repository → roadmap → epic → feature → subtasks**, with sub-features and
subtasks recursive beneath, and it is **stored and queried rather than inferred by a renderer from
a name**. Optionally a feature splits on another named dimension — a language, a platform, a
backend — and **that optional dimension is what introduces cells**; without the split there is no
mandatory synthetic cell between a feature and its subtasks. A table projection **selects** rows and
an axis from this durable hierarchy and **copies no work**; a cell references its canonical target;
and **W selects the leased open execution items beneath the selected feature or cell and is not
another level of ownership**. Epics, features, sub-features and tasks are typed canonical work
nodes by *The data* above; an implementation may realise epic and sub-feature as named container
policies, but **their meaning and their counting unit stay explicit and queryable**.

**A feature's implementation record and its acceptance record are two records and both are joined
by stable id.** The first links the source repository, the implementation commit or pull request
and the relevant paths or symbols; the second names the criteria, the tests or assertions, the
reproducible invocation and configuration, and the retained result receipts **at exact source
revisions**. **A file name and a green aggregate badge prove nothing**, and no renderer owns a
second verdict.

**Historical delivery and current verification are two questions and this document keeps both.**
When code, criteria or dependencies change, the past completion and its receipts are retained, the
affected current-verification summaries are invalidated, and the answer **says a recheck is
needed** — it does not erase history, claim an old test ran on new code, or silently reopen a
closed task because evidence went stale. **A confirmed regression creates linked open repair work**
under the ordinary policy. **A change outside a feature's declared proof scope invalidates no
unrelated receipt without a dependency reason**: provenance and scope decide what is still reusable
(replays `historic-tick-survives-a-source-change`, `regression-opens-repair-work`,
`unrelated-receipts-stay-reusable`).

**The table's display is Glenn's and is locked.** Rows are the roadmap's rows and columns are the
selected axis members; **the status cells are centred**; **a tick only for fully verified and an X
for every other state**; a percentage summary shows `z%` only. **Partial and unknown work is
preserved internally** and is reachable by drill-down, which separates *missing*, *partial* and
*stale*; **each column's percentage is fully verified applicable feature cells over applicable
feature rows and never an average of subtask percentages**, by *Counting* above; **an empty
denominator stays explicitly undefined**; discoveries append visibly and preserve the baseline
membership; and **a scope removal can never wear completion's clothes**.

**One renderer, two modes, byte-identical output.** A **read-only Markdown mode** prints the
selected roadmap's table so a coordinator can paste it without writing a file or asking a model to
recompute a cell, and the **file mode** writes the same bytes into a marked region — the same
renderer, rendering from **one captured work, evidence and scope revision whose provenance is in
the receipt**. The file mode **changes only the selected marker region**, refuses a missing,
duplicate or reversed marker pair, writes atomically and **preserves every other byte**; a check
mode reports drift and writes nothing; and **rendering into a repository is not permission to
commit or push it**, existing working-tree edits being respected. **Requiring the two modes to be
byte-identical for one projection and revision is a test**, shared prerequisites and private-data
filtering included (replay `chat-and-file-render-are-byte-identical`).

**A projection's target is stored view metadata, not a remembered command line.** Each configured
projection records its target repository and path, its marker pair and its display policy as
**non-executable** metadata on the roadmap, resolved **only within explicitly configured permitted
target roots**; the target state and the publishing receipts are stored **apart from the
authoritative progress data**; and **a missing mapping or a conflicting change is an explicit
refusal and never a guessed destination** (replay `render-refuses-a-target-outside-its-roots`).

**Every capability the fixed-table prototype has is kept, and its two known defects are fixed
rather than copied.** Kept: bounded restricted-data validation; the duplicate, dangling and cycle
checks; required-work rollups; exact roadmap selection; Unicode-safe marker replacement;
deterministic rerendering; drift checks; refusal of empty and whitespace-only evidence; and no
sentinel collision at a literal end of file. Fixed as production work: **the quadratic
descendant-set** and **the pre-parse depth gap** — *copying the prototype verbatim is not
completion*. Derived cell and column summaries are cached and the affected references invalidated
after a mutation, rendering costs the selected output's size, and **the whole table is never
claimed constant-time when it holds many cells**. Real operational token use is measured before and
after adoption at equal report quality, with implementation tokens sunk and excluded.

## The validator *(Rowan; every rule is a hurt already paid)*

The validator is one set of rules run two ways: **whole**, at session load and at every clip,
walking O once and printing one `WORK FAIL` line per finding, capped **per rule**, with one
count line always, exit 1 on any finding; and **on the candidate**, at every mutation, over the
resident O as it would be with the event applied, touching only the nodes the event reaches
(rule 11 for the node the event addresses; rules 1 to 10, 14, 15, 17 and 18 for the nodes
it names), refusing the event at exit 1 with the finding's line and changing nothing. Rules 12,
13 and 16 are the reader's, exit 2, at load. The validator never fetches; what a pointer proves is
`verify`'s, and an unverified pointer is a count, never a finding.

1. **duplicate id** — two nodes, or two events, with one `:id`.
2. **dangling reference** — a `:children`, `:deps`, cell `:ref`, event `:node`, `:lease`,
   `:blocked-by`, `:superseded-by` or `:criterion` that names nothing. **A name resolves against
   O's nodes or C's closed index, whichever branch holds that id**, so a reference to an item
   that has settled is not dangling — a finished task is still the task its parent contains and
   its cell points at — and the rule stays green with the retention archive absent, because the
   closed index is published with the snapshot and the body is what the archive holds. **The
   resolution against C is a required indexed dependency lookup**, one of the two ways the
   retention section above reaches past the default window: one bounded lookup for the one id,
   never a day opened whole. **Where the page that lookup needs cannot be read, the rule reports
   that it could not check and never that the reference is broken, and never that it is green**:
   `WORK FAIL <id>: rule 2: unavailable partition=<yyyy-mm-dd>`, which is a finding at exit 1 with
   `unavailable` as its reason and not `dangling`, because *incomplete* and *invalid* are two
   different answers and a validation that reported neither would be claiming every rule valid
   with its history missing (Stella at e79847fb, W1) (replay `rule-2-unavailable-is-not-green`); and, **for a first-axis
   member that is in its roadmap's required set**, a member naming nothing, or naming a node
   that is not of its roadmap's declared `:row-kind`, is the same finding, because such a member
   is counted by `rows=`
   and a mistyped or wrongly typed row was a phantom in the denominator that no rule refused
   (Opus at 7472e545, 2026-09-13). **What this rule checks for a first-axis member is the
   roadmap's required set and never the axis list**, so **a member whose feature has been
   removed, cancelled or superseded is not checked at all**: it has left the set by the delta
   table above, and the rule must not demand a live node for it. The hurt is the one draft 19's
   rule 7 paid: a removed node, its subtree and its events pass into the retention archive at
   the first clip after the `:remove` crosses the retention boundary, **a session whose archive
   file is absent must answer every ask and run every rule**, and a rule that wanted the node
   behind a departed member would find nothing from that clip on — every later clip red, the
   session accepting work it could never share, and a restart refusing to load (Opus at
   efc26a2e, 2026-09-13; the wedge is invisible inside a `--retain` window, which is why the
   replays below run a clip past the boundary). A member of any other axis is a column label,
   names no node, and is not checked here.
3. **cycle** — `:children` edges are not a forest, or `:deps` edges contain a cycle (a
   dependency cycle is a deadlock nobody can finish).
4. **two parents** — a node under two `:children` lists.
5. **done without evidence** — a `:to :done` transition naming no evidence events, or naming
   ones whose criteria do not cover the node's `:acceptance`, or of an older generation than
   the node's, or whose pointer is a `note:` scheme.
6. **green parent, unfinished child** — a parent derived `:done` while a required child is not.
7. **bad cell** — an unknown axis member or a duplicate coordinate. **A missing cell is not a
   finding.** An axis member begins with no cells, so `axis --add` would be refused by the
   candidate gate that runs this rule and, were the clause moved to the whole validation, by
   every clip until its cells existed — the roadmap could never grow a column, and the cells
   could never be written into the column that was refused (Fable at d1b20f42, 2026-09-13).
   What the hurt asked for is that an omitted cell never count as complete, and the `:roadmap`
   kind above says exactly that (5653990830).
8. **two live leases** on one node.
9. **lease without deadline or default.**
10. **invalid transition** — a transition whose **target state** the table does not allow from
    the node's state as derived from the events before it, or `:blocked` without `:blocked-by`
    (5653982211: *incompatible states, invalid scope transitions*). **The target state is the
    `:to` of a `:transition` and, for the four scope events that are also transitions, the state
    the kinds section names for that kind** — `:deferred`, `:cancelled`, `:todo` for a `:reopen`,
    `:superseded` — because a scope event carries no `:to` at all, and a rule that read one would
    have checked nothing on exactly the four events it was widened to cover (Fable at 7472e545,
    2026-09-13).
11. **scope change without event** — the direct required set of a roadmap or work set differs
    from its last `:baseline` plus the scope events recorded for it — **its own and those of
    its members, which are its by the table above** — **each applied by the per-kind delta
    of that table**, which is the arithmetic this comparison uses (a `:defer` or a
    `:reopen` moves the revision and no member, so a set that changed across one is a finding
    and not a rounding). **Where that `:baseline` is behind the loaded snapshot's retention
    boundary, the comparison starts at the boundary's recorded baseline members for the node
    and folds the scope events since**, which is why the boundary carries both the set and the
    baseline membership.
12. *(reader, exit 2)* **reader payload** — `#.` or any other refused syntax.
13. *(reader, exit 2)* **bounds exceeded** — bytes, depth or nodes past the flags.
14. **conflicting revisions** — a second `:baseline` for a node whose members differ from the
    required set derived at that point (its previous baseline plus every scope event since, by
    the delta table above; or, where that previous baseline is behind the loaded snapshot's
    retention boundary, the boundary's recorded set plus the scope events since it). Two baselines are never at one revision, since every scope event
    moves it, so the finding this rule must catch is the one that hurt: **a re-baseline that
    silently moves a denominator**. A re-baseline may only restate what is already derived;
    the honest ways to move a denominator are `:discovery`, `:remove`, `:cancel` and
    `:supersede`, each with an author and a reason (5653970526: *reject … conflicting
    revisions*). Staleness of evidence is a count, not a finding, by rule 17 below.
15. **no acceptance** — a required `:task` with no `:acceptance` entry, which could never be
    done.
16. **unknown type** is the reader's, exit 2, like rules 12 and 13, and a bound of zero or
    less is refused the same way (SPEC.md: *a budget of zero or less is likewise refused*).
17. **stale at the moment of claiming** — a `:to :done` whose cited evidence is already stale
    (its `:against` is not its node's current source revision, a local comparison) or already
    found-not-qualifying for its criterion **by a raw fact the session's verification cache
    already holds** (named at `session start` by `--cache`, so the candidate gate derives its
    verdict from a fact `verify` fetched and never fetches itself); refused in the candidate
    gate (5653982211: *stale evidence*). An evidence
    pointer the cache has never seen is unverified, which is a count, not a finding.
    After the claim, staleness that arrives with a later source revision is a count, not a
    finding, so a source bump never freezes the set; the rollup already demotes it.

18. **in two branches** — one `:id` in both C and O, or an item of C holding a live lease.
    The root is a partition: `open=` and `closed=` sum to the scope's counted total and no id is
    counted twice, so the arithmetic is guarded by a finding rather than repaired at read time,
    and W is a view inside O rather than a third membership (the root section above). A
    `:settle` whose item is already in C, and a `:revive` whose item is already in O, are the
    two ways to write one, and each is refused at the candidate gate before anything is written.
    **Branch membership is read at the latest state of an id**, by the root section above, so an
    id that settled, revived and settled again is one branch at any instant and never
    a finding. **The partition this rule guards is over CURRENT membership and never over
    historical occurrence**: C's day partitions hold a closure row for every settle that ever
    happened, so an id now in O has rows in C's history by design, and reading one of those rows
    as a second membership would make every honest reopen a finding (Stella at e79847fb, W2:
    historical C containing a revived id must be distinguished from current C membership for the
    partition invariant). The finding is an id whose newest transition row and whose place in O's
    tree disagree at one instant, and nothing older than that row; and a `node remove` settles only the open items beneath it, for exactly this
    reason, rather than being refused by this rule over a subtree that holds finished work.

**There is no rule about an empty required set, and its deletion is part of this draft**
(Stella 5655371246 item 4, Fable and Opus at d1b20f42, 2026-09-13). Draft 19's rule 7 reddened
every `:work-set`, `:feature` or cell with an empty required set at load and at every clip. But
`<repo>/shared` is required of every repository and holds no work in a repository that has
none; a container's last member leaves it legally by `:cancel` or by `node remove`; and a
repository registered before its first task is empty by design — so the rule refused the honest
empty scope along with the hole, wedged every later clip, and left the session accepting work it
could never share, with no remedy on its line. **No validator can tell an honest empty container
from a hole, so the honesty is kept where it was already kept and not by a finding**: the kinds
above say *an empty required set is **never** done*, so an empty container counts `unknown` in
every rollup, green in none, and is a finding in none. What the hurt asked for is that an
unjustified claim of completion be refused, never that an empty work set be forbidden to exist.
**The record says the rule was overruled, not that it was unopposed**: Johnny kept rules 5 to 7
on the record (5654622375 item 4; 5654749489, *rules 5–7 … still hold*), and this deletion
overrules him for the reason above (Fable at 7472e545, 2026-09-13). **And the honest empty needs
one sentence more, or the hole only moves**: `<repo>/shared` is created `:required false`, because
a repository holding no shared work must not count `unknown` forever on the strength of a
container the tool itself insists on, and rule 6 would keep its parent short of done for as long
as it stood required and empty; a repository that does have shared work makes it count with
`nova-work node require --node <id> --to true --reason <text>`, a recorded act with an author
and a reason, writing a `:require` scope event on its containment parent's set by the delta
table above; `--to false` takes it back out, detaching nothing. **The verb is named here because
before it there was none**: `node add` creates, a second `node add` of one id is rule 1, and
`accept` moves criteria only, so *makes it count* named a command nobody could run, and *carried
`:required false` until it holds work* read as an automatic flip no event recorded (Fable at
efc26a2e, 2026-09-13). `:required` still defaults to true on `node add`; nothing keys a default
on a node's name, and `<repo>/shared` is created with `--required false` by the hand or the
intake that creates it.

## Cost *(shared; 5653973972, 5654049969 and Stella's amendment)*

At load the session builds six indexes — id to node, containment adjacency, reverse dependency,
repository, category (5654164074), and **reverse roadmap reference: from a node id to every
roadmap that has it as a first-axis member, and to every cell whose `:ref` names it** — and every
walk goes through them. **A seventh is opened rather than built or loaded: C's closed index**, which the
clip publishes beside the snapshot in bounded pages and which no walk over O has to rebuild —
keyed by `:id` and by `<event-rev>:<id>`, partitioned by event day, ordered by event revision, and
grouped by repository, so a rollup reads a settled member's newest row in one bounded lookup, a
closed listing reads the pages of one revision range, and `--after` continues it. **A
settled item therefore costs a row and never a body**: the O(V+E) of a full validation is O's
edges plus one bounded lookup per settled member, and neither the fold nor the load grows with how
much work the team has finished, which is the whole reason C is a branch with an index rather than
more of O. **The eighth is the dedup index and is opened the same way**, in pages under the same
bounds, for the same reason: neither index is resident, and what is resident is a cache of at most
`--index-cache` pages across both. **What this section promises about either is bounded indexed
access and never O(1)**: a cold lookup may read as many pages as the index is deep, and the
session prints `pages=<n>` on the answer so the number is read rather than assumed. **The
measurement that keeps all of this honest is one experiment and it is named**: hold O, the
recent-window volume and the page bounds fixed, grow the old history, and assert that startup
resident bytes, index pages read, segment bytes read, parses, replays and emitted bytes do not
move (replay `history-grows-startup-does-not`, and Stella's `SPEC-WORK-CLOSED.md` acceptance 6,
whose *no whole-C load, no directory scan and no whole-history dedup load on the ordinary path* is
what the replay asserts). **The sixth is on the write path and not the read path**: a `:cancel`, a
`:supersede` and a `node remove` must each move *every roadmap that has it as a row* by the delta
table above, and `node remove` must find its cell referrers in order to refuse; with no such
index each of those is a scan of every roadmap on every cancel, on the write path, which at nine
ports per row over a large set is a cost this section promises nowhere (Opus at 7472e545,
2026-09-13). A mutation
updates only the affected index entries and invalidates only the affected derived values
(ancestors over containment, dependents over the reverse-dependency index, the projections
that reached them); an unchanged indexed query costs **zero parses and zero replays**, and the
session counts parses, replays, visits, journal writes and emitted bytes so the claim is
measured, never asserted (Stella, *Required measurements and replays*). Deriving a node's
current state from the journal is one pass at load, O(E_log), and incremental thereafter.
A full validation or fold visits every node and every edge once: O(V+E), with a visited set
for shared subgraphs, and detects cycles in the same walk. Counts roll up bottom-up over the
containment forest in O(V), cached per node keyed by its scope revision, the journal position
and the verification cache's revision, since a `verify` pass changes which done counts as done.
**The root and container open-item counters are the exception and are maintained on the write
path**, by the *Counting* bullet above: a mutation pays for the ancestors it touches and `|O|` is
then a read, so the cost of asking how much is open does not grow with the set — which is the one
number a coordinator asks for most often and the one a lazy rollup would make expensive exactly
when the set got large. **No transitive descendant set is materialised anywhere**; an ad hoc set query
walks the reached subgraph once, O(V_reached + E_reached), when it is asked. Propagation over
the dependency DAG is one affected topological pass, never a whole-S fixed point (Stella,
*Reusable recursion*). **These bounds are the resident graph's only**: fetching evidence is
`verify`'s, bounded by `--max-fetch` and `--fetch-timeout`, cached at `--cache`, and never
part of validation. Rendering a matrix costs its cell count; a clip costs its snapshot's size.
A dense dependency graph has a large E, and the session prints `edges=<n>` rather than
promising otherwise; every `OK` line prints `emitted=<bytes>`. No constant-time promise for
an arbitrary structural edit or a change reaching most of the graph. **Tests assert visit,
parse, replay and allocation counts, never wall time**, on four shapes: tree, shared DAG, deep
chain, high fan-out, and on one multi-command session.

## Output grammar

Every line's first token is the verb's (`SESSION`, `EXPORT`, `REPLAY`, `HANDOFF`, `CLIP`,
`OPERATION`, `CHECKPOINT`, `UNDO`, `REDO`, `FRIEND`, `CONFIG`, `MODEL`, `OBSERVE`,
`WORK`, `VERIFY`, `QUERY`, `RENDER`, `NODE`, `DECOMPOSE`, `DEP`, `AXIS`, `CELL`,
`RESPONSIBLE`, `ACCEPT`, `SOURCE`, `LEASE`, `HEARTBEAT`, `RELEASE`, `ATTEMPT`, `EVIDENCE`,
`ATTESTED`, `STATE`, `CORRECT`, `EVENT`), the second is `OK` or `FAIL`, `RACED` for a push the base predicate
refused (SPEC-MERGE rule 21's shape, exit 1, nothing pushed), or one of the informational
tokens `ROW`, `NOTE` and `MORE`. `OK`, `ROW`, `NOTE` and `MORE` go to stdout; `FAIL`, `RACED`
and refusals go to stderr. Every count line prints on failure as on
success. Every `OK` line ends `emitted=<bytes>`. Every mutation's `OK` line carries the
event's id, its request id, the session's local revision after it (`rev=<n>`), and
`pushed=<rev|->`, the clipped revision, the same number as the last `CLIP OK`'s `pushed=`;
`SESSION OK` is one shape, printed by `session start`, `session status` and `session stop`
alike, and its `state=` reads `live`, `fenced` or `red`: lowercase *active* is W's word in the
root section above, `ACTIVE` in capitals is the per-friend live-data node, and neither names a
session state here.

```
SESSION OK session=<path> owner=<name> generation=<n> state=<live|fenced|red> until=<stamp> file=<path> base=<sha> journal=<path> events=<n> pending=<n> pushed=<rev|-> nodes=<n> edges=<n> parses=<n> replays=<n> every=<duration> skew=<duration> clip-every=<duration> clip-after=<n> retain=<duration> index-cache=<n> page-bytes=<n> page-records=<n> closed-window=<duration> max-bytes=<n> max-depth=<n> max-nodes=<n> boundary=<rev> findings=<n> build=<identity> emitted=<bytes>
SESSION FAIL session=<path> owner=<name> generation=<n>: <reason>
SESSION RACED session=<path> generation=<n> expected=<sha12> found=<sha12>   (printed by the session's own reconfirm, on its stderr; the fence that follows is read by `session status`)
EXPORT OK session=<path> into=<path> requests=<n> base=<sha> pushed=<rev|-> emitted=<bytes>
EXPORT FAIL session=<path> into=<path> requests=<n> base=<sha> pushed=<rev|->: <reason>
REPLAY OK from=<path> requests=<n> applied=<n> refused=<n> pushed=<rev|-> shown=<n> emitted=<bytes>   (refused=0, exit 0)
REPLAY FAIL from=<path> requests=<n> applied=<n> refused=<n> pushed=<rev|-> shown=<n> emitted=<bytes>   (refused > 0, exit 1, the same fields on stderr)
REPLAY ROW request=<id> verdict=<applied|refused> rev=<n>: <reason>
HANDOFF OK session=<path> generation=<n> to=<name> commit=<sha> pushed=<rev> emitted=<bytes>
HANDOFF RACED session=<path> generation=<n> to=<name> expected=<sha12> found=<sha12>
HANDOFF FAIL session=<path> generation=<n> to=<name> pushed=<rev|->: <reason>   (a push refused other than by the base predicate, SPEC-MERGE rule 21's BLOCKED case among them)
ATTESTED OK id=<event-id> request=<id> node=<id> rev=<n> pushed=<rev|-> criterion=<id> against=<sha> emitted=<bytes>
CLIP OK session=<path> boundary=<request-id> events=<n> base=<sha> commit=<sha> pushed=<rev> attempts=<n> emitted=<bytes>
CLIP RACED session=<path> boundary=<request-id> generation=<n> expected=<sha12> found=<sha12>
CLIP FAIL session=<path> boundary=<request-id> events=<n> base=<sha> pushed=<rev|-> attempts=<n>: <reason>   (a whole validation that found anything is `findings=<n>`, its `WORK FAIL` lines printed above it)
WORK OK nodes=<n> edges=<n> events=<n> leases=<n> expired=<n> escalated=<n> stale=<n> scope=<rev> source=<sha|-> pushed=<rev|-> emitted=<bytes>
WORK FAIL <id>: rule <n>: <reason>
WORK FAIL nodes=<n> findings=<n> shown=<n> expired=<n> escalated=<n> stale=<n>
VERIFY OK pointers=<n> verified=<n> unverified=<n> stale=<n> fetched=<n> cached=<n> pushed=<rev|-> emitted=<bytes>
VERIFY ROW <event-id> pointer=<p> verdict=<verified|unverified|stale> at=<stamp>
VERIFY FAIL pointers=<n> verified=<n> unverified=<n> stale=<n> fetched=<n> cached=<n> pushed=<rev|-> shown=<n>
QUERY OK ask=<kind> scope=<rev> membership=<rule> branch=<open|closed|root> unit=<unit> source=<sha|-> freshest=<stamp|-> done=<n> done-unverified=<n> unknown=<n> deferred=<n> cancelled=<n> superseded=<n> stale=<n> required=<n> since-baseline=<n> private=<n> open=<n> closed=<n> gap=<n> [from=<stamp> to=<stamp> closed-in=<n> settles-in=<n> revives-in=<n> items-in=<n>] [green=<k> applicable=<n> baseline-rows=<n0> row-kind=<kind>] [held-not-worked=<n> unowned=<n>] [leases=<n>] [responsible=<name|->] pushed=<rev|-> rows=<n> shown=<n> pages=<n> parses=<n> replays=<n> emitted=<bytes>
QUERY ROW <id> kind=<k> state=<s> k=<n> n=<n> unknown=<u> responsible=<name|-> holder=<name|unowned> heartbeat=<age|none> deadline=<stamp|-> escalated-to=<name|-> blocked-by=<id|->
QUERY ROW <id> lease=<lease-id> holder=<name|unowned> heartbeat=<age|none> deadline=<stamp|-> default=<release|extend-once|escalate:<name>|-> escalated-to=<name|-> responsible=<name|->   (who, stale)
QUERY ROW <id> branch=<open|closed> disposition=<pending|working|deferred|done|cancelled|superseded|removed> repo=<o/n|-> kind=<k> state=<s> landed=<sha|-> released=<version|-> holder=<name|unowned> settled=<stamp|-> evidence=<n> verified=<n> responsible=<name|->   (done, remaining and under, under --branch closed or --branch root)
QUERY NOTE coverage-gap file=<name> range=<rev>-<rev>   (rows whose bodies the retention archive holds, or whose day partition or manifest the committed root names, and this read could not reach; an absent day inside a complete manifested range is no events and prints no note)
QUERY ROW <lease-id> node=<id> kind=<lease|heartbeat|release|handoff> rev=<n> at=<stamp> from=<name|-> to=<name|-> deadline=<stamp|-> default=<release|extend-once|escalate:<name>|->   (handoffs)
QUERY FAIL ask=<kind> rows=<n> shown=<n>: <reason>
QUERY FAIL ask=<kind> as-of=<stamp> partition=<yyyy-mm-dd>: historical window unavailable   (a state-as-of ask whose day partition the committed root names and this read could not open; never answered from a later row)
QUERY FAIL ask=<kind> after=<cursor> pinned=<rev> current=<rev>: page expired   (a continuation whose captured revision the session can no longer serve; never a drifted page)
OPERATION OK id=<id> op=<kind> state=<queued|running|done|cancelling|cancelled|failed> started=<stamp> updated=<stamp> staged=<bytes> rev=<n|-> pushed=<rev|-> shown=<n> emitted=<bytes>
OPERATION ROW id=<id> op=<kind> state=<s> started=<stamp> updated=<stamp> external=<known|uncertain|none>   (operation list, and one per event of a wait's cursor)
OPERATION NOTE waiting id=<id> timeout=<duration> after=<cursor>   (a wait that timed out: the operation is still running, and this line says so)
OPERATION FAIL id=<id> op=<kind> state=<s>: <reason>
CHECKPOINT OK id=<id> kind=<local|shared> rev=<n> pushed=<rev|-> boundary=<rev> age=<duration> unshared=<n> manifest=<sha> shown=<n> emitted=<bytes>
CHECKPOINT ROW id=<id> kind=<local|shared> rev=<n> at=<stamp> manifest=<sha> verdict=<good|corrupt|unverified>
CHECKPOINT NOTE recovery-gap kind=<missing-tail|remote-unavailable> since=<rev>   (reported, never rounded to success)
CHECKPOINT FAIL id=<id> kind=<local|shared> rev=<n>: <reason>
UNDO OK id=<event-id> request=<id> request-of=<id> nodes=<n> rev=<n> pushed=<rev|-> emitted=<bytes>   (redo prints REDO OK with the same fields)
UNDO ROW node=<id> effect=<state|scope|assignment|counter|verification> before=<text> after=<text>   (an undo-plan's or redo-plan's rows; redo-plan prints REDO ROW)
UNDO FAIL request-of=<id> expect=<rev> current=<rev>: stale plan   (nothing written)
UNDO FAIL request-of=<id> effect=<external> handle=<text>: not reversible here   (a sent message, a paid execution, a publication, a source deletion)
CONFIG OK friend=<name> verdict=<unchanged|manifest|delta> base=<hash|-> revision=<n> hash=<hash> parts=<n> emitted=<bytes>
CONFIG FAIL friend=<name> base=<hash|-> verdict=<schema|identity|hash|incomplete>: <reason>   (the old config is left intact)
RENDER OK view=<id> cells=<n> private=<n> bytes=<n> into=<path> pushed=<rev|-> emitted=<bytes>
RENDER FAIL view=<id> cells=<n> private=<n> drifted=<n> into=<path>: <reason>
NODE NOTE already-closed node=<id> disposition=<d> settled=<stamp>   (a `node remove` of an item already in C: nothing written, exit 0, its NODE OK line following)
<MUTATION> OK id=<event-id> request=<id> node=<id> rev=<n> pushed=<rev|-> ... emitted=<bytes>
<MUTATION> FAIL node=<id>: rule <n>: <reason>
<MUTATION> FAIL node=<id> expect=<rev> current=<rev>: stale
<MUTATION> FAIL request=<id> applied=<rev>: already applied
<MUTATION> FAIL request=<id>: reused with a different payload
<MUTATION> FAIL request=<id> page=<name>: dedup unavailable   (a dedup page the predicate needs and could not read: the request is refused admission, never admitted as new)
<MUTATION> FAIL node=<id> findings=<n> was=<n>: no repair   (--repair only)
LEASE FAIL node=<id> holder=<name> since=<stamp> deadline=<stamp> live=<n>: held
<TOKEN> NOTE <caveat>
<TOKEN> MORE kind=<rule|row> shown=<n> total=<t> <remedy>
nova-work <build identity> <goos>/<goarch> <go version>
```

where `<MUTATION>` is one of `NODE`, `DECOMPOSE`, `ACCEPT`, `SOURCE`, `DEP`, `AXIS`, `CELL`,
`RESPONSIBLE`, `LEASE`, `HEARTBEAT`, `RELEASE`, `ATTEMPT`, `EVIDENCE`, `ATTESTED`, `STATE`,
`CORRECT`, `EVENT`, `UNDO`, `REDO`, `FRIEND`, `MODEL`, `OBSERVE` and `CONFIG` (its `--intake`
form alone), each
adding the fields its section names (`LEASE OK … holder= deadline= default= live=`, `STATE OK
… from= to= evidence=`, `ATTEMPT OK … by= result= generation=`, `EVIDENCE OK … criterion=
against=`, `CORRECT OK … generation=`, `EVENT OK … kind= scope=`, and `DECOMPOSE OK …
children=<n> unit=leaves leaves-before=<n> leaves-after=<n> unit=features features-before=<n>
features-after=<n>`, which is the both-units promise of the scope section printed).
**Every token is its verb uppercased but three, named here so no reader infers them**: `take`
prints `LEASE`, `attest` prints `ATTESTED`, and **a `release` refused because its `--as` is not
the lease's holder prints `LEASE FAIL`** — that refusal is about the lease it could not end —
while every `release` that runs prints `RELEASE OK` like any other verb's (Fable at 7472e545,
2026-09-13). **`pushed=<rev|->` is on every scope line** —
`SESSION OK`, `EXPORT OK`, `REPLAY OK`, `WORK OK`, `VERIFY OK`, `QUERY OK`, `RENDER OK` and
every mutation's — **and on no row**, where it would be one number repeated per line.

**`verify` prints exactly one count line**: `VERIFY OK` when `unverified=0` (exit 0) and
`VERIFY FAIL` when it is above zero (exit 1, on stderr), never both, with one `VERIFY ROW` per
evidence event under `--max` in either case. **`session stop` prints its clip's `CLIP OK` line
first** (unless `--no-clip`), then one `SESSION OK` whose `owner=` is the name it released and
whose `until=` is the stop's stamp, exit 0; a stop whose clip is raced prints `CLIP RACED`,
releases no `OWNER`, exits 1, **and leaves the session process up and fenced**, keeping its
journal and its lock — so the way on from a raced stop is `session export` and then a new
owner, exactly as from any other fence, and the journal's holder is alive until that process
ends. **Neither `session stop` nor `session handoff` takes a
`--max` of its own**: the validation lines their clip prints are capped by the `--max`
`session start` was given, as the periodic clip's are.

Exit 0 the verb ran and passed; 1 it ran and said no (a finding, a refused take, a refused
transition, a stale expectation, drift, a divergence, a fenced session, and for `verify` its
own verdict `unverified=<n>` above zero, which is a report and not a `check` finding); 2 it could not run (a
missing flag, no such session, a refused snapshot, bounds exceeded, an unusable invocation,
which costs one line ending `run: nova-work help`). One line per event, escaped through
`internal/oneline`; every listing capped by `--max` with a `MORE` line naming the remedy; the
version line is `internal/buildinfo`'s.

## Acceptance replays *(shared; Glenn's list, 5653970526, and Stella's amendment)*

Fixtures, each tiny, each a test: nested completion; a failed gate; a shared dependency
counted once; unknown evidence; unverified evidence changing no recorded state and counting
as unknown in every rollup; stale evidence counted as unknown; a `note:` pointer refused as
evidence for done; a `run:` pointer to a failed run resolving and counting as unverified; a
structure-plus-scope envelope crashed between its two events replaying all-or-none; a task split by `decompose` (both units printed, revision moved); scope
expansion by `node add` (added since baseline visible, new rows at the bottom, `baseline-rows`
printed); deferral (cannot raise the done count, cannot raise the roadmap percentage, and cannot
raise a focus's completion ratio: a deferred row stays in `rows=` and a deferred leaf stays in
current required work); reopening; two processes opening one journal, the second refused; a
passing test of another name failing to qualify a criterion; a `correct` event voiding an
earlier qualification for the done claim; a correction bumping the
generation and an older attempt's result refused at `state --to done`; future work cannot
lower the active percentage; an out-of-scope cell leaving the applicable rows; a changed
nested task updates every affected view and no unrelated cached projection; `--at` replaying
to an earlier revision; malformed and cyclic data refused at load; a `#.` payload refused at
the reader; a `:deps` cycle refused; a cancel request withdrawn and a cancel confirmed; a deep
chain with no quadratic work (visit counts asserted); a lease past its deadline reads as
unowned, its responsibility unchanged, and its release is not blocked; `:extend-once` once; a
lease reads `pushed=-` until its clip reaches the branch; an invalid transition refused;
a refused mutation leaving O, the journal and the indexes unchanged; `render --check` fails
on one changed cell; **start once, run many** (zero parses and zero replays on unchanged
indexed queries, counts asserted); incremental results equal a clean reconstruction of the
same accepted revision; a crash after journal durability and before acknowledgement, then the
same request retried, yields one accepted event; a crash or disconnect during a clip retains
every accepted event and reports the last confirmed shared checkpoint honestly; a second
coordinator refused while one is owning; a controlled handoff by `session handoff` (the old
owner refuses writes from the handoff's admission, the successor starts with no wait, the
generation moves by one); a hand edit on the branch under a live session (the next reconfirm
is `SESSION RACED`, the session fences, the edit is on the branch untouched, the fenced
session's bundle replays into the next owner); a periodic clip on `--clip-after` and one on
`--clip-every`, and none with nothing pending; a replayed request carrying a stale clipped
revision refused with the current one printed; an
old owner returning with a delayed request, refused by generation; two sessions on different
socket paths naming one journal, the second refused `journal held`; an owner that is stopped
(`SIGSTOP`) and answers nothing on its socket keeps its journal lock and its lease until
`until`, and a second start in that window is refused; a journal copied to another bench
starts as a taker with a fresh generation and never as a resume; a reconfirm callback
delayed past `until` admits no write; a `:defer` moving the scope revision and no denominator, and a `:reopen` the same; `event
--kind split` and `--kind remove` refused at exit 2 naming `decompose` and `node remove`; a
`node remove` leaving its subtree in O as provenance and out of every count, and one refused
for a live lease; a re-baseline restating the derived set accepted and one differing from it
refused by rule 14; **an empty work-set — a `<repo>/shared` in a repository with no shared work,
and a container whose last member was cancelled — accepted, clipped, reloaded and clipped again
with no finding, counting `unknown` and never done and never green in any rollup**; a
`decompose` with a child missing acceptance refused
whole, and `accept --add` closing it afterwards; an `<id:kind:subject:predicate>` whose
subject holds `:` and `@` parsed by position; a scheme with no `--resolver` reported
unreachable and never guessed; one cached raw fact yielding two verdicts for two criteria, and
a `correct` event changing a verdict with no fetch; a `source` bump staling evidence and a
later `source` clearing it; a `QUERY OK` carrying exactly the fields its ask names; a lease
whose default is `(:escalate "<name>")` reading `escalated-to=` at expiry with responsibility
untouched; a clip refused because its retained events would pass `--max-bytes`, with
`lower --retain` named as the remedy and a lower `--retain` then passing; a removed subtree
written into the archive by the clip that carries its `:remove` past the boundary and absent
from the snapshot's structure thereafter, the live snapshot bounded by `--retain` across it;
a snapshot loaded as retention boundary plus retained events equalling a clean
reconstruction, and `--at` before the boundary refused naming the retention archive;
**a session started on a snapshot whose archive file is absent answering every ask of the
query table and running every rule of the validator**, with rule 11 and rule 14 green, and
`baseline-rows=`, `since-baseline=`, `stale=`, `freshest=`, `pointers=`, `unverified=` and
every `done-unverified=` equal to the same load with the retention archive present, **including
a `verify` over an evidence event no `:to :done` names**, whose `VERIFY ROW` carries the same
pointer and verdict either way; `handoffs --since` before the boundary refused like `--at`;
a `:cancel` on a member moving its containment parent's scope revision and denominator and
that of every roadmap that has it as a row, with rule 11 green on the walk after it, and the
same for a `node remove` — **the roadmap's row removed from its required set, `rows=` lower by
one, its cells cleared first and rule 11 green on the next whole walk**; **a first-axis member
naming no node, and one naming a `:task`, each a rule 2 finding, and a column label on a second
axis none**; **a `node remove` of a roadmap row carried past the retention boundary — the
`:remove` clipped, `--retain` elapsed, the node and its subtree written into the archive by the
clip that crosses it — leaving rule 2 and rule 11 green on that clip and on every clip after it,
and on a fresh `session start` from that snapshot with the archive file absent**, the member
still on the first axis and out of the required set and out of `rows=` (the replay runs clips
past the boundary, because inside a `--retain` window the wedge is invisible); **a `node remove`
refused for a cell `:ref` naming a node of the removed node's subtree, naming that node**; **an `event --kind discovery --node <roadmap>` refused at exit 2 naming `axis
--add`, and a `node add --under <roadmap>` refused at exit 2 the same way**; **a `percent` with
no `--axis` refused at exit 2 naming the flag**; **a `node require --to true` on a
`<repo>/shared` moving its parent's required set by one and detaching nothing, `--to false`
moving it back, and each a `:require` event with an author and a reason**; **a `percent --axis <member>` over a roadmap of ten rows with one out-of-scope cell for
that member printing `applicable=9 rows=10 baseline-rows=10` and its percentage taken over
`applicable`, the same ask under another member printing `applicable=10 rows=10`, and a `percent`
over zero applicable rows printing `green=0 applicable=0` with no percentage at exit 0**;
**`since-baseline=` after a discovery and a later cancel of the discovered member reading 1 and
not 0, with `rows=` back at `baseline-rows=`**; **`since-baseline=` reading the same number on a
`unit=features` line and on a `unit=leaves` line over one discovered feature holding twelve
leaves, and a `stream --repo` over three containers reading the sum of their three counts**; **an `axis --add` on a roadmap's first axis moving its required set
and its `rows=` by one, and an `axis --add` on a second axis moving its revision and neither**;
**a `cell --ref`, a `cell --ref -` and a re-point moving no required set and no `rows=`, with
rule 11 green on the walk after each**, and `percent` over a roadmap of one row and nine columns
printing `rows=1`; **an `axis --add` on a roadmap accepted with no cell yet written for the new
member, and its cells written afterward** (rule 7 is not a missing-cell rule), the member's
uncelled coordinates counting as not complete; **a `:reopen` from `:done` and one from
`:deferred` each landing in `:todo`, a `:defer` of a `:done` node refused by rule 10, and a
`:supersede` of a `:cancelled` node refused the same way**; **one `run:<o/r>#<id>@<sha>` pointer
used for the criterion whose subject is its job and again for a criterion naming another job,
and again at another sha: only the matching criterion qualifies, the cache holds three keys, and
the answer is the same under `--offline`**; **a request id retried after the clip that carried
its event past the retention boundary, and again against a successor after a handoff, refused
`already applied` with the revision named and applying nothing, and the same id with a different
payload refused `reused with a different payload`**; **a request id whose event is newer than the
retention boundary and still in the snapshot's retained events retried against a successor after
a handoff, refused `already applied` and applying nothing** (the window the index alone did not
cover); **a retry inside the owner's own journal with the same payload answered with the original
`OK` line, and one with a different payload refused `reused with a different payload`**; **one
request digested to one value by two independent serializers, over a `node add` envelope holding
a structure event and a scope event, with two `:stamp`s and two `:request` ids and the same
digest, and with an absent optional field written `()` by both**; **a clip whose snapshot passes `--max-bytes` refused with all four
numbers printed and `lower --retain or raise --max-bytes` named, the lower `--retain` then
passing, run with the published index bytes larger than the retained part so the remedy is tested
on the flag that moves the number and not on the sizes**; **a `session export --journal` writing
every request's `--expect` and the bundle's `base=` from the journal's newest clip boundary
record, with no repository present, and the bundle replaying whole**, its `base=` equal to the
live `session export --session`'s for the same journal; **a bundle holding two requests against
one node — `state --to doing` then `evidence` — replaying whole, both applied at one clipped
revision, and the same two refused `stale` where the coordinator itself moved that node between
the clip and the replay**; **a journal rotated at a clip, its fresh file opening with a copy of
that clip's boundary record, read by a `session start` and by `session export --journal` to the
same bundle as the unrotated one**; **a replayed request over a node the
coordinator changed since the clip refused `stale`, and a replayed request over a node it did
not touch applied, both at the same clipped revision**; **a fenced session killed before it
exported, its journal read by `session export --journal` into a bundle that replays whole, and
the same read refused `journal held` while its holder is alive**; a `session start` on a red set exiting 1 while the session it launched
answers reads; a `decompose` setting the split node's own required set and leaving its
parent's unchanged; a second named-pipe server on one name refused `socket held` by
`FILE_FLAG_FIRST_PIPE_INSTANCE`, and on Unix two starters racing one dead socket where only
the `<session>.lock` holder unlinks it; a `--cache` given to a client verb under `--session`
refused at exit 2; a resolver whose pointer holds shell metacharacters executed with no shell
and the characters reaching it whole; an exit-1 resolver fact cached and answered by
`--offline`; a `size` over a multi-repository scope printing `source=-`; an `accept --add` on
a done node refused by rule 5 and accepted after `correct`; a `session replay` with a refused
request printing `REPLAY FAIL` at exit 1; a red O at load
refusing every mutation without `--repair`, and under `--repair` accepting only events that
lower the finding count; a second start on one socket path refused `socket held` while its
journal is free; structure
verbs produce a reproducible `ROADMAP.md` with no hand edit.

**The root's replays carry names, one for every rule the COW refinement adds, so a reader can
say which test holds which sentence** (the list above is prose because it grew that way; these
are named because they were asked for by name):

- **`cow-root-partition`** — one id is in C or in O and never in both; `open=` plus `closed=`
  equals the scope's counted total on every ask; an event hand-written to put one id in both is
  a rule 18 finding at load and refused at the candidate gate.
- **`settle-keeps-id-and-evidence`** — a `state --to done` settles its task: the `:id`, the
  `:children`, the `:acceptance`, every evidence event and its five fields read the same before
  and after, from the closed index, and `verify` over that item prints the same `VERIFY ROW`
  verdicts it printed while the item was open.
- **`settle-moves-no-required-set`** — a task finishing leaves its parent's required set, its
  `rows=`, its `baseline-rows=` and its percentage exactly where they were, with rule 11 green
  on the walk after it; the same run with a `:cancel` moves the set by the delta table's row and
  by that row alone.
- **`settle-releases-the-lease`** — an item settled while a lease is live reads `holder=unowned`
  at once, its `:release` in the lease log with the settling author and the holder it ended,
  `handoffs --since` printing the whole transition log, and no item of C in any `who` answer.
- **`containers-settle-with-their-members`** — a feature of three tasks: settling the last task
  settles the feature and its parent work set in the same envelope and no other container, with
  one `OK` line and one request id, and a crash between the two replaying all-or-none; reopening
  that task returns all three to O together; an empty container in the path settling never.
- **`reopen-revives`** — a `:reopen` of a done item returns it to O at `:todo` with its id, its
  evidence and its generation intact, `open=` up by one and `closed=` down by one; a `:reopen`
  of a deferred item writes no `:revive`, because it never left O.
- **`revive-appends-and-counts-latest`** — the revived item's closed record and every event
  under it still in C after the `:revive`, the `:revive`'s own row carrying `revived=<rev>` and
  its newest row `settles=1`; the item settled a second time, a third row appended and the earlier
  two unchanged, `settles=2` on the newest, and the cursor naming that newest event revision; an ask whose window ends between the settle and the revive counting
  that id in `closed=` and one whose window ends after it counting the same id in `open=`, once
  either way, with no rule 18 finding on any of them.
- **`index-replayed-after-crash`** — a session killed between a `:settle` and the next clip:
  the restart's one journal replay puts the item in C's index and out of O's tree before the
  first ask, `open=` and `closed=` sum to the same total as before the crash, and the same run
  with a `:revive` after the settle recovers the item in O and its record in C.
- **`remove-settles-only-open-items`** — a feature of two leaves, one `done` and in C and one
  open: `node remove` on the feature settles the open leaf and the feature with `:reason
  removed`, leaves the closed leaf's `done` disposition, evidence and settle stamp untouched,
  names it in the feature's `:already-closed`, and moves `closed=` by two and not by three, with
  no rule 18 finding; a second `node remove` of the now-settled feature writes nothing, exits 0
  and prints one `NODE NOTE already-closed`; and a removal over a subtree holding a live lease is
  refused, naming the holder.
- **`working-is-a-view`** — `|W| ≤ |O|` over a set where every item is leased, then released;
  a `:doing` item with no live lease is `disposition=pending`; no verb writes W.
- **`settle-outside-the-digest`** — one `state --to done` digested by two independent
  serializers to one value, with two `:stamp`s and two settle event ids, and the retry answered
  by the original `OK` line.
- **`closed-paged-without-full-load`** — a closed listing over a thousand settled items with
  `--max 20` reads twenty rows, prints `MORE` naming `--after`, and the next page reads the next
  twenty; `parses=0 replays=0` on every page and the whole history never loaded.
- **`closed-row-with-archive-absent`** — the worked acceptance's ask answering the same four
  rows with the retention archive file absent, and an ask that reaches for an archived body
  printing `gap=<n>` and one `QUERY NOTE coverage-gap` rather than a shorter list.
- **`merged-is-not-distributed`** — a fix in C with `landed=<sha>` and `released=-` while its
  release task is open, and `released=<version>` on the same row once that task settles.
- **`branch-and-window-required`** — a `query --ask` with no `--branch` refused at exit 2 naming
  the flag; `--branch closed` with no `--from`/`--to` refused the same way; `--from` under
  `--branch open` refused; `who`, `stale` and `handoffs` refused under `--branch closed` and
  `--branch root`.
- **`findings-across-c-and-o`** — the worked acceptance above, asserted line by line: four ids,
  four rows, `open=1 closed=3 closed-in=3`, no id twice, the dispositions distinct, and the
  release task listed as `pending` by the second ask.
- **`clip-names-the-index-that-overflowed`** — a clip refused with `snapshot=`, `retained=`,
  `index=` and `closed-index=` all printed and `lower --retain or raise --max-bytes` named as the
  one remedy, since no part of the snapshot grows with history any more, with the lower `--retain`
  then passing; and an index page that would pass `--page-bytes` or `--page-records` split into a
  further page by the clip that writes it, with no clip refused for an index at all.

**Draft 25's replays, for the bound Glenn set on the closed history and for Stella's W1 and W2**:

- **`default-window-opens-two-days`** — a default closed-history listing at early morning, at
  midday and at exactly `00:00:00Z` opening at most two UTC day partitions and, at midnight, one;
  no partition older than the window opened for it; the same listing with `--from` reaching back
  a month opening exactly the days in that range that hold closure records and printing its
  `pages=` accordingly.
- **`busy-day-many-segments`** — one day holding many bounded segments read in bounded pages,
  `--max` capping the rows, `MORE` naming `--after`, and the day never read whole.
- **`history-grows-startup-does-not`** — O, the recent-window volume and the page bounds held
  fixed while the old history grows by orders of magnitude: startup resident bytes, index pages
  read, segment bytes read, parses, replays and emitted bytes unchanged, and no whole-C load, no
  directory scan and no whole-history dedup load anywhere on the ordinary path.
- **`one-revision-publishes-together`** — a clip staging its segments, verifying the hashes its
  manifests name, then committing the snapshot, both index roots and every file they reference in
  one commit; a kill before the commit leaving the previous root whole and readable; a kill after
  the commit and before the push leaving the work locally durable and unshared, reconciled against
  the exact remote commit and never by advancing a shared receipt; and one revision restoring the
  journal, the clip and both indexes together.
- **`absent-day-is-not-a-gap`** — a day with no manifest inside a complete manifested range
  answering its rows with `gap=0` and no note.
- **`missing-segment-is-a-gap`** — a segment the committed root names, removed: the listing
  answering what it can with `gap=<n>` and one `QUERY NOTE coverage-gap`, and never an empty
  closed set.
- **`as-of-refuses-unavailable-partition`** — a state-as-of ask whose day partition cannot be
  opened refused at exit 1 naming that one partition, never answered from a later row.
- **`as-of-reconstructs-settle-revive-settle`** — one id settled on day A, revived on day B and
  settled again on day C: an ask whose window ends inside each of the three intervals answering
  that interval's state, the three answers different, the earlier two unchanged by the later
  events, and each read one bounded lookup over that id's chain.
- **`cursor-pinned-across-a-new-settle`** — a paged closed listing with another item settling
  between two pages: no row missing, no row twice, the cursor's pinned revision honoured, and a
  continuation whose pinned revision can no longer be served refused `page expired`.
- **`activity-and-state-are-two-counts`** — an item settled and reopened inside one window
  printing `settles-in=1 revives-in=1 items-in=1 closed-in=0` with `open=` counting it once, and
  the same window's `closed=` unchanged by the reopen having happened.
- **`dedup-page-unavailable-refuses`** — a retry whose dedup page cannot be read refused
  `dedup unavailable`, applying nothing, and admitted once the page is readable again; a retry
  across a day rollover, a clip and a successor handoff refused `already applied`; a reused id
  with a different payload still refused `reused with a different payload`.
- **`rule-2-unavailable-is-not-green`** — a reference into C whose partition cannot be read
  reported `rule 2: unavailable partition=<yyyy-mm-dd>` at exit 1, distinct from `dangling`, and
  the validation never printing green over history it could not read.

**Draft 26's replays, for Stella's `docs/SPEC-WORK-PILOT.md` and `docs/SPEC-WORK-VALIDATION.md`
at `81c2885`** (the suite names of *Preservation and recovery acceptance* below are the other half
of this list and are not repeated here):

- **`wire-integers-are-strings`** — an id, a revision, a counter and a token total each above 2^53
  crossing the wire and returning unchanged; a frame carrying a JSON number refused; `null` and an
  absent key reading alike, and an empty string and an empty array reading as values.
- **`protocol-version-negotiated-or-refused`** — a client offering an unsupported version refused
  with the supported list named and the connection closed, no request admitted before the
  handshake, and an oversized frame refused with one framed error before the close.
- **`disconnect-is-not-a-rollback`** — a client killed after its mutation was journaled: the event
  stands, the same request id and body returns the recorded disposition, and the same id with
  different arguments is refused; `rev=` and `pushed=` distinct in every response.
- **`operation-survives-the-client`** — a long import returning an operation id, the CLI exiting,
  the work continuing, the result retrievable by id afterwards, and `operation wait` timing out
  while leaving the operation running.
- **`status-answers-while-io-runs`** — status and cancel answered within their bound while a busy
  capture, export and clip are in flight, with queues, staged bytes and retained results bounded,
  and a restart reconciling the operation ids that were pending.
- **`cancel-is-a-request-not-an-erasure`** — a cancellation acknowledged with its own final
  disposition, erasing no accepted mutation, and reporting an uncertain external effect as
  uncertain rather than as cancelled.
- **`undo-appends-and-preserves`** — an undo of a named request appending a typed compensating
  envelope with its lineage while the original event and every receipt stay exactly where they are.
- **`redo-refuses-a-stale-plan`** — a redo whose preconditions moved refused atomically, naming
  what changed, writing nothing, and never reached by deleting the undo.
- **`undo-refuses-an-external-effect`** — an undo over a sent message, a paid execution, a
  publication and a source deletion refused and reported as an external effect with its own
  compensating workflow; shared Git history never reset as the undo path.
- **`every-field-has-an-owning-verb`** — every canonical field mapped to its owning typed mutation
  or marked derived or immutable, with no generic set-field escape hatch and no parallel alias.
- **`open-count-is-read-not-computed`** — mutate, then ask `|O|` repeatedly: zero visits, zero
  parses, zero replays, and the counter equal to an independent full count after a close, a reopen
  and an import replay; the open-issue and open-leaf counters separate and neither labelled `|O|`.
- **`no-friend-name-in-the-tool`** — the whole normative text and the whole binary carrying no
  friend, bench, repository or house name; every identity arriving as configuration.
- **`roles-are-configured-not-inferred`** and **`reserved-role-is-not-spent-on-routine-work`** — a
  role read from CONFIG and never from the underlying model; a model capability never cancelling an
  agreed limit; an essential-security-only reserved role, a specialised different-perspective
  reserved-plan role and an agreed participation each expressible, and the reserved ones not spent
  on routine work.
- **`dispatch-ack-and-ownership-are-three`** — dispatch, delivery, acknowledgement and accepted
  ownership distinguished; a pending offer reserving only declared capacity; **a timeout alone
  launching no duplicate** while the old worker may run; a return reconciling before new dispatch.
- **`requested-model-is-not-observed-model`** and **`a-retry-does-not-overwrite-its-attempt`** —
  unknown staying unknown, concurrent attempts keeping separate model and usage attribution, and a
  friend's usual model never standing as proof of a delegated task's executor.
- **`silence-is-a-ping-not-a-verdict`**, **`explicit-rest-is-not-pinged`** and
  **`return-reconciles-before-dispatch`** — the configured silence threshold triggering one bounded
  ping, a nonresponsive capacity marked unavailable with reason `unconfirmed` and no claim of sleep
  or exhausted credit, a failed probe read as unresolved delivery, and explicit rest respected.
- **`unchanged-config-is-one-bounded-answer`**, **`an-invalid-delta-leaves-the-old-config`** and
  **`a-partial-manifest-is-refused`** — the exchange bounded, validated and atomic, with no roster
  and no prose repeated per poll and no secret in a manifest.
- **`pricing-is-pinned-by-revision`**, **`unknown-price-is-not-zero`**,
  **`subscription-is-not-free-reference-cost`** and **`local-tokens-cost-zero-api`** — an old
  estimate reproducible after a rate change, a missing dimension unknown, and the three cost values
  kept separately labelled.
- **`roadmap-outlives-its-work`** and **`roadmap-opened-after-the-window`** — an epic completed,
  clipped, restarted and advanced past 24 hours, then its roadmap listed and opened, rendering
  identical historical rows and retrieving the exact code and test receipts **without loading all
  of C**; completed rows still in the table and *remaining only* an explicit filter.
- **`historic-tick-survives-a-source-change`**, **`regression-opens-repair-work`** and
  **`unrelated-receipts-stay-reusable`** — a changed source or criterion preserving the historic
  tick at its pinned revision while the current view requires re-verification, a confirmed
  regression creating linked open repair work, and unrelated receipts untouched.
- **`chat-and-file-render-are-byte-identical`** and **`render-refuses-a-target-outside-its-roots`**
  — one projection and revision rendering the same bytes to chat and to a marker region, every
  other byte preserved, a missing, duplicate or reversed marker pair refused, and a target outside
  the configured permitted roots refused rather than guessed.
- **`restore-is-isolated-and-dispatches-nothing`**, **`local-checkpoint-is-not-a-shared-backup`**
  and **`compaction-keeps-the-last-copy`** — a restore taking no ownership, reanimating no
  assignment and replaying no message; checkpoint age, local and shared revisions, unshared work
  and failed backups all readable; and compaction never removing the only recoverable copy.

The stall replays of 5649089106
belong to stall detection, deferred below, and are listed there so they are not lost.

## Preservation and recovery acceptance *(Stella, `docs/SPEC-WORK-VALIDATION.md` at `81c2885`)*

**These are release gates, and what they demonstrate is specific protection against specific
failures — never that no defect remains.** **Nothing in this section is claimed implemented**: it
is the list of tests a release must have passed, and a list of intended tests is not evidence that
anything passed them.

**The oracle is independent and the evidence is retained.** Comparison uses **immutable source
captures and an independently implemented semantic comparator**, never the production serializer
checking itself. Raw provider records are preserved with a manifest of source ids, revisions,
counts and content hashes beside the normalised work; **originals are stored byte for byte where
the API supplies bytes**, and an API-normalised value keeps its declared semantics and its
provenance. **Every test retains** its input fixtures, its deterministic seed, the engine, client
and schema versions, its fault point, its invocation, its captured revision, its expected-against-
actual reconciliation and its result. **A fixture validation and a real read-only pilot are
distinguished and never traded for one another.** **An unknown, inaccessible, truncated or
unsupported source field is visible** — there is no silent success by dropping one — and where a
provider cannot expose deleted or private records, **completeness is claimed only for the declared
observable inventory and capture scope**.

| suite | required cases and pass condition |
| --- | --- |
| `source-inventory` | open and closed issues, comments, identities, labels, relationships, attachments and pagination; every captured source record maps to a preserved original plus a normalised mapping, or to an explicit unresolved entry; the same counts with different ids or content must fail reconciliation |
| `read-only-intake` | a dry-run capture or plan and a normal initial import **cannot call a source mutation endpoint**: a recording adapter fails on POST, PATCH, DELETE or equivalent, and the remote inventory is compared before and after; applying a plan changes only the destination, after revalidation |
| `import-replay` | repeated batches, interleaved retries, overlapping pages, reordered records, interruption and resume; a stable source id yields exactly one mapping, with no duplicate canonical work and no lost comment |
| `moving-source` | a body edited, a visible comment added and deleted, labels and state changed and an issue reopened **during** capture; captured versions preserved, a mixed or incomplete capture marked as such, newer observations reconciled, and no claim of a consistent provider snapshot where none was available |
| `archive-completeness` | a missing attachment, an unavailable comment, unsupported fields, size truncation, a rate limit and a mid-page failure each remain explicit gaps **and prohibit absorption**; mixed, external and unknown authors retain their source issues |
| `full-round-trip` | export a captured revision, load it in a **fresh isolated engine**, export again, and compare every semantic field, stable ids, Unicode and literal text, order where it means something, links, evidence, roles, CONFIG, ACTIVE observations, model and rate records, O and C history, roadmaps and accounting provenance; derived caches rebuild to equivalent values |
| `format-determinism` | one state and schema produce identical canonical bytes; null, absent and empty stay distinct; large integers, timestamps, escaping, multiline text and Unicode normalisation differences survive; an arbitrary provider's JSON key order is **not** required to be meaningful |
| `old-history` | an export includes the whole explicitly selected archive, records outside the resident 24-hour window included, with its scope and omissions declared; an old completed roadmap restores and yields its exact proof **without loading all of C**; **a recent-only export is never labelled a full backup** |
| `referential-integrity` | duplicate ids, dangling references, cycles, conflicting parents, invalid cells, duplicate ownership and mismatched manifests all fail **before** publication; a scoped export carries its dependency closure or names its unresolved external references, never a falsely complete backup |
| `atomic-mutation` | failure injected before, during and after the journal append, the durable sync, the apply, the checkpoint write, the rename and the reply; **every acknowledged mutation survives a process restart** under the declared storage assumptions; a torn unaccepted tail is diagnosed; no partial envelope and no count-versus-evidence split is admitted |
| `retry-protocol` | a lost reply, fragmented frames, a disconnect, a repeated id with an identical body, the same id with a different body, invalid UTF-8, types and versions, oversized frames and deadlines; **no duplicate accepted mutation and no executable payload**, and the outcome retrievable after the uncertainty |
| `async-operations` | status, wait and cancel under a busy import, export and clip; bounded queues and output; a restart with operations pending; a stale staged result and an uncertain external effect; **no double launch, no false cancellation success and no control plane stalled behind network I/O** |
| `single-writer` | two local processes, alias paths, a stale socket, partitioned benches, lease expiry, a delayed old owner and a handoff crash; the fencing rules prevent **stale mutation authority** and not only a stale Git push; exported unshared journal work is preserved |
| `indexes-and-counters` | random legal verb sequences compared after each step against an independent full reconstruction; the open-item counters and the friend indexes agree; closure, reopen, reparent and shared references never double-count; the required constant-time queries and the bounded historical paging are instrumented |
| `roadmap-proof` | full fixed-table prototype parity, optional axes, partial and stale evidence, shared prerequisites, newly discovered scope and closed members; chat and file renders identical; a marker edit preserving every unrelated byte and refusing ambiguity |
| `undo-redo` | reversible edits reversed, history preserved, redo only against valid preconditions; dependent later edits, changed criteria, close and reopen, decomposition, accounting receipts and uncertain external actions exercised; **a conflict is explicit and mutates nothing** |
| `recovery` | restore the newest valid checkpoint plus journal; reject a corrupt checkpoint; recover from a prior checkpoint **without silent loss**; compare an isolated old restore against current state; a missing tail or an unavailable remote backup **reported as a recovery gap** |
| `schema-evolution` | supported old schemas migrate losslessly against golden fixtures and semantic comparison; an unsupported version refuses while preserving the originals; **a migration never rewrites the only source copy** |
| `hostile-data` | reader evaluation disabled; pre-parse depth, byte and node limits enforced; path traversal and escaping archive paths rejected; imported prose cannot execute a command or alter authority; deep and high-fan-out inputs handled without quadratic copying |

**Checkpoints are local and shared and the two are never reported as one.** Every accepted mutation
is durably journaled before its success acknowledgement. **Validated atomic local snapshots** are
created periodically, by a configured elapsed time **and** a configured accepted-event count, each
naming its schema, revision, journal boundary and content manifest; the **periodic clip supplies
the separately observable shared checkpoint**, and **a local success is never reported as a shared
backup**. `checkpoint list`, `create` and `verify` expose checkpoint age, the local and shared
revisions, the unshared work and the failed backup attempts, and **the last known-good checkpoint
is kept while its replacement is written**. **Retention and journal compaction may never remove the
only recoverable copy** of accepted work or historical evidence; **pruning checkpoints is a
different thing from retaining C**; a remote outage that leaves new work only on this bench is
**stated as that exposure**; and **a local journal alone does not protect against losing the
machine** — an independent verified copy does. **A restore opens a read-only, isolated,
non-dispatching recovery session**: it inherits no coordinator ownership, reanimates no
assignment, replays no bus message and duplicates no external side effect, and a selected repair is
promoted only through a fenced validated reconciliation with the current state (replays
`restore-is-isolated-and-dispatches-nothing`, `local-checkpoint-is-not-a-shared-backup`,
`compaction-keeps-the-last-copy`).

**Seven obligations come from work already done and are fixtures or real-work replays, not an
invitation to grow a second scheduler** (Stella, `docs/SPEC-WORK-PILOT.md`; each needs an owner in
the implementation plan, and each already-captured requirement is linked and tested rather than
rewritten as a competing tool):

- **`inventory-expansion-and-contraction`** — the initial inventory preserved and discovered,
  completed, reopened, decomposed and explicitly removed work counted **separately**; expansion and
  contraction shown over a named interval; sustained divergence warned under a stated policy; **a
  changed denominator visible beside progress and never silently revised**.
- **`ready-names-the-blocker-and-the-resolver`** — the ready-to-assign view derived from
  dependencies, agreed scope, acceptance readiness, ownership, availability and resource limits,
  with **the exact reason work cannot proceed and who can resolve it** on every row that cannot.
- **`shared-prerequisite-owned-once`** — a shared prerequisite owned once and referenced by every
  affected cell, with integration and release gates beside feature completion: **a merged fix,
  verified behaviour and a published distribution are three evidence obligations**.
- **`review-cycles-stay-visible`** — each required friend's exact-revision review and finding ids,
  the author's dispositions and the clearance recorded; valid evidence reused and only the affected
  delta reread; **repeated review and repair cycles visible as work and as operational cost**.
- **`a-broken-assertion-must-fail`** — a test that still passes with its asserted behaviour
  deliberately broken is not regression evidence; the specific criterion and revision coverage and
  the remaining uncertainty are preserved, not a green badge.
- **`a-stop-reaches-distributed-work`** — a priority change, correction, pause or stop across
  already-distributed tasks, with durable request identity, delivery and acknowledgement and
  reconciled execution handles; blocked questions and bounded fallback plans persisted **so a
  missing answer at night does not stall every independent task**.
- **`cost-joins-include-the-coordinator`** — comparable-work experiment records and complete
  operational cost joins, coordinator overhead and rework included, with elapsed time attributed to
  execution, queueing, review and CI waiting where it is observable; the token-saving hypothesis run
  against real work after adoption, implementation cost sunk.

**Verification is staged, and the fast lane stays fast.** (1) unit, generated and property, golden
and independent-comparator suites, **with mutation tests proving the important assertions fail when
preservation is broken** — a test that still passes with its asserted behaviour deliberately broken
is not regression evidence. (2) process-level fault injection and restart-and-replay against
temporary Git remotes and fake providers, two-process fencing and interrupted I/O included. (3) an
authorised real repository for **read-only capture and dry-run plans only**, reconciled against the
captured records. (4) an import into a **disposable destination with the originals untouched**,
exported, loaded in a fresh engine, independently compared, repeated and resumed, with every gap
inspected. (5) dogfooding the reversible coordinator workflows with periodic snapshots, tested undo
and redo and **an actual isolated restore** — and only then ordinary live work. **Destructive
absorption is a separately gated feature and is never a pilot step.**

**The lanes are named because a slow gate is a gate nobody runs.** A per-change check **targets one
minute and must finish inside two**; the exhaustive fault, scale and generated matrices run in an
explicit pre-release, manual or nightly lane and **not on every change**. **The full preservation
and recovery gates still pass at the release revision**, and changing the code a receipt covers
invalidates that receipt. **Before the lock gate each suite is mapped to its named scenarios,
assertions, owner, command and CI lane**; before a release the exact-revision results are attached.

## What draft 26 changed in the older text *(Rowan)*

**Where a companion and this document disagreed, the older sentence is deleted and not kept beside
the newer one**, which is what *one unambiguous revision* means. The six places, so a reader of
draft 25 can find every one of them:

1. **The engine's language.** *The session's own language is the pilot's decision* is gone: it is
   **Common Lisp**, with the Go client thin over the socket, and the complexity guarantee named as
   the indexes and the bounded access rather than the language.
2. **The first axis.** *A row is a `:feature`* is gone: a row is a node of the roadmap's declared
   **`:row-kind`** with its **`:aggregation`** policy, `:feature` by default. Rule 2, the
   required-set definition and `rows=` all read the declared kind now.
3. **The axis and cell layer.** It is **optional, and optional whole**: a roadmap may declare no
   axes, in which case it has rows and no coordinates, and **no synthetic cell stands between a
   feature and its subtasks**.
4. **`|O|`.** A count reached by a cached bottom-up rollup is gone for this one number: the root
   and container **open-item counters are maintained by the accepted mutation envelope** and a
   resident `|O|` is a **read** with zero visits, parses and replays.
5. **A roadmap's fate when its work ends.** A roadmap still settles with its members, but its
   **view record no longer passes into the retention archive**: it stays in the live snapshot, so
   opening a named roadmap after everything under it closed is a bounded read and never a load of
   C **(Rowan's decision, for review)**.
6. **The word *active*.** It had one meaning and now has a second that must not blur into it:
   lowercase *active* stays W's predicate, **O is never called active**, and **ACTIVE** in capitals
   is the per-friend live-data node and nothing else.

**And one thing that reads like a contradiction and is not**: the engine section's revision-bound
**dry-run plan** — for an undo, a redo or an import — is not the `plan`/`apply`/`reconcile` triple
of 5653982211, which stays deferred below. A plan here shows what one named request would move and
accepts no mutation; the deferred triple is a different design with its own section to come.

## What this draft does not do

Stall detection and bounded recovery with its six replays (5649089106), `plan`/`apply`/
`reconcile` (5653982211), the cross-bench ownership backend and fencing generations (the rule is Glenn's; the git-lease backend above is this draft's proposal for the pilot and
Stella's section keeps the general requirement), the `link`/`absorb` intake modes and the
staged migration as code (their contracts are Stella's sections), the GitHub issue intake and correspondence adapter as code (its contract is Stella's
section below; the adapter is its own spec), token and cost joins beyond the attempt's
`:usage` pointer (#175, #181), and the categories taxonomy (5654164074) are later revisions,
each with its issue. Nothing here deletes, migrates or publishes an issue. Known work is not
authorized, working, scheduled or public by being in O.

**One design decision is deliberately open and this draft does not close it**: whether friends
participate in swarms at all, or whether pools hold model-only workers. It is asked and recorded,
not settled — **a friend's own disposition is the only record of that friend's consent, and a
worker's reply or a silence is not it** — and until it is resolved, identity provenance stands as
it is and no historical actor is renamed (Stella, `docs/SPEC-WORK-PILOT.md` at `81c2885`; *Friends,
CONFIG and ACTIVE* above says the same in its own place).

**Nothing here is implemented, and the lock gate says what would have to be true before it is.**
This document is integrated with both of Stella's companions into one revision, which is her first
condition. The rest of her gate stands unmet and is named so it cannot be skipped: **each requested
friend's explicit disposition at this exact revision**, with unresolved, unavailable and reserved
reviewers recorded separately and **no reply never counted as approval**; a resolved participation
policy; the complete verb and protocol schemas, including every **(Rowan's decision, for review)**
above; the migration and round-trip acceptance coverage of *Preservation and recovery acceptance*
mapped to named scenarios, owners, commands and CI lanes; and named implementation slices. **Cold
model reads supplement friend discussion and do not replace it.** The agreed revision is then
locked and every later change is explicit, scoped and reviewed — and **recording a requirement or
passing a fixture is not a runtime and is not adoption**.

**The four questions between Stella's sections and the shared ones are closed, by her, at
e79847fb**, and the answers are here rather than only in a review comment because that is where
the open ones lived. (1) Her resident-session load line against the execution model's: **one
initial snapshot load plus a journal-tail replay, zero repeated parsing for an ordinary resident
query**, subject to the bounded historical storage of *Retention* above — which is the condition
she attached and this draft's W1 work is. (2) Her clip section against the two `--expect`
revisions: **the local revision on the coordinator's own resident verbs, the clipped revision
required on every replayed request, with the per-node conflict check and the ordered-bundle
exemption** exactly as *The verbs* states them. (3) Her journal and retry line against
`--attempts`: **a durable request-id retry and a transport retry are different things**, as the
clip paragraph now says — dedup retained across every retry, bounded exponential backoff inside
the timeout and attempt budget, and no blind retry of a semantic conflict. (4) Her clip-cadence
sentence, which names no flags: **`--clip-every` and `--clip-after` are the cadence**, distinct
from the reconfirm's `--every`. **Her scoped clearances at that head are folded as cleared where
each stands and are not reopened by this draft**: the same-envelope settle and revive keeping
stable ids and required membership; `node remove` preserving an already-closed disposition and a
repeated removal being a no-op; and a revive appending and each current id counting once. They are
clearances of the specification's text and not of a runtime, which this draft has none of.

**Automatic cross-bench takeover stays disabled for the first Lisp pilot**, whatever the ownership
paragraphs above propose: the `OWNER`-on-the-branch mechanism is this draft's proposal and Stella's
*One coordinator, one live reader/writer* section governs the requirement, and her sentence that
*the ownership and recovery mechanism must be decided and tested before automatic takeover is
enabled* is the gate — the partition, clock-skew and fencing replays pass first, and no second
coordinator and no parallel writer is authorized by any review so far. **Reconciling her
cross-bench-backend-is-undecided paragraph with that proposal in one voice is still open and is
hers**, and it is named here so it is not mistaken for settled.

## Additions of the authors', not in the source

So a reader never mistakes them for Glenn's requirements. Rowan's: the lease model whole
(5654176537, its three changes from that comment: expiry derived and never stored, an expired
lease a count and never a finding, `:extend-once` defined) and its tool-owned random ids; the
`:superseded` state, the `:supersede` event and the `:cancel-requested` state; the
one-validation-of-the-candidate gate; rule 15 and the `note:`-never-for-done rule; counting
an unverified or stale done as unknown; the six indexes, the sixth of them the reverse roadmap reference, and the in-memory aggregate cache;
`--cache <path>` and its key of pointer, subject and resolver identity, the identity spelled as
the `--resolver` command string; the retention boundary's dedup index of request ids, the dedup
predicate of that index together with the retained events, and the request payload digest with
its canonical serialization, the `:structure` event kind and the per-verb and per-kind field
orders that serialization rests on; the clip's boundary record in the journal, the read that
starts at it and the rotation it allows; the offline `session export --journal`; `node require`
and the `:require` scope event; `applicable=` on `percent` and the gross definition of `since-baseline=`; `--foreground`;
the spelling of the root's COW refinement, whose shape is Glenn's (stella-461d99ec092d and his
live word on the words and letters) and whose mechanism is Rowan's — the `:settle` and `:revive`
scope kinds and the envelopes that carry them, their place outside the payload digest, the
closed index and the fields of its row, rule 18, `--branch` with its three values and its
refusals, `--from`/`--to` over settle stamps, `--after` and its cursor, the disposition row with
`landed=` and `released=`, `:version` on a `:task`, `open=`, `closed=`, `closed-in=` and `gap=`,
and `closed-index=` on `CLIP FAIL`; **the spelling of draft 25's bound, whose requirement is
Glenn's and whose mechanism is Stella's** — `--index-cache`, `--page-bytes`, `--page-records` and
`--closed-window` with its 48-hour refusal, `pages=` on an answer, `settles-in=`, `revives-in=`
and `items-in=` beside `closed-in=`, the `<event-rev>:<id>` cursor and its pinned revision, the
`page expired`, `historical window unavailable` and `dedup unavailable` refusals, and rule 2's
`unavailable` reason told apart from `dangling`; **and draft 26's decisions, each marked
*(Rowan's decision, for review)* where it is made and gathered here so a reviewer can find them in
one place**: the wire schema whole — the 4-byte big-endian length prefix, `--max-frame-bytes`, the
`hello` handshake and its version list, **every protocol integer a decimal string and no JSON
number anywhere**, RFC 3339 UTC stamps, absent and `null` alike against empty-as-a-value, the
request and response object shapes and `lines` carrying the output grammar verbatim; the socket's
`0700` directory and `0600` mode; the `operation status|list|wait|cancel` spelling with its event
cursor; the `undo-plan`/`undo`/`redo-plan`/`redo` spelling and `--request-of`; the
`checkpoint list|create|verify|restore|compare` spelling; the `friend`, `config`, `model` and
`observe` verb spellings and their flags; the `roadmap`, `friends`, `models` and `ready` asks; the
`OPERATION`, `CHECKPOINT`, `UNDO`, `REDO`, `CONFIG` line shapes; `--silence-ping` as a configured
duration rather than a number this tool believes in; `:row-kind` and `:aggregation` as the
declared spelling of Stella's row-kind amendment; and **the retention of a roadmap's view record
in the live snapshot** rather than exempting `:roadmap` from the settle cascade;
`cell --in-scope`; the exact list of refused reader syntax beyond `#.` (every dispatch macro,
`#'`, quote, backquote, package-prefixed symbols, ratios, floats, characters); `;` comments
discarded by the reader; the three bound flags and their no-default rule; unknown keys
preserved; deriving state, generation and scope revision from events; `:required` defaulting
to true; the `:review` state; `:responsible` and its inheritance (Stella's point 4); the
`file:` and `test:` pointer schemes and the generic `note:` scheme; `:criterion` binding on
evidence and `verify` as a separate pass with a cache (Stella's points 1 and 2); `:clock
:tool` with `--now` optional (Stella's point 3); the two-marker region in `ROADMAP.md`; the
`:repo` field on a top-level work set; the transition table's exact edges; `<repo>/shared`;
`--at <revision>`; `emitted=<bytes>` on every `OK` line; the structure verbs' names and
flags; the per-scheme resolver contract and `--resolver`; the retention boundary with its
derived state and its retention archive (`--retain`); the `accept`, `source` and `node remove` verbs and the
per-kind scope delta table with the set each kind moves; the `:axis` scope kind; the repair
mode (`--repair`); the endpoint's own lock, the first-pipe-instance spelling and the
`<session>.lock` path rule; the retention boundary's closed enumeration; the resolver's no-shell
execution, its two arguments and its `<fact>` token; the `@<sha>` on the `run:` scheme; the lease and transition row shapes of `query`. Stella's: the local recovery journal, event ids and expected revisions, the named event
boundary per clip, the offline-clip rule, the fencing-generation ownership record as a proposal (the `OWNER`-on-the-branch form with
CAS push, the lease `until`, the self-fence at `until`, `--skew`, the journal lock keyed by
the journal's canonical path and bench identity, per-request admission, the base predicate
before every CAS push, the two clip triggers, the handoff verb, the two `--expect` revisions,
and the export/replay path are Rowan's), fold/unfold/propagate as
operators, the measurement list, the `link`/`absorb` archive order, the migration dispositions;
**the whole of draft 25's closed-history contract from her amendment `docs/SPEC-WORK-CLOSED.md`
at `4978e8e`** — C partitioned by the event's own UTC day into bounded immutable segments under
dated manifests, the versioned index roots with their page bounds, the bounded resident index
cache, the rolling default window that opens two day partitions at most, historical access only by
an explicit query or a required indexed lookup, the append-only per-transition closed rows and the
bounded as-of lookup over them, closure activity counted beside item state, pagination pinned to a
captured revision and filter, an absent day told apart from a missing segment, the dedup page that
refuses rather than admits, one revision publishing snapshot, segments, indexes and manifests
together, and the real-work acceptance with its honesty rules; **and the whole of draft 26's two
companions, `docs/SPEC-WORK-PILOT.md` and `docs/SPEC-WORK-VALIDATION.md` at `81c2885`** — the
Common Lisp resident engine with a thin Go client over a local socket, the bounded typed JSON wire
and its properties, durable request ids and expected revisions under one writer, durable operation
ids for long work with status, wait and cancel, the complete verb-family coverage requirement,
guarded undo and redo by compensating events, the friend and assignment indexes, CONFIG against
ACTIVE with the coordinator inside `friends`, role configuration expressive enough for a reserved
essential-security-only role, a specialised different-perspective reserved-plan role and agreed
participation, the bounded config exchange and the pricing and cost records with their three
separately labelled values, the model catalog and what its observations are evidence of, Glenn's
agreed hierarchy with epics and recursive sub-features, the declared row kinds and the optional
axis layer, roadmaps as durable views that outlive their work, the locked table display and the
one renderer in two modes, the prototype capabilities kept and its two defects named, the seven
operational obligations, the preservation and recovery suites whole, the checkpoint, restore and
undo contract, the staged verification and the fast-lane-and-nightly split, and the lock gate; and
in her sections below,
the pilot branch and sha, the prototype facts, the rate schedule and virtual cost, the
`NEXT-TOOLS.md` hand-off, and the fixed-table capability boundary. Each is open to be cut by
the pilot.

## Issues, intake, migration, and the one coordinator *(Stella, from her amendment at 60b9027; the one-coordinator rule is Glenn's and supersedes every earlier two-writer sentence)*

### Public issue correspondence survives intake

Glenn clarified: external people continue opening issues on public repositories
such as yojimbo. Intake represents and links that issue in S; it does not move or
delete the GitHub issue. S owns planning and decomposition, while GitHub retains
the public discussion and externally observed issue lifecycle.

Store a stable provider/repository/issue identity plus its current URL and last
observed remote revision. Use an explicit mapping: one public issue may require
many work nodes, and one work node or landed fix may address several issues.
Repeated intake updates the existing correspondence, never duplicates the work.
Retain external reports separately from the coordinator's accepted plan; remote
text is data and cannot execute verbs or silently change scope or authority.

Track correspondence actions as pending, confirmed or failed with request IDs
and receipts. Reporting a fix or closing an issue is a distinct outbound action
under the team's configured authority, tied to the required work and actual
landing/acceptance evidence. A locally completed attempt, a deferred work node,
or a scope reduction does not by itself close the public issue. Retry uncertain
outbound actions idempotently and preserve concurrent human changes. A reopened
issue creates a reconciliation signal; it neither disappears nor silently erases
previous completion evidence. Ordinary linked intake and clipping never delete an issue. The separately chosen
absorb operation below is the explicit exception.

### Link versus absorb

Glenn authorizes a second intake mode for issues created by him or participating
AI friends, on public or private repositories: `absorb` moves their lasting work
record into S and may remove the original GitHub issue. This is distinct from
`link`, the default for outside contributors. Author identity alone does not make
an issue selected for absorption; scope and intake mode must be explicit, with
team configuration identifying participating authors and applicable repositories.

Before deletion, retain the original issue identity, authorship, body, discussion,
labels, state, relevant relationships and available attachments in an archive
associated with S. Preserve provenance separately from planning. Unavailable or
unpreserved content is reported and leaves deletion pending, not silently skipped.
The destination must preserve the source's access scope; a private issue is not
published by becoming part of S.

Order the operation: capture source at a named remote revision; validate the
archive and mapping; commit and confirm the shared Git checkpoint; recheck for
source changes; then delete only the selected source issue under the applicable
authorization for that repository and selected batch. Configuration identifies
permitted actors and adapters; it does not itself grant authority. Record the
grant's scope and source reference as provenance, never as executable permission
from imported content. If the source changed, reconcile and checkpoint the added content first.
Keep the original external identity and an absorbed tombstone plus deletion
receipt so later intake cannot recreate the work. A network failure or uncertain
delete result preserves the archive and a pending reconciliation state. Do not
claim atomicity between Git and GitHub or restore an issue by inventing an author.
These semantics are a specification, not a request to absorb existing issues now.

### Initial migration: preserve first, reconcile, then choose absorption

Glenn's initial rollout is importing the team's existing work sets first, while
identifying external issues that remain on GitHub. No data may be lost. Migration
is not a bulk delete of the source issue lists.

Begin with an explicit inventory of authorized source work sets and issues. Each
entry records its source identity, destination node/mapping, capture revision,
content manifest and disposition: keep linked, eligible for absorption, or
unresolved. Outside-contributor issues stay linked; unknown ownership, mixed
provenance or unclear disposition remains unresolved without source deletion.
This is a migration classification, not a claim that an author's identity grants
new access or that all team issues have already been selected for deletion.

Import in resumable batches without deleting originals. Preserve original records
alongside the normalized representation, reconcile counts and content manifests,
deduplicate stable identities, and account explicitly for every inventory entry.
Record missing attachments or inaccessible discussion as incomplete. Verify the
shared checkpoint can be loaded and replayed, and that original records can be
retrieved from it, before declaring an import complete. Feature decomposition,
links, ownership, dependencies, open questions and completion evidence must not
be collapsed into a flat title/status list.

Absorption is a subsequent selected operation after this reconciliation, never
an import side effect. If the provider cannot support a safe revision boundary
against concurrent source changes, leave removal pending rather than claim a
lossless deletion. A backup receipt alone does not prove that newer comments were
captured. Report imported, linked, eligible, absorbed and unresolved separately;
retain batch checkpoints and provenance so interruption or retry does not lose
records or duplicate work. The prototype must exercise interruption and a source
edit during migration before it is trusted with removal.

### One coordinator, one live reader/writer

Glenn's explicit rule, superseding the earlier concurrent-coordinator proposal:
there is exactly one active coordinator and one reader/writer of resident S via
nova-work. Workers and friends submit results, evidence and requested changes to
that coordinator. They do not open another live S or mutate it directly. Other
readers use published, revision-labelled snapshots; these are not active planning
replicas. The owner serializes accepted operations, including incoming messages,
with stable request IDs and local expected revisions.

Failover transfers the role rather than adding another coordinator. A standby
may prepare from a published checkpoint but cannot activate its work set until
ownership is transferred and the former owner is fenced out. Old processes and
old delayed requests must not resume mutation, task dispatch or Git publication
under an obsolete ownership generation. A stale heartbeat or unanswered ping is
an availability signal, not proof of exclusive takeover authority.

Proposed mechanism: an authoritative ownership record with atomic acquisition
and monotonically increasing fencing generations, enforced at the live session
and consequential dispatch/publication boundaries. The exact cross-bench backend
is still a design decision. A local PID/file lock alone only protects one host;
a Git lease file alone does not prevent two offline owners. If exclusive ownership
cannot be established, do not activate a competing coordinator. Define ownership
loss behavior and recovery of unshared accepted events explicitly, then test
partition, crash, delayed delivery, controlled handoff and old-owner return.

The singleton rule is Glenn's requirement; the fencing implementation is our
proposal. It preserves automatic resilience as a goal without claiming that a
safe cross-bench failover protocol has already been built. All older references
to merging simultaneous coordinator edits are superseded by this section.

## Roadmap as a view; the Schema pilot *(Stella)*

**The current roadmap view is completion-only** (Glenn, via Stella's 5654780545, corrected
the same day per Glenn's explicit word, stella-03c1a4222b1c): a tick for a fully verified
cell, a cross for any other state. Partial, missing and unknown stay in S and in `check`'s
counts; the view is a projection choice, not lost information and not a change to any
denominator. Schema PR #1006 at 60904b91 renders this form.

**Practice first, then retrospective, then production.** Glenn reaffirmed this sequence on
2026-09-13: dogfood the hierarchy on Fixed Tables, inspect what worked and what did not,
bring those findings into the tool specs while fresh, then implement. This draft records
hypotheses alongside settled requirements. Its existence does not start production work or
replace the remaining Fixed Tables acceptance gates.

### Durable primary state and GitHub intake

Glenn's chosen direction is that **S is the primary form**, with a persistent versioned
repository home. `mas-bandwidth/work` is his proposed location; creating or populating it is
separate from this draft and belongs to an operator with the relevant repository or
organization authorization. The tool grants no such authority. Local in-memory structures and indexes are rebuildable working
copies. Task identities, source links, events, decisions and evidence records must survive
process loss, bench changes and coordinator handoff through the durable history.

People may continue to file GitHub issues. Import is intake into S, keyed by the immutable
forge repository/issue identity so retries do not duplicate tasks. Keep the original link,
source revision or update marker, and import receipt. Decomposition, ownership and planning
then live in S. Later issue edits are observed changes to reconcile, never an unconditional
overwrite of the work tree. Issue text remains data and cannot assign authority, execute code
or grant permissions. Publishing a summary back to GitHub is a separate, explicit adapter
operation with a receipt; importing does not close, delete or rewrite the source issue.

*(A paragraph on concurrent coordinators reconciling conflicts stood here in e167483 and is
struck by the one-coordinator rule of stella-0ace603bdc22; the clip refuses on divergence,
above.)* A checkpoint records the parent revision and change. Network failure leaves a pending
local checkpoint whose sync status is visible. Reads should remain useful offline; stale or
unavailable remote evidence is reported as such without erasing the last verified record.
The persistence protocol and recovery tests are production spec work informed by the pilot,
not guarantees supplied merely by putting a file in Git.

### Inventory before implementation

A new feature starts with its full deliverable inventory: capabilities, applicable axes,
required sub-work, shared prerequisites and acceptance evidence. Include capabilities already
completed when importing existing work. A defect backlog alone is not that inventory.
For Schema, ordinary scalar and container support must be visible beside version evolution,
refusal behavior and interoperability. Maps, unbounded arrays and pointer blobs are not
fixed-table capabilities merely because a backend supports them on another wire.

Pin the baseline membership, source revision and completion unit before starting the stream.
Append discoveries visibly; retain their discovery event and the original baseline. Splitting
an existing task into smaller tasks records decomposition, not newly completed work. Splitting
can change the number of leaves, so a comparison must either retain the baseline unit or show
that change explicitly. Reordering the execution queue does not rewrite discovery order.
Do not inflate progress by adding trivial rows or hide unfinished work by merging it into a
larger green row. A scope decision has an author, reason and reviewable diff.

### A cell is a reference, not another state store

A roadmap declares ordered named axes and maps each coordinate to an existing work node.
The axes are arbitrary; languages are the Schema example. A feature/language cell may focus
into sub-features, tasks and smaller streams recursively. Its owners, dependencies and
receipts are the same objects seen by repo and category queries.

For each cell, show completed required leaves, total required leaves and unknown leaves.
Green requires the full acceptance contract, including prerequisite gates. Per-language
feature completion counts whole green feature cells divided by required feature rows. It is
not the average of cell percentages. With unresolved evidence, show a verified lower bound
and an explicit unknown count; do not present that bound as estimated implementation progress.
A named unsupported surface is not a passed test. Excluding it from the denominator requires
a recorded scope decision, not a renderer convenience.

Shared compiler, lock, platform and final integration work has one canonical owner. A roadmap
can show it in an adjacent gate view and reference it from affected cells. It does not create
nine copies of the work or its token cost. If the language matrix counts runtime capabilities
only, label that scope and show the shared release gates alongside it; a green runtime matrix
alone must never print that the whole goal is done.

Useful focused views include remaining features in one repo, ideas for that repo, open bugs,
work in a category, one language's unfinished cells, and a cell's nested tasks. A listing is
compact and capped; it carries stable IDs and a way to focus further. Category taxonomy is
TBD. The query machinery filters; the reader should not have to scan S manually.

### Evidence and the imported starting point

The Schema pilot is on `codex/fixed-tables-roadmap-20260913`; the surveyed implementation is
`8ea5ed8e4656875088250f564e88a965a7135e7c` on `fixed-table-form`, not a completed merge to main.
`docs/roadmap.sexp` is its state input and the marked region in `ROADMAP.md` is its projection.
Original audit IDs, historical reports and superseding aliases remain retrievable. They are
provenance, not current completion assertions.

The initial 23 audit-family rows omitted ordinary delivered capabilities. Glenn identified
that gap; the survey appended 34 ordinary capability rows and preserved the versioning
contract's individual rows for nested work. Schema PR #1006 at `c77d81fc` now separates
source support (built, partial, missing) from qualified ordinary acceptance, with source
references, invoked assertions and remaining work per cell. Its original audit-family
reconciliation remains incomplete. This is an incomplete imported baseline being
repaired, not evidence that the implementation just expanded by the same amount. The source
records both the inventory correction and implementation work discovered during execution.

A receipt must identify what was checked, against which revision, with what result and which
acceptance criterion it supports. A resolvable PR, a test name or a green aggregate CI run
alone does not prove a whole feature. Check whether the named assertion ran, whether it can
fail the gate, and whether it covers the claimed behavior. Generation, valid-data round trips,
hostile-input certification and performance are different proofs. Preserve disagreement and
unknowns rather than turning an attractive summary into green cells.

### What the current prototype proves, and does not

Emma's renderer validates a bounded restricted-data graph, rejects duplicate IDs, dangling
references and cycles, checks complete matrix coordinates, and derives progress from required
leaves. It writes only between the roadmap markers. Its replay tests cover Unicode byte
preservation, deterministic repeated rendering, drift detection, empty evidence, partial
rollups and reachable target selection. Stella's follow-up closed a literal-EOF sentinel
collision and whitespace-only evidence acceptance. A missing requested roadmap now refuses
instead of falling back to a different one.

These are prototype facts, not production conformance claims. The pilot still uses shared
containment references and memoized descendant sets, which can cost quadratic space/time;
the production design's counted containment forest and reference graph address that
specific duplication cost. The pilot does not yet implement them, indexed repo/category queries, leases,
network evidence validation or incremental updates. Its AST-depth check occurs after the Lisp
reader, so it is not the production reader's pre-parse depth guarantee. The existing parser
and gate tests passing does not certify those missing properties.

### Retrospective required before production implementation

Use the method to carry the Fixed Tables work forward, including at least an inventory
correction, a completed cell, newly discovered work, a dependency or handoff, and a changed
focus. Record failures and repairs as they occur. At the retrospective, compare the same
questions and acceptance scope against the previous manual workflow, then change the spec.

Measure total tokens by friend/model/bench/repo and attempt through the attempt event's
`:usage` pointer where available, including source survey, coordination, review and
correction. A missing or unresolved usage pointer is unmeasured, never zero.
Keep raw observations so alternative reports remain possible. Price with a versioned rate
schedule, separating billed cost from virtual model-weighted cost. Record useful work per
accepted result, wall time, retries, stale answers and questions that needed human rescue.
Quality and total required tokens come first, average token cost next; reduce wall time when
it does not materially worsen those objectives. A cheaper first draft that costs more to
repair has not demonstrated an efficiency gain.

Each proposed tool capability should cite the observed friction, the smallest operation that
would remove it, its safety boundary, a measurable benefit and an acceptance replay. Keep the
capabilities that earned their place; revise or defer the rest. The post-Fixed-Tables review
feeds `NEXT-TOOLS.md` and the production specs before implementation begins.

## The measurement that decides *(shared)*

Before this leaves draft, on the Fixed Tables roadmap: tokens and wall time to update one
fact in O (an `evidence` event and a `state --to done`) and regenerate the table, against editing
the table by hand; whether "who is on the C leg" is answerable from leases alone without
reading the bus; whether a reader of the generated table found a number the source did not
support. The benefit is observed or the tool is not built (5653982211).

**The closed history has its own workload, and it is real work rather than a fixture** (Stella,
`SPEC-WORK-CLOSED.md` at `4978e8e`): the security closeout report — findings across repositories
joined to their fixes, the releases that carry them, the friends and models that did the work, the
benches and the retained usage pointers — baselined as the **actual manual workflow** and compared
against the same report produced from O and C. **What counts as a win is stated before it is
measured, so it cannot be found afterwards**: correctness replays passing and fewer bytes emitted
are not a token saving; the implementation's own tokens are sunk cost and are outside the
before-and-after; usage nobody recorded stays unknown and is never estimated into the comparison;
and the honest outcomes are a **verified saving**, **inconclusive evidence** or a **failed
hypothesis**, each reportable and none of them a percentage invented to fill the line.
