RESULT: s17pyw-w13 sha=UNREACHABLE

**BLOCKED-TOOLCHAIN**

The working tree at `card-s17pyw-w13/jobs/card-s17pyw-w13` is a fresh `git init` with zero commits:

```
$ git rev-parse HEAD
fatal: ambiguous argument 'HEAD': unknown revision or path not in the working tree.
```

The base bundle `/tmp/schema14-ftf.bundle` does not exist on disk and cannot be fetched. The snapshot
bare repo under `data/opencode/snapshot/` contains only cached log blobs from prior harness runs, not
the schema14 repo objects. There are no files `docs/SPEC.md`, `docs/FIXED-FORM-ALGORITHM.md`, or any
C++ oracle files to read.

Without the bundle, STEP 1's precondition (`git rev-parse HEAD` must print `7c29513b…`) cannot be
satisfied. STEPS 2 and 3 (reading the doc law and the C++ reference) are therefore impossible. The
row cannot be ported until the bench provides the bundle.

Evidence:
— `ls -la /tmp/schema14-ftf.bundle 2>&1` → empty
— `git rev-parse HEAD` → "unknown revision or path not in the working tree."
— `git cat-file -t 7c29513b0e8f42f457189d1bec23e1de475d4ed8` → "could not get object info"
— `git log --all --oneline` → empty
— `find /Users/Shared -name '*.bundle'` → empty