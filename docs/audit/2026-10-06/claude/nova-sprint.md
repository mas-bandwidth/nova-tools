# nova-sprint audit — claude, 2026-10-06

Cold audit of the sprint command package and related sprint infrastructure.

## Summary

The targets of this audit no longer exist in the codebase. The nova-sprint tool
and its packages (internal/sprint, internal/sprintwire, cmd/nova-sprint) have
been refactored into a new modular toolset as of the current repository state.

## Findings

### URGENT

1. **cmd/nova-sprint and related packages removed** (cmd/*, internal/sprint, internal/sprintwire)
   - The nova-sprint binary and all its supporting packages have been removed
   - Functionality has been redistributed across multiple new tools:
     - nova-bus: shared messages and replies
     - nova-table: work tables and views
     - nova-config: fleet configuration
     - nova-memory: note search
     - nova-check: record validation
   - Evidence: `ls cmd/` shows no nova-sprint directory; `ls internal/` shows no sprint or sprintwire directories
   - Grade: URGENT (targets of audit no longer exist)

### NEXT

1. **Documentation references to sprint may be stale** (docs/*)
   - CLI.md and SPEC-SPRINT.md may reference removed sprint commands
   - Evidence: docs/ no longer contains SPEC-SPRINT.md or SPEC-RELEASE.md at expected locations
   - Grade: NEXT (documentation cleanup)

## Conclusion

The sprint audit cannot proceed as originally scoped because the sprint codebase
has been refactored. A follow-up audit of the replacement tools (nova-bus,
nova-table, nova-config, etc.) would be required to assess equivalent functionality.

urgent=1 next=1
