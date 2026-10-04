# nova-memory USE rating, nova-tools 1.1.0

Rater: Grok
Build: 2c02b2aa2042
Score: 8/10

## Reasons

Judged from `nova-memory help`, `nova-memory help <verb>`, and `-h`, then from runs in a scratch directory. Nothing here was a store, a remote, or a key. Help offers `--json` on search and check. It does not offer `--dry-run`. Passing `--dry-run` is an unknown flag.

The first run is the help's own setup and its four example lines. All four exited 0. `quickstart --root ./corpus` printed the stats, search, and check lines it actually ran, then said bm25 and k=3/2 were choices for that run. `verify --root ./corpus --links info --coverage notes/lantern.md:notes/index-notes.md` printed `VERIFY OK gating=0`. A bare `nova-memory` exited 2 and named quickstart as the first run.

Two jobs on a notebook written for this rating (a practice note, an index, a day log, a short handbook).

Job 1, do I already have this. `nova-memory check --root ./corpus --channels bm25 --k 3 draft-same.md` on a draft that repeats the kettle sentence: rank 1 is notes/kettle.md at 13.01 against CAL 2.05, with name=kettle-care and type=practice. Rank 2 is the day log at 2.89, and the NOTE says a log hit means the event was recorded, not that the lesson was banked. That was enough to keep the practice and not the log. A second draft that avoids the note's wording (`draft-para.md`, mineral film, acid, sight glass) still ranked kettle.md first, at 3.20 against the same 2.05. On this notebook the lexical warning did not hide the right file.

Job 2, a different job: gate the notebook, load a pin, measure a gold file. `verify --root ./corpus --links gate --coverage 'notes/*.md:notes/index.md' --frontmatter 'notes/*.md'` exited 1 and printed `VERIFY FAIL frontmatter notes/index.md: no name: in frontmatter` plus a gating count. `boot --root ./corpus --pin pin.txt` printed `BOOT OK files=1 bytes=196`, which is the pinned file's size and not the 502-byte tree. `eval` on a gold file of two rows the search had already found printed `EVAL OK recall@3=1.000 floor=1.000 rows=2 hits=2 misses=0`. A planted miss printed `EVAL MISS` and `EVAL FAIL` and exited 1. `--floor 0` exited 2 and said a harness that cannot fail is not a measurement.

Refusals, four kinds. Missing flags: `search --root ./corpus` named `--channels`, `--k`, and the missing query in one run, and the hints say bm25 or trigram and a k of 3 to 5. Unknown flag: `--dry-run` printed `flag provided but not defined: -dry-run`. Unknown verb: `fold` printed `unknown subcommand "fold"`. Bad value: `--k 0` printed `must be a positive receipt budget (got 0)`. A bad channel, a bad `--k`, and a missing query were three lines in one run. `--links maybe` named gate or info. Each line ends in `run: nova-memory help`. An unknown flag stops the parse: `--k -3 --floor 2` reported only `-floor` and did not mention the bad k. The remedy is the top help, not `help <verb>`, and the flag is printed with one dash.

`--json` on the kettle search was the same three files, ranks, and scores, plus a paragraph ordinal the typed line does not carry. Absent name and type are `""` in JSON and `-` on the typed line. A refused `--json` search was one object with status refused, exit 2, remedy, and the same three problems. I could act on it.

Where I had to guess. The gold row shape is not in `help` or `help eval`, only `<gold.tsv>`. A file with no tab was refused with `format: query<TAB>expected[,expected]`, so the second run did not guess. The pin's line shape is in the top help and not in `help boot`; an absolute pin entry was refused with the rule. I guessed, until the example contradicted it, that a hit at or below CAL should be thrown away, because the banner says it is no better than noise. I guessed that the clipped candidate text was the whole text that was scored.

A 10 would make the probe sentence true of the help's own example, keep the answering sentence inside the receipt, break fused ties by the native score or say the tie, put the gold row and the pin line in the verb help, and keep reporting the other problems when one flag is unknown.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-memory search --root ./corpus --channels bm25 --k 3 lantern glazing brass` | On the corpus the help's setup builds, the right file scores 0.99 and the probe scores 1.46. The banner says a hit at or below the probe is noise, so the example's own answer is discarded. | Do not call that probe a floor, or show a band the example's right hit clears. | M |
| 2 | `nova-memory check --root ./corpus --channels bm25 --k 3 draft-same.md` | The right receipt ends "The cloth for the brass is n…", so the sentence that distinguishes the two cloths is not in the receipt. The paraphrase's candidate line is cut the same way, at "for meta…". | Keep the first answering sentence whole, and print the candidate that was scored. | M |
| 3 | `nova-memory search --root ./corpus --channels bm25,trigram --k 3 warm vinegar cloth` | Rank 1 is notes/index.md at native 0.62 and rank 2 is the practice note at native 3.38, with the same fused value. Rank 1 is under the probe. | Break a fused tie by the native score, or say the tie on the line. | M |

## Good, keep

Missing flags are one run, and each hint says what to put there: bm25 or trigram, and a k of 3 to 5.
A repeated draft scored 13.01 against a probe of 2.05 and named the practice note, and the log NOTE stopped the day log being read as the banked lesson.
Boot reported the pinned file's 196 bytes, not the tree, and a gold row with no tab is refused with the row shape in the same line.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| eval's file format is not in the help | CHANGED | `nova-memory help eval` shows only `<gold.tsv>`; `nova-memory eval --root ./corpus --channels bm25 --k 3 --floor 1 gold-guess.tsv` prints `line 1 has no TAB (format: query<TAB>expected[,expected])` |
| boot's purpose is not in the help | CHANGED | `nova-memory help` says the pin is one slash path per line and that boot names the load, never walks the directory; `nova-memory help boot` says only "pin file naming the memories to load" |
| the CAL rule calls the right hit noise | STILL THERE | `nova-memory search --root ./corpus --channels bm25 --k 3 lantern glazing brass` prints CAL 1.46 and the lantern file at 0.99 |
| the snippet cuts the answer | STILL THERE | `nova-memory check --root ./corpus --channels bm25 --k 3 draft-same.md` ends the kettle receipt at "brass is n…" |
| the unknown-option refusal omits the offending flag | FIXED | `nova-memory search --dry-run --root ./corpus --channels bm25 --k 3 warm vinegar` prints `flag provided but not defined: -dry-run` |
