RESULT: s17pyw-f1 sha=???

**BLOCKED-TOOLCHAIN**

The base repo bundle `/tmp/schema14-ftf.bundle` does not exist and cannot be accessed. This is a re-run of a previous job that hit the same problem (see `harness-output.log`). The sandbox has no `--read` mount for `/tmp` and the bundle file is not present anywhere in any writable directory.

Attempts made:
- `ls /tmp/schema14-ftf.bundle` — no such file
- `find /tmp -maxdepth 3 -type f` — empty
- `find /Users/glenn/rowan-working/tmp -name "*.bundle"` — none found
- The snapshot at `data/opencode/snapshot/global/f11fc24e6ea28ed0d158fced3e5fab912bd3cce2/` contains only blob/tree objects in packs (no commits), reflecting the current job state, not the base repo
- No `.git` history exists in the job working directory

Without the bundle containing base commit `7c29513b0e8f42f457189d1bec23e1de475d4ed8` on branch `fixed-table-form`, I cannot read the C++ reference (`docs/SPEC.md`, `docs/FIXED-FORM-ALGORITHM.md`, oracle test files, `bench/paired/corpus`), find the rule for row F1, or write any code. This is the bench's infrastructure fault, not the row's.