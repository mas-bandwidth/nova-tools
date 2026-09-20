RESULT: s17rb-969 sha=7c29513b0e8f

**BLOCKED**

The bench never provisioned the source material for this card. STEP 1 could not be started, so there
is no rebase, no inventory, and no resolution.

What exactly stopped me:

1. **No `repo/` clone.** The task says "the base is the clone in `repo/`", but `repo/` does not exist
   in the job directory (`jobs/card-s17rb-969/`) or anywhere under the workspace root. The job
   directory contains only `.lease`, `.nova-sandbox-tmp/`, `harness-output.log`, `opencode.json`, and
   a freshly `git init`'d empty repo (branch `main`, zero commits, zero refs, zero objects,
   `git count-objects` = 0/0). `git rev-parse HEAD` → `fatal: ambiguous argument 'HEAD'`.

2. **Bundles are outside the sandbox.** The base and head are supposed to live at
   `/tmp/schema14-ftf.bundle` and `/tmp/s17-heads.bundle`, but the sandbox denies all of `/tmp`:
   `ls /tmp` → `Operation not permitted`, and the same for `/private/tmp`. The sandbox argv confirms
   why: the read list is the card dir, `harness-v1.18.20`, `sdk`, `go/pkg/mod`, and the JVM dir — `/tmp`
   is not readable and the bundles were never copied into any writable path.

3. **No copies anywhere else.** A full search of every accessible read path
   (`/Users/nova/rowan-working`, `/Users/nova/nova-bench`, `/Users/nova/sdk`, `/Users/nova/.local`)
   found no `*.bundle` files, no `schema14*`, no `s17-heads*`, and no `repo`/`fixed-table-form`/
   `ordinal-lands` directory. The writable `tmp/card-s17rb-969` dir is empty. The three required
   commits are not present as git objects: `git cat-file -t` for base `7c29513b0e8f`, head
   `21071a6ab5ca`, and merge-base `b119986e6251` all fail.

Consequently none of the base sha (7c29513b0e8f...), the head, or the merge base exists on the bench,
so STEP 2 (what the PR is for), STEP 3 (conflict inventory), STEP 4 (resolution), and STEP 5 (proof +
harness) are all impossible. This is the bench's fault, not the PR's — the same category as the
BLOCKED-TOOLCHAIN gate. The rebase work (branch `rebase-work`) was never created because there was
nothing to fetch; no branch was left, nothing was pushed.

Recommendation for the manager: re-run this card with the base clone present in `repo/` and the head
bundle readable (or copied into the sandbox's read/write paths). Nothing about this PR was evaluated.