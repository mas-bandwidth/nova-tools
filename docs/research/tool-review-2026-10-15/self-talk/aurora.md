# nova-self-talk AI-usability review, 2026-10-15

Reviewer: grok-build-0.1
Build: c0cf1f631b0e
Score: 9.5/10 (52/55)

## Rubric
| # | criterion | score | evidence |
|---|---|---|---|
| 1 | cold | 5 | `nova-self-talk help` -> `first run: nova-self-talk example ./pages writes the example pages from the binary itself;` |
| 2 | refusals | 4 | `nova-self-talk --maxx 3 testpage.md` -> `nova-self-talk REFUSED: unknown flag -maxx; the flags are --skip, --rule-doc, --max, --json; run: nova-self-talk help` |
| 3 | output | 5 | `nova-self-talk --json testpage.md` -> `{"result":{"verb":"scan","status":"failed","exit":1},"facts":{"files":1,"skipped":0,"claims":1,"standing":1,"installations":1,"dated":0,"shown":2},...}` |
| 4 | dry-run | 5 | `nova-self-talk example --dry-run drytest` -> `EXAMPLE OK dir=drytest would-write=RULES.md,journal.md kept=-` (and dir empty after) |
| 5 | idempotent | 5 | n/a: no persistent-state write verbs; example (the only writer) re-runs safely with kept= (no --op surface) |
| 6 | no prompts | 5 | `nova-self-talk testpage.md` -> direct output and exit 1, no wait for stdin or answer |
| 7 | exit codes | 5 | `nova-self-talk testpage.md; echo $?` -> 1; `nova-self-talk nonverb; echo $?` -> 2; clean sentence file -> 0 (and inside --json result.exit) |
| 8 | one word | 4 | `nova-self-talk scan <file>` and bare `nova-self-talk <file>` expose the same scan two ways |
| 9 | push | 5 | n/a: no waited state or poll; all verbs are local synchronous reads or one local write |
| 10 | no leaks | 5 | `nova-self-talk testpage.md` and error cases emit only named files and matched text, no env or secret |
| 11 | true help | 4 | `nova-self-talk help nonverb` -> full banner (exit 0, no "unknown verb" signal); all other claims (first-run, exits, --json shape, dry-run, MORE, all-skipped, classes, flags) held on run |

## 10/10 blockers
| # | where | what an AI meets | fix | size |
|---|---|---|---|---|
| 1 | `nova-self-talk help nonverb` | prints the full general help text and exits 0, with no signal that the topic is not a verb; an AI cannot tell documented from never-heard | refuse an unknown help topic with the verb list, as the scan path already does for a bare non-verb | S |
| 2 | `nova-self-talk shapes --jsonx` | verb path keeps raw `flag provided but not defined: -jsonx; the one flag is --json; run: ...` while bare/positional path uses `unknown flag -maxx; the flags are --skip, --rule-doc, --max, --json; run: ...` | route the verb path through the same curated refusal as the positional path | S |
| 3 | `nova-self-talk quote.md` (containing `She wrote: "I am the best reviewer here."` followed by `Keep my weakest instrument in view.`) | one SELFTALK FAIL at :1 whose text spans both lines, match on second line's words; the licensed imperative is flagged because join broke the QUOTED licence | end a sentence at a closing quotation mark; attribute each finding to the physical line its matched words sit on | M |
| 4 | `nova-self-talk --maxx 3 --foo 9 testpage.md` | refusal names only the first problem and remedy; second flag error is hidden until the first is corrected and the command is re-run | report every problem at once in one refusal line | S |
| 5 | `nova-self-talk example ./d` | the write verb offers no --op; while re-run keeps, there is no idempotency token a caller can pass for safety across runs | give the local write --op (or n/a the criterion if fixture writers stay out of scope) | S |
| 6 | `nova-self-talk nul2.dat` (NUL inside otherwise clean text) | prints `SELFTALK OK files=1 ...` exit 0 (a green over bytes a sentence scanner never meant to treat as prose) | refuse a file whose content is not text (NUL anywhere, or invalid utf8 already does) | S |
| 7 | `nova-self-talk scan -h` | usage says `usage: nova-self-talk scan [flags]` (drops the required <file>...) ; -h for verbs end with the scan exit table including all-skipped | print each verb's true usage line and its own exit codes | S |

## Nice-to-haves
| # | where | what | fix | size |
|---|---|---|---|---|
| 1 | `nova-self-talk help` | documents both the bare form and the named `scan` verb for identical behaviour | keep one surface as primary (bare matches the mental model "name files") or mark scan as alias only | S |
| 2 | every run | the NOTE is useful but always the same 2-line block on stdout | offer --no-note or move NOTE under --verbose | S |

## Top 3 fixes
1. refuse unknown `help <topic>` with verb list and exit 2 (lifts refusals and true-help scores)
2. unify unknown-flag wording and always report every flag problem in one refusal
3. fix sentence end after closing quote so quoted blocks do not swallow following lines and licensed text stays un-flagged

## Good, keep
The NOTE printed on every run ("catches known SHAPES only...") so a green is never misread as complete.
The "NOTHING was scanned, which is not a green" appended to unreadable-file refusals.
The two-class table and the licence rules in the banner, with the exact finds= and passes= sentences exercised by `nova-self-talk shapes` and the tests.

## What could not be tried
n/a: all surfaces exercised (bare scan, scan verb, shapes, example+--dry-run, version, help, --json on three, --max/--skip/--rule-doc, stdin -, -- for dashfile, four+ refusal cases, clean+flagging scratch files).
