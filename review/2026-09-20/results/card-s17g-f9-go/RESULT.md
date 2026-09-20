RESULT: s17g-f9-go sha=7c29513b0e8f
BLOCKED-TOOLCHAIN

The base-repo at `/tmp/schema14-ftf.bundle` is inaccessible from within the sandbox:
sandbox-exec blocks access to `/tmp` (Operation not permitted on ls, file, test -r).
No writable mount or read path includes the bundle.

The working directory contains an empty git repo with no commits.
The snapshot git directory at `data/opencode/snapshot/global/ecd13e25df891dd811c5f3e6efe5270f591e5d31/`
has pack files that git verify-pack reports as "bad" and cannot be read.

No `test/` or `internal/codegen/` directories exist. There is no source code to
measure or edit. The card cannot proceed without the base repo bundle being made
accessible (e.g. copied into the writable jobs directory or added to the sandbox
read path).