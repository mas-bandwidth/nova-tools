# nova-check READ rating, nova-tools 1.1.0

Rater: Grok
Build: 2c02b2aa2042
Score: 6.5/10
README: 7/10

## Reasons

README line, read before any code: README.md:34 says this tool "checks over markdown records and repositories, each finding named by file and line" and offers one first command, quickstart, on an included example tree.

Confused first at README.md:34. "Other problems" does not say which problems, and a finding "named by file and line" is a promise the row never shows.

Bored first at README.md:71. Line 44 already said to pick the row. The paragraph after the table says it again, then sends the reader onward before any sample of what a check prints.

Doubted first at README.md:47. "These are the Nova Tools 1.0.0 commands", and the install lines pin that release, while the same tree carries a 1.1.0 release note. A reader cannot tell whether the page describes the checkout in hand.

The banner's first sentence is that README sentence (cmd/nova-check/main.go:25), and the example block is three commands a stranger can paste. That part of the family shape holds. quickstart is a real first run: links, then nocode, both even when the first says no, with a ceiling and a next line (cmd/nova-check/main.go:343). A refusal names every missing flag in one run and says what the flag is for (cmd/nova-check/main.go:310). Comments say why, in the present tense. The tests under cmd/nova-check and internal/check are thick enough to teach the contracts they name.

The score is 6.5 because an AI that does not already keep this house's records cannot tell what the binary is for. docs/SPEC.md:329 says ten checks, seven of them over one writer's own record tree, and three pointed somewhere else: a receipt ledger, a branch range, and a forge reading. The usage string then adds spelling, which rewrites files. Kernel, floors, corpus and attest are unusable until the caller already owns a size budget, a derived charter, a ledger and a boot list. The words for those objects (self, kernel, door, floors, corpus) are not ordinary words, and the first screen does not define them (cmd/nova-check/main.go:41 and cmd/nova-check/main.go:151).

A 10 would open with the two checks a stranger can run, in ordinary words, and would park the house-specific checks behind names a newcomer can skip. One ceiling flag. The command reference, the spec and the usage string would name the same verbs. A rule written as already in force would be what the code does.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | docs/SPEC.md:329 | Ten checks and a spelling rewriter share one name. The banner promises every finding is a file and a line (cmd/nova-check/main.go:25), but kernel prints a byte count, hygiene prints a commit, dogfood prints receipt rows, and convergence prints stream ratios. An AI looking for broken links has to discard most of the usage string. | Lead the banner with links and nocode in ordinary words, and put the house checks under a second heading that says they need files only the caller owns. | M |
| 2 | docs/CLI.md:718 | This paragraph says an index-reading form of nocode is deliberately not here yet and ships when it reads the index. The usage string already ships that form (cmd/nova-check/main.go:60), the spec specifies it (docs/SPEC.md:800), and the nova-check synopsis in the command reference omits it (docs/CLI.md:15). | Delete the stale sentence, and add the staged form to the synopsis beside the spec. | S |
| 3 | docs/SPEC.md:5037 | The efficiency card cites two walk calls at internal/check/links.go:52 and internal/check/nocode.go:356, then states a rule that one quickstart walks the tree once (docs/SPEC.md:5043). Those calls are now at internal/check/links.go:74 and internal/check/nocode.go:340, and quickstart still invokes the two verbs separately (cmd/nova-check/main.go:367). The test that names the rule only checks that the spec still contains the sentence (internal/check/eff86_nova_check_spec_test.go:43). | Either share one walk, or mark the sentence as not done and stop pinning it as prose. | M |
| 4 | cmd/nova-check/hygiene.go:43 | Listings use --fail-max, except hygiene, which uses --max for the same ceiling. The banner's summary of --fail-max also omits dogfood, which takes the flag (cmd/nova-check/dogfood.go:252) and does not take --json, while the banner says quickstart and dogfood use typed lines only (cmd/nova-check/main.go:112). | One flag name on every listing, and say on each verb whether --json exists. | S |
| 5 | cmd/nova-check/main.go:124 | The example builds a directory named self and a file named SEED-CORE.md, and the closing line points at kernel, attest, floors and corpus. Those four assume a charter, a manifest and a ledger the newcomer does not have. Spelling, the other check a prose tree can use with no extra files, is absent from that next list. | Point next at spelling, and define each house noun in one ordinary clause before its flags. | S |

## Good, keep

A missing flag is named with every other missing flag, and the hint says what to put there (cmd/nova-check/main.go:310). quickstart runs both cheap checks even when the first fails, caps the lines, and withholds the OK word unless both passed (cmd/nova-check/main.go:382). The link check strips fenced and inline code before it resolves a target, and it reports the file and the line (internal/check/links.go:14).

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| several unrelated tools behind one name | STILL THERE | docs/SPEC.md:329 lists ten checks in one binary, and cmd/nova-check/main.go:235 dispatches eleven verbs |
| a private vocabulary | STILL THERE | cmd/nova-check/main.go:41 says did the full self load, and cmd/nova-check/main.go:151 calls a file the door |
| two cap flags | STILL THERE | cmd/nova-check/main.go:116 documents --fail-max, and cmd/nova-check/hygiene.go:43 takes --max for the same ceiling |
| a doc that says a shipped verb does not exist | STILL THERE | docs/CLI.md:718 says the index-reading form is not here yet, while cmd/nova-check/main.go:60 ships nocode --staged |
| the newcomer path and vocabulary assume one self-repository workflow | STILL THERE | cmd/nova-check/main.go:124 builds a self tree and a SEED-CORE file, and the next line names only the house checks |
