# nova-memory USE rating, nova-tools 1.1.0

Rater: abliterated-model-large-v2
Build: 044b5dfe9c1b
Score: 8.5/10

## Reasons

Used cold from `nova-memory help` alone, on a bakery corpus I wrote under the
job's scratch directory (seven files: notes with frontmatter, a dated log, a
handbook). Every verb ran, first try, and no verb needs a service: no store,
no network, no key, so nothing was left to judge from help alone. The first
run was the quickstart line from the banner; it printed each step's command
above its output and closed by saying its choices were made once — I had the
whole tool in one screen. Job one, the tool's reason to exist: a two-paragraph
draft checked against the corpus — the paragraph paraphrasing an existing note
scored 29.48 against a calibration band of 3.82 with the right file and line
on the receipt, and the genuinely new paragraph's best receipt scored 3.21,
below the band, which is exactly the answer "not yet known". Job two, the
self path: a boot pin of three memories (BOOT OK files=3 bytes=1297), then a
two-root search across the corpus and a distilled cairn, each receipt naming
its root. Refusals: three missing things reported in one run, each with a
hint saying what the flag wants — the best refusal experience in this family;
a planted verify fault exited 1 with the count line; a broken pin named the
missing entry. `--json` carries the same evidence as the typed lines plus the
paragraph ordinal; no `--dry-run` is offered and the how-it-works line says
why (nothing is written), so there was nothing to try. Where I had to guess:
the eval gold-file format (met only in the spec, the example file, or the
refusal after a wrong one), and `boot -h` states less than `help` does about
the same verb. A 10 needs the family refusal shape (REFUSED word, verbs and
flags named on a miss), frontmatter fences out of the receipts, and the JSON
refusal carrying reasons rather than a page of text.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-memory search --root ./corpus --channels bm25 --k 3 recipe procedure inventory` | every receipt is the frontmatter fence itself (snippets like `---\nname: flour\ntype: inventory\n---\n# Flour stock`), scored just under the calibration band, so a metadata-word query spends all k receipts on text the name= and type= fields already carry | skip the frontmatter block when chunking; the fields already surface it | S |
| 2 | `nova-memory search --channels bm25 --json` | the JSON refusal's why is one string of text lines joined by newlines — three refusals with their indented hints and the door sentence inside, repeated again in remedy — so a JSON consumer re-parses prose out of JSON | render the refusal reasons as a list in the envelope, one reason each | S |
| 3 | `nova-memory search --root ./corpus --channels bm25 --k 3 --jsonn rye` | the unknown-flag answer is the flag package's stock line: it names the typo but not the verb's flags, so a one-letter miss on --json costs a round trip to help to learn the flag exists | answer with the verb's flags, or the nearest of them | S |
| 4 | `nova-memory lookup --root ./corpus` | the unknown-verb answer names the typo and the door but not the verbs there are, so the next command is a help dump rather than a correction | list the verbs in the refusal line | S |
| 5 | `nova-memory boot -h` | the verb's own help shows only short flag texts; the pin grammar (one slash path per line, # comments, order is boot order) and the never-walks-the-directory purpose are in `help`'s flags block, so the documented door for verb help states less than the banner | put the pin grammar and the verb's effect in boot's own flag texts | S |
| 6 | `nova-memory eval --root ./corpus --channels bm25 --k 3 --floor 0.8 gold.tsv` | the gold-file format (query TAB expected path) is absent from the help: the banner's eval entry and the --floor text name no format, so a cold reader writes a wrong file and learns the shape from the refusal | one line in the eval entry naming the format and the shipped example file | S |

## Good, keep

The calibration band on every retrieval run made both jobs decidable without
guessing: a duplicate read 29.48 against a band of 3.82, and a new fact's
best receipt read 3.21, below it — the tool's whole promise, working on a
corpus it had never seen.

A refusal that reports every problem at once, sorted, each with a hint saying
what the flag wants and the usual values (three sentences and one run on a
first mistake) — nothing in the family does this better.

The quickstart that prints each command line above the output it produced and
ends by saying its choices were made this once only: a cold reader leaves with
 runnable lines, not an impression.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| eval's file format is not in the help | STILL THERE | `nova-memory eval --root ./corpus --channels bm25 --k 3 --floor 0.8 gold.tsv` runs, but the format appears only in the refusal after a wrong file: `gold-bad.tsv line 1 has no TAB (format: query<TAB>expected[,expected])` |
| boot's purpose is not in the help | CHANGED | `nova-memory help` now carries it in the --pin text ("boot names the load, never walks the directory"), and `nova-memory boot --root ./corpus --pin pin.txt` prints `BOOT OK files=3 bytes=1297`; `boot -h` alone still under-states (finding 5) |
| the CAL rule calls the right hit noise | FIXED | `nova-memory check --root ./corpus --channels bm25 --k 2 weekly.md`: the true duplicate scores 29.48 against `MEMORY CAL score=3.82`, far above the band; only the genuinely new paragraph's receipts sit below it |
| the snippet cuts the answer | STILL THERE | the same run's rank-1 receipt ends `...wipe the stones with a damp cloth, and n…` — cut mid-clause at the 120-byte bound; the file:line address is the remedy, but the receipt itself stops short |
| the unknown-option refusal omits the offending flag | FIXED | `nova-memory search --root ./corpus --channels bm25 --k 3 --jsonn rye` answers `flag provided but not defined: -jsonn` — the offending flag is named |
