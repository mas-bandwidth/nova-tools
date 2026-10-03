# nova-memory READ rating, nova-tools 1.1.0

Rater: Grok
Build: 2c02b2aa2042
Score: 7.5/10
README: 8/10

## Reasons

README.md:32 is the line: search your own markdown notes, and check a draft against what they already say, with a first command over the included corpus. The banner's first line is that same sentence.

Before any code. Confused at README.md:48, which calls the table the 1.0.0 commands, and at the installing section the README points to first, which tells a stranger to download that release and clone that tag. Bored at AGENTS.md:39, a list of working rules that never reaches this tool. Doubted USAGE.md:349, "points at the sources that matter," a relevance claim the next paragraphs walk back to the words that are there.

The spec then does the job an essay has to do. It says what the tool is for in a few lines: membership is k receipts, the tree is the store, the index dies with the process, and two verbs are walls while five only report. The status section says the value is unproven off the line it was measured on, and that the harness is how you find out. Comments say why, in the present tense: smoothed idf because the classic form goes negative on a small topical corpus, line endings normalized so a CR-LF file chunks like its LF twin, a cut on a rune boundary so a receipt stays valid text. Tests pin the contract: an empty corpus is refused, two runs over one tree are byte-identical, every missing flag is named in one run, the probe sentence may move only with the schema version, and a gold row with no tab is never skipped.

What keeps it off a 10 is that three claims do not survive the page they sit on, and the binary is not one of the family.

The banner at cmd/nova-memory/main.go:50 says a hit scoring at or below the unrelated probe is no better than noise. The command reference's own first-run transcript, docs/CLI.md:365, prints lexical hits under that score, and cmd/nova-memory/retrieval.go:75 only prints the probe. Nothing compares a hit to it. One fixed sentence is not a floor.

cmd/nova-memory/main.go is 1286 lines of hand dispatch. A refusal is `nova-memory <verb>: ...; run: nova-memory help` (main.go:200), not the family's one refusal line. `--json` exists only on search and check, and those two copy refusal text out of a side buffer and wrap it (main.go:864). The other verbs have no second rendering. Verb help quotes one banner that names each verb in the usage block and again in the example block (main.go:60 and main.go:140).

The opening sentence says a lookup, never a scan. The word channel walks posting lists. The trigram channel scores every chunk (internal/memindex/channels.go:120).

The same argument is written in the banner, the package comment, the command reference, and the spec. Help names the gold file as `<gold.tsv>` (main.go:65) and does not say a row is a query, a tab, and a path fragment. That shape is in the spec and in the parser's errors.

README 8/10. The row is the right sentence and the adoption card is honest about a lexical limit and a build paid on every run. The 1.0.0 pin, and the bus trial that follows an install example of this tool, are why it is not a 9.

A 10 would make the probe sentence match the code, render one result value two ways on every verb, name the trigram channel as a scan or make it a lookup, put the gold-file shape in the help, and say the essay once.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | cmd/nova-memory/main.go:50 | The banner calls a hit at or below the unrelated probe noise. The first-run transcript prints real lexical hits under that score, and the renderer never applies the rule. | Call the line one unrelated sentence's score, not a floor, or mark hits against a measured band. | M |
| 2 | cmd/nova-memory/main.go:864 | search and check build a second rendering by buffering refusal text. The typed refusal is not the shared grammar, and the other verbs have no second rendering. | One result value, rendered as lines and as the same object, refusals included, on every verb. | L |
| 3 | internal/memindex/channels.go:120 | The thesis is a lookup, never a scan. The trigram channel scores every chunk on each query. | Say so in the banner, or index the grams so a query does not walk the corpus. | M |
| 4 | cmd/nova-memory/main.go:65 | Help names the gold file and does not say the row is a query, a tab, and a path fragment. | Put that row shape on the eval usage line. | S |

## Good, keep

The status section that says the value is unproven off the measured line, and that the harness is how a reader finds out, at docs/SPEC.md:2771.
Every missing flag named in one run, with what to put there, and no corpus taken from the environment, at cmd/nova-memory/main.go:252.
The three measured decisions in the package comment, and the tests that pin an empty corpus, byte-identical runs, and a probe that moves only with the schema version.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| a 1394-line main that hand-prints every verb twice and scrapes its own stderr for the second rendering | CHANGED | main.go is 1286 lines; search is still named at main.go:60 and again at main.go:140; search still buffers stderr at main.go:864 |
| the universal unrelated-or-noise calibration claim is not supported | STILL THERE | main.go:50 states the floor; docs/CLI.md:365 prints a lexical hit under the probe; retrieval.go:75 prints the probe and does not compare |
| the README was scored from the mid 6s through the mid 8s | CHANGED | README.md:32 is one sentence plus a first command; README.md:48 still pins the 1.0.0 commands |
