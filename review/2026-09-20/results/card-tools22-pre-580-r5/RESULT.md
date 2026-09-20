RESULT tools22-pre-580-r5 sha=5298f6be12ea — a pre-read of mas-bandwidth/nova-tools#580 at head 5b5919945c52: Add a short source-linked terminology guide
ABSTAIN environment missing: the bench did not stage this PR. /tmp/nova-tools-mirror.git does not exist in the sandbox (git fetch: fatal: '/tmp/nova-tools-mirror.git' does not appear to be a git repository; ls /tmp and /private/tmp: Operation not permitted), and <JOBDIR>/repo was never created — the job clone has no refs, no objects, no index and no commits. No reading of PR 580's diff is possible.
PREREAD 580 claims=0 proven=0 unproven=0 defects=0 high=0
PR 580 — HEAD 5b5919945c52b580a845391177340b8a3a80bf3b — BASE dev — MERGE-BASE unknown — BEHIND unknown — FILES 0 production, 0 test — LINES +0 -0

WHAT I CHECKED BEFORE ABSTAINING
1. STEP 1 verbatim: `git fetch -q /tmp/nova-tools-mirror.git refs/pull/580/head:refs/tmp/pr580` -> fatal, mirror not a git repository; `git rev-parse refs/tmp/pr580` -> no such revision. The head cannot be obtained, so the card's own precondition (read the head from the bench-local mirror) fails.
2. `ls repo` in the job directory: no such directory. There is no staged worktree at the pinned base anywhere under the job directory.
3. The job git dir `.git/` is a fresh empty init: `git branch -a` empty, `git count-objects -v` all zeros, no `.git/index`, no refs, no packed-refs content, `git rev-parse HEAD` fatal. Nothing is staged.
4. Filesystem search (job tree and parent tmp tree): no `nova-tools` checkout, no `*mirror*` path, no pack files other than the opencode harness snapshot's own tiny packs (a few hundred bytes each: opencode config blobs, verified via `git verify-pack`), no PR refs.
5. The one gitdir that does exist alongside the job — the harness snapshot gitdir whose config names this job directory as its worktree — contains only `.lease`, `harness-output.log`, `opencode.json` (its index), i.e. the harness's own bookkeeping, not the repository.
6. Network: sandbox is `net=nopromise` and the card states there is no network inside the wall; no fetch from upstream is possible by design.

CLAIMS
None — no diff, no commit messages, no changed files were readable, so no claims could be enumerated or witnessed. Every line of STEP 3 (the claims) is therefore empty rather than `UNPROVEN`: there is no diff at all to judge.

DEFECTS
None — no code was readable, so there is nothing to point at. I am not reporting "no defects" as a judgment on PR 580; it is a statement about this run's input, which was empty.

QUESTIONS FOR THE REVIEWER
1. Was the mirror `/tmp/nova-tools-mirror.git` and the staged `repo` supposed to be provisioned into the sandbox before this card ran, and did that step fail here? The `ls /tmp` "Operation not permitted" suggests the sandbox read whitelist did not include the mirror path.
2. Should this card be re-run on a correctly staged bench, or is an ABSTAIN the accepted outcome when the bench provisioning fails?
3. If a human reviewer has local access to `mas-bandwidth/nova-tools` PR 580 (head 5b5919945c52b580a845391177340b8a3a80bf3b vs base dev@5298f6be12ea), is a fresh pre-read wanted rather than treating this card as closed?

Left owed
Everything. The entire diff of PR 580, its commit messages, and all production and test files were unreadable because the mirror and staged repo were not present in the sandbox and no network exists. I searched rather than assumed: verified the mirror path is unreachable from both `/tmp` and `/private/tmp`, confirmed the job clone is an empty init with zero objects, and inspected the only gitdir in the tree (harness snapshot) to rule out that the repository was staged there.

`git status --short` (it must print nothing; here the harness bookkeeping files are untracked because the repo was never staged):
?? .lease
?? .nova-sandbox-tmp/
?? harness-output.log
?? opencode.json

`git rev-parse HEAD`:
fatal: ambiguous argument 'HEAD': unknown revision or path not in the working tree.