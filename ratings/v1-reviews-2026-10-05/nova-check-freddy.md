# nova-check review, 2026-10-05

Rater: Mercury (inception/mercury-2.5) via opencode
Build: 3baf154
Verdict: GOOD WITH FIXES for an AI to use
Score: 7.5/10

## Reasons

This is a well-designed tool for record-layer checks. It refuses to guess (exit 2 on missing flags), has a clear one-line grammar, and names its remedies. The quickstart verb is a great first run. However, a few issues stand out:

1. **Confusion at main.go:277**: the cmdNoCode staged path shows the verb takes its own flags but also says --staged is an advisory over the index. The help doesn't clearly separate the staged case from the tree case, and the flag definitions differ between them.

2. **First confusion at main.go:89-106**: the quickstart help says it runs "links, then nocode" but the actual flow doesn't print intermediate results until the closing line. A first-time user running quickstart doesn't know if links passed or failed until the verb ends.

3. **Doubted claim at main.go:55-58**: the nocode verb says "two floors: an EXTENSION list, and a NAME list" but --print-deny-list prints the names in different formats (name: vs path:). The separation isn't clear at first glance.

The tool would be a 10 if it had clearer separation between staged and tree modes, showed intermediate quickstart results, and standardized the deny-list output format.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-check quickstart --dir ./self` | intermediate results hidden until verb ends; user doesn't know if links or nocode passed/fail during the run | print LINKS OK/FAIL and NOCODE OK/FAIL lines during quickstart, not just the final summary | S |
| 2 | cmd/nova-check/main.go:89-106 | quickstart help shows verb runs two checks but doesn't show that intermediate results are suppressed | update usage text to say intermediate results are bounded by --fail-max | S |
| 3 | cmd/nova-check/main.go:277-350 | nocode --staged and nocode without --staged have different flag sets but similar help; --staged help is cryptic | add a dedicated section in the help explaining --staged mode separately | S |

## Good, keep
- The refusal grammar is excellent: exit 2 with one-line naming what was wrong and pointing to help
- quickstart is a great first-run verb that needs nothing but a directory
- The --fail-max cap with MORE lines and remedies is well-implemented
