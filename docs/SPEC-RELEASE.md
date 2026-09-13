# nova-release — specification (draft 6, 2026-09-13)

`nova-release` is one binary at the **release layer**. It takes a repository
whose default branch is landed by a `nova-merge` lane and turns **one frozen
commit** of that branch into a tag and a GitHub release with notes, in one act,
and it refuses to do so until it can name the evidence for every part: the
candidate, every landing in the delta and who read it, the certification run at
exactly that commit, every version site, the notes and who read them. Then it
reads the release back from the wire and says whether what shipped is what was
cut.

This spec is normative. If the code and this document disagree, one of them has
a bug, and the tests decide which. It stands beside [SPEC.md](SPEC.md), whose
**Conventions** section — exit codes, no guessed paths, the one-line output
grammar, the cap-and-count rule, `internal/oneline` and `internal/bounded` —
applies here unchanged and is not restated, and beside
[SPEC-MERGE.md](SPEC-MERGE.md), whose lane, read records and record-file
mechanics (rules 19 and 22 there) this tool **reads and extends but does not
change**. Where this tool needs something neither covers, it is below and it
says so.

Draft 2 folded three cold reads of draft 1 at `7abc968e` — Fable's, Opus's and
Stella's. Draft 3 folded the two cold reads of draft 2 at `1af78e31`, Opus's and
Fable's, both HOLD. Draft 4 folded the three reads of draft 3 at `d224cafd`:
Stella's scoped HOLD, Emma's independent first-eye review (APPROVE) and the ten
polish lines of Opus's APPROVE. **Draft 5 folds the two reads of draft 4 at
`8ee0d38c`**, Fable's and Opus's, both HOLD, whose HIGH findings are one finding
read from two sides: draft 4's account of the release workflow's certification
gate was not the file. The file is nova-tools #252 at `59b85fd6`, its whole ask
is `.github/scripts/release-certified.sh`, and rule 4, work list item 11 and
every sentence here about that gate now say what the script does — one unfiltered
snapshot, a listing that must be whole, any unfinished run a refusal, and the
group of runs sharing the maximal `updated_at` uniformly green — with the script
as the oracle for the tool's own selection.

**Draft 6 folds the two reads of draft 5 at `1a5671fa`**, Fable's and Opus's,
both HOLD, whose HIGH findings are again one finding read from two sides and one
each. Draft 5's repair of `verify` — keyed to the cut record — collided with the
one line that names `verify` as a remedy: the exit-2 `CUT FAIL` after a 502,
which is exactly the case in which no cut record was ever written, so the
operator's remedy refused to run (Fable HIGH 1, Opus HIGH 1). The remedy is now
**re-running the same `cut`**, and the re-run of a `cut` whose act is already on
the wire delivers the record instead of re-deciding the evidence (Opus MEDIUM 4,
which found the same re-run refusing with the release live). And draft 5's
`total_count` term compared the host's count against the runs the tool **read**
after reading every page, which is equal by construction and can never fire,
while the sentence beside it claimed the tool refuses the one-page state the gate
refuses: the gate's page is now a stated number with its cite, and a sha carrying
more runs than one page lists is a refusal with the re-freeze as its remedy (Opus
HIGH 2, Fable MEDIUM 2). Every repair below is a reader's, named where it lands;
where two readers wanted different shapes for one repair, the rule says which was
taken and why; and a passage this draft repaired says `draft 6`.

Three releases of this repository were cut in two nights by hand, and one is
being prepared by hand today. Each shipped. Each also failed in a way the next
one repeated. This tool is those failures closed, one rule each.

| the failure, from the record | the rule that closes it |
|---|---|
| v0.13.0 and v0.14.0 (nights of 2026-09-12, nova-tools #229): "Until the freeze, every unrelated merge was held. At the freeze the hold lifted with one rule: every later change to main gets a recorded delta review against the candidate, a ledger line on #164 naming the head, the reviewer and the CI run." The ledger was an issue comment written by a hand; the tag went on whatever the tip was | `candidate` freezes **one sha** as a record in the lane branch (rule 1); the tag goes on **that sha and no other** (rule 6); anything landed after it is not in the release, and a re-freeze is a new record that forces a new `delta` (rule 2) |
| the same nights: merges landed after the candidate was chosen, so the release shipped a tree nobody had read whole; a `--auto` merge landed before CI finished (Glenn, 2026-09-11: **never `--auto`**) | `delta` walks every landing between the last release and the candidate and **refuses while any landing lacks a read** keyed to its head (rule 3); this tool has no merge verb and no `--auto` to refuse — it cannot land anything |
| 2026-09-13: v0.15.0 prepared by hand as PR #232 — two install lines in `docs/USAGE.md` bumped by an editor, the release body drafted inside the pull request's own body | version sites are a **manifest**, never a grep (rule 5); the notes are a **file a line wrote**, read by another line, keyed to the file's hash (rule 7) |
| 2026-09-13: certification run 34771523657, dispatched on `main` at 9a95e33e, RED on `build-windows` and `test-windows (internal/swarm)` — `symlink_fifo_test.go:28:20: undefined: syscall.Mkfifo` — "a leg the fast tier never ran"; fix PR #235 blocks the tag | `certify` requires the certification aggregate **green at exactly the candidate sha**, read job by job from the wire, never from a badge or a run's status (rule 4); **a red is stop** |
| 2026-09-13, nova-tools #250: v0.15.0's tag run (34775563018) ran `release.yml`'s **own** copy of the test set serially on Windows inside a fifteen-minute job, was cancelled with packages still to run, and the release job skipped — the tag exists with no release object, while certification 34775151906 had passed the same commit `8ba256bb` in six minutes | certification is read **once**, from the one run that certifies the commit (rule 4), and the release workflow asks that same question rather than running a second, slower, drifting copy of it: nova-tools #252 replaces the test job with a `certified` job whose whole ask is one script, run twice, requiring every certification run at the tagged commit to be finished and the group carrying the newest `updated_at` to be uniformly green (rule 4, rule 6, work list item 11) |
| #229, the same night: the release run for v0.14.0 "failed once on Windows: `internal/merge TestThirtyConcurrentWriters` … Attempt 2 on the unchanged tag succeeded at 23:27:37Z (56 assets)" | `verify` reads the tag, the release, the asset count and names and the version the shipped binary prints, and names the first field that disagrees (rule 8); a red release run is re-run on the **same tag**, never widened, never re-tagged |
| #229: stacked PR #144 "when it was called merged, it had merged onto the stack, not main" | a landing is a **first-parent commit on the default branch**, walked in a clone that fetched the wire; nothing is a landing because a sentence said so (rule 3) |
| Glenn, 2026-07-22: "when I ask you to create a release, I always mean, create the full github release. Tags + github release." — and 2026-07-30, the third time: "please always make a real github release with release notes, not just a tag" | `cut` creates the tag and the release **in one act**; there is no verb that makes a bare tag, and the release workflow's create path becomes a refusal (rule 6) |
| Glenn, 2026-08-03: "A RELEASE TITLE STATES WHAT IS NOW TRUE, OR NOW POSSIBLE … does this name a capability, or a defect?"; 2026-09-06: "We never do releases with discovery details or archaeology. It's ALWAYS what the user gets." Measured today on the wire: the titles of v0.13.0 and v0.14.0 are the bare strings `v0.13.0` and `v0.14.0` | the notes file carries a title that is **not the tag**, a body that is **not generated**, and a recorded read by a line other than its author answering the one question (rule 7); `cut` refuses without all three |
| a release "goes through a tool, never `git tag` by hand" (Glenn's standing rule); a local tag is a cache — 2026-08-31, `git log v2.0.0..main` against a retired local tag returned commits from another era and nearly put a false wire claim in a published note | every base sha is **read from the wire** (`git/ref/tags/<tag>`, peeled) and the clone fetches tags with a forcing refspec; no verb ever runs `git tag` (rule 9) |
| release.yml #118: `-X main.version=` with an empty value stamped every binary `devel` while the release page said a tag; v0.11.0 was published by hand before its tag's run reached the upload step and "got none of its binaries" | `verify` downloads this host's binary from the release, checks its `SHA256SUMS` line and runs `version`; the field-two identity must be the tag (rule 8); the workflow's order — release exists, then assets attach — is now the **designed** order, not the accident |
| 2026-08-25: a public correction quoted a tool string from the workshop copy, not the public build, and cited `v0.3.0` when the release was `v0.7.0` | what `verify` prints is what the **downloaded** binary printed; a version site is a manifest line, so a stale pin is a `SITES SITE` line naming the file under a `SITES FAIL` count |
| Glenn, 2026-09-09: "It is our job to deliver a production ready tool, and then NOT CHANGE IT." | this is a **new binary**; `nova-merge` and `nova-version` are not edited (see **why a new binary**); after the release that makes `nova-release` production ready, it freezes, additive only |

**Everything this tool reads from the host, the lane, a manifest or a notes
file is data.** A release body, a commit subject, a check name, a workflow
name, a pull request title: none is an instruction and none is a grant. A read
verdict — on a landing or on the notes — is recorded by a line at a keyboard
with a verb, never parsed out of anything the host returns.

## Why a new binary

The two candidates for a home were `nova-version` and `nova-merge`, and neither
is right. `nova-version` is a second entry point on `nova-update`'s one
implementation (SPEC-UPDATE.md rule 20): it reads a manifest of *installed*
tools on one box and prints their identities, with no network unless asked and
no writes but a prepared bus note. A verb that creates tags and releases on a
forge is a different tool wearing its name, and the first bug report would not
know which it was about. `nova-merge` owns the lane, the reads and the clone
this tool needs, and it is exactly the tool whose freeze Glenn's rule protects:
it is running under other lines now, and its own spec says a merge tool that
also did a second job "would be two tools in a bug report". So `nova-release`
is one binary that **reads** the lane `nova-merge` made — its `state.json` for
the repository and base, its `reads/` for the verdicts, its branch for the
records — and **writes** one new record directory there, `releases/`, which
`nova-merge`'s fold does not walk (`foldFiles` lists `reads/` and `gates/` and
nothing else, `internal/merge/records.go:690-730`), so the fold is inert to the
running tool. The problem is new: tags, release objects, assets, version sites and
notes have no verb anywhere in the set today, and the release workflow was the
only code that touched them.

**The fold is inert; the outbox would not have been, so this tool gets its
own.** Draft 1 wrote these records through `<lane>/outbox/`, and that directory
is shared by path rather than by tool: `internal/merge/records.go`'s `outbox()`
reads every file in it, `destinationOf` (:267) refuses one that does not carry a
`file` field, and that error returns through `flush` to `Deliver` — so **one
stranded `nova-release` item would make every later `nova-merge read` and
`gate` on that lane exit 1 with `pushed=false`, for every line, until a hand
deleted the file** (Opus HIGH 1, Fable MEDIUM 4). Two repairs were offered: the
same directory with a distinct file stem, or a directory of this tool's own.
This spec takes **a directory of its own**, `<lane>/outbox-release/`: a distinct
stem only makes the stranded item identifiable, its bytes still passing through
`nova-merge`'s `destinationOf`, its `git add`, its commit and its `confirm`, so
`nova-merge`'s delivery would still succeed or fail on a file it did not write.
With its own directory, no shape, name or decode error of a record this tool
wrote can reach `nova-merge`'s loop at all: the class is gone rather than
narrowed. Every record still carries `file` (**the records**, below), because
the same compare-and-swap loop reads it for the destination. The one change to
`internal/merge` is **additive and inert**: the outbox directory becomes a field
on `Records` defaulting to `outbox`, so `nova-merge` with the default does
byte for byte what it does today. Both tools take the same
`<lane>/checkout.lock`, so the two flushes never touch the checkout at once, and
`fetchAndReset`'s `reset --hard` leaves an untracked directory alone
(records.go:383-393), so a stranded item survives a competing round exactly as
`nova-merge`'s does.

**And the outbox directory ignores itself, because `nova-merge`'s recovery path
reads the porcelain (draft 3).** `reset --hard` leaves an untracked directory
alone, but `nothingStaged` (records.go:355-361) is `git status --porcelain` == ""
and the lane's committed ignore file (`merge.GitIgnore`, records.go:779-789)
lists `/outbox/` and not `/outbox-release/`, so one item under
`<lane>/outbox-release/` answers `?? outbox-release/`. On the re-run after a kill
between push and confirm — the case SPEC-MERGE rule 22 promises to repair — the
checkout is not clean, the commit fails with nothing staged, five rounds back
off, and the reader gets `READ FAIL … pushed=false` because a `nova-release` item
sits in the lane (Fable HIGH 2; Opus MEDIUM 3, which named the same hole in
`merge.GitIgnore` and the property `internal/merge/source_test.go` pins). So
**the tool writes `<lane>/outbox-release/.gitignore` holding `*` on first use**:
self-contained, no change to `merge.GitIgnore`, no `init` re-run, and a lane that
already exists is covered the first time a release verb writes into it. Because
the same outbox scan reads that directory, `outbox()` skips `.gitignore` as it
skips `.tmp` and `.summary` (work list item 2) — an ignore file read as a record
would be this very class moved inside this tool's own loop. Test 1 gains the
kill-after-push re-run with an item present.

## The rules, numbered

Every rule here is normative. Each has one line in **tests this spec demands**
near the end. The date on a rule is the day it was learned.

1. **A candidate is one sha, frozen by a verb, recorded where the lane's
   records live.** `candidate --lane <dir> --tag <tag> --sha <40 hex>` checks
   that the sha is a commit reachable from the lane's base on the wire, then
   writes one immutable file, `<lane>/releases/<tag>/candidate-<sha12>-<at>-<rand6>.json`,
   carrying the `file` field every record in this lane carries, and commits and
   pushes it to the lane branch through the same compare-and-swap loop
   SPEC-MERGE rule 22 defines, under the same `<lane>/checkout.lock`, out of
   **this tool's own outbox**, `<lane>/outbox-release/` (see **why a new
   binary**). Every other verb reads the **newest** candidate
   record for `--tag` and refuses with `refusing to guess: no candidate for
   <tag>` when there is none, **with two named exceptions (draft 5)**: `status`
   is the verb a line types before anything is frozen, so no candidate is an
   answer and not a refusal — `candidate=-`, `STATUS TERM term=candidate
   ok=false`, `ready=false`, exit 0, as its own grammar already provided for
   (Opus MEDIUM 3, which found rule 1 and `status` answering one invocation two
   ways); and `verify` is keyed to the **cut record** rather than the candidate
   (rule 8), because it asks what shipped and not what is staged. The file, not
   a person's memory, is where the freeze is (#229's third open question:
   "Where does the freeze itself get recorded so tools can read it").
2. **A re-freeze is a new record, and it voids the old delta.** A second
   `candidate` for the same tag at a different sha is allowed and is printed
   `CANDIDATE OK … supersedes=<sha12>`; every `delta`, `certify`, `notes` and
   `tree` record is keyed to the candidate sha it was made for, so after a
   re-freeze `status` shows `delta=- certify=- tree=- notes=-` and `cut` refuses
   until all four are made again at the new sha — the notes read among them,
   because the notes describe a range the re-freeze moved (rule 7). This is how
   "anything merged after the freeze is either excluded from the release or
   forces a recorded delta read at the new candidate" becomes a mechanism rather
   than a sentence: a landing after the
   freeze is simply not in the tree the tag points at, and including it means
   a new candidate, a new walk and a read for it. (2026-09-12: the hand-kept
   ledger line "naming the head, the reviewer and the CI run" was this rule
   done by a person.)
3. **The delta is every landing since the last release, every landing has a
   read, and the base is an ancestor.** `delta` derives the base **from the
   wire**: the release the host names `releases/latest`, which is the newest
   **non-draft, non-prerelease** release by `created_at` — the host's own order,
   named here because draft 1 said "newest published" and named none, and the
   two orders differ (v0.14.0 was created 22:56Z and published 23:27Z) — peeled
   to a commit through `git/ref/tags/<tag>`, an annotated tag object as
   v0.13.0's is and a lightweight one as v0.14.0's is, both peeling to the
   commit. `--since <tag|sha>` names another, and is **required** when the host
   holds no release: `DELTA REFUSED: no release on the wire; name the base with
   --since`. The base P must be an **ancestor of S** (`merge-base(P,S) = P`), or
   the walk is empty or backwards and the tag goes on an older tree: `DELTA FAIL
   field=since`, re-checked at `cut`. (Fable, LOW; Stella 1: a release picked by
   publication date alone can be a maintenance branch's.)

   **How the base was chosen is recorded, because `cut` re-checks the same
   question and not a different one (draft 4).** There are two modes, and the
   delta record carries which one it used, the ref the caller named and the sha
   it resolved to: `since_mode` is `latest` or `named`, `since_ref` is the tag or
   sha the caller typed — or the tag the host's `releases/latest` answered — and
   `since` is the peeled commit.

   - `latest`: the caller named no base, so the host chose it. `cut` re-derives
     `releases/latest` on the wire now, ignoring a release for `tag` itself, and
     **a base that moved voids the delta**: a release published between the walk
     and the cut widened the range, and the walk must be redone. That is the
     movement detection this mode exists for.
   - `named`: the caller named a base with `--since`. This is also the **only**
     mode a first release has, when the host holds no release at all. `cut`
     re-resolves `since_ref`, requires it to peel to the record's `since`, and
     requires `merge-base(since, S) = since`. A release published elsewhere on
     the wire does **not** void it: the caller named this base deliberately, and
     a first release and a maintenance line have no `latest` to move.

   Draft 3 required `--since` when the wire held no release and then wrote a cut
   condition whose P was "the previous release's sha read from the wire this
   run" alone, so a first release could never reach `cut` at all, and an explicit
   non-latest base was overruled at `cut` by the base the caller had rejected
   (Stella 2). `status`, `cut` and the reconciliation of a lost create answer all
   read the mode from the delta record, so the three cannot ask different
   questions.

   It fetches the repository into its own clone with a forcing tag refspec and
   walks `<base>..<candidate>` **first-parent**: each commit on that walk is one
   landing. For **every** landing, merge or squash, the verb resolves
   `commits/<sha>/pulls`: exactly one associated pull request is the landing's,
   and **none, or more than one, is a refusal naming the commit** (`DELTA FAIL
   field=pr`) rather than a guess (Stella 1). A landing's **head** is its second
   parent when it is a merge commit, and otherwise the head oid the host records
   for that pull request. The landing's **author** is that pull request's author
   login, read from the host for **both** shapes — draft 1 resolved a pull
   request only on the squash path, so on 17 of this repository's last 25
   landings there was no author at all and rule 3's exclusion was untestable
   from its own words (Opus HIGH 2).

   **A login is an account and `who` is a line, so the map is stated.**
   SPEC-MERGE resolves the exclusion "by the names in the lane's own state, not
   by anything the host says about identity" (SPEC-MERGE.md:810-815), and a
   landed pull request is no longer an entry, so the lane holds no name to
   resolve against: measured on this repository's own range the logins are
   `gafferongames` and `rowan-claude` while the lines are `rowan`, `emma` and
   `alex`. The policy's `authors` file (rule 12) is that map, `<login><TAB><line>`,
   read at the tree at S and reviewed like any other line of the tree. For each
   head the verb looks in the lane's `reads/` fold for an approve recorded by a
   line who is **not** the landing's author under that map, **and for no standing
   hold for that head** under the newest-per-`(who, head)` fold, ties folding
   toward the refusing verdict (draft 3: SPEC-MERGE's read condition is "a hold
   anywhere -> HOLD, never merges" (SPEC-MERGE.md:789) and rules 7 and 13 here
   both say a hold blocks, but draft 2's rule 3 asked only for the approve, so a
   landing with one approve and a standing hold by another line counted as read —
   Fable MEDIUM 3). The fold `delta` reads is the **read-only** fold of
   SPEC-MERGE rule 23 (`FoldReadOnly`, `internal/merge/records.go:519-539`),
   which writes nothing and takes no checkout lock: a reader refused by another
   line's `<lane>/checkout.lock` would be a reader given a writer's answer
   (draft 3, Fable LOW). **A login the map
   does not name makes the exclusion untestable, and an untestable exclusion is
   not an assumed pass**: the entry prints `line=untestable`, the landing counts
   as unread, and the remedy names the login and the file. Because the map is
   read at the tree at S, a login it does not hold is repaired only by a commit,
   which moves the tip and forces a re-freeze and a new walk, so **the authors
   file is complete before the freeze**, as the sites manifest is (draft 3,
   Fable LOW).

   `DELTA OK` prints `landings=<n> read=<n> unread=0`; `unread>0` is `DELTA
   FAIL`, exit 1, and the remedy names each unread landing's `nova-merge read
   --pr <n> --head <sha>` line up to `--max`. Only an `unread=0` walk writes the
   delta record `cut` needs. A read recorded **after** the landing counts: a
   read is of a head, whenever it is recorded — that is what a recorded delta
   read at the new candidate is.

   **What a per-landing read proves, and what it does not.** A read is of the
   head a reader named. On a merge landing the head is the second parent by
   construction, and a conflict resolution written into the merge commit is in
   the landing and was in no head; on a squash the landed tree can differ from
   the reviewed head, and the host offers no proof either way (Stella 1). That
   residue cannot be closed per landing, so it is closed once and whole by rule
   13's read of the candidate tree, and `DELTA ENTRY` carries
   `landing=<merge|squash>` so the record says which proof it has. (#229; and
   #144, which "had merged onto the stack, not main": a squash onto a stack is
   not on the default branch's first-parent walk and is not a landing here.)
4. **Certification is decided by the runs at exactly the candidate sha that
   carry the newest evidence, and the release workflow's own gate is the
   oracle.** The workflow file, the aggregate job, the jobs a run must carry and
   the jobs whose `skipped` is allowed come from the release policy (rule 12),
   not from flags a caller picks per run, so what counts as certified is a
   reviewed fact of the tree at S rather than the caller's choice (Stella 2).
   `certify` asks the host for **every** run of that workflow whose `head_sha`
   equals the candidate, completed or not, **from every event** — never the
   newest run on the branch, because a `workflow_dispatch` on the default branch
   runs at that branch's tip, and after a post-freeze landing the tip is not the
   candidate — reading **every page** of runs and of jobs, because a cap is on
   what is printed and never on what is read.

   **The gate this tool must agree with, quoted from the file (draft 5).** At
   nova-tools #252, head `59b85fd6`, `release.yml` holds its whole ask in
   `.github/scripts/release-certified.sh` and runs that script **twice**: once as
   the `certified` job (release.yml:74-84) and again inside the `release` job,
   after that job's checkout and toolchain steps (release.yml:108-117; it is the
   third step, not the first — draft 6, Fable LOW), "because one snapshot cannot
   see a run created after it was taken — a red that starts after the first ask
   and finishes before the binaries are built would otherwise release"
   (release-certified.sh:4-6, the script's own header; release.yml:109-111 says
   the same of the second ask in its own words, ending "must still stop the
   release" — draft 6, Fable LOW and Opus LOW, which found draft 5 attributing
   the script's sentence to the workflow). The script's rule, in its own order:

   - **one snapshot, no status filter**: every run of the certification workflow
     at the commit, fetched once (release-certified.sh:16-25), because "two asks
     (completed, then in flight) are two snapshots, and a run that finishes red
     between them is in neither answer";
   - **one page, and the listing must be whole**: the ask is
     `…/runs?head_sha=<sha>&per_page=100` with no `page` parameter (:25), so the
     gate sees at most one page of 100 — the host's own maximum `per_page` — and
     a `total_count` different from the number of runs listed refuses, "the
     selection would be ambiguous" (:36-41);
   - **anything unfinished refuses**: any run at the commit whose status is not
     `completed` — queued, waiting, in progress, and **whatever its age** — is a
     refusal with a wait line (:43-47);
   - **the latest-stamp group decides, and it must be uniformly green**: every
     run whose `updated_at` equals the maximum is taken, and a member of that
     group that did not conclude `success` refuses, naming it (:48-70). The
     stamp, not `created_at`, because "a rerun of an older run id is newer
     evidence than a later run that was never rerun" (:20-23); and no run id
     breaks a tie inside the group, because "an older id rerun later is the newer
     evidence, so id order is wrong in both directions" (:50-51);
   - **the run's own `conclusion` is all it reads**: no job is fetched, no
     aggregate job is named, and no event is filtered;
   - **no completed run at all refuses**, and every refusal names the remedy:
     dispatch the certification workflow at the ref, then re-run the release run
     (:60-69).

   **`certify` asks that question, with the script as its oracle (draft 5).** The
   selection is the script's: every run at S from any event, the listing whole,
   **any** unfinished run at S pending, and the deciding evidence the group of
   runs sharing the maximal `updated_at`, which must be **uniformly** `success`.
   Draft 4 keyed the choice to `created_at`, waited only on a run **newer than**
   the deciding one, filtered by the policy's events, and called the two asks
   "one question asked twice, phrased the same way on purpose". They were not one
   question: run A green at S, run B green and newer, then A re-run red — a
   re-run does not move `created_at` — is a `cut` that prints OK and a gate that
   refuses, so the tag lands and the release gets no assets; and an **older**
   unfinished run beside a completed one is green for the tool and a refusal for
   the gate, which is reachable because `certification.yml:22-24` groups its
   concurrency by ref **and event**, so a schedule run and a dispatch run at one
   sha do not cancel each other. Both reads of draft 4 named this from both sides
   (Fable HIGH 1, Opus HIGH 1). The events filter is **deleted** rather than
   reconciled: a run the tool ignored and the gate counted is the same hole in
   the other direction. So the terms are:

   - the host's `total_count` at S different from the number of runs read is
     `CERTIFY FAIL … : the host reports <n> runs at <sha12> and listed <m>; the
     selection would be ambiguous`, exit 1. This is the **race guard**: the tool
     reads every page, so the two numbers are equal unless the listing moved
     under it or the host answered inconsistently, and either way the selection
     cannot be trusted;
   - the gate's **page ceiling** is a second term, and it is the gate's number
     and not the tool's (draft 6): the gate asks one page of 100 and paginates
     nothing (`release-certified.sh:25`), so a sha whose `total_count` is
     **greater than 100** is a state the gate cannot see whole and refuses, while
     the tool, which reads every page, would otherwise pass it. `CERTIFY FAIL …
     : the host reports <n> runs at <sha12> and the gate lists one page of 100;
     the gate will refuse this commit`, exit 1. **The remedy is the re-freeze**,
     because no run at a sha can be removed and the count only grows: a new
     candidate at a new sha starts at zero. Draft 5 wrote this state's sentence
     beside a term that could never fire — `total_count` against the runs read,
     equal by construction after full pagination — so `certify` passed at 101
     runs what the gate refuses, the tag landed and the release got no assets,
     which is #250's failure by another road (Opus HIGH 2, Fable MEDIUM 2). The
     number is stated here because it is the gate's request, and a repository
     whose gate paginates has no such ceiling; work list item 11 owns the gate;
   - **any** run at S that is not `completed` is **pending, not green**:
     `CERTIFY FAIL run=<id> status=<queued|waiting|in_progress> conclusion=-`
     (draft 6, Fable LOW: `in_progress` is a status and draft 5 printed it in the
     conclusion field, which `CERTIFY RUN` already separates), exit 1, `status` prints
     `certify=pending`, and the remedy is to wait for the aggregate and run the
     verb again. Pending is said in that word and never folded into green or into
     red. An unfinished run that already has a required job concluded anything
     but `success` is named red instead — `CERTIFY FAIL run=<id> job=<name>
     conclusion=<word>` — because a red leg is a red whether or not its siblings
     have finished, and the gate will refuse that sha the moment it completes;
   - the **deciding group** is every run at S whose `updated_at` is the maximum.
     A member that did not conclude `success` is `CERTIFY FAIL run=<id>
     conclusion=<word>`, exit 1. Nothing orders that group and nothing breaks its
     ties;
   - every run at S **outside** the deciding group prints a `CERTIFY RUN` line
     with its id, both stamps and its conclusion, `deciding=false`, so a red that
     was re-run past is on the record rather than erased. A superseded red is
     history and is never a refusal by itself (Stella 3); a `cancelled` run left
     by the certification workflow's own `cancel-in-progress`
     (`certification.yml:22-24`) is that case (Fable MEDIUM 3, Opus MEDIUM 7).

   **Where `certify` asks more than the gate, and why that is not a
   disagreement.** In **every** run of the deciding group the verb also reads the
   **aggregate job's conclusion by name**, then every job's, and requires
   `success` of all: `failure`, `cancelled`, `timed_out`, `neutral`,
   `action_required`, a `skipped` job the policy does not name as skippable, and
   a job the policy requires that is **absent** from the run
   (`conclusion=absent`, which is how a required leg silently dropped from a
   matrix is caught — Stella 2) are each a `CERTIFY FAIL` naming the job and its
   conclusion. The script reads the run's `conclusion` and fetches no job, so a
   run whose required leg was dropped or skipped concludes `success` and vouches
   at the gate (Opus HIGH 1). The two answers therefore differ in exactly one
   direction — **`certify` refuses what the gate would pass, and never passes
   what the gate would refuse** — which is the safe direction, and it is the
   whole of the difference this spec accepts. Work item 11 does **not** close it:
   the gate is one shell script that must answer from one host call in seconds,
   and giving it the policy's job names would put a second tool inside a
   workflow, which is the drift #250 was. A run's own status or a status badge is
   never read as evidence (2026-09-07: "a QUEUED certification run hides a
   COMPLETED red leg, and five merges landed on a red main"). A green writes the
   certify record with the deciding group's run ids, the receipt run and attempt,
   the maximal `updated_at`, the job names **and the attempt each
   conclusion came from**, the count and the policy's hash at S. **`event` is the
   receipt run's and says so** (draft 6, Fable LOW): the deciding group can hold a
   `schedule` run and a `workflow_dispatch` run at one sha — the very pair this
   rule names, since `certification.yml:22-24` groups its concurrency by ref
   **and** event — so one `event=` word cannot describe a group, and the field is
   the one run the line prints, `run=`, whose event it is.

   **A partial re-run is coverage across the attempts of one run, never across
   runs.** A re-run of failed jobs makes a new attempt carrying only those jobs,
   so a required job absent from the latest attempt but `success` in an **earlier
   attempt of the same run** counts, and the record names the attempt each
   conclusion was read from; a required job absent from **every** attempt of that
   run is `conclusion=absent`. **A job that appears in more than one attempt is
   read from the newest attempt it appears in** (draft 6, Fable LOW: draft 5 said
   an earlier attempt can supply a missing job and left unsaid which attempt
   decides when two carry the job, so a re-run that turned a job red could have
   been answered by the attempt before it). Absent and proven-in-an-earlier-attempt are
   different answers and this rule keeps them different (Stella 3).

   **A record is not current proof.** `cut` re-asks the whole question on the
   wire — every run at S, the listing whole, any unfinished run, the deciding
   group and its jobs — because a green run can be re-run red after the record
   was written and a newer run can be red before it finishes (Stella 2, Stella
   3): the term is decided on the wire's answer now, not on the record's then. No
   run at the sha is `CERTIFY FAIL … no completed run at <sha12>`; the remedy
   names the **re-freeze first** and the dispatch second, because dispatching at
   a moved tip cannot produce a run at S (Opus). **A red is stop**: the verb
   exits 1 and nothing downstream can proceed; there is no `--allow-red`.
   (2026-09-13: run 34771523657, `build-windows` and `test-windows
   (internal/swarm)` red at 9a95e33e, "a leg the fast tier never ran".)

   **The release workflow asks the same question of the same host, and that is
   deliberate (draft 5).** Three things follow for this tool, and none of them is
   a new rule:

   - the tagged commit the workflow asks about **is S**, because rule 6 tags S
     and reads the tag back peeled to prove it, so the two asks are about the
     same tree;
   - the gate asks **twice**, the second time inside the `release` job, so a
     certification re-run that goes red between the `cut` and the binaries leaves
     a release with no assets rather than assets nobody vouched for. That is the
     right failure and `verify` names it, `field=assets` (rule 8);
   - a difference in the two answers is evidence that arrived between the `cut`
     and the tag event — a run created, finished or re-run in that gap. The
     gate's answer is the later one and it decides what ships.

   (2026-09-13, #250: `release.yml`'s second, slower copy of the test set was
   cancelled on Windows at its fifteen-minute cap and v0.15.0's tag got no
   release object, while certification had passed the same commit in six minutes.
   Two copies of one test set drift; one run read twice does not.)
5. **Version sites are a manifest, and the frozen tree already carries the
   new tag.** The policy's `sites` line (rule 12) names a tab-separated file
   **in the repository**, read from the tree at S rather than from anybody's
   disk, so the manifest that gates is the manifest the frozen tree carries
   (Opus MEDIUM 8, Stella 5): `path`,
   `template`, `count` (default 1), where `template` contains `{tag}` exactly
   once and is otherwise literal — `docs/USAGE.md<TAB>go install
   example.com/tools/cmd/nova-bus@{tag}` is one line. No grep, no regular
   expression, no discovery: a site the manifest does not name is not a site,
   and a site it names that is missing from the tree is a refusal naming the
   path, because a manifest that has gone stale is the failure a grep would
   have hidden (2026-08-25: a doc pinned `v0.3.0` while `v0.7.0` was current).
   `bump --policy <path> --dir <checkout> --from <tag> --tag <tag>` rewrites
   each site in a checkout the caller names, from the rendering with `--from`
   to the rendering with `--tag`, refusing a site that holds neither exactly
   `count` renderings of `--from` nor exactly `count` of `--tag` already; it
   commits nothing and pushes nothing, and
   the change lands through the lane like any other, **before** the freeze.
   `bump` reads the policy, and the sites manifest the policy names, **from
   `--dir`**, the caller's own checkout, because before the freeze there is no S
   to read them at; it takes no `--sites` (draft 3: draft 2's rule 12 said the
   policy is read from the caller's checkout "only by `bump`" while `bump` took a
   sites file and no `--policy`, so one of the two was wrong — Fable MEDIUM 4,
   Opus LOW). Every site path is checked before it is opened — relative, no
   `..`, and a **regular file** under `--dir`, never a symlink and never a FIFO
   (this repository's own rule, security#30).

   **What `bump` promises about a failure halfway, stated as a protocol and not
   as a word (draft 4).** Draft 3 said `bump` is "all or nothing", and it is not:
   planning every site before writing any prevents a **validation** failure from
   writing part of a bump, but N separate renames cannot be one act, and a write
   that fails at site four, or a kill between site three and site four, leaves
   sites one to three changed on a disk this tool does not own (Stella 1). A tool
   that cannot keep a promise must not make it. So:

   1. **Plan.** Every site named by the manifest is read, its renderings of
      `--from` counted against `count`, its new bytes computed, and its `sha256`
      before and after recorded. A site that already holds `count` renderings of
      `--tag` and none of `--from` is **not** a refusal: it is counted `done`,
      which is what makes a re-run finish an interrupted bump (step 4). Any
      other disagreement with `count` refuses the **whole** bump before a byte is
      written: `BUMP FAIL <path> sites=<n> done=<n> applied=0: <reason>`, exit
      1. This is the promise draft 3 could keep, and it is kept.
   2. **Apply, in manifest order, each site a temp file in the same directory
      and a rename.** A rename either happened or did not, so no site is ever
      half-written. Immediately before each rename the site is re-read and its
      `sha256` compared to the one the plan recorded: a caller who edited the
      file between the plan and the apply gets a refusal naming the path, never
      an overwrite of their edit with bytes computed from what the file used to
      be. **That refusal is step 3's line and step 3's exit (draft 5)**: the
      sites already renamed print their `BUMP PARTIAL` lines, the `BUMP FAIL`
      line names the edited path and carries `applied=<n>`, and the code is 1.
      Draft 4 gave this case no code and no report at all, and its exit-1 row
      said "nothing written" while three files had moved (Opus MEDIUM 4).
   3. **Report what is on the disk.** A write or rename that fails stops the
      verb where it stands — later sites are not attempted — and prints one
      `BUMP PARTIAL <path> before=<sha256:12> after=<sha256:12>` line for **every
      site already renamed**, then `BUMP FAIL <path> sites=<n> done=<n>
      applied=<n>: <reason>; re-run the same bump to finish it`, **exit 1**: the
      verb ran and said no, the durable before-and-after hashes are how a caller
      knows exactly which files moved and to what, and the re-run of step 4 is
      the remedy on the line. **`applied=` is renames this run and `done=` is
      sites already at `--tag`** (draft 5): draft 4 exited 2 here and made
      `done=3` mean both a planning refusal and a partial apply, so no scanner
      could tell the two apart, and it put a half-applied bump in the row
      SPEC.md reserves for "could not run" beside `refusing to guess`. The
      house precedent for the same class is exit 1: SPEC-MERGE's record written
      and push rejected is `READ FAIL … pushed=false` at exit 1 with "re-run the
      same verb to push it" on the line (SPEC-MERGE.md:387, :559). A bump with
      three files renamed is that case, and a failure at site **one** — nothing
      written there, nothing renamed before it — is the same line with
      `applied=0` and no `BUMP PARTIAL`, which is the gap draft 4's two rows left
      between them (Fable MEDIUM 2). The `CUT FAIL` at 2 is different in kind:
      nothing is known there, and rule 5 knows everything.
   4. **Resume, or revert with what is printed.** Re-running the same `bump`
      finishes it: a site already holding exactly `count` renderings of `--tag`
      and none of `--from` is counted `done` and not rewritten, a site still
      holding `--from` is applied, and **anything else is a refusal naming the
      path** — a mixed or hand-edited site is a question for a person. This is
      also what makes a kill mid-apply recoverable with no journal file: the disk
      is the state, and the manifest is how to read it. `--dir` is a checkout the
      caller owns and is under their version control, so the honest revert is
      theirs (`git checkout -- <path>` over the paths the `BUMP PARTIAL` lines
      name); this tool does not write a backup, because a backup outside `--dir`
      would break rule 10 and a backup inside it would be a file the manifest
      does not name. **The `BUMP PARTIAL` lines are exempt from rule 10's cap
      and say so** (draft 5, Opus LOW): they are the only record of which files
      on a caller's disk moved and to what, and a capped report with a `MORE`
      line would name some of them and lose the rest.

   Edits a caller made are preserved in the two senses a rewrite can preserve
   them: `bump` rewrites the manifest's renderings and nothing else in the file,
   and a site whose bytes moved between the plan and the apply is a refusal
   rather than an overwrite (step 2).

   `status` and `cut` then check that every site **at the candidate tree**
   renders `--tag` exactly `count` times, `SITES OK sites=<n>`, and a site
   that does not prints `SITES SITE <path>: <reason>` with the count line
   following it (draft 4, Opus polish 4). The tag therefore goes on a
   tree that already says its own version, and `cut` **never edits a tree**:
   the candidate is frozen, and a tool that bumped a file after the freeze
   would be tagging a commit nobody read. (2026-09-13: PR #232 is this rule
   done by hand — two lines in `docs/USAGE.md`, one pull request.)
6. **`cut` is one act: tag and release together, from a predicate, with no
   bare-tag path.** When the predicate in **the cut condition** holds, `cut`
   makes one host call that creates the tag **at the candidate sha** and the
   release with the notes' title and body in the same request (`gh release
   create <tag> --target <sha> --title … --notes-file … --verify-tag` is not
   it, because `--verify-tag` presumes the tag; the release-create API with
   `target_commitish=<sha>` creates the tag and the release as one object).
   It refuses a `--draft` and a `--prerelease` flag, and that refusal is
   Conventions' unknown-flag one-liner at exit 2 rather than a third refusal
   shape of this tool's own (draft 3, Fable). (The first is a release
   nobody can install; the second is a name for shipping before the predicate
   holds — draft and prerelease are legitimate states of a release in general,
   and a tool that offered them would need a second predicate saying when they
   are earned, which is an additive verb for a later release, named in the work
   list rather than a flag smuggled in here; Stella 6, whose point that this
   team's preference is not every user's is taken and answered that way.) It
   refuses when a tag of that name exists at **another**
   sha, `CUT REFUSED: <tag> exists at <sha12>, candidate is <sha12>`; and when
   the tag exists at exactly the candidate with no release (the repair path
   for a workflow that half-ran) it creates the release alone and says so,
   `created=release`. There is no verb that pushes a tag without a release,
   and no flag that suppresses the notes.

   **The create is not a compare-and-swap, so the tag is read back.** The host
   documents `target_commitish` as *unused if the Git tag already exists*, and
   it does not refuse: a tag pushed by another hand between the wire term and
   the create binds the release to **that** sha and still answers 201, and draft
   1 would have printed `CUT OK` and recorded S for a release that shipped
   another tree (Fable HIGH 1, Stella 3). So `cut` holds `<lane>/checkout.lock`
   across the act — which serialises this tool's own publishers on this lane —
   re-reads the wire term and the notes hold immediately before the call, and
   after **every** answer, success or ambiguous, reads `git/ref/tags/<tag>` back
   **peeled**. `CUT OK` prints and the cut record is written **only when the
   peeled tag equals S**; otherwise `CUT FAIL field=tag expected=<S12>
   found=<x12>`, **exit 1** — the verb ran and the wire said no — no record, and
   nothing is deleted or re-tagged: conflicting wire state halts (Stella 3).
   **The exit is decided here and nowhere else** (draft 4, Opus polish 6: draft
   3 said "stated once" in a document that then lists the same two cases in the
   exit table and the paragraph under it, which is a table doing its job and not
   a second decision): **there are three `CUT FAIL` shapes, two at exit 1 and one
   at exit 2, and two tokens tell all three apart** (draft 6, Opus MEDIUM 3).
   `field=tag` is this one, the read-back mismatch, exit 1. `pushed=false` is the
   record-not-delivered one below, also exit 1 — draft 5 added that shape thirty
   lines after writing "the only `CUT FAIL` at 1", so a scanner reading "no
   `field=tag`" as "exit 2, host answer" misread the line that says a release was
   created and the lane does not know it. A `CUT FAIL` carrying **neither** token
   is the host-answer one of **the cut condition**, exit 2. The exit table
   repeats those three rows and adds nothing to them (draft 3: draft 2's exit
   table put the token in its exit-2 row and made this paragraph and the table
   disagree — Opus MEDIUM 4, Fable LOW).
   The line says what the wire now holds:
   the release this call created **exists** at `<url>`, bound to a tag that
   points elsewhere, and removing it is a hand's act and not this tool's (draft
   3, Fable LOW). A create whose answer was lost is reconciled by re-running
   `cut`: the tag at S carrying a release that is **neither draft nor
   prerelease** and whose title and body are the notes' prints `CUT OK …
   created=already`, which is this operation finishing rather than a second one
   (draft 3, Fable LOW: draft 2's reconciliation accepted a draft that rule 8
   would then refuse).

   **The reconciliation is asked before the evidence, because the predicate is
   about acting and the act is already done (draft 6).** `cut` reads the wire
   term **first**: when the tag stands at S carrying a release that is neither
   draft nor prerelease and whose title and body are the notes' — the last clause
   of **the cut condition** — this run creates nothing, evaluates no other term,
   writes or delivers the cut record, and prints `CUT OK … created=already`.
   Draft 5 had `cut` stop at the first failing term in order (**the cut
   condition**) with the wire term last, and term R re-read on the wire now, so
   the operator told to re-run after a `pushed=false` — or after a 502 — got
   `CUT REFUSED: certify`, exit 1, when a certification run had been re-run red
   in the gap, with the release live on the wire and the record still in
   `<lane>/outbox-release/`: the act had happened, and no verb in the tool would
   say so (Opus MEDIUM 4). Re-deciding the evidence for an act already on the
   wire cannot unpublish it; it can only leave it unrecorded. What the terms are
   for is the decision to create, and that decision was made. A red certification
   after a release is published is real and is the release workflow's to catch
   (rule 4's second ask) and `verify`'s to name (`field=assets`, rule 8) — not a
   reason to withhold the record of what shipped.
   The comparison the wire term makes on a re-run is against **the outbox item's
   own `notes_hash` and `title` when one stands**, because that record is the act
   being reported; with no item in the outbox it is against the standing notes at
   S (rule 7). A tag at S whose release does not match — a draft, another title —
   is not this act finishing and is not reconciled: it is the ordinary refusal
   the wire term already names.

   **The lock is released before the record is delivered (draft 4).** `cut`
   holds `<lane>/checkout.lock` from the re-read of the wire terms through the
   create and the peeled read-back, and **releases it before writing the cut
   record**, because delivering a record takes that same lock itself
   (`internal/merge/records.go:152`, `Deliver` → `LockCheckout`) and a verb that
   held it would wait `--timeout` on itself and fail an act that had already
   succeeded on the wire (Fable, polish 2 on the draft-3 APPROVE). Nothing
   between the read-back and the delivery can change what the record says: the
   record states what the wire answered and what the tag peeled to at the moment
   the lock was held, both already read. **A line kept waiting on that lock must
   be told which tool holds it (draft 5)**: the waiter's line is fixed text
   naming `nova-merge` (`internal/merge/lock.go:90-92`), so a line running
   `nova-merge read` on a lane while a release is being cut is sent looking for a
   `nova-merge` that does not exist. The holder's tool name becomes a field of
   the lock the way the outbox directory and the commit prefix do, in the same
   additive change (work list item 2, Opus MEDIUM 6).

   **A `cut` that acted and could not deliver its record says both, and exits 1
   (draft 5).** Delivery gives up after its rounds with the record still in the
   outbox and not at the remote tip (`internal/merge/records.go:283-350`), and
   because the delivery now happens after the lock is released and after the wire
   act, this is the ordinary tail of a **successful** `cut` and not an exotic
   case (Opus HIGH 2). SPEC-MERGE gives every writing verb the shape, and this
   tool takes it unchanged for `cut` and for `delta`, `certify`, `notes` and
   `tree` as `candidate` already had it: `CUT FAIL … created=<tag+release|release>
   url=<url> file=<path> pushed=false: <reason>; re-run the same verb to push
   it`, **exit 1** (SPEC-MERGE.md:387, :559). The line says both facts in one
   place — the release exists at `<url>`, the lane does not yet know — so the
   operator is never told the release shipped by a line that names no failure.
   The re-run is the whole remedy, and it is one **because the wire term is asked
   first** (draft 6): the re-run reads the tag at S and the release this `cut`
   created, takes that as the reconciliation, evaluates nothing else, finds the
   outbox item, restarts the compare-and-swap loop and prints `CUT OK …
   created=already` and `pushed=true`. Test 6.

   **`cut` publishes a release that has no assets yet, and says so.**
   `release.yml` is triggered by `push: tags: ['v*']` (release.yml:35-38 at
   `59b85fd6`), and
   whether the release-create call's tag creation raises that event for the
   credential this tool runs under is a fact about the host to be **measured,
   not assumed** (Stella 6): the first `cut` is followed by `verify`, whose
   `field=assets` names the workflow run's status or its absence, and the remedy
   when there is no run is that workflow's own `workflow_dispatch` at the tag.
   `CUT OK` therefore carries `assets=pending`. Published, assets-pending and
   verified are three states, and no line of this tool says "ready to install"
   until `verify` does. **And that workflow can now say no (draft 4)**: since
   nova-tools #252 it asks, twice, whether every certification run at the tagged
   commit has finished and whether the group of them carrying the newest
   `updated_at` is uniformly green, and refuses the release otherwise (rule 4),
   so `assets=pending` covers three outcomes — the run has not started, the run
   is building, or the run **refused** because certification at S went red or was
   still in flight. `verify`'s `field=assets` names which, and the remedy for the
   refusal is that workflow's own: certify the commit, then re-run the run.
   The release workflow's job is then
   to **attach assets to a release that exists**: its `gh release create …
   --generate-notes` path becomes `refusing: no release for $TAG; nova-release
   cut creates the release, this workflow attaches to it` (work list item 11).
   (Glenn, 2026-07-22 and 2026-07-30; release.yml's own comment on v0.11.0.)
7. **The notes are written by a line, from the diff, read by another line, and
   keyed to (hash, S).** The notes ride the lane branch **inside the read record
   itself** (draft 3): `notes` normalises the file the reader names and writes
   the normalised bytes beside the record as its summary,
   `<lane>/releases/<tag>/notes-<hash12>-<at>-<rand6>.summary` — **one immutable
   copy per read, named by its hash** — so the reader and the cutter hash one
   copy on the wire instead of each hashing their own checkout of a local path
   (Fable MEDIUM 5), and `status`, `cut` and `verify` take the title and the body
   from the standing approve's summary rather than from anybody's disk.

   **Draft 2 put a `notes.md` on the branch, and the loop it reuses cannot carry
   one.** These records ride `internal/merge`'s compare-and-swap loop, whose
   `outbox()` (records.go:220-265) skips a `.summary` and calls `destinationOf`
   on every other item; `destinationOf` (records.go:268-278) `json.Unmarshal`s
   the body and requires a `file` field, so a markdown body fails, `flush`
   returns the error and the item stays — the first `notes` verb would strand
   `<at>-<rand6>.md` in `outbox-release/` and every later `nova-release` verb on
   that lane would exit 1 at flush, which is exactly the class this tool's own
   outbox exists to remove (Fable HIGH 1). A second draft written to the same
   path would also be a record edited and replaced, against rule 22's "never
   edited and never replaced" and the `WatchWrites` tripwire (records.go:206) —
   and no verb in draft 2 put the first copy there at all, since `notes` requires
   a verdict, so the reader would have uploaded the copy they then approved
   (Opus HIGH 2). So **`notes.md` is dropped**: nothing but a JSON record and its
   summary rides the loop, no path is ever rewritten, and a second draft is a
   second read at a second hash.

   `--notes <file>` is a flag of the `notes` verb alone — the local file the
   reader read — and no other verb takes one, because every other verb's copy is
   the standing approve's summary on the branch (draft 3: draft 2 required
   `--notes` on `status` and `cut` while `verify` read the branch's, and said
   why for neither — Opus LOW). Its first line is `# <title>` and its remainder
   is the body; a first line that is not `# <title>` is `NOTES FAIL <path>: first
   line is not "# <title>"`, exit 2, before anything is hashed (draft 3, Fable
   MEDIUM 5: draft 2 stated the shape and named no refusal).

   **The bytes are defined, so the hash is.** Before anything is hashed or
   compared the file is normalised: CRLF and lone CR become LF, trailing
   whitespace on every line is dropped, and **every trailing blank line, the
   final newline among them, is dropped** (draft 3: draft 2 dropped one newline,
   so a file ending in blank lines still ended `\n\n` while the host strips
   trailing whitespace whole, and `verify` printed `field=body` for a release
   that was right — the false positive this rule exists to prevent; Fable
   MEDIUM 5). The
   **title** is the first line without its `# `; the **body** is everything
   after the first line with leading blank lines dropped. The hash is the
   `sha256` of the normalised file, and it is the same number on a CRLF
   checkout and an LF one. `verify` compares the release's body to the notes'
   body **as strings under that same normalisation, never as raw hashes**,
   because the host may normalise what it stores and a raw-hash comparison would
   report `field=body` for a release that is right (Fable MEDIUM 5, Opus
   MEDIUM 8).

   `cut` checks three things it can check: the title is not empty, is not the
   tag, and does not **end** with the tag — `Release v0.15.0` names nothing a
   reader can use, and neither does `v0.14.0`, the title both of last night's
   releases carry, while a title that opens with the tag and then says something
   (`v0.12.0 — the fuse`) is fine (Fable, LOW: draft 1's "the tag with a prefix"
   named the wrong half); the body is not empty and does not contain the host's
   generated-notes markers (`## What's Changed`, `**Full Changelog**:`), because
   a body a tool wrote is a list of pull request titles, which is archaeology by
   construction; and the lane's `releases/<tag>/` holds a **notes read for
   (hash, S)** — `notes --who <name> --notes <file> --author <name> --verdict
   approve|hold [--note <text>]` writes one immutable record carrying both
   the hash and the candidate sha it was made at, and a `hold` blocks exactly as
   a lane hold does. **`notes` makes the same three checks `cut` makes** and
   refuses to record a read on notes `cut` would refuse: an approve of a title
   named for the tag alone costs a line's attention and buys nothing (draft 3,
   Fable LOW: test 7 named the refusal and draft 2 gave the check to `cut`
   only).

   **Which hash, when two drafts stand (draft 4).** `cut` takes no `--notes`, so
   the term needs a rule for choosing among notes records at S: it is the
   **newest notes record for S by `at`** that names the hash, and only the
   verdicts for that hash decide. An older draft's standing approve neither ships
   nor blocks; a `hold` recorded on the newest hash blocks, as any hold does. The
   general "the newest by `at` wins" under **the records** is a rule about record
   kinds; this is the one place the term needed it said about a hash (Opus,
   polish 1 on the draft-3 APPROVE).

   **The exits `notes` uses (draft 4, Opus polish 5).** They follow the split the
   exit table already makes. A notes file that does not **parse** — a first line
   that is not `# <title>` — is `NOTES FAIL <path>`, exit 2, in the row with the
   policy and the manifests. A title or body the rule **judges** — empty, the
   tag, ending with the tag, a generated marker — is the verb running and saying
   no about content it read: `NOTES FAIL <path>`, **exit 1**, the same answer
   `cut` gives for the same notes. A missing `--who` or `--author` is a missing
   flag, exit 2.

   **A re-freeze voids the notes read**, as it voids the delta, the certify and
   the tree record, even when the file's bytes did not change: after a re-freeze
   `status` prints `notes=-` beside `delta=- certify=- tree=-`. The notes
   describe a range, and a re-freeze moves it — draft 1 keyed the approve to the
   hash alone, so a release one landing larger would have shipped un-noted with
   a counted read (Fable HIGH 2, Stella 4).

   The reader answers one question, and the tool cannot: **does every sentence
   say what the reader of this release gets?** The tool prints the question on
   `NOTES OK` so the reader saw it.

   **The notes' author is declared, always, and is never inferred (draft 4).**
   `--author <name>` is **required** on `notes`; the record carries it; an
   **approve** whose `--who` equals its `--author` is refused at `notes` before a
   record is written (`NOTES REFUSED: --who and --author name one line; a notes
   read is another line's`, exit 2 — it is about the invocation, as the exit
   table's split says), and the predicate's term is "a standing approve by a
   line other than the record's declared author". **A `hold` with `--who` equal
   to `--author` stands (draft 5)**: an author withdrawing their own notes is a
   line refusing its own work, which nothing here has a reason to refuse, and
   draft 4 said `approve` in the rule while its exit table and its test 7 refused
   any such `notes` — one of the two had to go and the narrower is right (Fable
   LOW). Draft 3 made `--author`
   optional and fell back to "whoever recorded the candidate or the delta", so a
   line that wrote the notes and approved them without the flag inherited an
   author it never was — A records the candidate, B writes and approves, and B's
   self-review counted (Stella 4). **The inference is deleted, not reconciled**:
   there is no second rule about undeclared authors, because there is no
   undeclared author. What the record holds is a **declaration**, and this spec
   claims no more than that: it is provenance a line typed, not proof of a
   person, and two lines sharing one forge account are still two `who` names and
   two `--author` names here (rule 3's `authors` map is about logins on the host
   and has nothing to do with this term). Nothing stops a line typing another
   line's name as `--author`, and nothing could: it is exactly the trust `--who`
   already carries everywhere in this lane model, where names resolve "by the
   names in the lane's own state, not by anything the host says about identity"
   (SPEC-MERGE.md:810-815), and a spec that claimed more of one flag than of the
   other would be claiming it of the same keystroke (draft 5, Fable LOW).
   `delta` still carries `who`, as every record does, because a record names its actor; nothing reads it as an author
   any more.

   This read is about the notes: it
   neither satisfies rule 3 nor stands in for rule 13's read of the tree, and
   the three are separate terms (Stella 4). `delta` prints the exact range so
   the notes are written from the diff, not from the sitting
   (identity/releases.md, 2026-09-06: "the notes get drafted in the same sitting
   as the fold, in the fold's register").
8. **`verify` reads the release back and names the first field that
   disagrees.** `verify` is keyed to the **newest cut record for `--tag`**, not
   to the newest candidate (draft 5): S, the title and the notes hash come from
   that record, because the question is what shipped. With no cut record for the
   tag it refuses, `refusing to guess: no cut record for <tag>`, exit 2, **and
   that refusal's remedy is `cut`, not a flag** (draft 6): a tag with no cut
   record is either an act this tool never made or an act whose answer was lost,
   and re-running the same `cut` is what reads the wire back and writes the
   record — which is why no `CUT FAIL` sends an operator here any more (rule 6,
   both draft-5 reads, HIGH 1). Draft 4
   read the candidate, so a re-freeze after a `cut` — which voids every record
   keyed to the old S, the notes read among them (rule 2) — left `verify` with no
   standing approve to take a body from and no line saying what it printed (Opus
   LOW). After `cut`, and after the release workflow has run, `verify`
   reads, in order: the tag ref, peeled, which must be **the cut record's sha**
   (draft 6, Fable LOW: draft 5 keyed the verb to the cut record in its first
   sentence and then said "the candidate sha" here, which is the key a re-freeze
   moves); the
   release object by tag, which must exist, be neither draft nor prerelease, and
   carry the title and a body equal to the body of **the notes summary the cut
   record names, on the lane branch** (rule 7, draft 3) under rule 7's
   normalisation; the assets, whose count and names must equal **the expected
   set the policy derives** — every tool the policy's `tools` line names, times
   every platform in the policy's platform file, named
   `<tool>_<tag>_<goos>_<goarch>[.exe]`, plus `SHA256SUMS`; and the binaries:
   for every tool the policy's `probe` line names, it downloads this host's
   build into `--work`, checks its line in `SHA256SUMS`, runs it with `version`,
   and requires field two to equal the tag.

   **The expected set is a fact about a repository, stated in that repository's
   file.** This repository's policy says `tools cmd/*`, and **`release.yml`'s
   build loop reads that line** rather than holding a set, an exclusion or a copy
   of its own — the platform repair of rule 12, applied to the shipped set as
   well (draft 3: release.yml:132-134 at `59b85fd6` keeps the exclusion of a tool
   that must not ship "in one visible place" inside the workflow, so `verify`'s
   expected set could name an asset the build loop deliberately did not build;
   work list item 11 — Opus MEDIUM 5). Elsewhere a `cmd/`
   directory need not be a shipped binary, and a certification platform need not
   be a shipped platform, so neither is a rule inside the tool (Stella 5, Opus
   MEDIUM 4). `probe all` is this repository's value and the recommended one:
   #118's hurt was an unstamped `nova-wake` beside stamped siblings, so a probe
   of one tool can miss exactly the tool that mattered, and one host's downloads
   are inside the two-minute rule. (Fable wanted every tool and no flag, Opus
   wanted the flag kept with an `all`; the policy line is both — a stated set,
   `all` by default here, and no per-run choice.)

   **What the checksum proves.** `SHA256SUMS` comes from the same release as the
   binary, so a match proves the set is self-consistent and the download was not
   truncated. It is **not** independent authenticity, and this spec claims none
   (Stella 5); what makes the identity checkable is field two of `version`
   against the tag, which is a different fact, and one host's one run of one
   tool is evidence about that binary and not about the other platforms', which
   `VERIFY FIELD` lines say platform by platform rather than implying. **A
   downloaded file whose `SHA256SUMS` line disagrees is not executed**: the
   order is tag, release, assets, checksum, then `version`, and a `field=checksum`
   failure stops that binary before it runs (Stella 5).

   Each check is one `VERIFY` line; the first mismatch is `VERIFY FAIL
   field=<name> expected=<x> found=<y>`, exit 1, and the remaining checks still
   print, and **`VERIFY FAIL` carries the same counts `VERIFY OK` does**, because
   SPEC.md's count line prints on failure as well as success (draft 4, Opus
   polish 4: draft 3 said so here and then gave the counts to the `OK` line
   alone). A release whose assets are not
   there yet is a `VERIFY FAIL field=assets` naming the release workflow run's
   status, or its absence and the dispatch that starts one — **and since
   nova-tools #252 one of those statuses is a refusal** (draft 4): a run that
   stopped at its `certified` job — or at that job's second ask, inside the
   `release` job — because a certification run at the tagged commit was still
   unfinished, or because the group of them carrying the newest `updated_at` was
   not uniformly green (draft 5, rule 4).
   `field=assets` names that job's conclusion, so the remedy is the workflow's —
   certify the commit, re-run the run — and never a re-tag (rule 4). `verify`
   never waits
   (two-minute rule: a release build is a twenty-minute job, and a verb that
   polls it is a loop with no work). A red release run is re-run **on the same
   tag**; nothing here re-tags. (#118: `devel` under a tag; #229: "Attempt 2 on
   the unchanged tag succeeded … 56 assets" — which was eleven tools on five
   platforms **that night**, a measurement of v0.14.0 and not a constant:
   `main` holds thirteen `cmd/` directories today, so the next release's set is
   66, and no test or asset rule in this spec carries either number.)
9. **Every sha is read from the wire; no verb runs `git tag`.** The previous
   release's sha, the tag's sha, the candidate's reachability and the release
   object come from the host's API or from a clone that fetched
   `+refs/tags/*:refs/tags/*` this run. `git log <tag>..` is never run against
   a tag the clone did not just fetch with a forcing refspec. The one tag this
   tool makes is made by the host inside the release-create call of rule 6.
   (2026-08-31: "a local tag is a cache of a remote ref and the one ref git
   will not correct for you".)
10. **Bounded output, one remedy line, every loop ends.** SPEC.md's cap-and-count
    rule governs and is not restated; what is this tool's to say is where it
    applies. **Every verb that lists takes `--max <n>`, default 20, `0` for
    all** — `delta` over landings, `certify` over jobs and over the other runs at
    S, `bump`, `status` and `cut` over sites, `verify` over assets — each with
    its own `MORE` line and remedy, because draft 1 promised a cap on `CERTIFY
    JOB` from a verb that took no `--max` and printed one `BUMP SITE` per site
    with no ceiling at all (Fable MEDIUM 8, Opus MEDIUM 6). Every count on an
    `OK` or `FAIL` line is the truth about the walk, the run or the release,
    never about the listing, and **a cap is on what is printed and never on what
    is read**: every page of every host listing is read before anything is
    counted (Stella 2). Every host and git call is under `--timeout <seconds>`,
    default 120. No verb loops, no verb waits on a run, and **nothing is written
    outside `--lane`, `--work` and `bump`'s `--dir`** — draft 1's rule named two
    of the three and its own rule 5 wrote into the third (Opus MEDIUM 5).
    (Glenn, 2026-09-09: bounded output by design; counts not lists.)
11. **Production, then frozen.** The release of this repository that first
    ships `nova-release` with every verb above and the workflow change of rule
    6 is the release after which this tool changes additively only, on the
    same terms as `nova-bus` and `nova-merge` (Glenn, 2026-09-09). Until then
    it is dogfooded on this repository's own releases, and the friends' reads
    are owed before any other repository is asked to use it. This rule is a
    process rule and has no test; it is said here rather than given a line in
    the test list it cannot fill (Fable, LOW: draft 1's test 11 tested the
    banner, not rule 11).
12. **One file in git says what this repository ships, and the workflows read
    the same file.** `--policy <path>` names a file **in the repository**, read
    from the tree at S — and from the caller's checkout only by `bump`, which
    runs before there is an S — tab-separated, `key<TAB>value`:

    ```
    platforms   .github/platforms.tsv        one `<goos><TAB><goarch>` per line
    tools       cmd/*                        the shipped set: this token, or a file of names
    workflow    certification.yml            the certification workflow
    aggregate   certification-ok             the job that says the tier passed
    required    <job>[,<job>]                jobs that must appear in the run
    skippable   <job>[,<job>]                jobs whose `skipped` is not a failure
    sites       .github/release-sites.tsv    the version-site manifest of rule 5
    authors     .github/release-authors.tsv  `<login><TAB><line>` for rule 3
    probe       all                          the tools `verify` runs, or a list
    ```

    The third column above is prose about each key, not a third field: the file
    is `key<TAB>value` and nothing more, and a parser that read the comment would
    be reading this document rather than the file (draft 3, Opus LOW).

    **There is no `events` key any more (draft 5).** Draft 4 had one, and the
    release workflow's gate filters by no event at all (rule 4), so a run from an
    untrusted event was invisible to `certify` and decisive at the gate — the
    same hole as the ordering one, in the other direction. The key is deleted
    rather than reconciled; the run's event is still read and still recorded, as
    evidence about what happened, and never as a filter (Fable HIGH 1, Opus
    HIGH 1).

    Draft 1 typed the platform list into a flag "a third time on purpose, so a
    test can prove the three agree", and `certification.yml:492-494` already
    says of its own copy that the job "is worth nothing the moment the two lists
    disagree". Three copies and a test that compares them is a rule about
    copies; one file is no copies. Both workflows' target loops are shell and
    hold a typed list today (`release.yml:145` at `59b85fd6`,
    `certification.yml:523`), not matrices, so both can read one file with a
    `while read` — a matrix would have needed the typed copy, a loop does not, so
    the one-file form **is** possible here
    (Fable MEDIUM 7; Opus held that the typed flags satisfy Conventions'
    no-guessed-paths, and they do — but so does a flag naming the one file, and
    the one file also removes the disagreement, so this spec takes Fable's).
    Nothing here is guessed: `--policy` is a required flag, and every path
    inside the file is a path in the repository, refused when it is absolute,
    climbs with `..`, or is not a regular file in the tree at S. The policy's
    own `sha256` at S goes into the certify and cut records, so a record names
    the policy it was judged under (Stella 2, 5).

    **A workflow that reads the policy needs the policy's path, and a workflow
    has no flags (draft 4, Opus polish 3).** `--policy` has no default *in the
    tool*, because the path is a fact about one repository and the tool must not
    guess it. A workflow is that repository's own file, so it may hold that one
    path as a literal: **the repository states its policy path in its workflows,
    and this repository's is `.github/release-policy.tsv`** (work list item 11).
    The asymmetry is the rule, not an exception to it: a tool run against any
    repository is told the path; a repository's own workflow already knows it.

    **One literal per workflow, and the platform file is reached through the
    policy (draft 5).** A workflow that can find the policy reads the policy's
    `platforms` line for the path to the platform file, rather than holding that
    path as a second literal of its own. Draft 4 left it as one, which put the
    same path in three statements — each workflow's and the policy's — and the
    two the tool never reads are the two that can drift: a policy saying
    `platforms .github/targets.tsv` while the loops read another file builds one
    set and verifies another, and nothing catches it until `verify
    field=assets`, after the release exists (Fable MEDIUM 3). So **test 12
    asserts the equality**: the policy's `platforms` value is the file both
    target loops read. The same argument gives the `tools` line its proof — the
    shipped set has a counterpart to check only once the workflow can find the
    file.
13. **The candidate tree is read whole, by a line, as a term.** Both cold reads
    answered draft 1's open question the same way — yes — so #229's "whole-repo
    read of the candidate" is a term. `tree --lane <dir> --tag <tag> --who
    <name> --verdict approve|hold [--note <text>]` writes one immutable record
    keyed to S under `<lane>/releases/<tag>/`, by a line **other than** the one
    who recorded the candidate, and `cut` requires a standing approve for S. It
    is **its own record kind, not a `nova-merge` read record whose head is S**:
    a read record lives at `reads/<entry>/…`, would be folded by `nova-merge` as
    an entry read and would collide with a landing read for the same sha, which
    is exactly the inertness this tool's records are built to keep (Opus's
    shape, over Fable's "a read record with head S"; both wanted the term and
    differed only on where it lives). This is the term that closes what a
    per-landing read cannot: a merge resolution that was in no head, a squash
    whose landed tree differs from the reviewed one (rule 3, Stella 1). A
    re-freeze voids it, like every record keyed to S.

14. **A release is a two-line act, and the tool says so rather than letting an
    operator read it as a bug (draft 6, Opus MEDIUM 5).** Two terms of the cut
    condition each require a line that is not another named line: rule 13's tree
    approve is by a line **other than** the one who recorded the candidate, and
    rule 7's notes approve is by a line **other than** the notes record's
    declared author. One line therefore cannot reach `cut` — whichever way it
    distributes its own names across the records, one of the two terms names it
    twice — and **two distinct lines are enough**: A freezes the candidate and
    declares the notes, B approves the tree and the notes. What a one-hand cut
    does is **wait**: `cut` prints `CUT REFUSED: tree` or `CUT REFUSED: notes`
    with that term's line, exit 1, nothing is created, and the remedy on the line
    is the second line's verb. There is **no flag that stands in for the second
    line** and none is added here: the whole tool is the evidence having a second
    pair of eyes, and a `--solo` would be the hole every rule above closes.
    Stated because it is not otherwise anywhere: every release of this repository
    in the record, v0.15.1 last night among them, was cut by one hand — dispatch,
    wait, tag, release, notes, read back — so the first line to run this tool at
    three in the morning will meet this refusal, and it is the design and not a
    fault. A lane whose second line is offline holds the cut until it is not
    (2026-09-13).

## The verbs

```
nova-release candidate --lane <dir> --tag <tag> --sha <40 hex> --who <name> [--note <text>]
nova-release delta     --lane <dir> --tag <tag> --work <dir> --policy <path> --who <name> [--since <tag|sha>] [--max <n>]
nova-release certify   --lane <dir> --tag <tag> --work <dir> --policy <path> --who <name> [--max <n>]
nova-release bump      --policy <path> --dir <checkout> --from <tag> --tag <tag> [--max <n>]
nova-release notes     --lane <dir> --tag <tag> --notes <file> --who <name> --author <name> --verdict approve|hold [--note <text>]
nova-release tree      --lane <dir> --tag <tag> --who <name> --verdict approve|hold [--note <text>]
nova-release status    --lane <dir> --tag <tag> --work <dir> --policy <path> [--max <n>]
nova-release cut       --lane <dir> --tag <tag> --work <dir> --policy <path> --who <name> [--max <n>]
nova-release verify    --lane <dir> --tag <tag> --work <dir> --policy <path> [--max <n>]
nova-release version

every verb that runs git or gh also takes [--timeout <seconds>], default 120
```

The binary is `nova-release`, and that is its only name.

**No guessed anything, with one named exception.** `--lane` is a directory
`nova-merge init` made: the tool reads its `state.json` for `version`, `repo`,
`base` and `lane_branch`, refuses a `version` it does not know by number, and
reads no other field — the rest of that file is `nova-merge`'s. `--work` is
this tool's own directory for its clone and downloads; it is never `<lane>/repo`,
which is `nova-merge`'s clone and is under that tool's locks, and never a
checkout anybody works in (a `--work` that is a git work tree with a dirty
index is refused). There is no default tag and no default policy file:
each is a fact about one repository that only its owner can state. The
workflow, the aggregate job, the platform list, the shipped set, the sites, the
authors and the probe set are **inside the policy** (rule 12), read from the
tree at S rather than typed per run; the platform list is one file, and both
workflows reach it through the policy's `platforms` line (rule 12, draft 5). The exception is `--timeout`, 120 seconds, for the reason
SPEC.md gives.

**`status` is the survey and `cut` is the act, and they read the same
predicate.** `status` prints one line per term of the cut condition and the
verdict, **writes no record** and takes no lock, and **exits 0 whether `ready` is
true or false** — answering is its whole job, as `nova-fuse status` answers with
a fuse blown and `check` is the gate; it exits 2 only when it could not run
(Fable MEDIUM 6: draft 1 gave `status` no code at all). `cut` evaluates the same
terms in the same order and stops at the first that fails with the same line
`status` would have printed, so a `status` that says `ready=true` is a `cut`
that will not refuse on evidence — only on a race, and a race is not an answer
this tool accepts: `cut` re-reads the wire terms under the lane's checkout lock
immediately before it acts and reads the tag back after it acts (rule 6).

**`status` prints the base, because the base is a term (draft 3).** P is a term
of the cut condition — a delta record's `since` is re-derived from the wire at
`cut` and must equal the record's — and draft 2's `STATUS TERM` and `STATUS OK`
had no slot for it, so a release published between the `delta` and the `status`
moved the base and `status` still said `ready=true` for a `cut` that would then
refuse on evidence rather than on a race, against the sentence above (Opus
MEDIUM 6). So `status` prints `STATUS TERM term=since` and carries
`since=<P12|->` on `STATUS OK`: the base this run read from the wire, named
where every other term is named. **That term's `detail` is the delta record's
`since_mode`** (draft 4), `latest` or `named`, because the two modes are checked
differently and a reader who cannot see which one is in force cannot read the
verdict (rule 3, Stella 2). ("Writes no record" is the exact claim: `status`
clones into `--work`, which rule 10 allows — draft 3 said "writes nothing" beside
a verb that writes a clone; Opus polish 9.)

**`bump` is the one verb that edits files, and they are the caller's.** It
writes into `--dir`, a checkout the caller owns, and only the lines the
manifest names. It does not commit, stage, push or open anything. The bump
goes through the lane as a pull request with a read, exactly as any change
does, and it lands before the freeze, so that the frozen tree says its own
version (rule 5).

**`version` is the Conventions' line**: `nova-release <build identity>
<goos>/<goarch> <go version>`, one line, exit 0, from `internal/buildinfo`, no
flags, no arguments.

## The cut condition

There is one statement of when a release is cut, and `status`, `cut` and every
test refer to it.

**The reconciliation is asked before it, not inside it (draft 6, rule 6).** `cut`
first reads the wire: if the tag already stands at exactly S — the newest
candidate's sha — carrying a release that is neither draft nor prerelease and
whose title and body are the notes' (the outbox item's when one stands), the act
is already done. No term below is evaluated, nothing is created, the record is
written or the stranded item delivered, and the line is `CUT OK … created=already`.
Draft 5 kept that case as the last conjunct of the predicate, under terms that are
re-read on the wire now, so the re-run that is the remedy for a lost answer and
for an undelivered record could refuse on evidence that moved after the release
was live (Opus MEDIUM 4). The predicate below is therefore about **acting**, and
this sentence is about an act that already happened.

```
CUT(tag) :=
      C   = the NEWEST candidate record for tag; its sha is S
  and D   = a delta record for (tag, S) with unread=0
  and P   = D's since, with merge-base(P,S) = P, re-checked this run in D's OWN
            since_mode: in mode latest, the wire's releases/latest peeled now,
            ignoring a release for tag itself, MUST equal P; in mode named, D's
            since_ref MUST still peel to P, and a release published elsewhere
            does not move it (a first release has no latest, and is mode named)
  and R   = a certify record for (tag, S), RE-READ on the wire now: every run
            of the policy's workflow at S, from any event, listed whole (the
            host's total_count equals the number read) and no more of them than
            the gate's one page lists (total_count <= 100, release-certified.sh:25
            — draft 6); NONE of them unfinished;
            every run sharing the maximal updated_at concluding success; and in
            each of those the aggregate and every required job concluding
            success across that run's attempts. The release workflow's gate asks
            the first three of those and is this term's oracle (rule 4)
  and T   = a tree record for (tag, S): a standing approve by a line other than
            the one who recorded C
  and every site in the policy's sites file renders tag exactly count times in
      the tree at S
  and H   = the hash of the NEWEST notes record for (tag, S) by at
  and N   = a STANDING approve of the notes for (H, S) by a line other than that
            record's DECLARED author, whose summary on the lane branch carries
            a title that is not the tag and does not end with it and a body that
            is not generated
  and on the wire: no tag named tag; or the tag at exactly S with no release
      (the repair path for a workflow that half-ran). The third case — the tag at
      S with the notes' own release — is the reconciliation, and it is decided
      above this predicate rather than inside it (draft 6)
```

When it holds, `cut` takes `<lane>/checkout.lock`, re-reads the wire terms, makes
one release-create call with `target_commitish=S`, the title and the body, reads
the tag back peeled, and prints `CUT OK … assets=pending` (rule 6). When any
term fails, `cut` prints that term's `FAIL` line and `CUT REFUSED: <the term>`,
exit 1, and nothing was created. **`status` reads the reconciliation the same way
`cut` does** (draft 6): on that wire state it prints `wire=released` and
`ready=true` whatever the other terms now say, because the act is done and the
only thing a `cut` would do is record it — which is what keeps the sentence above
true, that a `status` saying `ready=true` is a `cut` that will not refuse. A host answer that is neither success nor a
refusal it can name — a 502, a rate limit, a dropped connection, a call cut at
`--timeout` — is `CUT FAIL … : <the host's first line>`, **exit 2**, and **the
remedy is to re-run the same `cut`** (draft 6), because a lost answer is read
back before it is reported absent and the re-run is what does the reading: its
wire term is asked first, so a create that did happen reconciles to `CUT OK …
created=already` with the record written, and a create that did not happen is
this same act attempted again (2026-09-13: two creates answered 502 and both
objects existed). Draft 5 named `nova-release verify` here, and draft 5 had also
keyed `verify` to the cut record — which a 502 never writes — so the remedy for
the one case it was printed for answered `refusing to guess: no cut record for
<tag>`, exit 2, at three in the morning on the exact hurt this line records (both
draft-5 reads, HIGH 1 each). `verify` stays keyed to the cut record (rule 8): it
answers what shipped, and after the re-run there is a record for it to read.
Exit 2 is what SPEC.md's table calls "could not run", and an answer
that was neither yes nor no is exactly that; draft 1 said 1 in this paragraph
and 2 in its own table (Fable MEDIUM 6, Opus HIGH 3, Stella 3).

**The predicate is keyed to S throughout.** Every record but the candidate —
delta, certify, notes, tree — carries the candidate sha it was made at; a record
for another sha is never consulted, `status` prints it as `-`, and there is no
flag that accepts an older one. The notes read is keyed to **(hash, S)**, so a
re-freeze voids it even when the bytes did not move (rule 7).

**Standing is defined here and stated nowhere else** (draft 3): a verdict is
standing when it is the newest for its **`(who, key)`** under the fold, ties
folding toward the refusing verdict, so a hold blocks and a hold its own line
later lifted does not. **The `key` is the record kind's own** (draft 4, Opus
polish 8): a landing read's key is the head it is a read of (rule 3), a notes
read's is `(hash, S)` (rule 7), a tree read's is S (rule 13). Draft 3 wrote the
notes key into the definition and left rule 3 restating the tie rule over a
different one, so one sentence claimed to cover three folds and named only one.
(Draft 2's predicate still read "with no hold for (hash, S)" beside this fold and
the two could be read against each other — Fable, not closed; draft 1's "no hold
for that hash" was the same hurt one draft earlier.)

A `delta` record's base is re-checked at `cut` time **in the mode the record
names** (rule 3, draft 4): in mode `latest`, a release published between the
delta and the cut moves the base and the walk must be redone, and the
re-derivation **ignores a release for `tag` itself**, or the reconciliation of a
lost create answer could never finish — the release this act published would have
become `latest` and moved its own baseline (Stella 3); in mode `named`, the base
is the one the caller named and only its resolution and its ancestry are
re-checked, which is what lets a first release and a maintenance line reach `cut`
at all (Stella 2).

## Exit codes

This tool's own table, in SPEC.md's three codes, with **every host-answer case
named once** — draft 1 put a 502 at 1 in its prose and at 2 in this table, gave
`status` no code, and spelled `REFUSED` both ways (Fable MEDIUM 6, Opus HIGH 3,
Stella 3).

| code | meaning |
|------|---------|
| 0 | the verb ran and passed: a record written, a walk with `unread=0`, a certification read green, a release created or reconciled, a release verified field for field — and every `status`, whatever `ready` says |
| 1 | the verb ran and said **NO**: an unread landing or an untestable author exclusion, a certification that is red, pending, missing, ambiguously listed or carrying more runs at the sha than the gate's one page lists (draft 6), a site that does not render the tag, a `bump` that refused in planning (`applied=0`) or stopped after a rename (`applied=<n>`, with a `BUMP PARTIAL` line per site already changed, rule 5), notes whose title or body the rule judges bad, notes without a read or with a hold, no tree read at S, a `cut` whose predicate does not hold, a tag that exists at another sha, a tag that reads back at another sha after the create (`CUT FAIL … field=tag`, rule 6), a `verify` with a field that disagrees, and **a record the verb wrote and could not deliver** (`file=<path> pushed=false`, any writing verb, rule 6) |
| 2 | the verb **could not run**: a missing flag, `refusing to guess` (no candidate for the tag, or no cut record for a `verify`), a `--lane` that is not a lane, a `--work` that is a dirty work tree, a policy, manifest or notes file that does not parse, a `notes` **approve** whose `--who` and `--author` name one line, `gh` or `git` absent, a call cut at `--timeout`, and **any host answer that is neither yes nor no** — a 502, a rate limit, a dropped connection — the `CUT FAIL … : <the host's first line>` of the cut condition among them, whose remedy is always **to re-run the same `cut`**, which reads the wire back and reconciles a create that did happen (draft 6) |

A host answer is placed in exactly one of those rows: **yes** decides the term;
**no** — a 404 for a tag that must exist, a 422 the host explains — is exit 1
with the field named; anything else is exit 2. `REFUSED` follows the same split
rather than carrying a code of its own: `refusing to guess: no candidate for
<tag>` is about the invocation and exits 2, `CUT REFUSED: <term>` is about the
state and exits 1.

**Three `CUT FAIL` shapes, two tokens, two codes (draft 6, Opus MEDIUM 3).**
`CUT FAIL … field=tag expected=<S12> found=<x12>` is rule 6's read-back: the verb
ran, the wire answered, the answer is no — **exit 1**. `CUT FAIL … created=<…>
url=<url> file=<path> pushed=false` is the act that could not report itself: the
release exists and the lane does not know — **exit 1**, remedy the re-run. `CUT
FAIL tag=… sha=…: <the host's first line>`, carrying **neither** token, is an
answer that was neither yes nor no — **exit 2**, remedy the same re-run. So
`field=tag` and `pushed=false` are the two tokens a scanner needs, and a line
with neither is the exit-2 one. Draft 5 said "two shapes" here and in rule 6
while its own grammar listed three, because the `pushed=false` shape was added to
every writing verb in that same draft (Opus MEDIUM 3); draft 2 stated the exit
twice and differently (Opus MEDIUM 4, Fable LOW).

## Output grammar

One line per event; first token names the verb or the check — `SITES` and
`POLICY` are checks several verbs run, not verbs, as work list item 12 already
had it (draft 3, Opus LOW) — and the second is `OK`, `FAIL`,
`REFUSED` or an informational token listed here. SPEC.md's stream rule (`OK` and
informational to stdout, `FAIL` and `REFUSED` to stderr), its one-line
guarantee through `internal/oneline` and its field law govern and are not
restated here (Fable, LOW; Opus LOW 9).

```
CANDIDATE OK tag=<tag> sha=<sha12> base=<branch> who=<name> supersedes=<sha12|-> file=<path> pushed=true
CANDIDATE FAIL tag=<tag> sha=<sha12> file=<path> pushed=false: <reason>; re-run the same verb to push it
CANDIDATE REFUSED: <reason>
DELTA ENTRY commit=<sha12> landing=<merge|squash> pr=<n> head=<sha12> author=<login> line=<name|untestable> read=<who,who|->: <subject>
DELTA MORE kind=landing shown=<n> total=<t> nova-release delta … --max 0
DELTA NOTE range=<P12>..<S12> since=<tag>: write the notes from this diff, not from the sitting
DELTA OK tag=<tag> who=<name> since=<P12> mode=<latest|named> since_ref=<tag|sha12> candidate=<S12> landings=<n> read=<n> unread=0 file=<path> pushed=true
DELTA FAIL tag=<tag> since=<P12> mode=<latest|named> candidate=<S12> landings=<n> read=<n> unread=<n>: <n> landings have no read; nova-merge read --lane <dir> --pr <n> --who <you> --head <sha> --verdict approve|hold
DELTA FAIL field=<since|pr> commit=<sha12> expected=<x> found=<y>: <reason>
DELTA FAIL tag=<tag> candidate=<S12> file=<path> pushed=false: <reason>; re-run the same verb to push it
DELTA REFUSED: <reason>
CERTIFY RUN run=<id> created=<stamp> updated=<stamp> attempt=<n> status=<word> conclusion=<word|-> deciding=<true|false>: <a run at this sha outside the deciding group|an unfinished run at this sha>
CERTIFY JOB run=<id> attempt=<n> job=<name> conclusion=<word|absent>
CERTIFY MORE kind=<job|run> shown=<n> total=<t> nova-release certify … --max 0
CERTIFY OK tag=<tag> sha=<S12> who=<name> workflow=<file> run=<id> attempt=<n> updated=<stamp> group=<n> event=<word> jobs=<n> aggregate=<job> file=<path> pushed=true
CERTIFY FAIL tag=<tag> sha=<S12> workflow=<file> run=<id|-> attempt=<n|-> job=<name|-> status=<word|-> conclusion=<word|absent|-> runs=<n> jobs=<n>: <reason>
CERTIFY FAIL tag=<tag> sha=<S12> file=<path> pushed=false: <reason>; re-run the same verb to push it
CERTIFY REFUSED: <reason>
BUMP SITE <path>: <count> renderings of <from> became <tag>
BUMP MORE kind=site shown=<n> total=<t> nova-release bump … --max 0
BUMP OK sites=<n> done=<n> applied=<n> from=<tag> tag=<tag> dir=<checkout>
BUMP PARTIAL <path> before=<sha256:12> after=<sha256:12>
BUMP FAIL <path> sites=<n> done=<n> applied=<n>: <reason>; re-run the same bump to finish it
BUMP REFUSED: <reason>
SITES OK sites=<n> tag=<tag> at=<S12>
SITES MORE kind=site shown=<n> total=<t> nova-release status … --max 0
SITES SITE <path>: <reason>
SITES FAIL sites=<n> ok=<n> bad=<n> tag=<tag> at=<S12>: <the first failing path>
NOTES OK tag=<tag> sha=<S12> who=<name> author=<name> verdict=<approve|hold> hash=<sha256:12> title=<title> file=<path> pushed=true: does every sentence say what the reader of this release gets?
NOTES FAIL <path> hash=<sha256:12|->: <reason>
NOTES FAIL tag=<tag> sha=<S12> <path> hash=<sha256:12> file=<path> pushed=false: <reason>; re-run the same verb to push it
NOTES REFUSED: <reason>
TREE OK tag=<tag> sha=<S12> who=<name> verdict=<approve|hold> file=<path> pushed=true
TREE FAIL <sha12>: <reason>
TREE FAIL <sha12> file=<path> pushed=false: <reason>; re-run the same verb to push it
TREE REFUSED: <reason>
POLICY OK file=<path> at=<S12> hash=<sha256:12> platforms=<n> tools=<n> probe=<n>
POLICY FAIL <path>[:<line>]: <reason>
STATUS TERM term=<candidate|since|delta|certify|tree|sites|notes|wire> ok=<true|false> detail=<one token or ->
STATUS OK tag=<tag> candidate=<S12|-> since=<P12|-> delta=<unread|-> certify=<run|pending|-> tree=<approve|hold|-> sites=<n|-> notes=<approve|hold|-> wire=<absent|tag-only|released> ready=<true|false>
CUT OK tag=<tag> sha=<S12> who=<name> created=<tag+release|release|already> assets=pending title=<title> url=<url> file=<path> pushed=true
CUT FAIL tag=<tag> sha=<S12> created=<tag+release|release> url=<url> file=<path> pushed=false: <reason>; re-run the same verb to push it
CUT REFUSED: <the failing term's line>
CUT FAIL tag=<tag> sha=<S12> field=tag expected=<S12> found=<x12>: the release at <url> stands on a tag that points elsewhere; removing it is a hand's act
CUT FAIL tag=<tag> sha=<S12>: <the host's first line>; re-run the same cut before reporting anything
VERIFY FIELD field=<tag|release|title|body|assets|asset|checksum|version> platform=<goos/goarch|-> expected=<x> found=<y> ok=<true|false>
VERIFY MORE kind=<asset|probe> shown=<n> total=<t> nova-release verify … --max 0
VERIFY OK tag=<tag> sha=<S12> assets=<n> probed=<n> version=<field two> run=<id|->
VERIFY FAIL field=<name> expected=<x> found=<y> fields=<n> ok=<n> assets=<n> probed=<n>: <reason>
VERIFY REFUSED: <reason>
<VERB> REFUSED: <reason>
```

**`STATUS TERM`'s `detail` is one token and says which question was asked
(draft 4)**: `term=since` carries the delta record's `since_mode`, `latest` or
`named` (rule 3), and `term=certify` carries `green`, `red`, `pending` or `-`,
because an unfinished run at S, of any age, is neither of the first two
(rule 4, draft 5).

**Every `FAIL` line carries its verb's counts (draft 4, Opus polish 4)**, as
SPEC.md requires the count line on failure as well as success: `CERTIFY FAIL`
carries `runs=` and `jobs=`, `VERIFY FAIL` carries `fields=`, `ok=`, `assets=`
and `probed=`, `BUMP FAIL` carries `sites=`, `done=` and `applied=`, and the
per-site problem `status` and `cut` print is the informational `SITES SITE` line with one
`SITES FAIL` count line closing it — draft 3 printed a bare `SITES FAIL <path>`
per site and no count at all.

`DELTA ENTRY` is capped at `--max` and `DELTA OK`/`FAIL` counts the walk, not
the lines. `DELTA NOTE` prints the range and the base's tag on every completed
walk, pass or fail, because the notes are owed either way and the diff is where
they come from. `VERIFY FIELD` lines print for every field even after the first
`false`, so a reader learns everything that is wrong in one run; `VERIFY FAIL`
names the **first** one. `CERTIFY JOB` prints only jobs whose conclusion is not
`success`, and `CERTIFY RUN` prints **every run at S outside the deciding group,
finished or not** (draft 5, `deciding=false` on each), both capped at `--max`; a
green run alone at S prints neither, and `jobs=<n>` says how many were read.
`CERTIFY RUN` carries **both** stamps, and `updated=` is the one the term is
decided by (draft 5, Fable LOW: draft 4 printed `created=` alone, which is the
key the gate does not use). **`CERTIFY OK`'s `event=` is the run `run=` names**
and no other (draft 6, Fable LOW): the deciding group can hold runs from two
events, and one word cannot describe a group.

**Every writing verb has a `pushed=false` line, and it is SPEC-MERGE's (draft
5).** `candidate` alone had it in draft 4, so a `delta`, a `certify`, a `notes`,
a `tree` or a `cut` whose record reached the outbox and not the remote tip
printed an `OK` line naming no failure (Opus HIGH 2). Each now prints `file=
pushed=false: <reason>; re-run the same verb to push it` at exit 1, as
SPEC-MERGE.md:559 and :562 do, and for `cut` the same line also carries what the
wire already holds — `created=` and `url=` — because that act succeeded and only
its record did not. **Each of those lines carries `tag=` and `sha=`** (draft 6,
Opus LOW): `NOTES FAIL … pushed=false` carried neither in draft 5, so a scan of a
lane running several tags could see that a notes record was stranded and not
which tag's.

**`title=` is a field and is printed as one (draft 3).** A title has spaces, so
SPEC.md's field law prints it with `\x20` for each of them and `\x3d` for an
`=`, exactly as `nova-memory`'s `name=` is printed: one token, scannable from the
line start, and unreadable as prose — which is why the title a person reads is
the one on the release, and `NOTES OK`'s free-text tail is the reader's question
and never the title (Fable LOW, which offered moving it to the tail; the field is
kept because `status` and `cut` are scanned, and the law is named instead).

## The records

Everything this tool records is one immutable file under
`<lane>/releases/<tag>/`, tracked and pushed to the lane branch through the
compare-and-swap loop of SPEC-MERGE rule 22, under `<lane>/checkout.lock`, out
of this tool's own outbox. The names and the shapes:

```
<lane>/releases/<tag>/candidate-<sha12>-<at>-<rand6>.json   {file, tag, sha, base, who, at, note, supersedes}
<lane>/releases/<tag>/delta-<sha12>-<at>-<rand6>.json       {file, tag, sha, who, since, since_mode, since_ref, policy_hash, landings, read, unread, entries:[{commit, landing, pr, head, author, line, read:[who]}], at}
<lane>/releases/<tag>/certify-<sha12>-<at>-<rand6>.json     {file, tag, sha, who, workflow, run, attempt, updated, group:[<run id>], event, aggregate, jobs, job_names:[{name, attempt}], policy_hash, at}
<lane>/releases/<tag>/notes-<hash12>-<at>-<rand6>.json      {file, tag, sha, hash, who, author, verdict, note, title, at}
<lane>/releases/<tag>/notes-<hash12>-<at>-<rand6>.summary   the normalised notes, one immutable copy per read (rule 7), tracked beside the record
<lane>/releases/<tag>/tree-<sha12>-<at>-<rand6>.json        {file, tag, sha, who, verdict, note, at}
<lane>/releases/<tag>/cut-<sha12>-<at>-<rand6>.json         {file, tag, sha, who, url, title, created, notes_hash, sites_hash, policy_hash, at}
<lane>/outbox-release/<at>-<rand6>.json                     a record not yet confirmed at the remote tip, untracked
<lane>/outbox-release/<at>-<rand6>.summary                  a notes record's normalised bytes, carried to SummaryFile of its destination by the same loop (records.go:245-263), untracked
<lane>/outbox-release/.gitignore                            `*`, written on first use, so a lane mid-verb still answers a clean porcelain (**why a new binary**)
```

**Every verb that reads these records fetches the lane branch first (draft 6,
Fable LOW).** `status`, `cut` and `verify` read `<lane>/releases/<tag>/` and
`delta` reads `<lane>/reads/`, and a record another line pushed a minute ago is
not in a stale checkout: each of them **fetches the lane branch ref this run** and
folds the records at that ref. The fetch updates a ref and no working tree, so it
takes **no checkout lock** and disturbs no round another line is in — which is
what keeps it consistent with rule 3's read-only fold, the one that exists so a
reader is never refused by a writer's lock. Draft 5 said this of nothing but the
clone in `--work`, so the freshness of the lane's own records was unstated while
four terms of the predicate stand on them (draft 6, Fable LOW). A fetch that
fails is the verb's own refusal, named, and never a fold of whatever was on disk.

**`file` is a field of every shape**, the record's path under the lane, because
the compare-and-swap loop reads it to know where the bytes belong
(`internal/merge/records.go:267`, `destinationOf`). Draft 1's five shapes
omitted it, which would have made every record undeliverable by the very loop it
was written for (Fable MEDIUM 4, Opus HIGH 1). `delta` carries `who` because
every record names the line that wrote it; draft 1's delta shape named nobody
(Fable MEDIUM 4), and draft 3 additionally read that field as a notes author,
which rule 7 no longer does (draft 4, Stella 4). `delta` also carries
`since_mode` and `since_ref`, because `cut` must re-check the base in the mode
the walk used (rule 3, draft 4), and **no `since_tag`** — draft 4's shape carried
one this document defined nowhere, and the tag a reader wants is `since_ref`
(draft 5, Opus LOW). The certify record carries `updated`, the maximal
`updated_at` the term was decided by, and `group`, the ids of every run that
shared it, because the deciding evidence is a group and not a run (rule 4, draft
5); its `event` is **the `run` it names**, not the group's, which can hold two
(draft 6, Fable LOW). The certify record's `job_names` carry the
**attempt** each conclusion was read from, so a job proven in an earlier attempt
of that run and a job absent from every attempt are different entries (rule 4,
draft 4, Stella 3). The certify
and cut records carry the hashes of what they were judged under, so a record
cannot be read as a claim about a manifest somebody edited afterwards (Opus
MEDIUM 8, Stella 2).

**`who` is a field of every shape and a required flag of every verb that writes
one (draft 3).** Draft 2 gave the candidate, delta and certify records a `who`
and gave `candidate`, `delta` and `certify` no flag to fill it, while two terms
of the predicate stood on it: rule 13's approve "by a line **other than** the one
who recorded the candidate", and rule 7's authors of record. Rule 7's half is
gone in draft 4 — the notes' author is a required declaration and is never
inferred from a recorder (Stella 4) — and rule 13's stands. The
tool may not infer `who` — **No guessed anything**, and it reads no lane field but
four, a host login being an account rather than a line (rule 3). So `--who
<name>` is required on `candidate`, `delta`, `certify`, `notes`, `tree` and
`cut`, `who=<name>` prints on each one's `OK` line, and the cut record carries it
too, so the act names its actor as every other record does (Opus HIGH 1 and
LOW). **`--author <name>` is required on `notes` beside `--who`** (draft 4,
Stella 4), and the two may not name one line.

The summary beside a notes record is the mechanism rule 22 already has: the
compare-and-swap loop carries `<id>.summary` from the outbox to `SummaryFile` of
the record's destination (records.go:245-263), so the notes reach the branch
**without a second path, a second write or a non-JSON item in the outbox** (rule
7, draft 3).

`nova-merge` folds `reads/` and `gates/` and walks nothing else, so a lane with
a `releases/` directory is, to `nova-merge`'s fold, the lane it was. But
SPEC-MERGE says more than the fold does: its lane table (SPEC-MERGE.md:663-680)
and rule 22's "Nothing else in the lane is tracked" (:416-418) are made wrong
the day the first candidate is recorded, so **one additive line in SPEC-MERGE —
`releases/` tracked and not folded, `outbox-release/` untracked and
self-ignoring — is owed in the pull request that lands work list item 2**, the
`internal/merge` change it describes, and not deferred past it (draft 3: draft 2
said "the same pull request as this spec", and this pull request touches one
file and leaves `docs/SPEC-MERGE.md` unchanged — Fable, not closed; the line is
owed with the code that makes it true, which is the first pull request in which
it is not premature).
The other line `nova-merge` might one day gain from these files — a
`candidate=<sha12>` on `STATUS OK` — is an additive change to that spec and is
**not made by this one**; it is named in the work list so it is not lost. A
record file that does not decode is refused by name, never skipped, exactly as a
lane record is. No record is ever edited or replaced: a re-freeze is a second
candidate file, a second walk is a second delta file, and the newest by `at`
wins, ties folding toward the refusing verdict (a `hold` after an `approve` with
one `at` holds).

## What it deliberately does not do

- **It does not merge.** It has no verb that lands anything and no path to
  `gh pr merge`; a landing after the freeze is `nova-merge`'s act and this
  tool's re-freeze. So there is no `--auto` here to refuse: the failure is
  structurally unreachable.
- **It does not write the notes.** A line does, from the diff `delta` names,
  and another line reads them. `--generate-notes` is not a fallback; it is the
  refusal of rule 6.
- **It does not edit the frozen tree.** `bump` edits a caller's checkout
  before the freeze; nothing edits anything after `candidate`.
- **It does not run the certification.** It reads a run that exists at the
  sha and says dispatch when none does. A tool that dispatched would be a
  tool deciding when the runners are spent.
- **It does not build or attach binaries.** The release workflow does, on the
  tag, into the release `cut` made; `verify` reads what it attached.
- **It does not wait.** No verb polls a run, a release or a push; a verb is
  one read or one act, and the next step is a person's.
- **It does not notify anybody.** Announcing a release is `nova-bus`'s job;
  the `CUT OK` line carries the URL a note would carry.
- **It does not read the tree for you.** #229's "whole-repo read of the
  candidate" is a term of the cut condition as of draft 2 (rule 13), and what
  the tool does is refuse without it: the reading is a line's, recorded by a
  verb, as every verdict in this set is.
- **It does not decide the version number.** `--tag` is typed by the person
  who owns the line, every time.
- **It does not let one line cut alone.** Two terms each require a second line
  (rule 14), so the minimum is two; there is no flag that stands in for the
  missing one, and a lane with one line waits.

## Tests this spec demands

One line per rule. Each is a test the work list builds, and each must be seen
red before it is trusted. No test carries a house literal: counts come from the
fixture's own policy, and asset names from its own tools and platforms (Opus
MEDIUM 4, Stella 5).

1. `candidate` on a sha the fake host does not hold under the base refuses; on a
   good sha it writes one file whose name carries `sha12` and whose body carries
   `file`, pushes it to the lane branch, and a second `candidate` at another sha
   prints `supersedes=` and leaves the first file untouched; `status` then names
   the newer. **And the stranded-record test**: an item left in
   `<lane>/outbox-release/` by a killed `candidate` is delivered by the next
   `nova-release` verb, and with that item present a `nova-merge read` on the
   same lane prints `pushed=true` and **exits 0** — the class Opus HIGH 1 and
   Fable MEDIUM 4 named cannot recur, whatever the release record's shape.
   **And the re-run after a kill between push and confirm** (draft 3): with an
   item under `<lane>/outbox-release/`, a `nova-merge read` on that lane finds
   `git status --porcelain` empty, **takes the `clean` branch and confirms** —
   the branch the stray item used to force away from, so the commit that used to
   fail with nothing staged is never attempted (records.go:317-330) — and prints
   `pushed=true`, because the tool wrote `<lane>/outbox-release/.gitignore`
   holding `*` on first use (Fable HIGH 2; draft 4 names the confirm, Opus
   polish 2: draft 3's line said "commits, pushes", which is the other branch);
   and `candidate` without `--who` refuses at exit 2, while
   `CANDIDATE OK` carries `who=` (Opus HIGH 1).
2. A `delta`, a `certify`, a `notes` approve and a `tree` approve recorded for
   S, then a re-freeze at S′: `status` prints `delta=- certify=- tree=- notes=-`
   **with the notes file's bytes unchanged**, `cut` refuses naming them, and the
   old records are still in the branch.
3. A fixture history with four landings — a two-parent merge, a squash the fake
   host associates with one pull request, a commit the host associates with
   **two**, and one it associates with none — and reads for the first two:
   `delta` prints `unread=1` for the merge-or-squash that lacks a read and
   `DELTA FAIL field=pr` for each ambiguous or unassociated landing, exit 1,
   writes no record; the author login comes from the host for **both** the merge
   and the squash; a read by the line the authors file maps that login to does
   not count; a login the authors file does not name prints `line=untestable`
   and the landing counts as unread; a squash onto a branch that is not the base
   is not a landing; the base P is taken from the host's `releases/latest`, not
   from a local tag planted at another sha, and a P that is not an ancestor of S
   is `DELTA FAIL field=since`; with no release on the wire and no `--since`,
   `delta` refuses; **a landing with an approve by another line and a standing
   hold by a third counts as unread** (draft 3), while a hold that line itself
   later lifted does not; and the fold `delta` reads takes no checkout lock, so a
   `delta` run while another line holds `<lane>/checkout.lock` still answers
   (draft 3). **And the two baseline modes reach `cut`** (draft 4, Stella 2): a
   **first release** — no release on the wire, `--since` naming the root — walks,
   records `since_mode=named`, and `cut` accepts it; an **explicit non-latest
   ancestor** is not overruled at `cut` by the host's `releases/latest`; in mode
   `latest` a release published between the `delta` and the `cut` makes `cut`
   refuse naming `since`, while the same publication in mode `named` does not;
   and the reconciliation of a lost create answer still finishes in mode `latest`
   because the re-derivation ignores a release for `tag` itself.
4. A run at the candidate whose aggregate is `success` but one matrix leg is
   `failure` is `CERTIFY FAIL` naming the leg; **a `cancelled` run at S whose
   `updated_at` is older than a green one's is not in the deciding group, is
   green, and prints a `CERTIFY RUN` line with `deciding=false`**; a green record
   whose run is re-run red before `cut` makes `cut` refuse, because the term is
   re-read on the wire; a required job absent from the run is
   `conclusion=absent`; a `skipped` job the policy does not name is red and one
   it names is not; a second page of jobs is read before anything is counted; a
   run of another workflow is not evidence, **while a run of the policy's
   workflow from any event is** (draft 5: there is no events key); a run at
   another sha is `no completed run at <sha12>`; a green run writes the record
   with its id, attempt, `updated`, the deciding group's ids and its job names.
   **And the re-run fixture the gate decides** (draft 5, Fable HIGH 1, Opus HIGH
   1): run A at S created 10:00 green, run B at S created 11:00 green, then A
   re-run red at 12:00 as attempt 2 — a re-run does not move `created_at`, so
   A's `updated_at` is now the maximum — and `certify` and `cut` are `CERTIFY
   FAIL run=<A> conclusion=failure`, exit 1, the same answer
   `.github/scripts/release-certified.sh` gives the same fixture, which is the
   test's oracle; **two runs sharing the maximal `updated_at` with opposite
   conclusions refuse**, and no run id breaks that tie in either direction;
   **an older unfinished run beside a completed green one is pending**, not
   green, `status` prints `certify=pending`, and so is a newer one; an unfinished
   run with a required job already `failure` is `CERTIFY FAIL` naming that job,
   not pending; a host whose `total_count` exceeds the runs it listed is
   `CERTIFY FAIL` naming the ambiguity, as the gate's `:36-41` does — that is the
   race guard, and it is writable only against a host that answers
   inconsistently; **and a sha with 101 runs, every one of them green and every
   page read, is `CERTIFY FAIL` naming the gate's one page of 100, at exit 1,
   with the re-freeze as its remedy** (draft 6, Opus HIGH 2, Fable MEDIUM 2:
   draft 5's term compared `total_count` against the runs read after full
   pagination, which is equal by construction, so this fixture passed a state the
   gate refuses); **a run
   whose required job is absent is red for `certify` and green for the script**,
   and the test asserts that direction — the tool refuses what the gate would
   pass and never the reverse. **And the attempts fixture** (draft 4, Stella 3):
   a **re-run of the same run id** makes a second attempt, and a required job
   absent from that attempt but `success` in the first attempt of that run
   counts, while a required job absent from **every** attempt of it is
   `conclusion=absent`; a job carried by **two** attempts is read from the newer
   one, so a job green in attempt 1 and red in attempt 2 is red (draft 6, Fable
   LOW); the record names the attempt each conclusion came from;
   and a run outside the deciding group that is red is history on a `CERTIFY RUN`
   line and is not by itself a refusal.
5. `bump` takes `--policy` and no `--sites`, and reads the policy and the
   manifest it names from `--dir` (draft 3). A manifest with a `{tag}` twice, or
   none, refuses at load; `bump` on a site
   holding two renderings of `--from` where `count` is 1 refuses naming the path
   **and writes nothing at all, including the sites it had already planned**; a
   site path that is a symlink, or climbs with `..`, refuses; a good bump
   rewrites exactly the manifest's lines and nothing else (a byte diff of the
   checkout); `status` at a candidate whose site still reads the old tag prints
   `SITES SITE <path>` and one `SITES FAIL` count line (draft 4); the sites file
   is read from the tree at S, so a local edit to it changes no verdict.
   **And the failure after the first rename** (draft 4, Stella 1; the exit and
   the token draft 5, Fable MEDIUM 2): a fixture in which site four's write fails
   leaves sites one to three **changed**, prints a `BUMP PARTIAL` line for each
   with its before and after `sha256`, prints `BUMP FAIL … applied=3` with the
   re-run remedy on the line, exits **1**, and never touches site five; a failure
   at site **one** prints no `BUMP PARTIAL` line, carries `applied=0`, and exits
   1 by the same rule; a planning refusal carries `applied=0` too, so `applied=`
   and not `done=` is what tells a scanner a rename happened; a process killed
   between site three and site four is resumed by re-running the same `bump`,
   which counts the three `done`, applies the rest, and leaves a checkout byte
   for byte equal to the one an uninterrupted run makes; and a site a caller
   edited between the plan and the apply is a **refusal naming the path** rather
   than an overwrite, at exit 1, with a `BUMP PARTIAL` line for each site already
   renamed (draft 5, Opus MEDIUM 4).
6. `cut` with `--draft` or `--prerelease` refuses before any host call; with a
   tag existing at another sha refuses naming both shas; with the tag at S and
   no release creates the release alone and prints `created=release`; **a fake
   host that answers 201 while the tag peels to another sha is `CUT FAIL
   field=tag`, exit 1, with no record written**; a create whose answer is lost
   and whose release exists, neither draft nor prerelease, with the notes' title
   and body is `CUT OK … created=already` on the re-run, **while the same release
   marked draft is not** (draft 3); a 502 from the create is `CUT FAIL` at
   **exit 2** whose line names **the re-run of the same `cut`**, and that re-run
   against a host that did create the objects prints `CUT OK … created=already`
   and writes the cut record, which `verify` then reads — the remedy runs (draft
   6, both HIGH 1: a fixture asserting `nova-release verify` straight after the
   502 must see `refusing to guess: no cut record`, which is why the line changed);
   **and the reconciliation is decided before the evidence**: with the release
   live at S and a certification run at S re-run **red** afterwards, a re-run of
   `cut` prints `CUT OK … created=already`, delivers the record and does **not**
   print `CUT REFUSED: certify` (draft 6, Opus MEDIUM 4), while a `cut` at a tag
   with **no** release and that same red certification refuses on `certify` as it
   always did; the read-back mismatch above is `CUT FAIL
   field=tag`, **exit 1** — one token apart and one code apart (draft 3); the
   fake host records exactly one create call carrying
   `target_commitish=S`, the title and the body; a source test finds no call
   site that runs `git tag` or `git push … refs/tags`; and **the cut record is
   delivered after `<lane>/checkout.lock` is released**, so a `cut` that
   succeeded on the wire does not then wait `--timeout` on its own lock (draft 4,
   Fable's polish on the draft-3 APPROVE). **And the act that could not report
   itself** (draft 5, Opus HIGH 2): a fixture whose delivery never lands leaves a
   `cut` that created the release printing `CUT FAIL … created=tag+release
   url=<url> file=<path> pushed=false`, exit 1, with the item still in
   `<lane>/outbox-release/`; re-running the same `cut` delivers that item and
   prints `CUT OK … created=already pushed=true`; and the same shape is asserted
   for `delta`, `certify`, `notes` and `tree`, each at exit 1 with its own
   record's path.
7. Notes titled with the tag alone, or ending with the tag, refuse **at
   `notes`, before a read is recorded, and at `cut`** (draft 3); a file whose
   first line is not `# <title>` refuses at exit 2 before anything is hashed
   (draft 3); a body containing `## What's Changed` refuses; notes with no read
   refuse; a `hold` for (hash, S) blocks while an older `approve` exists;
   **`notes` without `--author` refuses at exit 2, an **approve** whose
   `--who` equals its `--author` is `NOTES REFUSED` at exit 2 before a record is
   written while a **hold** with the two equal is recorded (draft 5, Fable LOW:
   an author withdrawing their own notes is not a self-review), and a line that
   recorded the candidate or the delta is otherwise an ordinary reader — nothing
   is inferred from a recorder** (draft 4, Stella 4: A records the candidate and
   the delta, B writes the notes and approves them, and B is refused as its own
   declared author rather than passing as an approve of A's); two lines sharing
   one forge account are two `who` names and two `--author` names, and rule 3's
   authors map does not enter this term; **with two drafts standing at S the hash
   is the newest notes record's by `at`**, an older draft's approve neither ships
   nor blocks, and a hold on the newest hash blocks (draft 4, Opus polish 1);
   **a `notes` approve writes the normalised bytes as
   the record's `.summary` on the lane branch, the outbox holds JSON and a
   `.summary` and nothing else, no lane path is written twice, and `status`,
   `cut` and `verify` take the title and body from that summary with no `--notes`
   flag of their own** (draft 3, Fable HIGH 1 and Opus HIGH 2); a second draft is
   a second record at a second hash and the first record is untouched; the same
   notes in CRLF and in LF hash equal, a file ending in blank lines hashes equal
   to one that does not (draft 3), and a release body the host stored with a
   trailing newline verifies equal under rule 7's normalisation.
8. `verify` against a fake release missing one asset of the set the fixture's
   own policy derives names that asset; with a tag peeled to another sha fails
   `field=tag`; with a body that differs fails `field=body`; with a probe binary
   printing `devel` fails `field=version`; with a wrong `SHA256SUMS` line fails
   `field=checksum`; `probe all` runs every tool of the expected set for this
   host and names the one that is wrong; every `VERIFY FIELD` line prints even
   after the first false; `VERIFY FAIL` carries the same counts `VERIFY OK` does
   (draft 4); a release whose assets are absent names the workflow run's status,
   **including a run that stopped at its `certified` job, or at that
   job's second ask inside the `release` job, because a certification run at the
   tag was unfinished or the newest-stamped group was not uniformly green**
   (draft 5, nova-tools #252); and `verify` with no cut record for the tag
   refuses at exit 2, while a re-freeze after a `cut` does not change what
   `verify` prints, because it reads the cut record (draft 5, rule 8).
9. Every sha the tool acts on is traced by a test to a host call or to a fetch
   with `+refs/tags/*:refs/tags/*` in the same run; a local tag planted in
   `--work` at a wrong sha does not change any verb's answer.
10. A fixture delta of 50 landings prints 20 `DELTA ENTRY` lines, one `DELTA
    MORE`, and `landings=50`; the same for `certify`'s jobs, `bump`'s and
    `status`'s sites and `verify`'s assets, each with its own `MORE` line; a
    paginated host listing is read whole while the printing stays capped; a host
    that never answers is cut at `--timeout` with exit 2 naming the call;
    nothing is written outside `--lane`, `--work` and `bump`'s `--dir` (watched
    temp roots stay empty).
11. The usage banner ends in a runnable `example:` block, and
    `TestExecutableFirstRun` runs it, as every binary's does. (Rule 11 is a
    process rule and has no test; this line is rule 10's banner half.)
12. A policy whose path is absolute, climbs with `..`, or names a file absent
    from the tree at S refuses naming the key and the path; the policy is read
    at S, so an edit to the caller's checkout changes no verdict; and **a test
    in this repository's CI** proves that `release.yml` and `certification.yml`
    hold no typed target list of their own and read the platform file — that
    test is about this repository's workflows, so it lives here and not inside
    the tool (Opus MEDIUM 4). **The same test reads
    `.github/release-policy.tsv`** and asserts two equalities (draft 5, Fable
    MEDIUM 3): the policy's `platforms` value **is** the path both target loops
    read, so a policy naming another file cannot build one set and verify
    another; and `release.yml`'s build loop and `assert-version-stamp.sh` take
    the shipped set from the policy's `tools` line and hold no list or exclusion
    of their own. The policy's path is the one literal each workflow keeps, and
    `--policy` has no default in the tool by design (rule 12).
13. `cut` refuses with `tree=-` when no `tree` record for S exists; an approve by
    the line that recorded the candidate does not count; a `hold` blocks; the
    record lands under `releases/<tag>/` and a `nova-merge` fold of the same
    lane counts exactly the reads and gates it counted before.
14. **One line cannot cut** (draft 6, rule 14): a fixture in which a single `who`
    records the candidate, walks the delta, certifies and writes the notes as
    their own declared author gets `NOTES REFUSED` at exit 2 for its own approve
    (rule 7) and a `tree` approve that does not count (rule 13), so `cut` is `CUT
    REFUSED: tree`, exit 1, nothing created; no flag in the binary's usage banner
    accepts either term without a second line; and the same fixture with a second
    `who` approving the tree and the notes cuts.

## The work list

To build it in Go under `cmd/nova-release`, the way `cmd/nova-merge` is built:
standard library only, `internal/oneline` for every printed value,
`internal/bounded` for every listing, `internal/buildinfo` for `version`, and
`internal/merge`'s record and lock code **reused, not copied**, for the outbox,
the compare-and-swap push and the checkout lock.

1. **`internal/release/records.go`** — the six record shapes, each carrying
   `file`, strict decode, newest-by-`at` with refusing-verdict-last, written
   through `internal/merge`'s compare-and-swap loop under the lane's checkout
   lock, **out of `<lane>/outbox-release/`**. Tests 1, 2, 7, 13.
2. **`internal/merge` (additive, inert)** — the outbox directory becomes a field
   on `Records`, **empty meaning `outbox`**, so `nova-merge` with the default is
   unchanged, this tool passes `outbox-release`, and a zero-value `Records{}`
   cannot write into `<lane>/` itself (`OutboxDir` is a package const today,
   `internal/merge/state.go:41`, and a Go string field's zero value is `""` —
   draft 3, Opus LOW); the commit subject's `nova-merge: ` prefix
   (records.go:330) becomes a field of the same kind, so a release record is not
   logged under the other tool's name (draft 3, Opus LOW); **the tool name in
   the lock waiter's refusal becomes a field of the same kind**
   (`internal/merge/lock.go:90-92` is fixed text naming `nova-merge`, so a line
   waiting on a lane a `cut` holds is sent looking for a `nova-merge` that is not
   there — draft 5, Opus MEDIUM 6); and the outbox scan
   skips `.gitignore` as it skips `.tmp` and `.summary` (records.go:220-265), so
   the ignore file this tool writes into its own outbox is never read as a record
   (draft 3, Fable HIGH 2). One line in SPEC-MERGE's lane table for `releases/`
   and `outbox-release/` lands in **this item's** pull request (**the records**).
   Test 1.
3. **`internal/release/policy.go`** — the policy file, read from the tree at S,
   every path validated, the `sha256` the records carry; the sites and authors
   manifests it names. Tests 3, 5, 12.
4. **`internal/release/wire.go`** — the host reads: peel a tag, read a release by
   tag, read `releases/latest`, list assets, list workflow runs by `head_sha`
   with every page, **with no status filter and no event filter, so an unfinished
   run at S is seen and a run from any event counts, and with the host's
   `total_count` compared against the number read **and against the gate's one
   page of 100** (draft 6, rule 4: the first is the race guard, the second is the
   state the gate cannot see whole), read a
   run's attempts and jobs with every page, associate a
   commit with its pull request, read a pull request's author and head; every
   answer's status read before its body (release.yml's `ask` pattern); the one
   host write, release-create with `target_commitish`, and the peeled read-back
   that follows it. Tests 4, 6, 8, 9.
5. **`internal/release/delta.go`** — the clone in `--work`, the forcing tag
   fetch, the ancestry check, the first-parent walk, the head and author
   resolution for both landing shapes, the authors map and the read lookup in
   the lane's `reads/` fold, and **the two baseline modes, recorded and
   re-checked** (draft 4, rule 3). Test 3.
6. **`internal/release/sites.go`** — the manifest the policy names, `bump` as a
   plan-then-apply reading the policy and the manifest from `--dir` (draft 3),
   the path checks, the check at a tree, and **the partial-application report and
   resume of rule 5** — before and after `sha256` per site, `BUMP PARTIAL` lines,
   `applied=<n>` at exit 1 with the re-run remedy (draft 5), and a re-run that
   counts a finished site `done`. Test 5.
7. **`internal/release/notes.go`** — the normalisation through trailing blank
   lines, the first-line refusal, the title and body checks `notes` and `cut`
   both make, the hash, the **required declared author and the refusal of a
   self-read** (draft 4), and the notes read record keyed to (hash, S) **carrying
   the normalised notes as its `.summary`**; no `notes.md`, and nothing but JSON
   in the outbox (rule 7, draft 3). Test 7.
8. **`internal/release/cut.go`** — the predicate as one function `status` and
   `cut` both call, with `cut` acting on `ready=true` only, under the checkout
   lock, re-reading the wire terms and reading the tag back, **and releasing the
   lock before the cut record is delivered**, because `Deliver` takes that lock
   itself (draft 4, `internal/merge/records.go:152`), **and the `pushed=false`
   line and exit 1 every writing verb here gives an undelivered record** (draft
   5, rule 6), **and the reconciliation asked before the predicate**, so a re-run
   over an act already on the wire delivers and reports rather than re-deciding
   (draft 6, rule 6, Opus MEDIUM 4). Tests 6, 13, 14.
9. **`internal/release/verify.go`** — the field walk, the expected set from the
   policy, the downloads into `--work`, the checksum check, running `version`.
   Test 8.
10. **`cmd/nova-release/main.go`** — the verbs, refusals naming every
    independent problem at once, the banner with its `example:` block, the
    `### First run` in `docs/CLI.md` and a row in `docs/USAGE.md`. Tests 10, 11.
11. **`.github/platforms.tsv`, `.github/release-policy.tsv`,
    `.github/workflows/release.yml`, `.github/workflows/certification.yml`,
    `.github/scripts/release-certified.sh`, `.github/scripts/assert-version-stamp.sh`**
    — the one platform file; **each workflow holding the policy's path as its one
    literal and taking the platform file's path from the policy's `platforms`
    line** (draft 5, Fable MEDIUM 3), both target loops reading it with `while
    read` in place of the typed list at release.yml:145 and
    certification.yml:523; **release.yml's build loop reading the policy's
    `tools` line for the shipped set** instead of the in-workflow exclusion at
    release.yml:132-134 (draft 3, Opus MEDIUM 5), and `assert-version-stamp.sh`,
    which walks `cmd/*/` on its own at release.yml:190, reading the same line, or
    a `tools` value that excludes a directory leaves it asserting an artifact the
    build loop never built; and release.yml's create path (release.yml:283-287)
    becoming a refusal; **the upload path unchanged** (release.yml:272-276),
    which is the condition under which partial-upload safety is left to the tag
    checks at release.yml:241-245 and :259-265 (draft 3, Fable's ruling); the
    comment that explains the v0.11.0 accident (release.yml:267-271) rewritten to
    say the order is now designed. **Two more house literals go with the same
    edit (draft 6)**: the build loop's `[ -f "dist/nova-bus_${TAG}_linux_amd64" ]`
    (release.yml:163), which fails any `tools` value that does not ship that one
    tool and is a check on a name where the policy now states the set (Fable
    LOW); and the comment at release.yml:176-189 naming "the six legacy tools",
    while `assert-version-stamp.sh:34` holds `LEGACY_NO_VERSION_VERB=""` — an
    empty list since #121 — so the prose claims an exemption the script no longer
    has (Opus LOW). All line numbers here are #252's head
    `59b85fd6`, the tree this item edits (draft 5: draft 4 cited `main`, which
    those numbers stopped describing the hour #252 merged — Fable LOW, Opus LOW).
    What this item inherits from #252: the file's first job is `certified`, whose
    whole ask is `.github/scripts/release-certified.sh`, run again inside the
    `release` job after its checkout and toolchain steps (release.yml:108-117,
    the third step — draft 6, Fable LOW), so **this item adds no test job back**; the
    policy's `workflow` line is the workflow that script asks about, while its
    `aggregate` line has **no counterpart in the script at all** — the script
    reads a run's conclusion and fetches no job (draft 5, Opus MEDIUM 5, which
    found draft 4 claiming both names were the script's), and that asymmetry is
    rule 4's accepted one-directional difference rather than a repair owed here.
    These are the edits outside `cmd/` and `internal/`, and they land in the same
    release as the binary (rules 4, 6, 8, 12).
12. **`docs/SPEC.md`** — one paragraph under the companion-spec list naming
    `RELEASE`'s tokens: `CANDIDATE`, `DELTA`, `CERTIFY`, `BUMP`, `SITES`,
    `NOTES`, `TREE`, `POLICY`, `STATUS`, `CUT`, `VERIFY`, with `ENTRY`, `JOB`,
    `RUN`, `SITE`, `TERM`, `FIELD`, `NOTE` and `MORE` informational.
13. **Named, not done here:** an additive `candidate=<sha12>` on `nova-merge
    status` read from `releases/`, for SPEC-MERGE, after this tool has cut one
    release; and a second predicate for a draft or prerelease channel, if a
    repository that needs one asks (rule 6, Stella 6).

## The open questions, answered

Draft 1 asked three and the reads answered all three, twice each.

- **Should the candidate also be a ref on the wire?** No — both reads agreed
  with draft 1. A branch this tool creates is a mutation with a trigger surface,
  and a green needed at an S that is no longer the tip is the re-freeze case by
  definition. What changed is the remedy's order: `certify` names the re-freeze
  **first**, because dispatching at a moved tip cannot produce a run at S
  (rule 4, Opus).
- **Should the candidate be read whole, as a term?** Yes — both reads said yes,
  and it is rule 13. They differed on the shape: a `nova-merge` read record with
  head S (Fable) or a record kind of this tool's own (Opus). This spec takes
  Opus's, because a read record lives under `reads/<entry>/`, would be folded by
  `nova-merge` as an entry read and would collide with a landing read for the
  same sha.
- **Is `--probe <tool>` the right shape?** No — the probe set is a policy line,
  and this repository's value is `all` (rule 8). Fable wanted every tool and no
  flag, Opus wanted the flag with an `all`; a policy line is a stated set with
  no per-run choice, and #118's unstamped `nova-wake` is why the stated value
  here is `all`.

**And one the reads opened.** Draft 1 assumed that creating a release through
the API raises `release.yml`'s `push: tags` trigger for this credential. Draft 2
does not assume it: `cut` says `assets=pending`, `verify` names the run or its
absence, and the first `cut` under this tool is the measurement (rule 6,
Stella 6). Draft 4 adds the second half of that measurement: since nova-tools
#252 the run that trigger starts can itself refuse — twice, once per ask — when a
certification run at the tagged commit is unfinished or the group of them
carrying the newest `updated_at` is not uniformly green, so `assets=pending` has
three outcomes and `verify` says which (rules 4, 6, 8).
