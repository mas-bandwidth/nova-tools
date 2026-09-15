# nova-work — HISTORY

Struck sentences and paragraphs from `docs/SPEC-WORK.md`, kept verbatim with the date they stood.
Where each stood, the living page carries a one-line pointer.

## Resident-session paragraph

Stood in Stella's resident-session amendment `9757de0`, 2026-09-13; struck by the one-coordinator
rule of stella-0ace603bdc22:

> A rejected push or semantic conflict preserves both versions and names
> the conflicting nodes/events; never force-push or silently prefer the last writer.
> Apply nonconflicting incoming events incrementally where supported.