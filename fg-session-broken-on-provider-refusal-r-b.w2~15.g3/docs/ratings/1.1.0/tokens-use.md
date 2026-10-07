# nova-tokens USE rating, nova-tools 1.1.0

Rater: deepseek-v4.1-flash
Build: bd7949b97aec
Score: 9/10

## Reasons
nova-tokens is written for a machine caller. The top-level help is one page that states the whole contract: the flags of every verb, the five token types, the dash rule, the exit codes, and a copy-paste first-run recipe. That recipe runs verbatim, and fold, check, sum, sources, report and session all succeed on the first attempt with no guess. Every refusal names the problem, names every missing flag at once, and prints the next command to run. Every reading verb takes --json and every writing verb takes --dry-run, and the dry-run is the real run's own plan, not a separate sketch. The reasons it is not a 10 live in the machine interface. --json changes the type of the same fact between verbs and even inside one object, so a caller must coerce field by field and cannot trust one schema. A missing-flag refusal sends the reader to the global help page instead of the verb's own -h, while the bad-value and unknown-flag paths already point at the verb. A 10 needs one JSON type per fact across every verb and a remedy that points at the verb being built.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-tokens sum --out ./out --month 2026-09 --json` | the same fact changes type across verbs and within one object: sum prints input as the string "812" and turns as "1" while days prints as the number 1, and fold prints dashes as the number 1 where sum prints it as "0,0,0,0,1". An AI parsing the JSON must coerce field by field and cannot trust one schema. | emit every numeric fact as a JSON number, and keep one type per fact name across all verbs. | S |
| 2 | `nova-tokens fold --out ./out --day 2026-09-11 --json` | the refusal names both missing flags but sets remedy to nova-tokens help, the global page, where the unknown-flag path names the verb's own -h. An AI building a fold is sent to the long page instead of the flags of the verb it is building. | set the missing-flag remedy to the verb's own -h, as the bad-value and unknown-flag paths already do. | S |
| 3 | `nova-tokens report --who rater --day 2026-09-11 --repos ./repos.tsv --claude bench=./transcripts --dry-run` | the note body goes to stdout and the dry_run=true and REPORT OK lines go to stderr, so an AI that captures stdout to take the note never sees the dry-run confirmation the help promises on the last line. | carry the dry-run marker in the payload, or state in the help that report writes its status to stderr. | S |
| 4 | `nova-tokens version --json` | version refuses --json with "takes no flags and no arguments, got 1", which counts the flag as an argument and never names --json. The help does say only version takes no --json, but the message does not. | name the offending flag in the refusal, as the other verbs do. | S |

## Good, keep
- The first-run recipe in the top-level help runs verbatim; every command in it succeeds.
- Refusals name the problem, every problem at once, and the next command; a bare fold prints all four missing flags in one run.
- --dry-run is the real run's plan and --json carries the same facts as the lines, a refusal included.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| REPORT OK with exit 1 | FIXED | `nova-tokens report --who rater --day 2026-09-13 --repos ./repos.tsv --claude bench=./transcripts` prints REPORT FAIL who=rater day=2026-09-13 rows=0 and exits 1, while the 2026-09-11 run prints REPORT OK who=rater day=2026-09-11 rows=4 and exits 0 |
| subject= unquoted with spaces | STILL THERE | `nova-tokens report --who rater --day 2026-09-11 --repos ./repos.tsv --claude bench=./transcripts` prints REPORT OK who=rater day=2026-09-11 rows=4 at=2026-10-04T04:11:31Z build=v1.0.1-0.20261004040111-bd7949b97aec subject=tokens 2026-09-11 at=2026-10-04T04:11:31Z build=v1.0.1-0.20261004040111-bd7949b97aec, with the subject value unquoted |
| --json numbers as strings | STILL THERE | `nova-tokens sum --out ./out --month 2026-09 --json` prints "input":"812" and "turns":"1" as strings beside "days":1 as a number |
| the unknown-option refusal omits the offending flag | CHANGED | `nova-tokens sum --out ./out --month 2026-09 --nope` now prints SUM REFUSED: unknown flag --nope; the flags of sum are --json, --max, --month, --out, but `nova-tokens version --json` still says got 1 without naming the flag |
