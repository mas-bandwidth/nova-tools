RESULT: s17pyw-e7 sha=blocked

**BLOCKED-SANDBOX — the base repository was never loaded into this workspace.**

The harness provisioned an empty git repository with no commits, no objects, and no working-tree files.

* The `base-repo` bundle (`/tmp/schema14-ftf.bundle`) does not exist on disk.
* There are zero git objects in `.git/objects/`; `git cat-file --batch-check --batch-all-objects` returns nothing.
* No branches, tags, or reflogs exist beyond what this session created.
* Neither `docs/SPEC.md` nor any C++ reference file is present for Step 2 or Step 3 of the card instructions.
* `test/py-packet/` and `python/` do not yet exist to host test code or implementation.

Without the schema repository (containing `docs/SPEC.md`, `docs/FIXED-FORM-ALGORITHM.md`, the C++ packet reference/oracle files, and the existing nine legs' tests under `test/`), no rows can be analyzed, ported, or tested.

Steps taken before blocking:
1. **STEP 1 — Orient:** `git rev-parse HEAD` failed — no commits exist. `command -v python3 && python3 --version` → `/opt/homebrew/bin/python3 Python 3.14.7`. Branch `rowan/py-e7-packet` created.
2. **STEP 2 — Read the law:** Could not execute — `docs/SPEC.md` does not exist anywhere in the workspace or under `/Users/glenn/`.
3. **STEP 3 — Read the C++ reference:** Could not execute — no oracle files, no bench directory, no corpus present.
4. **STEPs 4–5 (scaffolding / red case):** Not possible without knowing the rule or reference implementation.

This card requires a properly populated workspace containing the schema14 repository (or its bundle) before it can proceed.
