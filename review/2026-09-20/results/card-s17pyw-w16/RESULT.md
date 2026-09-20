RESULT: s17pyw-w16 sha=NO-HEAD

**BLOCKED** — the base repository was never provisioned, so there is no law, no oracle, and no corpus to port row W16 against.

What stopped me, exactly:

1. **STEP 1 fails.** `git rev-parse HEAD` does not print `7c29513b0e8f...`; it fails with
   `fatal: ambiguous argument 'HEAD': unknown revision or path not in the working tree.`
   The working tree has **no commits at all** (`git log` -> `fatal: your current branch 'master' does not have any commits yet`), zero refs (`git for-each-ref` empty), and zero objects (`find .git/objects` empty; `git cat-file -t 7c29513b0e8f...` -> `could not get object info`).

2. **The base bundle is inaccessible.** `ls /tmp/schema14-ftf.bundle` -> `ls: /tmp/schema14-ftf.bundle: Operation not permitted` (sandbox denies read of /tmp outside the workspace; the bundle also is not present inside the workspace). There is no way to obtain the base-sha tree.

3. **No source files exist in the tree.** `find .` (excluding .git and .nova-sandbox-tmp) returns only `harness-output.log`, `opencode.json`, `.lease`. Therefore:
   - STEP 2 cannot run: `docs/SPEC.md` and `docs/FIXED-FORM-ALGORITHM.md` do not exist, so the W16 rule ("nested union answers outer tag") cannot be quoted with file:line.
   - STEP 3 cannot run: the C++ packet reference files and the paired corpus (`bench/paired/corpus`) do not exist, so the oracle line range cannot be quoted.
   - STEP 4 cannot run: there is no way to scaffold `test/py-packet/w16/` against a corpus that does not exist.

This is an environment/harness failure (the bench never checked out base-sha `7c29513b0e8f42f457189d1bec23e1de475d4ed8`), not a failure of row W16. Python itself is present (`python3` = Python 3.14.7 at /opt/homebrew/bin/python3), so this is not BLOCKED-TOOLCHAIN either; the blockage is the missing source tree.

Nothing was committed: there is no base commit and no code could be written against an absent oracle. No files were created under `test/py-packet/` or `python/`.