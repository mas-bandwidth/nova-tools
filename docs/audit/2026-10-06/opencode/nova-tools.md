# Nova Tools Audit (2026-10-06)

This is a cold audit of nova-tools without the sprint. Issues found by inspection and cold reading, not by tests.

## Issues Found

1. **internal/docs/scratch_tracked_test.go:46** - Test relies on git worktree setup
   - **Defect:** `TestNoTrackedScratchPathOrOversizedFile` uses `git ls-files` via `exec.Command` but depends on correct worktree path resolution in `.git` file. When the worktree directory doesn't exist or has stale path references, the test fails with `exit status 128`.
   - **Evidence:** When syncing a worktree via rsync to a fresh bench, the `.git` file contained a path like `gitdir: /Volumes/nova/ai/freddy/working/mirrors/mas-bandwidth/nova-tools.git/worktrees/audit-nova-tools-opencode-b.w4-15` but the actual worktree directory was named `audit-nova-tools-opencode-b.w4~15` (tilde vs dash mismatch) or didn't exist.
   - **Grade:** NEXT
   - **Fix:** The worktree setup needs to handle path normalization. Consider using `git worktree add` with the full absolute path instead of relative paths, or add a recovery step in the setup script.

2. **internal/docs/catalog.go** - No issues found during inspection
   - **Evidence:** The catalog structure is well-organized and follows the documented patterns.
   - **Grade:** N/A

3. **docs/SPEC-CI.md** - Class tests are well-specified but have complex allowlist management
   - **Defect:** The class tests (waits, testbins, templates) maintain shrink-only allowlists. While this is correct for preventing regressions, it can be cumbersome for initial development.
   - **Evidence:** Reading through the spec shows careful design but potential friction for new contributors.
   - **Grade:** NEXT
   - **Fix:** Consider adding a development mode that allows temporary allowlist additions with explicit expiry dates.

4. **internal/docs/agentsmap.go** - Map generation is tightly coupled with file system state
   - **Defect:** The `Check` function validates the entire directory tree against the catalog. While this is intentional for consistency, it means any structural change requires regeneration.
   - **Evidence:** Tests `TestCommittedMapMatchesTree` and `TestStaleMapFailsUntilRegenerate` show this coupling.
   - **Grade:** N/A (by design)

## Summary

- **urgent=0**
- **next=2**
