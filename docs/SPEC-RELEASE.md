# nova-release — specification (draft 1)

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
`nova-merge`'s fold does not walk (it folds `reads/` and `gates/` and nothing
else, `internal/merge/records.go`), so the addition is inert to the running
tool. The problem is new: tags, release objects, assets, version sites and
notes have no verb anywhere in the set today, and the release workflow was the
only code that touched them.

## The rules, numbered

Every rule here is normative. Each has one line in **tests this spec demands**
near the end. The date on a rule is the day it was learned.

1. **A candidate is one sha, frozen by a verb, recorded where the lane's
   records live.** `candidate --lane <dir> --tag <tag> --sha <40 hex>` checks
   that the sha is a commit reachable from the lane's base on the wire, then
   writes one immutable file, `<lane>/releases/<tag>/candidate-<sha12>-<at>-<rand6>.json`,
   and commits and pushes it to the lane branch through the same outbox and
   compare-and-swap loop SPEC-MERGE rule 22 defines, under the same
   `<lane>/checkout.lock`. Every other verb reads the **newest** candidate
   record for `--tag` and refuses with `refusing to guess: no candidate for
   <tag>` when there is none. The file, not a person's memory, is where the
   freeze is (#229's third open question: "Where does the freeze itself get
   recorded so tools can read it").
2. **A re-freeze is a new record, and it voids the old delta.** A second
   `candidate` for the same tag at a different sha is allowed and is printed
   `CANDIDATE OK … supersedes=<sha12>`; every `delta` and `certify` record is
   keyed to the candidate sha it was made for, so after a re-freeze `status`
   shows `delta=-` and `certify=-` and `cut` refuses until both are made again
   at the new sha. This is how "anything merged after the freeze is either
   excluded from the release or forces a recorded delta read at the new
   candidate" becomes a mechanism rather than a sentence: a landing after the
   freeze is simply not in the tree the tag points at, and including it means
   a new candidate, a new walk and a read for it. (2026-09-12: the hand-kept
   ledger line "naming the head, the reviewer and the CI run" was this rule
   done by a person.)
3. **The delta is every landing since the last release, and every landing has
   a read or the verb refuses.** `delta` derives the base **from the wire**:
   the newest published, non-draft release's tag, peeled to a commit through
   `git/ref/tags/<tag>` (an annotated tag object, as v0.13.0's is, and a
   lightweight one, as v0.14.0's is, both peel to the commit); `--since
   <tag>` names another. It fetches the repository into its own clone with a
   forcing tag refspec and walks `<base>..<candidate>` **first-parent**: each
   commit on that walk is one landing. A landing's **head** is its second
   parent when it is a merge commit, and otherwise the head oid the host
   records for the pull request associated with that commit
   (`commits/<sha>/pulls`); a landing with neither is a landing nobody can
   have read. For each head the verb looks in the lane's `reads/` fold for an
   approve recorded by a line who is not the pull request's author
   (SPEC-MERGE's read condition, applied unchanged) for exactly that head sha.
   `DELTA OK` prints `landings=<n> read=<n> unread=0`; `unread>0` is `DELTA
   FAIL`, exit 1, and the remedy names each unread landing's `nova-merge read
   --pr <n> --head <sha>` line up to `--max`. Only an `unread=0` walk writes
   the delta record `cut` needs. A read recorded **after** the landing counts:
   a read is of a head, whenever it is recorded — that is what a recorded
   delta read at the new candidate is. (#229; and #144, which "had merged onto
   the stack, not main": a squash onto a stack is not on the default branch's
   first-parent walk and is not a landing here.)
4. **Certification is a run at exactly the candidate sha, read job by job.**
   `certify --workflow <file> --aggregate <job>` asks the host for completed
   runs of that workflow whose `head_sha` **equals** the candidate — never the
   newest run on the branch, because a `workflow_dispatch` on the default
   branch runs at that branch's tip, and after a post-freeze landing the tip is
   not the candidate. It reads the **aggregate job's conclusion by name**, and
   then every job's, and requires `success` of all: `failure`, `cancelled`,
   `timed_out`, `skipped`, `neutral`, `action_required` and a run that is not
   `completed` are each a `CERTIFY FAIL` naming the job and its conclusion. A
   run's own status or a status badge is never read as evidence (2026-09-07:
   "a QUEUED certification run hides a COMPLETED red leg, and five merges
   landed on a red main"). A green writes the certify record with the run id.
   No run at the sha is `CERTIFY FAIL … no completed run at <sha12>`, remedy:
   dispatch the workflow at a ref that resolves to the candidate now, or
   re-freeze at the tip. **A red is stop**: the verb exits 1 and nothing
   downstream can proceed; there is no `--allow-red`. (2026-09-13: run
   34771523657, `build-windows` and `test-windows (internal/swarm)` red at
   9a95e33e, "a leg the fast tier never ran".)
5. **Version sites are a manifest, and the frozen tree already carries the
   new tag.** `--sites <file>` names a tab-separated file kept in git: `path`,
   `template`, `count` (default 1), where `template` contains `{tag}` exactly
   once and is otherwise literal — `docs/USAGE.md<TAB>go install
   example.com/tools/cmd/nova-bus@{tag}` is one line. No grep, no regular
   expression, no discovery: a site the manifest does not name is not a site,
   and a site it names that is missing from the tree is a refusal naming the
   path, because a manifest that has gone stale is the failure a grep would
   have hidden (2026-08-25: a doc pinned `v0.3.0` while `v0.7.0` was current).
   `bump --sites <file> --dir <checkout> --from <tag> --tag <tag>` rewrites
   each site in a checkout the caller names, from the rendering with `--from`
   to the rendering with `--tag`, refusing a site that does not hold exactly
   `count` renderings of `--from`; it commits nothing and pushes nothing, and
   the change lands through the lane like any other, **before** the freeze.
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
   It refuses a `--draft` and a `--prerelease` flag (the first is a release
   nobody can install; the second is a name for shipping before the
   predicate holds); it refuses when a tag of that name exists at **another**
   sha, `CUT REFUSED: <tag> exists at <sha12>, candidate is <sha12>`; and when
   the tag exists at exactly the candidate with no release (the repair path
   for a workflow that half-ran) it creates the release alone and says so,
   `created=release`. There is no verb that pushes a tag without a release,
   and no flag that suppresses the notes. The release workflow's job is then
   to **attach assets to a release that exists**: its `gh release create …
   --generate-notes` path becomes `refusing: no release for $TAG; nova-release
   cut creates the release, this workflow attaches to it` (work list item 9).
   (Glenn, 2026-07-22 and 2026-07-30; release.yml's own comment on v0.11.0.)
7. **The notes are written by a line, from the diff, and read by another
   line.** `--notes <file>` is a Markdown file whose first line is `# <title>`
   and whose remainder is the body. `cut` checks three things it can check:
   the title is not empty and is not the tag or the tag with a prefix (a title
   of `v0.14.0` names nothing a reader can use — both of last night's releases
   have exactly that title); the body is not empty and does not contain the
   host's generated-notes markers (`## What's Changed`, `**Full Changelog**:`),
   because a body a tool wrote is a list of pull request titles, which is
   archaeology by construction; and the lane's `releases/<tag>/` holds a
   **notes read** for the file's current sha256 — `notes --who <name> --notes
   <file> --verdict approve|hold [--note <text>]` writes one immutable record
   keyed to the hash, and a `hold` blocks exactly as a lane hold does. The
   reader answers one question, Glenn's, and the tool cannot: **does every
   sentence say what the reader of this release gets?** The tool prints the
   question on `NOTES OK` so the reader saw it. The author of the notes is
   whoever recorded the candidate or the delta; their own approve is recorded
   and does not count. `delta` prints the exact range so the notes are
   written from the diff, not from the sitting (identity/releases.md,
   2026-09-06: "the notes get drafted in the same sitting as the fold, in the
   fold's register").
8. **`verify` reads the release back and names the first field that
   disagrees.** After `cut`, and after the release workflow has run, `verify
   --platforms <list>` reads, in order: the tag ref, peeled, which must be the
   candidate sha; the release object by tag, which must exist, be neither draft
   nor prerelease, and carry the notes' title and a body whose sha256 is the
   notes body's; the assets, whose count and names must equal the expected set
   — every `cmd/*` directory in the candidate tree times every platform in
   `--platforms`, named `<tool>_<tag>_<goos>_<goarch>[.exe]`, plus
   `SHA256SUMS` (56 for eleven tools on five platforms, which is the number
   #229 measured); and the binary: it downloads this host's build of the tool
   named by `--probe <tool>` into `--work`, checks its line in `SHA256SUMS`,
   runs it with `version`, and requires field two to equal the tag. Each check
   is one `VERIFY` line; the first mismatch is `VERIFY FAIL field=<name>
   expected=<x> found=<y>`, exit 1, and the remaining checks still print (the
   count line prints on failure). A release whose assets are not there yet is
   a `VERIFY FAIL field=assets` naming the workflow run's status; `verify`
   never waits (two-minute rule: a release build is a twenty-minute job, and
   a verb that polls it is a loop with no work). A red release run is re-run
   **on the same tag**; nothing here re-tags. (#118: `devel` under a tag;
   #229: "Attempt 2 on the unchanged tag succeeded … 56 assets".)
9. **Every sha is read from the wire; no verb runs `git tag`.** The previous
   release's sha, the tag's sha, the candidate's reachability and the release
   object come from the host's API or from a clone that fetched
   `+refs/tags/*:refs/tags/*` this run. `git log <tag>..` is never run against
   a tag the clone did not just fetch with a forcing refspec. The one tag this
   tool makes is made by the host inside the release-create call of rule 6.
   (2026-08-31: "a local tag is a cache of a remote ref and the one ref git
   will not correct for you".)
10. **Bounded output, one remedy line, every loop ends.** `delta` and `verify`
    take `--max <n>`, default 20, `0` for all, with one `MORE` line and the
    remedy; every count on an `OK` or `FAIL` line is the truth about the walk
    or the release, never about the listing. Every host and git call is under
    `--timeout <seconds>`, default 120. No verb loops, no verb waits on a run,
    and nothing is written outside `--lane` and `--work`. (Glenn, 2026-09-09:
    bounded output by design; counts not lists.)
11. **Production, then frozen.** The release of this repository that first
    ships `nova-release` with every verb above and the workflow change of rule
    6 is the release after which this tool changes additively only, on the
    same terms as `nova-bus` and `nova-merge` (Glenn, 2026-09-09). Until then
    it is dogfooded on this repository's own releases, and the friends' reads
    are owed before any other repository is asked to use it.

## The verbs

```
nova-release candidate --lane <dir> --tag <tag> --sha <40 hex> [--note <text>]
nova-release delta     --lane <dir> --tag <tag> --work <dir> [--since <tag>] [--max <n>]
nova-release certify   --lane <dir> --tag <tag> --workflow <file> --aggregate <job>
nova-release bump      --sites <file> --dir <checkout> --from <tag> --tag <tag>
nova-release notes     --lane <dir> --tag <tag> --notes <file> --who <name> --verdict approve|hold [--note <text>]
nova-release status    --lane <dir> --tag <tag> --work <dir> --sites <file> --notes <file>
nova-release cut       --lane <dir> --tag <tag> --work <dir> --sites <file> --notes <file>
nova-release verify    --lane <dir> --tag <tag> --work <dir> --platforms <list> --probe <tool> [--max <n>]
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
index is refused). There is no default tag, no default workflow file, no
default aggregate job name, no default platform list and no default probe tool:
each is a fact about one repository that only its owner can state, and the two
lists that `certification.yml` and `release.yml` already hold by hand are typed
here a third time on purpose, so a test can prove the three agree (tests, 8).
The exception is `--timeout`, 120 seconds, for the reason SPEC.md gives.

**`status` is the survey and `cut` is the act, and they read the same
predicate.** `status` prints one line per term of the cut condition and the
verdict, writes nothing and takes no lock; `cut` evaluates the same terms in the
same order and stops at the first that fails with the same line `status` would
have printed, so a `status` that says `ready=true` is a `cut` that will not
refuse on evidence — only on a race, which is the host's answer and not the
tool's.

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
  and D   = a delta record for (tag, S) with unread=0, since=<P>,
            where P is the previous release's sha read from the wire this run
  and R   = a certify record for S whose run concluded success on every job
  and every site in --sites renders tag exactly count times in the tree at S
  and the notes file has a title that is not the tag, a body that is not generated,
      and an approve by a line other than the notes' author for the file's current sha256,
      with no hold for that hash
  and on the wire: no tag named tag, or the tag at exactly S with no release
```

When it holds, `cut` makes one release-create call with `target_commitish=S`,
the title and the body, and prints `CUT OK`. When any term fails, `cut` prints
that term's `FAIL` line and `CUT REFUSED: <the term>`, exit 1, and nothing was
created. A host answer that is neither success nor a refusal it can name — a
502, a rate limit, a dropped connection — is `CUT FAIL … : <the host's first
line>`, exit 1, and the remedy is `nova-release verify`, because a failed create
is read back before it is reported absent (2026-09-13: two creates answered 502
and both objects existed).

**The predicate is keyed to S throughout.** A record for another sha is never
consulted, `status` prints it as `-`, and there is no flag that accepts an older
one. A `delta` record's `since` is re-derived from the wire at `cut` time and
must equal the record's: a release published between the delta and the cut
moves the base, and the walk must be redone.

## Exit codes

| code | meaning |
|------|---------|
| 0 | the verb ran and passed: a record written, a walk with `unread=0`, a certification read green, a release created, a release verified field for field |
| 1 | the verb ran and said **NO**: an unread landing, a red or missing certification, a site that does not render the tag, notes without a read or with a hold, a `cut` whose predicate does not hold or whose tag exists elsewhere, a `verify` with a field that disagrees |
| 2 | could not run: missing flag, a `--lane` that is not a lane, a manifest or notes file that does not parse, `gh` or `git` absent, a host answer that was neither yes nor no |

## Output grammar

One line per event; first token is the verb, second is `OK`, `FAIL`,
`REFUSED` or an informational token listed here. `OK` and informational lines
go to stdout; `FAIL` and `REFUSED` go to stderr. Every path, title, subject,
job name, reason and asset name renders through `internal/oneline`, and every
`key=value` field carrying stored text is a single token by SPEC.md's field law.

```
CANDIDATE OK tag=<tag> sha=<sha12> base=<branch> supersedes=<sha12|-> file=<path> pushed=true
CANDIDATE FAIL tag=<tag> sha=<sha12> file=<path> pushed=false: <reason>; re-run the same verb to push it
CANDIDATE REFUSED: <reason>
DELTA ENTRY commit=<sha12> pr=<n|-> head=<sha12> author=<name|-> read=<who,who|-> : <subject>
DELTA MORE kind=landing shown=<n> total=<t> nova-release delta … --max 0
DELTA NOTE range=<P12>..<S12> — write the notes from this diff, not from the sitting
DELTA OK tag=<tag> since=<P12> candidate=<S12> landings=<n> read=<n> unread=0 file=<path> pushed=true
DELTA FAIL tag=<tag> since=<P12> candidate=<S12> landings=<n> read=<n> unread=<n>: <n> landings have no read; nova-merge read --lane <dir> --pr <n> --who <you> --head <sha> --verdict approve|hold
DELTA REFUSED: <reason>
CERTIFY JOB run=<id> job=<name> conclusion=<word>
CERTIFY OK tag=<tag> sha=<S12> workflow=<file> run=<id> jobs=<n> aggregate=<job> file=<path> pushed=true
CERTIFY FAIL tag=<tag> sha=<S12> workflow=<file> run=<id|-> job=<name|-> conclusion=<word|->: <reason>
CERTIFY REFUSED: <reason>
BUMP SITE <path>: <count> renderings of <from> became <tag>
BUMP OK sites=<n> from=<tag> tag=<tag> dir=<checkout>
BUMP FAIL <path>: <reason>
BUMP REFUSED: <reason>
SITES OK sites=<n> tag=<tag> at=<S12>
SITES FAIL <path>: <reason>
NOTES OK tag=<tag> who=<name> verdict=<approve|hold> hash=<sha256:12> title=<title> file=<path> pushed=true: does every sentence say what the reader of this release gets?
NOTES FAIL <path>: <reason>
NOTES REFUSED: <reason>
STATUS TERM term=<candidate|delta|certify|sites|notes|wire> ok=<true|false> detail=<one token or ->
STATUS OK tag=<tag> candidate=<S12|-> delta=<unread|-> certify=<run|-> sites=<n|-> notes=<approve|hold|->  wire=<absent|tag-only|released> ready=<true|false>
CUT OK tag=<tag> sha=<S12> created=<tag+release|release> title=<title> url=<url>
CUT REFUSED: <the failing term's line>
CUT FAIL tag=<tag> sha=<S12>: <the host's first line>; nova-release verify before reporting anything
VERIFY FIELD field=<tag|release|title|body|assets|asset|checksum|version> expected=<x> found=<y> ok=<true|false>
VERIFY MORE kind=asset shown=<n> total=<t> nova-release verify … --max 0
VERIFY OK tag=<tag> sha=<S12> assets=<n> probe=<tool> version=<field two> run=<id|->
VERIFY FAIL field=<name> expected=<x> found=<y>: <reason>
VERIFY REFUSED: <reason>
<VERB> REFUSED: <reason>
```

`DELTA ENTRY` is capped at `--max` and `DELTA OK`/`FAIL` counts the walk, not
the lines. `DELTA NOTE` prints the range on every completed walk, pass or fail,
because the notes are owed either way and the diff is where they come from.
`VERIFY FIELD` lines print for every field even after the first `false`, so a
reader learns everything that is wrong in one run; `VERIFY FAIL` names the
**first** one. `CERTIFY JOB` prints only jobs whose conclusion is not `success`,
capped at `--max`; a green run prints none and `jobs=<n>` says how many were
read.

## The records

Everything this tool records is one immutable file under
`<lane>/releases/<tag>/`, tracked and pushed to the lane branch through the
outbox and compare-and-swap loop of SPEC-MERGE rule 22, under
`<lane>/checkout.lock`. The names:

```
<lane>/releases/<tag>/candidate-<sha12>-<at>-<rand6>.json   {tag, sha, base, who, at, note, supersedes}
<lane>/releases/<tag>/delta-<sha12>-<at>-<rand6>.json       {tag, sha, since, since_tag, landings, read, unread, entries:[{commit, pr, head, read:[who]}], at}
<lane>/releases/<tag>/certify-<sha12>-<at>-<rand6>.json     {tag, sha, workflow, run, aggregate, jobs, at}
<lane>/releases/<tag>/notes-<hash12>-<at>-<rand6>.json      {tag, hash, who, verdict, note, title, at}
<lane>/releases/<tag>/cut-<sha12>-<at>-<rand6>.json         {tag, sha, url, title, created, at}
```

`nova-merge` folds `reads/` and `gates/` and walks nothing else, so a lane with
a `releases/` directory is, to `nova-merge`, the lane it was. The one line
`nova-merge` might one day gain from these files — a `candidate=<sha12>` on
`STATUS OK` — is an additive change to SPEC-MERGE and is **not made by this
spec**; it is named in the work list so it is not lost. A record file that does
not decode is refused by name, never skipped, exactly as a lane record is. No
record is ever edited or replaced: a re-freeze is a second candidate file, a
second walk is a second delta file, and the newest by `at` wins, ties folding
toward the refusing verdict (a `hold` after an `approve` with one `at` holds).

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
- **It does not read a whole tree.** #229 asks for "a whole-repo read of the
  candidate" as part of the gate; this tool checks the per-landing reads the
  lane already records, and a whole-tree read of the candidate is a
  coordinator's decision recorded, today, as a hold or approve on the notes.
  Whether it becomes a term of the cut condition is an open question for
  draft 2.
- **It does not decide the version number.** `--tag` is typed by the person
  who owns the line, every time.

## Tests this spec demands

One line per rule. Each is a test the work list builds, and each must be seen
red before it is trusted.

1. `candidate` on a sha the fake host does not hold under the base refuses;
   on a good sha it writes one file whose name carries `sha12`, pushes it to
   the lane branch, and a second `candidate` at another sha prints
   `supersedes=` and leaves the first file untouched; `status` then names the
   newer.
2. A `delta` and a `certify` recorded for S, then a re-freeze at S′: `status`
   prints `delta=- certify=-`, `cut` refuses naming both, and the two old
   records are still in the branch.
3. A fixture history with three landings — a two-parent merge, a squash the
   fake host associates with a pull request, and a direct commit with neither
   — and reads for the first two: `delta` prints `unread=1` naming the third,
   exit 1, writes no record; add a read for it and the walk writes a record
   with `landings=3 read=3 unread=0`; a read by the pull request's own author
   does not count; a squash onto a branch that is not the base is not a
   landing; the base P is taken from the fake host's release, not from a
   local tag planted at another sha.
4. A run at the candidate whose aggregate is `success` but one matrix leg is
   `failure` is `CERTIFY FAIL` naming the leg; a newest run at the tip while
   the candidate's run is red is red; a run `in_progress` is not evidence;
   `queued` with one completed red leg is red naming the leg; a run at another
   sha is `no completed run at <sha12>`; a green run writes the record with
   its id.
5. A manifest with a `{tag}` twice, or none, refuses at load; `bump` on a
   site holding two renderings of `--from` where `count` is 1 refuses naming
   the path; a good bump rewrites exactly the manifest's lines and nothing
   else (a byte diff of the checkout); `status` at a candidate whose site
   still reads the old tag is `SITES FAIL <path>`; a manifest naming a path
   absent from the tree refuses.
6. `cut` with `--draft` or `--prerelease` refuses before any host call; with
   a tag existing at another sha refuses naming both shas; with the tag at S
   and no release creates the release alone and prints `created=release`; the
   fake host records exactly one create call carrying `target_commitish=S`,
   the title and the body; a 502 from the create is `CUT FAIL` naming
   `verify`; a source test finds no call site that runs `git tag` or
   `git push … refs/tags`.
7. Notes titled `v0.15.0`, or `Release v0.15.0`, refuse; a body containing
   `## What's Changed` refuses; notes with no read refuse; a `hold` blocks
   while an older `approve` exists; an approve by the candidate's recorder
   does not count; editing one byte of the notes after the read makes
   `status` print `notes=-` because the hash moved.
8. `verify` against a fake release with 55 assets names the missing one;
   with a tag peeled to another sha fails `field=tag`; with a body whose hash
   differs fails `field=body`; with a probe binary printing `devel` fails
   `field=version`; with a wrong `SHA256SUMS` line fails `field=checksum`;
   every `VERIFY FIELD` line prints even after the first false; and a test
   reads `release.yml`'s and `certification.yml`'s platform lists as data and
   proves them equal to the `--platforms` example in the usage banner.
9. Every sha the tool acts on is traced by a test to a host call or to a
   fetch with `+refs/tags/*:refs/tags/*` in the same run; a local tag planted
   in `--work` at a wrong sha does not change any verb's answer.
10. A fixture delta of 50 landings prints 20 `DELTA ENTRY` lines, one `DELTA
    MORE`, and `landings=50`; a host that never answers is cut at `--timeout`
    with exit 2 naming the call; nothing is written outside `--lane` and
    `--work` (a watched temp root stays empty).
11. The usage banner ends in a runnable `example:` block, and
    `TestExecutableFirstRun` runs it, as every binary's does.

## The work list

To build it in Go under `cmd/nova-release`, the way `cmd/nova-merge` is built:
standard library only, `internal/oneline` for every printed value,
`internal/bounded` for every listing, `internal/buildinfo` for `version`, and
`internal/merge`'s record and lock code **reused, not copied**, for the outbox,
the compare-and-swap push and the checkout lock.

1. **`internal/release/records.go`** — the five record shapes, strict decode,
   newest-by-`at` with refusing-verdict-last, written through
   `internal/merge`'s outbox loop under the lane's checkout lock. Tests 1, 2,
   7.
2. **`internal/release/wire.go`** — the host reads: peel a tag, read a release
   by tag, list assets, list workflow runs by `head_sha`, read a run's jobs,
   associate a commit with its pull request, read a pull request's author and
   head; every answer's status read before its body (release.yml's `ask`
   pattern); the one host write, release-create with `target_commitish`.
   Tests 4, 6, 8, 9.
3. **`internal/release/delta.go`** — the clone in `--work`, the forcing tag
   fetch, the first-parent walk, the head resolution and the read lookup in
   the lane's `reads/` fold. Test 3.
4. **`internal/release/sites.go`** — the manifest, `bump`, and the check at
   a tree. Test 5.
5. **`internal/release/notes.go`** — title and body checks, the hash, the
   notes read record. Test 7.
6. **`internal/release/cut.go`** — the predicate as one function `status` and
   `cut` both call, with `cut` acting on `ready=true` only. Test 6.
7. **`internal/release/verify.go`** — the field walk, the download of the
   probe binary into `--work`, the checksum check, running `version`. Test 8.
8. **`cmd/nova-release/main.go`** — the verbs, refusals naming every
   independent problem at once, the banner with its `example:` block, the
   `### First run` in `docs/CLI.md` and a row in `docs/USAGE.md`. Tests 10,
   11.
9. **`.github/workflows/release.yml`** — the create path becomes a refusal;
   the upload path is unchanged; the comment that explains the v0.11.0
   accident is rewritten to say the order is now designed. This is the one
   edit outside `cmd/` and `internal/`, and it lands in the same release as
   the binary (rule 6).
10. **`docs/SPEC.md`** — one paragraph under the companion-spec list naming
    `RELEASE`'s tokens: `CANDIDATE`, `DELTA`, `CERTIFY`, `BUMP`, `SITES`,
    `NOTES`, `STATUS`, `CUT`, `VERIFY`, with `ENTRY`, `JOB`, `SITE`, `TERM`,
    `FIELD`, `NOTE` and `MORE` informational.
11. **Named, not done here:** an additive `candidate=<sha12>` on `nova-merge
    status` read from `releases/`, for SPEC-MERGE, after this tool has cut one
    release; and whether a whole-tree read becomes a term of the cut
    condition (#229, point 6).

## Open questions for the reads

- Should the candidate also be a ref on the wire (`refs/heads/<prefix>/<tag>`
  at S), so certification can be dispatched at the candidate after the tip has
  moved? Draft 1 says no: the lane file is on the wire, the ordinary flow
  dispatches at the tip the moment it is frozen, and a branch this tool creates
  is a mutation with a trigger surface. The re-freeze covers the other case.
- Should `delta` also require a read for the **candidate as a whole** (#229's
  whole-repo read), recorded as a read record whose head is S? It would make
  the tree-nobody-read-whole hurt a mechanical term. Draft 1 leaves it to the
  notes read and asks.
- Is `--probe <tool>` the right shape for the binary check, or should `verify`
  run every tool for this host (eleven downloads, well within two minutes)?
