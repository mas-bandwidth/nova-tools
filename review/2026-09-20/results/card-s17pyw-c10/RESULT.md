RESULT: s17pyw-c10 sha=7c29513b (expected from card; could not verify — bundle missing)

**BLOCKED — `base-repo: /tmp/schema14-ftf.bundle` is inaccessible**

The sandbox read permissions do not include `/tmp/`. The bundle file cannot be read, fetched, or extracted:

```
$ ls /tmp/
ls: /tmp/: Operation not permitted
```

Without access to the bundle I cannot:

- **STEP 1**: Check out `base-sha=7c29513b0e8f42f457189d1bec23e1de475d4ed8` on branch `fixed-table-form`. The git object database contains only harness output logs (all blobs start with `SANDBOX OK ...`), trees that enumerate `.lease` and `harness-output.log`, and pack files of identical harness data. No commit with hash `7c29513b` exists. No branch `fixed-table-form` exists.

- **STEP 2**: Read `docs/SPEC.md` and `docs/FIXED-FORM-ALGORITHM.md` to find the rule for row C10 ("enum ordinal past top → None").

- **STEP 3**: Locate and read the C++ packet reference code and the paired corpus under `bench/paired/corpus`.

All 48 loose objects and both pack files in the snapshot were inspected; every blob begins with harness output from previous sandbox runs. No schema documentation, C++ source, Makefiles, or CI workflow files are present among the git objects.

**What was available:**

| Item | Status |
|------|--------|
| `python3 --version` | Python 3.14.7 ✓ |
| `test/py-packet/` directory | Does not exist (must be created) |
| `python/` directory | Does not exist (must be created) |
| `/tmp/schema14-ftf.bundle` | **Cannot read** (`Operation not permitted`) |
| Any SPEC.md or .md docs | Not present in any accessible path |
| C++ source files (`.cpp`, `.hpp`) | Not present in any accessible path |

Searched paths: `/Users/glenn/sdk/*`, `/opt/homebrew/*`, `/Users/glenn/rowan-working/tmp/cache/*`, all git object stores (loose + pack), opencode.db, all config files (`.yml`, `.yaml`, `.json`). No schema14 repo content found anywhere.

This is an infrastructure fault — the bench's sandbox did not extract or mount the base repo bundle before starting the card. Without it, no porting can proceed. A separate card should address making `/tmp/schema14-ftf.bundle` readable within the sandbox (e.g., adding `/tmp/` to the read ACL, or extracting the bundle into a sandbox-visible path before the card starts).
