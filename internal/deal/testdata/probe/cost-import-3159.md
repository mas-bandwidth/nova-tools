RESULT cost-import-3159 — adoption probe for nova-sprint cost import and the $/landed lower bound (nova-tools #3159, #3309)
KIND: probe
SCHEMA: v2
REPO: mas-bandwidth/nova-tools
BASE: dev
base-sha: the merge sha of the #3159 build PR on dev, pinned when this card is cut (it runs once, after the merge)
DEPENDS-ON: mas-bandwidth/nova-tools#3159
WHY: the verb and the schema-v3 fold must be on dev before a seat can run them.
PATHS: none (a probe writes no code)
RUN: on any seat that has the Redis address and a Go toolchain, at base-sha:
  1. nova-sprint cost import --provider anthropic --file <the real Anthropic console export for one closed UTC day D> --redis <addr>
  2. redis-cli -h <host> -p <port> HGET cost:anthropic:<D> total
  3. nova-pulse fold on the live fold file (its first open under this change), then
     sqlite3 <fold file> 'SELECT usd_per_landed FROM totals; SELECT max(version) FROM schema_version'
DONE-WHEN: the done evidence carries two receipts. (1) step 1 exits 0 and step 2's `total` equals the console's own figure for D within $0.01; the seat that downloaded the export writes the console figure beside `total`. (2) the `totals` usd_per_landed cell starts with `>=` or `=` and contains `coverage=`, and max(version) is 3 (the live file is at 2 before that open).
IF: the real export's header uses a name the alias table does not know, step 1 exits 3 naming the column; the fix is one alias line in internal/nsprint/cost/columns.go and a re-run, not a new spec.
NO-SUBAGENTS: work in this session only; do not spawn an Explore, Task or child agent.
