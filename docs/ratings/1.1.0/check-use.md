# nova-check USE rating, nova-tools 1.1.0

Rater: deepseek-v4.1-flash
Build: e77c0df08f00
Score: 8/10

## Reasons
nova-check is a clean, honest inspection tool. Every verb names its required flags, reads only what it is told to read, and separates a bad invocation (exit 2, "refusing to guess") from a failed check (exit 1). The refusals name every missing required flag at once, each with its own one-line reason; --json is one object on stdout; --dry-run writes nothing. Two silent traps keep it from 9: JSON stops its findings at the default fail-max with no marker, and --exclude is relative to --dir in a way the help does not say, so the obvious path excludes nothing. A 10 would add: a shown/truncated marker in every JSON facts object; the exclude base stated (or both bases accepted); an offline --dry-run for convergence; refusal pointers to the verb's own help; all parse errors named at once; and a warning when a check scans zero files.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-check links --dir ./many --json` | items stop at 20 by default while facts.broken says 25, with no shown or truncated field, so a parser undercounts silently | add shown and truncated to the JSON facts, or leave JSON untruncated | M |
| 2 | `nova-check links --dir ./emptydir` | a directory with no markdown reports `LINKS OK files=0 links=0` and exits 0, a green over zero files; spelling does the same | warn when a check classifies nothing, as nocode already does with its NOTE line | M |
| 3 | `nova-check links --dir ./lex --exclude ./lex/sub` | the exclude prefix is relative to --dir, not the cwd; the path the user sees silently excludes nothing and reports excluded=0 | state the base in the --exclude help, or accept a path that starts with --dir | S |
| 4 | `nova-check convergence --repo owner/name --ledger ./conv-ledger.md --receipts ./receipts --retired ./retired.md --since 24h --dry-run` | dry-run still runs `gh pr list` first, so an offline card cannot exercise the reading at all | with --dry-run mark the forge streams ABSENT instead of reading them | M |
| 5 | `nova-check links --dir ./lex 2>/dev/null` | plain findings go to stderr and only OK lines go to stdout, so a reader that takes stdout alone sees an empty result on failure | state the stream contract in help | S |
| 6 | `nova-check links --bogus` | the next command offered is the global `nova-check help`, over a hundred lines, not `nova-check help links` | point refusals at the verb's own help | S |
| 7 | `nova-check links --bogus --alsobad` | only the first unknown flag is named, not every problem at once | collect and print all parse errors | S |
| 8 | `nova-check quickstart --dir ./badself` | the summary line goes to stdout while the per-check FAIL lines go to stderr, so a captured stream can show the summary before its detail | order the summary after the details, or repeat the detail in the summary | S |

## Good, keep
Refusals name every missing required flag at once, each with a one-line reason, and use exit 2 distinct from a failed check's exit 1.
--json is one clean object on stdout (result, facts, items), and --dry-run writes nothing for spelling and dogfood record.
The layered help (`help`, `help <verb>`, `-h`) carries each verb's flags, effect and exit codes.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| green over zero files (links, spelling) | STILL THERE | `nova-check links --dir ./emptydir` prints `LINKS OK files=0 links=0 excluded=0` and exits 0 |
| two refusals name one problem | FIXED | `nova-check corpus` names all three missing flags, each with its own reason line, and exits 2 |
| every remedy is the whole help | CHANGED | refusals still end `run: nova-check help`, but now add a per-problem explanation line |
