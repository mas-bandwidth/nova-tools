# nova-check READ and USE rating, nova-tools 1.2.0

Rater: OpenAI GPT-6 Luna in OpenCode
Build: a65b5cba44ab
READ: 6/10
USE: 6.5/10

This rates the supplied 1.2.0 release candidate at `sprint/mechanical-2026-10-02`; `nova-check version` prints `nova-check v1.0.1-0.20261007015135-a65b5cba44ab linux/amd64 go1.26.6`. I read `nova-check help`, every root and `dogfood` verb's `-h`, `docs/SPEC.md`'s nova-check section and `docs/SPEC-CHECK.md`. I built the binary and used it on copied `example-self` and `example-dogfood` fixtures plus an empty throwaway directory and receipts directory under the job. No live store, server or forge access was used; I did not run convergence because its LANDING and PRS streams read the forge through `gh`.

## Reasons

READ. The first-run path is concrete, and `quickstart` says what it checks and runs both links and nocode successfully against the copied fixture. Every inspected verb help prints an effect and exit-code table, and most flags say what they accept. That makes a cold trial possible without infrastructure. READ is 6 because the help excerpt itself is not a reliable reference: it reflows descriptions into usage, `dogfood -h` includes a stray convergence sentence, several root usage summaries omit flags accepted by the verb, and the `dogfood gate` summary overstates what the default gate checks. The normative docs have drifted too: the convergence spec names a different binary and promises a help line that is not present, and the record-layer spec describes old flags and output grammar.

USE. On the fixture, quickstart reported four markdown files, three links and five clean files; the dogfood record verb appended a receipt, and the gate reported the open fixture edge with its remedy. The errors distinguish a failed check from a refusal, and the help's effects matched the writes I tried. USE is 6.5 because green over zero files, paths that are silently interpreted relative to `--dir`, failure output that disappears from stdout, and a dogfood gate that returns OK with declared verbs never run can mislead an AI that consumes only the result stream or trusts a green verdict. Unknown-flag recovery also names only the first problem and sends the reader to the whole help.

## Findings

| # | where | finding | fix |
|---|---|---|---|
| 1 | `nova-check links --dir ../trial/empty`; `nova-check spelling --dir ../trial/empty` | Both print an OK line with `files=0` and exit 0, so an empty or misspelled input path can look fully checked. | Print a NOTE or refuse when no files are classified. |
| 2 | `nova-check links --dir ../trial/self --exclude ../trial/self/notes` | This prints `excluded=0`, while `--exclude notes` excludes one file; the help does not say prefixes are based at `--dir`. | State the base explicitly or accept paths that include the `--dir` prefix. |
| 3 | `nova-check links --dir ../trial/self --file ../trial/self/README.md` | The caller-visible path fails as unreadable, while `--file README.md` succeeds; `-h` does not explain that relative files are joined to `--dir`. | Document and consistently accept one path base for `--file`. |
| 4 | `nova-check links --dir ../trial/self --file ../trial/missing.md` | On exit 1 the finding and summary are both on stderr, leaving stdout empty for a parser that reads the normal result stream. | Put check findings and summaries on stdout; reserve stderr for refusals and diagnostics. |
| 5 | `nova-check links --bogus --alsobad` | The refusal names only `--bogus`, so fixing one typo costs another invocation. | Collect and report all independent parse errors in one refusal. |
| 6 | `nova-check links --bogus` | The remedy is `nova-check help`, the entire banner, rather than the known verb's help. | Point a verb refusal to `nova-check help links` or `nova-check links -h`. |
| 7 | `nova-check links -h`; `nova-check dogfood -h` | Help excerpts left-align wrapped description text, making prose look like more usage; the dogfood excerpt also includes the unrelated line `nova-check dogfood record the finding`. | Extract complete usage blocks and preserve their description indentation. |
| 8 | `nova-check dogfood gate -h` | It says exit 1 with verbs no non-author has run, but the default gate passed with only `links` recorded and `quickstart` and `nocode` unrun (`require-all=no`). | Describe the default as checking recorded open edges and state that `--require-all` adds unrun verbs. |
| 9 | `nova-check help` | The `--max` paragraph names quickstart, attest, links, nocode, corpus and spelling, but hygiene and dogfood also accept `--max`. | List every verb accepting the shared flag or direct readers to each verb's `-h`. |
| 10 | `nova-check help` convergence and dogfood usage | The summary lines omit accepted flags including convergence's `--gh`, `--git`, `--now` and `--timeout`, and dogfood's `--tools` and timeout flags. | Generate the summary from the actual flag set or label it as abbreviated. |
| 11 | `nova-check dogfood ledger -h` | `--authors` is shown as `<<tool> <verb> = <who wrote it>>`, an invalid-looking nested placeholder for a file path. | Show a simple `<file>` argument and keep the mapping syntax in its description. |
| 12 | `nova-check version --json` | The result has `facts={}` and puts the complete build identity in `payload`, unlike a typed version record. | Put version, OS, architecture and Go version in named facts. |
| 13 | `docs/SPEC-CHECK.md:1,13` | The file is titled `nova-dev convergence` and claims help prints that tool's synopsis byte for byte; this binary is `nova-check` and prints a different synopsis. | Rename the spec heading and quote the current `nova-check convergence` help line. |
| 14 | `docs/SPEC.md:344,353,458` | The spec says refusals omit `REFUSED`, listings use `--fail-max`, and link failures say `FAIL`; the binary prints `REFUSED`, uses `--max` as primary, and prints `FAILED`. | Bring the normative grammar and flag names in line with the built binary. |
| 15 | `README.md:55-60` | The first-run section calls these 1.0.0 commands and installs v1.0.0, despite this rating being for the 1.2.0 release candidate. | Update the release label and install example for the current release. |

## Good, keep

The copied fixture's first run needs only a directory and returns concrete counts for both checks. Verb help names effects and exit codes, and the flags generally state their inputs. `dogfood record` gives a useful receipt path after writing; `dogfood gate` names an open edge, its receipt, and the ways to close it. The output is compact on successful checks, and the checked fixture works without a store or network.

## Compared with earlier ratings

| earlier | now | evidence |
|---|---|---|
| Green over zero files in links and spelling | STILL THERE | Both report OK with `files=0` and exit 0 on `../trial/empty`. |
| `--exclude` interpreted relative to `--dir` without saying so | STILL THERE | `--exclude ../trial/self/notes` gives `excluded=0`; `--exclude notes` gives `excluded=1`. |
| Failed links output only on stderr | STILL THERE | Redirecting streams for an exit-1 missing-file check leaves stdout empty. |
| Refusals point to global help | STILL THERE | The unknown-flag refusal ends `run: nova-check help`. |
| Only the first unknown flag is named | STILL THERE | `--bogus --alsobad` names only `--bogus`. |
| Convergence spec names `nova-dev` | STILL THERE | `docs/SPEC-CHECK.md:1,13` still uses `nova-dev` and its nonexistent synopsis. |
| Dogfood gate says what the default checks | NOT FIXED | With only `links` recorded and no open edges, the default returns `DOGFOOD GATE OK ... require-all=no` despite two other verbs having no receipt. |
