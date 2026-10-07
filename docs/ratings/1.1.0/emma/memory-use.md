# nova-memory USE rating, nova-tools 1.1.0

Rater: gemini-2.5-pro
Build: 2c02b2aa2042
Score: 8.5/10

## Reasons
The tool is fast, responsive, and intuitive to use. Setting up a corpus and running quickstart demonstrates the core functionality immediately, showing the exact commands executed alongside their outputs. Search and check return deterministic rankings with clear attribution to source files and lines. Refusals are exceptionally helpful: omitting required flags produces clear error messages reporting every missing flag at once with copy-pasteable next steps.

A score of 10 would require documenting the TSV format for eval in help, clarifying boot behavior, allowing configurable or full snippet lengths in output receipts, normalizing calibration thresholds across query lengths, and extending JSON support to verification and benchmark verbs.

All verbs were exercised locally in the scratch directory without external network or store dependencies.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-memory search --root ./corpus --channels bm25 --k 1 optics` | snippet is truncated to 120 characters in text and json output requiring callers to inspect raw files | provide a full or snippet-len flag to allow callers to receive entire passages | M |
| 2 | `nova-memory help eval` | command help for eval omits the required TSV file structure query-tab-expected | document expected gold benchmark column format in eval help | S |
| 3 | `nova-memory help boot` | command help describes boot as loading memories into context when it only validates existence and byte totals | document that boot checks file validity without printing or returning file contents | S |
| 4 | `nova-memory quickstart --root ./corpus` | exact matching hits score lower than calibration threshold because probe is long while search words are short | calibrate probe score against query term count to avoid labeling real hits as noise | S |
| 5 | `nova-memory stats --root ./corpus` | inspection and verification verbs do not support structured json output | implement json output across stats verify and eval | M |
| 6 | `nova-memory search --unknown-flag` | flag refusal message reports single hyphen prefix for flags passed with double hyphens | preserve caller option spelling in refusal error messages | S |

## Good, keep
Quickstart demonstrates stats search and check end to end while printing runnable command lines for immediate reuse.
Missing flag refusals report all omitted arguments at once with explanatory usage hints and exit code 2.
Evaluations provide objective recall and mean reciprocal rank scores over gold fixtures without modifying corpus files.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| eval's file format and boot's purpose are not in the help | STILL THERE | `nova-memory help eval` omits tab syntax and `nova-memory help boot` omits validation-only behavior |
| the CAL rule calls the right hit noise | STILL THERE | `nova-memory quickstart --root ./corpus` yields hits scoring 0.59 below CAL 1.46 |
| the snippet cuts the answer | STILL THERE | `nova-memory search` truncates snippets to 120 characters with ellipsis |
| the unknown-option refusal omits the offending flag | FIXED | `nova-memory search --unknown-flag` names flag provided but not defined: -unknown-flag |
