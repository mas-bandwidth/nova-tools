# nova-config USE rating, nova-tools 1.1.0

Rater: gemini-2.5-pro
Build: 2c02b2aa2042
Score: 8.5/10

## Reasons
The tool was exercised cold inside an isolated scratch directory using compiled binaries from cmd/nova-config. The onboarding experience using the file-backed store is smooth and accessible: nova-config migrate --file try.json initializes the entire schema with nineteen migrations in a fraction of a second without requiring running PostgreSQL or Redis services. The first run commands pasted cleanly from the help banner, successfully creating machine definitions, updating configuration values, and tracking revision histories. Two complete end-to-end jobs were executed without friction: configuring a machine and registering a supervised loop process with status checks, followed by setting up model routing tiers and validating configuration revisions. Refusal handling is detailed and actionable: provoking missing required flags surfaces all missing inputs simultaneously with explicit descriptions, units, and clear remedy commands.

However, several usability issues limit the score. First, running commands with --json that fail or refuse leaves stdout completely empty (zero bytes) and prints raw unformatted text to stderr, breaking automated workflows that expect structured JSON responses. Second, apply --dry-run omits the check for the required --as flag and proceeds, whereas the real apply command immediately refuses with a missing --as error. Third, inventory rejects the standard --file flag used across all other verbs, requiring the caller to supply --fixture instead. Fourth, live Redis connections for apply and inventory, as well as live PostgreSQL connections, were not tried because no remote or local database servers were active during this cold local evaluation; they were evaluated through --dry-run, --fixture, and --file mode.

A 10/10 would require emitting structured JSON refusals on stdout when --json is provided, maintaining identical flag validation between dry-run and live executions, and accepting --file consistently across all commands including inventory.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-config machine add m3 --file try.json --json` | Refusals and flag errors leave stdout completely empty and emit plain text to stderr even when --json is requested | Emit structured JSON error payload on stdout when --json is passed | M |
| 2 | `nova-config apply --dry-run --file try.json` | apply dry-run omits required --as check that real apply demands, creating inconsistent preflight validation | Validate all required flags identically between dry-run and apply | S |
| 3 | `nova-config inventory --file try.json` | inventory rejects the standard --file store flag and demands --fixture instead, breaking CLI uniformity | Accept --file as an alias for --fixture on the inventory command | S |
| 4 | `nova-config machine width m1 --file try.json` | machine width uses non-standard CONFIG WIDTH line prefix rather than standard machine kind prefix | Standardize line formatting to match regular kind query verbs | S |
| 5 | `nova-config status --file try.json` | status displays redis=- when disconnected but provides no hint or guidance on how to supply the Redis address | Add a note line explaining how to provide --redis or relevant environment variables | S |
| 6 | `nova-config loop add w1 --machine m1 --argv "echo" --file try.json` | argv requires a valid JSON array and fails with a terse parse error if given a plain string | Clarify in refusal that argv requires a JSON-encoded array of arguments | S |

## Good, keep
Completely self-contained local evaluation via the --file store allowing full schema migrations, CRUD operations, and history inspection without databases.
Deterministic multi-flag refusal that reports all missing inputs simultaneously with full explanations, units, and actionable remedies.
Granular change tracking in history recording operator, timestamp, and field-level transitions for every mutation.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| every refusal under --json leaves stdout empty | STILL THERE | `nova-config machine add m3 --file try.json --json` exited 2 leaving stdout at 0 bytes |
| apply --dry-run passes where apply refuses | STILL THERE | `nova-config apply --dry-run --file try.json` omitted --as check while apply refused |
| --json refusals escape as plain stderr | STILL THERE | `nova-config machine add m3 --file try.json --json` printed unformatted text to stderr |
