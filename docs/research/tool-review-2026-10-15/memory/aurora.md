# nova-memory AI-usability review, 2026-10-15

Reviewer: Claude Sonnet 5.5
Build: a404821accb3
Score: 9/10 (49/55)

## Rubric
| # | criterion | score | evidence |
|---|---|---|---|
| 1 | cold | 4 | `nova-memory quickstart --root ./corpus` -> `QUICKSTART OK done=3` (first run from help alone worked; the help lead never says what stats, eval or boot are for) |
| 2 | refusals | 3 | `nova-memory search --root c2 hull` -> `SEARCH REFUSED: --channels is required; refusing to guess; run: nova-memory help` (cause named, both missing flags at once, but the next command is the whole 170-line help, not the corrected line) |
| 3 | output | 4 | `nova-memory search --root c2 --channels bm25 --k 2 --json hull paint` -> `{"result":{"verb":"search","status":"ok","exit":0},"facts":{...` (every verb has --json, refusals included; check prints MEMORY lines and stats prints a timing field build=) |
| 4 | dry-run | 5 | n/a: every verb is an inspection that writes nothing (`nova-memory stats --root c2 --dry-run` -> `STATS REFUSED: unknown flag --dry-run; the flags of stats are --exclude, --json, --root`) |
| 5 | idempotent | 5 | n/a: no verb writes, so there is no op to repeat |
| 6 | no prompts | 5 | `nova-memory check --root c2 --channels bm25 --k 1` -> `MEMORY REFUSED: name exactly one candidate file, or - for stdin` (a missing input is refused, never waited on) |
| 7 | exit codes | 5 | `nova-memory verify --root c2 --links info --frontmatter 'notes/*.md'` -> exit 1 with `VERIFY FAIL gating=1 ...`; bad invocation exits 2; a clean run and a search with no hit exit 0, all as help states |
| 8 | one word | 4 | `nova-memory check --root c2 --channels bm25 --k 2 draft.md` -> `MEMORY OK candidates=1 ...` (the verb is check, its lines are MEMORY; every other verb prints its own name) |
| 9 | push | 5 | n/a: nothing is waited on; each run builds its index and returns |
| 10 | no leaks | 5 | `nova-memory boot --root c2 --pin pinfile` (entry /etc/hostname) -> `BOOT REFUSED: pin entry "/etc/hostname" is absolute; every entry is relative to --root` (paths echo, no content; absolute and ../ pins are refused) |
| 11 | true help | 4 | `nova-memory quickstart --root c2` -> `nova-memory check --root c2 --channels bm25 --k 2 -` (help says each printed line can be edited and run yourself; this one needs the candidate piped on stdin, which is not printed) |

## 10/10 blockers
| # | where | what an AI meets | fix | size |
|---|---|---|---|---|
| 1 | `nova-memory search --root c2 hull` | the next command in nearly every refusal is `run: nova-memory help`, a 170-line page, while only unknown-flag and bad-number refusals say `run: nova-memory search -h` | end every verb refusal with `run: nova-memory <verb> -h`, and the bare-verb refusal keeps `help` | S |
| 2 | `nova-memory search --root c2 --bogus --k two hull` | help says a refusal reports every problem at once, but this stops at `unknown flag --bogus` and never mentions that `two` is not a number | collect the unknown-flag and bad-value findings together before refusing | M |
| 3 | `nova-memory boot --root c2 --pin pins.txt` with two missing entries `a.md` and `b.md` | `BOOT REFUSED: pin entry "a.md" does not exist under --root` names only the first; the second needs another run | list every bad pin entry in one refusal | S |
| 4 | `nova-memory verify --root c2 --links maybe --coverage x` | `VERIFY REFUSED: --links must be gate or info (got "maybe")`; the malformed --coverage is not named until the first is fixed | report both in one refusal | S |
| 5 | `nova-memory quickstart --root c2` | prints `nova-memory check --root c2 --channels bm25 --k 2 -` with no stdin; run as printed it waits for the candidate (the help promises each line is one you can run yourself) | print the demo check with `--draft`-style file or show the pipe, e.g. `printf '...' ` before the line | S |
| 6 | `nova-memory --help` | the lead paragraph names search, check and verify only; stats, eval and boot have no one-line purpose anywhere in the usage block (boot -h says only `checks the pin`) | add one purpose line per verb to the usage block | S |
| 7 | `nova-memory eval --root c2 --channels bm25 --k 2 --floor 0.9 gold.tsv` | `EVAL MISS query=weather\x20forecast expected=notes/tides.md`: a blank in the query is printed as `\x20`, which an AI must know to decode | quote the query (`query="weather forecast"`) as search does | S |
| 8 | `nova-memory check --root c2 --channels bm25 --k 2 draft.md` | the line prefix is `MEMORY` (and `MEMORY CAL`, while search prints `SEARCH CAL`), not `CHECK`; the grammar is one word for every other verb | print `CHECK` for the check verb (help and its example line change with it) | S |
| 9 | `nova-memory search --root c2 --channels bm25,bm25 --k 2 hull` | duplicate channel is accepted and doubles fusion: `fused=0.03333` with `channels=bm25,bm25` | refuse a repeated channel with the same wording as unknown channel | S |
| 10 | `nova-memory stats --root c2 --root c2/notes` | the same files are indexed twice, with no notice: `files=6 chunks=8` for a four-file tree | refuse an overlapping or repeated --root, naming both | S |

## Nice-to-haves
| # | where | what | fix | size |
|---|---|---|---|---|
| 1 | `nova-memory stats --root c2` | `build=416.189µs` varies on every run, so the line cannot be diffed | move the timing to a separate NOTE line or drop it from the JSON facts | S |
| 2 | `nova-memory boot -h` | root flag is `<string>` and described as `memory root directory`, while every other verb says `<value>` and `corpus root directory` | one wording for --root | S |
| 3 | `nova-memory check --root c2 --channels trigram --k 1 draft.md` | a hit of score 0.08 on an unrelated note, below the CAL of 0.11, with nothing marking it as weak | add a `below-cal` field to a hit at or under the CAL line | M |
| 4 | `nova-memory --help` | the exit-code paragraph begins `by verb (each ran here)`, which reads as a leftover | reword to one line per verb | S |

## Top 3 fixes
1. End every refusal with the verb's own `-h` rather than the whole help (blocker 1), the largest single gain on refusals and cold use.
2. Make refusals report every problem at once for bad values and pin entries too (blockers 2, 3, 4), so the help's promise is true.
3. Make quickstart's printed check line runnable as printed and give stats, eval and boot a one-line purpose (blockers 5, 6).

## Good, keep
Quickstart, the printed command lines, and the CAL scale line make a first run self-explanatory; the required --root, --channels and --k with no defaults stay.
Every verb takes --json, a refusal included, with status and exit inside the object; exit 2 for bad invocation and 1 for a finding match the help.
Refusals are specific (unknown channel, empty root, escaping pin, floor out of range) and nothing is ever written.

## What could not be tried
Nothing was left out: all eight verbs (version, quickstart, stats, search, check, verify, eval, boot) ran. There is no dry-run and no write, because the tool only reads. A very large corpus and a file-system under permission errors were not tried.
