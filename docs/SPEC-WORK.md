# nova-work — specification (DRAFT 28, 2026-09-14)

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
5655905248; draft 22 folded the two whole reads at efc26a2e, 5655987384 and 5655988102; **draft
23** folded Glenn's root refinement of 23:36Z, stella-461d99ec092d, and his live word on the
words and the letters that followed it; and draft 27 folds the whole read at 08650d11,
5657782701).

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
fixed here and used nowhere else**: the letters are **C**, **O** and **W**; **the set** **O** is
*open* and **the set** is never called *active* — lowercase *active* keeps naming W's predicate,
and Glenn's verbatim *active percentage* below keeps his words and names no branch; and **ACTIVE**, in capitals, is Glenn's per-friend live-data
node of *CONFIG and ACTIVE* below and is the only thing that word names in this document.

**Draft 27 folds the whole read at `08650d11`, PR #231 comment 5657782701, and changes no
requirement of Glenn's or of Stella's.** That read held on one HIGH — the six mutation verbs draft
26 added were listed as mutations and given no `:event` kind, no ordered field list and no
subject, so the durable-request-id promise this document rests on was reopened for exactly those
verbs — and the repair is the five new kinds in *The data*, their subjects, their places in the
payload digest and the `OK` lines that print them. Three decisions the read asked for are made
rather than deferred: **the clip's transport is one long operation** and the engine section, the
output grammar and `session stop` now read alike; **an absent field in the payload digest is
`(:absent)`** where `()` is the empty list, so `format-determinism` can pass on the serializer's
own terms; and **the local durable snapshot is a *savepoint*** where *checkpoint* stays Stella's
word for the clip commit, which is the one rename of older text this draft makes and the only
place a draft-26 reader must relearn a word. The rest is coverage the read named and this draft
owed: which verbs are reversible and which are refused, verb by verb; the missing verbs listed by
name instead of implied; the flags and vocabularies that lived only in prose put into the
grammar; each Rowan decision marked where it is made as the header promises; each acceptance
suite's lane named; and **four sentences of Stella's `docs/SPEC-WORK-PILOT.md` and
`docs/SPEC-WORK-VALIDATION.md` at `81c2885` restored** — they were dropped by draft 26's
integration and none of them contradicted anything, which is why each says where it came from.

**Draft 28 folds two more of Stella's companion commits on `codex/work-closed-day-partitions`:
`4e800fb`, *Specify eager working-set index and query cost checks*, and `bc4a4a4`, *Design
bounded batches for resident work server*.** Both land the way drafts 25 to 27 landed hers —
into this file, marked where they are hers — and **neither reopens a decision draft 27 made**.
`4e800fb` answers a cost question draft 27 left implicit, on Glenn's rule of 2026-09-13 that
**reading W must never walk O**: *W is materialised eagerly, never rebuilt on a read* below gives
the view a resident form maintained inside the same accepted mutation envelope that carries the
item and the lease, beside the `|O|` counter of *Counting*. `bc4a4a4` answers a transport
question the same way: **the three batch modes ride the accepted mutation envelope and the long
operation protocol this document already has, and add no second transaction mechanism** —
*Batches ride the envelope that is already here* below. **Three things the two commits ask for
are not done here, and each is named rather than quietly dropped**: their two new suites become
rows of *Preservation and recovery acceptance* below rather than a file, because draft 26 settled
that **neither companion lands as a file** and this draft keeps that; `bc4a4a4`'s *refuse the
verbs a table marks as carrying an external effect* has no referent, because draft 27 settled
that **the external effects are outcomes and not verbs of this grammar**, so the atomic batch
refuses the long operations by entry id instead, by name; and `bc4a4a4`'s *checkpoint write*
keeps draft 27's word **savepoint**.

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
   and the tip's if known), except that a fenced session admits `operation status|wait|cancel`
   ONLY with an explicit id resolving to its own `session.export` operation; other ids,
   general operation listing and every other `operation` verb keep the fenced refusal; an
   unknown or non-export id refuses (#293),
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
packaging and its supported platforms are pinned and tested before any release — **and for the
first pilot they are pinned here, decided by Glenn, 2026-09-15**: the runtime is SBCL and the
platforms are the E02-F06 matrix below. The version line
is the client's; a session in another language answers `session
status` with its own `SESSION OK … build=<identity>` field, so every running binary says
which build it is. A session's identity, bounds, state, journal path, base revision and clip cadence are
explicit at start and readable at any time (`session status`, whose `SESSION OK` carries
`state=`, `every=`, `skew=`, `max-bytes=`, `max-depth=`, `max-nodes=`, `index-cache=`,
`page-bytes=`, `page-records=` and `closed-window=` beside the clip
cadence, so every bound is read rather than remembered), and it is stopped explicitly;
no always-on daemon is required, and a supervised session that a coordinator starts for a
sitting and stops at its end is enough (Stella, *Keep the work set alive*).

**The platform matrix (E02-F06), pinned** *(decided by Glenn, 2026-09-15)*. The runtime is SBCL;
the first pilot's cells are the two supported ones; Windows is an out-of-scope cell by
a recorded scope event until the measurement is in, and the named-pipe endpoint text of *The
engine and its client* below stays as the Windows spelling for later.

| cell | runtime | first pilot | endpoint |
|---|---|---|---|
| darwin-arm64 | SBCL | supported | Unix-domain socket |
| linux-x64 | SBCL | supported | Unix-domain socket |
| windows | — | out of scope by a recorded scope event, until the measurement is in | named pipe `\\.\pipe\<name>`, the spelling kept for later |

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
field the caller did not give written as **`(:absent)`**, so the body has a constant shape and two builds
cannot disagree about a default. **Absent has a spelling of its own, because `()` is the empty
list and the empty list is a value** *(Rowan's decision, for review)*: a caller who gave no
`:members` and a caller who gave an empty one asked two different things, the wire already keeps
them apart below, and a digest that wrote both `()` would make `format-determinism`'s *null,
absent and empty stay distinct* false on its own serializer. A JSON `null` on the wire means *not
given* and serializes `(:absent)` too, so absent and null digest alike and empty digests as
itself (replay `absent-empty-and-null-are-three-spellings`) — each element printed by the same deterministic printer a clip
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

A valid mutation whose patches change nothing still records a durable event with a real event id,
its request id, its payload digest and the preimage it was applied against, and reports `changed=0`;
a lost reply retries through the ordinary request-id lookup of the journal's two-part test and is
answered with the original receipt; a later retry, past the journal and into the dedup index, is
refused `already applied` by the rule above; nothing is silently dropped and no phantom `id=-`
outcome exists for an accepted mutation; a preview is not a mutation. A no-effect event is a
historical receipt and never a domain change, and a no-op never evades its verb's reversibility or
its stale-and-conflict rules.

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
names and the `operation status|wait|cancel` that rule 2 admits on an id resolving to its own `session.export` operation — keeps
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

**A journal has an identity, every record in it has a place, and a rotation changes neither**
(#295 at `c2be4d6b`, Stella's *Savepoints name a complete journal envelope*, folded on Rowan's read
there; the built shape is Emma's #333 at `79277f05`, and where the two differ this paragraph says
which is the contract). A newly initialized journal writes a header first: `:magic
"nova-work/journal"`, `:version`, a **journal id** — 256 random bits as 64 lowercase hex
characters, an identity and never an ownership token — and `:initial-state`, the root digest of the
state its first record applies against, so a replay over the wrong seed is refused `journal
mismatch` (`SESSION FAIL` or `SAVEPOINT FAIL`, exit 1) and applies nothing. The bench, lock identity and coordinator generation of rule 2 are
bound beside it; **a copied journal grants nothing on another bench** — the fencing rules decide
activation, and the id only says which journal a savepoint or a rotation is talking about. Every
accepted envelope and every clip boundary record is one record with a **sequence**, a positive
consecutive integer within the journal — a decimal string under the reader's bounds
like every protocol integer, never a machine word — and a **record hash**: SHA-256, lowercase hex,
over the canonical serialization (the digest paragraph's printer, so a successor of another build
recomputes the same chain) of the journal id, the sequence, the previous record's hash — `(:absent)`
on the first — and the whole body; the hash lies outside its own preimage, detects a mismatch and
authenticates nothing. **A mutation record holds, in order**: its journal id, sequence and previous
hash; the request id, the canonical requester payload and its digest; the local revision after the
envelope and every assigned event in its semantic order, generated events included; and **the
original reply whole** — exit, the ordered output lines verbatim, the resulting local revision and
the `pushed=` it reported — constructed before the record is synced, because **no acknowledgement
precedes the durable storage of the complete record**, and a record that would pass the reader's
three bounds whole is refused before acceptance, exit 2, `indivisible` naming `--max-bytes`, and
never trimmed to fit (the admission rule of *Retention* below, whose line shape it shares). Recovery replays the retained events and never runs the verb again, which is
how it mints no id, reads no clock and settles nothing twice. **A rotation opens a new physical
segment of the same logical journal**: the segment's header names the journal id, the previous
segment and the copied boundary record, which keeps its sequence, previous hash and hash, its
`:initial-state` the digest that record names, and the next record continues the chain; a new file is not a new journal, and a rotation creates no second
accepted mutation and resets no request identity. **Where #333 differs, this is the contract and
#333 is the slice to change** *(Rowan's decision, for review)*: it frames each record as `(:frame
:seq :len :checksum :record)` with a checksum over the record alone and no journal id in its header
— the journal id and the previous hash join the preimage and the id joins the header; its
`:capacity` refuses a new request outright once its store is full (`journal capacity exceeded`)
where the bound this document names is the clip's rotation, `--clip-after` events and never a
record count, so a full journal is a clip due and not a refused request; its `:line` retains one
reply line where a reply of several lines is retained whole; and its replay begins at the header
where this document's begins at a savepoint's cut or the newest boundary record. **Where it
agrees, it is the built witness of this paragraph**: the record is written and synced before the
state is applied and the reply sent (`durable-journal-append-plus-lost-reply-recovers-once`); a
torn tail, a flipped bit and a wrong header refuse with the file bit for bit as it was
(`durable-journal-corrupt-data-refuses-without-truncation`); a multi-event envelope is one record
under one hash; a replay reconstructs the exact root digest and next revision without a fresh id;
and an append or sync that failed leaves the journal **uncertain**, every mutation refused
`journal uncertain` until the tail has been read and diagnosed
(`durable-journal-uncertain-write-refuses-until-recovery`; its other witnesses are named beside
the replays `crash-after-append-recovers-the-reply-once`, `torn-tail-is-diagnosed-not-truncated`
and `replay-mints-nothing` below).

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
wrong one. **And the local durable snapshot of *Preservation and recovery acceptance* below is a
*savepoint*, never a checkpoint** *(Rowan's decision, for review)*: draft 26 let that word name
the clip commit and the local snapshot both, which is the same fault in the same sentence. A
**savepoint** is this bench's validated atomic snapshot of the resident state with its journal
boundary; a **checkpoint** is the shared clip commit; **a savepoint writes neither the clip's
deterministic snapshot file nor a retention archive**, holds no retention boundary, and is never
reported as a backup — the archive and the boundary are written by the clip, by the paragraph
above, and by it alone. **So the retention boundary and the two published indexes together carry everything the normal
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

**Days are merged by revision and never concatenated, and a historical ask has a page budget that
`--max` is not** (#295 at `c2be4d6b`; Rowan's read there, items 2 and 3). A time query routes only
the days its `[from, to)` intersects and then **merges** their revision-ordered streams — one
bounded leaf cursor per day and a heap over `<event-rev>:<id>` up to a configured fan-in, and past
it deterministic external merge passes over operation-local runs that are never canonical, are
held only for the ask and its continuation, and are discarded at its end, its cancel or the
continuation's expiry (the bounds and the expiry are open below) — because a backdated stamp keeps a row in its
recorded day while the revision remains the order, and two days laid end to end would print a
later revision before an earlier one; **no row is emitted until no unvisited selected stream can
hold an earlier eligible one**. The default window needs no run under any fan-in of two or more,
which the fan-in must be, and only an explicit historical ask pays. **`--max` caps the rows printed and bounds nothing
else**, so a filtered historical ask whose filter rejects every row it reads could scan unbounded
history while claiming a small answer; `query --page-budget <n>` *(Rowan's decision, for review)*
caps the index pages and segments read in one call whatever the filter rejects, and an ask that
meets it before it can print a row answers `QUERY MORE rows=<n> shown=0 pages=<n> after=<cursor>` —
the one cursor rule of the closed-index paragraphs below, `--after <cursor>` with its pinned
revision, and not a second one, the cursor carrying the captured root, the merge-run identity and
each run's position, so the next call continues the same root or is refused `page expired` like
any other continuation. The stamp summaries a manifest carries per day and per segment may let a
merge skip a leaf the window cannot touch, as an optimization, and are never proof of time order;
**`pages=` is printed and never promised** — a cold lookup reads as many pages as the routed path
is deep, the depth is bounded by the key's bit length under the reader's field bound, and no
segment outside the routed path is read, which is the *no whole-C load* of
`history-grows-startup-does-not` stated as a number a test reads (replays
`days-merge-by-revision-never-concatenate`, `page-budget-is-not-max`).

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

**The keys are ordered without a hash, one key set is one tree, and the bounds are checked at
start** (#295 at `c2be4d6b`). A key is a prefix-free encoding of its fields in the order its index
routes by — the closed index by id then revision, by revision then id, and by repository then
revision then id; the day tree by UTC date; the dedup index by request id — in which **opaque text
sorts in raw byte order, a prefix before its extension, and a revision sorts numerically with no
width**, so `2` precedes `10` and no counter limit is fabricated; no composite key is hashed, and a
key on a page is a string beside its bit length and never an evaluated form. Every root is a
compact binary radix tree: an internal page holds its split bit and two child references (path,
hash, record count) and repeats no key range; a leaf holds complete keys with their locators; a
bucket that would pass either bound splits at the first bit after its longest common prefix; and
**one logical key set under one pair of bounds yields one tree, whatever the clip batching or the
insertion order** — which is what lets a busy day's segments come out the same however the day was
clipped, and a backdated closure rewrite its own bucket and its ancestors and nothing beside them.
A page's own references count as its records and bytes, so **`session start` preflights every
fixed-arity wrapper** — the closed root with its four references, a day manifest, a two-child
internal page — against `--page-bytes`, `--page-records` and the reader's three bounds, and refuses
at exit 2 naming the flag a bound no wrapper fits, `--page-records` below `4` among them, beside
`--closed-window`'s `48h` refusal. Different bounds are a different tree: a root names the bounds
it was built under, and changing them is an explicit full reindex under a new root, never a mix
under one header. The byte forms of the encoding are open (*What this draft does not do* below);
the ordering, the prefix-freedom and the one-tree property are the contract.

```lisp
;; EXAMPLE DATA, NOT A LOCKED CODEC: one internal page and one leaf of the closed index by id.
(:version "work-index-v1" :kind :radix-internal :key-kind :closed-by-id :bit 149 :records 2
 :zero (:path "closed/pages/a.sexp" :sha256 "<64-hex>" :records 71)
 :one  (:path "closed/pages/b.sexp" :sha256 "<64-hex>" :records 64))
(:version "work-index-v1" :kind :radix-leaf :key-kind :closed-by-id :records 1
 :entries ((:key "<hex>" :bits 231
            :value (:day "2026-09-14" :segment "closed/segments/x.sexp" :sha256 "<64-hex>"
                    :event (:revision 812 :id "T-7")))))
```

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
the pages, and until it does, a query reads the pages and the overlay as one (replay
`one-revision-publishes-together`, which is this paragraph's own test and is named here as well
as in the list below). So a crash between a
settle and the next clip leaves no id in both branches and none in neither, whether the crash fell
before the acknowledgement, after it and before the clip, or inside the publication (replay
`index-replayed-after-crash`).

**Recovery starts at a savepoint's cut when there is one, and the overlay it builds is bounded**
(#295 at `c2be4d6b`). With a savepoint, the session loads its image and its retained local replies
and replays only the complete records strictly after the cut, once, in sequence, boundary records
included; without one it replays from the journal's newest boundary record as above. What the
replay passes becomes **overlay
pages** — closed rows and locators over the published roots, and the local dedup entries that still
answer their original `OK` — held in bounded paged scratch under the same `--index-cache` limit as
every other page, rebuilt from the durable journal at recovery and never by replaying the journal
on a query; **evicting an overlay page erases nothing**, because the journal is the truth and the
next clip writes the overlay into the pages. A startup still loads no whole C and no whole dedup
index (replay `overlay-is-bounded-and-rebuilt`).

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
**What is refused is the one record no page could hold, and it is refused at admission and never
at the clip** (#295 at `c2be4d6b`; Rowan's read there, item 1): a growing record splits or chunks
— repeatable evidence behind a detail root, a long row behind its locator — but a single key with
its one locator that would pass `--page-bytes` on a page of its own is indivisible, and the
mutation that would write it is refused at the candidate gate, exit 2, `<MUTATION> FAIL
request=<id> key=<kind> bytes=<n> past <--page-bytes|--max-bytes>=<n>: indivisible`, nothing journaled and
nothing acknowledged, so canonical history never holds a record the clip could not publish — the
journal paragraph's over-bound record is refused by this gate too, naming `--max-bytes`. So the
sentence before this one stays true: the clip never refuses what was never admitted (replay
`indivisible-record-refused-before-ack`).
The one file a
tool must never write is one it cannot read back, and a remedy that cannot move the number it
names is worse than no remedy at all (Fable at 7472e545, 2026-09-13: the refusal named
`--retain` for an overflow `--retain` cannot shrink; Opus at efc26a2e, 2026-09-13: branching on
the larger part told an operator with `index=55 retained=50` under a bound of 100 to find a
bigger bench, when lowering `--retain` on his own keyboard would have done; Stella at e79847fb,
2026-09-13: while the indexes were in the file, an operator could reach a clip no flag of his
could pass) (replay `clip-names-the-index-that-overflowed`). **A removed subtree leaves the live snapshot the same way**:
the snapshot's structure is O's tree **and the view records of settled roadmaps beside it**, by
item 5 of *What draft 26 changed in the older text* below — a settled roadmap's record stays in
the live snapshot so that opening it is a bounded read — and a node removed by `node remove`, its subtree
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
 :links ("https://github.com/mas-bandwidth/schema/issues/898")   ; an issue is a link, never a type
 :priority (:self (:absent) :subtree (:absent)))               ; ordering intent only; see `prioritise` below
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
- `:epic` — a semantic container, not a prescribed depth or mandatory layer.
  Project and stream containers may sit above it; epics and features may decompose recursively.
  It is a `:work-set` by every rule; it is a kind of its own because **a row kind must be
  queryable and never inferred from a title word** (Stella, `docs/SPEC-WORK-PILOT.md`).
- `:feature` — a work-set whose completion is what a roadmap row counts; the unit of "green
  feature cells / applicable rows". **A feature decomposes into sub-features recursively**, each a
  `:feature` under it, and the counting unit of every rollup stays the one `unit=` names.
- `:roadmap` — a typed view over its cells: `:axes` (ordered, named members), `:cells` mapping
  a coordinate to a `:ref`, `:scope-revision`, `:completion-policy` (`:all-required-features` is the only policy — **default by Rowan,
  unobjected 2026-09-15**), and — from `roadmap create` below — `:members` (the ordered rows of an axisless
  roadmap), `:permitted-roots` and `:projections`, the stored targets. A cell references a node; it never contains state of its own. An unknown axis member and a
  duplicate coordinate are refusals; **a missing cell is not**, and an omitted cell is never
  complete (5653990830). A cell may be marked `:out-of-scope` by a recorded scope event, which
  is distinct from unstarted and from unknown, and **an out-of-scope cell leaves that axis
  member's applicable rows** (5654160320: *fully green features / applicable features*).
  **The members of a roadmap's first axis are its rows, and a row is a node of the roadmap's
  declared `:row-kind`** — `:feature` by default, and `:epic` or `:work-set` where the roadmap
  declares it; **the two field names `:row-kind` and `:aggregation` are the spelling of Stella's
  amendment and are Rowan's (default by Rowan, unobjected 2026-09-15)**, each with the `:aggregation` policy that says how a row of that kind rolls its
  members up. **`:aggregation` is one of three values and no others** *(default by Rowan, unobjected
  2026-09-15)*: `:required-members`, the default, where a row is green when every direct required
  member of it is green; `:all-members`, where every member counts required or not; and
  `:leaves`, where the row folds to the required leaves beneath it and its own intermediate
  containers count for nothing. An unknown value is a refusal at load, never a guessed default. **This amends draft 24's feature-only rule** (Stella,
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
    `node add` — `:verb`, `:node-type`, `:title`, `:under` (`(:node "<id>")` or `(:open-root)`),
    `:category`, `:required`, `:acceptance` (the criteria in the order given), `:repo`, `:links`,
    `:private`, `:version`, `:reason` (completed by the #293 fold below);
    `node edit` (`:verb :node-edit`, wire `node.edit`) — `:verb`, `:title-patch`, `:category-patch`, `:links-patch`, `:private-patch`,
    `:version-patch`, `:reason`, each patch `(:keep)`, `(:clear)` or `(:set V)`;
    `node move` (`:node-move`, wire `node.move`) — `:verb`, `:from`, `:under`, `:reason`;
    `roadmap create` (`:roadmap-create`, wire `roadmap.create`) — `:verb`, `:node-type`, `:title`, `:under`, `:row-kind`, `:aggregation`,
    `:completion-policy`, `:axes`, `:members`, `:permitted-roots`, `:reason`;
    `roadmap configure` (`:roadmap-configure`, wire `roadmap.configure`) — `:verb`, `:row-kind-patch`, `:aggregation-patch`,
    `:completion-policy-patch`, `:axes-patch`, `:permitted-roots-patch`, `:reason`;
    `roadmap row` (`:roadmap-row-add` or `:roadmap-row-remove`, wire `roadmap.row.add|remove`) —
    `:verb`, `:roadmap`, `:member`, `:reason`;
    `roadmap projection --add` (`:roadmap-projection-add`, wire `roadmap.projection.add`) —
    `:verb`, `:projection`, `:reason`; `--remove` (`:roadmap-projection-remove`, wire
    `roadmap.projection.remove`) — `:verb`, `:projection-id`, `:reason`;
    `node remove` — `:verb`, `:under` (the parent it detaches from), `:reason`;
    `node require` — `:verb`, `:to` (`true` or `false`), `:reason`;
    `decompose` — `:verb`, `:children` (in the order `--into` gave them), `:acceptance` (per
    child, in that child order), `:reason`;
    `accept` — `:verb`, `:add`, `:remove`, `:reason` (the one of `:add` and `:remove` the call
    did not give is written `(:absent)`, as every absent field is, and never `()`, which is an
    empty list);
    `dep` — `:verb`, `:add`, `:remove`, `:reason`;
    `axis` — `:verb`, `:roadmap`, `:axis`, `:add`, `:remove`, `:reason` (the one not given
    `(:absent)`; one verb and one wire op `axis` for both forms, where the draft spelled
    `axis.remove` — *(Rowan's decision, for review)*);
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
  - `:prioritise` — `:change` (`:set` or `:clear`), `:context` (`:self` or `:subtree`), `:rank`,
    `:reason`: a recorded ordering act on a node's `:priority`, **neither structure nor scope**, by
    the #293 fold below; its `:before` and `:after` are the engine's, outside the digest.
  - `:lease`, `:heartbeat`, `:release`, `:handoff` (carrying the new holder's `:deadline` and
    `:default`, so a handed lease is a whole lease) — the lease log, below.
  - `:undo` — `:request-of`, `:reason`; and `:redo` — `:request-of`, `:reason`. **The six verbs
    draft 26 added are mutations by *Output grammar* below, so each has a kind, an ordered field
    list and a named subject here** *(Rowan's decision, for review)*: a verb inside `<MUTATION>`
    whose kind the list did not carry would reopen the durable-request-id promise above for
    exactly those verbs, which is the hurt of 7472e545 arriving by a second road (Fable at
    08650d11, 2026-09-13). **These two address no node of the containment forest**: `:node` is
    written `(:absent)`, like any other field the caller did not give, and their `OK` lines print
    `nodes=<n>` — the count of nodes the compensating envelope moved — in place of `node=<id>`,
    which is what `UNDO OK` below already prints. **The typed compensating events the engine
    derives from the named request's preimage are the session's own half of the envelope and are
    outside the payload digest**, exactly as a `:settle` is, so two builds digest one `undo`
    request to one value whatever the preimage held.
  - `:friend` — `:change` (`:register`, `:retire`, `:role`, `:participation`, `:capability` or
    `:limit`, exactly one per event, the form of `friend` the caller used), `:friend`, `:role`,
    `:scope`, `:participation`, `:capability`, `:group`, `:limit`, `:reason`. **Its subject is a
    friend identity and not a node**: `:node` is `(:absent)` and `FRIEND OK` prints
    `friend=<name>` in place of `node=<id>`.
  - `:model` — `:change` (`:register`, `:rate` or `:evidence`), `:model`, `:provider`, `:route`,
    `:billing`, `:pricing`, `:effective`, `:source`, `:task-class`, `:result`, `:samples`,
    `:reason`. Its subject is a model identity: `:node` is `(:absent)` and `MODEL OK` prints
    `model=<id>`.
  - `:observe` — `:change` (`:state` or `:attempt`), `:friend`, `:state`, `:source`, `:attempt`,
    `:observed-model`, `:bench`, `:usage`, `:reason`. Its subject is a friend identity: `:node`
    is `(:absent)` and `OBSERVE OK` prints `friend=<name>`.
  - `:config` — the validated intake of `config --intake`, and the only form of `config` that
    writes an event: `:friend`, `:base` (the hash the delta applies to), `:revision`, `:hash`,
    `:parts`, `:reason`. **What is digested is the manifest's own identity and never the path it
    arrived on**, because `--from` names a file on one bench and a successor of another build
    would digest a different string for the same request. Its subject is a friend identity:
    `:node` is `(:absent)` and `CONFIG OK` prints `friend=<name>`.
  - `:machine` — `:change` (`:register`, `:retire`, `:permit`, `:exclude`, `:limit` or `:fact`),
    `:machine`, `:name`, `:owner`, `:connect`, `:roles`, `:workload`, `:key`, `:value`,
    `:declared-by`, `:reason`. Its subject is a machine identity of *The fleet* below: `:node` is
    `(:absent)` and `MACHINE OK` prints `machine=<id>`.
    **The `friends`, `models` and `fleet` sections these six kinds write are indexes in the resident
    model under the one writer and the one journal, by *Friends, CONFIG and ACTIVE* below, and
    are no node kind of *The data* above** — which is why their events name a friend or a model
    identity rather than a `:node`, and why no count, roadmap or required set moves when one is
    written (replays `new-verbs-have-a-kind-and-a-field-order`,
    `new-verbs-retry-to-one-event`).
  - `:goal` — `:change` (`:set` or `:clear`), `:scope`, `:goal` (the node id; `(:absent)` on a
    `:clear`), `:reason`. **Its subject is a scope and not a node**: `:node` is `(:absent)` and
    `GOAL OK` prints `goal=<id|->`. It writes the `goal` index of *The current goal* below,
    beside `friends` and `models`, under the same one writer and one journal, and moves no
    count, roadmap or required set. `goal update` writes no `:goal` event: it writes a
    `:transition` or an `:evidence` event on the goal node by their own field lists above.
  - `:offer` — `:offer`, `:attempt`, `:generation` (the node's, pinned), `:to`, `:profile`
    (`<capability-id>@<config-revision>`), `:request-ref`, `:payload`, `:payload-sha256`, `:reserve`, `:until`, `:requested-model`,
    `:predecessor-offer`, `:predecessor-attempt`, `:reason`; `:acknowledge` — `:offer`, `:reply`,
    `:stage` (`:received` or `:accepted`), `:provenance`, `:provenance-sha256`, `:deadline`,
    `:default`, `:observed-model`, `:bench`, `:execution`, `:reason`; `:decline` — `:offer`,
    `:reply`, `:provenance`, `:provenance-sha256`, `:reason`. **Their subject is the task node
    and an offer identity**: `:node` is the node the offer names, and `OFFER OK`, `ACKNOWLEDGE
    OK` and `DECLINE OK` print `node=<id> offer=<id>`. Each also carries three fields **the
    verifier derives and a request never supplies** — `:sender`, `:receipt-digest`, `:effect` —
    the session's own half of the envelope, outside the payload digest as a `:settle` is;
    `:effect` is one of `:dispatched`, `:received`, `:accepted`, `:accepted-held`, `:declined`,
    `:late`, `:duplicate`, or `:cancelled` on the one `:offer` an undo appends to end a pending
    offer (*Assignment and execution control* below).
  - `:execution-control` — `:change` (`:pause`, `:stop`, `:resume`, `:correct` or `:reconcile`),
    `:control` (the prior control a `:resume` or a `:reconcile` names, `(:absent)` otherwise: a
    new control's identity is its own request id), `:scope` (a typed selector, `(:node "<id>")`,
    `(:repo "<owner>/<name>")` or `(:all)`), `:action` (`:release-hold` or `:resume-workers`),
    `:instructions`, `:sha256`, `:manifest`, `:reason`. **Its subject is a control and not a
    node**: `:node` is `(:absent)` and `EXECUTION OK` prints `control=<id>`. The capture anchor,
    the target manifest and the receipts the engine derives are records outside the payload
    digest, as a long operation's are. A `:correct` change writes the node's own `:correct` event
    in the same envelope under one request id, so a retry cannot bump a generation twice.
  - `:baseline`, `:discovery`, `:remove`, `:require`, `:defer`, `:cancel` (carrying
    `:evidence` that the worker stopped), `:reopen`, `:split`, `:supersede`, `:scope`, `:axis`,
    `:source`, `:reparent`, `:view`, `:roadmap-row`, `:axis-remove`, `:settle`, `:revive` — the scope log: a baseline records the required set of its node **as the tool
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
    `:axis`, `:member`, `:reason`; `:source` — `:to`, `:reason`; `:reparent` — `:from`,
    `:under`, `:reason`; `:view` — `:change`, `:reason`; `:roadmap-row` — `:change`, `:member`,
    `:reason`; `:axis-remove` — `:axis`, `:member`, `:reason`; `:settle` — `:disposition`
    (`done`, `cancelled`, `superseded` or `removed`), `:reason`, `:already-closed` (the ids
    beneath a removed node that were already in C, empty on every other settle, by the removal
    paragraph below); `:revive` — `:reason`.
    **`:settle` and `:revive` are the session's own half of another verb's envelope and are
    outside the payload digest**, with `:stamp`, `:clock`, `:request` and `:generation-owner`:
    the requester never sent one, the session derives it from the transition it accompanies, and
    a digest that covered it would make two builds disagree about a field neither was given (the
    root section below). **The session-written events of an envelope are these two, the
    `:release` a settle writes for a live lease, and the typed compensating events an `:undo` or
    a `:redo` derives from a named request's preimage, and there are no others**: every one of
    them is outside
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
| `:reparent` | removes the event's node from `:from`'s set where it was in it, and adds it at the bottom of `:under`'s listing where its unchanged `:required` is true; **its own set is unchanged** | both named parents' own, and every roadmap that has it as a row (a revision each, no set of theirs) | `node move` only |
| `:view` | **none** — a create, configure or projection change of the view record | none — the roadmap's revision moves, its set does not | `roadmap create`, `roadmap configure` and `roadmap projection` inside their envelopes |
| `:roadmap-row` | with `:change :add` adds the member at the bottom of the listing; with `:remove` removes it | the roadmap's own view-required set, never a containment parent's | `roadmap row` only, on an axisless roadmap |
| `:axis-remove` | on the first axis removes the row from the set while it is live; on any other axis **none** | the roadmap's own view-required set | `axis --remove` only |
| `:settle` | **none** — it moves the item from O to C, and a member that has finished is still a member: what takes one out of a set is the `:remove`, `:cancel` or `:supersede` in the same envelope, by its own row above | none — the node's revision moves, no set does | `state --to done`, `event --kind cancel`, `event --kind supersede` and `node remove`, each inside its envelope, and never `event --kind settle`, which is exit 2 |
| `:revive` | **none** — it moves the item from C back to O, at `:todo` by the transition table | none — the node's revision moves, no set does | `event --kind reopen` inside its envelope, and never `event --kind revive`, which is exit 2 |

**A roadmap's required set is the members of its first axis whose node is live** — live
meaning not removed, not cancelled and not superseded — **and the axis list is not the set**.
Each row is the node of the roadmap's declared `:row-kind` whose completion that row counts; a row enters the set with the
`:discovery` an `axis --add` on the first axis writes; and it leaves the set, **while staying on
the axis**, with the `:remove`, `:cancel` or `:supersede` of its feature, by the delta table
above. **The two differ by exactly the rows that have left, and that is the design**: a row
leaves the set by its node's closure while staying on the axis, and `node remove` keeps the
member on the first axis (the removal paragraph below says so); the one act that takes a member
off an axis is `axis --remove` of the #293 fold below, a `:axis-remove` scope event of the
roadmap's own, which on the first axis retires the row and on any other axis moves no set. **And the set is derivable with the retention
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
is never a way around the transition table; **and its `:evidence` must cover every attempt of
the node that was live or uncertain at the request**, each confirmed stopped or `not-started`,
so one worker's stop note cannot cancel a node with another live attempt (*Assignment and
execution control* below). `:unknown` is explicit and is neither
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

### The current goal

Glenn (5672006742, *Shared current goal across models*): *"It should be somewhere we could
store the current goal, like /goal is here, but cross model."* The required operations are
his: **set** and **retrieve** the current goal, **update** it as work proceeds, across models
and harnesses; a model switch loads the same current revision and preserves outstanding
ownership; status and evidence updates stay distinguishable from objective and constraint
edits; an old harness state cannot silently overwrite a newer goal, restart completed work or
discard a stop. The coordinator notes a goal is read beside are *Delegation* below; the
goal itself is this subsection's, folded here from SPEC-DELEGATION.md draft 3 (now *Delegation*)
on Stella's clearance (stella-1858e1eeef8d).

**The goal is a node of O, and the current goal is a reference to it.** No new record kind
holds the objective: the objective, its completion criteria, its constraints, its progress,
blockers, ownership, linked work and evidence are what a node already carries above —
`:title`, `:acceptance`, `:deps`, `:responsible`, `:links`, its `:lease`, its `:attempt` and
`:evidence` events, its derived state — *"reuse the existing work/attempt/accounting records"*
(5672006742). What this subsection adds is the reference: a `goal` index in the resident
model, beside `friends` and `models` and no node kind of O, keyed by **scope** — a coordinator
name, or a node id, the same key *Delegation* gives a note — holding one node id per
scope, written by the one event kind `:goal` above **(Rowan's decision, for review)**. A
delegated goal keeps its parent/child mapping because it is a node under its parent node: the
mapping is `:children`, and nothing is copied; a cross-repository goal is a view over owned
nodes, by the paragraph above.

**`goal set`** writes the reference. It is refused when the node does not exist; when the
node's disposition is closed (`done`, `cancelled`, `superseded`, `removed`) — an old harness
cannot restart completed work by pointing at it, and reviving is `event --kind reopen` on a
person's word, its own verb and its own event; when `--as` is not a configured writer of the
scope, by *Friends, CONFIG and ACTIVE*; and when `--expect` is stale. `set` takes no lease and
starts nothing: *"ownership and actual execution handles remain distinct from the goal
record"* (5672006742).

**`goal show`** is what a newly selected model or harness loads, and it is the whole of what it
needs: the `GOAL OK` line — `scope=`, `goal=<id>`, `rev=<n>` (the revision the answer is
evaluated at), `generation=<n>` and `scope-revision=<n>` of the node, `state=`, `owner=<lease
holder|->`, `stop=` (**derived from the state and no second field**: `requested` when the
state is `:cancel-requested`, `cancelled` when `:cancelled`, `deferred` when `:deferred`,
`none` otherwise), `constraints=<n>`, `notes=<n>`, `outstanding=<n>` — then `GOAL ROW` lines:
the objective (`:title` and each `:acceptance` criterion with its current verdict), every
constraint line and note id *Delegation*'s `applicable` would carry for this coordinator,
the progress (evidence events, done and remaining required work under the node, by kind,
counted), the blockers, the live leases and attempts under it with their holders, and the
linked work. Rows are capped by `--max` and counted, `GOAL MORE` when cut; **the `GOAL OK`
fields, the stop, and the constraint rows are never cut** — they print before the capped rows,
as `applicable`'s verdicts do — and with the notes index unloadable `show` prints `GOAL FAIL`
and no row. A reader with no live session reads the published snapshot at its revision under
`--snapshot` with the three bounds, as `check` and `query` do, and gets the same answer for
that revision. Nothing in the answer is the conversation it came from (5672006742: *"must not
require copying the accumulated conversation or guessing whether earlier work stopped"* — the
stop is a field).

**`goal update`** is a thin verb: it names the current goal node of the scope so a harness need
not know the id, and writes the existing event kinds on that node and no other — `--progress
<text>` with `--evidence <pointer> --criterion <id> --against <sha>` an `:evidence` event, the
same event `evidence` writes; `--progress <text>` alone a `:transition :to :doing` with
`:reason <text>`, admitted only where the table admits it (from `:todo`, `:blocked`, `:review`,
and from `:unknown` because the reason is carried) and refused `no edge` on a node already
`:doing` — a progress line on a node already under way carries evidence or it is not written,
because a progress claim with no pointer is the success claim the goal must not accept
**(Rowan's decision, for review)**; `--blocked-by <node-id> --reason` a `:transition :to
:blocked` with its `:blocked-by`, admitted from `:todo`, `:doing` and `:unknown` and refused
`no edge` from `:review`, as the table says; `--stop --reason` a `:transition :to
:cancel-requested` with `:reason` — all under the same `--expect`. **A stop request is not stopped-worker evidence.**
`--stop` takes no evidence and observes nothing; it is exactly the table's own edge to
`:cancel-requested` from `:todo`, `:doing` or `:blocked`, and from `:unknown` because the
reason is carried (*States and transitions* above; the same request `state --to
cancel-requested` writes), with no edge added to the table and none removed from it; and
confirmed cancellation remains `event --kind cancel --evidence <pointer>` on the node, the one
evidence-bearing operation, admitted only from `:cancel-requested`: there is no second
cancellation mechanism (Stella, 5673066509). While the request is pending, `show` prints
`stop=requested` on every harness and every build, and a harness that reads it does not resume
the work: **`goal update` writes no transition on a node in `:cancel-requested`** — a
`--progress` or `--blocked-by` there is refused `stop requested`, nothing written — although
the table's withdrawal edge to `:doing` exists, because that edge is a person's explicit act,
`state --to doing --reason` by id, and never a progress update. **This is the verb narrowing
what the table admits, and the table itself is unchanged; the source draft said the table
refuses it, which the withdrawal edge makes false (Rowan's decision, for review).** To act on a child, the
coordinator either `set`s the goal reference to that child first or uses `state` and `event`
on the child by id. **Objective and constraint edits are not `update`**: changing what the
goal is, is `accept` (criteria), `dep`, `node` and `correct` on the node, each bumping its
scope revision or generation by the rules above, and a note supersede for a routing
constraint (*Delegation*); so a status update and an objective edit are different event
kinds with different revision effects, and a reader tells them apart from the log, never from
wording.

**Revision and conflict.** `goal set` and `goal update` wear `<write flags>` of *The verbs*,
and `--expect` is **required** on both, an omission exit 2 naming the flag — the one place
this file requires it on the coordinator's own path, where *The verbs* leaves it optional
**(Rowan's decision, for review)** — because the goal is what a
harness switch reads, and *"explicit conflict handling"* (5672006742) is the existing rule
applied and not a new one: the expectation is the local revision on `--session`, checked as
every mutation's is, and a stale one is refused at exit 1, `GOAL FAIL scope=<scope>
goal=<id|-> expect=<rev> current=<rev>: stale`, nothing written, the caller re-reading with
`show`. A progress update from a harness that read before a stop is stale by construction,
because the stop moved the revision; a `set` back to a node that finished is refused by
disposition whatever the expectation; and there is one writer, so two harnesses never merge.
A native harness goal feature (a `/goal`) is an adapter: on load it calls `show` and keeps
`rev=`; on write it calls `set` or `update` with that revision; **an adapter that holds no
revision writes nothing**, which is why the flag is required (5672006742: *"native harness
goal features may serve as views/adapters"*). A completed goal requires evidence against its
criteria — the node's `:to :done` names evidence events — *"not merely a worker's success
claim"* (5672006742), which is the rule above already. The five `goal-` replays and
`applicable-cap-never-hides-a-deny` in *Acceptance replays* are the witnesses, stated so a test
can be written from the text and nothing else (Stella, stella-a8da9cb0e0a4).

### Recursive structure within a repository

**The repository boundary does not prescribe the hierarchy beneath it.** Glenn's
2026-09-14 clarification makes project and stream layers first-class uses of the existing
recursive container, not additional mandatory levels. For example:

```text
nova-tools repository
  stream (nova-work)
    epic
      feature
        sub-feature
          task

monorepo
  project
    stream
      epic
        feature
          sub-feature
            task
```

These are examples, not grammars. A team may omit, repeat or nest grouping layers as its
work requires; validation must not enforce a repository/epic/feature depth sequence.
Project and stream containers use `:type :work-set` with the existing explicit `:category`
label (`"project"` or `"stream"`), plus stable `:id`, display `:title` and `:children`.
A tool stream may use category `"stream"` and title `"nova-work"`; its role must not be
inferred from that title or from its position. This adds no new kind, executable Lisp or
untyped mutation verb. Existing kind-specific evidence and completion rules still apply.

Canonical containment, reference edges, ownership, required-member aggregation, settling
and reopening retain the same meaning at every grouping depth. Roadmaps remain durable
views over selected node ids; a roadmap is not a compulsory containment level. Queries
and renders select a scope by stable id and declared units, never by a fixed number of
parent hops. Existing category/repository indexes support discovery of streams and projects;
adding a grouping layer must not make finding W or reading maintained open counts require
a walk of O. Mutations update the affected ancestry and references under the existing
atomic envelope. Any operational depth or size limit must be explicit and fail without
partial mutation; it must not masquerade as a domain hierarchy restriction.

**Acceptance:** round-trip both examples with identity, labels, order, evidence and links
preserved; also round-trip a deeper witness, repository → project → project → stream →
work-set → stream → epic → feature → sub-feature → sub-feature → task, and a shallow
repository → feature → task witness with optional layers omitted; exercise omitted and
repeated grouping layers; query equal semantic scopes at different depths; verify a task is
counted once even when referenced by multiple roadmaps;
settle and reopen through nested project/stream ancestors; compare maintained counts and W
against an independent traversal oracle in tests; reject cycles and multiple containment
parents atomically. Rendering must preserve the selected scope and completion unit across
these layouts. None of this relaxes the separate repository registration or access boundary.

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

**W is materialised eagerly, never rebuilt on a read** *(Stella, `4e800fb`; on Glenn's rule of
2026-09-13 that reading W must never walk O)*. W stays exactly what the paragraph above says —
`(working O)`, derived, written by no verb — and this paragraph says only **how it is held**: a
resident materialised view of the canonical open ids that hold a live lease, **updated inside the
accepted mutation envelope** that carries the item and the lease, beside the canonical task, the
lease and the per-friend indexes, so a reader at a published revision sees one consistent state
and **no read ever rebuilds it**. A pending offer alone is not in W, and two live attempts on one
id count that id once; an assignment, a `take`, a renew, a `release`, an expiry, a settle, a
`:cancel`, a reassignment, an undo and a replay each leave the materialisation equal to
`(working O)` at the revision they publish (**W1**; suite `materialized-working-set`).

**Membership and `|W|` are resident reads, and a listing is `O(k)`** *(Stella, `4e800fb`)*. At a
published revision, *is this id working* and `|W|` are **constant-time resident lookups**;
enumerating k working ids costs `O(k)` in pages bounded by `--page-bytes` and `--page-records`
like every other listing; and **neither scans O and neither reads C, the first ask after a
mutation included**. `|W|` is a counter carried and read, never computed, exactly as `|O|` is in
*Counting* below — and the two are separate counters, neither silently labelled the other. As
there, **no promise of constant time is made for an arbitrary new filter**, only for what this
paragraph names (**W2**; suite `materialized-working-set`).

**Due leases are found through a deadline index, and the watermark is printed** *(Stella,
`4e800fb`)*. Expiry stays derived and is never stored, so the materialisation is exact only as of
**its lease-time watermark**, which every ask that reads W prints beside its `scope=<rev>`.
Advancing the clock barrier processes the leases that are due **through that index, visiting due
leases and not all of O**, and **no constant-time promise is made for that processing** — it is
bounded by how many leases are due and says so. **A stale watermark is printed as stale and is
never presented as current** (**W3**; suite `materialized-working-set`).

**An expiry is not proof that the remote work stopped** *(Stella, `4e800fb`)*. A lease past its
deadline reads as unowned with its responsibility unchanged, which is the lease section's own
rule; and the uncertain records it leaves — an `:attempt` whose outcome is unknown, an operation
whose `external=` is `uncertain`, a friend's ACTIVE capacity — are **retained separately until
they are reconciled** and are never cleared by the expiry that prompted the question, for the
same reason a cancellation is never reported as an erasure (**W4**; suite
`materialized-working-set`).

**W holds references and is not a second store** *(Stella, `4e800fb`)*. It holds ids, not task
bodies, and nothing is true by being in W. **A startup, a recovery and an explicit integrity
check may rebuild it from the canonical state**, with its time-sensitive leases reconciled before
readiness is advertised, and **an ordinary read may not** — the same division *Counting* draws
for the counters, and a recovery that has not reconciled them **advertises no live lease**
(**W5**; suite `materialized-working-set`).

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

## Bugs found while working *(Rowan, on Glenn's word of 2026-09-15, nova-tools#463; additive to the lock at #231)*

**Glenn's words, 2026-09-15 16:00Z, are the requirement**: *"we may find bugs while doing work.
These don't map neatly to features, or sub-features always; at various points in the recursive
structure we should feel free to track straight up bugs that need to be fixed, at the highest
level in the work tree that they can be placed, feature, subfeature, epic whatever."* And of the
same hour: *"once we dogfood, test and find issues, we reproduce those errors and fix and lock in
with tests"*; *"no point just fixing; tests make sure we fix, and the fix stays"*; *"make sure to
capture the dogfood breakage with new tests."* This section adds one kind, three fields, one
validator rule, one count and seven replays, and changes no sentence above or below it.

- `:bug` — a kind of *The data*, beside the six above. **A bug is legal as a child of any node**
  — the open root, an `:epic`, a `:feature`, a sub-feature, a `:work-set` or a `:task` — and **is
  placed at the highest node whose scope contains it**, never lower to make a feature look busy
  and never higher than the work it breaks (Glenn: *the highest level in the work tree that they
  can be placed*). By every rule this section does not restate it is a `:task`: `:acceptance`
  (rule 15), the transition table, generation and `:correct`, `:attempt`, `:evidence` events,
  `verify`, the lease and its defaults, `:private`, `:version`, and `unit=leaves` counts it as
  a leaf. Its own fields:
  - `:found-during "<node-id>"` — **required**: the work that surfaced it. It is a reference and
    never containment — the parent is where the bug is *placed*, `:found-during` is where it was
    *met*, and the two differ exactly when a bug is placed higher than the work that found it.
    Rule 2 checks it as it checks every reference.
  - `:evidence` — the dogfood shape, one record `(:tool "<name>" :command "<argv>" :output
    "<verbatim>" :expected "<text>")`: the tool, the command, what it printed, what it should
    have. Optional at `node add`, because a bug may be reported from a reading before anyone has
    reproduced it; a bug without it prints `evidence=-` on every row so the gap is visible.
  - `:test "<name>"` — **required to close**: the reproducing test that fails before the fix and
    passes after it, in the `test:<path>@<rev>` form an acceptance subject takes. The bug's
    `:acceptance` must hold one `:kind :test` criterion whose `:subject` is this name, and **a bug
    with no `:test` cannot reach `:done`** (rule 19 below): the test is the lock, and a fix without
    one is the fix Glenn called *no point*.

**Counting.** A bug is counted **beside** the tree and never inside a feature's item total:
`done=`, `required=`, `since-baseline=`, `rows=`, `applicable=` and every percentage exclude it,
so finding a bug moves no denominator and fixing one raises no percentage (5654160320: a
denominator moves only by a scope event, and a bug is not scope; the delta table has no row for
it). **An open bug blocks its parent's done**: a container with an open bug beneath it — placed on
it directly or on any descendant — is not derived `:done` and no cell over it is green, exactly as
an unmet dependency gate blocks it; rule 6 reads an open bug as an unfinished child, though it is
no member of the required set and rule 11 never sees it. A bug in O is *open*; a bug in C with
disposition `done` is *fixed*, and one cancelled or superseded is neither. **`who`, `check` and
`stale` print `bugs=<open>/<fixed>` on every line**, both numbers under the line's scope and never
added to any other field (*Counting* below: unknown is a count of its own, and so is this one).
`remaining` and `under` list bugs with the counting row under `--kind bug`; `done` under `--branch
closed` lists fixed ones with the disposition row, `landed=` the revision the fix landed at.

**Verbs.** `node add --type bug --found-during <id> [--evidence <record>] [--test <name>]` writes
the `:structure` event with `:found-during`, `:evidence-record` and `:test` after `:version` in the
field order above, and `NODE OK` prints `kind=bug found-during=<id>`. `accept --add` may add the
test criterion after creation; `node edit --test-patch` sets `:test`, on a bug only. **`decompose`
may mint bugs**: `--into` accepts `bug:<id>` children under any node, each with `--found-during`
and its acceptance, in the one envelope every child shares; a minted bug is listed in the
`:split`'s `:children` beside the tasks, enters no required set, and the delta table counts only
the tasks, so a decomposition that mints one bug and no task moves the scope revision and nothing
else.

**Validator, rule 19 — bug without its lock.** A `:bug` with no `:found-during` is refused at the
candidate gate at `node add`, naming the field. A `:to :done` on a `:bug` whose `:test` is absent,
or whose named evidence qualifies no `:test` criterion with that subject, is refused at the
candidate gate and is a finding on the whole walk: `WORK FAIL <id>: rule 19: no test locks this
fix`. A `:found-during` naming nothing is rule 2, not this rule. A bug on a node in C is rule 18.

**The roadmap lisp.** The roadmap file is restricted Lisp — lists, keywords, strings, integers —
so #463's shorthand `(bug "<id>" :found-during ... :test ...)` is spelled without the symbol:
**any level of `docs/roadmaps/nova-work.sexp`** — the root, an epic, a feature, an item — may carry
`:bugs (...)`, each entry `(:id "<id>" :title "<text>" :found-during "<node-id>" :test "<name>"
:status "open"|"fixed" :source "<issue>")`; the root carries `:current-bugs <open> <fixed>`, and
`tools/roadmap-parity.sh` prints `bugs=<open>/<fixed>` beside its feature and item counts, checked
against that pair, and **never folds a bug into `:current-acceptance-items` or `:current-features`**.
The first bugs are the dogfood edges of 2026-09-15 — #417, #418, #457, #459, #460, #461, #462 —
each entered with the test that closed it, or open with `:test` absent until one does.

### Required replays

| Replay | Required outcome |
| --- | --- |
| bug-at-epic-level-is-legal | `node add --type bug` under the open root, an epic, a feature, a sub-feature and a task each accepted in one session; `unit=features` and `unit=leaves` counts unchanged; `check` prints `bugs=5/0`. |
| bug-needs-found-during | `node add --type bug` without `--found-during` refused at exit 1 naming the field, O, the journal and the indexes unchanged; one naming a missing id refused by rule 2. |
| bug-cannot-close-without-test | `state --to done` on a bug with no `:test` refused `rule 19`; with `:test` set and a verified passing test of another name, refused; with the named test verified at the current generation, accepted; a `correct` then voids it. |
| bug-blocks-parent-done | A feature whose required leaves are all verified done and which holds one open bug, directly or on a descendant, is `unknown`, its cell not green, `percent` unchanged; the bug settled `done`, the feature green with no scope event and no revision moved. |
| who-line-counts-bugs | `who`, `check` and `stale` print `bugs=<open>/<fixed>` under the scope; a bug opened, fixed and revived moves the two numbers and never `done=`, `required=`, `rows=` or `since-baseline=`. |
| decompose-may-mint-bug | `decompose --into bug:<id>` under a feature writes one envelope; the bug carries `:found-during` and its acceptance; the feature's required set is unchanged and its scope revision moved by one; the same call without `--found-during` refused whole. |
| roadmap-bug-form-parses | A `:bugs` list at the root, an epic, a feature and an item level parses under the three bounds; parity prints `bugs=<open>/<fixed>` equal to `:current-bugs`, features and items unchanged; a bare `(bug ...)` symbol form refused by the reader at exit 2 naming the byte offset. |

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
  what a row was, and **the `unit=` label is the row kind pluralised, one per kind and no other
  spelling**: `unit=features` where the kind is `:feature`, which is the default and was the only
  kind before draft 26, `unit=epics` where it is `:epic`, and `unit=work-sets` where it is
  `:work-set` *(Rowan's decision, for review)*. So one line carries
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
  (Stella, `docs/SPEC-WORK-PILOT.md`). **It is envelope metadata and not an event**, alongside
  `:stamp` and `:generation-owner` and outside the payload digest: the closed list of
  session-written event kinds above admits the `:settle`, the `:revive`, the settle's `:release`
  and an undo's compensating events and nothing else, and a counter that had become a fourteenth
  event kind would have broken it *(Rowan's decision, for review)*. The root's open-item count, and the per-repository and
  per-container counts beneath it, are updated by the same envelope that moves an item, including
  the whole of a settle or revive cascade, before its `OK` line is printed; counters count canonical
  item ids once while a container is itself an item, and while it is open it is in `|O|` and in its
    own container's open count, and its settle removes it from both like any other id. **A resident
  current-revision `|O|` query reads the counter and triggers no rollup, no scan, no parse and no
  replay**, and a test that mutates and then asks repeatedly asserts zero visits, zero parses and
  zero replays and compares against an independent full count after a close, a reopen and an
  import replay (replay `open-count-is-read-not-computed`). **The counters count canonical item ids
  once** and exclude references, attempts and history records; **the count's unit and revision are
  printed with it, on one named line**: the ask is `query --ask size`, and `QUERY OK`'s own
  `open=<n>`, `unit=<unit>` and `scope=<rev>` are the count, its unit and its revision, so there
  is no second line and no second spelling for this number; and **an open linked issue count and an open leaf-task count are separate
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
| `fleet` / `fleet --for <workload-kind>` | the fleet of *The fleet* below: every live member with owner, roles, limits and dated declared facts; under `--for`, the members whose declared roles and permits admit the kind and whose exclusions do not — **a recommendation from declared facts, never a lease** (johnny-5b879930aae8) |

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
year of older closed history prints the same four rows, the same `pages=4` (if the depth is
unchanged) and the same startup resident bytes**, which is Stella's W1 written as an assertion
(replay `history-grows-startup-does-not`); and **the same
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
<name> [--request <id>] [--expect <rev>] [--now <stamp>] [--deadline <stamp>] [--dry-run]`**, `--deadline`
being the wire's `"deadline"` field spelled for the CLI — after it the caller stops waiting for
this answer, and it is not a cancellation of the work, which is `operation cancel`: the request id is drawn by
the tool and printed when absent. **`--dry-run` validates arguments, authority and permissions,
fencing and every precondition against the revision `--expect` names (or the captured current
revision if `--expect` is omitted), prints the projected receipt with `dry-run=true`, and writes
zero events, commits no journal revision and records no dedup entry; a dry run reserves nothing
and promises nothing about a later apply — it holds no lease, no id and no slot for the caller;
a later real apply may carry the same `--request` id, and that apply revalidates the expectation
rather than trusting the preview — the revision-bound-plan invariant of the section "The engine
and its client" below, worn by every mutation verb rather than by a plan verb alone.** **`--expect` names a revision the requester can read, and
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
`--every` and `--clip-every` by `session start`; `--by` and `--default` on `acknowledge --stage
accepted` are the same two flags meaning the same lease. **`--as <name>` is caller text on every verb**:
the tool authenticates nobody, the name is what the record will say, and the one place it is
checked against anything is `release`, below. On `session replay` it names the coordinator
applying the bundle and is recorded in `:generation-owner`; **the event's `:by` stays the
request's own author, as the bundle carries it**, because a replay moves a request and never
re-authors it. `--now <stamp>` is optional on every verb and records `:clock
:given`; absent, the session's clock is used and recorded as `:clock :tool`.

```
nova-work session start  --session <path> --as <name> --file <path-in-repo> --journal <path> --cache <path> --repo <path> --remote <name> --branch <name>
                         --max-bytes <n> --max-depth <n> --max-nodes <n> --every <duration> --skew <duration> --clip-every <duration> --clip-after <n> --retain <duration>
                         --savepoint-every <duration> --savepoint-after <n> --max-frame-bytes <n> --silence-ping <duration>
                         --index-cache <n> --page-bytes <n> --page-records <n> [--closed-window <duration>] [--render-root <root-id>=<owner/name>:<directory> ...]
                         [--resolver <scheme>=<command> ...] --git-timeout <seconds> [--attempts <n>] [--repair] [--foreground] [--max <n>] [--now <stamp>]
nova-work session export (--session <path> | --journal <path> --max-bytes <n> --max-depth <n> --max-nodes <n>) --into <path>
nova-work session export (--session <path> | --snapshot <path> --cache <path>) --state --at <revision> --closed-history <none|all|range> [--from <stamp> --to <stamp>] --into <new-directory> --max-bytes <n> --max-depth <n> --max-nodes <n> --max-output-bytes <n>   (a long operation under --session; one finite process under --snapshot)
nova-work state load     --from <export-directory> --into <new-readonly-session> --max-bytes <n> --max-depth <n> --max-nodes <n>   (isolated and read-only: starts no daemon, takes no ownership)
nova-work session replay --session <path> --from <path> --as <name> [--max <n>]
nova-work session status --session <path>
nova-work session stop   --session <path> --git-timeout <seconds> [--attempts <n>] [--no-clip]
nova-work session handoff --session <path> --to <name> --git-timeout <seconds> [--attempts <n>]
nova-work operation status  --session <path> --id <id>
nova-work operation list    --session <path> [--max <n>]
nova-work operation wait    --session <path> --id <id> --timeout <duration> [--after <cursor>]
nova-work operation cancel  --session <path> <write flags> --id <id> --reason <text>
nova-work savepoint list     --session <path> [--max <n>]
nova-work savepoint create   --session <path> --as <name> --reason <text>
nova-work savepoint verify   --session <path> --id <id>
nova-work savepoint restore  --savepoint <path> --into <path> --max-bytes <n> --max-depth <n> --max-nodes <n>   (isolated and read-only: takes no ownership, dispatches nothing, replays no message)
nova-work savepoint compare  --savepoint <path> --against (--session <path> | --snapshot <path> --max-bytes <n> --max-depth <n> --max-nodes <n>) [--max <n>]
nova-work undo-plan      --session <path> --request <id> [--max <n>]
nova-work undo           --session <path> <write flags> --request-of <id> --reason <text>
nova-work redo-plan      --session <path> --request <id> [--max <n>]
nova-work redo           --session <path> <write flags> --request-of <id> --reason <text>
nova-work friend         --session <path> <write flags> (--register <name> | --retire <name> | --role <name>=<role>[:<scope>] | --participation <name>=<yes|no|withdrawn> | --capability <name>=<capability-id> --group <child|swarm|local|one-shot> --limit <n> | --limit <name>=<n>) --reason <text>
nova-work config         --session <path> (--request <name> --base <hash|-> | --export <name> --into <path> | --intake --from <path> <write flags>) [--max <n>]
nova-work model          --session <path> <write flags> (--register <id> --provider <name> --route <text> --billing <metered|subscription|local|unknown> | --rate <id>=<pricing-id> --effective <stamp> --source <pointer> | --evidence <id> --task-class <label> --result <pointer> --samples <n>) --reason <text>
nova-work observe        --session <path> <write flags> --friend <name> (--state <awake|resting|unavailable|unconfirmed> --source <pointer> | --attempt <id> --observed-model <id> --bench <name> --usage <pointer>) --reason <text>
nova-work goal set       --session <path> <write flags> --expect <rev> [--scope <scope>] (--goal <node-id> | --clear) --reason <text>   (--expect required here; --scope defaults to the caller's --as)
nova-work goal show      (--session <path> | --snapshot <path> --max-bytes <n> --max-depth <n> --max-nodes <n> --cache <path>) --as <name> [--scope <scope>] --max <n>
nova-work goal update    --session <path> <write flags> --expect <rev> [--scope <scope>] (--progress <text> [--evidence <pointer> --criterion <id> --against <sha>] | --blocked-by <node-id> --reason <text> | --stop --reason <text>)   (writes on the current goal node of the scope and on no other node)
nova-work machine        --session <path> <write flags> (--register <id> --name <text> --owner <name> --connect <ref> --role <build|test|profile> ... | --retire <id> | --permit <id>=<kind> | --exclude <id>=<kind> | --limit <id> <key>=<n|n,n,...> | --fact <id> <key>=<value> --declared-by <name>) --reason <text>
nova-work offer          --session <path> <write flags> --node <id> --offer <offer-id> --to <name> --profile <capability-id>@<config-revision> --attempt <attempt-id> --generation <n> --request-ref <opaque-id> --payload <pointer> --payload-sha256 <hex> --reserve <slots> --until <stamp> [--requested-model <model-id>] [--predecessor-offer <offer-id> --predecessor-attempt <attempt-id>] [--reason <text>]
nova-work acknowledge    --session <path> <write flags> --offer <offer-id> --reply <receipt-id> --stage <received|accepted> --provenance <pointer> --provenance-sha256 <hex> [--by <duration|stamp> --default <release|extend-once|escalate:<name>>] [--observed-model <model-id>] [--bench <name>] [--execution <handle>] [--reason <text>]   (--stage accepted: --by and --default, required, create-if-needed; --stage received: both exit 2)
nova-work decline        --session <path> <write flags> --offer <offer-id> --reply <receipt-id> --provenance <pointer> --provenance-sha256 <hex> [--reason <text>]
nova-work execution pause     --session <path> <write flags> (--node <id> | --repo <owner/name> | --all) --reason <text>
nova-work execution stop      --session <path> <write flags> (--node <id> | --repo <owner/name> | --all) --reason <text>
nova-work execution resume    --session <path> <write flags> --control <id> --action <release-hold|resume-workers> --reason <text>
nova-work execution correct   --session <path> <write flags> --node <id> --instructions <pointer> --sha256 <hex> --reason <text>
nova-work execution reconcile --session <path> <write flags> --control <id> --from <manifest-id> --reason <text>   (a content identity, never a local path)
nova-work execution status    --session <path> --control <id> [--max <n>]
nova-work clip           --session <path> --as <name> --git-timeout <seconds> [--attempts <n>] [--max <n>] [--now <stamp>]
nova-work check          (--session <path> | --snapshot <path> --max-bytes <n> --max-depth <n> --max-nodes <n> --cache <path>) [--max <n>]
nova-work verify         --session <path> (--offline | --max-fetch <n> --fetch-timeout <seconds>) [--node <id>] [--max <n>]
nova-work query          (--session <path> | --snapshot <path> --max-bytes <n> --max-depth <n> --max-nodes <n> --cache <path>) --ask <kind> --branch <open|closed|root>
                         (--ask is one of: done, remaining, who, percent, size, stream, under, stale, handoffs, roadmap, friends, models, ready, fleet)
                         [--node <id>] [--repo <o/n>] [--owner <name>] [--category <label>] [--axis <member>] [--for <workload-kind>]
                         [--since <revision>] [--at <revision>] [--from <stamp>] [--to <stamp>] [--after <cursor>] [--page-budget <n>] [--max <n>] [--order <discovery|priority>]
                         (who and stale: --window <duration>, required; percent: --axis <member>, required on a matrix and refused on a zero- or one-axis roadmap;
                          ready: --order, optional, discovery by default; --order priority on any other ask is exit 2;
                          --branch closed and --branch root: --from and --to, required, and refused under --branch open;
                          who, stale and handoffs: --branch open only, the other two exit 2;
                          fleet: --for optional, --node names a machine id, and --for with --node on a member that excludes the kind is refused)
nova-work render         --session <path> --view <roadmap-id> (--chat [--projection <id> | --row-axis <id> --column-axis <id> --fixed <axis-id>=<member-id> ...] | --projection <id> (--file | --check)) [--at <revision>]
nova-work node add       --session <path> <write flags> --id <id> --type <work-set|epic|feature|task> (--under <parent-id> | --under-root open --repo <owner/name>) [--title <text>] [--category <label>] [--required <true|false>] [--acceptance <id:kind:subject:predicate> ...] [--link <text> ... | --links-empty | --clear-links] [--private <true|false>] [--version <text>] --reason <text>   (--type roadmap is exit 2 naming `roadmap create`)
nova-work node edit      --session <path> <write flags> --node <id> (--title <text> | --clear-title | --category <label> | --clear-category | --link <text> ... | --links-empty | --clear-links | --private <true|false> | --clear-private | --version <text> | --clear-version) ... --reason <text>
nova-work node move      --session <path> <write flags> --node <id> --from <parent-id> --under <parent-id> --reason <text>
nova-work node remove    --session <path> <write flags> --node <id> --reason <text>
nova-work node require   --session <path> <write flags> --node <id> --to <true|false> --reason <text>
nova-work decompose      --session <path> <write flags> --node <id> --into <id,...> --acceptance <child-id:id:kind:subject:predicate> ... --reason <text>
nova-work accept         --session <path> <write flags> --node <id> (--add <id:kind:subject:predicate> | --remove <id>) --reason <text>
nova-work source         --session <path> <write flags> --node <id> --to <sha> --reason <text>
nova-work dep            --session <path> <write flags> --node <id> (--add <id> | --remove <id>) --reason <text>
nova-work axis           --session <path> <write flags> --roadmap <id> --axis <id> (--add <member> | --remove <member>) --reason <text>
nova-work roadmap create --session <path> <write flags> --id <id> --under <parent-id> [--title <text>] --row-kind <feature|epic|work-set> --aggregation <required-members|all-members|leaves> --completion-policy all-required-features (--axes-none | --axis-id <id> ...) [--permit-root <root-id> ...] --reason <text>
nova-work roadmap configure --session <path> <write flags> --roadmap <id> [--row-kind <kind>] [--aggregation <policy>] [--completion-policy all-required-features] [--axes-none | --axis-id <id> ...] [--permit-root <root-id> ... | --roots-empty] --reason <text>
nova-work roadmap row    --session <path> <write flags> --roadmap <id> (--add <member> | --remove <member>) --reason <text>   (axisless roadmaps only)
nova-work roadmap projection --session <path> <write flags> --roadmap <id> (--add <id> --root <root-id> --repo <owner/name> --path <relative-path> --start <marker> --end <marker> --policy markdown-table [--row-axis <id> --column-axis <id> --fixed <axis-id>=<member-id> ...] | --remove <id>) --reason <text>
nova-work prioritise     --session <path> <write flags> --node <id> (--set <rank> | --clear) [--context <self|subtree>] --reason <text>
nova-work cell           --session <path> <write flags> --roadmap <id> --coord <member,member> (--ref <id|-> | --out-of-scope | --in-scope) --reason <text>   (--ref - clears the mapping)
nova-work responsible    --session <path> <write flags> --node <id> --to <name> --reason <text>
nova-work take           --session <path> <write flags> --node <id> --by <duration|stamp> --default <release|extend-once|escalate:<name>>
nova-work heartbeat      --session <path> <write flags> --node <id> --evidence <pointer>
nova-work release        --session <path> <write flags> --node <id> [--handed <name> --by <duration|stamp> --default <release|extend-once|escalate:<name>>]
nova-work heartbeat      --session <path> <write flags> --allocation <id> --generation <n>   (allocation heartbeat: --allocation names the allocation id returned by take, --generation is the machine generation)
nova-work release        --session <path> <write flags> --allocation <id> --generation <n> [--handed <name>]   (allocation release: --allocation names exactly one allocation, --generation is the machine generation; frees that allocation's slot only)
nova-work attest         --session <path> <write flags> --node <id> --criterion <id> --result <pointer> --against <sha>
nova-work attempt        --session <path> <write flags> --node <id> --model <name> --bench <name> --result <pointer> [--usage <pointer>]
nova-work evidence       --session <path> <write flags> --node <id> --pointer <pointer> --criterion <id> --against <sha> [--attempt <id>]
nova-work state          --session <path> <write flags> --node <id> --to <state> (--evidence <event-id> ... | --reason <text>) [--blocked-by <id>]
nova-work correct        --session <path> <write flags> --node <id> --reason <text>
nova-work event          --session <path> <write flags> --kind <baseline|discovery|defer|cancel|reopen|supersede> --node <id> --reason <text> [--member <id,...>] [--superseded-by <id>] [--evidence <pointer>] (baseline and discovery: --member, required, and --kind discovery on a :roadmap is exit 2 naming `axis --add`; supersede: --superseded-by, required; cancel: --evidence <pointer>, required, and a note: pointer IS admitted here, because it evidences a stopped worker and never a done; --member on any other kind is exit 2)
nova-work version
nova-work help
```

**Every verb spelling in that block is Rowan's, and the block is where they are made**
*(Rowan's decision, for review)*: `operation status|list|wait|cancel`, `savepoint
list|create|verify|restore|compare`, `undo-plan`/`undo`/`redo-plan`/`redo` with `--request-of`,
and `friend`, `config`, `model`, `observe` and `machine` with their flags. Stella's companion names the
operations and leaves the spelling open, and *Additions of the authors'* gathers them again so a
reviewer can find them in one place rather than two. **The `offer`, `acknowledge`, `decline` and
`execution …` spellings are her draft's (#294 at `4fddfcb2`)**, folded in *Assignment and
execution control* below with one change: `acknowledge --stage accepted` spells its lease inputs
`--by` and `--default`, as `take` does, in place of the draft's `--lease-by` and
`--lease-default`, by the no-parallel-aliases rule **(Rowan's decision, for review)**.

**What a mutation does, stated exactly.** `node add`, `node edit`, `node move`, `node remove`, `node require`,
`decompose`, `accept`, `dep`, `axis`, `cell`, `roadmap`, `responsible` and `source` are structure verbs: each appends one structure event and, where it changes a
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
own node is already in C changes no domain state**: it is a no-op at exit 0 with one `NODE NOTE
already-closed node=<id> disposition=<d> settled=<stamp>` line before its `NODE OK`. A retry of the
same request id returns its prior disposition and writes nothing; even after a reopen it leaves the
reopened item untouched. A fresh-id remove after a reopen is evaluated against the current open
state and may really settle or remove. A fresh-id repeated remove while the node is still closed is
the no-effect case: one typed receipt, `changed=0`, with no second `:settle`, no detach, no scope,
membership or counter change. The event revision advances; scope counters and effective-change
identities do not. A second `:settle` for the node finds the closed disposition (rule 18) rather
than writing a second history.
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
force-push and never merge (the resident-session sentence struck here is preserved verbatim, with
its date, in docs/HISTORY.md). An explicit
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

## The duty tier and the single-writer kernel *(Rowan, on the first day of a duty tier, 2026-09-15; #500)*

From the first day of a duty tier (Glenn, Rowan, Stella, 2026-09-15), not cut as a card until
nova-work is open again: the resident session is the duty tier, and the kernel that owns the
structure is one command thread. This section is the amendment. Every rule here has no code yet,
so each rule's first line is **SPEC-AHEAD: #500**, and each rule's replay is appended at the end of
the replay list in *Acceptance replays* below. **Nothing is renumbered and nothing is rewritten**:
the six rules below are added, the validator's rules 1 to 18 and the efficiency rules 1 to 10 are
untouched, and the one older sentence the amendment replaces is the decisions-per-token sentence of
*Decision packets*, replaced in place.

1. SPEC-AHEAD: #500
   **The resident session's authority.** The resident session (E02) is the duty tier: it executes an
   approved finite policy — a versioned record in **C**, `:by` on every rule — with the cheapest
   qualified model or none; it never authors policy; every judgment it cannot make becomes an
   escalation. This is the session's authority, and it is stated as such: the authority to execute
   the approved policy, to pick the cheapest qualified model or none, and to escalate what it cannot
   decide; never the authority to author policy. A policy rule with no `:by` is refused at load,
   because an approved policy is a record of whose word every rule is, exactly as every event's
   `:by` is. Replay: `duty-tier-executes-the-policy`.

2. SPEC-AHEAD: #500
   **Escalation is a node kind, and the row gains the three fields.** Escalation becomes a node kind
   (or the row gains fields): the policy rule that could not decide it, the default that fires on
   silence, and its age (Gas Town idea 5); the coordinator's stale pass reads them. So an escalation
   row carries `:rule` — the policy rule that could not decide; `:default` — the default that fires
   on silence, what happens if the escalation is read too late; and the age — how long the
   escalation has stood. `stale` reads the three and reassigns nothing: they are information, exactly
   as the `escalated-age=` and `reread=` of *Efficiency: lessons absorbed* are information. Replay:
   `escalation-carries-rule-default-age`.

3. SPEC-AHEAD: #500
   **The wait table gains the four presence columns.** The per-harness wait table of *Presence*
   gains four columns, the presence facts: **process alive**, **beat written**, **delivery
   handled**, **parent woke** (the nova-wake drill). A harness with the fourth unproven cannot hold
   a resident session; it may hold a duty session driven by notes. A resident session is a
   coordinator's line, and a coordinator whose parent cannot be woken is the sleeping-coordinator
   case of *Presence* under a second name; so the wait that holds a resident session must have
   demonstrated the whole drill, while a duty session driven by notes — which never authors policy
   and escalates what it cannot decide — may be held by a harness that has proven the first three.
   Replay: `wait-table-four-presence-columns`.

4. SPEC-AHEAD: #500
   **Quiet time.** A resident or duty session makes no model call and sends no note when nothing
   changed; state is published mechanically; so cost per event is measurable. Quiet time is the
   durable-triggers rule of *Efficiency: lessons absorbed* applied to the duty tier itself: nothing
   changed is a trigger that fires on nothing, an empty pulse reruns nothing, and the mechanical
   publication — the clip, the beat, the projection — still happens, so the cost of one event is the
   measured spend of the one call that event caused. Replay: `quiet-time-calls-nothing`.

5. SPEC-AHEAD: #500
   **The coordination measure.** The coordination measure is **cost per accepted decision** across
   tiers, with **wrong or missed decisions** and **recovery latency** as gates; it replaces the
   decisions-per-token sentence of *Decision packets*, replaced in place. A cheap decision that was
   wrong, missed, or recovered slowly is not an accepted decision; the gates are the wrong or missed
   decisions and the recovery latency, and what is measured is what the accepted decisions cost.
   Replay: `cost-per-accepted-decision`.

6. SPEC-AHEAD: #500
   **The single-writer kernel.** The kernel that owns **O** and **C** is one command thread, like
   redis, or it corrupts (Glenn, 2026-09-15). Every mutation is a command applied in order by that
   thread and journaled in the same order — the sequence number is the order; readers, network,
   journal fsync and clip may run elsewhere but never touch the structure; the duty tier and every
   other client are clients of that thread. **Validator rule: a mutation outside the command loop is
   a defect.** Replay: `single-writer-kernel-total-order` — two concurrent clients' commands land in
   one total order, and the journal shows the sequence numbers in that order.


## The engine and its client *(shared; Stella's `docs/SPEC-WORK-PILOT.md` at `81c2885`, integrated; the wire schema is Rowan's)*

**The engine is the resident Common Lisp session of the execution model above; the Go CLI is a
thin client of it over one explicitly named local endpoint: a Unix-domain socket, or on Windows
the named pipe `\\.\pipe\<name>` of the execution model above, which is the same endpoint under
its platform's spelling and never a second transport.** The engine owns the
canonical state, the journal, the indexes and the mutation ordering; **starting a CLI process
reloads nothing**; the session and journal locks and the coordinator fencing of the execution
model are unchanged. **The socket and the directory that holds it belong to the account that runs
the session** — the directory created `0700` and the socket `0600`, both owned by that account
**(Rowan's decision, for review**: Stella's text asks for a local-only endpoint and leaves the
modes open**)** —
**there is no network listener and no remote evaluation protocol anywhere in this scope**, and
**reaching the socket is not a grant of coordinator authority**: every request still carries its
author, its request id and its expectation, and the fencing rules still decide (replay
`endpoint-is-local-and-private`).

**The wire is a versioned, bounded, length-prefixed UTF-8 JSON protocol, and this paragraph pins
it** (Stella's requirement; **the exact schema below is the contract, protocol version 1 —
default by Rowan, unobjected 2026-09-15** — since her text names the properties and leaves the
spelling open; it is pinned by **one generated schema file** (every verb with its op, event kind,
ordered fields and grammar line) and **one coverage test** over it; Emma is building the file, and
the file with its test is the lock gate's artifact for the verb and protocol schemas). One message is a **4-byte big-endian
unsigned length** followed by that many bytes of one UTF-8 JSON object; a frame past
`--max-frame-bytes` (defaulting to the session's `--max-bytes`) is refused with one framed error
and the connection is then closed, never truncated and never partially applied. **Every integer
the protocol carries is a JSON string of decimal digits — ids, revisions, counters, byte counts
and token totals alike — and the protocol carries no JSON numbers at all**, because an IEEE-754
double rounds silently above 2^53 and a usage total is exactly where that bites; a reader that
meets a JSON number refuses the frame. **Every timestamp is RFC 3339 in UTC with a trailing `Z`**,
the same spelling `:stamp` uses, never an offset, never a local zone and never an epoch count.
**An absent key and a JSON `null` both mean *not given*, while an empty string and an empty array
are values** — the same distinction the payload digest makes by writing an absent field
`(:absent)` and an empty one `()`, so the two serializations agree and a wire `null`, a missing
key and an empty array cannot digest to one value (replay
`absent-empty-and-null-are-three-spellings`). A request is `{"op": "<verb>", "request": "<id>", "as": "<name>", "expect": "<rev>", "now":
"<stamp>", "max": "<n>", "deadline": "<stamp>", "args": {…}}` with `op` a **typed operation name
and never an executable form**, no Lisp, no shell, no path the engine did not resolve itself. A
response is `{"request": "<id>", "ok": true|false, "exit": "<0|1|2>", "lines": [ … ], "rev": "<n>", "pushed":
"<rev>|-"}`, where **`lines` are exactly the one-line answers of *Output grammar* below,
verbatim** — so the grammar has one definition and the CLI prints what it was handed rather than
formatting a second time. **The client splits them by the second token and by nothing else**:
`OK`, `ROW`, `NOTE` and `MORE` to stdout, `FAIL` and `RACED` to stderr, which is *Output
grammar*'s own rule read on the client side, so the wire carries one ordered list and needs no
stream field. **Versions are negotiated before any request**: the client's first frame
is `{"op": "hello", "protocol": ["1"], "client": "<build identity>"}` and the session answers with
the one version it will speak or refuses, naming what it supports, and closes; an unsupported
version fails clearly and never degrades into a guess. **Restricted s-expressions remain the
durable work-data format** — JSON is the wire and never the store (replays
`wire-integers-are-strings`, `protocol-version-negotiated-or-refused`).

**Every ordinary response echoes its request id** *(Stella's correction for review)*.
This includes read-only queries, refusals, batch envelopes, and the initial acknowledgement
of a long operation. The client matches replies by this field, never arrival order or the
human-readable lines: a bounded status query may finish before an earlier slow operation.
The long operation's durable id identifies the continuing work; it does not replace the id
of the request that asked for it. An independent batch also names each entry's request id
beside its disposition, including `not attempted`; an atomic batch retains its envelope id
and the stable entry ids used to identify validation failures. These are the existing ids,
not a second deduplication mechanism.

The client does not put the same request id in flight twice on one connection. An uncertain
retry on a new connection keeps the original id and payload and uses the existing durable
mutation-disposition rules. A response with an unknown, duplicate or missing request id is a
protocol error: close the connection and reconcile outstanding mutation ids rather than
guessing which request succeeded. A frame refused before a valid id can be decoded carries
`"request": null` and closes the connection; it does not acknowledge any queued request.
The initial `hello` negotiation is the sole ordinary exchange without a request id and
finishes before pipelining begins (replay `pipeline-replies-are-correlated`).

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
CLI may exit while the work continues. **The id is durable before it is printed** *(Rowan's
decision, for review)*: the engine draws it, appends the accept record — the id, the operation
kind, the request id, the author and the stamp — to the local recovery journal, waits for that
journal to be durable, and only then replies, so a crash between accepting the work and
acknowledging it can never leave a caller holding an id the restart never heard of, and the
recovery reconciliation below has an id for every operation a caller was ever told about. **An id
no journal holds has a line of its own**: `OPERATION FAIL id=<id> op=- state=-: no such
operation`, exit 2, never an invented `queued`. The spellings are `nova-work operation status --id <id>`, `operation list`,
`operation wait --id <id> --timeout <duration> [--after <cursor>]` and `operation cancel --id
<id>`, each bounded and capped like every other listing. **Waiting is an event cursor and a bounded
block, never a model asking again in a loop** (a poll loop is a whole agent per tick, which the
record already paid for). **A wait that times out leaves the operation running**; a completed
result is retrievable by its id afterwards; **a cancellation is a request with its own
acknowledgement and its own final disposition** — so it takes `<write flags>` like every other
request, carrying its `--as` and its own `--request` id, deduplicated by the same predicate, and
a cancel replayed twice cancels once — and it can neither erase an accepted mutation nor
undo an external effect that may already have happened. **The clip's transport is that shape and is not a second synchronous one** *(Rowan's decision,
for review)*, which is the one reading of it this document keeps: `clip` prints `OPERATION OK
id=<id> op=clip state=<queued|running>` at once and exits; the `CLIP OK` line, carrying
`operation=<id>`, is what `operation wait --id <id>` prints when the transport settles, and
`CLIP RACED` and `CLIP FAIL` arrive the same way; and **`session stop` is the one caller that
waits for its own clip**, and it waits by that same `operation wait`, inside its
`--git-timeout`, rather than by a synchronous path of its own. So the engine section, the output
grammar and the stop paragraph say one thing (replay `clip-is-one-long-operation`).
**Slow I/O stages its inputs and results
outside the mutation loop** and only the owning engine admits a validated result at an expected
revision, so a concurrent capture never becomes a second writer. **Exports pin a captured
revision** (Stella, `docs/SPEC-WORK-PILOT.md` at `81c2885`, restored in draft 27): an export
names the revision it was taken at, reads that revision and no later one, and its receipt carries
that number — which is the revision `full-round-trip` below compares across a fresh engine, and
the reason a long export can run beside accepted mutations without either one moving under the
other. Queues, jobs, staged bytes and retained results are bounded by explicit limits, accepted work stays recoverable, and **recovery
reconciles interrupted operation ids and their external outcomes before anything is retried**.
**No unbounded scan and no network wait may hold the mutation loop**: it is paginated or staged,
and status and cancellation responsiveness are measured under load (replays
`operation-survives-the-client`, `cancel-is-a-request-not-an-erasure`,
`status-answers-while-io-runs`).

**Batches ride the envelope that is already here** *(Stella, `bc4a4a4`; the three modes are hers,
the refusals are this document's)*. One invocation may carry **a bounded array of
schema-validated commands** over the framed protocol above: the resident session is not restarted
for a command, a persistent client **may pipeline request frames** without waiting for each
preceding reply, and every request carries its own id so every reply is attributable.
**Pipelining creates no second canonical writer** — *One coordinator, one live reader/writer*
below is unchanged by it. **There are three modes and no fourth, and none of them is a second
transaction mechanism**: each rides the accepted mutation envelope and the long-operation
protocol this section already defines, and the spelling of each is generated from the command
schema rather than written twice (**B1**; suite `batches-and-pipelines`).

**A read bundle is one revision and one watermark** *(Stella, `bc4a4a4`)*. It evaluates bounded
queries against **one captured revision and one lease-time watermark**, returns the fields and
aggregates its asks name and never the work set, and a later page **keeps that snapshot identity
or refuses `page expired`** — the cursor rule of *Retention* above, not a second one (**B2**;
suite `batches-and-pipelines`).

**An independent batch is ordered entries with outcomes of their own** *(Stella, `bc4a4a4`)*.
Each entry carries its own request id, its own validation and its own durable disposition; **the
default is to stop at the first refusal and mark every remaining entry `not attempted`**, and
continuing past a refusal is an explicit flag and never the default; other requests may interleave
between entries; **the mode promises no rollback and no single shared revision**; and an entry
that accepts a long operation returns **an operation id**, which a later entry may not read as a
completed one (**B3**; suite `batches-and-pipelines`).

**An atomic mutation batch is one envelope, all or none** *(Stella, `bc4a4a4`)*. Every entry,
including its ordered staged effect, is validated **against the expected revision** before
anything is published; then the item state, O and W membership, the counters and the reverse
indexes move **in one accepted mutation envelope or not at all** — the envelope of *The execution
model*, never a second path. **No external I/O and no worker launch happens inside it.** Draft 27
settles that **the external effects are outcomes and not verbs of this grammar**, so there is no
verb to refuse under that name and the refusal is written where the effect actually is *(Rowan's
decision, for review)*: an entry that would accept a long operation — a source capture, an import
staging, an export, a clip's transport — is **refused by its own entry id before anything is
staged**, and a dispatch that follows the batch keeps its own operation id, its own durable
outcome and its own retry rule (**B4**; suite `batches-and-pipelines`).

**The ids, the limits and the backpressure are the ones already written down** *(Stella,
`bc4a4a4`)*. A batch id and an entry id are **stable across an uncertain retry**; the dedup
predicate of *Retention* applies to each independent mutation and to the atomic envelope whole;
**the same id with a changed payload is refused** `reused with a different payload`; **a
disconnect is not a cancellation** — the accepted outcome is asked for and never guessed — and
**a successful prefix is never replayed as new work**. Every response carries per-entry status
and revision, the aggregate accepted, refused and not-attempted counts, and bounded error detail
with receipt drill-down, and **a compact form may never hide a failed or an uncertain entry**.
The named limits — command count, request and reply bytes, staged mutation size, snapshot
lifetime and queued work — are **negotiated at the `hello` handshake** and enforced there; **an
oversized atomic batch is refused before any mutation and is never silently split**; and an
independent batch is bounded and backpressured so bulk work **can never starve status,
cancellation or lease renewal**, which is the responsiveness the paragraph above already
measures. **The entries are typed verbs of *The verbs* and nothing else**: batch data evaluates
no Lisp, and no entry may reference an unchecked result of another (**B5**; suite
`batches-and-pipelines`).

**What the batches are a hypothesis about is said once and claimed nowhere.** The sources are
[Redis pipelining](https://redis.io/docs/latest/develop/using-commands/pipelining/) and
[Redis transactions](https://redis.io/docs/latest/develop/using-commands/transactions/), which
separate grouped execution from amortised round trips; **neither is a storage dependency**, and
this document keeps its own stronger all-or-none validated contract rather than Redis's
runtime-error semantics. **The hypothesis is measured on accepted-work tokens and turns,
corrections and retries included, and never on socket speed**: a sequential and a bundled run of
one real workload are compared on what the accepted work cost, because neither fewer calls nor
smaller output is itself a saving. **No performance claim is made here** (suite
`batches-and-pipelines`).

**Mistakes are reversible by appending, never by erasing.** `undo-plan` and `undo`, `redo-plan` and
`redo` name **accepted request ids**, and each reversible verb records enough preimage and
provenance for the engine to build a **typed compensating envelope**: the original event stays
exactly where it is, the reversal is appended with its lineage, and **redo reapplies the intent
against current preconditions rather than deleting the undo**. A plan is revision-bound and shows
the nodes, dependencies, counters, verification and assignment effects it would move; **a stale or
conflicting plan refuses atomically**, naming what changed, and is never half-applied. **Stella's
companion allowed a second answer here — *refuse atomically or require explicit reconciliation* —
and this draft keeps only the refusal** (`docs/SPEC-WORK-VALIDATION.md` at `81c2885`; the
narrowing is named rather than silent): a reconciliation the tool asks for is a second interactive
path with its own states, and the refusal already tells the caller what moved, after which a
fresh plan at the current revision is the whole reconciliation. **Accepted
evidence and source and accounting receipts are historical facts**: an undo may supersede what they
currently support and can never erase that they happened. **A sent message, a paid execution, a
publication and a source deletion are not undone by rewinding local state** — they are reported as
external effects with their own compensating workflow, a cancellation stays a request until its
outcome is known, and **a generic undo of an irreversible or uncertain operation is refused**.
**Resetting shared Git history is never the undo mechanism** (replays `undo-appends-and-preserves`,
`redo-refuses-a-stale-plan`, `undo-refuses-an-external-effect`).

**Which verbs are reversible is named here, verb by verb, and the rest are refused by name**
*(Rowan's decision, for review)* — *each reversible verb* above named no set, and a reader could
not tell whether an `event --kind cancel` had a compensating kind to append. It does not: the
transition table's `:cancelled`, `:superseded` and `:removed` are terminal and no `:reopen`
reaches them, so an undo over one of them is refused rather than given a new kind that would
make a terminal state reachable by a back door. The way on from a cancelled or removed item is
new work with a `:dep` on the closed id, which is a record of the decision and not a rewind.

| verb | undo appends | refused, `not reversible here`, when |
| --- | --- | --- |
| `node add` | a `node remove` envelope on the node it created | the node has since taken children, evidence, a lease or a cell reference |
| `node remove` | — | always: `:removed` is terminal |
| `node require` | `node require --to` the preimage `:required` | — |
| `decompose` | a `node remove` envelope for each child it created | any child has since taken work of its own |
| `accept` | `accept --remove` of an added criterion, `accept --add` of the removed preimage | the node is derived `:done`, by rule 5 at the candidate gate |
| `dep` | `dep --remove` of an added edge, `dep --add` of a removed one | — |
| `axis` | `axis --remove` of an added member, `axis --add` of a removed one restoring its position and its cells | a removed member's node has since been removed, or the restored coordinates now hold other references |
| `node edit` | the compensating `node edit` restoring `:before` | the node is closed, or its current metadata is not this event's postimage |
| `node move` | a `node move` back to `:from`, restoring the preimage order and required sets in one envelope | the node is closed, either parent's children, required set or scope revision is not this event's postimage, or a context guard now fails |
| `roadmap create` | a `node remove` envelope on the roadmap | a member, cell or projection outside its preimage survives |
| `roadmap configure`, `roadmap row`, `roadmap projection` | the same verb in its preimage form | the current view record is not this event's postimage |
| `prioritise` | `prioritise` restoring the preimage slot | the slot's latest-change identity is not this event's `:after` |
| `cell` | `cell` back to the preimage `:ref`, `:out-of-scope` or `:in-scope` | the preimage `:ref` names a node since removed |
| `responsible` | `responsible --to` the preimage name | — |
| `source` | `source --to` the preimage sha | — |
| `take` | a `release` envelope | the lease has expired or another holder took it |
| `release` | a `take` envelope restoring the preimage holder and deadline | the node has since been taken by another |
| `offer` | a cancellation of the pending offer, releasing only its untouched reservation | the offer has been handed to transport — a claimed or in-flight send included — a receipt exists, or a successor changed its lineage; the way on is `decline`, a hold or a replacement offer |
| `acknowledge`, `decline` | — | always: each records a verified receipt, and an acceptance may have created or bound a lease; a later decline, hold, release or reconciliation is a new act |
| `execution pause`, `execution stop` | a reversal removing only this untouched hold and cancelling its unsent directives | any directive delivered or any target uncertain; the way on is `execution resume --action release-hold` after the control's outcomes reconcile, and no undo claims a worker restarted |
| `execution resume` | a reversal restoring only the prior untouched hold and cancelling unsent resume directives | a resume delivered, or a running or unknown observation |
| `execution correct` | a reversal, only before any correction directive leaves the coordinator and only while the old and new generation postimages and the retained instruction identity are unchanged | any delivery or any old- or new-generation uncertainty; a reapply of earlier instructions is a new generation with lineage, never a rewind |
| `execution reconcile` | — | always: it records validated observations; a later reconciliation records a new set and keeps the earlier one, contradictions included |
| `state --to <s>` | a `state --to` the preimage state, and where the original settled the item, the `event --kind reopen` envelope that writes its `:revive` | the preimage state is unreachable by the transition table |
| `event --kind baseline` | — | always: a baseline records what the set was at a moment |
| `event --kind discovery` | a `node require --to false` for each member it added | a member has since closed |
| `event --kind defer` | `event --kind reopen` | — |
| `event --kind reopen` | the verb that closed it, against the preimage disposition | the preimage disposition is `cancelled`, `superseded` or `removed` |
| `event --kind cancel`, `event --kind supersede` | — | always: both dispositions are terminal |
| `heartbeat`, `attempt`, `evidence`, `attest`, `correct`, `observe` | — | always: each records that something happened, and the paragraph above already says an accepted receipt is a historical fact an undo may supersede and can never erase |
| `friend` | the same verb in its preimage form | the preimage is a retirement whose identity has since been reused |
| `model --register`, `model --rate` | the same verb naming the preimage route or rate | a pricing record — immutable by content identity, so an undo supersedes it and never rewrites it |
| `model --evidence` | — | always: an observation |
| `config --intake` | an intake of the retained preimage manifest | the preimage manifest is no longer retained |
| `undo`, `redo` | — | always: an undo is not undone, it is redone |

**The external effects the paragraph above names are outcomes and not verbs of this grammar** — a
sent message, a paid execution, a publication, a source deletion — and none of them is written by
any line of *The verbs*. They reach an undo only through the verb that recorded them here: an
`:attempt` with its `:usage`, an `:evidence` pointer, a `:model` rate receipt, or an operation id
whose external state is `known` or `uncertain`. Each of those rows above is already refused, so
`UNDO FAIL request-of=<id> effect=external handle=<text>: not reversible here` is printed with
the operation id or the pointer as its `handle=`, and the compensating workflow is the owning
system's (replay `undo-names-its-reversible-set`).

**The verb families below are a coverage requirement, and the grammar above is the one spelling
of every verb this draft has.** Where the table names an action no verb above writes, it is a
**missing verb**, and the missing ones are listed after the table rather than left for a reader
to discover by failing to find them — *the implementation plan names the missing verbs before the
lock gate* is the rule, and a list here is what makes it checkable.
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
| session and durability | start, status, clip, savepoint list/create/verify, isolated restore and compare, export/replay, stop, fenced handoff |
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

**The missing verbs, named** *(Rowan's decision, for review — the list, not its contents: each is
Stella's table above asking for something the grammar has not got)*. Until each exists, the field
or action it owns is unreachable by any recorded act, which is the coverage requirement unmet and
not a second way in:

- **`node edit`** — *filled by the #293 fold below*: one verb over exactly the five permitted
  metadata fields, each a tagged patch, a `:structure` event with its preimage, and **no generic
  set-field escape hatch**.
- **`node add --repo`** — *filled by the #293 fold below*: the flag is on `node add` with
  `--under-root open` and nowhere else, since a work set's repository is not editable metadata.
- **`node move`** — *filled by the #293 fold below*: *move/reparent* in the table above, two
  required sets in one envelope with its `:reparent` scope event.
- **`axis --remove`** — *filled by the #293 fold below*, and `axis` is reversible in the undo
  table above from there.
- **`roadmap`** — *filled by the #293 fold below*: `roadmap create|configure|row|projection`
  own the view record, and `render` reads the stored projection, so the command-line gap the
  stored-metadata sentence named is closed.
- **`prioritise`** — *filled by the #293 fold below*: a recorded ordering act, `:priority` on
  the node and `:prioritise` in the log, a noun and a verb both.
- **`session export --at <revision>`** — *filled by the #293 fold below* as `session export
  --state --at`, a second product beside the request bundle, with `state load` to read it.

**And the reverse: no verb of this grammar lacks a noun any more.** The six `<MUTATION>` verbs
draft 26 added had no `:event` kind and no subject; the kinds above give each one, and
`every-field-has-an-owning-verb` reads both ways from here — every field to its verb, and every
mutation verb to its event kind, its ordered field list and its subject.

## The structural verbs, the roadmap's verbs, priority and the captured-state export *(Stella's draft on #293 at `47c3f9a3`, folded; the contracts are the draft's, each spelling below is Rowan's where marked)*

Seven rows of the missing-verb list above are filled here, from the five drafts #293 carried,
read and approved at `47c3f9a3`: `node add --repo` and the completed `node add`, `node edit`,
`node move`, `axis --remove`, `roadmap`, `prioritise` and `session export --at`. Each is folded
into the grammar block, the `:structure` field orders, the scope-kind registry and its delta
table, the reversible-verb table, *Output grammar* and the named replays above and below, so a
reader finds the verb where every other verb is and not in a draft. Two things the drafts
proposed are already this document's and are cited rather than re-decided: `--dry-run` is in
`<write flags>` of *The verbs*, and **the no-effect receipt** — an accepted typed event with a
real id, `changed=0`, the event revision advanced and no scope, membership, counter or index
moved — is the rule the removal paragraph and replay `no-effect-mutation-is-journaled` already
state, and every verb here wears it as written there.

**`node add`, completed** *(Rowan's decision, for review: the draft's order, kept)*. Its
`:structure` field order is the twelve fields the `:structure` registry above lists for it,
every field serialized —
absent `(:absent)`, an empty list `()`, an empty text `""`, `false` a value — and that order is
the payload digest. **There is no legacy path for the shorter order**: no production stream
exists to keep it for, so a loader that meets a fixture or event in the pre-fold add shape
refuses `schema revision unsupported` rather than reading it as the new one, and fixtures are
regenerated; a migration, if one is ever wanted, names its input revision and its exact
re-encoding and is approved on its own. **The parent is typed, never spelled**: `--under <id>` is
`:under (:node "<id>")` and the wire's `{"kind":"node","id":"<id>"}`; the top of O, which the
grammar could not name, is `--under-root open`, `:under (:open-root)`, wire `{"kind":"open-root"}`;
an unknown kind or key refuses, and no id is read as a root by its spelling. **`--repo` is
required with `--under-root open` and refused anywhere else** (`repo outside root`, exit 2;
`root needs a repo` where the root form omits it), and the root form admits `--type work-set`
alone (`root needs a work set`, exit 2); it is a nonempty `<owner>/<name>`, unique in the repository index
(`repo held by <id>`), and **immutable**: no edit and no move changes a work set's repository.
`--type roadmap` is refused at exit 2 naming `roadmap create`, below; `:event` and `:lease` stay
unaddable; a direct add under a roadmap stays exit 2 naming `axis --add`, by the delta table. A
link is nonempty UTF-8 reference text under the session's string bounds, NUL and ASCII control
characters refused (`bad link`); it need not be a URL, its bytes and order are kept, **it is
never fetched, executed, normalised or given any access**, and it is not a dependency edge.
`--link` repeated sets the whole ordered list, `--links-empty` writes `()`, `--clear-links`
writes `(:absent)`, and the three are exclusive.

**`node edit`** is the one verb over exactly the five permitted metadata fields — `:title`,
`:category`, `:links`, `:private`, `:version` — **and each field is a tagged patch, present on
every request**: `(:keep)`, `(:clear)` or `(:set V)`, the wire's `{"op":"keep"}`,
`{"op":"clear"}` and `{"op":"set","value":V}`, so keep, clear, set-empty and set-value digest
to four values. Its `:structure` order is `:verb`, `:title-patch`, `:category-patch`,
`:links-patch`, `:private-patch`, `:version-patch`, `:reason`; **its `:before` is the engine's**,
a five-field map in that field order outside the payload digest and kept for undo, absent
written `(:absent)` and an explicit empty or `false` kept as the value. A missing, `null`,
unknown or duplicate key, an `op` with the wrong value shape, a `set` of the wrong type, or a
request that keeps all five is refused at exit 2 (`all keep`: it has no CLI spelling); `:version`
set or clear is admitted on a `:task` only (`version on a <kind>`), its keep legal on every kind
because it changes nothing. **It changes no `:id`, `:type`, containment, `:deps`, `:acceptance`,
`:required`, `:responsible`, `:repo`, source, state or view record.** It writes its `:structure`
event and no scope event, moves no required set, no scope revision and no counter, updates the
category index where the category moved, invalidates the render projections that reach the
node, and keeps the privacy floor: a private node and its descendants leave every public
render and `private=<n>` is all that is printed of them; a refusal names the field and never
prints a private value. `changed=` counts the fields that differ, `0` the no-effect receipt above. `NODE OK … change=edit
changed=<n>`. Its undo is the compensating edit restoring `:before`, admitted only while the
node is open and its current metadata still equals this event's postimage; an intervening edit
is a conflict, never overwritten (replays `metadata-patches-preserve-intent`,
`edit-is-atomic-and-replayable`, `edit-undo-preserves-later-work`, `edit-never-fetches-a-link`,
`repo-only-at-the-root`, `roadmap-has-one-creator`).

**`node move`** is *move/reparent* of the table above, and it is one envelope: a `:node-move`
`:structure` event — `:verb`, `:from`, `:under`, `:reason`, both parents `(:node "<id>")` and
never `(:open-root)` — and a paired **`:reparent` scope event on the moved node** — `:from`,
`:under`, `:reason` — serialized in that order, the structure event first. `--from` is the
caller's stated parent and is checked, not inferred afresh on a retry; the id is opaque and is
never renamed to match the new place; the node is appended at the destination's end — **this is
reparenting and never sibling reordering**. **By the sentence above the delta table, the
`:reparent` counts in the source parent's, the destination parent's, the moved node's own, and
every scope revision of a roadmap that has the node as a row**, the last found through the
reverse roadmap reference index and never by walking descendants; its delta is in the table. A
required node leaves `:from`'s set and enters `:under`'s at the bottom of the listing under its
own unchanged `:required`; **|O|, |C| and W are unchanged**, no state, acceptance, generation,
source revision, `:responsible`, lease, attempt or usage pointer moves, nothing settles and
nothing revives, and closed descendants stay closed under their ids. Parent-local reporting
records the transfer out and the transfer in as such, so a coordinator can tell redistribution
from new work; a common ancestor is updated once with the net delta. **The refusals, `NODE FAIL
node=<id>: <reason>`, nothing written**: `parent conflict` (`--from` is not the actual parent),
`cycle` (a destination inside the subtree), `not movable` (a root container, a repository root
or a validator-owned shared container), `repository change` (a destination under another
repository — a move is not a source, issue or attribution migration, and the cross-repository
transfer is named unsolved below), `roadmap operation required` (a roadmap as source or
destination: rows are `axis`'s and `roadmap row`'s), `active context change` (the move would
change the effective `:responsible` of any node in the reached subtree that holds a live lease
or an unreconciled attempt, the affected ids named; silence never proves an execution ended),
`privacy reduction` (a node whose effective privacy — its own marker or any containment
ancestor's — would fall; raising privacy is admitted and invalidates the public projections),
and `unavailable` (a guard whose history this read cannot reach is refused, never guessed
public or idle). A request whose `--from` equals `--under` and names the actual parent is the
no-effect receipt: the structure event alone, `changed=0`, sibling order untouched. **Its
`:before` and `:after` are the engine's, outside the digest**: `:from-children`,
`:under-children` (ordered direct ids), `:from-required`, `:under-required` (ordered direct
required sets), `:from-scope`, `:under-scope` (revisions), `:roadmap-scopes` (ordered `(:roadmap
<id> :scope <rev>)` rows for the complete affected set), then `:context` rows sorted by id, each
`(:node <id> :private <bool> :responsible <name|absent> :execution-refs (<id> ...))`; the
direct lists are bounded by the envelope bounds and an oversize set refuses before it is
accepted; no transitive descendant set is stored. `NODE OK … change=move changed=<0|1>
from=<id> under=<id>`. Undo compares the current ordered children, required sets and scope
revisions of both parents and every affected roadmap with the stored `:after`, re-evaluates the
two context guards against the live lease indexes, and then restores containment alone in one
validated envelope, appending fresh scope revisions — **an old scope number is a guard and
never a value written back** — refusing conflict on an intervening reorder, reparent or privacy
change and never guessing an insertion point (replays `move-keeps-every-count`,
`move-same-parent-is-a-receipt`, `move-refuses-by-name`, `move-keeps-the-lease`,
`move-updates-every-roadmap-scope`, `move-undo-refuses-a-reorder`).

**A roadmap is a node plus a view record, and `roadmap create` is its one creator** *(Rowan's
decision, for review: the draft's owner, adopted; `node add --type roadmap` refuses naming it,
so there is no second spelling)*. It writes the node and its initial view atomically, as one
`:roadmap-create` `:structure` event: `:verb`, `:node-type` (`:roadmap`), `:title`, `:under`,
`:row-kind`, `:aggregation`, `:completion-policy`, `:axes`, `:members`, `:permitted-roots`,
`:reason`, its `:under` `(:node "<id>")` only, never the open root; every create starts with
`:members ()`, `--axes-none` writes `:axes ()`, and axis ids are distinct. Zero or one axis is rows with no cells; two or more are a matrix. **The view
record gains three fields**: `:members`, the ordered rows of an axisless roadmap;
`:permitted-roots`, opaque root ids; and `:projections`, each `(:id <id> :root <root-id> :repo
<owner/name> :path <relative> :start <marker> :end <marker> :policy :markdown-table :row-axis
<id|absent> :column-axis <id|absent> :fixed ((<axis-id> <member-id>) ...))`, ids unique per
roadmap, the path clean, relative and contained, the markers distinct nonempty text, and for a
matrix the row and column axes distinct declared axes with `:fixed` naming exactly one member
of every other axis; for zero or one axis the two are `(:absent)` and `:fixed` is `()`. **Four
verbs own the record after creation.** `roadmap configure` patches `:row-kind`, `:aggregation`,
`:completion-policy`, `:axes` and `:permitted-roots` with `(:keep)` or `(:set V)` — the three
policies admit no clear, `()` is explicit empty axes or roots — in the order `:verb`,
`:row-kind-patch`, `:aggregation-patch`, `:completion-policy-patch`, `:axes-patch`,
`:permitted-roots-patch`, `:reason`, `:before` engine-derived as `node edit`'s is; **the axis
layout changes only while every axis is empty, `:members` is empty and no cell exists**
(`layout populated`), a missing, `null`, unknown or duplicate key, a wrong type or an all-keep
request is refused at exit 2 as `node edit`'s is (`bad patch`; `all keep`), a row-kind change
requires every retained row to match (`row kind mismatch`), and an aggregation change changes how a row qualifies green and never how many rows
apply. `roadmap row --add|--remove` — `:verb`, `:roadmap`, `:member`, `:reason` — is admitted on an
axisless roadmap only (`has axes`, naming `axis`), its member an existing node of the declared
`:row-kind`; add appends to `:members`, remove retires the row from the view alone and touches
no containment, state, evidence, lease or repository. `roadmap projection --add|--remove` — `:verb`,
`:projection`, `:reason` and `:verb`, `:projection-id`, `:reason` — own the targets; only
`:markdown-table` is a policy; a second projection under a held id with a different payload
refuses `duplicate projection`, a remove of an id the roadmap has not got refuses `no such
projection`, and a matrix selection naming an unknown, missing or duplicate axis or member
refuses `bad selection`. **`axis --remove <member>` exists, on any declared axis, and
amends the two sentences that said nothing takes a member off an axis**: it removes the member
from its ordered axis and from every current cell coordinate holding it, records the position
and the removed cells as its `:before`, and **on the first axis retires the row from the
view's required set while the row is live** (done included: only removed, cancelled and
superseded are not live); on another axis it moves no set. It never calls `node remove`,
`cancel` or any terminal verb; the node, its evidence and its state stand. An absent member
refuses. **Three scope kinds carry these, each after its structure event and each a scope
revision of the roadmap**: `:view` — `:change` (`:create`, `:configure`, `:projection-add` or
`:projection-remove`), `:reason` — for an effective create, configure or projection change; `:roadmap-row` — `:change`, `:member`, `:reason`; `:axis-remove` —
`:axis`, `:member`, `:reason`; the last two move the roadmap's view-required set and never a
containment parent's. A container settle or revive that a membership change implies follows the
ordinary cascade — an empty required set is still not done — and reopens or cancels no row;
metadata and projection changes may address a settled roadmap without reviving it. An equal-value configure and an identical projection re-add are the no-effect receipt,
no scope event.
**`percent --node R` on a zero- or one-axis roadmap takes no `--axis`**, its applicable rows
being its live ordered rows and green by its aggregation, while a matrix still requires one and
a non-matrix given one refuses at exit 2, which amends the grammar's *required*. Undo of
configure, row, projection and axis removal compares the current postimage, cell and member
order and scope revision with the stored `:after` and appends a typed compensation or refuses
conflict; undo of a create is admitted only with no surviving member, cell or projection
outside its preimage. A settled roadmap keeps its whole current head — axes, members, cells,
configuration, projections and receipt identities — and opens from it plus bounded closed
reads. `ROADMAP OK … change=<create|configure|row-add|row-remove|projection-add|projection-remove>
changed=<n>`; `AXIS OK … change=<add|remove>` (replays `axisless-history`, `matrix-retirement`,
`configure-no-effect-and-undo-conflict`, `completed-view-mutation`, and the existing
`chat-and-file-render-are-byte-identical` and `render-refuses-a-target-outside-its-roots`).

**`render` reads the stored projection and no longer a remembered command line** *(Rowan's
decision, for review: the `--into --start --end` spelling is retired, because the stored target
was already the requirement and those flags were the gap the missing-verb list named)*.
`render --view <id> --chat [--projection <id> | --row-axis <id> --column-axis <id> --fixed
<axis-id>=<member-id> ...] [--at <revision>]` needs no filesystem mapping: with a projection it
reads that projection's display selection and not its file, without one a matrix names its
selection and a zero- or one-axis view needs none, and the two forms are exclusive. `render
--view <id> --projection <id> (--file | --check) [--at <revision>]` reads the stored target,
resolves its root through the session's mapping, captures the file's SHA-256 and marker
offsets, and replaces its region atomically after a hash recheck, `--check` writing nothing;
the receipt records the target identity, the old and new hashes and the render revision;
missing, duplicate or reversed markers, a path outside the effective root, a symlink escape, a
target whose hash moved, or **a mapping whose repository identity is not the projection's stored
`:repo`** (`target identity`: remapping a root never redirects a projection to another
repository) refuse and leave the file untouched. **A stored root id grants no
access**: `session start --render-root <root-id>=<owner/name>:<directory>` maps it to a bench
path, file mode needs both the stored permission and that mapping, and no mutation grants
filesystem access. The per-target renderer lock is advisory among cooperating renderers; an
external editor can still race the final replace, and this document claims no more than that.
**One wire exception, for review**: a successful `--chat` render carries no `lines` and exactly
one bounded `artifact` object beside the response's request and revision fields,
`{"encoding":"utf8","body":"…","sha256":"<hex>","bytes":"<n>"}`; the client verifies the count
and the hash and writes `body` to stdout unchanged with no `RENDER OK` prefix, because a prefix
cannot be raw identical Markdown; a refusal is ordinary stderr `lines`; an artifact past the
frame or output bound refuses and is never truncated; file and check keep their status lines
(replays `render-artifact-is-bounded`, `a-root-id-grants-nothing`).

**`prioritise` is a noun and a verb, and it steers ordering and nothing else.** Every node
carries one fixed field, `:priority (:self <rank|absent> :subtree <rank|absent>)`, a rank an
unsigned integer atom of at most eighteen digits — checked before conversion, so a bignum
runtime allocates nothing for a longer one — absent written `(:absent)`, and on the wire a
decimal string as every integer is; **rank 2 precedes 10**. There is no stored default; the two
slots are the only canonical data and effective rank, ordering and latest-change lookup are
derived. **A node snapshot without the field is the pre-fold shape and is refused `schema
revision unsupported` at load, as the shorter `node add` order is**; no draft data is reread as
a priority. `prioritise --node <id> (--set <rank> | --clear) [--context <self|subtree>] --reason`,
context defaulting to `self`, `--clear` with a rank refused at exit 2, addresses O; a settled
node keeps its slots into C and a reopen restores them. Its event kind is **`:prioritise`** —
`:change` (`:set` or `:clear`), `:context`, `:rank`, `:reason` — **neither structure nor
scope**: it moves no containment, dependency, acceptance, state, generation, baseline, required
set, O or C membership, W, lease, capacity, count or percentage, and never reorders baseline or
discovery rows or roadmap rows, which the scope paragraph above already forbids. Its `:before
(:value V :change <event-id|absent>)` and `:after (:value V :change <this-id>)` are the
engine's, outside the digest. **Effective rank is the nearest context**: the node's own `:self`,
else the deepest `:subtree` on its containment path, else the default; a clear reveals the
next; a move re-reads the new path and clones no event. **`query --ask ready --order
<discovery|priority>`** is the one reader, `discovery` the default and `--order priority`
refused at exit 2 on every other ask; it sorts only rows `ready` already admits by its
predicates — **it grants no capacity, bypasses no approval, takes no lease, changes no
responsibility, selects no worker and starts or interrupts no work** — explicit ranks
ascending, then defaults, then bytewise stable id, independent of clocks and arrival; blocked
rows are not dropped: they follow the eligible rows in discovery order with their blocker and
resolver fields, under the ordinary cursor. A ready row prints `priority=<rank|default>
priority-source=<id|default> priority-context=<self|subtree|default>`. The derived order index
is keyed by captured revision, scope, filter and readiness watermark; a first unseen filter
costs `O(k log k)` in its k eligible candidates and later pages read the pinned order; no
descendant carries a copied rank and no second priority table exists; a cursor names its
revision and watermark, and eligibility is never reused across a watermark because ranks stayed
fixed. A same-value set or a clear of an absent slot is the no-effect receipt, `changed=0`, its
before and after identical. **Undo restores the preimage only while the slot's latest-change
identity equals this event's `:after`**: set 2, set 9, set 2 refuses undo of the first, because
the same value is not the same history. `PRIORITY OK … change=<set|clear> context=<self|subtree>
rank=<n|-> changed=<n>` *(Rowan's decision, for review: the draft named no line)* (replays
`priority-orders-only-the-eligible`, `priority-inherits-and-clears`, `rank-2-precedes-10`,
`priority-undo-is-history-not-value`, `priority-grants-nothing`).

**`session export --state --at <revision>` is the export of a captured revision that
`full-round-trip` asks for, and it is a second product, not a change to the request bundle.**
`session export (--session <path> | --snapshot <path> --cache <path>) --state --at <revision>
--closed-history <none|all|range> [--from <stamp> --to <stamp>] --into <new-directory>
--max-bytes <n> --max-depth <n> --max-nodes <n> --max-output-bytes <n>`: `--state` is required
with `--at` and the request export refuses `--at`; `range` requires both stamps and the other
two refuse them; the range is half-open, `[from, to)`, in the query stamp grammar, so adjacent
ranges do not overlap. **The resident form is one long operation**, by *The engine and its
client*: the request is acknowledged at once with `OPERATION OK id=<id> op=export
state=<queued|running>`, the terminal `EXPORT OK` or `EXPORT FAIL` is what `operation wait`
prints, and the wire op is `session.export`; it captures and pins the revision under the owning
engine, stages the traversal and the output I/O outside the mutation loop, takes no ownership
and writes no canonical event or index; an atomic batch refuses it by entry id (B4 above); a
fenced session runs it and reaches its terminal line through the `operation status|wait|cancel`
that rule 2 above already admits for its own export id and nothing else — that amendment landed
from #293 ahead of this fold and is cited, not re-made. `--at` is one accepted local
revision, resolved to a savepoint plus an exact journal prefix and pinned until the operation
ends, so a concurrent clip or retention pass reclaims nothing under it; a revision outside
recoverable retention refuses naming the revision and the member, never approximated by a later
snapshot. The `--snapshot` form is the offline counterpart: `--at` must equal that snapshot's
captured revision, it reads the supplied verified closure and cache and no session or
repository, runs as one finite process, and prints the same terminal line with `operation=-`.
Cancellation acknowledges, then reconciles whether publication happened, and never promises to
unpublish; a disconnect cancels nothing; recovery resolves the operation's id, captured sources
and output identity before any retry. **Open state at the capture is always primary; closed
history is what `--closed-history` selects, and the manifest declares what it left out, so no
scoped artifact claims to be a full backup.**

The output directory is immutable and publishes `MANIFEST.sexp` in the restricted
S-expression format — JSON is the wire and never the store — whose v1 shape is:

```lisp
;; EXAMPLE DATA, NOT PRODUCT CONSTANTS: one captured-state manifest, values invented.
(:version "nova-work-state-export-v1"
 :kind :captured-state
 :captured (:revision 42
            :snapshot (:sha256 "<64-lower-hex>" :revision 40 :journal "<journal-id>"
                       :replay-cut (:sequence 25 :sha256 "<record-hash>"))
            :journal-end (:sequence 27 :sha256 "<record-hash>")
            :verification-cache "<64-lower-hex>")
 :schema (:id "work-v1" :sha256 "<64-lower-hex>")
 :scope (:open :all :closed-history (:range :from "2026-09-14T00:00:00Z" :to "2026-09-15T00:00:00Z"))
 :members ((:path "state/root.sexp" :kind :state-root :bytes 8123 :sha256 "<64-lower-hex>"))
 :omissions ((:kind :primary-closed-history :identity "2026-09-13" :reason :outside-selected-range)))
```

Integers are canonical unsigned decimal atoms, stamps RFC 3339 UTC ending `Z`, absent values
`(:absent)`, members sorted bytewise by path and the manifest not listing itself; the artifact's
identity is the hash of its exact manifest bytes. **The snapshot reference binds the base image
at revision B and the journal interval that reaches R**: B never exceeds R; `:journal-end
(:absent)` is legal only when B equals R and the image is the exact capture; when B is below R
the end is required and names one complete envelope by sequence and hash in the same logical
journal, R never splitting an envelope; the members cover the base image and every complete
record after the base cut through that end, clip markers included, as one unbroken sequence
and hash chain that physical rotation cannot alter; **load verifies that coverage and the
revision chain before exposing any state and applies that interval and never a later tail**;
a missing cut, a gap, a wrong end, a split envelope or an absent end below R refuses. Paths are
clean relative slash paths — no absolute path, dot segment, NUL, duplicate, case-fold alias,
symlink, special file or escape — each with an exact byte count and lower-case SHA-256.
**Closure is not selection**: whatever the primary selection, every canonical internal
dependency of a selected record is added — structure, scope and lease events, index and counter
provenance, roadmaps, evidence references, CONFIG and ACTIVE observations, model, rate and
accounting records, the schema bytes, and any record an internal id needs to resolve — and a
missing, corrupt or redacted mandatory closure member is an export refusal and never a
queryable, valid-looking empty load; omission entries name only unselected primary history,
rebuildable derived caches and declared external references. **A full export includes private
work**, the render audience filter not applying; stored non-secret CONFIG, profile,
resolver-command and cache-provenance text travels as inert data, literal empty and absent
kept apart; **credential values and secret-store content are excluded, and a record that
cannot be represented losslessly without redaction refuses rather than calling the artifact
full**; export and load read no path that text names, execute no profile and touch no
resolver, network or repository. Raw resolver observations and the captured verification-cache
identity are included where they are the only proof; a historical export uses the observations
of its captured revision and refuses with a named proof gap rather than substituting current
ones — a cache that recorded unknown is valid, a fact once recorded and now missing is not.
Reads stream under `--max-bytes`, the reader checking depth and node bounds before it
descends; `--max-output-bytes` is an exact running sum of member and manifest bytes; any bound
breach, changed identity, malformed data or cycle refuses, and no truncation marker ever
stands for a member. **Publication is no-replace**: an owned private staging sibling, members
created exclusively, hashed and counted during the copy, synced, the manifest written and
synced last, then one no-replace directory commit into `--into` and a sync of its parent, and
success reported only after that barrier; an existing destination refuses; a filesystem that
cannot promise the commit refuses rather than pretending check-then-rename is CAS; a crash
before the commit leaves owned staging that recovery may inspect or clean by its nonce and
never auto-publishes.

**`state load --from <export-directory> --into <new-readonly-session> --max-bytes <n>
--max-depth <n> --max-nodes <n>` is the isolated read-only load**, a finite process and never a
request to a session: it verifies the manifest version, every count and digest, the member set,
the closure and the schema under its bounds, materialises one exclusively created directory in
the existing snapshot and cache schemas, prints `LOAD OK` naming the captured revision, the
manifest hash and the two paths — *(Rowan's decision, for review: the draft's `STATE` token is
the `state` verb's, so this verb prints `LOAD`)* — and exits, starting no daemon, keeping no
socket, creating no writable journal and no `OWNER`; an incomplete load is staging and never a
success. The loaded snapshot is read by the existing `query --snapshot <path> --cache <path>`,
and the export's own `--snapshot` source re-exports it through a fresh reader that rebuilds the
model rather than copying an unchecked archive; neither starts a coordinator, and no
mutation, replay, clip, handoff or execution verb accepts it as a `--session`. Live `--session`
reads still reload nothing; only an explicit offline read pays a parse. Savepoint meanings are
unchanged (replays `state-export-describes-exactly-r`, `state-export-is-one-long-operation`,
`state-export-pin-survives-clip`, `state-export-disconnect-and-cancel`,
`state-export-refuses-a-gap`, `state-load-is-isolated`, `fenced-export-can-finish`, and the
existing `full-round-trip` and `old-history`).

**What #293 left open is listed once, with its owners, in *What this draft does not do*
below, and this fold closes none of it.**

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
persist in the savepoint and in the clip's checkpoint alike; time-sensitive availability is revalidated on recovery. A `friends`
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
and one-shots are execution instances** (replay `four-capability-groups-and-three-fields`) linked to their friend, capability, canonical task and
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
configured silence threshold — `--silence-ping <duration>` on `session start`, in the grammar
above, **a team's configuration and not a number this tool believes in** *(Rowan's decision, for
review)* — triggers **one bounded availability ping** through the existing
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

**Swarms are model-only** *(decided by Glenn, 2026-09-15, his words "for swarms only")*: a friend
is never a pool worker; a friend's children are the friend's own and are not swarm members; the
child capability is unchanged by this. A swarm is an execution capability holding jobs and **is
neither a friend nor a model**; a swarm labelled with a friend's name does not make its workers
that friend, carry that friend's continuity or speak for them; a model-only worker needs no
invented friend identity; and the friend responsible for a pool and the actor executing a job are
two references that are never double-counted. **Every worker carries a model or route label, no
historical actor is renamed and every past source label and attribution is preserved.** A friend's
own disposition remains the only record of that friend's consent, and a worker's reply or a silence
is not it. There is no participation record for pools and no door into one: `friend
--participation` and the `:participation` change are reserved — they stay the spelling of the
agreed participation of *A role is configured* above and are not a door into a pool.

## The fleet *(Rowan, on Glenn's word of 2026-09-15; a draft for Stella's review as the section's owner)*

**Glenn's word, 2026-09-15 01:05Z to 01:07Z, verbatim, is the whole of the requirement**: "A fleet
is the set of machines that we have available to work on, including this studio, ssh space, and now
the mac mini. I will add more as machines come online. These machines are places where we can build
code, run tests, and do profiling. This is a new concept." / "Please tell Stella that I would like us
to add the 'fleet' to the nova-work config." / "So we can configure it within nova work, and query
it." Stella bounds the scope (stella-1858e1eeef8d): **inside the nova-work config, a static
description of the test machines available for building and profiling**; her dynamic-resource
design (stella-42885d237271) is not part of the request; machine metadata is instance data. This
section is that and nothing more. So: the fleet is a set of **member records**; it is **configured**
by one verb; it is **queried** by two asks; a machine comes online by a person adding its record,
which is how Glenn said he will add more.

**A member is a record of the desired half — configuration, never work — `:kind :machine`, and
every field of it is instance data.** It lives in a `fleet` section of CONFIG beside `friends` and
`models`, under the one writer, the one journal, the clip and the three bounds like everything else
there; it is **no child of O and under no repository work set**, so no count, roadmap or required
set moves when one is written, and *The root is COW* is untouched by it. It is mostly constant and
changes only on a meaningful configuration change — never on a heartbeat, a probe or a load sample —
which is what makes it CONFIG and not ACTIVE. **Nothing in nova-work names a machine**: no hostname,
alias, user, path, architecture or core count is a constant in the product, the way no friend name
is (*Friends, CONFIG and ACTIVE* above; Johnny, johnny-5b879930aae8: *a tool that names a host in
source has already failed the test*). Every value in this section is a team's own configuration and
**the three records below are EXAMPLE DATA, NOT PRODUCT CONSTANTS**. The fields, each of them
Stella's (stella-42885d237271) or Johnny's (johnny-5b879930aae8) and none of them the tool's:

- `:id` — the **stable machine id**: never reused, never display text, never a locator. One physical
  host reached through two aliases is **one record and one unit**, never two.
- `:name` — display only; may change freely.
- `:owner` — a friend of `friends`, **required**: the person whose word admits a workload there.
- `:connect` — a **connection-profile reference** the team's own store resolves, of the form
  `"profile:<name>"`; **never a credential**: no key, token, password or secret is ever in a record,
  a manifest or a clip, by the CONFIG rule above.
- `:roles` — the **intended roles**, each one of `:build`, `:test` and `:profile` (Glenn's three:
  *build code, run tests, and do profiling*); an unknown role is a refusal, never a guess.
- `:permits` and `:excludes` — **workload kinds** as a team's own labels (`"go-test"`,
  `"bench:schema"`): the kinds admitted and the kinds that **must never run there**. `:excludes`
  wins wherever the two name one kind.
- `:limits` — declared **concurrency and resource limits**: `:concurrent <n>` and whatever resources
  a team names (`:cores`, `:memory-gb`); **declared by the owner and never derived** from a friend's
  child ceiling, a sitting or a probe (Johnny: *do not offer unused child ceiling as capacity*; *a
  sitting is not a fleet slot*).
- `:facts` — **hardware and OS facts with provenance**: the facts, then `:declared-by <name>` and
  `:declared-at <stamp>`, so a fact is a dated declaration and **a stale declaration is never read as
  a current probe**.

```lisp
;; EXAMPLE DATA, NOT PRODUCT CONSTANTS: three example members of a `fleet` section.
(:kind :machine :id "m-a1" :name "studio" :owner "glenn"
 :connect "profile:studio"
 :roles (:build :test)
 :permits ("go-test" "cpp-test" "swarm-cards")
 :excludes ("bench:schema")                    ; loud bench; never a profile run here
 :limits (:concurrent 4 :cores 16 :memory-gb 128)
 :facts (:arch "arm64" :os "macos" :cores 24 :memory-gb 192
         :declared-by "glenn" :declared-at "2026-09-15T01:05:00Z"))
(:kind :machine :id "m-b2" :name "profiling host" :owner "glenn"
 :connect "profile:space"
 :roles (:build :test :profile)
 :permits ("go-test" "cpp-test" "bench:schema")
 :excludes ("swarm-cards")                     ; profiling wants a quiet box
 :limits (:concurrent 1 :isolated-cores (2 3))
 :facts (:arch "x86_64" :os "linux" :cores 8 :memory-gb 125
         :declared-by "glenn" :declared-at "2026-09-15T01:05:00Z"))
(:kind :machine :id "m-c3" :name "mini" :owner "glenn"
 :connect "profile:mini"
 :roles (:build :test)
 :permits ("go-test")
 :excludes ("bench:schema")
 :limits (:concurrent 2)
 :facts (:arch "arm64" :os "macos"
         :declared-by "rowan" :declared-at "2026-09-15T01:06:00Z"))
```

**One verb configures it** *(Rowan's decision, for review; the spelling mirrors `friend`)*:
`nova-work machine --session <path> <write flags> (--register <id> --name <text> --owner <name>
--connect <ref> --role <build|test|profile> ... | --retire <id> | --permit <id>=<kind> | --exclude
<id>=<kind> | --limit <id> <key>=<n|n,n,...> | --fact <id> <key>=<value> --declared-by <name>) --reason
<text>`, in the grammar block above. It writes one `:machine` event — `:change` (`:register`,
`:retire`, `:permit`, `:exclude`, `:limit` or `:fact`), `:machine`, `:name`, `:owner`, `:connect`,
`:roles`, `:workload`, `:key`, `:value`, `:declared-by`, `:reason`, in that order for the payload
digest — whose subject is a machine identity and not a node: `:node` is `(:absent)` and `MACHINE
OK` prints `machine=<id>`. A retired record stays in the journal; it answers no query but the
history. **The verb refuses, exit 1, `MACHINE FAIL machine=<id>: <reason>`, nothing written**:
a record with **no `:owner`** or **no stable `:id`** (`no owner`; `no id`, printed `machine=-`); an
`:owner` who is not a friend of `friends` (`unknown owner`); a `--register` whose `--connect` is
**the connection profile of a member already in the fleet** (`connect held by <id>`: one profile is
one unit — and because the tool sees a profile name and never resolves it, **two profiles that reach
one host is the owner's duty to refuse and not a check this tool can make**, so the one-host-one-unit
rule above is the owner's word, enforced here only as far as the profile); a **credential in a record** — a `--connect` that is not a `profile:` reference, or any
field named `key`, `token`, `password` or `secret` (`credential in record`; the record is refused
whole and the value is not echoed); a role outside the three (`unknown role`); a `--fact` with no
`--declared-by` (`fact without provenance`). A `--limit` value is one integer or a comma-separated
list of them (`isolated-cores=2,3` writes `:isolated-cores (2 3)`).

**Two asks query it, and both are in the `--ask` list and the grammar** *(Rowan's decision, for
review)*. `query --ask fleet` **lists the fleet**: every live member, one `QUERY ROW` per machine
with its owner, roles, limits and declared facts and their dates, capped and counted like every
listing. `query --ask fleet --for <workload-kind>` answers **which machines admit this workload**:
the members whose declared `:roles` and `:permits` admit the kind and whose `:excludes` do not, in
their configured order, each row carrying the declared facts and their dates so the asker can see
how old the declaration is. **The answer is a recommendation from declared facts and never a
lease** (Johnny: *the registry is facts; picking a machine is a recommendation; dispatch still needs
a lease*): it reserves nothing, dispatches nothing, probes nothing and proves nothing about the
machine now; what execution needs is *The lease* above and an attempt whose `--bench` may carry the
machine id, so a build, test or profile result retains the machine it ran on (Stella). `--node
<machine-id>` narrows either ask to one member, and **an ask asked to choose that member for a
workload it excludes is refused**, `QUERY FAIL ask=fleet rows=0 shown=0: <id> excludes <kind>`, exit
1, never an empty answer a caller could read as *no machine* and fall through — the exclusion is the
owner's word and the answer says so. The rows:

```
QUERY ROW <machine-id> kind=machine name=<text> owner=<name> roles=<build,test,profile> admits=<kind|-> concurrent=<n|-> arch=<text|-> os=<text|-> declared-by=<name> declared-at=<stamp>   (fleet; admits= is the --for kind where given, and every row printed under --for admits it)
QUERY FAIL ask=fleet rows=0 shown=0: <id> excludes <kind>   (--for with --node naming a member that excludes the kind: a refusal, never an empty answer)
```

**What this section does not do, and must not.** It probes no machine, reads no load, tests no
reachability, discovers no toolchain, schedules nothing, dispatches nothing, leases nothing and runs
nothing: **a configured member is not authority to run anything there** (Stella), and enrolling a
host, its accounts and its keys stays a person's hand outside this tool. Selection under live
capacity, observed availability with its stamps and sources, oversubscription across coordinators
and a child node exposing delegated resources are Stella's dynamic design (stella-42885d237271),
recorded for a later revision and **not requested**; ownership of execution is the existing lease
contract; results are the existing attempt records. **A sitting — an interactive session a friend
holds on a machine — is not a fleet slot and is never counted as one** (Johnny). Replays:
`fleet-is-static-config` (a `:machine` event moves no count and no roadmap; a heartbeat, an
`observe` and a probe change no member), `no-machine-name-in-the-tool`, `one-profile-one-unit`,
`no-credential-in-a-member`, `unknown-owner-is-refused`, `fleet-for-is-a-recommendation-not-a-lease` (the ask writes no lease
and leaves `who` unchanged), `an-excluded-choice-is-refused-not-empty`.

## Fleet allocation *(Rowan and Stella, on the machine record of *The fleet* and the ACTIVE data of *Friends, CONFIG and ACTIVE*; #500)*

**This amendment is the later revision Stella's dynamic design (stella-42885d237271) was recorded
for, and it is a draft with no code yet.** Every rule here carries **SPEC-AHEAD: #500** on its
first line; nothing already numbered is renumbered or rewritten — the validator's rules 1 to 18,
the efficiency rules 1 to 10 and the duty-tier rules 1 to 6 stand — and each rule's replay is
appended at the end of the list in *Acceptance replays* below. *The fleet* stays the desired half —
configuration, never work — and this section adds the ACTIVE half: **who holds a machine's slots
now**. Allocation is execution data, and execution needs a lease: `query --ask fleet` stays a
recommendation from declared facts and never a lease, an allocation is what makes it one, and the
allocation lives in ACTIVE, never in CONFIG. The line shapes the rules name are this amendment's
additions to *Output grammar* — `ALLOC` and `PROBE` join the first-token list — and are proposals
under #500 like every rule here.

1. SPEC-AHEAD: #500
   **A machine is a CONFIG member with declared facts, and equipment never completes.** A machine
   is the `:kind :machine` member record of the `fleet` section of CONFIG that *The fleet* defines,
   changed only on a meaningful configuration change and never by a heartbeat, a probe or a load
   sample: its `:name` is display only; its **ssh host** is the `:connect` `profile:<name>`
   reference, which **never carries a credential** and whose **auth path is never read** — the tool
   sees the profile name and resolves nothing, by the one-profile-one-unit and
   no-credential-in-a-member rules; its **usable cores** are the declared `:limits` (`:cores <n>`,
   `:concurrent <n>`), **declared by the owner and never derived** from a probe, a load sample or a
   child ceiling; its **core-pin rule** is a declared limit of its own (`:isolated-cores (2 3)`);
   its **harness path** and its **root** are declared `:facts` with `:declared-by` and
   `:declared-at`, so a stale declaration is never read as a current probe. **A machine is equipment
   and never a work-tree node**: it is no child of O and under no repository work set, so no count,
   roadmap or required set moves when one is written; it has no `:acceptance`, no derived state, no
   `:to :done` and no settle, and every `:machine` event writes `:node (:absent)` by the kind's
   own subject rule; **equipment does not complete, so nothing a machine does is completion
   evidence**. Replay: `machine-is-config-and-never-a-work-tree-node`.

2. SPEC-AHEAD: #500
   **An ACTIVE allocation binds machine, slot and generation to a batch, a node and an (offer,
   attempt).** An allocation is **ACTIVE data per machine** — variable operational data, never
   called configuration — that references the machine's CONFIG identity and revision rather than
   copying a definition into every record, exactly as ACTIVE references CONFIG's stable identity
   and revision above. One allocation binds **machine** (`<machine-id>`), **slot** (one unit of
   the machine's declared `:concurrent`) and an **allocation generation** (a token the allocator
   drew at creation) to a **batch** (the `--request` id of the envelope), a **node** (the
   task node the work belongs to) and an **(offer, attempt)** — the assignment pair the offer
   section pins and the reservation key the offer already writes, so the allocation and the
   reservation are one fact. **Admission happens before preparation**: the physical slot is
   secured and the ACTIVE allocation is written before any clone, scp or preparation work begins,
   and the pre-existing friend/profile reservation binds in the same step without a second debit.
   **Both capacity constraints validate atomically** — the friend's declared reservation capacity
   and the machine's declared slot capacity are checked in one predicate, and a single
   binding-and-accounting transition converts the offer's reservation to committed capacity while
   writing the ACTIVE allocation, never two debits. **Capacity is retained through verified
   release**: the slot is not freed until a release confirms termination — a verified stop
   observation, a `not-started` rejection, or machine-side fencing. **Core affinity is a separate
   resource constraint**: the core-pin rule is a declared CONFIG `:limits` constraint on which
   slot may serve which workload, never part of the allocation's identity and never changed by a
   take. Replay: `allocation-binds-machine-slot-generation`.

3. SPEC-AHEAD: #500
   **`take` is atomic and idempotent under a stable request identity, and returns all requested
   capacity or a bounded refusal.** Two generations are distinct: the **machine generation** is the
   `:generation` field of the machine's CONFIG member, bumped on every meaningful configuration
   change; the **allocation generation** is a token the allocator drew when creating the allocation,
   recorded in the ACTIVE allocation's own `:allocation-generation` field. `--generation` takes the
   machine generation and is compared to the machine's current CONFIG generation. Each allocation
   carries its own unique **allocation id** (`<allocation-id>`), returned as `id=` on the `OK` line
   and used by `heartbeat` and `release` to name exactly one allocation.
   `nova-work take --session <path> <write flags> --machine <id> --node <id> --slots <n> --offer
   <offer-id> --attempt <attempt-id> --generation <n> --request-ref <opaque-id> --batch <id>`
   writes one ACTIVE allocation in one envelope under the one writer: all-or-none, one journal
   record, one `OK` line, one request id — a retry of that id is answered with the original line
   and applies nothing, by the two-part dedup test, and a changed payload under the id is refused
   `reused with a different payload`. **It grants every requested slot or none**: any slot it
   cannot grant — a held slot, a declared `:limits` or `:concurrent` passed, an `:excludes` kind,
   a stale machine generation — is a bounded refusal, exit 1, `ALLOC FAIL machine=<id> slots=<n|->
   holder=<name|->: <reason>`, naming the machine and the holder, **never a partial grant**.
   `nova-work heartbeat --session <path> <write flags> --allocation <id> --generation <n>` names
   exactly one allocation by its allocation id and supplies the machine generation; it validates
   both the allocation generation and the machine generation against the current ACTIVE and CONFIG
   records, and refuses `ALLOC FAIL machine=<id> allocation=<id>: stale token` if either does not
   match. `nova-work release --session <path> <write flags> --allocation <id> --generation <n>`
   names exactly one allocation by its allocation id, validates both generations the same way, and
   frees exactly that allocation's slot, printing `ALLOC RELEASE OK` and applying no change to any
   other allocation on the same machine; `release` is the holder's act, or a `--handed` act from
   that holder, and never a third name reaching in. **`list --machine <id>` shows the holders and
   the ages**: one `ALLOC ROW` per live allocation with its allocation id, holder, node, batch,
   (offer, attempt) and `age=<duration>`. Replay: `allocation-take-is-atomic-and-idempotent`.

4. SPEC-AHEAD: #500
   **Expiry marks an allocation suspect and blocks renewal or start with the stale token; reuse
   requires confirmed termination or machine-side fencing, never expiry alone.** An allocation's
   expiry is derived, as a lease's is, and it **marks the allocation suspect** — retained, counted,
   never cleared by the expiry that prompted the question, by the W4 rule that an expiry is not
   proof the remote work stopped. A renewal and a fresh `take` using the stale token are **refused
   while the allocation is suspect**: `ALLOC FAIL machine=<id>: suspect since=<stamp>`, so the
   stale token renews nothing and starts nothing. **Capacity returns only on confirmed process
   termination — a verified stop observation, a `not-started` bound to the exact launch authority's
   durable rejection, or machine-side fencing — and never on expiry alone**, exactly as the
   reconcile section admits stop evidence and as silence, an expired lease and an elapsed estimate
   are not stop evidence; until then the refusal is `ALLOC FAIL machine=<id>: not fenced`. Replay:
   `expiry-marks-suspect-reuse-needs-fencing`.

5. SPEC-AHEAD: #500
   **Probes produce dated observed ACTIVE evidence and never silently overwrite declared CONFIG
   facts; no credentials and no guessed capacity.** A probe is an observation and writes **ACTIVE
   evidence with its date, its source and its last-contact stamp** —
   `PROBE OK machine=<id> slot=<n|-> fact=<observed|absent> at=<stamp> source=<pointer>` — and it
   **never writes CONFIG**: a heartbeat, a probe or a load sample changes no machine member, no
   `:limits`, no `:facts`, no `:connect` and no `:roles`, and *declared support, verified runtime
   and current free capacity stay three fields*; a stale declaration is never read as a current
   probe. **A probe carries no credential**: the profile is never resolved, and no key, token,
   password or secret is in a record, a manifest or a clip. **It never guesses capacity**: what a
   probe observes is observed — `fact=absent` for what it could not establish — and declared
   capacity is the owner's declared number, never a probe's reading offered as one, by *capacity is
   declared and never guessed from a catalog or an old heartbeat*. Replay:
   `probe-records-observed-active-and-touches-no-config`.

6. SPEC-AHEAD: #500
   **One authoritative allocator per physical machine, shared by every session and controller;
   aliases share identity; nested quotas conserve capacity; capacity reduction preserves active
   work.** Allocations are admitted by **one authoritative allocator per physical machine**, under
   the one writer and the fencing rules like every other mutation, so **every session and every
   controller on the bench shares that allocator and reads one allocation set**; a second allocator
   for one machine is refused like a second live writer, exit 1, `ALLOC FAIL machine=<id>:
   allocator held`. **Machine aliases share identity**: one physical host reached through two
   aliases is one record and one unit, by the fleet section's own rule, so an allocation taken
   through one alias reads as the same allocation under the other and is never double-counted.
   **Nested quotas conserve capacity**: an allocation nested under another allocation's scope draws
   from the same declared capacity and is never an additional grant — nested delegated executions
   keep their parent lineage without counting one slot twice, by the nested-execution rule of
   *Friends, CONFIG and ACTIVE*, and a nested allocation lists once per slot. **Slots come from the
   declared concurrency, not from cores: cores and affinity are a separate constraint.** For a
   machine with `:cores 16` and `:concurrent 2`, two allocations each occupying one slot fill the
   concurrency and a third `take --slots 1` is refused `ALLOC FAIL machine=<id> slots=1 holder=<n>:
   capacity` at exit 1, even though fourteen cores sit idle; core-affinity limits are validated
   independently after the slot check. **On capacity reduction** (a declared `:concurrent` or
   `:cores` lowered by a machine edit): **preserve active allocations** — every live allocation is
   retained and never silently cancelled; **drain and refuse new admission** — no new `take` is
   admitted once the reduced capacity is declared, and pending offers for that machine are held;
   **do not silently cancel work** — running preparations and live attempts continue until their
   own verified release or fencing, and no reconciliation frees a slot without a confirmed
   termination. Replay: `one-allocator-per-machine-aliases-share-nested-conserve`.

## Assignment and execution control *(Stella's draft, nova-tools #294 at `4fddfcb2`, folded; Root and Terra's corrections taken as she took them; the spellings are this file's)*

**The six verbs the missing-verb register named, and two beside them, have their contracts here
and their spellings in *The verbs*: `offer`, `acknowledge` and `decline` for an assignment, and
`execution pause`, `stop`, `resume`, `correct` and `reconcile` for work already distributed.** They add no second
scheduler, no provider adapter, no bus and no protocol lock: every one of them is a mutation verb
under the one writer, the one journal, `<write flags>` and the dedup predicate of *Retention*,
and every external effect it starts is a long operation of *The engine and its client* with its
own durable id. What this section settles is which fact each verb writes and which it must never
infer.

**Dispatch, delivery, acknowledgement and accepted ownership are four facts, and the four verbs
keep them apart** (*Friends, CONFIG and ACTIVE* above). `offer` writes dispatch — the
coordinator's intent to send a named offer — and nothing else: no node state, no task evidence,
no `:attempt`, no lease and no W. `acknowledge --stage received` writes delivery, a verified
report that the named recipient received that exact offer, and consents to nothing. `acknowledge
--stage accepted` writes accepted ownership, the coordinator's admission of a verified acceptance
as an assignment to a lease holder; it changes no `:responsible` and proves nothing about whether
remote work began. `decline` writes a verified refusal. None of the four is inferred from
another, and none of them launches anything (replay `four-facts-four-verbs`).

**A receipt is a verified observation and never a request's word.** A bus note, a launcher
callback and a copied JSON body are provenance data; `--as` names the request's author and
authenticates nobody, as on every verb. The coordinator admits `acknowledge` and `decline` only
after an **operator-configured verifier** has returned the configured recipient identity, a
stable receipt id and the digest of the received bytes, and the three fields it derives —
`:sender`, `:receipt-digest`, `:effect` — are the session's own half of the envelope, refused
when a plain request carries them. **The verifier, the provenance body and the offered payload
are staged inputs**: their readers run outside the mutation loop, produce immutable staged bytes
and a validation result, and only then does the one writer revalidate `--expect`, the offer's
immutable tuple, the profile and the capacity before admitting one envelope — *Slow I/O stages
its inputs and results outside the mutation loop* applied here and not a second rule. A stale or
failed stage writes no reservation, no lease, no W entry and no receipt, and `status` answers
while the stage runs. A verifier outage is `ACKNOWLEDGE FAIL … : provenance unverified`, canonical
state unchanged, and never a bus body promoted to authority (replays `a-receipt-needs-a-verifier`,
`staged-admission-refuses`).

**An offer is an identity the recipient must echo whole.** `--offer <offer-id>` is drawn by the
requester and unique in the session — `--request` stays the mutation's idempotency key and is
not substituted for it — and the offer pins `--node`, the node's current `--generation`, an
`--attempt <attempt-id>`, the recipient `--to`, the execution profile `--profile
<capability-id>@<config-revision>` (an entry of that friend's CONFIG at that exact revision, one
of the four capability groups), `--request-ref` for the transport, `--payload <pointer>` with
its `--payload-sha256` — the exact bounded assignment body, instructions and any non-secret
launch plan, staged by its configured reader and retained by the writer before the offer is
acknowledged, so the dispatched bytes are the retained bytes — `--reserve <slots>`, required and
positive, **because capacity is declared and never guessed from a catalog or an old heartbeat**,
and `--until <stamp>`, the response deadline, which is neither the request's `--deadline` nor
the lease's. A replacement names `--predecessor-offer` and `--predecessor-attempt` and
overwrites neither lineage. An absent `--requested-model` is unknown, never CONFIG's usual model
(*requested-model-is-not-observed-model*). A receipt is admitted only against the same offer id,
node and generation, attempt id, payload digest and profile revision it was sent with. **`offer`
refuses, nothing written**: a malformed id, stamp or digest; a reused offer or attempt id; a
profile that is not that friend's at that revision, or under whose policy the requested model is
not admissible; a payload whose staged digest is not `--payload-sha256`; a reservation that is
not positive or that declared free capacity does not cover; a stale `--generation`; a
predecessor that is not this node's; an effective hold on the scope (below); and **a
cross-holder conflict** — a second offer or live attempt for one node is admitted only for the
same current or proposed lease holder with its own declared capacity, and an offer to another
name while a holder is pending or accepted is refused, because **an offer cannot create a shadow
lease** and reassignment is a reconciliation (replays `offer-writes-intent-and-a-reservation`,
`no-shadow-lease-across-holders`). Admitted, it writes `:effect :dispatched`, a pending-offer
entry in the assignment index under both the node and the friend, and a reservation keyed by
`(offer, attempt)`; the friend is not thereby willing or available (*willingness is never
inferred from configured capacity*).

**Acceptance creates exactly one lease or binds to the holder's own.** `acknowledge --stage
accepted` requires an earlier verified `:received` on the offer, a still-current generation, an
unresolved pending offer, and either no live lease on the node or a live lease held by that same
recipient; it carries `--by` and `--default` as **create-if-needed lease inputs**, so two
concurrent acceptances from one holder need no second payload once the first has created the
lease. Where no lease exists, the one accepted envelope writes `:effect :accepted`, converts the
reservation to **committed** execution capacity — an accounting fact, not an observation that
work runs — creates one canonical `:lease` with the supplied deadline and default, and W updates
from it once. Where the holder's lease exists, the envelope binds the assignment to it in the
assignment index and changes neither its deadline nor its default; `ACKNOWLEDGE OK` names the
lease that actually holds. The binding is the index's and not a field of the lease. **No
`:attempt` is written because a recipient accepted**: an execution's handle, observed model,
usage and outcome arrive by `observe --attempt` and `attempt`, independently, as the
requested-model rule already says (replay `accepted-creates-one-lease-or-binds`).

**A deadline releases nothing and a late reply revives nothing.** At `--until` an unanswered
offer is **overdue and unreconciled**: no new offer and no automatic launch is admitted for it,
the reservation stands, and any receipt that then arrives is retained as `:effect :late` with no
conversion and no release — only `execution reconcile` below chooses the next act. Lease expiry
takes the task out of W as *The lease* says and leaves the friend's ACTIVE capacity and any
uncertain execution retained (W4). A timely `decline` records `:declined` and releases only that
still-pending reservation; a decline after acceptance or over an uncertain execution is `:late`
and releases neither committed nor uncertain capacity. **Receipt id and digest are a second
uniqueness key beside the request id**: the same request replays to its recorded disposition;
the same verified receipt under a fresh request journals one no-effect `:duplicate` receipt and
consumes no capacity twice; a receipt after a decline, an acceptance, a generation change, an
expiry or a replacement is `:late`, linked to its lineage, and can create no lease, no launch, no
conversion and no release; **conflicting bytes for one receipt id are refused**. Silence is
still a question and not a failure (replays `until-is-overdue-not-released`,
`late-and-duplicate-receipts-are-retained`).

**A stop is a scheduling hold plus directives, and it is not a cancellation.** `execution pause`
installs a **durable scheduling hold** on its scope and stages a cooperative pause directive for
each captured assignment; `execution stop` installs the same hold and stages stop directives. The
scope is a typed selector, `(:node "<id>")`, `(:repo "<owner>/<name>")` or `(:all)`, never a
reserved node id, and it covers descendants later added or moved beneath it; **a held node cannot
be moved out from under its hold while a captured or uncertain execution remains** — moving it is
a reconciliation, not an escape. A worker that reports no pause or stop support is counted
`unsupported`, neither killed nor counted paused, and its hold and its uncertainty stand; a
target with no report is `unresolved`, and the two are never one count. Neither verb
cancels the task, marks it done, grants a permission or restarts anything, and a later offer is
its own admission. `session stop` ends the coordinator's session and `operation cancel` ends a
local long operation; **neither spelling is overloaded to mean this**. And the task's own
cancellation is untouched: **the request is `state --to cancel-requested` — or `goal update
--stop`, which is that edge — and the confirmation is `event --kind cancel --evidence`, the one
evidence-bearing operation, admitted from `:cancel-requested` alone**, exactly as *States and
transitions* and *The current goal* say; `execution stop` writes no transition, adds no edge to
the table and is not a second cancellation mechanism. What this section adds to that gate is
stated at the gate, in *States and transitions*: the `:cancel`'s evidence covers the attempt
set — `stopped` or `not-started` below, per attempt — so **one worker's stop note cannot cancel
a node with another live attempt**. A coordinator who wants a task cancelled and its workers
stopped writes two things, the request on the node and `execution stop --node`, and neither
implies the other **(Rowan's decision, for review)**. `goal show`'s `stop=` stays derived from
the state and no hold is read into it (replays
`stop-is-a-hold-not-a-cancel`, `one-stop-note-cannot-cancel-two-attempts`).

**The hold is durable before it is acknowledged, and the capture is anchored before it is
read.** Admission journals the hold and a recoverable **capture anchor** — the validated base
snapshot hash, the accepted journal boundary and the captured model revision — and only then
replies `EXECUTION OK`, so a crash can never leave an acknowledged pause with no hold or no
reproducible target set. Here C/O/W is the source's closed, open and working root of *The data*
and **not copy-on-write**: the in-memory pointer is no anchor. The capture then selects pending
offers and current or unresolved executions **through the existing node, repository and friend
indexes**, reads that immutable revision in pages bounded by `--page-bytes` and `--page-records`,
stores the ordered target manifest with its content hash, and stages one directive per target
with a stable identity — proportional to the selected assignments and no read of all of C, and
**no promise of an O(1) stop of arbitrarily many workers**. A lease expiry cannot remove a target
from the capture. **A clip may publish while a capture is live, and it carries the pin forward**:
the anchored snapshot object and the committed journal span stay retained until the manifest is
durable, and the capture never reconstructs from a newer scope; where the configured retention
cannot hold those exact references, admission refuses the control before acknowledging it rather
than waiting on I/O or weakening the capture, and the pin is released only after the manifest's
anchor and bytes verify (replays `hold-survives-a-crash`, `capture-survives-clip`).

**The dispatch barrier is checked at offer, at conversion and at the last send.** The single
writer that admits an offer, converts an acceptance to a lease and hands a directive to
transport revalidates every effective hold at each of those three points, so an offer prepared
before a hold cannot launch after it and no dispatch slips between capture and hold. **An
acceptance arriving under a hold is retained as `:effect :accepted-held`**, reservation intact:
no lease, no launch, no capacity release; lifting the hold does not convert it, and only
`execution reconcile` rechecks its generation, identity, capacity and the other holds. Work
already launched stays in the manifest; an in-flight send with an uncertain outcome stays a
target until reconciled. A directive carries the control id, the offer and attempt identity, the
node generation, the coordinator's fencing generation, the action and the content hash; a
transport retry resends that identity and never makes a new model job; a receiver refuses a
stale fence or a mismatched target with a bounded disposition. **Delivery, acknowledgement and an
observed pause or exit are three receipts**, a bus receipt proving delivery to the configured
transport and nothing further; imported prose mutates nothing. Pending sends and uncertain
outcomes survive clip and recovery so a successor reconciles before it resends; a fence stored
in a message is not proof that a provider enforces it (replays `no-dispatch-slips-past-a-hold`,
`held-acceptance-converts-nothing`).

**A control is a long operation and `EXECUTION OK` acknowledges intent, not a stopped fleet.**
Its transport is `op=execution` under *The engine and its client*: durable id before it is
printed, `operation wait`, `operation cancel` as a request with its own disposition, and
**refused by its own entry id inside an atomic batch** (B4) — an independent batch may carry it
with per-entry correlation. `execution status --control <id>` prints bounded counts — selected,
pending-delivery, acknowledged, confirmed, unsupported, unresolved — and one row per target
under `--max`; an observation that times out prints unresolved, completes nothing and launches
no replacement.

**An observation manifest is evidence, retained with provenance, and contradictions are kept.**
`execution reconcile --control <id> --from <manifest-id>` admits a bounded, content-addressed
manifest whose records bind control, offer and attempt id, node generation, source identity,
observed handle, observation time, an outcome in `running`, `paused`, `stopped`, `completed`,
`not-started`, `unsupported`, `unknown`, and result and usage references where present. A late
record about an earlier attempt attaches there and releases nothing of the newer one; a negative
process lookup counts only for its bound execution identity; **silence, an expired lease and an
elapsed estimate are not stop evidence**; contradictory observations are preserved unresolved,
never last-write-wins; missing usage stays unknown, **and a stop report never synthesises zero
cost**. `not-started` qualifies **only when the responsible launch authority durably rejects that
exact assignment identity from future launch** — a queue miss is not it — and it alone may
release an unlaunched reservation without inventing an attempt. Confirmed termination permits
capacity reconciliation but **bypasses no holder-only `release`**: a validated release by the
holder may join the envelope, and otherwise the lease reads held-not-worked to its deadline or
an authorised settlement; the coordinator never signs for a holder. It manufactures no verdict
and erases no result, usage, side effect or history; a completed execution still needs ordinary
evidence to make its task done; capacity held for an unknown execution is never advertised free;
W stays live leases and ACTIVE keeps the uncertain executions W no longer names (replay
`reconcile-preserves-contradiction`).

**Resume is two different acts under one verb.** `--action release-hold` removes only its named
control's hold, after the outcomes that control required have reconciled; overlapping holds stay
effective, and it neither resumes nor relaunches a remote process. `--action resume-workers`
stages an explicit resume directive for each confirmed-paused bound execution, refuses an
unsupported capability before sending, and **keeps the hold until a running observation at the
resumed boundary arrives** — a delivery receipt alone releases nothing; it starts no replacement
and lifts no other control's hold. An `unsupported` outcome closes the control's transport
operation `failed` and clears neither the hold nor the uncertainty (replay
`resume-is-two-actions`).

**`execution correct` is the correction that reaches workers, and the bare `correct` is not.**
It installs the node's hold, captures its old-generation executions, writes the node's own
`:correct` event and binds the new generation to immutable instruction bytes and their SHA-256,
all in one envelope under one request id, so a retry cannot bump the generation twice. The
instruction reference is data — no command, no access grant — and its configured reader
validates and bounds the bytes **outside** the mutation loop; the writer revalidates the
revision, the generation and the holds after the staged bytes return and retains those bytes,
and dispatch sends the retained body and never newer text at the reference; a missing,
mismatched or stale stage refuses whole. Each capable worker receives the old and new
generation, its offer and attempt, and the instruction hash, and acknowledges the boundary it
applied, keeping old-generation usage and results as **a linked segment, not a rewrite** of the
earlier attempt; an unsupported correction stays held and the way on is `execution stop`,
reconciliation and a new offer with lineage — never an automatic duplicate launch. **The bare
`correct` still invalidates evidence and claims no delivery, and while any execution of the node
is live or uncertain it is refused, `CORRECT FAIL node=<id>: execution live, use execution
correct`**, because otherwise it would bypass the barrier (an amendment to *The verbs*; replays
`correct-is-a-linked-segment`, `bare-correct-refuses-under-execution`).

**Undo stops at transport handoff, and a handoff includes a claimed send.** The rows are in the
reversible-verb table above; what the table does not say is the boundary: a reversal is admitted
while the exact postimage, the captured target set and the hold generation are unchanged, and
**a claimed or in-flight send with no receipt is a handoff**, because the absence of a receipt
does not prove a directive was never sent. The `offer` compensator is one `:offer` event with
`:effect :cancelled`, the session's own, and until its codec is pinned (open item 6) an
implementation refuses the undo rather than inferring that a queue, a worker or a lease can be
restored (replay `undo-names-its-reversible-set`, extended to these rows).

```lisp
;; EXAMPLE DATA, NOT PRODUCT CONSTANTS: one offer, its two receipts and a hold.
(:kind :offer :id "e-7c21" :node "schema/fixed-tables/versioning/cpp" :by "coord" :stamp "2026-09-15T02:00:00Z"
 :offer "o-4b9e" :attempt "a-0d13" :to "worker-a" :profile "child-agent@17" :request-ref "dispatch-91"
 :generation 2 :payload "note:bus:coord-5e1a" :payload-sha256 "3f…" :reserve 1 :until "2026-09-15T02:30:00Z"
 :requested-model (:absent) :predecessor-offer (:absent) :predecessor-attempt (:absent) :reason (:absent)
 :sender (:absent) :receipt-digest (:absent) :effect :dispatched)
(:kind :acknowledge :node "schema/fixed-tables/versioning/cpp" :offer "o-4b9e" :reply "r-1" :stage :received
 :provenance "note:bus:worker-a-88c0" :provenance-sha256 "9a…" :deadline (:absent) :default (:absent)
 :observed-model (:absent) :bench (:absent) :execution (:absent) :reason (:absent)
 :sender "worker-a" :receipt-digest "9a…" :effect :received)
(:kind :acknowledge :node "schema/fixed-tables/versioning/cpp" :offer "o-4b9e" :reply "r-2" :stage :accepted
 :provenance "note:bus:worker-a-88d4" :provenance-sha256 "c2…" :deadline "2026-09-15T08:00:00Z" :default :release
 :observed-model (:absent) :bench (:absent) :execution (:absent) :reason (:absent)
 :sender "worker-a" :receipt-digest "c2…" :effect :accepted)          ; created lease l-…, W += the node
(:kind :execution-control :node (:absent) :change :stop :control (:absent)
 :scope (:node "schema/fixed-tables/versioning/cpp") :action (:absent) :instructions (:absent) :sha256 (:absent)
 :manifest (:absent) :reason "wrong generation dispatched")            ; hold durable; capture anchored; op=execution queued
```

**What this section leaves open, each with an owner, and none of it inferred meanwhile.** (1) The
receipt-verifier interface and its bounded public output, and how a configured friend identity
is bound without a secret in any record — Stella. (2) The assignment-index binding from an
accepted offer to the holder's existing lease, outside the lease schema — Stella. (3) The wire's
omission, `null` and list grammar and byte limits for the payload, the reason, the provenance
body, execution handles and the assignment body — Rowan, with the protocol table. (4) The
reconciliation and reassignment policy: what may clear uncertain capacity and authorise a
replacement — Glenn. (5) Model-only pool actors as recipients without an invented friend —
the open decision of *Friends, CONFIG and ACTIVE*, Stella. (6) The capture-pin and clip
representation in *Retention*, the reversal-envelope codec and its relation to `undo`, the
derived capture, receipt and observation codecs, adapter pause, resume and correction
capabilities with the segment-boundary codec, and fencing enforcement at the receiver — Stella,
before any runtime intake. (7) The `EXECUTION` and `OFFER` line shapes below are this file's
first spelling and are a protocol-lock decision — Rowan.

## Presence: who is awake and who is asleep *(Rowan, on Glenn's word of 2026-09-15)*

Glenn: *"so far both you and Stella have failed to notice when friends fall asleep ... design a
system where friends report in somewhere and not reporting in for 5 minutes = gone to sleep. we
should be able to track who is awake and who is asleep"*; and the consequence he named:
*"delegating work to somebody who is asleep = stalled forever"*, *"so we need a way to recover
from this"*, *"and a way to detect it"*. The hurt behind both is one: a line that stops waiting
stops existing, and nothing in O says so, so work handed to it is silent rather than refused.

**The beat, and the steady state.** The optimal case is **a beat a minute from every line**, and
**no beat for five minutes is asleep**. The beat costs nothing because it rides work the line is
already doing: `nova-bus wait`'s poll tick, the harness's per-turn hook, the cursor commit an
`inbox` already writes. A friend's steady state is one loop and this file names it: **wait, wake
on a note, `take`, work, `settle`, tell, wait**. Every leg of it writes a `:presence`, so a line
that is in the loop is visibly awake and a line that has left it is visibly asleep inside the
window — which is the whole reading. Nothing here asks a friend to remember anything: **the loop
lives in machinery per harness** (a Claude Code hook, an OpenCode plugin, the harness's own
always-loading file), never in a model's memory, because the persistent hurt is that friends
across harnesses forget to wait and so go to sleep. **A friend for whom no presence source is
configured at all reads `unknown` and never `asleep`**, because silence from a line nobody
watches is our gap and not that friend's; a friend whose source is configured and stops beating
reads `asleep`, which is the case the whole section is for.

**The `:presence` event.** One more kind of *The data* above, written by machinery and never by
prose. Its subject is a friend identity and not a node: `:node` is `(:absent)`, and `FRIEND OK`
prints `friend=<name>` with `change=presence` added to its enumeration.

```lisp
(:kind :presence :friend "emma"
 :at "2026-09-15T14:41:11Z" :clock :tool     ; the instant the line was observed to be running
 :source :bus-cursor                          ; :bus-cursor | :wake-probe | :harness-hook | :manual
 :seen "3f9a1c2b8d40"                         ; the cursor commit or the revision the source saw; "-" where it saw none
 :by "emma")                                  ; the author, as on every event
```

**The four sources, ranked, and which one wins.** Presence is derived from **the newest record,
whatever its source** — that is the rule, and it is the only rule that needs no arbitration. The
ranking below settles a tie *inside one coalescing bucket* and nothing else, and it ranks by how
directly the record proves a turn ran on that line:

1. `harness-hook` — the friend's own session ran a turn. The strongest: a turn is not claimable.
2. `bus-cursor` — the friend's line advanced its own `CURSOR` commit reading the bus (SPEC.md's
   `from-<me>/CURSOR`, already a free liveness signal in git, written by `nova-bus wait`/`inbox`).
3. `manual` — the friend's own word, by `friend --here`. A claim, not a turn, so it ranks below
   the two that are machinery.
4. `wake-probe` — SPEC-WAKE's bounded availability probe: **another line's observation**, the
   weakest, because reachable is not awake. A probe writes a `:presence` only on an answer from
   the friend's own line; **a probe that times out writes nothing**, since missing contact is
   `unknown` and never failure, and never `asleep`.

**How a friend reports in without new work.** (a) the cursor advance every line already makes when
it reads; (b) `nova-wake probe`; (c) a harness hook at session start and once per turn; (d)
`nova-work friend --here <name> [--seen <rev|sha>]` by hand. The tool derives one presence per
friend from whichever record is newest and reads no other store.

**The readings.** `awake` is a new ask of *Queries — the contract* above, and `who` and `stale`
carry the same facts on their existing rows, so nobody reads presence from a second place:

```
FRIEND ROW <name> presence=<awake|asleep|unknown> age=<dur|-> source=<bus-cursor|wake-probe|harness-hook|manual|-> at=<stamp|-> seen=<rev|sha12|-> wait=<hook|plugin|scheduled|manual|none> coordinator=<true|false>   (awake)
QUERY OK ask=awake ... [friends=<n> awake=<n> asleep=<n> unknown=<n>] rows=<n> shown=<n> ...
QUERY ROW <id> lease=<lease-id> holder=<name|unowned> ... presence=<awake|asleep|unknown> presence-age=<dur|-> presence-source=<s|-> wait=<hook|plugin|scheduled|manual|none> assigned=<name|-> ack-age=<dur|-> finding=<holder-asleep|assigned-unacknowledged|->   (who, stale)
```

`asleep` is no presence inside `--window`, **default 300 s**, Glenn's five minutes. `unknown` is no
presence record ever, or a clock the session does not trust (`:clock :given`, or an `:at` outside
`--skew`): the two are different facts and neither is failure. Rows are capped and counted like
every ask, and `QUERY MORE` continues them. Glenn's own spelling of the two `stale` findings is
`STALE <node> holder=<friend> asleep age=<dur>` and `STALE <node> assigned=<friend> unacknowledged
age=<dur>`; this file prints those two facts in the `finding=` field above, because *Output
grammar*'s first token is the verb's and `stale` is an ask of `query` — the words are Glenn's and
the line is this file's *(Rowan's decision, for review)*.

**Detect at delegation time.** `offer`, `take --for <name>` and every other assignment verb consult
the reading before writing: an assignee whose presence is `asleep` is **refused at
exit 2, nothing written**; an assignee whose presence is `unknown` because no source is configured for it is admitted with `presence=unknown` on the verb's OK line (a live line without a source is not a sleeper, Johnny's read of draft 1), and refused only when a source is configured and has never spoken, the verb's own `FAIL` line carrying `presence=<asleep|unknown>
age=<dur|-> source=<s|->` as its reason — Glenn's spelling of the same refusal is `ASSIGN REFUSED
<friend> asleep age=<dur>`. `--anyway --reason <text>` proceeds and records the reason in the
event, because a coordinator who knows a line is about to wake must not be blocked by its own
reading. **Detect after the fact**: `stale` lists every open lease *and every pending offer* whose
holder reads `asleep`, so a delegation that went to sleep after it was taken is never silent.

**Assigned, acknowledged and started are three facts.** An `:offer` is assignment; the assignee's
own `:acknowledge` (or its `take`) is acknowledgement; a `:lease` with a heartbeat is started. An
assignment with no acknowledgement for `--ack-window`, **default 600 s**, reads
`finding=assigned-unacknowledged` in `stale` **regardless of the presence reading** — presence may
say awake and the friend still never picked it up — and that reading is ground for `reassign` on
its own. This is Glenn's worst case said plainly: *"they have been on it without acknowledging
start for x minutes, ok, they are probably asleep, reassign."*

**Recover.** `nova-work reassign --node <id> --to <name> --reason <text>` is the coordinator's verb
and writes a `:reassign` event whose subject is the node: `:from` (the prior holder), `:to`,
`:ground` (`:asleep` or `:unacknowledged`), `:presence` (the reading it cites — source, age, at),
`:lease` (the lease it fences), `:reason`. **The prior lease is fenced**: from that event onward
its `heartbeat`, `release` and `handoff` are refused at exit 1 naming the reassignment, so a
friend who wakes cannot resume over the new holder; its work is not lost, because the events it
already wrote stand and its responsibility is untouched, exactly as expiry leaves them. **The
woken friend's first `who` says so**: `QUERY NOTE reassigned node=<id> from=<name> to=<name>
at=<rev>`, Glenn's spelling `REASSIGNED <node> to <friend> at <rev>`. **Nothing reassigns on its
own**: the reading is a reading, the ten-minutes-silent rule is the coordinator's and is applied
by a person or their machinery, and the event names the reading it stood on so the judgement is
auditable rather than guessed.

**The coordinator's own presence counts, and a sleeping coordinator is the worst case.** `awake`
prints `coordinator=true` on that row, and the coordinator's line beats like every other. A second
line that reads the coordinator `asleep` **does not become coordinator**: per *One coordinator, one
live reader/writer* below, a stale beat is an availability signal and not proof of takeover
authority. It may print the reading, and nothing else, because it holds no writer of O; the only
path on is that section's ownership transfer with a higher fencing generation once the `OWNER`
record's `until` has passed. A presence reading never fences anybody.

**Announce, so nobody learns of work only when it ends.** `take` and `settle` are the required
events at the two ends of a piece of work, and the friend's own machinery sends **one bounded line**
on its own bus as each is written — the tool writes the event, the hook sends the note, and the
friend never has to remember either. `nova-work` itself sends nothing and names no transport: a
verb that messaged a person here would be naming one house's bus, which is the fence the
escalation paragraph above already holds.

**Refusals.** A `:presence` whose `:source` is not configured for that friend is refused at exit 2,
nothing written — a source is configured per friend by `friend --presence-source <name>=<s>,...`,
and **a friend's own disposition is the only consent record**, so a line nobody configured is
`unknown` for good and no other line may declare it awake. A presence whose `:at` is ahead of the
session clock beyond `--skew` is refused at exit 2. **More than one presence per friend per 10 s is
coalesced**: the later record folds into the open bucket, the `OK` line prints `changed=0`, and no
second event is written, so a per-minute beat costs one event a minute and a chatty harness costs
no more.

**Replays.** `friend-falls-asleep-mid-lease` — a held lease whose holder stops beating reads
`finding=holder-asleep` in `stale` inside 300 s, the lease itself untouched.
`coordinator-asleep` — the coordinator's own beat stops; a second line's `awake` prints
`coordinator=true presence=asleep` and that line acquires nothing.
`presence-sources-disagree` — a `wake-probe` answer and a `harness-hook` beat one second apart
resolve to the newer; the same two inside one 10 s bucket resolve to `harness-hook` by rank.
`delegated-to-a-sleeper-then-recovered` — an `offer` to an asleep friend refused at exit 2; with
`--anyway` it is written, appears in `stale`, is reassigned with the reading cited, the prior
lease is fenced, and the sleeper's `heartbeat` on waking is refused while its first `who` shows
the reassignment. `assigned-never-acknowledged` — an assignee reading `awake` who never
acknowledges shows `finding=assigned-unacknowledged` at 600 s and is reassigned on that ground
alone. `friend-forgets-to-wait-and-is-seen` — a harness whose wait loop is not installed writes no
beat, reads `asleep` within 300 s of its last turn, and every assignment verb refuses it.

**The wait is mechanical where the harness allows it.** Glenn: *"wherever possible, friends should
mechanically set up poll the bus so it deterministically wakes them up"*, and *"may not be possible
on all harnesses"*. So the mechanism that holds a line's wait is a declared fact of the friend, its
**`:wait-source`**, configured beside its presence sources by `friend --wait-source <name>=<w>` and
printed as `wait=` on the rows above. It is one of `hook`, `plugin`, `scheduled`, `manual` or
`none`, and this table is the current reading per harness, each row a fact to be corrected by the
line that runs it rather than a promise:

| harness | what holds the wait | survives compaction | survives restart | `:wait-source` |
|---|---|---|---|---|
| Claude Code | a session-start and per-turn hook that runs `nova-bus wait`, in `settings.json` | yes, the hook is config and not context | yes | `hook` |
| OpenCode | a plugin holding the poll outside the turn | yes | yes | `plugin` |
| Codex | a scheduled command re-entering the session on a note | yes | yes, while the schedule lives | `scheduled` |
| Antigravity | its own always-loading file naming the wait as the first act of every load | yes | yes | `hook` |
| Grok | an OS process outside the turn holds the poll and writes the beat (Johnny's `johnny_bus_heartbeat`, 2026-09-15) | yes | no, a 10 h session cap | `plugin` (SPEC-WAKE's third shape) |
| a bare API loop | the loop program itself, outside any session | not applicable | yes | `plugin` |

**Where the harness cannot hold it**, `:wait-source` is `manual` or `none` and nothing is pretended:
the line simply stops beating when its turn ends, the reading shows it `asleep` within 300 s, its
assignments refuse, and the coordinator reassigns by the recovery path above. **The human is never
the waker** — that is the point of the whole section, and a harness that cannot wait is a fact the
reading carries rather than a person's job to notice.

**What this does not do.** No paging and no notification: the reading assigns nobody and wakes
nobody. No automatic reassignment: every `:reassign` has a coordinator and a reason. No telemetry
beyond the four sources and the four fields above — no machine facts, no command lines, no content
of any turn, nothing about another house's bench. No heartbeats from swarm cards, which are
model-only executions and never friends, by *the participation question is closed* below. No
presence for the machines of *The fleet*, which are declared facts and not lines. And no second
availability store: `:observe`'s `:state` remains what a person recorded, and presence is derived
from `:presence` events alone.

## Delegation *(Rowan; folded from `docs/SPEC-DELEGATION.md` draft 3 on Glenn's word of 2026-09-15, spoken live in Rowan's window and carried by no comment id — *"merge 'delegation' into work spec as a new epic"*; Stella's reviews 5672078177 and 5673066509 taken as that draft took them)*

Glenn asked for one explicit **DELEGATION** section in this contract, applying at every node,
real or virtual, human-only, AI-only or mixed (nova-tools#321, 5671991172), and for a place
*"where you write your own notes to, as informed by our conversations"*, so that *"what I just
told you about how and what to delegate"* is kept (5672006742). The hurt is generic: a changed
instruction lives in the conversation it was spoken in, and a later window, a later model or
another coordinator that reads the older word, or none, routes by it. On 2026-09-14 the
instruction narrowed twice in one day (stella-5e0d788049ca, then stella-1a9783ed1ccf: *"Astra
default is for coordination and thinking. Not for coding."*), and nothing was lost only because
the same person was in the window both times. This section is planning inside the v2
recursive-node boundary of #321; it changes no v1 completion count, and nothing in it is built.
The draft it folds and its two review rounds are history in PR #335 and the commits under it;
the file itself is deleted, as its own status paragraph said it would be.

### The five duties

The duties are Glenn's (5671991172), stated once; four are carried by sections this file already
has, and this section points rather than restates:

1. **Decide** whether to do work locally, batch it, or delegate it, comparing eligible routes by
   model-specific effective token price and expected context, coordination, mandatory review and
   rework, preserving quality and scarce coordinator capacity — on #175 and
   [PROPOSAL-SCHEDULING-COST.md](PROPOSAL-SCHEDULING-COST.md); *"do not create a second ledger"*.
2. **Accept** from the coordinating parent, then perform or delegate; each bounded assignment
   gets a small fresh brief with scope, acceptance criteria, dependencies, budget and required
   context — *Admission and result gates* rule 1, and `:effort` of *Efficiency: lessons absorbed*.
3. **Preserve** the parent offer, local work, child assignment/attempt and external
   Issue/Discussion mappings through every transition — #321 *Durable mapping*, and *Assignment
   and execution control* above.
4. **Show** actual availability and ownership separately from assigned, accepted and
   verified-running states; no duplicate active writers during transfer — the lease and W
   sections, *Presence* above.
5. **Collect** evidence and usage, apply the required review, integrate child results, and
   acknowledge completion upstream; **partial child success never closes the parent or the mapped
   external issue** — *Evidence*, *Cost*, and *The envelope up* below.

What no other section carried, and this one does: **the coordinator's notes**, which duty 1
reads before it decides and duty 2 reads before it writes a brief; **the six gates** a delegation
passes, read as one route; **the decision packet** a child's result comes back as; and **the edge
contract** of the tree, the same at every hop. The current goal, which a coordinator on any model
loads first, is *The current goal* under *The data*, folded there by #340, and this section
points at it.

### The coordinator's notes

A note is one record a coordinator writes from a conversation or from experience, kept so that a
later window, a later model, or another coordinator reads it before routing work (5672006742).
**It is data.** Reading a note never executes it; a note that reads like a command to the reader
is still a record of what somebody said.

A note is one flat list of *The data*'s restricted Lisp — lists, keywords, strings and integers —
in the field order below, **every field written**, an absent one `(:absent)`, exactly as this
file writes an event. A note missing any of the first six is refused at write.

| field | value | source |
|---|---|---|
| `:id` | `"note:<sha256>"`, assigned by the tool, below | (Rowan's decision, for review) |
| `:scope` | `(:coordinator "<name>")`, `(:group "<name>")` or `(:node)` | 5672006742: *"one coordinator, a configured group, or the shared node"* |
| `:author` | the participant who wrote it | 5672006742: *"keep authorship and applicability explicit"* |
| `:date` | UTC, `"2026-09-14T23:05Z"` | 5672006742 |
| `:source` | a quote of the human's words, or a pointer (comment id, bus note id, commit) that holds them | 5672006742: *"preserve the source conversation/decision"* |
| `:kind` | exactly one of `:instruction`, `:observation`, `:heuristic` | 5672006742 |
| `:text` | the note, bounded below | |
| `:constraint` | optional; the constraint form below | 5672006742: *"explicit routing constraints alongside prose"* |
| `:uncertain` | optional; what the author does not know | 5672006742: *"record uncertainty rather than inventing a user decision"* |

**`:state` is not a field of the note.** It is derived from the log — `:active` unless a
supersede event names the note, then `(:superseded-by "<id>" "<date>")` — and every reader prints
it beside the note as the snapshot prints derived state beside a node. Nothing the tool writes
later touches the note's own list.

**Identity is the content.** `:id` is `note:` followed by SHA-256, lowercase hex, over the
canonical serialization of `:scope`, `:author`, `:date`, `:source`, `:kind`, `:text`,
`:constraint`, `:uncertain`, absent fields `(:absent)`, printed by the deterministic printer the
payload digest uses — never a file, never a position in one, so the id is the same on every bench
and every build **(Rowan's decision, for review)**. A second write of the same preimage is the
same note, refused `NOTES FAIL …: already written note=<id>`, nothing written.

**`:kind :instruction` means the human said it.** Its `:source` must quote or point at the
human's words; a note whose source is the author's own inference is an `:observation` or a
`:heuristic`, whichever the author claims. The tool checks the shape of the source — a quote or a
pointer is present — never its truth; truth is what the reads are for **(Rowan's decision, for
review)**. The names inside `:scope`, `:author`, `:source` and `:constraint` are **instance
data**: nothing in nova-tools knows or prefers any of them (5672006742: *"product rules must not
hard-code our names or model choices"*). A test that mentions a model name mentions a made-up one.

**Where they live, and who writes.** Notes are records of the resident work set, written by the
one writer this file has and no other: the owning session, one typed event per mutation, a
request id, validated against the current revision, journaled before it is acknowledged,
published by a clip in the one revision-labelled snapshot (*The execution model*; *One
coordinator, one live reader/writer*). **No new bus, no new repository, no file a second process
writes** — a shared file with several writers has no atomic supersession, and a file's own hash
is not an identity (Stella, 5672078177). Notes are an index of the resident model in the sense
`friends` and `models` are: **no node kind of O, no count, no roadmap cell, no required set
moves**. Their event kind is `:note` — `:change` (`:write` or `:supersede`), `:note` (the id),
`:scope`, `:author`, `:date`, `:source`, `:kind`, `:text`, `:constraint`, `:uncertain`,
`:superseded-by`, `:reason`, in that order for the payload digest; the subject is a note
identity, `:node` is `(:absent)`, and `NOTES OK` prints `note=<id>` where `node=<id>` would stand
**(Rowan's decision, for review)**; the replays `new-verbs-have-a-kind-and-a-field-order` and
`new-verbs-retry-to-one-event` cover `:note` as they cover `:friend`. A participant writes a note
as a friend submits a result: a request to the owning session with `:by` the participant, checked
against the scope's configured participants — a `(:coordinator "A")` note only by A, a `(:group
"G")` note only by a member of G, where G is `friend --group`'s group and not a second registry,
a `(:node)` note by any registered participant. Whether a participant may *read* another's notes
is the node's access rule, not this section's; filtering a view never establishes an access
boundary (#321). **A note grants nothing** (5672006742: notes *"never create credentials,
permissions or access"*). Another bench reads notes as it reads everything: the published snapshot
at a named revision, or the live session.

**Bounds.** Small by refusal, not by construction. Three limits are CONFIG data of the node, none
defaulted, a missing one `refusing to guess` (SPEC.md *Conventions*):

```lisp
(:notes :max-active 200 :max-text-bytes 2048 :max-constraint-nodes 64)
```

`:max-active` bounds the active notes across all scopes; a `write` past it is refused `NOTES
FAIL …: active=<n> past :max-active=<n>, supersede or retire one`, and a `supersede` never
changes the active count. `:max-text-bytes` bounds `:text`, `:source` and `:uncertain` each;
`:max-constraint-nodes` bounds the constraint form; past either is refused at write naming the
field and both numbers. Every read is under the session's `--max-bytes --max-depth --max-nodes`,
which refuse and never truncate. Superseded notes are never deleted by the tool: a clip moves one
older than `--retain` to the closed archive under the same versioned index root, in pages bounded
by `--page-bytes` and `--page-records`, and `list --all --from --to` reads them from there — the
existing retention boundary with one more record kind in it.

**The verbs.** Four, under the `notes` subject of the one binary; `NOTES` joins *Output grammar*
with the same `OK`/`FAIL`/`ROW`/`MORE` lines and the cap-and-count law; the duties are Glenn's and
the names and flags are **(Rowan's decision, for review)**.

```
nova-work notes write      --as <name> --scope <scope> --kind <kind> --source <text> --date <utc> --text <text> [--constraint <form>] [--uncertain <text>] --request <id> --expect <rev>
nova-work notes list       --as <name> [--scope <scope>] [--all [--from <stamp> --to <stamp>]] --max <n>
nova-work notes supersede  --as <name> --id <old> --source <text> --date <utc> --text <text> [--constraint <form>] [--uncertain <text>] --reason <text> --request <id> --expect <rev>
nova-work notes applicable --as <name> --task-class <class> [--candidate <role>/<model> ...] --max <n>
```

`write` appends one `:note :write` event; refused (exit 2, one line naming the field) when
`:source` or `:date` is missing, when `:kind` is not one of the three, when `--as` is not a
configured writer of the scope, when a bound is exceeded, when `--expect` is stale, and when the
constraint form carries anything but `:deny`, `:prefer` and `:reason` — there is no `:allow`.
`list` prints the active notes for a scope, one `NOTES ROW` each (id, kind, date, author, the
first line of the text, `constraint` if present), capped and counted; `--all` adds the superseded
ones with the id and date that superseded each. **A superseded note is never printed as active,
by any verb** (5672006742: *"preserve the old entry as superseded and apply the current
applicable decision"*). `supersede` is **one mutation, one envelope, two events**: a `:note
:write` of the replacement and a `:note :supersede` on the old id naming `:superseded-by`. The
replacement is constructed in full before anything is hashed — `:scope` and `:kind` inherited
from the old note (`supersede` has no `--kind`; Stella, 5673066509), `:author` from `--as`, the
rest from the flags — validated as `write` validates, and the envelope is validated whole,
journaled whole, applied whole; a failed validation writes neither event, and a multi-event
envelope never partly publishes. Refused when the old id is not active — of two competing
supersedes the second is refused `not active: superseded-by <first>`, so at no revision are an
obsolete instruction and its replacement both active, or either lost — and when the replacement's
kind would be weaker: a `:heuristic` or an `:observation` cannot supersede an `:instruction`
(5672006742: *"inferred heuristics cannot silently override explicit instructions"*), which
inheritance makes true by construction and the validator still checks. **A note of another kind
is a new `write`, not a supersede.** To change an instruction the human must have said something,
and the new `:source` shows where.

**The constraint form.** Beside the prose a note may carry one structured constraint, so the
filter has something to filter by **(Rowan's decision, for review)**:

```lisp
(:constraint
  (:deny   (:model "<name>" ...) (:role "<name>" ...) (:task-class :<class> ...))
  (:prefer (:task-class :<class> ...) (:route "<role>/<model>" ...))
  (:reason "<text>"))
```

A candidate route is excluded by a `:deny` when it matches **every** named axis; `:prefer` is
advice to the route selector and excludes nothing. Task classes are the vocabulary `:model`
events already carry in `:task-class`, configured per node, and an unknown class is refused at
write, so a typo cannot open a hole. Two active constraints that disagree are not resolved by
the tool: `applicable` prints both and the candidate is `excluded` — a deny wins over a prefer,
and over silence — and the coordinator supersedes one with a source.

**The worked example is instance data.** With Glenn's instruction of 2026-09-14 as a
`(:coordinator "Stella")` `:instruction` note whose `:constraint` denies `(:model "Astra")` for
`(:task-class :coding :implementation :execution)` and whose `:source` quotes his words and their
comment id, `applicable --as Stella --task-class coding --candidate coordinator/Astra` prints
`excluded note:<id>`, and a card for that route is refused before any child starts; the older
instruction of 20:00Z is superseded by name, so the next window reads the change and not only
the result. A second, `(:node)` note with the whole-route economics of
PROPOSAL-SCHEDULING-COST.md and no constraint excludes nothing; it is read. The ids in any
example are abbreviated and illustrative; a test computes the full digest from the fields
(Stella, 5673066509).

### The read before the route

**`applicable` is the read before the route**, and the rule is Glenn's (5672006742): *"retrieve
the applicable notes before selecting a route or constructing a job brief"*, and *"a configured
restriction on a model/role/task class must filter candidate routes before dispatch, with a
visible reason for exclusion"*. Given a task class and the candidate routes, it loads **every**
active note whose scope covers `--as` (its own scope, every group it belongs to, and `(:node)`),
evaluates every constraint against every candidate, and only then prints: one `NOTES ROW` per
candidate, `eligible` or `excluded <note-id>` — never cut by `--max` — then the applicable notes
under `--max`, `NOTES MORE` when cut. When the verdict rows alone would not fit `--max-bytes` the
verb refuses `past --max-bytes` and prints no verdict at all, never a partial list of `eligible`
rows. **The cap is on the prose rows and never on the evaluation**: a deny in a note the display
cut still excludes (replay `applicable-cap-never-hides-a-deny`). The reason is the note id, and
the note is one `list` away; what the caller carries into the brief is the constraint lines and
the note ids, not the conversation (5672006742: *"without copying accumulated conversation
history"*).

**Unknown is not eligible.** Where the complete set cannot be loaded — no live session and no
`--snapshot`, a snapshot past a bound, a missing notes index — the verb prints `NOTES FAIL …:
<reason>` at exit 2 and **no candidate is printed `eligible`**; a candidate whose model is not in
`models` is `unknown` for the same reason (gate 2: *unknown price is not cheap*; here unknown
policy is not open). Every answer prints `rev=<n>` and `from=live` or `from=snapshot`, and
**display and eligibility are separate**: a snapshot answer is read-only planning, and no row of
it makes a route eligible, because the age of a snapshot alone cannot establish that a stop or a
deny has not been written since (Stella's decision, 5673066509). **The check boundary**: before a
route is priced and before a card is written for it, the caller evaluates the complete current
constraints against the owning session at its current revision; the eligible verdict carries
`rev=<n> from=live`, and the write that admits the route carries `--expect` that revision, so a
stop or a deny written between the check and the admission refuses it as `stale`. **A brief built
from a snapshot answer is a draft until that check passes.** This adds no lease and no scheduler,
promises nothing about an instruction that changes after admission — that reaches running work by
a stop — and sets no age bound: there is no `:max-snapshot-age` anywhere in this section, because
the live check makes one unnecessary (Stella's decision, 5673066509). Where route selection is built (#175), it calls `applicable`
first and prices only the routes that came back `eligible`; a brief constructor (nova-swarm's
card, SPEC-SWARM.md) handed an `excluded` or `unknown` route refuses to build the card, naming
the note or the reason. **Narrative reminders alone do not filter** (5672006742): a note with
prose and no `:constraint` is printed for the coordinator to read, and it excludes nothing.

### The current goal

Specified in *The current goal* under *The data* (folded by #340), and this section points at it:
the goal is a node of O and the current goal a scope-keyed reference in the `goal` index, keyed
by the same scope a note is; `goal show` prints the constraint rows `applicable` would carry for
that coordinator, uncut, before the capped rows, and `GOAL FAIL` with no row when the notes index
is unloadable; `--stop` is a request through `:cancel-requested` and never stopped-worker
evidence. The witnesses are the five `goal-` replays and `applicable-cap-never-hides-a-deny` in
*Acceptance replays*. What the goal needs from this section is the note scope and the
constraint rows, both above.

### The six gates a delegation passes

*Admission and result gates* above states eight rules of the efficiency policy. **A delegation
passes the first six as one route, in this order, and a refusal at any of them is a refusal by
name at exit 2 and never a silent skip**; this subsection adds no rule and reads the six as the
life of one packet. **Admission, gates 1 to 3:** (1) the notes are read — `applicable` for the
task class and the candidate routes, at the live revision, before anything is priced; (2) the
packet is bounded — one objective, source revision, acceptance criteria, allowed scope, result
contract, recovery checkpoint and `:effort` (rule 1; *Efficiency: lessons absorbed* rule 9), and
a packet lacking any of them is refused; (3) the route is eligible, then economical — only routes
that came back `eligible` are priced, an expensive-route exception records why the cheaper
eligible choice does not fit, unknown price is not cheap, and a dispatch that would cross the
daily spend ceiling is refused naming it (rule 2; lessons rule 10). **Execution and result, gates
4 to 6:** (4) real bounds and a live recipient — the launcher's actual input, output, deadline
and attempt limits are recorded separately from the instruction's words, a timeout is not proof of
termination, another attempt is never started silently after uncertainty about the first (rule
3), and an `offer` to a friend who reads `asleep` or `unknown` is refused (*Presence*, replay
`delegated-to-a-sleeper-then-recovered`); (5) receipts by machinery and one read per head —
unchanged-state detection, deduplication and receipt collection require no model call (rule 4), a
receipt is a verified observation and never a request's word (*Assignment and execution
control*), and a review binds to the exact head it had open, reused only while its reviewed
content, acceptance contract and dependencies are unchanged, never across an unchecked rebase
(rule 5; lessons rules 7 and 8); (6) the whole unit is measured and the result integrated — usage
counts parent, descendants, coordination, review, retries and repair once, missing usage is
unknown and never zero (rule 6), the child's result is integrated against the parent's acceptance,
and partial child success never closes the parent (duty 5). Rules 7 and 8, promotion and
regression, are the policy's own and not a delegation's.

### Decision packets

What comes back up is a **decision packet**, built by machinery, one per item and revision, the
smallest that lets the coordinator decide (Stella, stella-b4e4367c44b6, 2026-09-11;
SPEC-REVIEW.md's `packet` is the model at the review layer). The contract, so the
work verbs and the review verbs say it once: **machinery observes and books** — refresh, exact
revisions, check outcomes, delivery, receipts — and **a model wakes only when an action is
possible or a new hold or question exists**; an empty pulse re-executes nothing (*Efficiency:
lessons absorbed* rule 6, durable triggers). **One packet per item and revision**, amended while
the reader is busy, a maximum delay for urgent failures; a newer revision supersedes the packet
without losing its open findings. **The smallest sufficient packet**: the delta since this
reader's recorded head, the rules it touches, the open findings with their dispositions, the new
behaviour with its evidence pointers, and links to the full sources — the whole diff only when
this reader has never read the entry. **One writer and one durable home per fact**: a verdict is
keyed (reader, sha), a gate (base, head, integration), ownership on the node; a bus note carries
questions, findings and handoffs only, and **there is no receipt-of-receipt** — a worker returns
one structured result, and an independent review does not route through the coordinator to be
counted. The coordination measure is cost per accepted decision across tiers, with wrong or missed
decisions and recovery latency as gates; equal correctness is proved before fewer turns is called a
win (the duty-tier amendment, #500).

### The envelope up, and the no that survives the hop

Glenn, 2026-09-14 (ideas#778, live 21:04Z to 21:16Z): *"each layer summarizes up. As above, so
below."*; *"we should strive to never lose intelligence when we gain efficiency"*;
*"Intelligence should propagate upwards, the distilled form of it."*; *"This same structure can
apply to models in a tree. Just as it applies to nodes in nova-work. It is 'the way'."* Three
trees share one edge: nova-work's nodes (#321, recursive, real or virtual); the coordination
tree of minds under a seat; the model tree under each mind. **The edge contract is written once,
here, and the other two point at it.**

**Downward, a card is an offer with a contract, and the node's no survives the hop.** A
`decline`, a refused `offer`, an `excluded` or `unknown` route, an asleep recipient, a refused
packet, a tripped node, a child past its `:effort` — each arrives at the parent as a refusal by
name and its reason, at the revision it was given, never as silence and never as success; the
parent that cannot see the no has not been told. A child does not widen its own card, and the
coordinator who widens it records why (*Efficiency: lessons absorbed* rule 9). **Upward, the
envelope is a copy and a distillation, never a retelling**: the verdict byte-copied by machinery
(the result pointer, the evidence events, the usage pointer, the exact head), plus the child's
distilled learning in its own words with its evidence, folded by the parent into its own record.
**Finality rises with the tier and is never final below the seat**: a child's `done` is a claim
against the parent's acceptance until the parent verifies it itself, with evidence bound to the
criteria and *"not merely a worker's success claim"* (5672006742), and a partial result closes
nothing (duty 5). **(Rowan's decision, for review, on ideas#778)**: the walls are strongest at the
cheapest leaves; the tree is shallow and wide; and a layer is measured by whether the parent can
still open the floor's evidence and find what the summary dropped. Escalation is the same edge in
reverse: a hold, a question or an exception the child cannot decide rises as a packet with its
reason, the stale pass shows `escalated-age=` and `reread=` as information and reassigns nothing
(lessons rule 5), and recovery is a coordinator's recorded act (*Presence*).

### What this section does not do

No new bus, scheduler, writer or repository: notes and the goal are records of the resident set
under the one session, the route selector is #175's, and nothing here dispatches. No enforcement
claim (5672006742: *"not a claim that persistence or routing enforcement ships today"*): until
`applicable` exists and the card builder calls it, the filter is a coordinator reading its own
notes first, and that is still the rule. No second ledger (5671991172); usage, attempts and prices
stay where *Cost* and SPEC-TOKENS.md keep them. No permissions and no credential mechanism; access
is configuration and git. No transaction engine; atomic supersession is one envelope through the
writer that exists. **Open decisions: none**; Rowan's decisions are marked where they stand and
are the content-digest id and preimage order, the `:note` event kind and field order, group
membership from `friend --group`, the three bounds and the archive road, the verb names and
flags, the source check as a shape check, the `:deny`/`:prefer`/no-`:allow` form and every-axis
match, task classes reused from `:model` events, deny over prefer, `unknown` for an unregistered
model, and the replacement's kind inherited on `supersede`.

### Required replays

| Replay | Required outcome |
| --- | --- |
| notes-refuse-missing-source-or-date | `notes write` without `--source` or `--date`, or with a `:kind` outside the three, is refused `NOTES FAIL` at exit 2 naming the field, nothing written. |
| notes-id-is-content-digest | The same eight fields written on two benches yield one id; a second write of the same preimage is refused `already written`; no id is ever reused. |
| notes-writer-is-scoped | A `(:coordinator "A")` note by `--as B`, a `(:group "G")` note by a non-member, or an unregistered `--as` is refused; a note grants no access to anything. |
| notes-bounds-refuse | A write past `:max-active`, `:max-text-bytes` or `:max-constraint-nodes` is refused naming the field and both numbers; a missing bound refuses to guess; a supersede never changes the active count. |
| notes-supersede-is-one-envelope | A supersede writes both events or neither; the second of two competing supersedes is refused `not active: superseded-by <first>`; a superseded note is never printed as active by any verb. |
| notes-weaker-kind-cannot-supersede | An `:observation` or `:heuristic` replacement for an `:instruction` is refused; the replacement inherits `:kind` and `:scope` and carries a new `:source`. |
| applicable-before-route | Route selection calls `applicable` first and prices only routes that came back `eligible`; a card builder handed an `excluded` or `unknown` route refuses, naming the note id or the reason. |
| applicable-unknown-is-not-eligible | With no live session and no `--snapshot`, a snapshot past a bound, a missing notes index, or an unregistered model, `applicable` prints `NOTES FAIL` and no candidate prints `eligible`. |
| applicable-snapshot-is-planning-only | An answer `from=snapshot` admits no route; the admission write carries `--expect` the live `rev=` and a stop or deny written between check and admission refuses it `stale`. |
| narrative-does-not-filter | A note with prose and no `:constraint` is printed by `applicable` and excludes nothing; a `:deny` matching every named axis excludes; two disagreeing constraints print both and exclude. |
| delegation-admission-gates | A packet lacking objective, source revision, criteria, scope, result contract, checkpoint or `:effort` is refused at gate 2; a dispatch that would cross the daily spend ceiling is refused at gate 3 naming the ceiling; each refusal names its gate and reason at exit 2. |
| delegation-result-gates | An offer to a friend reading `asleep` or `unknown` is refused at gate 4; the requested execution limit and the observed expiry or stop outcome are recorded separately and a timeout is not termination; no second attempt starts silently after uncertainty about the first. |
| receipt-at-exact-head | A child's result is booked as a machinery receipt at the exact head it ran against; a review verdict binds to `--head <sha>` and is not reused across a changed head or an unchecked rebase. |
| decision-packet-per-item-revision | Machinery builds one packet per item and revision; a newer revision supersedes it keeping its open findings; while the reader is busy the packet is amended, not duplicated; an empty pulse wakes no model and re-executes nothing. |
| packet-is-smallest-sufficient | The packet carries the delta since this reader's recorded head, the rules it touches, the open findings with dispositions, the new behaviour with evidence pointers and links to the full sources; the whole diff only when this reader has never read the entry. |
| no-receipt-of-receipt | A worker returns one structured result; a verdict is keyed (reader, sha) and a gate (base, head, integration) in one durable home; an independent review is not re-routed through the coordinator; a receipt of a receipt is refused as a duplicate. |
| no-survives-the-hop | A decline, refused offer, excluded route, asleep recipient, tripped node or effort limit reaches the parent as a named refusal with its reason and revision, never as silence or success. |
| envelope-up-is-a-copy | The child's verdict, result pointer, evidence events, usage pointer and exact head arrive byte-copied by machinery, beside the child's distilled learning in its own words; the parent can open the child's evidence from the envelope and find what the summary dropped. |
| finality-rises-with-tier | A child's `done` is a claim: the parent moves only after its own verification with evidence bound to its own criteria; a worker's success claim alone never moves a node on any tier below the seat. |
| escalation-is-a-packet | A hold, question or exception the child cannot decide rises as a packet with its reason and revision; the stale pass prints `escalated-age=` and `reread=` as information and reassigns nothing; an `:effort` widening or an expensive-route exception carries the coordinator's recorded reason. |
| partial-child-never-closes-parent | One child done and one refused, blocked or asleep leaves the parent open with `outstanding=<n>` and the mapped external issue open; the parent's outstanding count and its issue mapping survive the child's refusal unchanged. |

These items are epic E11 of the roadmap. They add no verified completion until implementation
and failure replays pass.

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

## Efficiency policy is validated data *(Stella; proposed enforcement, 2026-09-14)*

Glenn asks that lessons from real coordination become hard rules in nova-work structure and
configuration. This section specifies those gates; **it does not claim they are implemented**.
It refines the existing CONFIG, attempt, pricing and measurement contracts, without creating
another scheduler, usage ledger or executable Lisp configuration. Pulse duration and worker
choice remain team policy: diversity of friends, models and harnesses remains welcome.

### Records and owning operations

Each friend CONFIG may hold a versioned `:efficiency-policy` record, scoped only to that friend
and its capabilities. A team-wide change is explicit validated intake for each affected friend,
never an implicit cross-friend mutation. Its required fields are a stable
id, schema version, revision/content hash, scope, author/provenance, and these typed groups:

- `:quality`: references to the task-class acceptance and required review gates.
- `:routing`: eligible capability references, configured preference order among suitable
  economical routes, and conditions requiring an explicitly recorded escalation reason.
  Role, consent, availability, fencing and capacity constraints still take precedence.
- `:bounds`: positive integer input-packet bytes, report bytes, attempt count and execution-limit
  milliseconds; checkpoint reference and a boolean full-history permission. Limits are
  configured per task class, never hardcoded friend or model names.
- `:measurement`: workload/accepted-unit definition, required actor and attempt coverage,
  rate-revision references, and separate operational versus tool-implementation allocation.
- `:trial`: optional experiment reference, stage (`:observe`, `:trial`, `:adopt`, `:retire`),
  expiry, and explicit quality, total-token, cost and wall-time regression tolerances.

Validated `config --intake` owns policy admission and replacement under the existing config
journal event, existing friend subject and revision guard. Trial-stage changes use that same
config event with prior revision, new policy and evidence references; no out-of-band promotion
exists. An invalid or incomplete policy leaves the previous revision
intact. The canonical schema, digest field ordering and grammar must include these fields
before this slice can lock; no generic field edit bypasses validation.

An experiment is retained as a canonical task with acceptance criteria and references to an
immutable manifest and observation artifacts. The manifest pins its hypothesis, workload,
source/acceptance revisions, baseline selection, changed variables, comparison procedure,
sample/stop rule and tolerances **before a prospective trial**. A historical comparison is
explicitly `:retrospective`, retaining its selection rule and confounders; it cannot be relabelled
as preregistered. Outcomes are `:saving`, `:inconclusive`, or `:regression`, with coverage and
uncertainty. A zero accepted-unit denominator is undefined, never zero cost per result.

ACTIVE executions reference policy revision, task generation, attempt and parent execution,
packet digest/byte count, requested and observed model, harness, bench,
`:execution-limit-ms` (positive integer), `:execution-started-at` and
`:execution-expires-at` (UTC stamps or explicitly unknown before launch),
`:stop-outcome` (not-requested, requested, confirmed-stopped, completed or unresolved),
`:stop-observed-at` (UTC stamp or explicitly unknown), checkpoint, result and usage pointers.
The client wait deadline is not stored in any of these execution-bound fields. These extend existing execution/attempt records; references never
duplicate usage. Unattributed coordinator work remains an explicit allocation gap. C retains
closed experiments and attempts; roadmap and friend views reference them after closure.

### Admission and result gates

1. **Bound the work packet.** A delegated request has one named objective, source revision,
   acceptance criteria, allowed scope, result contract and recovery checkpoint. Prefer a focused
   packet to a transcript fork. Refuse dispatch above the configured packet bound, or a history
   fork when disallowed. Context expansion needs a recorded reason and revised packet.
2. **Prefer a suitable economical route.** Eligibility is checked before price/preference.
   An expensive-route exception records why the cheaper eligible choice does not fit. Unknown
   price is not cheap; new routes can run only as explicitly bounded authorised trials.
3. **Enforce real bounds.** Record launcher support separately for input, output, deadline and
   attempt limits. An instruction saying “five minutes” is not an enforced timeout. The
   execution limit is distinct from the existing client `--deadline`, which bounds waiting and
   does not stop a worker. The execution record retains the requested limit and observed expiry
   or stop outcome; timeout is not proof of termination. If a configured hard bound lacks adapter
   support, refuse automatic dispatch with the missing
   capability; leave manual work visibly outside that enforcement claim. Never silently start
   another attempt after uncertainty about an earlier one.
4. **Keep routine traffic mechanical.** Unchanged-state detection, deduplication and receipt
   collection require no model call. Queue independent actionable deltas for a bounded pulse;
   corrections, stop requests, lease loss and deadlines bypass batching. A pulse resumes from
   a revision-bound checkpoint and returns changed facts, decisions and evidence pointers.
   The interactive coordinator is included in measurement; a worker pulse does not imply that
   its parent context was cleared or stopped accumulating tokens.
5. **Review once per applicable scope and revision.** Reuse a valid review only when its
   reviewed content, acceptance contract and dependencies are unchanged. Read the relevant
   delta when they change; expand for unresolved interaction risk. The reviewer determines
   relevance and depth against the unchanged acceptance contract and records the reviewed scope,
   dependency assumptions and unresolved risks. Reuse never shortens the required read of the
   integrating context; a prior receipt cannot decide that context safe on the reviewer's behalf.
   Required independent friend reviews remain required. A repeated review records its trigger rather than silently charging
   the task twice. A shorter report is never a quality waiver.
6. **Measure the whole operational unit.** Include preparation, parent and descendants,
   coordination, review, retries and repair. Exclude building the optimisation tool itself;
   mixed unallocated sessions remain incomplete. Native totals, cache subsets and reasoning
   subsets follow their source semantics; never sum overlapping counters twice. Preserve raw
   observations and immutable rate references. Unknown usage or rates block a complete saving
   claim, not unrelated authorised work.
7. **Promote evidence, not enthusiasm.** Automatic trial-to-adopt promotion requires matching
   acceptance scope, completed coverage, passing quality and the predeclared tolerances with
   referenced results. A cheap model rate, shorter response, fewer emitted bytes, lower tokens
   per model response or faster wall clock alone cannot satisfy it. Keep total operational
   tokens per accepted unit, comparable cost per accepted unit, and weighted cost per million
   tokens separate; never average model prices without their token weights. Cash, estimated
   marginal and virtual cost use separate columns and consistent scope.
8. **Respond to regressions.** A measured tolerance breach suspends new automatic routing under
   that trial and returns to an eligible approved policy, recording the reason. It does not
   erase failed attempts, terminate live work blindly, relax quality, or manufacture consent.
   No eligible fallback leaves an explicit scheduling blocker for the coordinator.

### Cache-aware context policy

The measurement group also pins provider token-category semantics, service tier and
long-context thresholds. Unknown tier or cache-write semantics leaves actual priced cost unknown. A separate scenario
may show an explicitly assumed tier, token semantics and immutable rate revision; label its
assumptions and any applicable range. It never fills missing fields in the actual record,
satisfies complete-cost coverage, or qualifies automatic adoption. A public API reference
scenario is not a subscription charge or plan-usage measurement.
Optimise cached-input volume as well as hit rate: repeated large prefixes still incur a charge.
Record request count and input-size distribution alongside accepted units, without substituting
either for completed work.

Context refresh is a policy decision, not an unconditional timer. The friend policy stores a
`:context-mode` (`:retain`, `:trial-refresh`, or `:verified-refresh`), the refresh adapter capability
reference, and a revision-bound decision artifact containing the chosen action, checkpoint,
comparison inputs, assumptions and quality gate. Validated `config --intake` owns changes to these
fields under the same friend-subject event. The dispatch admission validator refuses an automatic
refresh lacking that artifact, capable adapter, or applicable trial/adoption evidence; it does
not pretend to reset a host that exposes no reset operation. A bounded trial can investigate
unknown costs, but unknowns cannot satisfy a verified-refresh gate. Compare retained-context cost
against checkpoint creation, new-prefix processing/cache writes, expected subsequent reads and
any recovery/review cost. Trial stable compact instruction/tool prefixes with task-specific deltas
at the end where the harness supports this. Preserve required safety and tool schemas. A new
worker does not guarantee a cache hit. Do not rewrite prefixes repeatedly to save a few bytes,
keep a giant context merely for its hit rate, or infer that a reset saves money without including
its cache rebuild. Adapter support for caching controls is explicit; nova-work never claims to
control settings a host does not expose.

### Batch the round trips, preserve urgency

CONFIG bounds each coordinator batch by records, bytes and maximum delay. Accumulate
independent ready results and questions until one bound is reached, then present one focused
revision-bound packet. Reuse the existing read-bundle, independent-batch and atomic-batch
semantics; no new transaction protocol is implied. Dependent work still waits for its prerequisite,
and urgent corrections, stop requests, lease changes and deadlines bypass the delay. Do not
inflate a prompt just to fill a batch. Each packet manifest references the task or required
shared instructions for every included fragment and records its digest and bytes. The packet
builder admits only those fragments; its validator rejects unreferenced padding and excess
bounds. Whether a referenced fragment is necessary remains reviewer judgment, recorded in the
packet review: the byte validator cannot establish semantic relevance. Empty or unchanged
batches require zero model calls.

Measure batches by accepted work, total actor tokens and priced cost, with queueing latency and
quality beside them. Record model round trips, API requests and work units separately: reducing
one is not proof the others fell. Distinguish programmatic tool-call batching, result aggregation
and provider batch billing. A provider batch discount applies only to a supported, authorised
route and eligible nonurgent work; tool calls in one shell invocation do not earn that discount.
A large batch must stay inside input and context-tier limits, with partial failures and retries
attributed once. Compare pooled work against the same unbatched acceptance scope; preserve a
safety margin for latency and head-of-line blocking rather than maximising batch size blindly.

### Required enforcement replays

| Replay | Required outcome |
| --- | --- |
| policy-round-trip-and-replay | Policy, trial manifests and execution references survive export/import, restart, undo rules and revision replay; malformed intake has no partial effect. |
| packet-and-route-gates | Oversized/history-disallowed packets, reserved or stale routes and unexplained costly escalation refuse before dispatch; a valid scoped exception is retained. |
| bounds-are-not-prompts | A launcher lacking a required hard limit refuses automatic dispatch; a supported deadline returns a terminal or unresolved handle without duplicate execution. |
| quiet-until-actionable | Unchanged observations cause zero model dispatches; actionable batching respects bounds and urgent corrections/stops bypass it. |
| reuse-only-valid-review | Same-scope review is reusable; changed acceptance/dependencies invalidate it; independent friend gates cannot be replaced by reuse. |
| complete-cost-lineage | Parent/child/retry receipts join once, failed attempts count, cache subsets do not double count, implementation cost stays separate, gaps remain unknown. |
| batch-with-bounds-and-urgency | Independent results coalesce within byte/record/delay bounds, unchanged batches cause no call, unreferenced padding refuses, urgent corrections bypass delay, dependencies and partial retry identities survive. |
| cache-aware-context-choice | Cache reads/writes and tier thresholds price separately; a reset includes rebuild costs and refuses missing decision/adapter/evidence; a lower hit rate can still win when total matched-work cost falls. |
| evidence-before-adoption | Missing baseline/coverage, unmatched quality or a retrospective correlation alone cannot auto-promote; a fully qualified prospective result can. |
| regression-and-recovery | A breached trial stops new automatic assignments; eligible fallback preserves role limits, history and uncertain live handles. |

These acceptance items belong to the existing configuration, execution, measurement and recovery
roadmap features. They add no verified completion until implementation and failure replays pass.

## Efficiency: lessons absorbed 2026-09-15 *(Glenn; Emma, Stella)*

Before implementing the resident session and execution slices of `nova-work`, the owner requires that
all operational efficiency and token optimization lessons learned from the pilot bench and real multi-agent
coordination are absorbed as normative spec constraints and fences. These rules guard against token
burn and serial bottlenecks below the model.

### Rationale: Gas Town token facts

The rationale rests on measured facts from Gas Town:
- Root-only step records cut operational row counts fifteen-fold compared to fine-grained event emission.
- Patrol agents cycling on an unconditional timer became the dominant serial token bottleneck.
- Checklist steps read inline within task descriptions rather than materialized as full graph records conserve working memory and prevent graph explosion.

### The absorbed contract rules

1. **`prime` projection.** `prime` is a read-only projection (including current goal, applicable notes,
   caller leases, and pending stop requests) bounded under `--max-bytes`, run by a configurable
   harness hook at session start and immediately before compaction; it creates and maintains no shadow state.
2. **`decompose --pour` discipline.** Subtasks remain inline checklists by default. A child node in O
   materialises only for independent verification, independent worker assignment, an explicit dependency
   edge, or an isolated recovery boundary; unpoured checklist items never count in `|O|` and never count
   as verified.
3. **`:max-attempts` and `tripped=`.** Node attempts are bounded by `:max-attempts` (default 3), surfacing `tripped=`
   as a status reading; taking a lease on a tripped node requires an explicit `--reason`, which explains
   the operator's intent and grants no execution authority by itself.
4. **Delegate mode.** A declared harness role profile restricts the worker to designated verbs and
   read-only git operations; file edits and build execution are refused below the model by the sandbox
   and tool layer, with role transitions permitted strictly by configuration.
5. **Stale pass fields.** The stale pass surfaces read-only inspection fields `escalated-age=` and
   `reread=` derived from a persisted escalation event; these fields are strictly informational and never
   perform automatic reassignment.
6. **Durable triggers.** A durable `next-trigger` is maintained per waiting item (owner delivery, job handle,
   review completion, or due checkpoint), ensuring that an empty pulse reruns nothing.
7. **Scoped review coverage.** Review coverage is strictly bound to source revision and changed scope,
   and is never carried across an unchecked rebase.
8. **Implementation hygiene from W1 onward.** Every implementation slice from W1 onward adheres to
   strict prompt and context hygiene: fresh minimal context per slice, discrete cards for bounded work,
   exactly one read per exact commit head, machine-generated test receipts, and zero whole-history prompts.
9. **`:effort` bound on every card and delegation packet.** Beside bytes, attempt count and milliseconds,
   every card and delegated packet carries an explicit `:effort` bound (a small integer scale per task
   class stating how many reads, tool calls and how wide a fan-out the objective is worth); it is stated
   by the coordinator when the card is cut, and a packet lacking it is refused. A worker past its effort
   limit stops and reports rather than widening on its own; only the coordinator may widen a card's
   `:effort`, and only with an explicitly recorded reason.
10. **Fleet-wide spend ceiling per day.** CONFIG maintains an explicit fleet-wide spend ceiling per day
    and per model family. When an automatic or delegated dispatch would cross that ceiling, it is refused
    with a line naming the configured ceiling and `local-spend=` for the bench's own spend; it polices
    the local bench rather than guessing other benches' spend. A fleet total needs the benches joined, a later slice.

### Required enforcement replays

| Replay | Required outcome |
| --- | --- |
| gas-town-efficiency-accounting | Root-only step records and inline checklists avoid node explosion; durable next-triggers ensure empty pulses cause zero model re-executions. |
| efficiency-lessons-gate | Prime read-only projection respects `--max-bytes`, unpoured checklist items never count in `|O|`, tripped nodes require `--reason`, delegate mode refuses edits below the model, packets lacking `:effort` are refused, and dispatches crossing the configured daily fleet spend ceiling are refused. |

These acceptance items belong to the existing measurement, configuration and release roadmap features
(E10-F02 and E10-F05). They add no verified completion until implementation and failure replays pass.

## The hierarchy, the table and the roadmap's own record *(Stella, `docs/SPEC-WORK-PILOT.md` at `81c2885`)*

**Repository → epic → feature → subtasks is one example of Glenn's recursive hierarchy**,
with optional, repeated project and stream groups and recursive sub-features and subtasks.
Containment is **stored and queried rather than inferred by a renderer from a name**;
a roadmap is a durable view over that structure, not a required containment level. Optionally
a feature splits on another named dimension — a language, a platform, a
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

**For now nova-work owns the data, the queries and the render, and a second command is a
packaging question and never a second owner** (Stella, `docs/SPEC-WORK-PILOT.md` at `81c2885`,
restored in draft 27 — it was dropped by draft 26's integration and it contradicted nothing). A
future `nova-roadmap` may be a **thin rendering client** if that makes adoption easier, but **it
cannot own a second work set, a second progress state or a second evidence ledger**; that
packaging is decided only after the integrated workflow has been tried, and no extra service is
required for any of it.

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
read walk goes through the five read-path indexes (all but the reverse roadmap, which serves the
roadmap walk) — id to node, containment adjacency, reverse dependency, repository and category.
**A seventh is opened rather than built or loaded: C's closed index, additional to the six**, which the
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
resident bytes, segment bytes read, parses, replays and emitted bytes **do not move**; index
pages read stay **bounded by the index depth** — the `pages=<n>` bound of the bounded indexed
access promise above — and may grow with the depth, never with the volume within one depth
(replay `history-grows-startup-does-not`, and
Stella's `SPEC-WORK-CLOSED.md` acceptance 6,
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
`OPERATION`, `SAVEPOINT`, `UNDO`, `REDO`, `FRIEND`, `CONFIG`, `MODEL`, `OBSERVE`, `GOAL`, `MACHINE`,
`OFFER`, `ACKNOWLEDGE`, `DECLINE`, `EXECUTION`, `ALLOC`, `PROBE`, `WORK`, `VERIFY`, `QUERY`, `RENDER`, `LOAD`, `NODE`, `DECOMPOSE`, `DEP`, `AXIS`, `CELL`,
`ROADMAP`, `PRIORITY`, `RESPONSIBLE`, `ACCEPT`, `SOURCE`, `LEASE`, `HEARTBEAT`, `RELEASE`, `ATTEMPT`, `EVIDENCE`,
`ATTESTED`, `STATE`, `CORRECT`, `EVENT`), the second is `OK` or `FAIL`, `RACED` for a push the base predicate
refused (SPEC-MERGE rule 21's shape, exit 1, nothing pushed), or one of the informational
tokens `ROW`, `NOTE` and `MORE`. `OK`, `ROW`, `NOTE` and `MORE` go to stdout; `FAIL`, `RACED`
and refusals go to stderr. Every count line prints on failure as on
success. Every `OK` line ends `emitted=<bytes>`. Every mutation's `OK` line carries the
event's id, its request id, the session's local revision after it (`rev=<n>`), and
`pushed=<rev|->`, the clipped revision, the same number as the last `CLIP OK`'s `pushed=`;
`changed=<n>` records how many effective mutations the event produced.
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
EXPORT OK state=true session=<path> operation=<id|-> at=<rev> manifest=<sha256> members=<n> bytes=<n> complete=<true|false> into=<path> pushed=<rev|-> emitted=<bytes>   (--state: printed by `operation wait --id <id>` under --session, the export itself printing OPERATION OK; operation=- under --snapshot; complete=false names a declared omission, never a gap)
EXPORT FAIL state=true session=<path> operation=<id|-> at=<rev> into=<path> pushed=<rev|->: <reason>   (--state: a revision outside retention, a missing closure member, a proof gap, an existing destination, a bound breached: nothing published)
LOAD OK from=<path> at=<rev> manifest=<sha256> snapshot=<path> cache=<path> members=<n> emitted=<bytes>   (state load: no session, no rev=, no pushed=)
LOAD FAIL from=<path> at=<rev|-> manifest=<sha256|->: <reason>   (a manifest, digest, member-set, closure or schema failure: nothing materialised)
REPLAY OK from=<path> requests=<n> applied=<n> refused=<n> pushed=<rev|-> shown=<n> emitted=<bytes>   (refused=0, exit 0)
REPLAY FAIL from=<path> requests=<n> applied=<n> refused=<n> pushed=<rev|-> shown=<n> emitted=<bytes>   (refused > 0, exit 1, the same fields on stderr)
REPLAY ROW request=<id> verdict=<applied|refused> rev=<n>: <reason>
HANDOFF OK session=<path> generation=<n> to=<name> commit=<sha> pushed=<rev> emitted=<bytes>
HANDOFF RACED session=<path> generation=<n> to=<name> expected=<sha12> found=<sha12>
HANDOFF FAIL session=<path> generation=<n> to=<name> pushed=<rev|->: <reason>   (a push refused other than by the base predicate, SPEC-MERGE rule 21's BLOCKED case among them)
ATTESTED OK id=<event-id> request=<id> node=<id> rev=<n> pushed=<rev|-> criterion=<id> against=<sha> emitted=<bytes>
CLIP OK session=<path> operation=<id> boundary=<request-id> events=<n> base=<sha> commit=<sha> pushed=<rev> attempts=<n> emitted=<bytes>   (printed by `operation wait --id <id>` when the transport settles, and by `session stop`, which waits for its own; `clip` itself prints OPERATION OK)
CLIP RACED session=<path> operation=<id> boundary=<request-id> generation=<n> expected=<sha12> found=<sha12>
CLIP FAIL session=<path> operation=<id> boundary=<request-id> events=<n> base=<sha> pushed=<rev|-> attempts=<n>: <reason>   (a whole validation that found anything is `findings=<n>`, its `WORK FAIL` lines printed above it)
WORK OK nodes=<n> edges=<n> events=<n> leases=<n> expired=<n> escalated=<n> stale=<n> scope=<rev> source=<sha|-> pushed=<rev|-> emitted=<bytes>
WORK FAIL <id>: rule <n>: <reason>
WORK FAIL nodes=<n> findings=<n> shown=<n> expired=<n> escalated=<n> stale=<n>
VERIFY OK pointers=<n> verified=<n> unverified=<n> stale=<n> fetched=<n> cached=<n> pushed=<rev|-> emitted=<bytes>
VERIFY ROW <event-id> pointer=<p> verdict=<verified|unverified|stale> at=<stamp>
VERIFY FAIL pointers=<n> verified=<n> unverified=<n> stale=<n> fetched=<n> cached=<n> pushed=<rev|-> shown=<n>
QUERY OK ask=<kind> scope=<rev> membership=<rule> branch=<open|closed|root> unit=<unit> source=<sha|-> freshest=<stamp|-> done=<n> done-unverified=<n> unknown=<n> deferred=<n> cancelled=<n> superseded=<n> stale=<n> required=<n> since-baseline=<n> private=<n> open=<n> closed=<n> gap=<n> [from=<stamp> to=<stamp> closed-in=<n> settles-in=<n> revives-in=<n> items-in=<n>] [green=<k> applicable=<n> baseline-rows=<n0> row-kind=<kind>] [held-not-worked=<n> unowned=<n>] [leases=<n>] [responsible=<name|->] pushed=<rev|-> rows=<n> shown=<n> pages=<n> parses=<n> replays=<n> emitted=<bytes>
QUERY ROW <id> kind=<k> state=<s> k=<n> n=<n> unknown=<u> responsible=<name|-> holder=<name|unowned> heartbeat=<age|none> deadline=<stamp|-> escalated-to=<name|-> blocked-by=<id|->
QUERY ROW <id> kind=<k> state=<s> ready=<true|false> reason=<text|-> resolver=<name|-> priority=<rank|default> priority-source=<id|default> priority-context=<self|subtree|default> responsible=<name|-> holder=<name|unowned>   (ready; the three priority fields read the same under either --order)
QUERY ROW <id> lease=<lease-id> holder=<name|unowned> heartbeat=<age|none> deadline=<stamp|-> default=<release|extend-once|escalate:<name>|-> escalated-to=<name|-> responsible=<name|->   (who, stale)
QUERY ROW <id> branch=<open|closed> disposition=<pending|working|deferred|done|cancelled|superseded|removed> repo=<o/n|-> kind=<k> state=<s> landed=<sha|-> released=<version|-> holder=<name|unowned> settled=<stamp|-> evidence=<n> verified=<n> responsible=<name|->   (done, remaining and under, under --branch closed or --branch root)
QUERY NOTE coverage-gap file=<name> range=<rev>-<rev>   (rows whose bodies the retention archive holds, or whose day partition or manifest the committed root names, and this read could not reach)
QUERY ROW <lease-id> node=<id> kind=<lease|heartbeat|release|handoff> rev=<n> at=<stamp> from=<name|-> to=<name|-> deadline=<stamp|-> default=<release|extend-once|escalate:<name>|->   (handoffs)
QUERY FAIL ask=<kind> rows=<n> shown=<n>: <reason>
QUERY ROW <machine-id> kind=machine name=<text> owner=<name> roles=<build,test,profile> admits=<kind|-> concurrent=<n|-> arch=<text|-> os=<text|-> declared-by=<name> declared-at=<stamp>   (fleet)
QUERY FAIL ask=fleet rows=0 shown=0: <id> excludes <kind>   (--for with --node on a member that excludes the kind: a refusal, never an empty answer)
QUERY FAIL ask=<kind> as-of=<stamp> partition=<yyyy-mm-dd>: historical window unavailable
QUERY FAIL ask=<kind> after=<cursor> pinned=<rev> current=<rev>: page expired   (a continuation whose captured revision the session can no longer serve; never a drifted page)
QUERY MORE rows=<n> shown=<n> pages=<n> after=<cursor>   (a page budget met: shown=0 is permitted when no row could yet be emitted; the continuation is the one cursor rule and never a second)
OPERATION OK id=<id> op=<capture|stage|export|clip|execution> state=<queued|running|done|cancelling|cancelled|failed> started=<stamp> updated=<stamp> staged=<bytes> rev=<n|-> pushed=<rev|-> shown=<n> emitted=<bytes>
OPERATION ROW id=<id> op=<kind> state=<s> started=<stamp> updated=<stamp> external=<known|uncertain|none>   (operation list, and one per event of a wait's cursor)
OPERATION NOTE waiting id=<id> timeout=<duration> after=<cursor>   (a wait that timed out: the operation is still running, and this line says so)
OPERATION FAIL id=<id> op=<kind> state=<s>: <reason>
OPERATION FAIL id=<id> op=- state=-: no such operation   (an id the journal does not hold: exit 2, never an invented state)
SAVEPOINT OK id=<id> rev=<n> checkpoint=<rev|-> pushed=<rev|-> boundary=<rev> age=<duration> unshared=<n> manifest=<sha> shown=<n> emitted=<bytes>   (rev= is this bench's savepoint, checkpoint= the newest clipped one, so a local success can never be read as a shared backup)
SAVEPOINT ROW id=<id> rev=<n> at=<stamp> manifest=<sha> verdict=<good|corrupt|unverified|failed>   (failed: an attempt whose image, manifest or sync did not complete, listed and never restored)
SAVEPOINT NOTE recovery-gap kind=<missing-tail|torn-tail|corrupt-record|coverage-unverified|remote-unavailable> since=<rev>   (reported, never rounded to success; torn-tail is an interrupted append at the end of the journal, corrupt-record anything else, and neither is truncated)
SAVEPOINT FAIL id=<id> rev=<n>: <reason>   (cut inside an envelope, journal mismatch, image revision differs from its cut, missing original reply: nothing published, the previous verified savepoint kept)
UNDO OK id=<event-id> request=<id> request-of=<id> nodes=<n> rev=<n> pushed=<rev|-> emitted=<bytes>   (redo prints REDO OK with the same fields)
UNDO ROW node=<id> effect=<state|scope|assignment|counter|verification> before=<text> after=<text>   (an undo-plan's or redo-plan's rows; redo-plan prints REDO ROW)
UNDO FAIL request-of=<id> expect=<rev> current=<rev>: stale plan   (nothing written)
UNDO FAIL request-of=<id> effect=<external> handle=<text>: not reversible here   (a sent message, a paid execution, a publication, a source deletion)
CONFIG OK friend=<name> verdict=<unchanged|manifest|delta> base=<hash|-> revision=<n> hash=<hash> parts=<n> emitted=<bytes>   (--request and --export: no event, no id, no rev=)
CONFIG OK id=<event-id> request=<id> friend=<name> base=<hash|-> revision=<n> hash=<hash> parts=<n> rev=<n> pushed=<rev|-> emitted=<bytes>   (--intake alone, the mutation form, its :config event by the kinds above)
CONFIG FAIL friend=<name> base=<hash|-> verdict=<schema|identity|hash|incomplete>: <reason>   (the old config is left intact)
FRIEND OK id=<event-id> request=<id> friend=<name> change=<register|retire|role|participation|capability|limit> rev=<n> pushed=<rev|-> emitted=<bytes>
MACHINE OK id=<event-id> request=<id> machine=<id> change=<register|retire|permit|exclude|limit|fact> rev=<n> pushed=<rev|-> emitted=<bytes>
MACHINE FAIL machine=<id|->: <reason>   (no owner, no id, unknown owner, connect held by <id>, credential in record, unknown role, fact without provenance: nothing written, the value never echoed)
MODEL OK id=<event-id> request=<id> model=<id> change=<register|rate|evidence> rev=<n> pushed=<rev|-> emitted=<bytes>
OBSERVE OK id=<event-id> request=<id> friend=<name> change=<state|attempt> rev=<n> pushed=<rev|-> emitted=<bytes>
GOAL OK id=<event-id> request=<id> scope=<scope> goal=<id|-> change=<set|clear|progress|evidence|blocked|stop> kind=<goal|transition|evidence> rev=<n> pushed=<rev|-> emitted=<bytes>   (goal set and goal update: change= is the form the caller used, evidence for --progress with the evidence triple and progress for --progress alone; kind= is the event written, :goal for set and clear, the node's own :transition or :evidence for update)
GOAL OK scope=<scope> goal=<id|-> rev=<n> pushed=<rev|-> generation=<n> scope-revision=<n> state=<s> owner=<name|-> stop=<none|requested|cancelled|deferred> constraints=<n> notes=<n> outstanding=<n> rows=<n> shown=<n> emitted=<bytes>   (goal show: no event, no id=; stop= derived from state=)
GOAL ROW kind=<objective|criterion|constraint|note|progress|blocker|lease|attempt|link> <the fields its kind's own row carries above: a criterion row is ACCEPT's, a constraint row is *Delegation*'s applicable row byte for byte, a lease row is QUERY's who row>
GOAL MORE rows=<n> shown=<n>   (constraint rows and the stop are never among the cut)
GOAL FAIL scope=<scope> goal=<id|-> expect=<rev> current=<rev>: stale   (nothing written)
GOAL FAIL scope=<scope> goal=<id|->: <reason>   (no such node; disposition=<done|cancelled|superseded|removed>; not a writer of the scope; no edge; stop requested; notes index unloadable — each named, exit 1, nothing written)
NOTES OK id=<event-id> request=<id> note=<id> change=<write|supersede> superseded=<id|-> scope=<scope> kind=<instruction|observation|heuristic> active=<n> rev=<n> pushed=<rev|-> emitted=<bytes>   (notes write and notes supersede; note= stands where node= would; a supersede's two events share one request= and one rev=)
NOTES OK scope=<scope|-> task-class=<class|-> candidates=<n> eligible=<n> excluded=<n> unknown=<n> rev=<n> from=<live|snapshot> rows=<n> shown=<n> emitted=<bytes>   (notes list and notes applicable: no event, no id=; from=snapshot admits no route)
NOTES ROW candidate=<role>/<model> verdict=<eligible|excluded <note-id>|unknown>   (applicable: one per candidate named, never among the cut, printed before every note row)
NOTES ROW note=<id> kind=<instruction|observation|heuristic> date=<utc> author=<name> state=<active|superseded-by <id> <date>> constraint=<present|-> <first line of :text>   (list and applicable; capped by --max)
NOTES MORE rows=<n> shown=<n>   (verdict rows are never among the cut)
NOTES FAIL note=<id|-> expect=<rev> current=<rev>: stale   (nothing written)
NOTES FAIL note=<id|->: <reason>   (missing :source or :date; kind not one of three; not a writer of the scope; already written note=<id>; active=<n> past :max-active=<n>; <field>=<n> past <bound>=<n>; unknown task class; :allow in constraint; not active: superseded-by <id>; kind weaker than <id>; past --max-bytes; notes index unloadable; no live session and no --snapshot — each named, exit 2, nothing written, and no candidate printed eligible)
OFFER OK id=<event-id> request=<id> node=<id> offer=<id> attempt=<id> to=<name> effect=dispatched reserved=<n> until=<stamp> rev=<n> pushed=<rev|-> emitted=<bytes>
ACKNOWLEDGE OK id=<event-id> request=<id> node=<id> offer=<id> attempt=<id> stage=<received|accepted> effect=<received|accepted|accepted-held|late|duplicate> lease=<lease-id|-> reserved=<n> committed=<n> rev=<n> pushed=<rev|-> emitted=<bytes>   (lease= names the lease that actually holds, created or bound; a late or duplicate line carries its original lineage in offer= and attempt=)
DECLINE OK id=<event-id> request=<id> node=<id> offer=<id> attempt=<id> effect=<declined|late|duplicate> released=<n> rev=<n> pushed=<rev|-> emitted=<bytes>
OFFER FAIL node=<id> offer=<id>: <reason>   (malformed id, stamp or digest; offer or attempt id reused; profile not <name>'s at <revision>; model not admissible; payload digest mismatch; reserve not positive; capacity <n> < reserve <n>; stale generation; predecessor not this node's; held by control <id>; holder conflict with <name> — each named, exit 1, nothing written)
ACKNOWLEDGE FAIL node=<id> offer=<id> reply=<id>: <reason>   (provenance unverified; tuple mismatch; invalid stage; no received receipt; conflicting bytes for receipt <id> — nothing written; DECLINE FAIL the same shape)
EXECUTION OK id=<event-id> request=<id> control=<id> change=<pause|stop|resume|correct|reconcile> scope=<selector> operation=<id|-> selected=<n> rev=<n> pushed=<rev|-> emitted=<bytes>   (durable intent and an anchored capture, never a stopped fleet; operation= is the directives' transport, - for a release-hold and a reconcile)
EXECUTION OK control=<id> change=<c> selected=<n> pending-delivery=<n> acknowledged=<n> confirmed=<n> unsupported=<n> unresolved=<n> rev=<n> pushed=<rev|-> shown=<n> emitted=<bytes>   (execution status: no event, no id=)
EXECUTION ROW control=<id> node=<id> offer=<id> attempt=<id> generation=<n> disposition=<pending-delivery|acknowledged|confirmed|unsupported|unresolved> observed=<running|paused|stopped|completed|not-started|unsupported|unknown|-> at=<stamp|->
EXECUTION FAIL control=<id> change=<c>: <reason>   (unknown selector; capture pin unrepresentable; instructions unverified; stale generation; unsupported before send; no such control — exit 1, nothing written, no hold installed)
CORRECT FAIL node=<id>: execution live, use execution correct   (an attempt of the node live or uncertain: the bare verb bypasses no barrier)
RENDER OK view=<id> projection=<id|-> scope=<rev> cells=<n> private=<n> bytes=<n> target=<owner/name:path|-> was=<sha256|-> now=<sha256|-> pushed=<rev|-> emitted=<bytes>   (--file and --check; a --chat success prints no line and carries its bytes as the wire's one `artifact`, by the #293 fold)
RENDER FAIL view=<id> projection=<id|-> cells=<n> private=<n> drifted=<n> target=<owner/name:path|->: <reason>   (no mapping, target identity, a path outside its root, a marker missing, duplicated or reversed, a hash moved, an artifact past its bound: the target untouched)
NODE OK id=<event-id> request=<id> node=<id> rev=<n> pushed=<rev|-> change=<add|edit|move> changed=<n> [from=<id> under=<id>] emitted=<bytes>   (from= and under= on a move alone)
NODE FAIL node=<id>: <reason>   (node move: parent conflict, cycle, not movable, repository change, roadmap operation required, active context change, privacy reduction, unavailable; node add: repo outside root, root needs a repo, root needs a work set, repo held by <id>, bad link; node edit: all keep, version on a <kind>: nothing written, no private value printed)
ROADMAP OK id=<event-id> request=<id> node=<id> rev=<n> pushed=<rev|-> change=<create|configure|row-add|row-remove|projection-add|projection-remove> changed=<n> emitted=<bytes>
ROADMAP FAIL node=<id>: <reason>   (layout populated, bad patch, all keep, row kind mismatch, has axes, unknown member, duplicate projection, no such projection, bad selection: nothing written)
AXIS OK id=<event-id> request=<id> node=<id> rev=<n> pushed=<rev|-> change=<add|remove> member=<id> cells=<n> emitted=<bytes>   (cells= the coordinates a remove retired, 0 on an add)
PRIORITY OK id=<event-id> request=<id> node=<id> rev=<n> pushed=<rev|-> change=<set|clear> context=<self|subtree> rank=<n|-> changed=<n> emitted=<bytes>
NODE NOTE already-closed node=<id> disposition=<d> settled=<stamp>   (a same-id retry returns its prior disposition and writes nothing; a fresh-id repeat while still closed appends one typed no-effect receipt with changed=0)
<MUTATION> OK id=<event-id> request=<id> node=<id> rev=<n> pushed=<rev|-> changed=<n> ... emitted=<bytes>
<MUTATION> OK id=- request=<id> node=<id> rev=- pushed=<rev|-> changed=<n> dry-run=true ... emitted=<bytes>
<MUTATION> FAIL node=<id>: rule <n>: <reason>
<MUTATION> FAIL node=<id> expect=<rev> current=<rev>: stale
<MUTATION> FAIL request=<id> applied=<rev>: already applied
<MUTATION> FAIL request=<id>: reused with a different payload
<MUTATION> FAIL request=<id> page=<name>: dedup unavailable   (a dedup page the predicate needs and could not read: the request is refused admission, never admitted as new)
<MUTATION> FAIL request=<id> key=<kind> bytes=<n> past <--page-bytes|--max-bytes>=<n>: indivisible   (exit 2: one key with its one locator no page could hold, or one journal record the reader's bounds could not read back, refused at admission, nothing journaled; a growing record splits instead)
<MUTATION> FAIL request=<id> journal=<path>: journal uncertain   (an append or sync that failed: nothing admitted until the tail is read and diagnosed, and the tail is never truncated)
<MUTATION> FAIL node=<id> findings=<n> was=<n>: no repair   (--repair only)
ALLOC OK id=<event-id> request=<id> machine=<id> allocation=<id> slot=<n> node=<id> batch=<id> offer=<offer-id> attempt=<attempt-id> machine-generation=<n> allocation-generation=<n> rev=<n> pushed=<rev|-> changed=<n> emitted=<bytes>
ALLOC FAIL machine=<id> slots=<n|-> holder=<name|->: <reason>   (capacity, excludes, stale machine generation: exit 1, nothing written)
ALLOC FAIL machine=<id> allocation=<id>: stale token   (allocation generation or machine generation mismatch: exit 1)
ALLOC FAIL machine=<id>: suspect since=<stamp>   (allocation suspect, renewal and take refused: exit 1)
ALLOC FAIL machine=<id>: not fenced   (no verified termination: exit 1)
ALLOC FAIL machine=<id>: allocator held   (second allocator for one machine: exit 1)
ALLOC HEARTBEAT OK allocation=<id> machine=<id> machine-generation=<n> allocation-generation=<n> rev=<n> pushed=<rev|-> changed=<n> emitted=<bytes>
ALLOC RELEASE OK allocation=<id> machine=<id> slot=<n> freed=<true> rev=<n> pushed=<rev|-> changed=<n> emitted=<bytes>
ALLOC ROW allocation=<id> machine=<id> slot=<n> holder=<name> node=<id> batch=<id> offer=<offer-id> attempt=<attempt-id> age=<duration>
PROBE OK machine=<id> slot=<n|-> fact=<observed|absent> at=<stamp> source=<pointer>
LEASE FAIL node=<id> holder=<name> since=<stamp> deadline=<stamp> live=<n>: held
<TOKEN> NOTE <caveat>
<TOKEN> MORE kind=<rule|row> shown=<n> total=<t> <remedy>
nova-work <build identity> <goos>/<goarch> <go version>
```

where `<MUTATION>` is one of `NODE`, `DECOMPOSE`, `ACCEPT`, `SOURCE`, `DEP`, `AXIS`, `CELL`,
`ROADMAP`, `PRIORITY`, `RESPONSIBLE`, `LEASE`, `HEARTBEAT`, `RELEASE`, `ATTEMPT`, `EVIDENCE`, `ATTESTED`, `STATE`,
`CORRECT`, `EVENT`, `UNDO`, `REDO`, `FRIEND`, `MODEL`, `OBSERVE`, `MACHINE`, `OFFER`, `ACKNOWLEDGE`,
`DECLINE`, `EXECUTION` and `CONFIG` (its `--intake`
form alone), `GOAL` (its `set` and `update` forms). **Nine of them name no node, and their lines are written out above rather than left
to `node=`**: `UNDO` and `REDO` print `nodes=<n>`, `FRIEND`, `OBSERVE` and `CONFIG --intake`
print `friend=<name>`, `MODEL` prints `model=<id>`, `GOAL` prints `scope=<scope> goal=<id|->`, `MACHINE` prints `machine=<id>`, and `EXECUTION` prints `control=<id>` — each the subject its `:event` kind above
names, each still carrying `id=`, `request=`, `rev=` and `pushed=`, so the once-only retry
promise reads the same for them as for every other mutation. The rest each
add the fields their section names (`LEASE OK … holder= deadline= default= live=`, `STATE OK
… from= to= evidence=`, `ATTEMPT OK … by= result= generation=`, `EVIDENCE OK … criterion=
against=`, `CORRECT OK … generation=`, `EVENT OK … kind= scope=`, and `DECOMPOSE OK …
children=<n> unit=leaves leaves-before=<n> leaves-after=<n> unit=features features-before=<n>
features-after=<n>`, which is the both-units promise of the scope section printed).
**Every token is its verb uppercased but four, named here so no reader infers them**: `take`
prints `LEASE`, `attest` prints `ATTESTED`, `state load` prints `LOAD` (the `state` verb owns
`STATE`), and **a `release` refused because its `--as` is not
the lease's holder prints `LEASE FAIL`** — that refusal is about the lease it could not end —
while every `release` that runs prints `RELEASE OK` like any other verb's (Fable at 7472e545,
2026-09-13). **`pushed=<rev|->` is on every scope line** —
`SESSION OK`, `EXPORT OK`, `REPLAY OK`, `WORK OK`, `VERIFY OK`, `QUERY OK`, `RENDER OK` and
every mutation's — **and on no row**, where it would be one number repeated per line.

**`verify` prints exactly one count line**: `VERIFY OK` when `unverified=0` (exit 0) and
`VERIFY FAIL` when it is above zero (exit 1, on stderr), never both, with one `VERIFY ROW` per
evidence event under `--max` in either case. **`session stop` prints its clip's `CLIP OK` line
first** (unless `--no-clip`) — the clip is one long operation like any other, and `stop` reaches
that line by waiting on its operation id inside its own `--git-timeout`, never by a second
synchronous clip path — then one `SESSION OK` whose `owner=` is the name it released and
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
`node remove` leaving its subtree in O as provenance and out of every count; a re-baseline restating the derived set accepted and one differing from it
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
untouched; a removed subtree
written into the archive by the clip that carries its `:remove` past the boundary and absent
from the snapshot's structure thereafter, the live snapshot bounded by `--retain` across it;
a snapshot loaded as retention boundary plus retained events equalling a clean
reconstruction, and `--at` before the boundary refused naming the retention archive;
**a session started on a snapshot whose archive file is absent answering every ask of the
query table that does not reach for an archived body and running every rule of the validator**, with rule 11 and rule 14 green, and
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
the answer is the same under `--offline`**; **a request id whose event is newer than the
retention boundary and still in the snapshot's retained events retried against a successor after
a handoff, refused `already applied` and applying nothing** (the window the index alone did not
cover); **one
request digested to one value by two independent serializers, over a `node add` envelope holding
a structure event and a scope event, with two `:stamp`s and two `:request` ids and the same
digest, and with an absent optional field written `(:absent)` by both**; **a clip whose snapshot passes `--max-bytes` refused with all four
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
say which test holds which sentence**:

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
  no rule 18 finding; a fresh-id repeat while still closed appends one typed no-effect receipt with
  `changed=0` and prints one `NODE NOTE already-closed`; and a removal over a subtree holding a live lease is
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
  fixed while the old history grows by orders of magnitude: startup resident bytes, segment bytes
  read, parses, replays and emitted bytes **do not move**; index pages read stay **bounded by the
  index depth** — the `pages=<n>` bound of the bounded indexed access promise in *Cost* — and may
  grow with the depth, never with the volume within one depth, and no whole-C load, no directory
  scan and no whole-history dedup load anywhere
  on the ordinary path.
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
- **`pipeline-replies-are-correlated`** — pipeline two different queries, a mutation and a
  long-operation acceptance, then deliver their response frames out of order and in fragments:
  every response reaches only its matching request, and the operation id remains distinct.
  Include independent-batch `not attempted` entries and atomic-batch validation failures.
  Unknown, duplicate and absent response ids close the connection without falsely settling
  any outstanding request; malformed input with no decodable id receives a null-id refusal
  and no admission. Reconnect after a lost mutation response and prove same-id reconciliation
  applies no second event. These are required implementation tests, not results already observed.
- **`disconnect-is-not-a-rollback`** — a client killed after its mutation was journaled: the event
  stands, the same request id and body returns the recorded disposition, and the same id with
  different arguments is refused; `rev=` and `pushed=` distinct in every response.
- **`no-effect-mutation-is-journaled`** — a mutation whose patches are all no-ops; the event id
  recorded, journal length +1, `changed=0` on its OK line, and the domain-projection **digest** unchanged
  while the event revision advances by one. The `lost-reply-then-reopen` fixture shows a retry of the
  same request id returns its recorded disposition unchanged; even after a reopen it leaves the
  reopened item untouched. A fresh-id invocation validates current state and gives its normal effect
  or refusal. A fresh-id while still closed is the no-effect case: one typed receipt, `changed=0`,
  with no second `:settle`, no detach, no scope, membership or counter change.
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
- **`dry-run-writes-nothing`** — a dry run that validates and projects without mutating; after a
  green preview at revision R, the `--request` id is still new to the dedup index by the preview,
  and the `SESSION OK` counters -- `events=`, `pending=`, `pushed=` -- are unchanged by the preview;
  an accepted mutation moves the revision to R+1; then a real apply `--expect R` is refused `stale`
  by name, but an apply at the current R+1 is newly validated and may succeed.
- **`undo-refuses-an-external-effect`** — an undo over a sent message, a paid execution, a
  publication and a source deletion refused and reported as an external effect with its own
  compensating workflow; shared Git history never reset as the undo path.
- **`every-field-has-an-owning-verb`** — every canonical field mapped to its owning typed mutation
  or marked derived or immutable, with no generic set-field escape hatch and no parallel alias.
- **`open-count-is-read-not-computed`** — mutate, then ask `|O|` repeatedly: zero visits, zero
  parses, zero replays, and the counter equal to an independent full count after a close, a reopen
  and an import replay; the open-issue and open-leaf counters separate and neither labelled `|O|`.
- **`no-friend-name-in-the-tool`** — **the binary, its defaults and its shipped fixtures**
  carrying no friend, bench, repository or house name; every identity arriving as configuration.
  **It is not a test of this document**, which cites Glenn, Stella, Johnny and the model reads by
  name on nearly every rule: provenance is what makes a requirement checkable, and a test that
  forbade it would forbid the citations this spec is built on.
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
- **`four-facts-four-verbs`** and **`offer-writes-intent-and-a-reservation`** — an admitted
  `offer` with declared free slots writing `:effect :dispatched`, a pending-offer index entry and
  a reservation, and leaving node state, W, the lease index, attempts, evidence and completion
  unchanged; `acknowledge --stage received` writing delivery only; nothing inferred from
  anything.
- **`a-receipt-needs-a-verifier`** and **`staged-admission-refuses`** — a copied note and an
  `--as <recipient>` with no verifier result refused with no canonical write; a verifier or
  payload reader that returns after a conflicting revision, or fails validation, writing no
  reservation, receipt, lease or W change while `session status` answers inside its bound.
- **`accepted-creates-one-lease-or-binds`** and **`no-shadow-lease-across-holders`** — an
  accepted receipt after `received` creating exactly one `:lease` and one W entry, or binding a
  second attempt to the same holder's lease with its deadline unchanged, converting and never
  doubling capacity, an absent observed model recorded unknown; a cross-holder offer or accepted
  reply refused or retained late and creating no lease.
- **`until-is-overdue-not-released`** and **`late-and-duplicate-receipts-are-retained`** — at
  `--until` and at lease expiry no duplicate launch and no stopped or completed claim, the
  reservation and any uncertain execution retained until reconciled; a late accept after a
  decline, a replacement, an expiry or a generation change retained `:late`, reviving no lease
  and overwriting no successor; the same request replaying its success, the same verified
  receipt under a new request consuming no capacity, conflicting bytes for one receipt id
  refused.
- **`stop-is-a-hold-not-a-cancel`** and **`one-stop-note-cannot-cancel-two-attempts`** —
  `execution stop --node` writing a hold and directives and no transition, `goal show` still
  printing `stop=none`; `state --to cancel-requested` then an `event --kind cancel` whose
  evidence covers one of two live attempts refused, the same with both covered admitted.
- **`hold-survives-a-crash`** and **`capture-survives-clip`** — a crash after the hold is durable
  and before capture or send recovering the same hold and target identities with no duplicate
  launch; a clip completing between the anchor and the manifest, the pin resolving the same
  revision and span, and an unrepresentable pin refusing admission before `EXECUTION OK`.
- **`no-dispatch-slips-past-a-hold`** and **`held-acceptance-converts-nothing`** — an offer
  prepared before a pause refused at the last send; a launch, a correction and a move raced
  against a scope pause all held; an acceptance under a hold retained `:accepted-held` with no
  lease, launch or release until reconciled, and a lift of the hold converting nothing.
- **`reconcile-preserves-contradiction`** — live, expired, pending, paused, unsupported and
  unreachable targets captured exactly through the indexes in bounded pages; duplicate, delayed
  and out-of-order receipts and a forged source inferring no ownership, release, cancellation or
  completion; two contradictory observations kept unresolved; a stop report carrying no
  synthesised zero usage; two attempts on one task stopped by two receipts, usage retained apart,
  the task counted once in W; and the holder-only `release` rule unbypassed by a confirmed exit.
- **`resume-is-two-actions`** — `release-hold` lifting only its control's hold with an
  overlapping hold still effective and no process resumed; `resume-workers` refusing an
  unsupported capability before sending and holding until a running observation, a delivery
  receipt alone releasing nothing.
- **`correct-is-a-linked-segment`** and **`bare-correct-refuses-under-execution`** — one
  envelope writing the hold, the `:correct` and the instruction binding, a retry bumping the
  generation once; a worker's old-generation usage kept as its own segment; the bare `correct`
  refused by name while an attempt is live and admitted once none is.
- **`endpoint-is-local-and-private`** — the session's directory created `0700` and its socket
  `0600`, both owned by the account that runs it, a pre-existing directory or socket with wider
  modes refused rather than reused, the Windows named pipe created with
  `FILE_FLAG_FIRST_PIPE_INSTANCE`, and no listener bound to any network address.
- **`four-capability-groups-and-three-fields`** — child agents, swarms, local models and
  one-shots each expressible as a capability group with a stable id, its source, its
  last-verified stamp, its availability and its constraints, and declared support, verified
  runtime and current free capacity kept as three fields that never collapse into one.
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
  of C**; completed rows still in the table and *remaining only* an explicit filter; **and then a
  referenced task reopened and a new sub-feature added, the affected rollups updated without
  dropping a completed member and without double-counting a shared prerequisite, with the summary
  and the expanded-row views exercised over the same ids** (Stella,
  `docs/SPEC-WORK-PILOT.md` at `81c2885`, restored in draft 27: draft 26 folded the first half of
  her acceptance and dropped the second).
- **`historic-tick-survives-a-source-change`**, **`regression-opens-repair-work`** and
  **`unrelated-receipts-stay-reusable`** — a changed source or criterion preserving the historic
  tick at its pinned revision while the current view requires re-verification, a confirmed
  regression creating linked open repair work, and unrelated receipts untouched.
- **`chat-and-file-render-are-byte-identical`** and **`render-refuses-a-target-outside-its-roots`**
  — one projection and revision rendering the same bytes to chat and to a marker region, every
  other byte preserved, a missing, duplicate or reversed marker pair refused, and a target outside
  the configured permitted roots refused rather than guessed.
- **`restore-is-isolated-and-dispatches-nothing`**, **`a-savepoint-is-not-a-shared-backup`**
  and **`compaction-keeps-the-last-copy`** — a restore taking no ownership, reanimating no
  assignment and replaying no message; savepoint age, the local and the shared revisions, unshared work
  and failed backups all readable, and a savepoint never printed where a checkpoint was asked for; and compaction never removing the only recoverable copy.

**Draft 27's replays, for the read at `08650d11` (5657782701)**:

- **`new-verbs-have-a-kind-and-a-field-order`** — `undo`, `redo`, `friend`, `model`, `observe`
  and `config --intake` each writing an event of its own kind, with every field of its list
  written in order and absent ones written `(:absent)`, `:node` written `(:absent)` on all six,
  and a second build serializing each one to the same bytes.
- **`new-verbs-retry-to-one-event`** — each of the six replayed twice under one request id
  yielding **one** event: the second call answered with the original `OK` line, applying nothing,
  across a clip and across a successor handoff; the same id with a changed payload refused
  `reused with a different payload`; and each `OK` line carrying the subject its kind names —
  `nodes=`, `friend=` or `model=` — and never an empty `node=`.
- **`absent-empty-and-null-are-three-spellings`** — one request giving no `:members`, one giving
  an empty list and one giving a wire `null` digesting to three values of which the first and the
  third are equal and the second differs; the same three surviving a wire round trip unchanged.
- **`clip-is-one-long-operation`** — `clip` returning `OPERATION OK id= op=clip` and exiting, the
  transport continuing, `operation wait --id` printing the `CLIP OK` line with that
  `operation=<id>` and its `pushed=`, a raced transport printing `CLIP RACED` through the same
  wait, and `session stop` printing the same `CLIP OK` first and then `SESSION OK` by waiting on
  its own operation inside `--git-timeout`.
- **`undo-names-its-reversible-set`** — every row of the reversible-verb table exercised: each
  reversible verb undone by the envelope the table names, each refused verb refused
  `not reversible here` naming itself, an undo over `event --kind cancel` and over `node remove`
  refused because both dispositions are terminal, and no undo reaching a terminal state by any
  path.
- **`add-field-order-is-complete`** — a `node add` with every one of the twelve fields given,
  one with each absent, one with `--links-empty` and one with `--clear-links`, digested by two
  serializers to one value each and to four distinct values; a fixture in the pre-fold order
  refused `schema revision unsupported` at load, never read as the new shape.
- **`repo-only-at-the-root`** — `--repo` with `--under-root open --type work-set` accepted and
  unique; the same `--repo` again refused `repo held by <id>`; `--repo` under a parent refused
  `repo outside root`; `node edit` and `node move` unable to change it.
- **`roadmap-has-one-creator`** — `node add --type roadmap` exit 2 naming `roadmap create`;
  `roadmap create` writing one node and one view in one envelope, a crash between them
  replaying all-or-none; no second alias.
- **`metadata-patches-preserve-intent`** — keep, clear, set-empty, set-false and set-value on
  each of the five fields round-tripping and digesting distinctly; an all-keep, a malformed tag
  and a wrong type refused with no event and no counter moved; `--version` set on a `:feature`
  refused `version on a feature` and its keep accepted.
- **`edit-is-atomic-and-replayable`** — a bad one-of-five patch writing nothing; an accepted
  mixed edit moving only its named fields and the category index; the same request id retried
  answered by its original `NODE OK`; a changed payload refused; an equal-value edit the
  no-effect receipt, `changed=0`, rev up by one.
- **`edit-undo-preserves-later-work`** — an edit undone restores `:before`; the same undo after
  an intervening edit refused conflict, both events standing.
- **`edit-never-fetches-a-link`** — a link that is a live URL to a counting endpoint added,
  edited and rendered with zero requests observed; a link holding NUL refused `bad link`; a
  refusal on a private node printing no value.
- **`move-keeps-every-count`** — a required subtree moved between two features: the source's
  and destination's required sets and open counts move by the subtree, the common ancestor's
  net count is stable, `|O|`, `|C|`, W and every task state unchanged, rule 11 green on the
  walk, and no whole-set scan (visits asserted).
- **`move-same-parent-is-a-receipt`** — `--from` equal to `--under` and true: the structure
  event alone, `changed=0`, sibling order unchanged; a lost reply retried yields one envelope;
  a different payload under the id refused; every refusal leaves both parents unchanged.
- **`move-refuses-by-name`** — a wrong `--from`, a destination inside the subtree, a root
  container, a repository root, a shared container, another repository, and a roadmap as either
  parent, each refused with its named reason and the identity, nothing written.
- **`move-keeps-the-lease`** — a working subtree moved with its effective `:responsible`
  unchanged keeps the same lease, attempt and usage; a move that would change it over an
  unreconciled attempt refused `active context change` naming the ids; a move under a public
  parent from a private one refused `privacy reduction`, the reverse admitted and the public
  render losing the rows.
- **`move-updates-every-roadmap-scope`** — a row referenced by two roadmaps outside both
  parent chains and one unrelated roadmap: both referencing scope revisions advance in the
  envelope, the unrelated one stays, a failed acceptance moves none, historical renders keep
  the old captured scope, and an intervening affected-roadmap mutation makes undo conflict.
- **`move-undo-refuses-a-reorder`** — undo after a sibling reorder, a further reparent or a
  privacy change refused conflict and guessing no position; undo otherwise restoring the exact
  before order and required sets with fresh scope revisions, the old numbers unwritten; a kill
  around acceptance and during clip exposing neither two parents nor none.
- **`axisless-history`** — two ordered rows added by `roadmap row`, one finished, the state
  exported and loaded, the view reopened past the default window: both rows and their evidence
  present, the denominator not reduced by completion; a row retired records a scope movement,
  keeps its node, and the prior view reconstructs at its captured revision.
- **`matrix-retirement`** — `axis --remove` of a first-axis row and then of another axis's
  member: only the selected coordinates retired and recoverable, no task cancelled, an unknown
  member refused, a layout change on a populated roadmap refused `layout populated` with no
  partial write, and a matrix never flattened without explicit selections.
- **`configure-no-effect-and-undo-conflict`** — an equal-value configure, its reply lost, a
  later edit, then the retry: the original receipt returned and the later value kept; undo
  restoring an ordered preimage only while its guards match.
- **`completed-view-mutation`** — metadata, projection and render on a settled roadmap
  reviving nothing; an outstanding member added applying the atomic revival rule so no settled
  container silently holds open required work; counts and indexes checked by the reference
  fold after each step.
- **`render-artifact-is-bounded`** — ordinary replies and a `--chat` artifact interleaved in one
  correlated batch, request ids, byte length and hash verified; a corrupt or oversized artifact
  a bounded refusal and never partial Markdown; `--check` creating no target, no receipt claiming
  a write, no commit and no push.
- **`a-root-id-grants-nothing`** — a stored permitted root with no `--render-root` mapping
  refusing file mode while `--chat` renders; an escaping path, a symlink escape and a target
  identity other than the mapping's refused; the cooperative lock exercised and its
  external-editor limit retained.
- **`priority-orders-only-the-eligible`** — a blocked rank-0 task stays blocked with its reason
  and resolver while a rank-9 ready sibling is first among the eligible; `--order priority`
  under `done` exit 2; capacity loss, approval withdrawal, a dependency change or a hold
  rechecked before ranking and starting or interrupting nothing.
- **`priority-inherits-and-clears`** — a root `:subtree` rank changing ready order with no
  lease, attempt, state, O, C, W, counter, baseline or roadmap moved; a child's `:self`
  overriding it; a clear revealing the parent; settle and reopen keeping the slots; a move
  re-reading inheritance with no cloned event.
- **`rank-2-precedes-10`** — ranks compared as integers, and equal and default rows ordered by
  id across a restart, a handoff, a cursor continuation and skewed clocks; a first unseen filter
  `O(k log k)`, later pages from the pinned order, a subtree invalidation touching no unrelated
  scope and no C.
- **`priority-undo-is-history-not-value`** — a same-value set and a clear of an absent slot each
  the no-effect receipt; set 2, set 9, set 2, then undo of the first refused although the value
  matches.
- **`priority-grants-nothing`** — with priority set on every node, `who` unchanged, no lease
  written, no worker selected, no approval bypassed.
- **`state-export-describes-exactly-r`** — capture R while R+1 is accepted and the bytes
  describe R; an exact-snapshot export with B equal to R and an absent end, and one with B below
  R over a multi-record prefix across a rotation; an absent end below R, a missing or swapped
  record, a wrong end hash or revision and a cut inside an envelope each refused, and a present
  later tail never replayed.
- **`state-export-refuses-a-gap`** — a missing mandatory member, a changed digest, a dangling
  internal reference, a path escape, a symlink, an output overrun and a corrupt S-expression
  each refused with no valid load; a historical export whose resolver observations are gone
  refused with a named proof gap and never given current ones; `--closed-history range` over
  `[from, to)` declaring its omissions while keeping closure, `all` reaching C past the resident
  window and a fresh load reproducing its proof.
- **`state-export-is-one-long-operation`** — an export blocked on archive I/O acknowledging its
  operation at once, status, cancel and an unrelated mutation responsive under it, and `wait`
  returning the same operation and captured revision after publication or refusal; an export
  inside an atomic batch refused by entry id.
- **`state-export-pin-survives-clip`** — capture R, a clip and a retention pass at R+1 during
  the copy, then exactly R completed or a recovery gap named; no pinned member reclaimed, no
  current bytes substituted.
- **`state-export-disconnect-and-cancel`** — a lost client, a restart and a cancellation around
  the no-replace publication keeping one operation and one output identity, no duplicate
  directory, no claim to reverse a published one; an existing destination refused; a staged
  manifest before the commit not published.
- **`state-load-is-isolated`** — an instrumented export and load with no ownership change, no
  dispatch, no replay, no merge, no resolver run, no network and no repository write, only the
  declared exclusive paths written; the loaded snapshot answering `query --snapshot` and refused
  as a `--session` by every mutation, replay, clip and handoff verb; a re-export of it comparing
  equal in every field, id, Unicode, empty and absent value, role, CONFIG, ACTIVE, O, C, roadmap
  and accounting record.
- **`fenced-export-can-finish`** — in a fenced session an export started, its status and
  terminal line read by its id, and separately an unfinished one cancelled with publication
  reconciled; an unknown id, a non-export id, `operation list` and every canonical write refused
  `fenced`; no second owner and no mutation authority created.

- **`goal-crosses-harness`** — G a `:doing` leaf at revision r; harness A, as coordinator C,
  `goal set --goal G --expect r`, `GOAL OK … rev=r+1`;
  writes a `(:coordinator "C")` note (*Delegation*) with a `:deny` on a made-up model for
  `:coding`; `goal update --stop --reason` on G. Harness B, another build, `goal show --as C`
  against the live session and against the clipped snapshot: both print `goal=G`, a `rev=` at or
  after every write of A's, `stop=requested` — not `cancelled`: no evidence of a stopped worker
  has been written — and the same note id and constraint row A wrote, byte for byte; B's `goal
  update --progress` on G is refused `stop requested`, nothing written. A writes `event --kind
  cancel --evidence <pointer>` on G; B's next `show` prints `stop=cancelled`. B copied no
  conversation.
- **`goal-stale-update-refuses`** — A and B both `show` at revision r. A writes `update
  --progress` with the evidence triple on G, a `:doing` leaf, r+1; B's `update --progress --expect r` is refused `GOAL FAIL …
  expect=r current=r+1: stale`, the snapshot is unchanged, and B's next `show` prints A's
  evidence row. A then `update --stop --reason --expect r+1`, r+2; B's `update --progress
  --expect r+1` is refused `stale` and B's next `show` prints `stop=requested`; B's `update
  --progress --expect r+2` is refused `stop requested`, and `stop=requested` stands until a
  `:cancel` with evidence or `state --to doing --reason` by id. A `goal set` to a node on the
  closed branch is refused `disposition=done` whatever `--expect` says, and the node stays
  closed.
- **`goal-stop-is-a-request-not-evidence`** — G `:doing` at r; `goal update --stop --reason
  --expect r` prints `GOAL OK … change=stop kind=transition rev=r+1`, and the event is a
  `:transition :to :cancel-requested` carrying `:reason` and no `:evidence`; `check` has no
  finding; `state --to cancel-requested --reason` on a sibling writes an event of the same kind
  and fields. `state --to doing --reason` by id is admitted (the withdrawal) and `show` prints
  `stop=none`; stopped again, then `event --kind cancel --evidence <pointer>`, and `show` prints
  `stop=cancelled`, terminal, and `goal set --goal G` is then refused `disposition=cancelled`. `goal update
  --stop` on a `:review` node is refused `no edge`, as `state --to cancel-requested` is there,
  and on a `:done` node the same: the edges are the table's and no other.
- **`goal-update-writes-only-existing-kinds`** — every `goal update` form written, then the
  journal read: each event is a `:transition` or an `:evidence` with exactly the field list of
  its kind, on the goal node of the scope and no other node; `--progress <text>` alone on a
  `:todo` node writes `:to :doing` with the text as `:reason` (the same event `state --to doing
  --reason` writes) and on a `:doing` node is refused `no edge`, nothing written; `--progress`
  with the evidence triple on a `:doing` node writes the `:evidence` event `evidence` would;
  `goal set` and `goal set --clear` each write one `:goal` event with `:node (:absent)`; `accept
  --add` on the node moves its scope revision and `goal update` never does; a retried `set` with
  the same `--request` id and payload returns the same event id once.
- **`goal-expect-is-required`** — `goal set` and `goal update` without `--expect` exit 2 naming
  the flag, nothing written; with `--expect` at the current local revision, admitted; with
  `--expect` one behind, refused `stale` at exit 1 with the current value printed; `--dry-run`
  with the stale expectation prints the same refusal and writes no event, no journal revision
  and no dedup entry; a `show` never takes `--expect` and answers at the revision it prints.
- **`applicable-cap-never-hides-a-deny`** — N active notes, N > `--max`, the only `:deny` in
  the note that sorts last (*Delegation*'s `applicable`); `applicable --max 1 --candidate
  <role>/<model>` prints `excluded <that id>` and `NOTES MORE`; `goal show --max 1` prints the
  same constraint row before any cut row, then `GOAL MORE`; with the notes index unloadable,
  both print `FAIL` and neither prints `eligible` nor any row.

**The replays of #295 at `c2be4d6b`, for the journal's identity, the savepoint's cut and the
index's admission** (where #333 at `79277f05` already has a built witness, it is named):

- **`savepoint-cut-never-splits-an-envelope`** — a two-event request accepted and a savepoint
  after it: both events represented once in the image and a retry answered the byte-identical
  original lines; a savepoint whose cut would fall between the two events refused `cut inside an
  envelope` and no image published.
- **`crash-after-append-recovers-the-reply-once`** — a crash after the durable append and before
  the reply: the envelope replayed once from the record, the same request and payload answered
  with the original `OK`, the retry mutating nothing; the same id with a changed payload refused
  `reused with a different payload` after the restore as before it (#333
  `durable-journal-append-plus-lost-reply-recovers-once`, `durable-journal-changed-payload-refuses`).
- **`rotation-keeps-one-journal`** — a clip rotating the journal after a savepoint: the new
  segment's header naming the same journal id and the copied boundary record with its sequence and
  hashes, the chain continuing, the savepoint's cut and every disposition it needs reachable
  without a scan of old segments, and `session export --journal` writing the same bundle from
  either file.
- **`torn-tail-is-diagnosed-not-truncated`** — a partial frame at the end of the journal, a
  flipped bit inside a frame and a wrong header each refused with the file bit for bit as it was,
  the first reported `recovery-gap kind=torn-tail`, the second `kind=corrupt-record` and the third
  `journal mismatch`, none rounded to another, and every mutation refused `journal uncertain` after a failed append until
  the tail is read (#333 `durable-journal-corrupt-data-refuses-without-truncation`,
  `durable-journal-partial-write-refuses-without-truncation`,
  `durable-journal-uncertain-write-refuses-until-recovery`).
- **`replay-mints-nothing`** — a replay of five envelopes reconstructing the exact root digest, the
  closed rows and the next revision with no fresh event id, no clock read and no verb run; a replay
  failing midway leaving its target untouched (#333 `durable-journal-replay-generates-no-fresh-ids`,
  `durable-journal-replay-failure-isolates-target-kernel`).
- **`savepoint-write-failure-keeps-the-previous`** — the image write, the manifest publication and
  the sync each failed in turn: the previous verified savepoint restores, and `savepoint list`
  prints the attempt `verdict=failed`.
- **`copied-journal-grants-nothing`** — a journal and its savepoint copied to another bench:
  `savepoint restore` there inspects in isolation, takes no ownership and dispatches nothing, and a
  `session start` over the copy is refused by the fencing rules, the journal id notwithstanding.
- **`reply-retired-only-under-verified-coverage`** — a request whose events lie before the clip
  boundary: its retry answered `already applied` only once the committed snapshot, its retained
  events and the dedup root the boundary record names are verified reachable; with the push failed
  or the root unreadable, the original `OK` still answered from the retained disposition or
  `recovery-gap kind=coverage-unverified` printed, and never a reply invented or deleted.
- **`indivisible-record-refused-before-ack`** — one key with its locator that no page under
  `--page-bytes` could hold refused `indivisible` at exit 2 with nothing journaled, while a request
  whose repeatable evidence would pass the bound is admitted and chunked behind a detail root; and
  `session start --page-records 2` refused at exit 2 naming the flag.
- **`days-merge-by-revision-never-concatenate`** — a closure backdated by `--now` into an earlier
  day: its row in that day, and a `--from`/`--to` over both days printing rows in revision order
  across the day boundary; the same day clipped in one batch and in ten yielding identical leaves.
- **`page-budget-is-not-max`** — a filtered historical ask whose filter rejects every row read:
  `QUERY MORE … shown=0 pages=<n>` at the budget, the whole history never scanned, `--max`
  untouched by it, and the continuation answering from the same captured root.
- **`overlay-is-bounded-and-rebuilt`** — a recovery replaying a thousand settles into overlay pages
  under `--index-cache`, an eviction under load losing no row, the next clip writing them into the
  pages, and no query replaying the journal.

The stall replays of 5649089106
belong to stall detection, deferred below, and are listed there so they are not lost.

**The replays of the duty-tier amendment (#500), for the resident session's authority, escalation,
the wait table, quiet time, the coordination measure and the single-writer kernel:**

- **`duty-tier-executes-the-policy`** — a resident session (E02) given an approved finite policy
  record from C, `:by` on every rule, executes it with the cheapest qualified model or none, authors
  no policy of its own, and escalates every judgment it cannot make; a policy rule with no `:by` is
  refused at load.
- **`escalation-carries-rule-default-age`** — an escalation row carries the policy rule that could
  not decide it, the default that fires on silence, and its age; `stale` reads the three and
  reassigns nothing.
- **`wait-table-four-presence-columns`** — the per-harness wait table carries the four presence
  facts — process alive, beat written, delivery handled, parent woke — and a harness with the fourth
  unproven holds no resident session, only a duty session driven by notes.
- **`quiet-time-calls-nothing`** — a resident or duty session with nothing changed makes no model
  call and sends no note; state is published mechanically; the cost of one event is the measured
  spend of the one call that event caused.
- **`cost-per-accepted-decision`** — the coordination measure is cost per accepted decision across
  tiers, with wrong or missed decisions and recovery latency as gates; the decisions-per-token
  sentence of *Decision packets* is replaced.
- **`single-writer-kernel-total-order`** — two concurrent clients' commands to the one kernel land
  in one total order; the journal shows the sequence numbers in that order; a mutation outside the
  command loop is a defect (the validator rule).

**The replays of the fleet allocation rules (#500), for the machine as CONFIG, the ACTIVE
allocation, the atomic take, the suspect expiry, the dated probe and the one allocator per machine.
Each replay is a command line and the exact printed line, the line shapes of *Fleet allocation*
being the amendment's additions to *Output grammar*:**

- **`machine-is-config-and-never-a-work-tree-node`** — `nova-work machine --session <path>
  --register m-a1 --name studio --owner glenn --connect profile:studio --role build --role test
  --permit go-test --limit cores=16 --fact os=macos --declared-by glenn --reason "studio online"`
  prints `MACHINE OK id=<event-id> request=<id> machine=m-a1 rev=<n> pushed=<rev|-> changed=<n>
  emitted=<bytes>`; `|O|`, `rows=`, every roadmap and every count is unchanged, the event's `:node`
  is `(:absent)`, the record is a `:kind :machine` member of the `fleet` section of CONFIG, and no
  verb can settle it, `:to :done` it or make it completion evidence.
- **`allocation-binds-machine-slot-generation`** — `nova-work take --session <path> --machine m-a1
  --node schema/cpp/refuse-newer --slots 1 --offer <offer-id> --attempt <attempt-id> --generation
  <n> --request-ref <opaque-id> --batch <request-id>` prints `ALLOC OK id=<event-id> request=<id>
  machine=m-a1 allocation=<allocation-id> slot=1 node=schema/cpp/refuse-newer batch=<request-id>
  offer=<offer-id> attempt=<attempt-id> machine-generation=<n> allocation-generation=<g> rev=<n>
  pushed=<rev|-> changed=<n> emitted=<bytes>`; the allocation is ACTIVE data referencing the
  machine's CONFIG identity and revision, the (offer, attempt) is the offer section's reservation
  key, admission happens before preparation and both capacity constraints validate atomically in
  one binding, and the core-pin rule stays a declared `:limits` constraint and is not in the
  allocation.
- **`allocation-take-is-atomic-and-idempotent`** — `nova-work take --session <path> --machine m-a1
  --node schema/cpp/refuse-newer --slots 2 --offer <offer-id> --attempt <attempt-id> --generation
  <n> --request-ref <opaque-id> --batch <request-id>` prints `ALLOC OK … machine=m-a1 allocation=<id>
  slots=2 …`; the retry under the same request id prints the original `ALLOC OK` line and applies
  nothing, a changed payload under the id is refused `reused with a different payload`, a take for
  more than the declared `:concurrent` admits is refused whole `ALLOC FAIL machine=m-a1 slots=2
  holder=<name>: capacity` at exit 1 with **no partial grant**, `nova-work heartbeat --session <path>
  --allocation <allocation-id> --generation <n>` prints `ALLOC HEARTBEAT OK allocation=<id>
  machine=m-a1 machine-generation=<n> allocation-generation=<g> …`, a heartbeat with a stale
  allocation generation or stale machine generation prints `ALLOC FAIL machine=m-a1
  allocation=<allocation-id>: stale token`, `nova-work release --session <path> --allocation
  <allocation-id> --generation <n>` prints `ALLOC RELEASE OK allocation=<id> machine=m-a1 slot=<n>
  freed=true …` and frees exactly that allocation's slot, and `list --machine m-a1` prints one
  `ALLOC ROW allocation=<allocation-id> machine=m-a1 slot=<n> holder=<name> node=<id> batch=<id>
  offer=<offer-id> attempt=<attempt-id> age=<duration>` per live allocation.
- **`expiry-marks-suspect-reuse-needs-fencing`** — an allocation past its deadline reads suspect: a
  renewal and a `take` with the stale token are refused `ALLOC FAIL machine=m-a1: suspect
  since=<stamp>` at exit 1, the uncertain ACTIVE capacity retained and never cleared by the expiry;
  only a verified stop observation, a bound `not-started` or machine-side fencing releases the slot,
  and until one of those, the refusal is `ALLOC FAIL machine=m-a1: not fenced` at exit 1.
- **`probe-records-observed-active-and-touches-no-config`** — `nova-work probe --session <path>
  --machine m-a1 --slot 1 --source <pointer>` prints `PROBE OK machine=m-a1 slot=1
  fact=observed at=<stamp> source=<pointer>`, and an unestablished fact prints `PROBE OK
  machine=m-a1 slot=1 fact=absent at=<stamp> source=<pointer>`; `query --ask fleet` prints the same
  declared `:facts` with their `declared-by` and `declared-at` before and after, no `:limits`, no
  `:facts`, no `:connect` and no `:roles` change, the profile is never resolved, and no credential
  is in any record or clip.
- **`one-allocator-per-machine-aliases-share-nested-conserve`** — a second allocator for one
  machine is refused `ALLOC FAIL machine=m-a1: allocator held` at exit 1; an allocation taken
  through one alias of a host reads as the same allocation under the other alias and counts once;
  and an allocation nested under another allocation's scope draws from the same declared capacity,
  never an additional slot, `ALLOC ROW` listing each live allocation once.
- **`release-one-allocation-spares-the-other`** — machine m-a1 with `:concurrent 2` holds two
  allocations `alloc-a` (slot 1, node N1) and `alloc-b` (slot 2, node N2); `nova-work release
  --session <path> --allocation alloc-a --generation <n>` prints `ALLOC RELEASE OK allocation=alloc-a
  machine=m-a1 slot=1 freed=true …` and `list --machine m-a1` then shows exactly one `ALLOC ROW`
  for `alloc-b` with `slot=2`, proving `alloc-b` is untouched; the released slot 1 is free for a
  new `take --slots 1` while `alloc-b` continues.
- **`stale-allocation-id-refused-by-name`** — after `alloc-a` is released, a heartbeat or release
  using `--allocation alloc-a --generation <n>` is refused `ALLOC FAIL machine=m-a1
  allocation=alloc-a: stale token` at exit 1, naming the allocation id that no longer matches any
  live allocation; a take using the released allocation's generation against a changed machine
  configuration is refused `ALLOC FAIL machine=m-a1 slots=1 holder=<n>: capacity` at exit 1 by the
  stale machine generation check.
- **`preparation-interrupted-before-launch`** — `take` allocates slot 1 on m-a1 to node N1,
  allocation `alloc-c`; preparation (clone/scp) is interrupted before the model launches; a second
  session's `take --machine m-a1 --slots 1` for node N2 is refused `ALLOC FAIL machine=m-a1
  slots=1 holder=<N1>: capacity` because `alloc-c` still holds the slot; only after reconciliation
  (a verified stop observation, a `not-started` rejection, or machine-side fencing) frees
  `alloc-c` does the slot become available, and no second session reuses the capacity until that
  reconciliation happens.
- **`concurrent-slots-refuse-third-job`** — machine m-a1 with `:cores 16` and `:concurrent 2`
  holds two allocations each consuming one slot; a third `take --machine m-a1 --slots 1 --node N3
  --generation <n>` is refused `ALLOC FAIL machine=m-a1 slots=1 holder=<name>: capacity` at exit 1
  even though fourteen cores sit idle, because slots are bounded by declared concurrency and cores
  are a separate constraint validated independently.

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
| `format-determinism` | one state and schema produce identical canonical bytes; null, absent and empty stay distinct, in the payload digest as on the wire — `(:absent)`, `()` and an empty string are three serializations and no two of them digest alike; large integers, timestamps, escaping, multiline text and Unicode normalisation differences survive; an arbitrary provider's JSON key order is **not** required to be meaningful |
| `old-history` | an export includes the whole explicitly selected archive, records outside the resident 24-hour window included, with its scope and omissions declared; an old completed roadmap restores and yields its exact proof **without loading all of C**; **a recent-only export is never labelled a full backup** |
| `referential-integrity` | duplicate ids, dangling references, cycles, conflicting parents, invalid cells, duplicate ownership and mismatched manifests all fail **before** publication; a scoped export carries its dependency closure or names its unresolved external references, never a falsely complete backup |
| `atomic-mutation` | failure injected before, during and after the journal append, the durable sync, the apply, the savepoint write, the rename and the reply; **every acknowledged mutation survives a process restart** under the declared storage assumptions; a torn unaccepted tail is diagnosed; no partial envelope and no count-versus-evidence split is admitted |
| `retry-protocol` | a lost reply, fragmented frames, a disconnect, a repeated id with an identical body, the same id with a different body, invalid UTF-8, types and versions, oversized frames and deadlines; **no duplicate accepted mutation and no executable payload**, and the outcome retrievable after the uncertainty |
| `async-operations` | status, wait and cancel under a busy import, export and clip; bounded queues and output; a restart with operations pending; a stale staged result and an uncertain external effect; **no double launch, no false cancellation success and no control plane stalled behind network I/O** |
| `batches-and-pipelines` | the same workload run sequentially and in each batch mode under its declared revision semantics; a read bundle held on one snapshot across its pages; a bad middle entry, a moved revision, fragmented frames, a lost reply, a disconnect and a crash injected at every acceptance boundary — **an atomic batch publishes all or none, an independent batch preserves its exact accepted prefix and marks the remainder not attempted**; a same-id retry duplicating no mutation and a changed payload refused; per-entry errors, output limits, an oversized atomic batch refused whole, backpressure, and status, cancel and lease renewal answered within bound under bulk load; O, W, the counters and the indexes consistent after each mode, and **no external effect inside an atomic batch**; the operational token and turn totals measured after adoption, corrections included. `expected=atomic=all-or-none,prefix=exact,unattempted=marked,duplicates=0,oversize=refused,external-in-atomic=0,control-plane=responsive` (**B1**–**B5** above; **T2**) |
| `single-writer` | two local processes, alias paths, a stale socket, partitioned benches, lease expiry, a delayed old owner and a handoff crash; the fencing rules prevent **stale mutation authority** and not only a stale Git push; exported unshared journal work is preserved |
| `indexes-and-counters` | random legal verb sequences compared after each step against an independent full reconstruction; the open-item counters and the friend indexes agree; closure, reopen, reparent and shared references never double-count; the required constant-time queries and the bounded historical paging are instrumented |
| `materialized-working-set` | **hold W fixed while O and C grow**: repeated membership and `|W|` asks, and the first ask after each mutation, visit **zero unrelated nodes**, scan neither O nor C, and a listing visits only the page it returns; W, `|W|` and the per-friend indexes compared against an independent reconstruction after `take`, renew, `release` and expiry, a settle, a `:cancel`, a reassignment, duplicate attempts on one id, an undo and a crash replay; **a fake clock expiring leases through the deadline index**, with a delayed watermark printed rather than freshness claimed; an expiry retaining its uncertain remote execution and capacity records; and a recovery that has not reconciled its leases advertising none of them as live. `expected=unrelated-visits=0,scans=0,reconstruction=equal,watermark=printed,uncertain-retained=yes` (**W1**–**W5** above; **T1**) |
| `roadmap-proof` | full fixed-table prototype parity, optional axes, partial and stale evidence, shared prerequisites, newly discovered scope and closed members; chat and file renders identical; a marker edit preserving every unrelated byte and refusing ambiguity |
| `undo-redo` | reversible edits reversed, history preserved, redo only against valid preconditions; dependent later edits, changed criteria, close and reopen, decomposition, accounting receipts and uncertain external actions exercised; **a conflict is explicit and mutates nothing** |
| `recovery` | restore the newest valid savepoint plus journal; reject a corrupt savepoint; recover from a prior savepoint **without silent loss**; compare an isolated old restore against current state; a missing tail or an unavailable remote backup **reported as a recovery gap** |
| `schema-evolution` | supported old schemas migrate losslessly against golden fixtures and semantic comparison; an unsupported version refuses while preserving the originals; **a migration never rewrites the only source copy** |
| `hostile-data` | reader evaluation disabled; pre-parse depth, byte and node limits enforced; path traversal and escaping archive paths rejected; imported prose cannot execute a command or alter authority; deep and high-fan-out inputs handled without quadratic copying |

**Each suite's lane is named here, because *the lanes are named* below is worth nothing if the
table is silent** *(Rowan's decision, for review)*. **Per change**, inside the one-minute target
and the two-minute bound, each over a bounded fixture subset: `format-determinism`,
`referential-integrity`, `retry-protocol`, `read-only-intake`, `undo-redo` and `roadmap-proof`.
**Nightly or pre-release**, whole matrices, because none of them fits two minutes and a gate
nobody runs is worse than an honest slow one: `source-inventory`, `import-replay`,
`moving-source`, `archive-completeness`, `full-round-trip`, `old-history`, `atomic-mutation`,
`async-operations`, `single-writer`, `indexes-and-counters`, `materialized-working-set`,
`batches-and-pipelines`, `recovery`, `schema-evolution` and
`hostile-data`. Every suite's whole matrix runs at the release revision whatever its lane, and
each row still needs its fixture, command and observable before the lock gate, which the
paragraph after the seven obligations below says and this list does not replace.

**Six of these rows gate the intake adapter, which *What this draft does not do* calls its own
spec**: `source-inventory`, `read-only-intake`, `import-replay`, `moving-source`,
`archive-completeness` and `schema-evolution`. They are here rather than there because the
preservation promise is this document's, and they are named as the adapter's gate so that no
release of nova-work is read as a release of the adapter, and no adapter is read as gated by a
suite nobody assigned to it.

**The savepoint is local, the checkpoint is shared, and the two are never reported as one** — and
they are two words because they are two things, by the retention paragraph above. Every accepted mutation
is durably journaled before its success acknowledgement. **Validated atomic local savepoints** are
created periodically, by a configured elapsed time **and** a configured accepted-event count —
`--savepoint-every <duration>` and `--savepoint-after <n>` on `session start`, both in the grammar
above and neither a number this tool believes in — each
naming its schema, revision, journal boundary and content manifest; the **periodic clip supplies
the separately observable shared checkpoint**, and **a local success is never reported as a shared
backup**. `savepoint list`, `create` and `verify` expose savepoint age, the local and the shared
revisions side by side, the unshared work and the failed backup attempts, and **the last known-good savepoint
is kept while its replacement is written**. **Retention and journal compaction may never remove the
only recoverable copy** of accepted work or historical evidence; **pruning savepoints is a
different thing from retaining C**, and both are different from the clip's retention archive,
which is provenance and is never pruned at all; a remote outage that leaves new work only on this bench is
**stated as that exposure**; and **a local journal alone does not protect against losing the
machine** — an independent verified copy does. **A restore opens a read-only, isolated,
non-dispatching recovery session**: it inherits no coordinator ownership, reanimates no
assignment, replays no bus message and duplicates no external side effect, and a selected repair is
promoted only through a fenced validated reconciliation with the current state (replays
`restore-is-isolated-and-dispatches-nothing`, `a-savepoint-is-not-a-shared-backup`,
`compaction-keeps-the-last-copy`).

**A savepoint names one complete journal record, and its manifest says which** (#295 at
`c2be4d6b`, Stella's draft, folded on Rowan's read there). Three boundaries stay three: the
**retention boundary** is the clip snapshot's and the archive's; a **clip boundary record** in the
journal names what was clipped and is the offline bundle's base and the dedup index's edge; a
**savepoint's replay cut** names the last complete journal record its image reflects — and it moves
neither of the other two. The manifest is one restricted S-expression whose fields, in order, name
the schema, the journal id, the image's local revision, the replay cut as a sequence and record
hash, the newest boundary record the image reflects the same way, and two hash-checked content
references inside the savepoint's own root — the image and the retained local replies — with
`(:absent)` for the cut or boundary an initial image has none of; the manifest holds no digest of
itself, and `manifest=<sha>` is its complete canonical bytes hashed outside it:

```lisp
;; EXAMPLE DATA, NOT A LOCKED CODEC: the content references' path, hash and size form is open below.
(:schema "work-savepoint-v1" :journal "<64-hex>" :local-revision 812
 :replay-cut (:sequence 420 :sha256 "<record-hash>")
 :boundary (:sequence 390 :sha256 "<record-hash>")
 :state <content-reference> :local-replies <content-reference>)
```

**The cut can never fall between an envelope's events**: it resolves to one complete verified
record whose resulting revision is the image's, or the manifest is refused, `SAVEPOINT FAIL id=<id>
rev=<n>: cut inside an envelope`, and no image is published; the image is the resident state whole
— configuration, observations and index overlay included — and is neither the clip snapshot nor the
retention archive. **The local replies are retained until their coverage is verified, and a marker
alone proves neither coverage nor publication** — the boundary record is the `:boundary` above and
never a filename or a wall-clock guess: each retained disposition is a request id, its
payload digest, its accepted record's sequence and hash and the original reply, kept in the image
even when its events lie before the cut; a disposition whose events lie before the clip boundary is
retired to the dedup index's `already applied` only once the committed snapshot, its retained
events and the dedup root that boundary record names are verified reachable, because a commit that
exists locally is not a push that succeeded; until then the original `OK` still answers or
`SAVEPOINT NOTE recovery-gap kind=coverage-unverified` prints, and a reply is never deleted, never
invented and never reconstructed from current state — a restore missing one refuses. **Writing one
is four steps and restoring one is five**: one immutable image and one record cut captured at the
same revision under the single writer; the image and reply objects written and synced; the
candidate manifest validated against their exact identities, then published atomically and synced
under the declared filesystem contract, the prior verified savepoint kept through it; and on
restore the manifest, the journal id and the exact cut verified, the image and its replies loaded,
only the complete records strictly after the cut replayed once in sequence, boundary records
processed, then the whole validation before anything is exposed — read-only, isolated,
non-dispatching, promoted only through the fenced reconciliation above. A missing or mismatched
cut, a missing required tail, a broken hash or sequence or an incomplete envelope is a recovery gap
by its kind and never a truncation or a rollback of acknowledged work; **a torn tail is evidence of
an interrupted append, told apart from a corrupt record, and both are kept**. A small cut licenses
no history-wide pass: a new savepoint verifies what it newly wrote, a retained immutable object
keeps its prior verified identity, and a later read checks the bytes it uses. **Rotating or pruning
a journal keeps a reachable verified cut and every tail record and disposition each retained
savepoint needs, or publishes a validated replacement first** — the newest filename and the largest
revision are never that proof, and a rotation may hand a bounded locator to the same retained
record rather than a scan. **The image is one immutable revision, and how it is captured beside a
running writer is open**: whether by pinning a generation, by copy on write or by a serialization
held for the capture is specified and tested before concurrent mutation is allowed, and the C, O
and W roots above imply no copy-on-write mechanism by their names (replays
`savepoint-cut-never-splits-an-envelope`, `savepoint-write-failure-keeps-the-previous`,
`copied-journal-grants-nothing`, `reply-retired-only-under-verified-coverage`,
`rotation-keeps-one-journal`, `torn-tail-is-diagnosed-not-truncated`). **#333 at `79277f05` has no
savepoint and no boundary record**: its `replay-journal` with a stop sequence is this paragraph's
cut once it starts at the cut rather than the header, which the journal paragraph above already
names as its slice to change; nothing here contradicts what it built.

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
  reconciled execution handles — reached by `execution pause`, `stop` and `correct` of
  *Assignment and execution control*; blocked questions and bounded fallback plans persisted **so a
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

## What the bug amendment adds *(Rowan, 2026-09-15, nova-tools#463)*

One section, *Bugs found while working*, above *Counting*: the `:bug` kind at any level, its
`:found-during`, `:evidence` and `:test` fields, rule 19, the `bugs=<open>/<fixed>` count, the
`:bugs` roadmap form and seven replays. Nothing older is changed; a bug is outside every
denominator and inside every parent's done gate.

## What this draft does not do

Stall detection and bounded recovery with its six replays (5649089106), `plan`/`apply`/
`reconcile` (5653982211), the cross-bench ownership backend and fencing generations (the rule is Glenn's; the git-lease backend above is this draft's proposal for the pilot and
Stella's section keeps the general requirement), the `link`/`absorb` intake modes and the
staged migration as code (their contracts are Stella's sections), the GitHub issue intake and correspondence adapter as code (its contract is Stella's
section below; the adapter is its own spec), token and cost joins beyond the attempt's
`:usage` pointer (#175, #181), and the categories taxonomy (5654164074) are later revisions,
each with its issue. Nothing here deletes, migrates or publishes an issue. Known work is not
authorized, working, scheduled or public by being in O.

**The participation question is closed**: swarms are model-only and a friend is never a pool
worker (decided by Glenn, 2026-09-15; *Friends, CONFIG and ACTIVE* above holds the rule), and no
historical actor is renamed (Stella, `docs/SPEC-WORK-PILOT.md` at `81c2885`).

**The #293 fold leaves these open, each with its owner, and closes none of them by folding**:
the link-text grammar and the JSON spelling of the tagged patches; the same-repository
boundary of `node move` and the cross-repository transfer, the inherited-context guard's exact
predicates, the parent-local transfer accounting and the undo preimage codec (Rowan and Emma,
whom the draft names); adding a dimension to a populated roadmap, the one-axis row-only
reading, the `--render-root` mapping's ownership, the permitted-root mutation policy, the
historical view-pointer index, projection receipt retention and the cooperative renderer-lock
boundary; the rank range and policy, the default rows' position and priority on other
projections (each of these the friend review the draft asks for); the export's member-path
taxonomy and record codecs, the journal-reference identity scheme its `:replay-cut` rests on,
no-replace platform support and recovery, and signing and audience policy (the draft's author,
before its codec lock). Until each is closed the sentence it would settle is a proposal.

**Nothing here is implemented, and the lock gate says what would have to be true before it is.**
This document is integrated with both of Stella's companions into one revision, which is her first
condition. The rest of her gate stands unmet and is named so it cannot be skipped: **each requested
friend's explicit disposition at this exact revision**, with unresolved, unavailable and reserved
reviewers recorded separately and **no reply never counted as approval**; the complete verb and
protocol schemas as the generated schema file and its coverage test of *The engine and its client*
above, with every remaining **(Rowan's decision, for review)** read; the migration and round-trip acceptance coverage of *Preservation and recovery acceptance*
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

**What #295 at `c2be4d6b` leaves open is named here with an owner, so a fold is not read as a
lock.** The **physical codecs** — the byte forms of the prefix-free key encodings, the exact record
field order, empty roots, path derivation, the minimum wrapper sizes, the detail and segment
reference forms, the savepoint's content-reference form, and the journal segment header and locator
forms — are Stella's, as the draft's author, with Emma's #333 the slice that widens the built frame
to the journal contract above. The **merge fan-in, the temporary runs' bounds and lifetime, and the
work-continuation's expiry** behind `--page-budget` are Rowan's. The **snapshot-isolation
mechanism** for capturing one immutable image beside the single writer is Emma's, specified and
tested before concurrent mutation is allowed. The **bench, lock identity and generation fields of
the journal header** wait on the cross-bench backend decision above and are Stella's with it. No
bounded historical-range latency is claimed until the fan-in and the continuation are pinned.

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
`savepoint list|create|verify|restore|compare` spelling and the word *savepoint* for the local snapshot against *checkpoint* for the clip commit; the `friend`, `config`, `model` and
`observe` verb spellings and their flags; the `roadmap`, `friends`, `models` and `ready` asks; the
`OPERATION`, `SAVEPOINT`, `UNDO`, `REDO`, `CONFIG` line shapes; `--silence-ping` as a configured
duration rather than a number this tool believes in; `:row-kind` and `:aggregation` as the
declared spelling of Stella's row-kind amendment; and **the retention of a roadmap's view record
in the live snapshot** rather than exempting `:roadmap` from the settle cascade;
`cell --in-scope`; **and draft 27's decisions, each marked *(Rowan's decision, for review)*
where it is made**: the `:undo`, `:redo`, `:friend`, `:model`, `:observe` and `:config` event
kinds with their ordered field lists, their `(:absent)` `:node` and the subject each `OK` line
prints in its place; the compensating events of an undo added to the closed list of
session-written events; `(:absent)` as the digest's spelling of an absent field against `()` for
an empty one; the clip as one long operation with `operation=<id>` on its three lines and
`session stop` waiting by `operation wait`; the operation id journaled before it is printed and
`no such operation` at exit 2; `operation cancel` taking `<write flags>`; `--deadline` as the
CLI's spelling of the wire's `"deadline"`; the reversible-verb table and its refusals; the list
of missing verbs; `--savepoint-every`, `--savepoint-after`, `--max-frame-bytes` and
`--silence-ping` on `session start`; the three `:aggregation` values; `unit=epics` and
`unit=work-sets` beside `unit=features`; `|O|` named as envelope metadata and `query --ask size`
as the line that prints it with its unit and revision; the word *savepoint* and the
`SAVEPOINT` line shapes; and each acceptance suite's named lane; **and draft 28's one
decision, marked *(Rowan's decision, for review)* where it is made**: the atomic mutation
batch refusing a long-operation entry **by its own entry id**, which is where `bc4a4a4`'s
*refuse the verbs marked as carrying an external effect* had to be written once draft 27 had
settled that the external effects are outcomes and not verbs of this grammar; **and the fold of
Stella's #294 (`4fddfcb2`), whose contracts are hers and whose spellings are marked *(Rowan's
decision, for review)* where made**: `--by` and `--default` on `acknowledge` in place of
`--lease-by` and `--lease-default`; the `:offer`, `:acknowledge`, `:decline` and
`:execution-control` kinds with their ordered field lists and subjects; `execution status`; the
`OFFER`, `ACKNOWLEDGE`, `DECLINE` and `EXECUTION` line shapes and `op=execution`; the
cancellation request and `execution stop` kept as two acts that imply nothing of each other;
the six reversible-verb rows over eight verbs;
**and the #293 fold's decisions, each marked *(Rowan's decision, for review)* where it is
made**: the completed `node add` order with no legacy path, `roadmap create` as the one creator,
the retired `render --into --start --end`, the `PRIORITY OK` line, `LOAD` as `state load`'s
token, `change=`/`changed=` unifying the drafts' two receipt spellings on `NODE OK`, `roadmap
row` and `roadmap projection` taking `--add|--remove` flags where the drafts spelled
subcommands, one `axis` wire op for both forms, `NODE FAIL node=<id>: <reason>` with spaced
reason words where the draft printed `code=<hyphenated>`, and `EXPORT OK state=true` carrying
`session=`, `into=`, `pushed=` and `emitted=` like every other scope line;
the exact list of refused reader syntax beyond `#.` (every dispatch macro,
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
recursive hierarchy with optional and repeated grouping layers, epics and sub-features,
the declared row kinds and the optional axis layer, roadmaps as durable views that outlive their work, the locked table display and the
one renderer in two modes, the prototype capabilities kept and its two defects named, the seven
operational obligations, the preservation and recovery suites whole, the savepoint, checkpoint,
restore and undo contract, the staged verification and the fast-lane-and-nightly split, and the lock gate; and
in her sections below,
the pilot branch and sha, the prototype facts, the rate schedule and virtual cost, the
`NEXT-TOOLS.md` hand-off, and the fixed-table capability boundary. Each is open to be cut by
the pilot.

**From #295 at `c2be4d6b`, Stella's draft folded on Rowan's read there**: Stella's — the journal
id and the record hash chain, the record's retained original reply, the rotation that keeps one
logical journal, the savepoint manifest and its replay cut, the retained local replies and their
coverage rule, the prefix-free unhashed keys and the one-tree radix layout, the admission refusal
of an indivisible record, the day merge by revision and the page budget's need; Rowan's spellings,
each marked *(Rowan's decision, for review)* where it is made — `--page-budget <n>`, `QUERY MORE`
with `pages=` and `after=`, `indivisible` and `journal uncertain` as refusal reasons, `cut inside
an envelope`, the three added `recovery-gap` kinds, `verdict=failed` on a `SAVEPOINT ROW`, `:boundary` as the
manifest's name for the draft's `:clip-marker`, the `4` below which `--page-records` is refused,
and the choice of the chain over #333's per-record checksum.

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
**For the v1 pilot, `absorb` is disabled and `link` is the default** *(default by Rowan, unobjected
2026-09-15)*: migration is import without delete. Close-and-point — the source issue closed with a
comment naming its node (roadmap E09-F02) — is the v1 shape, to be decided when E09-F02 exists. The read-only real-repository pilot grant remains Glenn's, asked when
E09-F01 can run.

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
above.)* A checkpoint — the clip commit, by the word this
document fixed above, and never the local savepoint — records the parent revision and change.
Network failure leaves a **checkpoint commit made and not yet pushed**, whose sync status is
visible. Reads should remain useful offline; stale or
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
deferred to its issue (5654164074); until then `:category` is an opaque keyword the validator
accepts (default by Rowan, unobjected 2026-09-15). The query machinery filters; the reader should not have to scan S manually.

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
