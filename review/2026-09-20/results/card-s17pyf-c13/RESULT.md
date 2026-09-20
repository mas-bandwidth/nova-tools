RESULT: s17pyf-c13 sha=7c29513b0e8f

**BLOCKED** — the base repository is unreachable in this sandbox, so none of
STEPS 1–5 could be executed. This is a bench/harness fault (the repo bundle is
missing/unreadable), not a fault of the row and not a toolchain fault.

What stopped me, exactly:

1. STEP 1 required `git rev-parse HEAD` to print `7c29513b...`. It printed:
   ```
   fatal: ambiguous argument 'HEAD': unknown revision or path not in the working tree.
   Use '--' to separate paths from revisions, like this:
   'git <command> [<revision>...] -- [<file>...]'
   ```
   The workspace `.git` is empty: no objects, no refs, no commits
   (`git fsck` -> `notice: No default references`; `.git/objects/pack` empty;
   `git show-ref` empty; `git log` -> "your current branch 'master' does not
   have any commits yet"). The sha12 `7c29513b0e8f` below is taken from the
   card header because STEP 1's required output never existed here.

2. The base bundle `/tmp/schema14-ftf.bundle` is not accessible:
   ```
   $ git fetch /tmp/schema14-ftf.bundle
   fatal: '/tmp/schema14-ftf.bundle' does not appear to be a git repository
   fatal: Could not read from remote repository.
   $ ls -la /private/tmp/schema14-ftf.bundle
   ls: /private/tmp/schema14-ftf.bundle: Operation not permitted
   $ python3 -c "import os; print(os.path.exists('/tmp/schema14-ftf.bundle'))"
   False
   ```
   (`/tmp` is a symlink to `/private/tmp`; both the symlink target and the
   bundle path are denied by the sandbox. `find` over reachable trees found no
   `schema14*` bundle and no checkout of the base-sha anywhere.)

3. Because the repo has no content, the inputs STEPS 2–3 require do not exist,
   so the row cannot be ported:
   - STEP 2 law source `docs/FIXED-FORM-ALGORITHM.md` — absent.
   - STEP 3 C++ oracle `test/tables/fixedform_main.cpp` and
     `test/tables/fixedform_properties.cpp` — absent.
   - Byte oracle `build/fixedform-corpus` — absent.
   - `python/`, `test/py-fixedform/`, `make/` — all absent (verified:
     `ls python/ test/py-*` returned nothing).

4. Toolchain is fine and is NOT the blocker: `command -v python3` ->
   `/opt/homebrew/bin/python3`, `python3 -VV` -> Python 3.14.7. So this is not
   `BLOCKED-TOOLCHAIN`.

Nothing was committed. There are no files under `test/py-fixedform/` or
`python/` to commit, and STEP 6 forbids committing paths outside those two
trees (RESULT.md at the repo root is left uncommitted for the manager).
No scaffolding was written or assumed; no case was written; there is no red or
green output because the RED test itself cannot exist without the oracle.