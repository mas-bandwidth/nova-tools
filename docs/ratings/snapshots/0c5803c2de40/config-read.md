# nova-config READ rating, current baseline 0c5803c2de40

Rater: a flash-class language model rater, reading cold, with no memory of this tool's code, its makers' reasoning or its earlier ratings

Build: 0c5803c2de40

Score: 7.5/10

README: 7.5/10

## Reasons

The README row that opens the tool is README.md:28: "Keep fleet configuration durable." with the tool's one sentence, "a fleet's machines and AI friends as rows in PostgreSQL, applied into Redis", and a first command, `nova-config migrate --print`, that really connects to nothing (the verb returns before any store is opened, cmd/nova-config/main.go:1075). The banner's line 1 is that same sentence (cmd/nova-config/main.go:67), the example block runs with no database (docs/TESTS.md:698-714 holds the transcript), and the guide (docs/nova-config/README.md) is the best writing here: it states the boundary (Postgres permanent, Redis a rebuildable copy), walks the first run on a local file, and ends with "What is deliberately not here". The design underneath is the reason to trust it: one descriptor per kind (internal/config/kind.go:232) generates the six verbs, the SQL, the refusals and the apply diff, so a new kind is one descriptor, one migration and one Redis writer; every comment says why and cites the section it serves; the dry run walks the same checks as the write; refusals carry a pastable remedy; the file store is the test store (internal/config/file.go:28), so there is exactly one store contract.

Where the reading cost me:

- Confused first at README.md:28, cell four: "This is the Nova fleet configuration model." — a capitalised name of a model the README never defines and does not link; I only learn the contract is SPEC-CONFIG.md two clicks later, in docs/CLI.md:1970.
- Bored first at README.md:30: the table rows are 400-600 characters of multi-sentence setup prose in HTML cells; by the secrets row I was skimming past the setup I needed.
- Doubted a claim at README.md:48: "These are the Nova Tools 1.0.0 commands", pointing to the 1.0.0 release — but the v1.0.0 tree carries no nova-config binary at all (git ls-tree v1.0.0 cmd lists no nova-config), so the row's first command cannot run on the release the README names.

What keeps the score from 10: the normative spec's examples still use the fleet's real machine, seat and friend names (nine of them, parked as counted debt), the command file is one 1,645-line wall, a dozen verbs render their result twice by hand, the command-reference section is two ~2,000-character paragraphs, the adoption guide the README calls "start here" has no nova-config section, and the README's release claim is false at the release it names. A 10 needs: the spec's examples rewritten to the guide's generic placeholders and the debt rows deleted; main.go split on the pattern verbs.go and machine.go started; one builder per verb whose value renders both as lines and as JSON; the reference paragraphs broken up; a nova-config section in the usage guide; the README naming a release that carries the tool.

## Findings

| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | docs/SPEC-CONFIG.md:120 | The normative spec's example refusals and handover use the fleet's real machine, seat and friend names (nine occurrences, docs/SPEC-CONFIG.md:120-123, 137, 428, 526-527), held back only by six rows of counted debt in internal/ci/testdata/generality-text/docs.txt:22-26; the guide writes the same examples with generic m1 and f1, so the contract a stranger reads models the exact practice the generality rule refuses and reads like a private notebook | Rewrite the spec's examples to the guide's generic names and delete the six debt rows | M |
| 2 | cmd/nova-config/main.go:601 | One 1,645-line main.go still carries the dispatch, the flag and refusal helpers, five kind verbs, migrate, status, apply and inventory (runKindWrite at 601, runApply at 1374, runInventory at 1506); the earlier split began (verbs.go, machine.go) but left the wall a reader wades to reach any verb | Split on the started pattern: kinds_write.go, migrate.go, status.go, apply.go, inventory.go | M |
| 3 | cmd/nova-config/main.go:710 | A dozen verbs render their result twice by hand: the JSON facts built on tool.Out and the typed line printed beside it as separate statements (710-724, 936-948, 1188-1196, 1302-1315), so each pair can drift one edit at a time, though the skeleton's own render prints both from one value (internal/tool/out.go:185) | Build one Out per verb and render lines from it, as the skeleton does | M |
| 4 | docs/CLI.md:1970 | The tool's definition paragraph is one ~1,400-character sentence listing all seven kinds inline, and the inventory paragraph at 1999 runs ~2,000 characters; the per-line usage comments above (1945, 1957, 1965) carry 300-character clauses, so the reference the stranger opens first is the densest prose in the tree | List the kinds as short bullets, keep each usage comment to one clause, and let the guide carry the detail | M |
| 5 | README.md:48 | "These are the Nova Tools 1.0.0 commands" with install advice to the 1.0.0 release, while the table's nova-config row (README.md:28) names a tool the v1.0.0 tree does not carry (git ls-tree v1.0.0 -- cmd lists no nova-config); a reader who follows the install advice for this row gets no binary | Say the commands are the current tree's, or name the release that carries each tool | S |
| 6 | docs/USAGE.md:140 | The adoption guide the README calls "start here" and "why each tool helps" (README.md:95-97) covers nine tools and has no nova-config section at all, though README.md:82-84 names it as one of the two tools that need more setup | Add the nova-config section: what it owns, its Postgres and Redis prerequisites, and the --file trial that needs neither | S |
| 7 | cmd/nova-config/machine.go:150 | machine width --json prints a bare {"machine","width","member"} object instead of the result envelope every other verb uses, a second JSON shape in one tool (documented at docs/SPEC-CONFIG.md:168, but off the family's one-value-two-renderings rule) | Return the same facts inside the result envelope, or state the exception beside the rule | S |

The rated source is exactly 0c5803c2de406c1b0b2b0841f579c9bf73406b1c; every file:line reference below is at that snapshot.

## Good, keep

The one-descriptor design (internal/config/kind.go:232): verbs, SQL, refusals, history and apply diff all generated from it, so a new kind is one descriptor and every kind behaves identically. The refusal grammar with a pastable remedy on every line, verified against the code at every spot I checked. The --file store (internal/config/file.go:28), the same strict Mem the tests hold to the store contract, so the whole tool is tryable with no database.

## Compared with earlier ratings

| earlier | now | evidence |
|---|---|---|
| a 1,655-line main.go that writes every result twice | CHANGED | wc -l cmd/nova-config/main.go is 1,645 and the wall is now three files (verbs.go, machine.go split out), but a dozen verbs still render lines and JSON by hand (cmd/nova-config/main.go:710-724) |
| flags off the family's shape | STILL THERE | the verbs hand-roll dispatch, banner and refusal printing beside the shared skeleton (cmd/nova-config/main.go:223-270), and machine width --json is a bare object outside the result envelope (cmd/nova-config/machine.go:150); the refusal grammar, --as, --json, --dry-run and the exit table are on the family shape |
| contradictory storage guidance and a large command file | CHANGED | the guidance is now consistent everywhere I read (Postgres permanent, Redis a rebuildable copy: docs/SPEC-CONFIG.md:12-20, docs/nova-config/README.md:9-13, cmd/nova-config/main.go:67-76); the command file is still large (cmd/nova-config/main.go, 1,645 lines) |
| README then: 6.5 to 7 from one rater, 8.4 from another | CHANGED | the row's sentence and the banner's line 1 now agree (README.md:28, cmd/nova-config/main.go:67) and the first command connects to nothing, but the door gained a false claim: README.md:48 names the 1.0.0 release for commands that include nova-config, which the v1.0.0 tree does not carry |
