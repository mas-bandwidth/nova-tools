RESULT: s17pr2-1357 sha=7c29513b0e8f42f457189d1bec23e1de475d4ed8

**CANNOT READ**

This card cannot be read because the base tree `repo/` does not exist in the job sandbox and `base-repo /tmp/schema14-ftf.bundle` is unreadable (operation not permitted on disk; bare git object access fails with `fatal: could not get object info`). The git repo at the job path has zero commits. Therefore:

- **STEP 1** fails: cannot read any file the diff touches at base. The file-level context needed to verify byte-offset correctness in the forged-record section (offsets `body+0`, `body+40`, `body+48` into the binary `FixedTable` layout) is absent.
- **STEP 2** cannot execute: cannot locate or quote any rule from `docs/FIXED-FORM-ALGORITHM.md`, `docs/SPEC.md`, or `docs/PORTING.md` because none of these files are present.
- **STEP 4** cannot execute: no `docs/PORTING.md` is available to check flipped cells. (The diff also does not touch `docs/PORTING.md`, so no DEFECT of this shape is introduced.)
- **STEP 5** cannot execute: no `make/` or `Makefile` is present to verify gate-target coverage.

The diff itself is not truncated and is analyzable in part:
- The new function `fixedBoundsCase()` is called from `main()` at line 2071 — it is not an orphaned call-site.
- The test follows the same two-phase pattern used elsewhere in the file (clean round-trip, then forged record).
- However, without the base tree and specs, the byte-offset arithmetic, the expected clamped count of 3, and the clamp values (`65535 * 256`, `281474976710655`, `65535`) cannot be verified against any law.
