RESULT tools22-pre-865-r3 sha=5298f6be12ea — a pre-read of mas-bandwidth/nova-tools#865 at head 49aa717c4342: pulse: failure by cause, tools file their own edges, the cut stamp, no "whole" (classes I, J, P
ABSTAIN environment not staged: <JOBDIR>/repo does not exist and /tmp/nova-tools-mirror.git is absent (the wall refused /tmp), so the PR head 49aa717c4342a99b4f82e74c23a5da89fbcffa2c could not be fetched or verified and there is no diff to read

PR 865 HEAD 49aa717c4342a99b4f82e74c23a5da89fbcffa2c (card-stated, unverified) BASE dev MERGE-BASE unknown BEHIND unknown FILES 0 production, 0 test LINES +0 -0

No CLAIMS, DEFECTS, or QUESTIONS are written: nothing was read. Manufacturing them would be worse than none.

What was tried, and what each step produced:

1. `cd repo && git fetch -q /tmp/nova-tools-mirror.git refs/pull/865/head:refs/tmp/pr865 && git rev-parse refs/tmp/pr865`
   -> `/bin/bash: line 0: cd: repo: No such file or directory`. There is no `repo` subdirectory anywhere under the job root (searched).
2. `git fetch -q /tmp/nova-tools-mirror.git refs/pull/865/head:refs/tmp/pr865`
   -> `fatal: '/tmp/nova-tools-mirror.git' does not appear to be a git repository` (exit 128). The mirror does not exist.
3. `ls /tmp/nova-tools-mirror.git`, `cat /tmp/nova-tools-mirror.git/HEAD`
   -> `ls: /tmp/: Operation not permitted`; `No such file or directory`. `/tmp` is outside the sandbox's read set.
4. `launch.out` (card root) records the staging failure at the wall:
   `SECRETS EXEC OK as=studio ... cmd=/Users/glenn/.local/bin/nova-swarm`
   `WALL REFUSED denied /tmp/ task=card-tools22-pre-865-r3 step=-`
   The mirror was to be staged at /tmp/nova-tools-mirror.git (per card `base-repo:`); the wall refused /tmp, so neither the mirror nor the repo clone was provisioned.
5. The job's own `.git` is an empty repository (no objects, no refs; `git rev-parse HEAD` -> `fatal: ambiguous argument 'HEAD'`). No source tree exists in the job at all.
6. No network exists inside the wall (sandbox `net=nopromise`), so there is no fallback path to the PR.

The head SHA on this card (49aa717c4342a99b4f82e74c23a5da89fbcffa2c) is reported from the card text only; it could not be confirmed or refuted. A re-run of this card with the mirror staged (or with /tmp granted to the sandbox) is required before any reading can be produced.

Left owed: everything — the diff, the commit messages, the production files, the test files — none of it existed to read.

git status --short:
?? .lease
?? .nova-sandbox-tmp/
?? harness-output.log
?? opencode.json

git rev-parse HEAD:
fatal: ambiguous argument 'HEAD': unknown revision or path not in the working tree.