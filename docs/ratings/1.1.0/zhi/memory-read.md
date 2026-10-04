# nova-memory READ rating, nova-tools 1.1.0

Rater: deepseek-v4-pro
Build: 2c02b2aa2042
Score: 8.5/10
README: 8/10

## Reasons

The README line is exactly the banner's first line — "search your own markdown
notes, and check a draft against what they already say" (README.md:32,
main.go:44) — and the code does that and no more. Three lines of the spec state
the problem, the requirement and the shape (SPEC.md:2343-2349): membership
against a markdown corpus is an O(n*m) scan, the requirement is k receipts per
new learning with k constant, and the index narrows m to k so the mind judges
only the survivors. The code bears every word of it out: Build rebuilds an
in-memory index per run and writes nothing (memindex.go:229), BM25 touches only
its own terms' postings (channels.go:53), and nothing in the package writes the
corpus.

A cold reader finds the entry point, the verbs and the data in a minute:
main.go:205 dispatches seven verbs from one switch, and the data is Chunk,
Corpus and FileHit (memindex.go:67, memindex.go:89, channels.go:212), each named
for what it is. Names a stranger understands: quickstart, stats, search, check,
verify, eval, boot are verbs a person would guess, and --root, --channels, --k,
--floor are explained where they are required rather than in a glossary.

First place I was confused: docs/CLI.md:356, where the QUICKSTART transcript
opens with words=glazing\x20signal\x20tide and words-source=corpus-top-terms,
before any sentence says those are the corpus's three most common terms. First
place I was bored: docs/SPEC.md:2330-2341 repeats the usage block I had just
read at CLI.md:335-348 almost verbatim before its prose adds anything. First
place I doubted a claim: docs/CLI.md:408 calls the calibration line "the band
below which a raw score means nothing", but main.go:181 scores one fixed
sentence once — that is a point, not a band.

The writing is the strong half. Comments say why, in the present tense, and name
the wrong turn each decision prevents: the normalizer strips blockquote and
emphasis markers before collapsing whitespace because the order is the whole fix
(memindex.go:99-121); postings arrive sorted by construction and the invariant
is asserted cheaply rather than trusted (memindex.go:308-315). It is one family
with the other tools: the same refusal grammar, the 0/1/2 exit table, the shared
internal/tool result value and oneline escaping, and version through
internal/buildinfo.

Weight is the cost. main.go is 1,286 lines and holds seven verbs plus the shared
flag plumbing, the shell-arg quoting and the function-word chooser — "each file
one thing" (AGENTS.md section 11) is met only for retrieval.go and version.go.
I found no dead code: every verb is reachable from the switch and every flag
appears in the usage banner, which firstrun_test.go:629 pins.

Tests teach the contract, by name: TestRefusesToGuess,
TestRootIsNeverTakenFromTheEnvironment, TestARefusalReportsEveryReasonAtOnce,
TestRetrievalOutputIsByteIdentical, TestNoCorpusOrCallerTextCanForgeALine, and
TestCalibrationProbeAndSchemaVersionMoveTogether read as the spec's rules, not
as coverage.

What a 10 would need: split the seven verbs into one file each, or at least move
the flag plumbing and the quickstart chooser out of main.go; and either say
"control score" instead of "band" everywhere, or score several probes and print
a real spread. The honesty is otherwise already at the level the owner asks for.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | cmd/nova-memory/main.go:205 | main.go holds all seven verbs plus the shared flag plumbing, the shell-arg quoter and the function-word chooser in 1,286 lines — one file, many things, so a cold reader pages the whole file to reach one verb | split each verb into its own file (quickstart.go, stats.go, search.go, check.go, verify.go, eval.go, boot.go) and move parse and rootFlags into a flags.go | L |
| 2 | cmd/nova-memory/main.go:181 | one fixed sentence scored once is printed as the CAL band, so "clearly above CAL" has no spread and the word band overclaims a point estimate | say control score instead of band, or score a small probe set and print min and max | S |
| 3 | internal/memindex/memindex.go:171 | the hand-rolled frontmatter parser is narrow: a UTF-8 BOM or a fence with trailing spaces reads as no frontmatter, so verify reports a missing name that is present | strip a leading BOM and trailing fence spaces before the fence test, or name the exact accepted shape in CLI.md | S |
| 4 | cmd/nova-memory/main.go:477 | a hand-rolled function-word map of about 120 entries sits in the verb file; it exists only to choose the demo query's words, but it looks like a stopword list and must be read carefully not to become one | move it beside topTerms into its own file with a one-line contract | S |

## Good, keep

The CAL line, scored live against the caller's own corpus each run, is the most
honest thing a retriever can print — keep it and the test that pins probe and
schema together. The refusal that names every missing flag at once, with a hint
saying what each flag wants, is the model for the whole family. The "what it
refuses to be" header and the check NOTE that "asserts nothing and never exits
1" must not be lost.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| a 1,394-line main.go that hand-prints every verb twice and scrapes its own stderr for --json | FIXED | retrieval.go:35 renders text and JSON from one retrievalResult; main.go:924 captures refusals into a buffer instead of scraping |
| the universal unrelated/noise calibration claim is not supported | FIXED | main.go:181 scores the probe live each run; SPEC.md:2570 now says "unrelated text scores about this much on YOUR corpus" |
