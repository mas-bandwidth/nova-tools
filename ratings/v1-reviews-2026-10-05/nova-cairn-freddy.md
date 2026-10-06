# nova-cairn review, 2026-10-05

Rater: inception/mercury-2.5
Build: 52046bd9a0ebf726b40617a81e678d2855047a60
Verdict: GOOD WITH FIXES for an AI to use
Score: 7.5/10

## Reasons
The tool is well-structured for checkpointing sessions without imposing a memory lifecycle. The CLI is consistent with nova-tools conventions and the spec is comprehensive. However, I couldn't run the tool here (go commands refused on the Studio), so I can't verify runtime behavior. My first confusion was whether bench stores (markdown-file-per-session) are fully supported across all verbs. The first claim I doubted is that "the nested record wins when a store somehow holds both" shapes—this needs verification. The tool would need runtime verification and test coverage for bench stores to reach 10.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-cairn help` | The help output does not show the bench store shape (<id>.md); an AI won't know the tool can read hand-written markdown files without reading the spec. | Add a bench-store paragraph to the `--store` flag description in usage constant | S |
| 2 | docs/SPEC-CAIRN.md line 52 | The spec says "A refusal names the remedy verb whole" but I couldn't verify the runtime behavior by running append without open first | Run a quick test against a scratch store to verify | S |
| 3 | cmd/nova-cairn/main.go line 505 | The bench store reading path exists in internal/cairn but lacks runtime verification in available tests; spec lists 11 tests not yet written | Verify the 11 unwritten tests exist and pass | M |

## Good, keep
- The separation of persisted=true and published=false is excellent for AI use cases—local durability is real while publication is pending
- Exit codes are meaningful: 0=passed, 1=conflict (caller must resolve), 2=could not run
- The refusal pattern with complete remedy verbs is well-implemented in the code
