RESULT: s17pyf-c8 sha=7c29513b0e8f

**BLOCKED**

What stopped me, exactly:

- STEP 1 `git rev-parse HEAD` fails: `fatal: ambiguous argument 'HEAD': unknown revision or path not in the working tree.` The checkout at `jobs/card-s17pyf-c8` has **no commits, no refs, and an empty object store** (`git count-objects -v` → `count: 0, in-pack: 0, packs: 0`). `.git/HEAD` points at `refs/heads/master`, which does not exist.
- The working tree contains only harness scaffolding: `.lease`, `.nova-sandbox-tmp/`, `harness-output.log`, `opencode.json`. There is **no source code at all**: no `docs/`, no `test/`, no `make/`, no `python/`.
- The base-repo bundle `/tmp/schema14-ftf.bundle` (base-sha 7c29513b0e8f, branch `fixed-table-form`) is **not readable from this sandbox**: `ls`, `cat`, and `git fetch /tmp/schema14-ftf.bundle` all return `Operation not permitted` / `does not appear to be a git repository`. `/tmp` is only reachable as a symlink to `/private/tmp`, which the sandbox also denies.
- `find` across the accessible workspace (`/Users/glenn/rowan-working/tmp/...card-s17pyf-c8`) found no copy of the schema: `data/opencode/repos` is empty, `tmp/card-s17pyf-c8` is empty.

Consequences for this card (row C8, fixed-point F-shift / bits(N)):

- STEP 2 cannot be done: `docs/FIXED-FORM-ALGORITHM.md` does not exist, so the law for C8 cannot be read or quoted.
- STEP 3 cannot be done: `test/tables/fixedform_main.cpp` and `test/tables/fixedform_properties.cpp` do not exist, so the C++ oracle cannot be read.
- STEP 5 cannot be done: the byte oracle `build/fixedform-corpus` (written by the C++ reference) does not exist, so a red-first case cannot be written or verified against the reference.

Not BLOCKED-TOOLCHAIN: `python3` IS present (`/opt/homebrew/bin/python3`, Python 3.14.7) and pytest availability could not even be checked because there is no test tree to run.

No files were created under `test/py-fixedform/` or `python/`; nothing was committed. The branch `rowan/py-c8-fixed` cannot be created on top of a non-existent base.

This is the bench's fault: the base repo bundle was not materialized into the checkout, and the bundle itself is unreadable from the sandbox. The row cannot be ported until the checkout is populated (fetch the bundle into `.git` and check out `fixed-table-form`).