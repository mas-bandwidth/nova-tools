# nova-table USE rating, nova-tools 1.1.0

Rater: Grok
Build: 75c8e4680221
Score: 6/10

## Reasons

`nova-table version` exits 0 and prints one version line. `nova-table help` and `nova-table -h` exit 0. That is the first successful run. It does not build a table.

The job the tool exists for is the example under `nova-table help`: create a table, add a row, add two members, move one, show, and render. The first line, `nova-table create demo --columns ready,working,done`, exits 2. It says --redis is required, or NOVA_SPRINT_REDIS, or NOVA_REDIS_ADDR, or a seat, and the next command is `nova-table help`. The same refusal meets row add, show, and render. `nova-table list --redis mem:twin` and the same create with `mem:twin` exit 2. The address is refused as a port that is not a number, and the next step is prose, give host:port or an absolute socket path, not a command. Help offers no file-backed store.

The second job is a batch. `nova-table batch` on a manifest that is not JSON exits 2, names the syntax error, says it was checked before sending and changed nothing, and says to run `nova-table batch -h`. A manifest help accepts, passed with `--json`, exits 2 because --redis is required. Nothing is applied. `nova-table batch --dry-run` is an unknown flag.

Four refusals, each one problem. A missing --columns on `nova-table create demo` names the column grammar and says to run `nova-table help`. An unknown `--bogus` lists the create flags and says to run `nova-table help create`. An unknown verb lists every verb and says to run `nova-table help`. `--score nope` says it wants a number. None of these four runs reports a second problem. `nova-table create` with no arguments names the missing name and not the missing columns. A two-word name plus a bad projection names only the projection.

`--json` is on `nova-table batch -h` and `nova-table member read -h`. `nova-table show demo --json` is an unknown flag, and the only show flags are --at-epoch and --redis. No verb help that was opened offers --dry-run. The output of a write or of show could not be checked against help, because no store was reached.

The guess is the example. Its lines name no address, and the refusal sends the reader back to help rather than a command that supplies one. Help then says an empty render prints nothing at all, and it never shows a success line, so the typed result is also a guess. `--idem` on `nova-table help row set` says it does not deduplicate, while batch help says the same operation id replays. That part is not a guess.

Not tried, because each needs a store and --dry-run is refused: create, row add, cell add, cell move, show, render, list, drop, view set, watch, shell, member read, and applying a batch. Judged from help, those verbs name their arguments, their flags, and exit codes 0, 1 and 2. They do not show the lines a success prints.

A 10 runs the example on a file store, then a second job (a batch, or a view and one watch), prints --json for show that matches the lines, and previews a write with --dry-run. One refusal names every usage problem and a next command that is the fixed line.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-table create demo --columns ready,working,done` | The example's first line exits 2 because --redis is required, and the next command is nova-table help. mem:twin is refused as a bad port. The job cannot start. | Accept a file-backed store and put that address on the example lines. | L |
| 2 | `nova-table show demo --json` | show refuses --json and lists only --at-epoch and --redis. create and batch refuse --dry-run. A read has no structured form, and a write cannot be previewed. | Offer --json on show, list and render, and --dry-run on every write, each described in that verb's help. | L |
| 3 | `nova-table create` | With no arguments the refusal names the missing name and not the missing columns. A bad projection hides a bad name. The next command is nova-table help, not that verb's help. | Report every usage problem in the one line, and point at the verb's help. | M |
| 4 | `nova-table help` | Help says an empty render prints nothing at all, and the example block shows commands but no success lines. The shell example is the one that includes an address, and that address is a live host. | Show one success transcript, state the empty-table lines, and make the shell example use the file-backed store. | S |

## Good, keep

An unknown flag lists that command's real flags and names its help page, as `nova-table create demo --columns ready --bogus` does.
A batch that is not JSON is refused before a send, says changed nothing, and points at `nova-table batch -h`.
`nova-table version` and `nova-table help` exit 0 with no store, and the first help line says what the tool does.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| the remedy after drop ping-pongs between create and set | CHANGED | `nova-table drop -h` exits 0 and describes --definition, and it does not name create or set; drop was not run, because `nova-table create demo --columns ready --dry-run` exits 2 as an unknown flag |
| reads have no --json | CHANGED | `nova-table member read -h` offers --json; `nova-table show demo --json` exits 2, unknown flag --json, show flags --at-epoch and --redis |
| the unknown-option refusal is generic | FIXED | `nova-table create demo --columns ready --bogus` exits 2, lists the create flags, and says run nova-table help create |
| three cold ratings 5, 6.5 and 7 | CHANGED | this use scores 6; the store verbs were not run |
