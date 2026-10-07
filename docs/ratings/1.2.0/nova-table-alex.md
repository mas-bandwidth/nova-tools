# nova-table READ and USE rating, nova-tools 1.2.0

Rater: Alex (inception/mercury-2.5, using opencode)
Build: 38890605d34d
READ: 8/10
USE: 7/10

## Reasons

READ. The banner says what the tool is, how it works, and names the store it needs. It gives a throwaway-store recipe that works as printed. Every `-h` page has usage, example, flags grouped logically, exit codes, and an `effect:` line. Unknown verbs/flags are refused in one line with remedies.

What keeps READ at 8: The banner is 127 lines with prose before the example block. Some flags show `<string>` when they want numbers.

USE. The example ran, and writes print `trips=`. `--dry-run` works. Refusals have remedies. Formulas compute correctly.

What keeps USE at 7: Several silent no-ops: cell remove of absent id exits 0 without counts; move from column to itself prints a move; width for unknown column is ignored. Only batch and member read take `--json`, so reads can't use the one output shape.

## Findings

| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | cmd/nova-table/main.go:13 | banner is 127 lines with column grammar, fold table, epoch rules, shell rules before example block | move shared prose to a topic and keep one short effect line per verb | M |
| 2 | cmd/nova-table/cell.go:23 | --score is a string flag, so `cell add -h` prints `<--score <string>` | register or describe it as `<n>` | S |
| 3 | cmd/nova-table/member.go:131 | --at-epoch prints `<string>` on member read | name the value `<n>` in the usage text | S |
| 4 | internal/nsprint/verbflag/verbflag.go:113 | unknown-flag refusal lists --at-epoch, --redis but leaves out --seat | build the list from the same flag set the help prints | S |
| 5 | cmd/nova-table/cell.go:100 | cell remove of absent id prints TABLE CELL ... n=<size> at exit 0, naming no removed or absent count | print removed=<k> absent=<k>, or a distinct tag | S |
| 6 | cmd/nova-table/cell.go: cell move from column to itself prints TABLE MOVE ... from=a to=a as if it moved | refuse, or print moved=0 | S |
| 7 | cmd/nova-table/table.go | width for column table does not have is silently ignored at exit 0 | refuse an unknown column, or note it | S |
| 8 | cmd/nova-table/main.go:228 | only batch and member read take --json; show, list, cell members refuse it | give every verb --json from the value it already renders | M |

## Good, keep

The banner's throwaway-store recipe works as printed. Every write prints trips=, and --receipt gives the event id, epoch and both revisions. --dry-run makes every check the real run makes and prints the exact FCALL with dialled=0 written=0. Bound cells, standing sorts, stale or ahead epochs, col del of an occupied column, duplicate adds and a binding into table:* are each refused with a runnable remedy.

## Compared with earlier ratings

| earlier | now | evidence |
|---|---|---|
| READ 1.1.0: banner is a wall | STILL THERE | 127 lines |
| USE 1.1.0: reads have no --json | STILL THERE | show, list, cell members refuse it |
| USE 1.1.0: silent no-ops | STILL THERE | cell remove of absent id, move from column to itself |
