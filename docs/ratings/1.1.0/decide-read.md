# nova-decide READ rating, nova-tools 1.1.0

Rater: a cold reading of the head, no memory of this tool's code, its reasoning or its ratings
Build: bd7949b97aec
Score: 8.5/10
README: 9/10

## Reasons
The README keeps its one-line promise: "Let a cheap model make a typed call, and learn how far to trust it." (README.md:34), and the row is honest about what the printed command runs (a fixed file, no network, no key) and what the real ask needs. The code does what the row says: the fixed backend carries the whole first run, every deciding verb appends to one record, and the train side reads the bar from the outcomes it recorded (pkg/decide/calibrate.go:31).

First confusion: docs/CLI.md:2273. The command reference's first output shape leans on "a noul's `p=yes:<p>`", and the reference defines "noul" nowhere; the gloss lives in the banner (cmd/nova-decide/main.go:55) and the spec's section 2, two doors away.
First boredom: docs/CLI.md:2293. The brief paragraph runs eight lines of nested parentheses carrying who asks it, the two forms, the server split and the record's home; the reference is read as a parse, not a scan.
First doubt: docs/SPEC-NOVA-DECIDE.md:101. The record's design — append-only, one label, one writer — rests on "the one writer is the lock's holder", and the same sentence admits its model is owed and its export verbs are not built, so nothing yet checks the guarantee the tool's trust rests on.

Why not 10: the front door spends its five how lines on a compressed pseudo-run (cmd/nova-decide/main.go:58) whose printed line is not the line the tool prints, instead of naming the nouns and where the state lives; the command reference is one prose wall from the read line to the findings line (docs/CLI.md:2271); the gate walks the whole record twice per failure beside a batcher built for exactly that (pkg/decide/gate.go:392). A 10 needs: the how paragraph a cold reader parses on the first pass, the reference scannable one verb at a time, the gate's asks in one batch, and the record's model landed so the lock's claim is checked, not owed.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | cmd/nova-decide/main.go:58 | The banner's how-it-works, the first paragraph a cold reader reads, is a pseudo-run in shorthand: a schema no fixture holds (`q`, `ok`), a state written `R?`, and an output line `ASK OK id=f decision=q backend=fixed recorded=new` that the tool does not print — the fixture run prints `id=first decision=reply ... tokens_in=0 tokens_out=0` (docs/TESTS.md:773) — and the paragraph never names the record file, where the tool's whole point lives. | Quote the fixture run's own two lines (docs/TESTS.md:773), or spend the five lines naming the nouns: schema, state, backend, record, and where each lives. | M |
| 2 | docs/CLI.md:2271 | From the read line to the findings line the section is one prose block carrying eight verbs' output shapes, routing and caller contracts in run-on sentences; the brief paragraph alone is eight lines of nested parentheses (docs/CLI.md:2293), and a reader hunting one verb's answer parses the whole wall. | One short paragraph per verb, or a command table as nova-table's section has (docs/CLI.md:2387), with the transcript in docs/TESTS.md carrying the detail. | M |
| 3 | pkg/decide/gate.go:392 | The gate asks its failures one Make at a time; each Make loads and parses the whole record and each Append opens, parses and writes it again, so a gate of eight failures walks the record sixteen times — the cost batch.MakeAll exists to erase, and the package's own comment names it (pkg/decide/batch.go:15). | Build the failures as batch items, ask them in one MakeAll, and route the results: one read, one write. | M |
| 4 | docs/CLI.md:2273 | `noul` and `flash child` carry the section's output shapes and voice and are defined nowhere in the reference (the banner glosses noul, the spec's section 2, the tiers are the sprint's words), so a cold reader of the reference alone cannot parse `p=yes:<p>` the first time. | A parenthetical at each first use: a noul is a yes-or-no question answered with a probability of yes; flash is the fast, cheap tier. | S |
| 5 | docs/USAGE.md:127 | The adoption guide the README calls "start here" never introduces the tool: Choosing a tool walks ten tools and stops before this one, so the reader the README sends there meets the tool with the most new words in no guide at all. | An honest row in Choosing a tool: what it is for, that it wants a provider key today, and that the sprint and the swarm are its first callers. | S |
| 6 | docs/CLI.md:2259 | The first-run commands write `./decisions.jsonl` into the checkout root, while the README's own rule for created files is a `./trial-` name (README.md:59); a reader who follows both documents leaves a stray file in the tree. | `--record ./trial-decisions.jsonl` in the reference's first run, as the README's row already does. | S |
| 7 | cmd/nova-decide/main.go:397 | The gate verb re-rolls the named-flags file loop that readFiles (cmd/nova-decide/main.go:355) already is, minus the inputs half; two loops now collect read problems and can drift. | Give readFiles a texts-only mode and call it here. | S |
| 8 | cmd/nova-decide/main.go:628 | The headline answer a decision prints is found by trying a fixed list of names (`verdict`, the attempt's class, the grade's grade), a convention the schemas hold and main.go guesses; a new schema with a headline choice prints none. | One field on the schema, naming the headline question, that the renderer reads: the choice lives with the questions. | S |

## Good, keep
The first run is a transcript the tests execute: cmd/nova-decide/firstrun_test.go:56 runs the banner's examples and the docs/TESTS.md block (docs/TESTS.md:764) and compares the whole output, and the ids come from --op so every line reads the same twice (docs/TESTS.md:766).

The spec publishes the numbers that argue against its own automation — inside_paths at 0.612 and needs-pro at 0.494, near chance (docs/SPEC-NOVA-DECIDE.md:201, docs/SPEC-NOVA-DECIDE.md:320) — and ships every routing bar empty until independent labels land.

The record is append-only, idempotent by op id and taken under a file lock, and a decision the record holds is answered, never asked again (pkg/decide/record.go:143, pkg/decide/firstread.go:86).

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| the install pin's release tree held no nova-decide, and the want-cell promised a model call the printed command did not make | FIXED | the 1.0.0 tag holds cmd/nova-decide, and the row now names what the trial command runs and what the model ask needs (README.md:34) |
| with no --op the id folded the clock in, so the same ask run twice asked twice | STILL THERE | cmd/nova-decide/main.go:580 still hashes the stamp into the id |
| the file opened calling this a system trained from its record while the export and training verbs were not built | STILL THERE | cmd/nova-decide/main.go:1 opens so, and docs/SPEC-NOVA-DECIDE.md:17 says the verbs are not built |
| the how paragraph used noul with no gloss | FIXED | cmd/nova-decide/main.go:55 opens the how with the gloss: a yes-or-no question answered with a probability of yes |
| outcome's help showed only the one-word label, not the joined class form | STILL THERE | cmd/nova-decide/main.go:216 still names one word; the joined form is the spec's (docs/SPEC-NOVA-DECIDE.md:122) |
