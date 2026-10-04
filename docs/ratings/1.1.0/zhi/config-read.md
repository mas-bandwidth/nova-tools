# nova-config READ rating, nova-tools 1.1.0

Rater: Claude <deepseek-v4-pro>
Build: 2c02b2aa2042
Score: 8/10
README: 8.5/10

## Reasons

The tool's own guide (docs/nova-config/README.md) says what the tool is for in
three lines: the one tool for the fleet's permanent, non-ephemeral
configuration, which owns Postgres and applies it into Redis as a rebuildable
copy. The first run needs no database (`--file try.json`), and every verb's
`-h` prints a worked example, so a stranger can try it cold. The code bears
the doc out: cmd/nova-config/main.go dispatches to six generated verbs per
kind, and the kinds are descriptors in internal/config/kind.go, so the flags,
help, refusals, SQL, history and apply diff all come from one source. Names
read as a stranger wrote them; comments say why, in the present tense; tests
are thorough (main_test.go plus about nine thousand lines under
internal/config). It sits in the family: internal/tool, internal/nsprint's
verbflag, the one refusal grammar, the 0/1/2 exit table, `--json` and
`--dry-run`.

First place I was confused: docs/nova-config/README.md:179 ("Migrating to a
set width") — the migration-0012 backstory of how width used to be derived is
woven into current use and needs a second read.

First place I was bored: docs/SPEC-CONFIG.md:279 — the route price sheet lists
fourteen fields before saying what to do with them; it reads as inventory, not
guidance.

First place I doubted a claim: docs/nova-config/README.md:470 — "Every apply is
compare-and-set on a revision"; the claim holds, but only after
docs/SPEC-CONFIG.md:478 spells out the WATCH and the stamp.

The score is held down by weight, not clarity: the whole command surface lives
in one 1,645-line main.go with the same store/actor/json/dry-run flag plumbing
repeated in each verb, and two verbs (status, migrate --dry-run) still assemble
the line and the JSON object side by side, so one result is built twice. A 10
folds that shared flag set and the store preamble into the internal/tool
skeleton so each verb is a few lines and one value renders two ways.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | cmd/nova-config/main.go:1645 | the whole command surface is one 1,645-line file: runKinds, runKindWrite, runKindRemove, runKindList, runKindRead, runMigrate, runStatus, runApply, runInventory, runMachineSelf and runMachineWidth each re-declare the same --pg/--file/--as/--json/--dry-run flags and the store-open, behind-schema preamble | move the shared flag set and the store preamble into internal/tool or one helper, so a verb is a short function that names its nouns | L |
| 2 | cmd/nova-config/main.go:1300 | status and migrate --dry-run build the result twice: the typed line (or lines) and the tool.Out facts and items are assembled side by side, so the two renderings can drift instead of one value rendering two ways | build one tool.Out per verb and render the line and --json from it (the standard's one output structure, two renderings) | M |
| 3 | cmd/nova-config/main.go:105 | machine self carries a private exit code 3 (cannot read) outside the family's 0/1/2 table, so a script special-cases one verb | fold the cannot-read case into exit 2, or give the whole tool one shared three-way table | S |

## Good, keep

The descriptor-driven kind design: one Kind descriptor yields the six verbs,
the flags, help, refusals, SQL, history and the apply diff, so a new kind adds
no verb code (internal/config/kind.go:1-10). The refusal grammar: one stderr
line naming every problem at once and the next command, with --pg or --file
repeated so the remedy pastes (cmd/nova-config/main.go:272-299). The --file
store: every verb but apply's write runs against a local JSON file, so a cold
reader tries the whole tool with no database (docs/nova-config/README.md:35-48).

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| 1,655-line main.go that writes every result twice | CHANGED | cmd/nova-config/main.go:1645 — still one file, but most verbs now build one result; only status (main.go:1300) and migrate --dry-run (main.go:1151) assemble line and JSON together |
| flags off the family's shape | FIXED | cmd/nova-config/main.go:227 — dispatch goes through internal/nsprint's verbflag and internal/tool; --json, --dry-run, --as and the refusal grammar match the family |
| contradictory storage guidance | FIXED | docs/nova-config/README.md:9-13 — Postgres is the permanent store, Redis is a copy of it, consistent through docs/SPEC-CONFIG.md:12-25 |
| a large command file | STILL THERE | cmd/nova-config/main.go:1645 — the command surface is still one 1,645-line file |
