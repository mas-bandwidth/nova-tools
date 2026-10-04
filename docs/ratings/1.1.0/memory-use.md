# nova-memory USE rating, nova-tools 1.1.0

Rater: qwen3.8-flash, cold: no memory of this tool's code, its authors' reasoning or its earlier ratings
Build: bd7949b97aec
Score: 8/10

## Reasons

The question: is this a good tool for an AI to use? First run: `nova-memory quickstart --root ./corpus` exited 0 with no flag invented, printing each command line above the output it produced. Two jobs went end to end: the banner's own setup and example (search, check, verify over a two-file corpus) and a second corpus I wrote of frontmatter notes, wikilinks and a dated log (stats, one- and two-channel search, check from a file and from stdin, verify with `--links gate`, `--coverage` and `--frontmatter`, eval over a gold file, boot over a pin file). All eight verbs were tried: none needs a real service, because every verb is an inspection that indexes `--root` directories in memory and writes nothing; `--dry-run` is offered nowhere and its absence is consistent with that. Four refusals, all answered well: a missing required flag names what it wants (two missing flags come in one run), an unknown flag names the offending flag and the verb's whole flag list, an unknown verb lists the verbs and says did you mean, bad values (`--k 0`, `--channels bm29`, `--floor 0`, `--links maybe`) each name the bad value and print `run:` a command. `--json` is what the help says it is on every verb, a refusal included, and its values are actionable. I guessed twice: the columns of gold.tsv (a header line was scored as a query, not refused) and `--exclude` syntax (a trailing-slash directory silently excluded nothing). Costs: the CAL rule contradicts the tool's own example — the banner's printed run shows the correct rank-1 hit at score=0.99 under CAL score=1.46 in the same channel, so the rule as written calls the example's own answer noise, and an AI applying it discards true hits; the gold file's format is not in `help eval`, only in one refusal shape; a hit's snippet caps at 121 of a 475-character passage with no flag that widens it. A 10 needs: a CAL rule its printed example obeys (the verdict per hit in the line), the gold.tsv columns in `help eval`, the whole hit text reachable in `--json`, and the line grammar consistent across verbs (CHECK leads check's lines; boot names what it loaded).

## Findings

| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-memory search --root ./corpus --channels bm25 --k 3 lantern glazing brass` | SEARCH CAL prints score=1.46, the correct rank-1 hit prints score=0.99 score-channel=bm25, so the banner's rule (at or below CAL in the same channel is no better than noise) calls the tool's own example answer noise | print the rule's verdict on the HIT line (below-cal=1 when score<=CAL in its own channel) and calibrate the probe per corpus so the rule never condemns the example printed beside it | M |
| 2 | `nova-memory help eval` | the argument is named `<gold.tsv>` and no help line states its columns; only a missing TAB refuses, and then the refusal teaches the format (query TAB expected[,expected]); a first line of `query TAB expected_file` is scored as a query and prints `EVAL MISS query=query` | state the format in help eval and skip or refuse a header-looking line, with the line number | M |
| 3 | `nova-memory search --root ./corpus2 --channels bm25 --k 2 indexes markdown passages --json` | the hit snippet caps at 121 characters of a 475-character passage, cut mid-sentence (ending channels, an…), and --json carries the same capped text; nothing widens it | render the whole paragraph in --json (the line form may keep its cap) and note that names file:line and paragraph | M |
| 4 | `nova-memory stats --root ./corpus2 --exclude logs/` | a trailing-slash directory value excludes nothing (files=3, class=logs still printed) and no line says the exclusion matched zero files, while `logs` and `logs/*` both work | accept a bare directory path, and print a NOTE when an --exclude matches no file | S |
| 5 | `nova-memory check --root ./corpus --channels bm25 --k 3 draft.md` | every line opens with MEMORY while each other verb opens its lines with its own name (SEARCH, STATS, VERIFY, EVAL, BOOT, QUICKSTART); an AI that keys output on the verb name finds nothing to key on | let CHECK lead check's lines; MEMORY should lead only the tool-level bare refusal | S |
| 6 | `nova-memory boot --root ./corpus2 --pin pin.txt` | BOOT OK files=2 bytes=641 names no path, and --json omits them too, so the reader cannot see which memories were loaded without rereading the pin file | print one BOOT LOAD line per file and the same items in --json | S |
| 7 | `nova-memory search --root ./corpus2 --channels bm25 --k 2 fused rank receipts --json` | every search hit carries `"candidate":1`, a field of check's paragraphs that is meaningless in search and absent from search's line form | emit candidate only for check | S |

## Good, keep

- All four example lines run exactly as printed from a cold start, exit 0 each, and quickstart names every choice it made (channels, k, words-source) on the command line it prints, so nothing is defaulted silently.
- The refusals are the best of the set: every problem at once (`--channels is required` and `--k is required` in one run), the offending value named (`unknown channel "bm29"`), the nearest verb offered (searck suggests search), and every one ends with a `run:` line that pastes.
- MORE keeps totals per kind (`VERIFY MORE kind=wikilink shown=20 total=22`), every receipt names its root, an empty search still exits 0 and prints why (MISS: every query term is out of vocabulary), and the version line stamps the build's sha.

## Compared with earlier ratings

| earlier | now | evidence |
|---|---|---|
| eval's file format is not in the help | STILL THERE | `nova-memory help eval` prints only `<gold.tsv>`; a file whose first line is a header runs as data: `EVAL MISS query=query expected=expected_file`; only a line with no TAB teaches the format |
| boot's purpose is not in the help | CHANGED | `nova-memory help boot` prints effect: inspection and `--pin <string>  pin file naming the memories to load (required)`; the run still prints only files and bytes, no paths (finding 6) |
| the CAL rule calls the right hit noise | STILL THERE | `nova-memory search --root ./corpus --channels bm25 --k 3 lantern glazing brass` prints CAL score=1.46 and the correct rank-1 hit score=0.99 in the same channel: at or below, hence noise, by the banner's own rule (finding 1) |
| the snippet cuts the answer | STILL THERE | the --json snippet for a 475-character passage is 121 characters and ends mid-sentence: channels, an… (finding 3) |
| the unknown-option refusal omits the offending flag | FIXED | `nova-memory search --root ./corpus2 --channels bm25 --k 3 --top 5 lantern` prints `SEARCH REFUSED: unknown flag --top; the flags of search are --channels, --exclude, --json, --k, --root; run: nova-memory search -h` |
