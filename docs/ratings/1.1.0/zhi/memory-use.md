# nova-memory USE rating, nova-tools 1.1.0

Rater: deepseek-v4-pro
Build: 2c02b2aa2042
Score: 8/10

## Reasons

A first run succeeds on a corpus I wrote under scratch: `nova-memory quickstart
--root ./corpus` runs stats, one search and one check, and prints each command
line above its own output; the echoed lines paste back and run (I re-ran the
search line it printed and got the same receipts). It ends by saying it used
bm25 and k=3/2 "this once", which is honest.

Two real jobs, end to end. Job one: `nova-memory search --root ./corpus
--channels bm25 --k 3 when do I water the tomatoes` returns notes/watering.md
rank 1 (score 3.09, above the live CAL 1.88) with class, frontmatter name and
type, and a file:line. Job two: a draft repeating the watering rule and the
marigold pair, run through check, returns notes/watering.md rank 1 at 13.55
against CAL 1.88 — the "you already know this" answer, with a NOTE that the verb
asserts nothing and the verdict stays mine. verify, eval and boot also ran:
verify gated coverage and wikilinks (exit 1 with both findings when I planted a
coverage gap and a dangling link), eval hit recall@3=1.000 on a two-row gold set
and then failed below a 0.9 floor listing the one miss, and boot loaded exactly
the two pinned files.

Four refusals, each naming the problem, every problem at once, and the next
command: a missing --k prints a hint ("search: 3 to 5"); an unknown flag prints
"flag provided but not defined: -bogus"; an unknown verb prints "unknown
subcommand"; a bad value prints "must be a positive receipt budget (got 0)".
Missing --channels and a bad --k in one run are both reported, once.

--json is faithful: search and check each render one value, and the JSON carries
the same facts, hits, calibration and notes as the typed lines, with a refused
envelope (exit 2, remedy) when the flags are bad. --dry-run is offered nowhere,
correctly: no verb writes the corpus, so the whole tool is dry by construction
and the help says nothing is written.

Where I had to guess: the --k value (help gives a range, no default), the eval
--floor starting point, and — in a bm25,trigram run — which CAL a trigram native
score should be read against.

The costs are below, ordered. The frontmatter block is indexed and echoed as
the receipt's text, so a frontmatter note's hit points at line 1 (the fence) and
quotes `---` and `name:` before the matching sentence; and the one calibration
probe is a single channel's point, printed beside hits on another scale in a
multi-channel run. The snippet also still cuts at 120 bytes, mid-sentence, with
no flag to widen it — the file:line is the remedy.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-memory search --root ./corpus --channels bm25 --k 3 soak tomatoes` | the hit for notes/watering.md quotes "---\nname: watering\ntype: measured\n---\nWater the tomatoes..." and points line 1 — the frontmatter fence, not the matching sentence — and the frontmatter keys are indexed as corpus terms, so name or measured matches every such file | skip the frontmatter block when building the chunk Text and Line, so the receipt points at and quotes content only | M |
| 2 | `nova-memory search --root ./corpus --channels bm25,trigram --k 3 tomotoes` | the correct hits score 0.02 via trigram while CAL prints score=1.88 score-channel=bm25, so the right answer reads as noise; native scores on different scales compare only within one channel, but no output line says so | print one CAL per channel, or add a NOTE to compare a hit only against CAL of the same score-channel | S |
| 3 | `nova-memory help eval` | the gold file format (query<TAB>expected-path[,expected]) is absent from the verb help, so a first eval user cannot write the input from the tool alone | state the gold row shape in eval -h | S |
| 4 | `nova-memory help boot` | the effect (loads exactly the pinned memories, never walks the directory) is absent from the per-verb help; it lives only in the top-level usage line | put the effect sentence in boot -h | S |

## Good, keep

The live CAL probe — scored against the caller's own corpus each run — is the
most honest thing a retriever can print, and the probe-and-schema co-move test
must stay. The refusal that reports every missing flag at once, each with a hint
saying what the flag wants, is the model for the whole family. The check NOTE
that the verb asserts nothing and never exits 1, and the class-on-each-receipt
distinction, must not be lost.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| eval's file format is not in the help | STILL THERE | `nova-memory help eval` lists flags only; the query<TAB>expected row shape appears in SPEC.md and the example fixture, not in -h |
| boot's purpose is not in the help | STILL THERE | `nova-memory help boot` shows only --root and --pin flags; the effect sentence sits in the top-level usage line |
| the CAL rule calls the right hit noise | STILL THERE | `nova-memory search --root ./corpus --channels bm25,trigram --k 3 tomotoes` prints CAL score=1.88 score-channel=bm25 while the right hits show score=0.02 score-channel=trigram |
| the snippet cuts the answer | STILL THERE | `nova-memory check --root ./corpus --channels bm25 --k 2 draft.md` ends the watering receipt "Deep w…" and points line 1, the fence, not the matching sentence |
| the unknown-option refusal omits the offending flag | FIXED | `nova-memory search --root ./corpus --channels bm25 --k 3 --bogus tomato` answers "flag provided but not defined: -bogus; run: nova-memory help" |
