# nova-config READ rating, nova-tools 1.1.0

Rater: gpt-5.6-sol
Build: 2c02b2aa2042
Score: 7/10
README: 7.5/10

## Reasons

The documentation gives a cold AI a strong conceptual boundary, a store-free first run, explicit effects, concrete refusal semantics, and detailed output shapes. The README row is unusually candid about PostgreSQL and Redis prerequisites and offers a connection-free inspection command, so it supports an adoption decision well. The command reference then provides a complete local-file workflow.

My first confusion is at `docs/CLI.md:1970`, where the overview restores a loop `width` field that the synopsis immediately above omits. The first place I become bored is `docs/CLI.md:1943`: a 25-line command inventory with several paragraph-length annotations arrives before the much clearer five-command first run. The first claim I doubt is at `docs/nova-config/README.md:146`, where width 0 is called the default despite the normative spec's unset and CPU-derived default. Reading the descriptors confirms the unset behavior and confirms that loop width is absent. The code does bear out the central storage claim: the entry point uses one store seam, the file implementation wraps the strict in-memory store and saves atomically, and first-run tests execute the documented transcript without Redis.

The score falls because the documentation disagrees with itself about live fields and defaults. The normative spec's summary still assigns `width` to loops after the later migration removes it, the command reference repeats that removed field in two places, and the dedicated guide says omitted machine width means zero even though the current contract says it means a CPU-derived default. These are operationally expensive errors for an AI that turns prose directly into commands or configuration. A 10 needs one current field inventory generated from the descriptors, synchronized guide examples, and a README first command whose output and next step are visible without another document lookup.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | docs/SPEC-CONFIG.md:52 | The normative field-placement table says a loop has `width`, contradicting the later migration contract that removes loop width and takes it from the machine. An AI may generate an invalid loop field from the highest-authority document. | Remove `width` from the loop row and add a focused text test that compares documented fields with `Kind` descriptors. | S |
| 2 | docs/CLI.md:1970 | The overview repeats `width` as a loop field even though the command synopsis correctly omits it. This makes the same reference internally inconsistent before the first-run section. | Remove loop `width` from the overview and derive or test the kind summaries against the descriptors. | S |
| 3 | docs/nova-config/README.md:146 | The guide says omitted machine width defaults to 0 and means no member; the current spec says omitted is `default`, resolved from half the reported cores, while explicit 0 means no member. | Rewrite the paragraph and examples for unset, explicit zero, and positive width, matching migration 0019. | S |
| 4 | docs/CLI.md:1999 | The inventory output list still includes loop `width`, teaching consumers to expect a field that migration 0017 removed. | Delete `width` from the typed loop-record list and pin the documented inventory keys in a text test. | S |
| 5 | README.md:28 | The selection table's first command prints migrations but gives neither output shape nor a next action, so it demonstrates contents more than a usable configuration workflow. | Add the expected summary and point directly to the local `--file` first run as the next step. | S |

## Good, keep

Keep the crisp Postgres-as-truth and Redis-as-copy boundary, including the recovery sentence.
Keep the store-free `--file` workflow and explicit descriptions of effects, exit meanings, and remedies.
Keep the README's candid prerequisite statement and harmless `migrate --print` entry point.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| 1,655-line main writes every result twice | CHANGED | `cmd/nova-config/main.go:1` is still 1,645 lines, but results increasingly share `tool.Out` and `emit`; the oversized dispatcher remains costly to read. |
| Flags depart from the tool family | CHANGED | `cmd/nova-config/main.go:426` defines the shared `--json` flag and parsing runs through `internal/nsprint/verbflag`; these are verified family-shape improvements, while this read does not reconstruct every earlier flag defect. |
| Contradictory storage guidance | FIXED | `README.md:28`, `docs/CLI.md:1970`, and `docs/SPEC-CONFIG.md:12` consistently state PostgreSQL is durable truth and Redis is a rebuildable applied copy. |
| Large command file | STILL THERE | `cmd/nova-config/main.go:1` spans 1,645 lines even after help extras moved to the 184-line `cmd/nova-config/verbs.go`. |
