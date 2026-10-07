# nova-table READ and USE rating, nova-tools 1.2.0

Rater: GLM z-ai/glm-5.3-flash in opencode, a sprint worker on a friend's re-rate card
Build: c76fcb249cc1
READ: 8/10
USE: 8.5/10

No v1.2.0 tag exists on the forge yet, so this rates the release candidate at the head of
sprint/mechanical-2026-10-02. `nova-table version` prints
`nova-table v1.0.1-0.20261007015429-c76fcb249cc1 linux/amd64 go1.26.6`. Built and run on a Linux
bench machine, in a scratch directory made for the trial. No server was started here and no live
store was touched, so the tool was used the way its banner offers a store-less bench: every write
verb ran for real under `--dry-run` (create, row add, cell add, cell move, set, col add, row sort,
batch, view set), every read verb, watch and shell was run and refused with its remedy, every verb
answered `-h`, and the banner, the guide, docs/CLI.md, docs/SPEC-NOVA-TABLE.md and the README row
were read cold.

## Reasons

READ. The banner answers the three questions in order and in five lines: what it is, how a table
works (rows, columns, one ordered set per cell, a projection per column, a fold per footer), and a
first run that needs a Redis 7 or later — with the store-less door stated in the same breath
("every verb that writes runs under --dry-run"). The throwaway-store recipe sits in the banner with
its stop command, the stale-epoch refusal is quoted with its exact line, and `idem` is documented
honestly as receipt metadata that does not deduplicate retries. Every verb page carries usage,
example, flags, connection, exit codes and an `effect:` line that says which checks are made
before sending and which are the store's. The guide opens with the design statement; docs/CLI.md's
First run explains the function load in full (first contact, `trips=` counts it, a library the
store holds is kept) and was produced by running the tool, pinned by cmd/nova-table/firstrun_test.go;
the package doc states its layout in the present tense with no names or dates. This is writing a
stranger can act on.

What keeps READ at 8. The banner is still a wall: `usageDetails` (cmd/nova-table/main.go:35) holds
about 56 lines of spec prose — epochs, the column grammar, folds, order, shell, the store chain —
before the `example:` block at cmd/nova-table/main.go:91, and the column grammar is restated in
docs/CLI.md, docs/nova-table/README.md and docs/SPEC-NOVA-TABLE.md, so drift is a standing risk.
`--json` reaches batch and member read alone (cmd/nova-table/main.go:228) while
docs/STANDARD.md:56 promises every verb accepts it, so list, show, render, cell members, check and
view show leave a program parsing drawn text. The README's setup sentence (README.md:26) still
says only "nova-table loads the functions it needs" — which functions, loaded where, and what a
refused load does live in First run, not here. A seat comment still opens on history
(cmd/nova-table/main.go:249), and the general renderer still imports the card-cost cents formatter
(internal/ntable/render.go:12, used at render.go:590) against the package doc's own "knows nothing
about sprints".

USE. The job ran end to end under `--dry-run`: a table built one verb at a time (create, row add,
cell add, cell move), each plan line naming the exact call it would send
(`TABLE DRY-RUN verb=cell-add arg1=demo arg2=build arg3=ready arg4=b1 sends="FCALL
ns_table_cell_add" redis=- dialled=0 written=0`), and a duplicate cell add plans identically
because the dedup refusal is the store's — the effect: line says so before you run it. batch under
`--dry-run --json` prints the plan as one JSON object (`dry_run:true, dialled:0, written:0,
sends:"FCALL ns_table_apply"`), and a refused manifest under `--json` prints the standard result
envelope (`{"result":{"verb":"batch","status":"refused","exit":1,"remedy":"nova-table batch
-h","why":[...]},"facts":{}}`), so a program gets one shape. Every refusal met — missing required
flag, unknown flag, unknown verb, bare group, bad value (`--score notanumber`, `--epoch x`), a
formula over columns that do not exist, a bound row without `--owner`, a dead address, no store —
names the problem, the valid alternatives and one runnable next command; the dead-address refusal
names the address, what came back, and the recipe; the reads' no-store refusal prints the whole
throwaway-store recipe. Help is layered (help, help <verb>, help <group>, <verb> -h) and exits 0
on stdout. member read is now the read set as a verb, with `--cell` and `--json`.

What keeps USE at 8.5. No daemon may start here and `--redis mem:` is refused ("not an address:
its port is not a number from 1 to 65535"), so every read verb (list, show, render, check, member
find, cell members), view show, watch and shell is judged from its help alone; the refusal's remedy
is a server start, which this bench forbids. A manifest with several faults is refused one problem
per attempt: `{"schema":1,"table":"demo"}` names operation_id alone, and the same unusable epoch is
refused at two exits in two shapes — the key missing exits 1 with `code=EPOCH`
(internal/ntable/manifest_validate.go:628), the value `"x"` exits 2 as "invalid batch manifest"
with no `code=` (cmd/nova-table/batch.go:117). Only the first of two unknown flags is named. A ten
needs a store-less way to run the reads end to end, `--json` on every verb, and manifest checks
that name every fault at once.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | cmd/nova-table/main.go:35 | usageDetails keeps about 56 lines of spec prose (epochs, the column grammar, folds, order, shell, the store chain) between the usage list and the example: block at main.go:91; the grammar is restated in docs/CLI.md, docs/nova-table/README.md and docs/SPEC-NOVA-TABLE.md, so drift is a standing risk | keep the three-question banner, the verb table and the example: lines; one sentence and a pointer per subject; the details stay in each verb's help | M |
| 2 | cmd/nova-table/main.go:228 | --json reaches batch and member read alone; docs/STANDARD.md:56 promises every verb accepts it, so list, show, render, cell members, check and view show leave a program parsing drawn text | build the one value each verb already renders and encode it when --json is asked | M |
| 3 | `nova-table list` | with no daemon permitted here and `--redis mem:` refused ("not an address: its port is not a number"), no read verb (list, show, render, check, member find, cell members), view show, watch or shell can be tried at all; the refusal's remedy is a server start | accept a mem: address (or an embedded store) so the reads run end to end with nothing dialled | L |
| 4 | internal/ntable/manifest_validate.go:246 | the manifest validator returns on the first fault: a manifest missing epoch, expected_table_revision, operation_id and members is refused naming operation_id alone, so the caller fixes one fault per attempt | check the whole manifest and name every problem in one refusal | S |
| 5 | cmd/nova-table/batch.go:117 | the same unusable epoch is refused at two exits in two shapes: the key missing exits 1 with `code=EPOCH` (manifest_validate.go:628), the value `"x"` exits 2 as "invalid batch manifest" with no `code=`; a reader cannot tell which half a fault takes without the spec's fine print | classify once and print one shape: every manifest fault carries `code=` and one exit | S |
| 6 | `nova-table row add demo build --bogus=1 --alsobad` | only the first unknown flag is named, though both were given | collect every parse error before refusing | S |
| 7 | cmd/nova-table/main.go:249 | the seat-resolution comment opens on history ("the old nova-sprint's seat.go, nova-tools#4330, carried here") against the tree's own citation rule | say why the code is so, in the present tense; the ticket belongs in the design note | S |
| 8 | internal/ntable/render.go:12 | the general table renderer imports the card-cost cents formatter (used at render.go:590) while the package doc says the package knows nothing of sprints: a sprint-shaped dependency in a general file | fold the numbers in the table; let the caller format money | S |
| 9 | README.md:26 | the setup sentence still says "nova-table loads the functions it needs" with no word of which functions, loaded where, or what a refused load does; docs/CLI.md's First run and the banner both say it in full | say the load the way First run does: first contact, `trips=` counts it, a library the store holds is kept | S |
| 10 | cmd/nova-table/session.go:165 | shell's no-store refusal is the short form ("--redis <addr> is required (or a configured seat)") while every other store verb names the address, what came back and the throwaway recipe | print the reads' recipe from shell too | S |

## Good, keep

The refusal grammar: one line, the problem, the valid alternatives, a runnable next command; the
dead-address refusal names the address and what came back, and the reads' no-store refusal prints
the whole recipe with its stop command. `--dry-run` that prints the exact call the verb would send
(`sends="FCALL ...", dialled=0, written=0`), refuses where the real run refuses before sending, and
says honestly in its `effect:` line what only the store can check. Layered help (help, help <verb>,
help <group>, <verb> -h) at exit 0 on stdout, every page ending in exit codes and `effect:`, and
the banner's first example line runs with no store and says so. member read as the read set as a
verb, with `--cell` and `--json`, and the batch `--json` refusal carrying the standard result
envelope. The epoch and receipt honesty: every write names the epoch it read and prints a receipt,
`idem` is documented as metadata only, and drop's definition-keeping is spelled out.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| 1.1.0 READ 7.5: the guide opens on transcribed design chat | FIXED | docs/nova-table/README.md:1 opens "## The design" with the primitive, no quotations |
| 1.1.0 READ: the package doc opens on a dated quotation naming a person | FIXED | internal/ntable/ntable.go:1 states the layout in the present tense, no names or dates |
| 1.1.0 READ: five dead oset helpers unreachable and still presented in the guide | FIXED | the helpers are deleted (internal/ntable/oset.go holds only the queued count and move plumbing) and the guide no longer presents them |
| 1.1.0 USE: the reads have no --json (member read listed among them) | CHANGED | member read is now the read set as a verb with --json and --cell (`nova-table member read -h`); list, show, render, cell members and view show still print drawn text |
| 1.1.0 USE 8.5: a manifest with several missing keys is refused one problem at a time | STILL THERE | `{"schema":1,"table":"demo"}` names operation_id alone at exit 1 |
| 1.1.0 USE: --redis mem: refused, so the reads cannot be tried without a daemon | STILL THERE | `nova-table list --redis mem:` → "not an address: its port is not a number from 1 to 65535" |
| 1.1.0 READ: the README's setup sentence hides the load | STILL THERE | README.md:26 |
| 1.1.0 READ: the banner is a wall before the example | STILL THERE | cmd/nova-table/main.go:35 usageDetails is about 56 lines before the example: block at :91 (was about 80; help now 127 lines) |
| 1.1.0 READ: history prose in a seat comment | STILL THERE | cmd/nova-table/main.go:249 |
| 1.1.0 READ: the card-cost import in the general renderer | STILL THERE | internal/ntable/render.go:12, used at :590 |
| snapshot 0c5803c2de40 USE 7 and 1.1.0 USE 8.5 | CHANGED | USE holds at 8.5 on this head: the dry-run path and the refusal grammar unchanged, member read gained --json, the reads still need a daemon |
