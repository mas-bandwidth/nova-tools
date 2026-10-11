# nova-config READ rating, nova-tools 1.1.0

Rater: gemini-2.5-pro
Build: 2c02b2aa2042
Score: 8/10
README: 8/10

## Reasons
The README introduces nova-config in its overview table at README.md:28 as managing a fleet's machines and AI friends as rows in PostgreSQL applied into Redis, providing a clean zero-dependency first command in nova-config migrate --print. The spec and command documentation present a coherent architecture separating durable SQL configuration from ephemeral runtime state in Redis.

However, a cold reading reveals three significant design friction points. The first place of confusion is docs/SPEC-CONFIG.md:18 and docs/CLI.md:1999, where Postgres is proclaimed the one true durable store for configuration while the Ansible dynamic inventory is routed exclusively to the ephemeral Redis cache rather than querying PostgreSQL directly. The second friction is boredom in docs/nova-config/README.md:179, where multiple long paragraphs recount historical migration mechanics from migrations 0012, 0014, and 0017 instead of documenting the current operating model and field contracts. The third is a doubted claim at cmd/nova-config/main.go:9, which asserts that every kind has the same six verbs generated from its descriptor, when in reality machine adds two unique verbs (self and width), singleton kinds omit add, remove, and list, and tier omits add and remove. Furthermore, cmd/nova-config/main.go:1645 hand-rolls its command dispatch in 1,645 lines, duplicating output formatting across dual JSON and text paths while failing to produce structured JSON on refusals.

A 10/10 would require refactoring main.go onto the pkg/tool skeleton, unifying the output pipeline so that text and JSON share one value without duplication, providing structured JSON refusals on stdout, aligning inventory store flags with the rest of the CLI, and trimming historical migration narratives from the reference documentation.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | cmd/nova-config/main.go:158 | Monolithic 1,645-line entry point hand-rolls command routing, flag parsing, and exit codes instead of using pkg/tool | Refactor verbs onto the pkg/tool.Tool framework to eliminate boilerplate | L |
| 2 | cmd/nova-config/main.go:276 | Refusal handlers print raw text to stderr and exit without producing structured JSON output when --json is passed | Route refusals through tool.Out to emit structured JSON on stdout under --json | M |
| 3 | cmd/nova-config/main.go:710 | Dual rendering implementation where verbs branch on asJSON to emit tool.Out versus manual fmt.Fprintf formatting | Construct a single tool.Out per verb and let Render produce both line and JSON shapes | L |
| 4 | cmd/nova-config/main.go:83 | inventory expects --fixture while all other verbs take --file to operate against local file storage | Accept --file as a supported flag on inventory to maintain CLI consistency | S |
| 5 | docs/nova-config/README.md:179 | Historical migration details for old schema versions clutter the user documentation with obsolete upgrade notes | Relocate historical migration walkthroughs into a dedicated migration doc | S |
| 6 | cmd/nova-config/main.go:9 | Header comments claim every kind has the same six verbs though machine has eight and singletons have three | Update doc comment to accurately describe singleton and machine verb variations | S |

## Good, keep
Strict architectural separation between durable PostgreSQL storage and ephemeral Redis projection with revision-based compare-and-set.
Zero-dependency local experimentation via the --file JSON store allowing all verbs to be verified without running database daemons.
Descriptor-driven schema and validation enforcing declarative kind definitions across SQL migrations and CLI flags.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| a 1,655-line main.go that writes every result twice | STILL THERE | cmd/nova-config/main.go:1 |
| flags off the family's shape | STILL THERE | cmd/nova-config/main.go:83 |
| contradictory storage guidance and a large command file | CHANGED | docs/SPEC-CONFIG.md:8 |
| README rating then: 6.5 to 7 from one rater, 8.4 from another | CHANGED | README.md:28 |
