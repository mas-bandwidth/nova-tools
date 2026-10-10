# nova-self-talk READ rating, nova-tools 1.1.0

Rater: a large language model, cold to this tool
Build: 99e4a903966c
Score: 8.5/10
README: 8/10

## Reasons

The README row at README.md:26 says the tool "flags sentences where a writer passes a standing verdict on themselves", and the code does that in two disjoint classes, STANDING/DATED and INSTALLATION, explained at cmd/nova-self-talk/main.go:1. The first place I was confused: the usage block at cmd/nova-self-talk/main.go:25 embeds the whole shape table before the flag list, so the first help screen reads like a spec, not an orientation. The first place I was bored: internal/selftalk/selftalk.go:24 uses capitals for emphasis in a package comment, a style the rest of the family avoids. The first claim I doubted: the same package comment says a green "means ONE CLASS IS CLEAR, never that the file is" and the CLI prints the NOTE every run; that is exactly the honest reading, and the code keeps it.

The tool says what it is in one line, the verbs are named plainly (scan, shapes, example, version), and the data is markdown files named on the command line, with - for stdin. The names a stranger understands: STANDING, DATED, INSTALLATION, RANKING, FORECLOSURE, TRAIT. The comments say why in present tense: cmd/nova-self-talk/main.go:1 explains why it is an advisory instrument and never a wall, and internal/selftalk/selftalk.go:7 explains why it measures a construct and not grammar, with the history that made the old grammar measure wrong. The tests are extensive and pin the shapes and their near misses, and the audit tests assert the refusal grammar.

What keeps it from a 10: the banner wall, the capitalised comment emphasis, and the verbs are still hand-dispatched with verbflag rather than the pkg/tool Verb table, so it is only half on the family skeleton.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | cmd/nova-self-talk/main.go:25 | the usage block embeds the full shape table before the flags; a cold reader meets a spec before the tool | move the shape table under shapes or a shorter help, and keep the banner to the five-line orientation | M |
| 2 | internal/selftalk/selftalk.go:24 | capitalised comment lines for emphasis are a style the family avoids, and they read as shouting | rewrite the two lines in sentence case, keeping the content | S |
| 3 | cmd/nova-self-talk/verbs.go:23 | the verbs are hand-dispatched with verbflag and refuse(), not the pkg/tool Verb table the bigger siblings use | port scan, shapes, example and version onto pkg/tool | S |

## Good, keep

The honest NOTE on every run that a green clears known shapes and never the file. The shapes verb that prints the exact table the scan walks, with a finds and a passes sentence per row. The dated-vs-standing distinction that turns a date into a record instead of a finding.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| off the skeleton | STILL THERE | cmd/nova-self-talk/verbs.go:23 hand-dispatches with verbflag |
| an 88-line banner | STILL THERE | cmd/nova-self-talk/main.go:25 usage is long and embeds the shape table |
| comments in capitals | STILL THERE | internal/selftalk/selftalk.go:24 |
| a class name nobody glosses | FIXED | shapes prints each row with says=, finds= and passes= |
| jargon and a semantic overstatement | CHANGED | cmd/nova-self-talk/main.go:1 says advisory instrument, never a wall; the NOTE prints every run |
