# nova-tokens READ rating, nova-tools 1.1.0

Rater: a large language model, cold to this tool
Build: 99e4a903966c
Score: 7/10
README: 8/10

## Reasons

The README row says the tool reports token spend per day, model and repository, read from AI session logs, and the code folds declared sources into one day file per day keyed exactly by (day, model, repo); the code does that. The first place I was confused: cmd/nova-tokens/main.go:1, the package comment, is a long historical narrative about the Python scripts this replaced before it states what the tool now is. The first place I was bored: cmd/nova-tokens/main.go:456, the flag setup shared by all verbs, where every verb's flags are assembled in one function. The first claim I doubted: the help says check is "the gate", but the README row and the fold output both call check the gate; the code and spec agree, so the doubt resolved, but only after reading three places.

The code is one large file, cmd/nova-tokens/main.go at 1491 lines, holding the dispatch, the fold logic (cmdFold, 554 to 811), and the check/sum/report paths. The internal/tokens package holds the row and day-file model. The verbs are named what they do (fold, report, sum, check, sources, profiles, session), and the exit table is one sentence per code. The tests are extensive and pin the spec's demanded behaviours, including the overlap and shrink cases.

What keeps it from a 10: the 1491-line main.go, the fold verb inline in main.go rather than in internal/tokens, and the package comment that leads with history rather than the tool's shape.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | cmd/nova-tokens/main.go:1491 | main.go is 1491 lines holding the dispatch and most verbs; not one-file-one-thing | split fold, sum, check, report and sources into their own files | M |
| 2 | cmd/nova-tokens/main.go:554 | cmdFold, 257 lines, lives in main.go instead of internal/tokens beside the row model | move the fold logic into internal/tokens and keep main.go as dispatch | M |
| 3 | cmd/nova-tokens/main.go:1 | the package comment leads with the Python history before saying what the tool now is | move the history to a docs note and open with the one-paragraph shape | S |

## Good, keep

The honest "never estimates, never fills a gap, never removes a file" rule. The day-file model keyed exactly by (day, model, repo) with the five token types kept apart. The tests that pin the overlap and shrink behaviours.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| a 1,454-line main.go | STILL THERE | cmd/nova-tokens/main.go is 1491 lines |
| a 220-line fold | STILL THERE | cmdFold at cmd/nova-tokens/main.go:554 runs to line 811 |
| five line-tokens and three status words in one tool | CHANGED | the refusal grammar is now one shape per verb with `; run:` |
