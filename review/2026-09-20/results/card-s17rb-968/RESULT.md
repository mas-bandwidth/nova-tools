RESULT: s17rb-968 sha=7c29513b0e8f

**BLOCKED** — the bench never provisioned the two trees this card needs, so there is nothing to orient, inventory, or rebase.

What stopped me, exactly:

1. **No base clone.** The task says "the base is the clone in `repo/`", but `repo/` does not exist in the job directory (`/Users/nova/rowan-working/tmp/76d3a108-0c5f-4b92-a974-c81430401367-card-s17rb-968/jobs/card-s17rb-968`). A full recursive search of the whole sandbox found no `repo/` and no git bundle anywhere.
2. **The bundles are unreachable.** `git fetch /tmp/s17-heads.bundle refs/s17/rowan/twin-guard-chain` and `git fetch /tmp/schema14-ftf.bundle` both fail: `fatal: '/tmp/s17-heads.bundle' does not appear to be a git repository` (exit 128). `ls /tmp/schema14-ftf.bundle` → `Operation not permitted`. The sandbox profile (from `native-argv.log`) grants read only under the job dir, the harness, the SDK, the Go module cache, Java, and dotnet; `/tmp` is outside it and the two bundles are not present in any readable location.
3. **The working git repo is empty.** `git rev-parse HEAD` fails ("unknown revision"), `git status` reports "No commits yet", `git count-objects` reports 0 objects / 0 packs, and `.git/refs` is empty. The base sha `7c29513b0e8f42f457189d1bec23e1de475d4ed8` exists in no object store on this bench.
4. **No network, no `gh`.** Confirmed per the card; there is no way to fetch either tree from outside the wall.

Consequences:
- STEP 1 (`git rev-parse HEAD` must be `7c29513b...`) could not even begin; I could not print a head sha, so line 1 carries the task-provided base sha as the card identifier.
- STEP 2 (read the PR's commits / diff against the merge base) is impossible — no commits, no diff.
- STEP 3 (inventory conflicts) is impossible — `git checkout -B rebase-work FETCH_HEAD` has no `FETCH_HEAD` and no objects.
- No `rebase-work` branch was created (nothing exists to branch from); nothing was pushed.

This is the bench's fault, not PR #968's. If the manager can re-provision the base clone and the `s17-heads.bundle`, the recon can proceed as specified.