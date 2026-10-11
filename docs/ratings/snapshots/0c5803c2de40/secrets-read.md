# nova-secrets READ rating, current baseline 0c5803c2de40

Rater: deepseek-v4.1-flash
Build: 0c5803c2de40
Score: 7/10
README: 7/10

## Reasons
Rated source snapshot 0c5803c2de406c1b0b2b0841f579c9bf73406b1c; the staged checkout's HEAD was that same commit when the read began, and the findings below name lines at it.
The README's row for this tool (README.md:30) reads "encrypted secrets in a git repository, handed to one command at a time", and the banner's first line (cmd/nova-secrets/main.go:19) repeats it word for word; the promise is honest and the tool does it. The spec (docs/SPEC-SECRETS.md) is the best writing in the tree: it says what the boundary is, what it is not, and why.

The first place I was confused: docs/CLI.md:1704 opens the tool's section, gives one paragraph, then goes straight to `### Gate a seat pull request`. Every other tool's section opens with `### First run` (thirteen of them), so a reader who arrives here has no paste-ready first sitting in the reference and must find docs/TESTS.md:215. The first place I was bored: cmd/nova-secrets/main.go:43-92, a fifty-line flag dump plus an eleven-command example, is a wall to read as a stranger; the banner should show the first commands and move the rest to the verb helps. The first claim I doubted: pkg/secrets/secret.go:2 says the package "provides four verbs -- exec, names, check, and keygen", but the binary ships twelve entries; and docs/SPEC-SECRETS.md:34 says "about two hundred lines of Go over two binaries" while the reviewed files are thousands of lines. Both send a reader in the wrong direction.

A 10 would need: one refusal grammar everywhere, no ticket numbers in names, a package doc that matches the verbs, a `### First run` whose transcript runs as printed, and the banner trimmed to the first reading.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | cmd/nova-secrets/main.go:284 | exec carries another tool's law: it sniffs a redis-cli argv and refuses a write to one table's key (called at cmd/nova-secrets/main.go:421). No other verb or spec mentions it, and it puts a fleet-table concept inside the credential tool. | delete the function and its call; the table's own loop is the guard | L |
| 2 | cmd/nova-secrets/main.go:43 | the banner's flag list and example block run to fifty lines; a cold reader meets every flag before any first command. | print the first commands and the common flags; move the rest into each verb's help | M |
| 3 | cmd/nova-secrets/main.go:96 | comments and test names cite tickets, not behaviour: "nova-tools#3550", "#2676", "#3807", "#3447", "#4505", "#1393" here and at 264, 277, 282, 302, 307, 417, 631; the files issue2378_test.go, issue2676_test.go and issue3550_test.go name a number, not a contract. | rewrite each comment as the rule it keeps, and rename each test for the behaviour it pins | M |
| 4 | cmd/nova-secrets/main.go:834 | the refusal grammar splits: seal, seat add, seat inject and exec print `SECRETS SEAL FAIL`, `SECRETS SEAT ADD FAIL`, `SECRETS SEAT INJECT FAIL` and `SECRETS EXEC FAIL` (also 697, 729, 413, 422, 455), while the shared grammar is `VERB REFUSED: <reason>; run: <remedy>`. | print the one grammar; exec may keep its 125 exit | M |
| 5 | docs/CLI.md:1710 | the tool's reference section never opens with `### First run`, unlike the other thirteen; the only first sitting is docs/TESTS.md:215, whose lines carry path placeholders, so nothing there is paste-ready. | add an opening `### First run` whose transcript is produced by running the fixture, and cut the placeholders | M |
| 6 | pkg/secrets/secret.go:2 | the package doc is stale for many verbs: it names four verbs ("exec, names, check, and keygen") for a binary that ships twelve entries, and says nothing about the store-change road that `seal`, `seat add` and `seat inject` walk. | state the package's real boundary, including the review road | S |
| 7 | docs/SPEC-SECRETS.md:34 | a claim the tree does not bear out: "about two hundred lines of Go over two binaries", when cmd/nova-secrets is over five thousand lines and pkg/secrets over eight thousand including tests; line 26's "links no cryptography" is likewise false of the package, which imports the crypto packages and parses SSH keys in pkg/secrets/storepull.go:3. | give the real size and name the one crypto corner, or delete the corner | M |
| 8 | pkg/secrets/gate.go:263 | the rule-matching loop is written twice: `matchingRuleIndex` here and `FindMatchingRule` at pkg/secrets/store.go:221 compile and match the same `path_regex` lists; a change to rule matching must be made in both. | keep one, and have the other call it | M |
| 9 | pkg/secrets/store.go:89 | weight: a hand-rolled line reader for `.sops.yaml` (89-218), a second YAML escape reader `unquoteYAML` (308-408) and a git index parser for v2/v3/v4 (541-649) stand beside a tool that already runs git; the package doc gives no reason a library or `git ls-files` was refused. | name the design reason in the package doc, or use git and the adopted modules | L |
| 10 | pkg/secrets/exec.go:70 | the refusal reads "...; the names the seat holds: run: nova-secrets names ..." with `run:` inside the reason and again after it. | one `run:` clause, at the end | S |

## Good, keep
The one-value Secret type in pkg/secrets/secret.go:32-64, which closes String, GoString, Format, MarshalText and MarshalJSON at once, is the right shape and the reason this tool is worth trusting with a plaintext.
The spec's honesty about what it does not give (docs/SPEC-SECRETS.md:102-106 and 351-354) belongs in every secrets tool.
sealCarry (pkg/secrets/seal.go:209-347) is walked by seal and seat inject, so both verbs leave the same shape of pull request; that shared road must not be lost.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| a table special case inside exec | STILL THERE | cmd/nova-secrets/main.go:284 and its call at 421 |
| a stale package doc | STILL THERE | pkg/secrets/secret.go:2 says four verbs |
| its own refusal grammar | STILL THERE | cmd/nova-secrets/main.go:413, 697, 729, 834 print `FAIL` |
| the advertised first sitting is incomplete and platform-specific | STILL THERE | docs/CLI.md:1710 has no `### First run`; docs/TESTS.md:206 uses path placeholders |
