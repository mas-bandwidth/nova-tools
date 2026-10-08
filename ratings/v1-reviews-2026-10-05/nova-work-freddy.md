# nova-work review, 2026-10-05

Rater: inception/mercury-2.5
Build: 52046bd9
Verdict: NOT YET for an AI to use
Score: 1/10

## Reasons

nova-work is parked under deprecated/ in nova-tools v1.0.0 and cannot be built or run. The deprecated/go.mod has no require lines, yet cmd/nova-work/main.go imports 15 packages from the root module (internal/buildinfo, internal/ci, internal/jobs, internal/oneline, internal/workclient, internal/worklang, and redis) that do not exist in deprecated/. No binary can be produced, so no help or verbs can be run.

First confusion: docs/CLI.md lists nova-work with extensive help text and verb documentation, but the tool is not buildable. An AI reading this documentation would not immediately understand that the tool exists only in deprecated/ and has no working implementation.

First doubted claim: The README.md says "Nothing here is built, tested or maintained" but the CLI.md still documents it as an active tool. This is contradictory and confusing for anyone trying to use it.

## Findings

| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | deprecated/go.mod | No require lines; imports from root module fail. The tool cannot be compiled. | Add require directives for internal packages if keeping, or remove all imports and stub out main | M |
| 2 | deprecated/README.md | Says tool is not maintained but docs/CLI.md still documents it. Inconsistent state. | Either move docs to deprecated/docs/CLI.md only or remove tool entirely | S |
| 3 | cmd/nova-work/main.go | Imports from github.com/mas-bandwidth/nova-tools/internal/* which don't exist in deprecated/. | Move internal packages to deprecated/internal/ or remove tool | L |

## Good, keep

- The tool's usage documentation in CLI.md is comprehensive and follows nova standards
- The code structure (main.go with usage constant) matches other nova tools
- If restored, the spec documents (SPEC-WORK.md, SPEC-JOBS.md) provide clear behavior contracts
