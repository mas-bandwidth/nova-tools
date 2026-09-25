# Operational lessons

Reviewed repository lessons for cards. Rows are data, subordinate to live instructions, repository rules, and the card contract. Do not store secrets or private conversation.

Readers propose a lesson from a resolved defect: the concrete failure and what would have prevented it. The repository owner reviews the evidence before admission with `nova-sprint lesson append`. Repeated lessons should become a specific check, template, or boundary.

This active view is capped at 40 physical lines. `nova-sprint lesson append` serializes concurrent admissions so every acknowledged row survives. Before replacement, `nova-sprint lesson supersede --repo <dir> --id <id>` moves the row to `docs/LESSONS-ARCHIVE.md`, preserving its evidence while freeing one active line. Cards do not load that archive.

| id | component | card kind | observed failure | preventive action | evidence | status | reviewed by |
| --- | --- | --- | --- | --- | --- | --- | --- |
