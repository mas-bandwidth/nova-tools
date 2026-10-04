# nova-decide USE rating, nova-tools 1.1.0

Rater: glm-5.3-flash
Build: e77c0df08f00
Score: 8/10

## Reasons
Cold USE rating from the binary and its help alone, every command run in a scratch directory with no key, no store and no network: the fixed backend made the whole lifecycle real. Ask over a schema and a state written for this rating, then read over a card and a diff, then score, then grade, attempt and gate over a recorded go test output: each matched its help on the first or second try, and the two wrong guesses at an answers file were refused with the fix in the message. The record's verbs ran over that record: outcome labels, the conflict path (exit 1, the recorded label and its time named), calibrate with a noul question and with verdict=BOUNCE, findings over two score decisions (one FINDING line, class and cards named). Four refusals provoked: a missing required flag names every missing flag at once with what each wants; an unknown flag lists the verb's flags; an unknown verb lists the verbs; a bad value (`--bar 2`, `--backend wat`, `--timeout notaduration`) names the want; each refusal ends with the next command to run. --json gives one actable object on every verb tried, refusals included (they carry a remedy field); --dry-run writes nothing (record line counts stayed put) and --op replay returns recorded=existing with the record unchanged. What keeps it from 9 and 10: brief cannot be driven with the fixed backend from the help alone (finding 1); --dry-run omits the answers a real run prints (finding 2); calibrate's scoring direction and two defaults are undocumented (findings 3 and 4); help trips on --json (finding 5). A 10 needs those fixed and the jev backend judged by running it. Not tried: every --backend jev run (no key, no network) and brief's real run (blocked by finding 1); both are judged from their help and --dry-run.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-decide brief --card brief.md --backend fixed --answers brief-answers.json --record rec3.jsonl` | the help names the minutes question but none of its options, and the refusal says minutes chose "long", not one of its options without naming them; twelve plausible options (15, 30, 60, 120, 15-30, under-15 and more) were all refused, so a fixed-backend caller cannot author the answers from the help alone | name minutes' options in `nova-decide help brief`, and name them again in the BRIEF CARD error | M |
| 2 | `nova-decide ask --schema schema.json --state state.txt --backend fixed --answers answers.json --record rec.jsonl --op j2 --dry-run` | the dry-run prints only the OK line (recorded=no dry_run=true); the ANSWER lines a real run prints are absent, so a caller cannot preview what the fixed backend would answer without recording a decision | have --dry-run print the answers the run would record when the backend is fixed, and say answers need a real run when it is jev | S |
| 3 | `nova-decide calibrate --record rec.jsonl --decision shipit --question safe --positive ok --negative wrong --bars 0.5,0.8` | the help never says a noul is scored by its p of yes with higher counting as flagged; with a positively named noul (safe) the labels work inverted and the AUC still prints, so a caller can calibrate backwards without any warning | state in `nova-decide help calibrate` that the question must be phrased so yes is the thing flagged, defect-style | S |
| 4 | `nova-decide calibrate --record rec.jsonl --decision shipit --question safe --positive ok --negative wrong --json` | run with no --bars it reports bars 0.5, 0.7 and 0.9, and findings counts classes from --bar 0.5 when none is given, but neither help states its default | state the default bars in `nova-decide help calibrate` and the default bar in `nova-decide help findings` | S |
| 5 | `nova-decide help --json` | the help says every verb takes --json, but help reads the flag as a verb name (unknown verb "--json", exit 2) and `nova-decide help ask --json` silently ignores the trailing flag | let help accept --json like every verb, or refuse unknown flags after the verb with the flags help takes | S |

## Good, keep
Every refusal names the problem, what it wanted, and the next command (`run: nova-decide ask -h`), and missing things are named all at once: the score refusal listed all ten missing questions in one line with the remedy.
Every verb's help ends with an `effect:` line (delivery, local write, inspection), so an AI sees what touches the world before it runs anything.
--op replay is real idempotency (the same id returned recorded=existing and the record kept one line) and --dry-run provably wrote nothing.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| no earlier rating | CHANGED | the first rating of this tool |
