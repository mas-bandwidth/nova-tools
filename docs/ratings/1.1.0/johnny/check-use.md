# nova-check USE rating, nova-tools 1.1.0

Rater: Grok
Build: 2c02b2aa2042
Score: 7.5/10

## Reasons

The first run is the one the banner prints. `nova-check quickstart --dir ./self`, after the two setup lines in `nova-check help`, exits 0 and prints `QUICKSTART OK done=2 worst-exit=0` with `LINKS OK files=1 links=0 excluded=0` and `NOCODE OK files=1 clean deny-list=floor-list`. `nova-check links --dir ./self` and `nova-check kernel --file ./self/docs/SEED-CORE.md --max-bytes 4000` then print `LINKS OK` and `KERNEL OK bytes=9 budget=4000`. A bare `nova-check` exits 2 and names quickstart as the first run.

Two jobs, both in a scratch directory. Links: a two-file tree with one broken relative link. `nova-check links --dir ./records` exits 1 and prints `LINKS FAIL index.md:3: gone.md (does not exist)` plus `LINKS FAIL files=2 links=3 broken=1 shown=1 excluded=0`. After the link is removed, the same command exits 0. Spelling: `nova-check spelling --file ./prose/note.md` exits 1 on `teh -> the` and does not flag a token inside a fence. `--write` prints `SPELLING FIXED` and leaves the fence alone. A second run exits 0.

Refusals. A missing `--dir` names the flag, says refusing to guess, and adds a hint of what the directory is. `nova-check attest` with no flags names both `--home` and `--manifest` in one run, each with a hint. An unknown verb lists the verbs. `--fail-max -1` and `--max-bytes 0` each name the bad value and the legal range. `nova-check convergence` with no flags names all five required flags and what each wants, and does not reach a forge. That verb's real read was not tried: the help says LANDING and PRS go through gh, and the help offers no `--dry-run`.

What keeps the score at 7.5. An empty directory is a green for links and for spelling, with `files=0`. A wrong flag beside a missing flag is reported as one problem. The `run:` door is `nova-check help`, about 8 KB, while `nova-check links -h` is under 1 KB. Spelling calls itself a misspelling check and then passes `sentance`. `--json` on links, spelling and hygiene matches the lines and is actionable; quickstart rejects `--json`; nothing in the help offers `--dry-run`, including on `spelling --write`.

Guessed once: `nova-check floors` on two files shaped only from the banner's last two lines exits 1 and names the headings it wanted (`## The floors`, `## 0.`, `## 6.`), so a second turn can proceed. Attest and corpus ran from the banner's one-line formats without a second guess. Hygiene, on a two-commit repository built for the trial, named an out-of-path file.

A 10 would refuse a scan of no files, report every problem in one invocation, point `run:` at that verb's `-h`, and say in the spelling line that the list is known typos.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-check links --dir ./empty` | Exit 0 and `LINKS OK files=0 links=0 excluded=0`. `nova-check spelling --dir ./empty` and the same command on a directory of only a text file are the same green. `nova-check nocode --dir ./empty` still exits 0, but it does write a stderr note that nothing was classified. An AI can treat the wrong directory as a pass. | Exit 2, or print that note and a non-zero status, when the scan saw no files. | S |
| 2 | `nova-check links --fail-max -1` | The only line is `--dir is required`. The illegal ceiling is not mentioned. `nova-check links --nope` names the unknown flag as `-nope` and does not also say `--dir` is missing. A bare `nova-check hygiene` names `--repo`, `--base` and `--head`, and names `--identity` only on the next run. | On one invocation, name every missing flag and every bad value, and do not stop at the first class of problem. | S |
| 3 | `nova-check spelling --file ./prose/only.txt` | The file is the single word-error `sentance`. The command exits 0 with `SPELLING OK files=1 misspellings=0 excluded=0`. The same checker does catch `teh` and does leave a fenced token alone. The help line says it checks prose for misspellings, which this green does not mean. | Say known typos in the usage line, so a green is not read as a dictionary pass. | S |
| 4 | `nova-check links --dir ./records --dry-run` | The help never offers `--dry-run`. The flag is refused as unknown, including on `spelling --write`, which edits the file in place. `nova-check quickstart --dir ./self --json` is the same refusal, while the banner says that verb has typed lines only. Every `run:` door observed is `nova-check help`, not `nova-check links -h`. | Point the door at the verb's own `-h`, and say in one line that this binary has no dry run. | S |
| 5 | `nova-check dogfood gate --tools ./bin --receipts ./receipts --allow-empty` | The banner says gate exits 1 for verbs no non-author has run. This run exits 0 with `DOGFOOD GATE OK verbs=261 ... require-all=no`. Adding `--require-all --fail-max 2` exits 1, shows two FAIL lines, and prints `DOGFOOD MORE kind=verb shown=2 total=260`. A green ledger with `--fail-max 3` still prints one row per verb of every binary, 261 lines, and the ceiling does not shorten it. | Make the gate synopsis name `--require-all`, and give the ledger a bounded summary that does not require reading every row. | S |

## Good, keep

`nova-check links --dir ./records --json` on a broken link returns one object whose item has file, line, target and reason, the same facts as the FAIL line. `nova-check attest` with no flags names both missing flags and what each file is. `nova-check quickstart --dir ./prose2` runs links and then nocode, and the closing line is `QUICKSTART FAIL` when only nocode fails, not OK.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| a green over zero files (spelling, links) | STILL THERE | `nova-check links --dir ./empty` prints LINKS OK files=0 links=0 excluded=0 and exits 0; `nova-check spelling --dir ./empty` prints SPELLING OK files=0 misspellings=0 excluded=0 and exits 0 |
| two refusals name one problem | STILL THERE | `nova-check links --fail-max -1` prints only that --dir is required, and does not mention the ceiling |
| every remedy is the whole help | CHANGED | a missing --dir still ends run: nova-check help, and now adds one hint line that says what --dir is; an unknown flag still has only that door |
| link findings, spelling fixes, JSON and dry-run as documented | CHANGED | `nova-check links --dir ./records` prints LINKS FAIL index.md:3: gone.md (does not exist); spelling --write prints SPELLING FIXED for teh; links --json matches those fields; `nova-check links --dir ./records --dry-run` is refused and the help offers no dry-run |
