# nova-table review, 2026-10-05

Rater: inception/mercury-2.5
Build: 52046bd9c6116d1a0e89261f887e08e51f6664f7
Verdict: GOOD WITH FIXES for an AI to use
Score: 8/10

## Reasons
The tool is well-structured and follows nova-tools conventions. Help is comprehensive and error messages provide clear remedies. The Redis function library auto-loading works correctly. The design for ordered sets, cells, and watches is solid. Two issues prevent a perfect score: (1) the --redis flag requires an absolute path for Unix sockets, which is not intuitive; (2) the help example doesn't show where to place connection flags relative to sub-commands.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | docs/CLI.md, README.md | The --redis flag documentation doesn't clearly show it can come after the verb | Add explicit example: `nova-table create demo --columns a,b --redis /path/to/redis.sock` | S |
| 2 | cmd/nova-table/main.go | Unix socket paths are not auto-detected from .sock extension | Add automatic conversion of relative .sock paths to absolute paths | S |

## Good, keep
Auto-loading of nova_sprint library on first store contact. The render rules are precise and consistent. The watch feature with atomic file updates is well implemented.
