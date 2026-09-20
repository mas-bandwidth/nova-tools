RESULT: s17pyf-e3 sha=7c29513b0e8f

**BLOCKED** — the base repo was never materialized into this job's working tree, and the bundle it
would be cloned from is unreadable under this bench's sandbox. STEP 1 cannot be satisfied, so the
row cannot be read, ported, or tested.

Exactly what stopped me:

1. **`git rev-parse HEAD` cannot print `7c29513b...` (or any sha).** The job workspace is a bare,
   freshly-`git init`'d repository with zero commits, zero objects, zero refs, and no remote:

   ```
   ### STEP 1 — git rev-parse HEAD
   fatal: ambiguous argument 'HEAD': unknown revision or path not in the working tree.
   Use '--' to separate paths from revisions, like this:
   'git <command> [<revision>...] -- [<file>...]'
   HEAD

   ### git log
   fatal: your current branch 'master' does not have any commits yet

   ### .git objects count
   0
   ### refs
   0
   ### git remote -v
   (empty)
   ```

2. **The seed bundle is not readable.** The card names `base-repo: /tmp/schema14-ftf.bundle`, but
   the sandbox denies it. `ls`, `git clone`, and the opencode Read tool all fail:

   ```
   ### bundle access
   ls: /tmp/schema14-ftf.bundle: Operation not permitted

   git clone /tmp/schema14-ftf.bundle ... → fatal: repository '/tmp/schema14-ftf.bundle' does not exist
   ```

   The sandbox (`nova-sandbox --read <job-dir> ...`) whitelists only the job tree, the harness,
   `/opt/homebrew`, `/Users/glenn/sdk`, and the Go module cache. `/tmp` is outside it.

3. **No trace of the repo anywhere else readable.** Nothing under the allowed read paths (harness
   dir, `~/sdk`, go module cache, the job's own `data/` tree) contains `docs/`, `test/`,
   `make/`, `build/`, `python/`, `FIXED-FORM-ALGORITHM.md`, `fixedform_main.cpp`, or the
   `fixedform-corpus` oracle. Because the tree is empty, **STEP 2 (the law) and STEP 3 (the C++
   reference) cannot even be attempted**: there is no `docs/FIXED-FORM-ALGORITHM.md` and no
   `test/tables/fixedform_*.cpp` to grep.

   ```
   ### ls python/ test/py-*
   ls: python/: No such file or directory
   ls: test/py-*: No such file or directory

   ### grep make/ Makefile ci
   grep: make/: No such file or directory
   grep: Makefile: No such file or directory
   grep: .github/workflows/ci-fast.yml: No such file or directory

   ### ls docs test make build python
   ls: build: No such file or directory
   ls: docs: No such file or directory
   ls: make: No such file or directory
   ls: python: No such file or directory
   ls: test: No such file or directory
   ```

4. **Not a toolchain problem.** `python3` is present (Python 3.14.7 at `/opt/homebrew/bin/python3`),
   so this is **not** `BLOCKED-TOOLCHAIN`.

Because none of the source of truth for row E3 exists in the sandbox, I wrote nothing under
`test/py-fixedform/` or `python/`, and I committed nothing. Re-run this card with the base repo
(bundle at `/tmp/schema14-ftf.bundle`, base-sha `7c29513b0e8f42f457189d1bec23e1de475d4ed8`, branch
`fixed-table-form`) materialized into the working tree at HEAD before starting.