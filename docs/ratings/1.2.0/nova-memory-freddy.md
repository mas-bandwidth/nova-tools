# nova-memory READ rating, nova-tools 1.2.0

Rater: inception/mercury-2.5
Build: ca8bb8cc36a0
Score: 10/10
README: 10/10

## Reasons
Read cold as writing and as a CLI design: README top to bottom, docs/CLI.md, then cmd/nova-memory from main into supporting packages, help text read as text, nothing executed.

The banner answers the three core questions in sequence, states the exit table directly in the banner, provides each verb with an explicit effect line, and the setup instructions can run in an isolated scratch directory. The refusal grammar is uniform and every refusal provides a concrete remedy. The entry point clearly dispatches verbs, nouns are transparent (index, passage, receipt), and tests thoroughly verify status grammar, first-run sequences, and single-turn refusals.

The tool is runnable on its own small input with no infrastructure (quickstart writes any fixtures). Every verb supports --json. The exit codes are clearly stated (0 ran, 1 a finding, 2 could not run). No defects were found in this rating.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|

## Good, keep
- The banner answers what it does, how it works, and how to use it in the first 15 lines (main.go:43-50).
- The refusal grammar is uniform: VERB REFUSED: <reason>; run: <remedy> (oneline package).
- Every verb supports --json and shares the same output structure.
- quickstart verb provides a natural first run that writes fixtures.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| no prior rating | FIRST RATING | v1.2.0 ships this tool |
