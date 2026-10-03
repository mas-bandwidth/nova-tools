# nova-secrets READ rating, nova-tools 1.1.0

Rater: a large language model, cold to this tool
Build: 99e4a903966c
Score: 8/10
README: 8/10

## Reasons

The README row at README.md:25 says the tool is "encrypted secrets in a git repository, handed to one command at a time" and gives the keygen first command; the code does exactly that, with the normative contract in docs/SPEC-SECRETS.md. The first place I was confused: docs/SPEC-SECRETS.md:16 says "Five verbs" and then names ten verbs across the following paragraph, so the count and the list do not line up on first read. The first place I was bored: cmd/nova-secrets/main.go:19, the banner usage block, is around seventy lines listing every verb and every flag before any explanation; it reads as a wall. The first claim I doubted: docs/SPEC-SECRETS.md:33 says the tool's own process opens no socket, but `place` runs `ssh` and `seal`/`seat inject` run `git push` and `gh`; the paragraph immediately walks that back per verb, so the headline overstates what the reader keeps.

Everything else is strong. The tool is a thin wrapper and says so honestly: sops and age give cryptographic certainty, while per-AI unix users and the sandbox win the rest. The verbs are named by what a person would ask for (exec, names, check, gate, keygen, place, placed, seal, seat add, seat inject), and the spec explains the credential shape, the recovery key, and the invariants in order. The code is one file (cmd/nova-secrets/main.go, 840 lines) plus internal/secrets, with parseVerb at cmd/nova-secrets/main.go:165 turning the flag package's raw errors into the family's `SECRETS REFUSED: ...; run: ...` grammar, and verbEffects at main.go:119 giving each verb its effect line. The tests are numerous and pin the spec's demanded behaviours, including dry-run examples and issue regressions.

What keeps it from a 10: the banner wall, the hand-rolled dispatch beside the internal/tool skeleton the other binaries use, and the sprint-table width-key special case living inside exec at cmd/nova-secrets/main.go:263 where another tool's law has to be known to read this one.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | cmd/nova-secrets/main.go:19 | the banner usage block is around seventy lines of verbs and flags before any explanation; a cold reader meets a wall | trim the banner to the five core verbs and move the full list under help | M |
| 2 | cmd/nova-secrets/main.go:305 | the dispatch is hand-rolled with flag and verbflag instead of internal/tool, so this tool keeps a second CLI style in the family | port the verbs onto internal/tool and keep only the exec/version deviations | M |
| 3 | cmd/nova-secrets/main.go:263 | the sprint table's width key is a special case inside exec, so another tool's law must be known to read this one | move friendWidthName and refusedWidthHandWrite to internal/secrets with a spec cross-reference | S |

## Good, keep

The honest "thin wrapper over sops and age" framing and the explicit certainty boundary. The refusal grammar with one next command per problem. The tests that pin each invariant with a named case.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| a table special case inside exec | CHANGED | cmd/nova-secrets/main.go:263 now names it, documents the law, and tests it |
| a stale package doc | CHANGED | docs/SPEC-SECRETS.md:1 is the normative description and matches the verbs |
| its own refusal grammar | FIXED | cmd/nova-secrets/main.go:202 emits one `SECRETS REFUSED: ...; run: ...` via oneline.WithRemedy |
| the advertised first sitting is incomplete | CHANGED | keygen ran end-to-end here and printed the rule, the key path, and the next step |
