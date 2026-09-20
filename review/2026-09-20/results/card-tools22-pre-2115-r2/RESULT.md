RESULT tools22-pre-2115-r2 sha=5298f6be12ea — a pre-read of mas-bandwidth/nova-tools#2115 at head 111e48c81139: nova-secrets: restore the store after seal --no-pr (#2016)
ABSTAIN environment unusable: <JOBDIR>/repo does not exist, mirror /tmp/nova-tools-mirror.git is absent and /tmp/ is refused by the sandbox wall (launch.out: "WALL REFUSED denied /tmp/"), and net=nopromise forbids fetching; head 111e48c81139e72e5bf2b323e74c2aaa338c71b2 could not be verified and no diff could be read.

PR 2115
HEAD 111e48c81139e72e5bf2b323e74c2aaa338c71b2 (unverified — fetch impossible)
BASE dev
MERGE-BASE unknown
BEHIND unknown
FILES 0 production, 0 test (none read)
LINES +0 -0

CLAIMS: none. STEP 1 could not be executed, so STEP 2's diff, STEP 3's claims and STEP 4's defects are all empty. This is not a reading of the PR.

DEFECTS none (nothing read; see note below)

QUESTIONS
1. Was this job's staging step expected to materialise <JOBDIR>/repo at the pinned base 5298f6be12eaa0f7e6622334d2b6a1eb427649e3? The directory is absent; the job's own .git has no commits, no refs and no objects.
2. Is /tmp/nova-tools-mirror.git meant to be pre-staged by the harness? launch.out shows the wall refusing /tmp/ ("WALL REFUSED denied /tmp/ task=card-tools22-pre-2115-r2"), and `ls /tmp/` returns "Operation not permitted" inside the sandbox.
3. Should this card have been cut with a mirror path inside the sandbox's read set, or should the staged clone have been dropped at the job root?

Left owed
- Everything. <JOBDIR>/repo (the staged nova-tools clone at base 5298f6be12eaa0f7e6622334d2b6a1eb427649e3) does not exist.
- The bench-local mirror /tmp/nova-tools-mirror.git carrying refs/pull/2115/head is not present and /tmp/ is blocked by the wall, so the head check, merge-base, diff --stat, git log and full diff of STEP 2 could not run.
- Searched (readable) for nova-tools objects: the job .git (empty), data/opencode/snapshot (only opencode's own empty data-dir repo), /Users/glenn/rowan-working/tmp/cache, the harness dir, sdk and go caches. No nova-tools git objects, packed-refs or pack files referencing PR 2115 were found.

`git status --short` (job root, the only git in the workspace): prints nothing on stdout; `git rev-parse HEAD` fails ("ambiguous argument 'HEAD': unknown revision or path not in the working tree"), which is itself the evidence that the staged repo was never materialised — the job .git is an empty, commit-less repository. Both command outputs are pasted in harness-output.log.