# nova-check READ rating, nova-tools 1.1.0

Rater: gemini-2.5-pro
Build: 2c02b2aa2042
Score: 8/10
README: 8.5/10

## Reasons
The README introduces nova-check cleanly in its overview table, highlighting its primary value in catching broken links and issues in markdown records with a clear example. The command reference and implementation demonstrate disciplined CLI design: standard banner structure, comprehensive error hints, deterministic reporting of multiple missing flags at once, and robust bounded outputs via line ceilings.

However, a cold reading reveals three significant design friction points. The first place of confusion is cmd/nova-check/main.go:26 and docs/CLI.md:9, where five disparate functional domains are packed into one binary: markdown document checks (links, spelling, corpus, kernel), repository structure enforcement (nocode, attest, floors), git commit hygiene (hygiene), receipt-based testing verification (dogfood), and multi-stream project velocity metrics querying external forge tools (convergence). The second friction is boredom in docs/CLI.md:63, where pages of internal dogfood ledger mechanics read like an organizational protocol manual rather than tool reference. The third is a doubted claim at cmd/nova-check/main.go:8, which promises that every path comes from a flag with no defaults, yet cmd/nova-check/spelling.go:58 silently defaults to the current working directory when --dir is omitted. Furthermore, cmd/nova-check/main.go:112 limits --json to certain verbs, leaving quickstart and dogfood on typed lines despite AGENTS.md requiring --json on every verb.

A 10/10 would require separating the git and forge lifecycle verbs into their own dedicated tooling, strictly enforcing the no-defaults rule across all subcommands, providing uniform --fail-max capping across all verbs (including hygiene), and ensuring quickstart is useful for general documentation repositories without assuming a self-repository layout.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | cmd/nova-check/main.go:26 | Combining markdown record auditing with git branch hygiene, dogfood receipts, and forge velocity telemetry dilutes tool focus and confuses newcomers | Split git and forge lifecycle verbs into dedicated tools or a CI helper | L |
| 2 | cmd/nova-check/main.go:70 | hygiene uses --max for capping findings while links, nocode, attest, corpus, and spelling use --fail-max | Unify capping flag to --fail-max across all verbs including hygiene | S |
| 3 | cmd/nova-check/spelling.go:58 | Falls back to the current working directory when --dir is omitted, violating the no-defaults and no-guessing rule in main.go:8 | Require --dir explicitly or resolve relative paths without guessing | S |
| 4 | cmd/nova-check/main.go:112 | quickstart and dogfood lack --json support despite repository standard expecting structured output across all verbs | Implement structured JSON output for quickstart and dogfood verbs | M |
| 5 | cmd/nova-check/main.go:343 | quickstart unconditionally runs nocode, causing immediate failure for any standard repository containing code | Allow quickstart to check links and spelling by default, reserving nocode for self-repo mode | S |
| 6 | docs/CLI.md:209 | Example for convergence embeds developer-specific paths instead of generic portable placeholders | Replace individual paths with standard placeholders like ./reports or ./receipts | S |

## Good, keep
Fast, zero-infrastructure markdown link checking with clear line-numbered findings and bounds.
Deterministic multi-flag refusal reporting that reports all missing inputs at once with actionable hints.
Dogfood receipt tracking with cryptographic-hash identifiers preventing silent regression in tool maturity.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| several unrelated tools behind one name | STILL THERE | cmd/nova-check/main.go:26 |
| a private vocabulary | STILL THERE | docs/CLI.md:11 |
| two cap flags | STILL THERE | cmd/nova-check/main.go:70 |
| a doc that says a shipped verb does not exist | FIXED | docs/CLI.md:9 |
| the newcomer path and vocabulary assume one self-repository workflow | STILL THERE | cmd/nova-check/main.go:343 |
| README rating then: 6.5 to 7 from one rater, 8.4 from another | CHANGED | README.md:34 |
