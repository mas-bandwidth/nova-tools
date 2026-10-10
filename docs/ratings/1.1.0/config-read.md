# nova-config READ rating, nova-tools 1.1.0

Rater: GLM
Build: c28448a54d56
Score: 8/10
README: 8.5/10

## Reasons
Read cold and in order, running nothing: README top to bottom, then AGENTS.md, the nova-config section of docs/CLI.md, the spec and guide it names, then cmd/nova-config from main into the internal packages it imports. First places that pulled: confused at README.md:29, where "This is the Nova fleet configuration model." names itself and says nothing, so the actual shape (PostgreSQL the truth, Redis a rebuildable copy) only lands at docs/CLI.md:2062; bored at docs/CLI.md:2062, one sentence near 500 words that lists seven kinds in seven parentheticals a cold reader cannot hold; doubtful at docs/CLI.md:2089, whose promise that "--json on every verb but inventory prints one object in pkg/tool's shape" is not true of machine width (cmd/nova-config/machine.go:150).
Why 8 and not more: cmd/nova-config/main.go hand-renders every result twice, once as lines and once as JSON, in parallel branches across 1,646 lines (cmd/nova-config/main.go:711), so the file's bulk is duplication rather than new decisions, and a change to a result must be made in both branches; machine width --json breaks the family envelope and a written claim (cmd/nova-config/machine.go:150); the nova-config prose in docs/CLI.md is two walls (docs/CLI.md:2062, docs/CLI.md:2091); the sprint kind's Doc sentence lists ten decide bars in one breath (pkg/config/kind.go:384). Why not less: the banner answers what, how and first run, and its example needs no database (cmd/nova-config/main.go:67); the test executes that example and proves it opens no Redis (cmd/nova-config/firstrun_test.go:39); the kinds are one descriptor generating six verbs with identical flags, refusals and history (pkg/config/kind.go:329); every verb states its effect (cmd/nova-config/verbs.go:17); refusals name every problem at once with a pasteable remedy (cmd/nova-config/main.go:630); comments say why and cite the ruling section (cmd/nova-config/main.go:649). A 10 needs one renderer per result (lines and JSON from one value), the envelope on machine width, and the CLI.md walls cut to sentences a stranger parses on one pass.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | cmd/nova-config/main.go:711 | every kind verb renders its result twice, as lines and as JSON, in parallel branches; the parallel paths are most of a 1,646-line file and a result change must be made in both | build one result value per verb and render lines and JSON from it, as the skeleton's one-shape rule states | L |
| 2 | cmd/nova-config/machine.go:150 | machine width --json prints a bare object while docs/CLI.md:2089 promises pkg/tool's envelope for every verb but inventory, and machine self --json on the same kind prints the envelope | print the envelope from tool.Out as self does, or narrow the doc's sentence | M |
| 3 | docs/CLI.md:2062 | the kinds paragraph is one sentence near 500 words holding seven parentheticals, and the inventory paragraph at docs/CLI.md:2091 is longer still; the verbs table above them is the model of what they should be | one short sentence per kind and per inventory group, carrying the same facts | M |
| 4 | pkg/config/kind.go:384 | the sprint kind's Doc lists ten decide bars in one sentence, and kinds prints it as one line no reader can parse on first sight | a clause per bar, or name the field group and let each field's -h carry its bar | S |
| 5 | cmd/nova-config/main.go:35 | the file imports the legacy sort package beside the adopted slices and sorts flag names with sort.Strings | use slices.Sort and drop the second import | S |

## Good, keep
- The --file store: every verb but apply's write runs with no database, same refusals and history (docs/CLI.md:2074), and the first-run test proves the example opens no Redis (cmd/nova-config/firstrun_test.go:77).
- The kinds as descriptors: six generated verbs with one grammar, and a new kind is one descriptor and one migration (pkg/config/kind.go:329).
- The password discipline: never on the line, refused in the DSN and the fleet row, delivered by a named variable (cmd/nova-config/main.go:383, pkg/config/kind.go:504).

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| a 1,655-line main.go that writes every result twice | STILL THERE | cmd/nova-config/main.go is 1,646 lines at this head and every kind verb still carries parallel line and JSON branches (cmd/nova-config/main.go:711) |
| flags off the family's shape | CHANGED | the banner and verbs now sit on the shared skeleton with the family's --as, --json, --dry-run and refusal grammar (cmd/nova-config/main.go:67, cmd/nova-config/main.go:277); machine width --json alone still leaves the envelope (cmd/nova-config/machine.go:150) |
| contradictory storage guidance | FIXED | docs/CLI.md:2074 and docs/nova-config/README.md:9 now agree: the file is a trial store, "A file is never the fleet's store: the runtime tools read PostgreSQL" |
| the README rated 6.5 to 7 and 8.4 by different raters | CHANGED | README.md:29 now states the prerequisites and a store-free first command in the row itself; it still ends in the self-referential "This is the Nova fleet configuration model." |
