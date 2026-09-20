RESULT tools22-pre-2118-r2 sha=5298f6be12ea — a pre-read of mas-bandwidth/nova-tools#2118 at head bb5553af4698: nova-review mutate --seed: isolate git/go children from the gate process (#1897)
ABSTAIN staged mirror absent: /tmp/nova-tools-mirror.git does not exist, job repo has 0 objects/refs and an unborn HEAD, so head bb5553af4698268ef9f398b4ebedb377f82caf0d could not be fetched or verified and no diff was available to read.

The card's precondition — "repo is already staged at the pinned base, and the mirror carries every PR ref" — is not met. What was checked, from the job directory:

- `git fetch -q /tmp/nova-tools-mirror.git refs/pull/2118/head:refs/tmp/pr2118` fails: "fatal: '/tmp/nova-tools-mirror.git' does not appear to be a git repository" (exit 128).
- `git for-each-ref` prints nothing; `git count-objects -v` reports count 0, packs 0; `.git/objects/pack/` is empty; `.git/config` has no remote; `git rev-parse HEAD` fails ("ambiguous argument 'HEAD'"), so the tree is unborn.
- No `nova-tools-mirror.git` exists anywhere findable under /tmp or the workspace (searched to depth 7), so this is not a path typo.
- `net=nopromise` in harness-output.log and the card's own statement ("There is no network inside the wall") rule out fetching the PR head from GitHub to re-stage; the RULES forbid going outside the job directory for this.

A pre-read of PR 2118 at head bb5553af4698268ef9f398b4ebedb377f82caf0d is therefore impossible from this environment: there are no commits, no PR ref, no diff, and nothing to attest. No CLAIMS, DEFECTS or QUESTIONS are asserted because none could be derived from data — every line of such a section would be fabrication.

PR 2118: UNREAD — no staged mirror or objects
HEAD: UNVERIFIABLE — expected bb5553af4698268ef9f398b4ebedb377f82caf0d, could not be fetched
MERGE-BASE: none (no objects)
BEHIND: unknown
FILES: 0 production, 0 test
LINES: +0 -0

CLAIMS: none — no diff available to read
DEFECTS: none — no diff available to read (the environment failure above is a staging defect, not a PR defect)
QUESTIONS: none — no diff available to read

Left owed: everything. The entire reading (STEP 1 head verification, STEP 2 diff, STEP 3 claims, STEP 4 defects, STEP 5 questions) is owed because the card's staged mirror `/tmp/nova-tools-mirror.git` and the pinned-base repo are absent from this job. Recommend re-cutting the card with the mirror staged at `/tmp/nova-tools-mirror.git`.

git status --short:
?? .lease
?? .nova-sandbox-tmp/
?? harness-output.log
?? opencode.json

git rev-parse HEAD:
fatal: ambiguous argument 'HEAD': unknown revision or path not in the working tree.
Use '--' to separate revisions from paths, like this:
'git <command> [<revision>...] -- [<file>...]'