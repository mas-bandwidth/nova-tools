RESULT: s17mr2-1037 sha=CANNOT READ

**DEFECTS**

1. **No `repo/` directory with expected SHA** — `git rev-parse HEAD` fails in the job working directory; `.git/` exists but contains no objects, only metadata files (HEAD, config, description, hooks, info, objects/empty, refs/empty). Expected base-sha `7c29513b0e8f42f457189d1bec23e1de475d4ed8` from branch `fixed-table-form` is absent.

2. **Empty diff provided** — The section labeled "THE DIFF (PR #1037, head c131867dda942d09709b04b78c7bf14701a17bb6)" contains an empty code block with zero hunks and zero file paths. No lines quoted, no context established. Cannot perform STEP 2 (the law), STEP 3 (the read, hunk by hunk), STEP 4 (the matrix), or STEP 5 (can it even run) without diff content.

3. **Bundle file missing** — `base-repo: /tmp/schema14-ftf.bundle` does not exist on disk. No source of the base tree is available to cross-reference.

4. **Spec documents missing** — `docs/FIXED-FORM-ALGORITHM.md`, `docs/SPEC.md`, and `docs/PORTING.md` referenced in STEP 2 are nowhere in the workspace. No laws to quote against.

Verdict: CANNOT READ — the diff below is completely empty (no files, no hunks), preventing any behavioral analysis. The input appears malformed: a PR diff was expected but none was supplied.
