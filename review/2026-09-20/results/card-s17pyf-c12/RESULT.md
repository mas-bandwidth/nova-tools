RESULT: s17pyf-c12 sha=7c29513b0e8f

**BLOCKED-ENV — bundle at `/tmp/schema14-ftf.bundle` inaccessible from sandbox.**

## STEP 1 — Orient

```
$ git rev-parse HEAD
fatal: ambiguous argument 'HEAD': unknown revision or path not in working tree.
(no commits exist; repo was initialised empty, no bundle was unpacked)
```

python/ and test/py-fixedform/ do not exist. No make targets reference python.
`python3 -VV`: `Python 3.14.7 (main, Aug 5 2026, 10:29:49) [Clang 21.0.0]` — interpreter present.

## STEP 2 — Law (docs/FIXED-FORM-ALGORITHM.md)

File does not exist in this workspace:

```
$ grep -rn "bool byte != 0\|C12" docs/FIXED-FORM-ALGORITHM.md
grep: docs/FIXED-FORM-ALGORITHM.md: No such file or directory
```

The algorithm documentation is part of the base-repo which could not be loaded.

## STEP 3 — C++ Reference

Files do not exist in this workspace:

```
$ ls test/tables/fixedform_main.cpp test/tables/fixedform_properties.cpp
ls: test/tables/fixedform_main.cpp: No such file or directory
ls: test/tables/fixedform_properties.cpp: No such file or directory
```

No C++ oracle for row C12 is available to port.

## Why blocked

The card specifies `base-repo: /tmp/schema14-ftf.bundle`. The sandbox (`sandbox-exec`,
`read=7`) denies all access to `/tmp/`. The bundle was never unpacked into the working
directory before the card started. Without the base-repo content there is no algorithm
documentation and no C++ reference tests — the two sources required by Steps 2–3 to
establish what row C12 does and how it should behave.

## What is needed to unblock

The harness must either:
1. Clone/copy the bundle into the working directory as a git repo with `7c29513b…` as HEAD, **or**
2. Grant the sandbox read permission on `/tmp/schema14-ftf.bundle` so it can be fetched.

Once the base-sha is available, Steps 2–5 proceed normally: read the law, read the C++
oracle, write a red case, write the green Python implementation, run the four control
outputs.
