RESULT: s17pyf-c7 sha=7c29513b0e8f

**BLOCKED** — the base repository was never provisioned into this workspace, and the bundle that would restore it is unreadable from the sandbox.

What stopped me, exactly:

1. STEP 1's orientation check fails. `git rev-parse HEAD` prints
   `fatal: ambiguous argument 'HEAD': unknown revision or path not in the working tree.`
   The workspace git repo has zero commits (`git log --all` is empty, `git show-ref` is empty, `git fsck` reports "No default references").

2. The entire base repo is absent. There is no `docs/`, no `test/`, no `python/`, no `make/`, no `Makefile`, no `.github/` — `ls docs test python make` returns "No such file or directory" for each. The only files present are the harness's own `.lease`, `opencode.json`, and `harness-output.log`.

3. STEP 2 and STEP 3 are therefore impossible: there is no `docs/FIXED-FORM-ALGORITHM.md` to quote the law for row C7 from, and no `test/tables/fixedform_main.cpp` / `test/tables/fixedform_properties.cpp` / `build/fixedform-corpus` to read the C++ reference from.

4. The one source that could provide the repo — the base bundle at `/tmp/schema14-ftf.bundle` — is not readable from inside this sandbox:
   - `ls /tmp/` → `Operation not permitted`
   - `ls /private/tmp/` → `Operation not permitted`
   - `cp /tmp/schema14-ftf.bundle .nova-sandbox-tmp/opencode/bundle` → `Operation not permitted`
   - `git bundle list-heads /tmp/schema14-ftf.bundle` → `could not open '/tmp/schema14-ftf.bundle'`
   `/tmp` is not in the sandbox read scope; `native-argv.log` shows the sandbox only reads the card directory, `/Users/Shared/nova-swarm-stella-20260913/harness-v1.18.20`, `/opt/homebrew`, `/Users/glenn/sdk`, `/opt/homebrew/Cellar/*`, and `/Library/Java/JavaVirtualMachines`.

5. I searched every readable location for a copy of the repo or bundle: the card directory tree (excluding harness internals), `/Users/glenn/rowan-working/tmp/cache`, `/Users/glenn/sdk`, and the opencode snapshot git dir under `data/opencode/snapshot/` (that git dir only contains opencode's own snapshots of `harness-output.log`, `.lease`, and `opencode.json` — no repo blobs). Nothing.

This is the bench's provisioning fault, not the row's. Python 3.14.7 is present (`/opt/homebrew/bin/python3`, `Python 3.14.7`), so this is not `BLOCKED-TOOLCHAIN`. The row cannot be ported until the base repo at `7c29513b0e8f` is provisioned (or the bundle is made readable). Committed nothing, per the card's instruction to stop on a finding.