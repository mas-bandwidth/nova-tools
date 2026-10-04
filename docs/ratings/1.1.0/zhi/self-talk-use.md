# nova-self-talk USE rating, nova-tools 1.1.0

Rater: a large language model, cold to this tool
Build: 99e4a903966c
Score: 9/10

## Reasons

The first successful run `nova-self-talk example ./pages` exits 0, writes RULES.md and journal.md, and prints the exact next command. The scan of the generated journal then exits 1 with two findings, each a `SELFTALK FAIL <file>:<line>: <CLASS> match="<words>": <sentence>` line, and closes with totals and the NOTE that a green clears known shapes only. A second, different job: `nova-self-talk --skip journal.md ./pages/journal.md` skips the file, exits 0, and says `SELFTALK SKIP files=0 skipped=1 reason=all-skipped`, never a false OK.

The four refusals all behave and exit 2: no files gives `nova-self-talk REFUSED: no files named; refusing to guess; run: nova-self-talk help` with an explanation of the default; an unknown flag lists the flags; a typo verb is read as a file and the refusal says it is not a verb either; a bad --max value names the parse error and the next command. `--json` returns one object with status, counts and one item per finding carrying file, line, shape, match and text, so a program can act on it without guessing. `example --dry-run` prints `EXAMPLE OK ... would-write=- kept=RULES.md,journal.md` and writes nothing.

What costs the score: `nova-self-talk shapes` prints the pattern column as raw regex with `\b` escapes; a cold user cannot read a row's rule without already knowing regex, so the one verb that exists to make the table legible is only half legible. The `--rule-doc` banner works, but its exit is the same as the findings' (1), so a caller cannot tell a rule-doc failure from a scan failure without reading the banner line.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-self-talk shapes` | the pattern column is raw regex (`(?i)\b(fallib...`) and a cold user cannot verify a row without regex | add a plain-language gloss column or drop the pattern to a shapes --verbose flag | S |
| 2 | `nova-self-talk --rule-doc rule.md rule.md` | a rule-doc finding exits 1 exactly like a scan finding, so callers must parse the RULEDOC banner to tell them apart | add a distinct exit or a result field for rule-doc findings | S |
| 3 | `nova-self-talk example --dry-run ./pages` | dry-run prints kept=RULES.md,journal.md but not what would be written inside them | list the files' content sizes or first lines in dry-run | S |

## Good, keep

The generated example pages that are themselves the test case. The JSON items that carry file, line, shape, match and text per finding. The NOTE printed on every run so a green is never overclaimed.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| raw Go flag errors | FIXED | `nova-self-talk --bogus ./pages/journal.md` exits 2 with the tool's own refusal |
| a green over a binary file | CHANGED | `nova-self-talk` on a binary or missing file refuses with `cannot read`, never a green |
| the unknown-option refusal omits the offending flag | FIXED | `nova-self-talk --bogus ...` names `--bogus` and lists the flags |
