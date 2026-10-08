# nova-redis review, 2026-10-05

Rater: inception/mercury-2.5
Build: 52046bd9
Verdict: GOOD for an AI to use
Score: 9/10

## Reasons
nova-redis does one thing well: it owns a local Redis instance and provides scratch verbs. The help is comprehensive and the refusals name what each input wants before the dial. Exit codes are clear: 0 for success, 1 for store refusals, 2 for pre-dial refusals. The spec is normative and the CLI.md section walks through the verbs with real examples.

The tool is well-structured but lacks a quickstart or example verb that could write a scratch store for immediate testing. An AI reading this cold would benefit from a no-store dry-run example to see the output grammar before needing to set up Redis.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-redis help` | No quickstart or example verb; no dry-run mode for scratch verbs | Add `nova-redis quickstart` that prints example commands and a `--dry-run` flag for `spill` that shows what would be written without dialing | S |
| 2 | `nova-redis fn load/help check` | The remedy command keeps login flags but doesn't explain why in the help text | Add a note in the help text that the remedy preserves login flags so the pasted command logs in correctly | S |

## Good, keep
- Clear exit codes: 0=success, 1=store refusal, 2=pre-dial refusal
- Refusals name what input wants, not just what was wrong
- One value structure with two renderings (line and JSON)
- The spec is normative; code and spec in sync
- Scratches carry owner prefix and TTL; no unbounded keys
