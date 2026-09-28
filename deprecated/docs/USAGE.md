# Deprecated tools: the usage entries they had

### nova-play — notes beside a shared text

**Development branch:** `nova-play` is not part of `v0.15.2`.

**Try it when** two people or AI friends want to leave questions, comments and
replies beside the same passages of a story, essay or specification.

**What it does.** Anchors each note to an exact passage and the source file's
hash. Notes carry their author's name and live in `<source>.notes` beside the
original plain text. The tool leaves the original text unchanged.

**First trial.** Make a disposable `story.txt` containing `A shared passage.`
and run:

```sh
nova-play annotate --source story.txt --author Reader --passage 'A shared passage.' --note 'What does this suggest?'
nova-play read --source story.txt
```

`ANNOTATE OK` returns a note ID. To answer it, pass that ID to
`nova-play reply --source story.txt --id <returned-id> --author Friend --body 'My reply.'`,
replacing `<returned-id>` with the actual ID. Read again to see the reply.

**It worked if** the note and reply return with the right words and authors,
and the original file has not changed.

**Limits and side effects.** This is local storage: it makes no model call and
does not share files or notify another participant. Agree separately how to
exchange the source and its sidecar. PDFs and EPUBs are not supported.

The current development build has [known round-trip and stale-reply defects](https://github.com/mas-bandwidth/nova-tools/issues/1542).
For now, use disposable copies, filenames without spaces and single-line notes;
do not add replies after editing the source. Reading a changed source reports
`ANCHOR STALE`, but replies do not yet enforce that check. Keep original notes
elsewhere until the data-preservation defects are repaired.

### nova-swarm — more work at once

**Try it when** you have bounded, independent jobs and workers configured to run
them, and doing them one after another is what is slowing you down.

**What it does.** Runs tasks in parallel using AI workers you configure, with
deadlines, collected results and usage accounting where the source supports it.

**You need** a pool directory, a worker description naming whose model runs, and
a harness and provider setup that actually works. **Every job runs inside
`nova-sandbox` on every supported platform**: `run` proves the wall once before
the first worker and refuses to start without a usable sandbox unless you
explicitly pass `--no-sandbox`, **which provides no containment at all**. macOS
uses `sandbox-exec`; Linux uses Landlock when the running kernel supports it.
Windows has no containment backend yet.

**First trial.** `nova-swarm quickstart --pool <dir>` makes the pool structure
and names the commands that follow, without running a worker or spending a
token. See the
[first-run transcript](TESTS.md#nova-swarm) and
[nova-swarm in the command reference](CLI.md#nova-swarm).

**It worked if** several jobs finished inside their deadlines and you could read
each result and the evidence behind it.

**Limits and side effects.** It runs other programs, writes job directories, and
spends real tokens once workers start. A worker exiting `0` means the process
succeeded, **not** that the requested work is complete — read the evidence. A
free worker helps only if its capabilities fit the task. The development branch
adds `nova-sandbox run` on macOS; it is not in `v0.15.2`, and its Linux form
refuses. On macOS, starting it from inside an existing sandbox may fail while
creating its APFS volume because the outer wall does not permit the mount. Start
the disposable volume from outside the existing wall; retrying the same nested
command does not grant the missing mount access.

**It may not help if** your work is mostly sequential, or you have no worker setup
to point it at yet.

### nova-merge — a controlled queue for landing work

**Try it when** changes land out of order, or land without the review and the
tests they were supposed to have had.

**What it does.** Checks reviews and tests before merging changes in order.

**You need** the lane's own directory (`--lane`), the repository it lands into
(`--repo <owner>/<name>`), the branch entries are merged onto (`--base`), the
lane's own branch (`--lane-branch`), `git`, and `gh` with access you already
have. `quickstart` names each missing one rather than assuming it.

**Read this before the first trial: `init` and `quickstart` create and PUSH the
lane branch.** They are not read-only. So rehearse against a bare repository of
your own, with an absolute `--remote` and a lane directory of its own, which
keeps the whole first run off any forge:

```sh
git init -q --bare ./rehearsal.git
nova-merge quickstart --lane ./rehearsal-lane --repo mas-bandwidth/nova-tools --base main \
           --lane-branch nova-merge/main --remote "$PWD/rehearsal.git"
```

The [command reference](CLI.md#nova-merge) carries this and what each verb
pushes; the [first-run transcript](TESTS.md#nova-merge) is executed by a test. See
also
[nova-merge in the command reference](CLI.md#nova-merge).

**Development branch.** The `nova-pulse` and `nova-work` commands in the next
two paragraphs are not part of `v0.15.2`.

**How work lands here today, in case it is useful.** We stopped landing one pull
request at a time. A **batch** is a handful of pre-tested changes merged onto one
tree, built and tested there, and then opened as a single entry that carries the
list of what is in it; the queue round lands the batch, and the members are closed
with a pointer to it. What makes that affordable is asking first: `nova-merge
simulate --repo <clone> --base <branch>` squash-merges the queue's entries in
order in a scratch worktree, runs your checks after each successful merge, and
reports the growing batch's first failing step while skipping conflicts. It does
not separately prove that entry green on its own. The other
half is upstream of the merge: a card that touches one area of the code declares a
**lane** with a `LANE: <name>` line, and `nova-pulse fill` keeps at most one card
per lane live at a time and holds the rest in order, so two workers do not spend an
afternoon writing changes that cannot both land. Neither half is required to use
`nova-merge`; both are how the lane stays cheap once there is more work than
reviewers.

`nova-merge batch` is the local landing gate: it builds the combined branch and
runs the repository's build, vet, Go and Lisp checks without pushing. After the
batch passes its required review and checks, `queue` records holds, skips and
ordering. `rebase` cuts bounded repair cards. Teams that already use Redis can
connect `nova-work events` to `nova-merge react`; the ordinary merge lane does not
require Redis or a resident loop.

**It worked if** it refused to land something whose checks had not passed, and
told you exactly which condition was missing.

**Limits and side effects.** It writes to a repository and can merge. It ties
validation to named revisions, so a gate proven on one revision does not vouch
for a different one.

**It may not help if** one of you lands everything anyway, or your forge already
enforces this for you.
