# nova-memory USE rating, current baseline 0c5803c2de40

Rater: deepseek-v4.1-flash (opencode)
Build: 0c5803c2de40
Score: 7/10

## Reasons
The banner is the best part of this tool and the reason the score is not lower: line
one says what it does, a five-line `how it works:` names the nouns (root, index, bm25,
trigram, the CAL probe), and the `example:` block runs as printed. The advertised first
run, `quickstart`, ran end to end in a scratch corpus with no store, no network and no
key, and re-printed each command line above its output, so the next turn is a paste. Two
real jobs ran in the supported local subset: a `search` over two roots and a `check` of a
draft against the corpus; both gave `file:line` receipts with the quoted text, and
`verify` gated an unresolved wikilink and a missing frontmatter name and exited 1. The
refusals are the second strength: one run reports every missing flag at once, each names
what the input wants, and unknown flags name the offending flag.

A 10 would need the one quality the tool claims and then does not deliver: the CAL line.
The banner calls it "the score a fixed unrelated probe gets here: a hit scoring at or
below it is no better than noise." On a small corpus it prints a number (1.46 for bm25 on
two files); on a forty-file corpus the same channel prints `score=-` while the hit scores
stay comparable (3.24). The reader loses the noise baseline exactly when a corpus is
large enough to need one, and `check` still prints every hit as `HIT` with no threshold,
so the verdict the tool exists to give cannot be acted on from the CAL line alone. The
per-verb help is also uneven: `eval` and `boot` print usage and flags but not their
purpose or the gold-file format, and `stats` has no `--json` where `search` and `check`
do. None of these is a crash; together they cost three points.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-memory search --root ./big --channels bm25 --k 3 topic17` prints `SEARCH CAL score=- score-channel=- probe=unrelated-control` while `nova-memory search --root ./corpus --channels bm25 --k 3 glazing` prints `SEARCH CAL score=1.46` | the CAL baseline is absent on a corpora large enough to need it: the fixed probe shares no terms, bm25 returns nothing and the reader cannot tell a 3.24 hit from noise; hits still print a numeric score, so the two lines disagree about what a score means | print the CAL as the top score of a probe guaranteed to match (or state `CAL unavailable, treat every hit as unverified`) and mark hits at or below it `NOISE` so `check`/`search` apply the rule the banner states | M |
| 2 | `nova-memory help eval` prints `usage` and `flags` only; the gold format appears only in the refusal `nova-memory eval --root ./corpus --channels bm25 --k 3 --floor 0.5 g2.tsv` -> `g2.tsv line 1 has no TAB (format: query<TAB>expected[,expected])` | a reader cannot learn the eval input shape from help, only by failing; `boot` is the same, and neither says what the verb is for | add the same one paragraph the banner gives each verb to its `help <verb>` output, with the gold format and boot's purpose, and an `example:` line that runs | M |
| 3 | `nova-memory stats --root ./corpus` prints `STATS OK schema=...`; `nova-memory stats --root ./corpus --json` prints `nova-memory stats: flag provided but not defined: -json` | `stats` refuses `--json` where `search` and `check` accept it, so the tool is not one shape: an AI that learns `--json` on one verb gets a refusal on the next | accept `--json` on every verb and render the same `facts` value, per the one-output rule | S |
| 4 | `nova-memory verify --root ./corpus --links info` prints `nova-memory verify: no gating check requested ... a run that cannot fail is not a verification` while `nova-memory verify --root ./corpus --links gate` prints `VERIFY OK gating=0 ... links=gate` | the two modes disagree about when a run is a verification; the `info` refusal is good, but a reader who starts with `--links gate` and an all-resolving corpus sees `OK` and may believe a check ran that found nothing to check | when `--links gate` is the only check and no link is unresolved, say so on the OK line (`gating=0` is already there) and keep the `info` refusal's sentence in the gate mode's empty case | S |
| 5 | `nova-memory verify` with no flags prints two refusal sentences, both ending `run: nova-memory help` | every refusal sends the reader to the whole banner, not the one command to run next; the standard asks a result to name the next command as a paste | make the remedy the exact corrected invocation when it can be reconstructed: `run: nova-memory verify --root <dir> --links gate` | S |

## Good, keep
- `quickstart` is the model first run: it prints each command above that command's
  output and ends `QUICKSTART NOTE this used bm25 alone and k=3/2; those are choices, not
  defaults`, so a reader edits a line instead of guessing one.
- Refusals report every independent problem in one run: `nova-memory search lantern`
  names `--channels`, `--k` and `--root`, each with what it wants and its unit.
- The `MEMORY NOTE` lines on `check` ("this verb asserts nothing ... the verdict stays
  yours") state the effect honestly and are exactly the kind of breadcrumb an AI needs.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| USE 1aac13259: eval's file format and boot's purpose are not in the help | STILL THERE | `nova-memory help eval` prints only `usage`, `flags`, `exit codes`; the only place the format appears is the run `nova-memory eval ... g2.tsv` -> `g2.tsv line 1 has no TAB (format: query<TAB>expected[,expected])` |
| USE 1aac13259: the CAL rule calls the right hit noise | STILL THERE | `nova-memory search --root ./big --channels bm25 --k 3 topic17` -> `SEARCH CAL score=-` beside `HIT rank=1 score=3.24`; on the two-file corpus the same channel gives `CAL score=1.46` |
| USE 1aac13259: the snippet cuts the answer | CHANGED | a multi-root hit prints the whole paragraph, frontmatter included: `HIT rank=1 ... root=./corpus2: notes/anvil.md:1 "---\nname: anvil\n---\nThe anvil rings when struck.\nSee [[missing-note]] for more."` — nothing is cut, though the literal `\n` is hard to read |
| USE 1aac13259: the unknown-option refusal omits the offending flag | FIXED | `nova-memory search --root ./corpus --channels bm25 --k 3 --bogus lantern` -> `nova-memory search: flag provided but not defined: -bogus; run: nova-memory help` |
