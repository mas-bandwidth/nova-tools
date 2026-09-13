# nova-release — specification (draft 3, 2026-09-13)

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
Stella's. **Draft 3 folds the two cold reads of draft 2 at `1af78e31`**, Opus's
and Fable's, both HOLD. Every repair below is one of theirs, named where it
lands; where two readers wanted different shapes for one repair, the rule says
which was taken and why; and a passage this draft repaired says `draft 3`.

Three releases of this repository were cut in two nights by hand, and one is
being prepared by hand today. Each shipped. Each also failed in a way the next
one repeated. This tool is those failures closed, one rule each.

| the failure, from the record | the rule that closes it |
|---|---|
| v0.13.0 and v0.14.0 (nights of 2026-09-12, nova-tools #229): "Until the freeze, every unrelated merge was held. At the freeze the hold lifted with one rule: every later change to main gets a recorded delta review against the candidate, a ledger line on #164 naming the head, the reviewer and the CI run." The ledger was an issue comment written by a hand; the tag went on whatever the tip was | `candidate` freezes **one sha** as a record in the lane branch (rule 1); the tag goes on **that sha and no other** (rule 6); anything landed after it is not in the release, and a re-freeze is a new record that forces a new `delta` (rule 2) |
| the same nights: merges landed after the candidate was chosen, so the release shipped a tree nobody had read whole; a `--auto` merge landed before CI finished (Glenn, 2026-09-11: **never `--auto`**) | `delta` walks every landing between the last release and the candidate and **refuses while any landing lacks a read** keyed to its head (rule 3); this tool has no merge verb and no `--auto` to refuse — it cannot land anything |
| 2026-09-13: v0.15.0 prepared by hand as PR #232 — two install lines in `docs/USAGE.md` bumped by an editor, the release body drafted inside the pull request's own body | version sites are a **manifest**, never a grep (rule 5); the notes are a **file a line wrote**, read by another line, keyed to the file's hash (rule 7) |
| 2026-09-13: certification run 34771523657, dispatched on `main` at 9a95e33e, RED on `build-windows` and `test-windows (internal/swarm)` — `symlink_fifo_test.go:28:20: undefined: syscall.Mkfifo` — "a leg the fast tier never ran"; fix PR #235 blocks the tag | `certify` requires the certification aggregate **green at exactly the candidate sha**, read job by job from the wire, never from a badge or a run's status (rule 4); **a red is stop** |
| #229, the same night: the release run for v0.14.0 "failed once on Windows: `internal/merge TestThirtyConcurrentWriters` … Attempt 2 on the unchanged tag succeeded at 23:27:37Z (56 assets)" | `verify` reads the tag, the release, the asset count and names and the version the shipped binary prints, and names the first field that disagrees (rule 8); a red release run is re-run on the **same tag**, never widened, never re-tagged |
| #229: stacked PR #144 "when it was called merged, it had merged onto the stack, not main" | a landing is a **first-parent commit on the default branch**, walked in a clone that fetched the wire; nothing is a landing because a sentence said so (rule 3) |
| Glenn, 2026-07-22: "when I ask you to create a release, I always mean, create the full github release. Tags + github release." — and 2026-07-30, the third time: "please always make a real github release with release notes, not just a tag" | `cut` creates the tag and the release **in one act**; there is no verb that makes a bare tag, and the release workflow's create path becomes a refusal (rule 6) |
| Glenn, 2026-08-03: "A RELEASE TITLE STATES WHAT IS NOW TRUE, OR NOW POSSIBLE … does this name a capability, or a defect?"; 2026-09-06: "We never do releases with discovery details or archaeology. It's ALWAYS what the user gets." Measured today on the wire: the titles of v0.13.0 and v0.14.0 are the bare strings `v0.13.0` and `v0.14.0` | the notes file carries a title that is **not the tag**, a body that is **not generated**, and a recorded read by a line other than its author answering the one question (rule 7); `cut` refuses without all three |
| a release "goes through a tool, never `git tag` by hand" (Glenn's standing rule); a local tag is a cache — 2026-08-31, `git log v2.0.0..main` against a retired local tag returned commits from another era and nearly put a false wire claim in a published note | every base sha is **read from the wire** (`git/ref/tags/<tag>`, peeled) and the clone fetches tags with a forcing refspec; no verb ever runs `git tag` (rule 9) |
| release.yml #118: `-X main.version=` with an empty value stamped every binary `devel` while the release page said a tag; v0.11.0 was published by hand before its tag's run reached the upload step and "got none of its binaries" | `verify` downloads this host's binary from the release, checks its `SHA256SUMS` line and runs `version`; the field-two identity must be the tag (rule 8); the workflow's order — release exists, then assets attach — is now the **designed** order, not the accident |
| 2026-08-25: a public correction quoted a tool string from the workshop copy, not the public build, and cited `v0.3.0` when the release was `v0.7.0` | what `verify` prints is what the **downloaded** binary printed; a version site is a manifest line, so a stale pin is a `SITES FAIL` naming the file |
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
   <tag>` when there is none. The file, not a person's memory, is where the
   freeze is (#229's third open question: "Where does the freeze itself get
   recorded so tools can read it").
2. **A re-freeze is a new record, and it voids the old delta.** A second
   `candidate` for the same tag at a different sha is allowed and is printed
   `CANDIDATE OK … supersedes=<sha12>`; every `delta`, `certify`, `notes` and
   `tree` record is keyed to the candidate sha it was made for, so after a
   re-freeze `status` shows `delta=- certify=- tree=- notes=-` and `cut` refuses
   until all four are made again at the new sha — the notes read among them,
   because the notes describe a range the re-freeze moved (rule 7). This is how "anything merged after the freeze is either
   excluded from the release or forces a recorded delta read at the new
   candidate" becomes a mechanism rather than a sentence: a landing after the
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
4. **Certification is one named run at exactly the candidate sha, read job by
   job, and read again at `cut`.** The workflow file, the aggregate job, the
   events a run may come from and the jobs whose `skipped` is allowed come from
   the release policy (rule 12), not from flags a caller picks per run, so what
   counts as certified is a reviewed fact of the tree at S rather than the
   caller's choice (Stella 2). `certify` asks the host for **completed** runs of
   that workflow whose `head_sha` equals the candidate — never the newest run on
   the branch, because a `workflow_dispatch` on the default branch runs at that
   branch's tip, and after a post-freeze landing the tip is not the candidate —
   reading **every page** of runs and of jobs, because a cap is on what is
   printed and never on what is read.

   **Which run counts**: the **newest completed run at S by `created_at` whose
   event the policy trusts**, and that run's **latest attempt's** jobs.
   `certification.yml:22-24` sets `cancel-in-progress: true` on
   `certification-${{ github.ref }}-${{ github.event_name }}`, so a re-dispatch
   at one ref leaves a `cancelled` run beside the green one at the same sha, and
   #229's own repair was a re-run: a rule that required every run at S to be
   green would refuse a sha that is certified (Fable MEDIUM 3, Opus MEDIUM 7).
   Every **earlier** completed run at S whose conclusion is not `success` prints
   a `CERTIFY RUN` line with its id and conclusion, so a red that was re-run is
   on the record rather than erased.

   In that run it reads the **aggregate job's conclusion by name**, then every
   job's, and requires `success` of all: `failure`, `cancelled`, `timed_out`,
   `neutral`, `action_required`, a `skipped` job the policy does not name as
   skippable, a job the policy requires that is **absent** from the run
   (`conclusion=absent`, which is how a required leg silently dropped from a
   matrix is caught — Stella 2), and a run that is not `completed` are each a
   `CERTIFY FAIL` naming the job and its conclusion. A run's own status or a
   status badge is never read as evidence (2026-09-07: "a QUEUED certification
   run hides a COMPLETED red leg, and five merges landed on a red main"). A
   green writes the certify record with the run id, the attempt, the event, the
   job names, the count and the policy's hash at S.

   **A record is not current proof.** `cut` re-reads that attempt and asks for
   any newer completed run at S, because a green run can be re-run red after the
   record was written (Stella 2): the term is decided on the wire's answer now,
   not on the record's then. No run at the sha is `CERTIFY FAIL … no completed
   run at <sha12>`; the remedy names the **re-freeze first** and the dispatch
   second, because dispatching at a moved tip cannot produce a run at S (Opus).
   **A red is stop**: the verb exits 1 and nothing downstream can proceed; there
   is no `--allow-red`. (2026-09-13: run 34771523657, `build-windows` and
   `test-windows (internal/swarm)` red at 9a95e33e, "a leg the fast tier never
   ran".)
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
   to the rendering with `--tag`, refusing a site that does not hold exactly
   `count` renderings of `--from`; it commits nothing and pushes nothing, and
   the change lands through the lane like any other, **before** the freeze.
   `bump` reads the policy, and the sites manifest the policy names, **from
   `--dir`**, the caller's own checkout, because before the freeze there is no S
   to read them at; it takes no `--sites` (draft 3: draft 2's rule 12 said the
   policy is read from the caller's checkout "only by `bump`" while `bump` took a
   sites file and no `--policy`, so one of the two was wrong — Fable MEDIUM 4,
   Opus LOW). It is **all or nothing**: every site is
   read and planned first, any refusal refuses the whole bump before a byte is
   written, and each write is a temp file and a rename, so a failure at site
   four never leaves sites one to three looking done (Stella 5). Every site path
   is checked before it is opened — relative, no `..`, and a **regular file**
   under `--dir`, never a symlink and never a FIFO (this repository's own rule,
   security#30).
   `status` and `cut` then check that every site **at the candidate tree**
   renders `--tag` exactly `count` times, `SITES OK sites=<n>`, and a site
   that does not is `SITES FAIL <path>: <reason>`. The tag therefore goes on a
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
   **The exit is stated once, here**: this is the only `CUT FAIL` at 1, the
   host-answer `CUT FAIL` of **the cut condition** is the only one at 2, and
   `field=tag` is the token that tells them apart (draft 3: draft 2's exit table
   put the token in its exit-2 row and made this paragraph and the table
   disagree — Opus MEDIUM 4, Fable LOW). The line says what the wire now holds:
   the release this call created **exists** at `<url>`, bound to a tag that
   points elsewhere, and removing it is a hand's act and not this tool's (draft
   3, Fable LOW). A create whose answer was lost is reconciled by re-running
   `cut`: the tag at S carrying a release that is **neither draft nor
   prerelease** and whose title and body are the notes' prints `CUT OK …
   created=already`, which is this operation finishing rather than a second one
   (draft 3, Fable LOW: draft 2's reconciliation accepted a draft that rule 8
   would then refuse).

   **`cut` publishes a release that has no assets yet, and says so.**
   `release.yml` is triggered by `push: tags: ['v*']` (release.yml:29-32), and
   whether the release-create call's tag creation raises that event for the
   credential this tool runs under is a fact about the host to be **measured,
   not assumed** (Stella 6): the first `cut` is followed by `verify`, whose
   `field=assets` names the workflow run's status or its absence, and the remedy
   when there is no run is that workflow's own `workflow_dispatch` at the tag.
   `CUT OK` therefore carries `assets=pending`. Published, assets-pending and
   verified are three states, and no line of this tool says "ready to install"
   until `verify` does. The release workflow's job is then
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
   (hash, S)** — `notes --who <name> --notes <file> --verdict approve|hold
   [--author <name>] [--note <text>]` writes one immutable record carrying both
   the hash and the candidate sha it was made at, and a `hold` blocks exactly as
   a lane hold does. **`notes` makes the same three checks `cut` makes** and
   refuses to record a read on notes `cut` would refuse: an approve of a title
   named for the tag alone costs a line's attention and buys nothing (draft 3,
   Fable LOW: test 7 named the refusal and draft 2 gave the check to `cut`
   only).

   **A re-freeze voids the notes read**, as it voids the delta, the certify and
   the tree record, even when the file's bytes did not change: after a re-freeze
   `status` prints `notes=-` beside `delta=- certify=- tree=-`. The notes
   describe a range, and a re-freeze moves it — draft 1 keyed the approve to the
   hash alone, so a release one landing larger would have shipped un-noted with
   a counted read (Fable HIGH 2, Stella 4).

   The reader answers one question, and the tool cannot: **does every sentence
   say what the reader of this release gets?** The tool prints the question on
   `NOTES OK` so the reader saw it. The notes' **author is declared** in the
   record — `--author <name>` when the writer is not the reader — and an approve
   by the declared author does not count; with no declaration, the authors of
   record are whoever recorded the candidate or the delta, which is why the
   delta record carries `who` (Fable MEDIUM 4). This read is about the notes: it
   neither satisfies rule 3 nor stands in for rule 13's read of the tree, and
   the three are separate terms (Stella 4). `delta` prints the exact range so
   the notes are written from the diff, not from the sitting
   (identity/releases.md, 2026-09-06: "the notes get drafted in the same sitting
   as the fold, in the fold's register").
8. **`verify` reads the release back and names the first field that
   disagrees.** After `cut`, and after the release workflow has run, `verify`
   reads, in order: the tag ref, peeled, which must be the candidate sha; the
   release object by tag, which must exist, be neither draft nor prerelease, and
   carry the title and a body equal to the body of **the standing approve's
   notes summary on the lane branch** (rule 7, draft 3) under rule 7's
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
   well (draft 3: release.yml:103-105 keeps the exclusion of a tool that must not
   ship "in one visible place" inside the workflow, so `verify`'s expected set
   could name an asset the build loop deliberately did not build; work list item
   11 — Opus MEDIUM 5). Elsewhere a `cmd/`
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
   print (the count line prints on failure). A release whose assets are not
   there yet is a `VERIFY FAIL field=assets` naming the release workflow run's
   status, or its absence and the dispatch that starts one; `verify` never waits
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
    all** — `delta` over landings, `certify` over jobs and over earlier runs at
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
    events      workflow_dispatch,schedule   the events a run at S may come from
    required    <job>[,<job>]                jobs that must appear in the run
    skippable   <job>[,<job>]                jobs whose `skipped` is not a failure
    sites       .github/release-sites.tsv    the version-site manifest of rule 5
    authors     .github/release-authors.tsv  `<login><TAB><line>` for rule 3
    probe       all                          the tools `verify` runs, or a list
    ```

    The third column above is prose about each key, not a third field: the file
    is `key<TAB>value` and nothing more, and a parser that read the comment would
    be reading this document rather than the file (draft 3, Opus LOW).

    Draft 1 typed the platform list into a flag "a third time on purpose, so a
    test can prove the three agree", and `certification.yml:492-494` already
    says of its own copy that the job "is worth nothing the moment the two lists
    disagree". Three copies and a test that compares them is a rule about
    copies; one file is no copies. Both workflows' target loops are shell
    (`release.yml:116`, `certification.yml:523`), not matrices, so both can read
    `.github/platforms.tsv` with a `while read` — a matrix would have needed the
    typed copy, a loop does not, so the one-file form **is** possible here
    (Fable MEDIUM 7; Opus held that the typed flags satisfy Conventions'
    no-guessed-paths, and they do — but so does a flag naming the one file, and
    the one file also removes the disagreement, so this spec takes Fable's).
    Nothing here is guessed: `--policy` is a required flag, and every path
    inside the file is a path in the repository, refused when it is absolute,
    climbs with `..`, or is not a regular file in the tree at S. The policy's
    own `sha256` at S goes into the certify and cut records, so a record names
    the policy it was judged under (Stella 2, 5).
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

## The verbs

```
nova-release candidate --lane <dir> --tag <tag> --sha <40 hex> --who <name> [--note <text>]
nova-release delta     --lane <dir> --tag <tag> --work <dir> --policy <path> --who <name> [--since <tag|sha>] [--max <n>]
nova-release certify   --lane <dir> --tag <tag> --work <dir> --policy <path> --who <name> [--max <n>]
nova-release bump      --policy <path> --dir <checkout> --from <tag> --tag <tag> [--max <n>]
nova-release notes     --lane <dir> --tag <tag> --notes <file> --who <name> --verdict approve|hold [--author <name>] [--note <text>]
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
tree at S rather than typed per run, and the platform list is the one file both
workflows read. The exception is `--timeout`, 120 seconds, for the reason
SPEC.md gives.

**`status` is the survey and `cut` is the act, and they read the same
predicate.** `status` prints one line per term of the cut condition and the
verdict, writes nothing and takes no lock, and **exits 0 whether `ready` is
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
where every other term is named.

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
test refer to it:

```
CUT(tag) :=
      C   = the NEWEST candidate record for tag; its sha is S
  and P   = the previous release's sha read from the wire this run, ignoring a
            release for tag itself, with merge-base(P,S) = P
  and D   = a delta record for (tag, S) with unread=0 and since=P
  and R   = a certify record for (tag, S) whose run, RE-READ on the wire now,
            concludes success on the aggregate and on every job, with no newer
            completed run at S that does not
  and T   = a tree record for (tag, S): a standing approve by a line other than
            the one who recorded C
  and every site in the policy's sites file renders tag exactly count times in
      the tree at S
  and N   = a STANDING approve of the notes for (hash, S) by a line other than
            the notes' declared author, whose summary on the lane branch carries
            a title that is not the tag and does not end with it and a body that
            is not generated
  and on the wire: no tag named tag; or the tag at exactly S with no release; or
      the tag at exactly S with a release that is neither draft nor prerelease
      and whose title and body are the notes' (the reconciliation of a create
      whose answer was lost)
```

When it holds, `cut` takes `<lane>/checkout.lock`, re-reads the wire terms, makes
one release-create call with `target_commitish=S`, the title and the body, reads
the tag back peeled, and prints `CUT OK … assets=pending` (rule 6). When any
term fails, `cut` prints that term's `FAIL` line and `CUT REFUSED: <the term>`,
exit 1, and nothing was created. A host answer that is neither success nor a
refusal it can name — a 502, a rate limit, a dropped connection, a call cut at
`--timeout` — is `CUT FAIL … : <the host's first line>`, **exit 2**, and the
remedy is `nova-release verify`, because a failed create is read back before it
is reported absent (2026-09-13: two creates answered 502 and both objects
existed). Exit 2 is what SPEC.md's table calls "could not run", and an answer
that was neither yes nor no is exactly that; draft 1 said 1 in this paragraph
and 2 in its own table (Fable MEDIUM 6, Opus HIGH 3, Stella 3).

**The predicate is keyed to S throughout.** Every record but the candidate —
delta, certify, notes, tree — carries the candidate sha it was made at; a record
for another sha is never consulted, `status` prints it as `-`, and there is no
flag that accepts an older one. The notes read is keyed to **(hash, S)**, so a
re-freeze voids it even when the bytes did not move (rule 7). **Standing is
defined here and stated nowhere else** (draft 3): a verdict is standing when it
is the newest for its `(who, hash, S)` under the fold, ties folding toward the
refusing verdict, so a hold blocks and a hold its own line later lifted does not
— draft 2's predicate still read "with no hold for (hash, S)" beside this fold
and the two could be read against each other (Fable, not closed; draft 1's "no
hold for that hash" was the same hurt one draft earlier). A `delta` record's `since` is re-derived from the wire at
`cut` time and must equal the record's: a release published between the delta
and the cut moves the base, and the walk must be redone. **The re-derivation
ignores a release for `tag` itself**, or the reconciliation of a lost create
answer could never finish: the release this act published would have become
`latest` and moved its own baseline (Stella 3).

## Exit codes

This tool's own table, in SPEC.md's three codes, with **every host-answer case
named once** — draft 1 put a 502 at 1 in its prose and at 2 in this table, gave
`status` no code, and spelled `REFUSED` both ways (Fable MEDIUM 6, Opus HIGH 3,
Stella 3).

| code | meaning |
|------|---------|
| 0 | the verb ran and passed: a record written, a walk with `unread=0`, a certification read green, a release created or reconciled, a release verified field for field — and every `status`, whatever `ready` says |
| 1 | the verb ran and said **NO**: an unread landing or an untestable author exclusion, a red, missing or superseded certification, a site that does not render the tag, notes without a read or with a hold, no tree read at S, a `cut` whose predicate does not hold, a tag that exists at another sha, a tag that reads back at another sha after the create (`CUT FAIL … field=tag`, rule 6), a `verify` with a field that disagrees |
| 2 | the verb **could not run**: a missing flag, `refusing to guess`, a `--lane` that is not a lane, a `--work` that is a dirty work tree, a policy, manifest or notes file that does not parse, `gh` or `git` absent, a call cut at `--timeout`, and **any host answer that is neither yes nor no** — a 502, a rate limit, a dropped connection — the `CUT FAIL … : <the host's first line>` of the cut condition among them, whose remedy is always `nova-release verify` |

A host answer is placed in exactly one of those rows: **yes** decides the term;
**no** — a 404 for a tag that must exist, a 422 the host explains — is exit 1
with the field named; anything else is exit 2. `REFUSED` follows the same split
rather than carrying a code of its own: `refusing to guess: no candidate for
<tag>` is about the invocation and exits 2, `CUT REFUSED: <term>` is about the
state and exits 1.

**Two `CUT FAIL` shapes share a second token and do not share a code, and the
difference is one field (draft 3).** `CUT FAIL … field=tag expected=<S12>
found=<x12>` is rule 6's read-back: the verb ran, the wire answered, the answer
is no — **exit 1**. `CUT FAIL tag=… sha=…: <the host's first line>` is an answer
that was neither yes nor no — **exit 2**, remedy `nova-release verify`. `field=tag`
is the only token a scanner needs to tell them apart, and draft 2 stated the exit
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
DELTA OK tag=<tag> who=<name> since=<P12> candidate=<S12> landings=<n> read=<n> unread=0 file=<path> pushed=true
DELTA FAIL tag=<tag> since=<P12> candidate=<S12> landings=<n> read=<n> unread=<n>: <n> landings have no read; nova-merge read --lane <dir> --pr <n> --who <you> --head <sha> --verdict approve|hold
DELTA FAIL field=<since|pr> commit=<sha12> expected=<x> found=<y>: <reason>
DELTA REFUSED: <reason>
CERTIFY RUN run=<id> created=<stamp> conclusion=<word>: an earlier completed run at this sha
CERTIFY JOB run=<id> job=<name> conclusion=<word>
CERTIFY MORE kind=<job|run> shown=<n> total=<t> nova-release certify … --max 0
CERTIFY OK tag=<tag> sha=<S12> who=<name> workflow=<file> run=<id> attempt=<n> event=<word> jobs=<n> aggregate=<job> file=<path> pushed=true
CERTIFY FAIL tag=<tag> sha=<S12> workflow=<file> run=<id|-> job=<name|-> conclusion=<word|absent|->: <reason>
CERTIFY REFUSED: <reason>
BUMP SITE <path>: <count> renderings of <from> became <tag>
BUMP MORE kind=site shown=<n> total=<t> nova-release bump … --max 0
BUMP OK sites=<n> from=<tag> tag=<tag> dir=<checkout>
BUMP FAIL <path>: <reason>
BUMP REFUSED: <reason>
SITES OK sites=<n> tag=<tag> at=<S12>
SITES MORE kind=site shown=<n> total=<t> nova-release status … --max 0
SITES FAIL <path>: <reason>
NOTES OK tag=<tag> sha=<S12> who=<name> author=<name> verdict=<approve|hold> hash=<sha256:12> title=<title> file=<path> pushed=true: does every sentence say what the reader of this release gets?
NOTES FAIL <path>: <reason>
NOTES REFUSED: <reason>
TREE OK tag=<tag> sha=<S12> who=<name> verdict=<approve|hold> file=<path> pushed=true
TREE FAIL <sha12>: <reason>
TREE REFUSED: <reason>
POLICY OK file=<path> at=<S12> hash=<sha256:12> platforms=<n> tools=<n> probe=<n>
POLICY FAIL <path>[:<line>]: <reason>
STATUS TERM term=<candidate|since|delta|certify|tree|sites|notes|wire> ok=<true|false> detail=<one token or ->
STATUS OK tag=<tag> candidate=<S12|-> since=<P12|-> delta=<unread|-> certify=<run|-> tree=<approve|hold|-> sites=<n|-> notes=<approve|hold|-> wire=<absent|tag-only|released> ready=<true|false>
CUT OK tag=<tag> sha=<S12> who=<name> created=<tag+release|release|already> assets=pending title=<title> url=<url>
CUT REFUSED: <the failing term's line>
CUT FAIL tag=<tag> sha=<S12> field=tag expected=<S12> found=<x12>: the release at <url> stands on a tag that points elsewhere; removing it is a hand's act
CUT FAIL tag=<tag> sha=<S12>: <the host's first line>; nova-release verify before reporting anything
VERIFY FIELD field=<tag|release|title|body|assets|asset|checksum|version> platform=<goos/goarch|-> expected=<x> found=<y> ok=<true|false>
VERIFY MORE kind=<asset|probe> shown=<n> total=<t> nova-release verify … --max 0
VERIFY OK tag=<tag> sha=<S12> assets=<n> probed=<n> version=<field two> run=<id|->
VERIFY FAIL field=<name> expected=<x> found=<y>: <reason>
VERIFY REFUSED: <reason>
<VERB> REFUSED: <reason>
```

`DELTA ENTRY` is capped at `--max` and `DELTA OK`/`FAIL` counts the walk, not
the lines. `DELTA NOTE` prints the range and the base's tag on every completed
walk, pass or fail, because the notes are owed either way and the diff is where
they come from. `VERIFY FIELD` lines print for every field even after the first
`false`, so a reader learns everything that is wrong in one run; `VERIFY FAIL`
names the **first** one. `CERTIFY JOB` prints only jobs whose conclusion is not
`success`, and `CERTIFY RUN` only earlier runs at S that are not `success`, both
capped at `--max`; a green run with no earlier red prints neither, and
`jobs=<n>` says how many were read.

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
<lane>/releases/<tag>/delta-<sha12>-<at>-<rand6>.json       {file, tag, sha, who, since, since_tag, policy_hash, landings, read, unread, entries:[{commit, landing, pr, head, author, line, read:[who]}], at}
<lane>/releases/<tag>/certify-<sha12>-<at>-<rand6>.json     {file, tag, sha, who, workflow, run, attempt, event, aggregate, jobs, job_names, policy_hash, at}
<lane>/releases/<tag>/notes-<hash12>-<at>-<rand6>.json      {file, tag, sha, hash, who, author, verdict, note, title, at}
<lane>/releases/<tag>/notes-<hash12>-<at>-<rand6>.summary   the normalised notes, one immutable copy per read (rule 7), tracked beside the record
<lane>/releases/<tag>/tree-<sha12>-<at>-<rand6>.json        {file, tag, sha, who, verdict, note, at}
<lane>/releases/<tag>/cut-<sha12>-<at>-<rand6>.json         {file, tag, sha, who, url, title, created, notes_hash, sites_hash, policy_hash, at}
<lane>/outbox-release/<at>-<rand6>.json                     a record not yet confirmed at the remote tip, untracked
<lane>/outbox-release/.gitignore                            `*`, written on first use, so a lane mid-verb still answers a clean porcelain (**why a new binary**)
```

**`file` is a field of every shape**, the record's path under the lane, because
the compare-and-swap loop reads it to know where the bytes belong
(`internal/merge/records.go:267`, `destinationOf`). Draft 1's five shapes
omitted it, which would have made every record undeliverable by the very loop it
was written for (Fable MEDIUM 4, Opus HIGH 1). `delta` carries `who` because
rule 7 derives the notes' authors of record from the candidate's and the delta's
recorders and draft 1's delta shape named nobody (Fable MEDIUM 4). The certify
and cut records carry the hashes of what they were judged under, so a record
cannot be read as a claim about a manifest somebody edited afterwards (Opus
MEDIUM 8, Stella 2).

**`who` is a field of every shape and a required flag of every verb that writes
one (draft 3).** Draft 2 gave the candidate, delta and certify records a `who`
and gave `candidate`, `delta` and `certify` no flag to fill it, while two terms
of the predicate stand on it: rule 13's approve "by a line **other than** the one
who recorded the candidate", and rule 7's authors of record, "whoever recorded
the candidate or the delta, which is why the delta record carries `who`". The
tool may not infer it — **No guessed anything**, and it reads no lane field but
four, a host login being an account rather than a line (rule 3). So `--who
<name>` is required on `candidate`, `delta`, `certify`, `notes`, `tree` and
`cut`, `who=<name>` prints on each one's `OK` line, and the cut record carries it
too, so the act names its actor as every other record does (Opus HIGH 1 and
LOW).

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
   `git status --porcelain` empty, commits, pushes and prints `pushed=true`,
   because the tool wrote `<lane>/outbox-release/.gitignore` holding `*` on first
   use (Fable HIGH 2); and `candidate` without `--who` refuses at exit 2, while
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
   (draft 3).
4. A run at the candidate whose aggregate is `success` but one matrix leg is
   `failure` is `CERTIFY FAIL` naming the leg; **a `cancelled` older run beside a
   newer green one at S is green, and the cancelled one prints a `CERTIFY RUN`
   line**; a green record whose run is re-run red before `cut` makes `cut`
   refuse, because the term is re-read on the wire; a required job absent from
   the run is `conclusion=absent`; a `skipped` job the policy does not name is
   red and one it names is not; a second page of jobs is read before anything is
   counted; a run of another workflow, or from an event the policy does not
   trust, is not evidence; a run `in_progress` is not evidence; a run at another
   sha is `no completed run at <sha12>`; a green run writes the record with its
   id, attempt and job names.
5. `bump` takes `--policy` and no `--sites`, and reads the policy and the
   manifest it names from `--dir` (draft 3). A manifest with a `{tag}` twice, or none, refuses at load; `bump` on a site
   holding two renderings of `--from` where `count` is 1 refuses naming the path
   **and writes nothing at all, including the sites it had already planned**; a
   site path that is a symlink, or climbs with `..`, refuses; a good bump
   rewrites exactly the manifest's lines and nothing else (a byte diff of the
   checkout); `status` at a candidate whose site still reads the old tag is
   `SITES FAIL <path>`; the sites file is read from the tree at S, so a local
   edit to it changes no verdict.
6. `cut` with `--draft` or `--prerelease` refuses before any host call; with a
   tag existing at another sha refuses naming both shas; with the tag at S and
   no release creates the release alone and prints `created=release`; **a fake
   host that answers 201 while the tag peels to another sha is `CUT FAIL
   field=tag`, exit 1, with no record written**; a create whose answer is lost
   and whose release exists, neither draft nor prerelease, with the notes' title
   and body is `CUT OK … created=already` on the re-run, **while the same release
   marked draft is not** (draft 3); a 502 from the create is `CUT FAIL` naming
   `verify`, **exit 2**, and the read-back mismatch above is `CUT FAIL
   field=tag`, **exit 1** — one token apart and one code apart (draft 3); the fake host records exactly one create call carrying
   `target_commitish=S`, the title and the body; a source test finds no call
   site that runs `git tag` or `git push … refs/tags`.
7. Notes titled with the tag alone, or ending with the tag, refuse **at
   `notes`, before a read is recorded, and at `cut`** (draft 3); a file whose
   first line is not `# <title>` refuses at exit 2 before anything is hashed
   (draft 3); a body containing `## What's Changed` refuses; notes with no read
   refuse; a `hold` for (hash, S) blocks while an older `approve` exists; an
   approve by the declared author, and by the candidate's recorder when none is
   declared, does not count; **a `notes` approve writes the normalised bytes as
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
   after the first false; a release whose assets are absent names the workflow
   run's status.
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
    in this repository's CI** reads `.github/platforms.tsv` and proves that
    `release.yml` and `certification.yml` both read that file and hold no typed
    target list of their own — that test is about this repository's workflows,
    so it lives here and not inside the tool (Opus MEDIUM 4).
13. `cut` refuses with `tree=-` when no `tree` record for S exists; an approve by
    the line that recorded the candidate does not count; a `hold` blocks; the
    record lands under `releases/<tag>/` and a `nova-merge` fold of the same
    lane counts exactly the reads and gates it counted before.

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
   logged under the other tool's name (draft 3, Opus LOW); and the outbox scan
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
   with every page, read a run's attempts and jobs with every page, associate a
   commit with its pull request, read a pull request's author and head; every
   answer's status read before its body (release.yml's `ask` pattern); the one
   host write, release-create with `target_commitish`, and the peeled read-back
   that follows it. Tests 4, 6, 8, 9.
5. **`internal/release/delta.go`** — the clone in `--work`, the forcing tag
   fetch, the ancestry check, the first-parent walk, the head and author
   resolution for both landing shapes, the authors map and the read lookup in
   the lane's `reads/` fold. Test 3.
6. **`internal/release/sites.go`** — the manifest the policy names, `bump` as one
   all-or-nothing plan reading the policy and the manifest from `--dir` (draft
   3), the path checks, and the check at a tree. Test 5.
7. **`internal/release/notes.go`** — the normalisation through trailing blank
   lines, the first-line refusal, the title and body checks `notes` and `cut`
   both make, the hash, and the notes read record keyed to (hash, S) **carrying
   the normalised notes as its `.summary`**; no `notes.md`, and nothing but JSON
   in the outbox (rule 7, draft 3). Test 7.
8. **`internal/release/cut.go`** — the predicate as one function `status` and
   `cut` both call, with `cut` acting on `ready=true` only, under the checkout
   lock, re-reading the wire terms and reading the tag back. Tests 6, 13.
9. **`internal/release/verify.go`** — the field walk, the expected set from the
   policy, the downloads into `--work`, the checksum check, running `version`.
   Test 8.
10. **`cmd/nova-release/main.go`** — the verbs, refusals naming every
    independent problem at once, the banner with its `example:` block, the
    `### First run` in `docs/CLI.md` and a row in `docs/USAGE.md`. Tests 10, 11.
11. **`.github/platforms.tsv`, `.github/workflows/release.yml`,
    `.github/workflows/certification.yml`** — the one platform file, both build
    loops reading it with `while read`, **release.yml's build loop reading the
    policy's `tools` line for the shipped set** instead of the in-workflow
    exclusion of release.yml:103-105 (draft 3, Opus MEDIUM 5), and release.yml's
    create path becoming a refusal; **the upload path unchanged**, which is the
    condition under which partial-upload safety is left to release.yml:231-232
    and :247 (draft 3, Fable's ruling); the comment that explains the v0.11.0
    accident rewritten to say the order is now designed. These are the edits
    outside `cmd/` and `internal/`, and they land in the same release as the
    binary (rules 6, 8, 12).
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
Stella 6).
