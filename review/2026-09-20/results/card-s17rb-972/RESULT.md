RESULT: s17rb-972 sha=7c29513b0e8f42f457189d1bec23e1de475d4ed8

**BLOCKED** — infrastructure did not provision the required data.

What stopped me:

1. **No `repo/` directory.** The task says "the base is the clone in `repo/`", but no `repo/` subdirectory exists in the job workspace. The `.git` at the workspace root is an empty repo (no commits, no refs, no objects).
2. **Bundles inaccessible.** Both `/tmp/schema14-ftf.bundle` (base) and `/tmp/s17-heads.bundle` (head ref) are blocked by the sandbox-exec environment — every access attempt (`ls`, `cat`, `stat`, `git fetch`) returns `Operation not permitted`. The bundles do not exist under any other accessible path searched.
3. **Sandbox redirects TMPDIR.** `${TMPDIR}` points to `.nova-sandbox-tmp`, which contains only toolchain binaries (dylib, xcrun_db) — no git data whatsoever.

This means Steps 1–6 cannot execute: I cannot clone the base, fetch the head ref, inspect commits or diffs, rebase, resolve conflicts, or run tests. Without access to either tree, nothing is possible.

**ROOT CAUSE:** This is the bench's fault, not this PR's. The setup that should have unpacked both bundles into `repo/` (for the base) and left them at `/tmp/*.bundle` (for fetching) did not complete before this card ran. The previous harness output confirms it failed immediately trying to `cd repo && git rev-parse HEAD`.
