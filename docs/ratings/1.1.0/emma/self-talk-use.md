# nova-self-talk USE rating, nova-tools 1.1.0

Rater: gemini-2.5-pro
Build: 2c02b2aa2042
Score: 9.5/10

## Reasons
The command-line workflow of nova-self-talk is coherent, fast, and completely runnable without external services or network access. A newcomer can begin with the example verb, which extracts built-in markdown test fixtures into a chosen directory and suggests the exact next command to execute.

Running two realistic workflows proved straightforward:
1. Scanning an agent journal (`nova-self-talk ./pages/journal.md`) correctly identified both standing capability claims and neutral-worded trait shapes, outputting precise line references and matching snippets alongside counted records.
2. Checking policy files with `--rule-doc RULES.md` displayed the advisory relocation banner above rule findings, and `--skip RULES.md` bypassed the file cleanly with an explicit skip line.

All four required refusals were provoked and behaved helpfully:
- Missing required argument (`nova-self-talk`) refused with a multi-line explanation of expected file arguments and suggested using the example command.
- Unknown flag (`nova-self-talk --bogus`) rejected the flag while enumerating all supported options.
- Unknown verb (`nova-self-talk bogus`) noted that the target path does not exist and listed the full set of recognized verbs.
- Bad value (`nova-self-talk --skip path/to/file.md`) explained that the flag accepts a basename rather than a full path.

Both `--dry-run` and `--json` produced expected output structures matching their help descriptions. No verbs required unsimulated real services; all verbs (scan, shapes, example, version, help) were exercised in the scratch directory.

To achieve a 10/10 rating:
- The tool should detect non-text or binary files and refuse them rather than reporting a misleading green status.
- Argument parsing could permit flags after positional files or offer automatic path normalization for basename flags.

## Findings
| # | where | finding | fix | size |
| 1 | `nova-self-talk /bin/ls` | Scanning a binary executable produces SELFTALK OK with zero claims instead of refusing non-text data | Check the initial byte stream for null bytes or invalid UTF-8 and refuse binary inputs | S |
| 2 | `nova-self-talk ./pages/journal.md --max 10` | Providing flags after positional filenames causes an immediate refusal | Accept trailing flags or add an explicit suggestion in the refusal note to place flags first | S |
| 3 | `nova-self-talk --skip ./RULES.md ./pages/RULES.md` | Supplying a path separator to a basename flag refuses rather than normalizing the filename | Automatically extract the basename with filepath.Base while keeping the match rule intact | S |

## Good, keep
- The example command generates offline sample pages immediately and prints the exact next verification command.
- Refusals validate all positional paths up front and report every unreadable file in a single turn.
- Rich `--json` output provides identical structural counts and advisory notes across both passes and findings.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| raw Go flag errors | FIXED | `nova-self-talk --bogus` outputs a formatted refusal listing all supported flags |
| a green over a binary file | STILL THERE | `nova-self-talk /bin/ls` prints SELFTALK OK files=1 claims=0 and exits 0 |
| the unknown-option refusal omits the offending flag | FIXED | `nova-self-talk --unknown` includes unknown flag -unknown in the refusal output |
