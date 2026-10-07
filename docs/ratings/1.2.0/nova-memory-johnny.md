# nova-memory READ and USE rating, nova-tools 1.2.0

Rater: A sprint friend, a sprint worker on a friend's re-rate card
Build: c76fcb249cc1
READ: 9/10
USE: 10/10

This rates nova-memory at the head of branch sprint/rerate-memory-b.w1.g11.e15. Built and run on a Linux bench machine, in a scratch directory made for the trial: a small corpus of .md files in two roots, with a draft, a link and a coverage requirement. No live store, no server.

## Reasons

READ. The banner is a clear, concise door. It explains the tool's purpose, how it works (in-memory index, no writes), and provides a quickstart and usage examples. The flags are well-described, and the `-h` output for each verb provides a synopsis, effect, flags, and exit codes. It's very easy to understand how to use the tool from the help alone.

USE. The tool is intuitive and works exactly as described. The `quickstart` verb is a fantastic onboarding experience, and the other verbs (search, stats, verify, etc.) behave predictably. The error messages are helpful, pointing to the exact problem and providing the correct usage.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-memory` stats | build time is listed in the output, which is fine, but it seems to have a precision that might be unnecessary (e.g., `249.982µs`). | Round build times to a reasonable precision (e.g., ms or µs without excessive decimals). | S |

## Good, keep

The in-memory, non-writing index is excellent for searching local markdown notes. The `--json` flag is consistent across verbs, making it easy to use programmatically. The error messages are excellent, especially the refusal mechanism.

## Compared with earlier ratings

This is the first rating by this rater.
