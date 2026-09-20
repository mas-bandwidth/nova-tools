RESULT: s17pyw-w15 sha=7c29513b

**BLOCKED — no accessible base repo**

The git working tree contains zero commits (`git rev-parse HEAD` fails). The specified `base-repo` at `/tmp/schema14-ftf.bundle` cannot be read (PermissionError: Operation not permitted). Consequently:

- **NO LAW**: `docs/SPEC.md` does not exist in this environment; greps for it, `docs/FIXED-FORM-ALGORITHM.md`, or any `*SPEC*`/`*FIXED*` files across the entire workspace returned zero matches.
- **NO REFERENCE**: No C++ oracle/source files exist on disk; the working tree is empty aside from `.git/`, `opencode.json`, `.lease`, and harness metadata.
- **CORPUS**: `bench/paired/corpus` does not exist.

`python3` is available (v3.14.7) — toolchain is **not** the blocker. Only the missing/inaccessible base schema repository blocks progress.
