RESULT: s17mr-1087 sha=7c29513b0e8f

**CANNOT READ**

The diff below the `--- THE DIFF (PR #1087, head c4df86e3f4c27ea5271b5a0b025668380e926b95) ---` heading is entirely empty: the ```diff fence opens and closes with no content between, so there are no files, no hunks, and no claims to read. I needed the whole diff — every file and every hunk of PR #1087 — and none was provided.

STEP 1 also cannot be run: the card asserts the base tree lives in `repo/` at 7c29513b, but `repo/` does not exist in the job directory (`ls repo` → "No such file or directory"), the job's own `.git` has no commits (`git rev-parse HEAD` → "unknown revision"), the bundle `/tmp/schema14-ftf.bundle` is unreadable under the sandbox ("Operation not permitted"), and there is no network. So `docs/FIXED-FORM-ALGORITHM.md`, `docs/SPEC.md`, `docs/PORTING.md`, the matrix, and the make gates are all unavailable for verification.

Because the diff is absent rather than merely truncated at a specific file/hunk, I cannot name a single offending hunk; the entire PR diff is the missing material.