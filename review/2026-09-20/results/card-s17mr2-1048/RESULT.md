RESULT: s17mr2-1048 sha=<unreachable>

**CANNOT READ**

The card asserts a tree at `repo/` (branch `fixed-table-form`, sha `7c29513b`) and a bundle at `/tmp/schema14-ftf.bundle`. Neither is accessible from this sandbox: `cd repo` fails (no such directory), and `/tmp/` is blocked by sandbox-exec. The snapshot bare repo at `data/opencode/snapshot/global/72d55f2cf4cd8ecb0d407528154ba981621156ab` contains only the job's own files (`.lease`, `harness-output.log`, `opencode.json`) and no commit objects from the bundle. STEP 1 (orient, read base files) and STEP 5 (make targets) cannot be executed.

The diff is also truncated at 6000 of 17198 bytes. The remaining ~11200 bytes likely contain emitter code changes and possibly `docs/PORTING.md` hunk(s) that STEP 4 requires. Without the emitter code I cannot verify whether the test assertions in `TestJavaEmitsTheIdTableWireRuntime` are backed by real emission, and without PORTING.md I cannot check for flipped matrix cells without assertions. The files I needed:

- The full diff (all 17198 bytes), especially the emitter portion (probably `compiler/tablesjava.go` or similar)
- `docs/PORTING.md` (if touched, likely in truncated portion)
- The base tree `compiler/tablesjava_test.go` to read `packetSrc`, `javaFiles()` and surrounding helpers
- The base tree `docs/SPEC-TABLES.md` to verify context line accuracy