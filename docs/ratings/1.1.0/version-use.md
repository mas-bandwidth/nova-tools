# nova-version USE rating, nova-tools 1.1.0

Rater: GLM-5.3-flash
Build: bd7949b97aec
Score: 8/10

## Reasons
The help carries the rating: `nova-version help` states what the tool is for, one usage line per verb, the exit codes (0 passed, 1 said NO, 2 could not run) and, per verb, an effect line that says before the run what it touches (inspection, local write, delivery); every success ends with a next-step note (`EXAMPLE NOTE next: nova-version report --file versions.tsv`). A cold AI runs a first job inside a minute: `nova-version example --out versions.tsv`, then `nova-version snapshot --file versions.tsv`, then `nova-version report --file versions.tsv`, each exit 0 with counts that match what happened.
Refusals are the other strong part: bare `nova-version diff` names --from and --to at once, each with the remedy; a manifest with two bad lines names line 3 and line 5 together; an unknown flag lists the verb's flags; an unknown verb lists all seven verbs; bad --max, --timeout and --kind values each state the wanted format; every refusal ends with `run:` and the command to run next. The exit-1 UNKNOWN path gives a remedy (`install nosuchbinary123 or supply its executable path; searched PATH=...`).
The machine contract holds where it is offered: --json returns the same result as one object with facts and items, the MORE line says how to raise the ceiling, --dry-run is honored and announces what it did not write, `example` never overwrites an existing file (unchanged=true), and diff tells the truth about a directory that lost tools (18 changed, each to=-).
The score loses for: moved's counts (added, deleted, renamed, verbs) are unexplained in help, note and JSON, so its one-line note cannot be read without guessing; snapshot's --dry-run still demands an --out it never writes; send's flag list offers a --draft the verb then refuses; report takes no --json (the help states this) so its only machine form is one long key=value line with escaped bytes. A 10 needs the moved counts defined with the counted rows in the note, a --dry-run that needs no invented --out, no unusable flag in a flag list, and a machine-readable report.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-version moved --from c28448a54d56 --to bd7949b97 --repo <checkout> --out note.md --dry-run` | exits 0 with `added=0 deleted=0 renamed=0 verbs=226 file=note.md dry_run=true`, but no line of help, note or JSON says what any count counts (the note file holds the single MOVED line), so the AI cannot tell what changed between the revisions without guessing | define each count in `help moved` and write the counted rows into the note and the JSON items, not bare totals | M |
| 2 | `nova-version snapshot --bin ./bin --dry-run --max 3` | exit 2, refused for missing --out although --dry-run writes no --out (given a path it prints `SNAPSHOT NOTE dry run: unused.tsv not written`); the AI must invent a path it never uses | let --dry-run list the rows without --out | S |
| 3 | `nova-version send --file versions.tsv --as tester --to a --bus ./nobus --remote origin --branch main --draft` | `send -h` lists `--draft` (print the note only), the usage line never offers it, and the run answers `SEND REFUSED: --draft and --send are exclusive (choose one)`; the flag list contradicts the usage line and the only draft form is refused | drop --draft from send's flag list, or let send --draft print the note as report --draft does | S |
| 4 | `nova-version report --file versions.tsv --json` | refused (`unknown flag --json`) while every other verb takes --json; report's only machine form is one key=value line with escaped bytes (`raw=go\x20version\x20go1.26.6\x20linux/amd64`), which the AI must parse by hand | give report a --json on its local path (without --send) with the facts and items shape the other verbs use | S |

## Good, keep
Refusals that name the problem, every problem of a kind at once, and the next command: bare diff names --from and --to together, a manifest with two bad lines names lines 3 and 5 together, and every refusal ends with `run:` plus the command to run next.
Per-verb help with usage, flags, exit codes and an effect line (inspection, local write, delivery) that says before the run what the verb touches, plus a next-step note after every success.
The honest machine contract: --json matches the lines, MORE explains how to raise it, --dry-run announces what it did not write, exit codes mean what the help says, and the UNKNOWN path names the remedy and the searched PATH.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| snapshot --bin names one problem where there are several | CHANGED | bare `nova-version diff` prints two DIFF REFUSED lines (--from then --to) and a manifest with two bad lines is refused naming lines 3 and 5 together; a bad value still ends the list early: `nova-version snapshot --dry-run --timeout 5x` names only --timeout |
| moved's counts are unexplained | STILL THERE | `nova-version moved --from c28448a54d56 --to bd7949b97 --repo <checkout> --out note.md --dry-run` prints `added=0 deleted=0 renamed=0 verbs=226`; `help moved`, the one-line note and the --json facts never say what the counts count |
| report --json is refused | STILL THERE | the run answers `REPORT REFUSED: unknown flag --json; the flags of report are --as, --branch, --budget, --bus, --draft, --file, --host, --kind, --max, --remote, --send, --snapshot, --timeout, --to; run: nova-version report -h`, and the help states up front that report and send take no --json |
| another earlier rater: snapshot, diff, report and JSON refusals worked | STILL THERE | all reproduce at this head: snapshot names the missing --out with the remedy, diff names --from and --to at once, report names the manifest line numbers, unknown --json names the flag list, every refusal ends with `run:` |
