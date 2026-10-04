# nova-update USE rating, nova-tools 1.1.0

Rater: glm-5.3-flash
Build: 0d264a320912
Score: 8/10

## Reasons
Cold USE rating from the binary and its help alone, every command run in the rater's own scratch directory, nothing real touched: no store, no remote, no gh, no model, no key, and the only network reach was the tool's own forge read inside `release cut --dry-run`, which refused on its own. The head works from the first line: `nova-update help` states the manifest byte for byte, the exit-code contract and a first-run path (`example --out`, then `report --file`), and every verb's `-h` repeats the lines that verb needs, names its effect and lists its flags with defaults.

What carries the 8: the refusal discipline. A missing `--file`, an unknown flag, an unknown verb and a bad `--max` value each name their problem, the valid vocabulary and the exact next command; a manifest with four problems lists all four at once, each with its line number and remedy, then one `run:` line. The documented exit codes held in every probe: 0 on target or plan printed, 1 the NO with counts, 2 the refusal with the remedy. `--json` is one envelope on every verb that takes it (result, facts, items or payload), `--dry-run` prints a real plan with the resolved argv and writes nothing, `example --out` never overwrites (unchanged=true), and a local fake upgrade applies end to end: APPLY OK from=0.1.0 to=0.2.0, the installer runs, the after-read agrees, exit 0.

What holds it from 10: a pin read through the help's own local pattern answers installed=version latest=version and counts the entry current with a green exit; `release cut --dry-run` reaches the forge before any plan exists; a refused check splits one watch pass across two streams; `release install -h` is the one help without an effect line and the verb has no `--dry-run`; `adoption -h` never names the state vocabulary its ledger wants. A 10 needs a pin read that refuses loudly when no token in the line is version-shaped, an offline cut plan, one stream (or a `--json` receipt) for a whole watch pass, the install effect line and dry-run, and the adoption states enumerated.

Not tried, so not rated: `report --store` (a fleet store), `--send` and watch `--bus` (delivery through nova-bus), `release build`, `adopt`, `pull` and `cycle` (the forge, ssh machines, an inventory), and every network latest source (github:, npm:, brew:, ollama:); every read this rating made was local: on this host, and release verbs beyond `cut` and `install` are judged from their help lines.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-update status --file pin.tsv --json` | a pin whose latest is the help's own local:go version pattern reports installed=version latest=version, counts the entry current and exits 0: the pin reader takes the line's second token, so an AI that pins this way is told a lie with a green exit; the counts also say pins=0 for a one-pin file | for kind pin, take a version-shaped token from the line and refuse loudly, naming the two-token shape, when no token is version-shaped; and count the pins the file holds | M |
| 2 | `nova-update release cut --repo owner/name --from dev --version v1.1.0 --changelog cl.md --no-dogfood-gate --reason cold --dry-run` | the dry-run asks the forge where the branch is (gh api repos/owner/name/commits/dev) before any plan exists: on a box with forge credentials the no-touch verb performs a real remote read, and offline it is the first thing to fail | read the range locally first (git in the given checkout or --local-diff), print the plan, and reserve the forge read for a real cut | M |
| 3 | `nova-update watch --adopt checks.tsv` | one refused check splits the pass across streams: ADOPT OK lands on stdout while ADOPT REFUSED, ADOPT ESCALATE and ADOPT DONE sit on stderr, and watch takes no --json, so an AI reading one stream loses the tail and the pass sha | keep every ADOPT line of a pass on one stream, or offer a --json receipt like the other verbs | M |
| 4 | `nova-update release install -h` | the one -h without an effect line (every other verb states what it writes), and the verb that writes the live bin directory has no --dry-run, so its only preview is running it | add the effect line and a --dry-run that prints the install plan, as cut's does | M |
| 5 | `nova-update adoption --file ledger.tsv --as me` | the -h names the five fields but never the state vocabulary; adopted and declined worked on the first guess, and any other word is a guess whose refusal the help does not prepare | enumerate the accepted states in the flags block of `nova-update adoption -h` | S |
| 6 | `nova-update example --out more.tsv` | the byte-exact six-field TSV is hand-written and the only writer emits the one-line Go stub, so the tool's front door is the file an AI types alone; a stray double tab or a wrong header is a refusal (the refusal is perfect, the writing is manual) | let `example --out` append one validated entry (name, kind, installed, latest, apply, owner) so a manifest grows without hand-editing | S |

## Good, keep
- Every refusal names its problem, the valid vocabulary and the exact next command, and a manifest refusal lists every problem at once: `nova-update check --file bad.tsv` named four problems, each with its line number and remedy, then one `run:` line.
- The exit-code contract printed in every help held in every probe: 0 on target or plan printed, 1 the NO with counts, 2 the refusal with the remedy.
- One --json envelope on every verb that takes it, `--dry-run` prints the resolved plan and writes nothing, `example --out` never overwrites, and the local apply loop (BEFORE, RUN, AFTER with the re-read) leaves nothing to infer.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| a pin through the help's own local:go version reports latest=version (2026-10-02 at 1aac13259, one rater scored 6.5) | STILL THERE | `nova-update status --file pin.tsv --json` exits 0 and its items carry "installed":"version","latest":"version" |
| release verbs refuse the help's example version (2026-10-02 at 1aac13259) | STILL THERE | `nova-update release install -h` says the release version is "such as 1.2.0"; `nova-update release install --from absent --version 1.2.0 --bin bin2` refuses: the version "1.2.0" is not v-prefixed (pass --version vX.Y.Z) |
| watch's summary moves to stderr (2026-10-02 at 1aac13259) | CHANGED | a clean pass prints ADOPT DONE on stdout with stderr empty and exit 0; with one refused check, ADOPT REFUSED, ADOPT ESCALATE and ADOPT DONE sit on stderr while ADOPT OK stays on stdout and the exit is 1 |
| a local fake upgrade completes and rechecks (2026-10-02 at 1aac13259, one rater scored 10) | STILL THERE | `nova-update apply --file fake.tsv fakey` prints APPLY OK from=0.1.0 to=0.2.0, runs the entry's argv and its APPLY AFTER reads installed=0.2.0, exit 0 |
| three cold raters scored 6, 6.5 and 7 (2026-10-01 at c83ca717), one rater scored 6.5 (2026-10-02 at 1aac13259) | CHANGED | this rating scores 8: the pin lie and the example-version refusal stand, a clean watch pass is one-stream, refusals still name every problem and the next command, and cut's dry-run now shows its forge read |
