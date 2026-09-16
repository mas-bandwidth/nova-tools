# Coordinator-token improvement portfolio

Requested by Glenn, 2026-09-13. This collates Stella's measured-operation inventory, Rowan's own reply, the existing seven-tool efficiency cards (#80–#87), and the coordination requirements already captured in #175–#187. It records proposed work, not a new implementation campaign or extra gate for Fixed Tables.

## Ten priority tool families

Rank estimates total coordinator-token opportunity: frequency × avoidable context/turns, adjusted for repair risk, implementation overhead and confidence. It is a reasoned ordinal ranking, not measured savings or a dollars forecast. Quality is required; count both coordinators and workers, preserve token/cache distinctions, then compare weighted cost and wall time.

| Rank | Tool | Change | Issue / concrete scope |
| ---: | --- | --- | --- |
| 1 | nova-work | New tool; spec #231 | [#177](https://github.com/mas-bandwidth/nova-tools/issues/177#issuecomment-5655461179) — Priority slice: one work set, changed-only state and generated coordination views |
| 2 | nova-review | New tool; spec #236 | [#183](https://github.com/mas-bandwidth/nova-tools/issues/183#issuecomment-5655461279) — Priority slice: reusable exact-revision packets and a durable finding ledger |
| 3 | nova-wake | Extend existing tool; spec #239 | [#178](https://github.com/mas-bandwidth/nova-tools/issues/178#issuecomment-5655461377) — Priority slice: event-driven changed-only wake with honest availability |
| 4 | nova-bus | Extend existing tool | [#246](https://github.com/mas-bandwidth/nova-tools/issues/246) — nova-bus: bounded refresh/read/reply transactions that preserve delivery identity |
| 5 | nova-swarm | Extend existing tool; spec #241 | [#133](https://github.com/mas-bandwidth/nova-tools/issues/133#issuecomment-5655461544) — Priority slice: prepare workers once, validate results mechanically, bound correction cost |
| 6 | nova-tokens | Extend existing tool; spec #240 | [#181](https://github.com/mas-bandwidth/nova-tools/issues/181#issuecomment-5655461642) — Priority slice: incremental retained accounting and whole-route efficiency reports |
| 7 | nova-test | New proposed tool; name provisional | [#247](https://github.com/mas-bandwidth/nova-tools/issues/247) — Proposal: nova-test — reproducible validation recipes, run reuse and compact failure receipts |
| 8 | nova-memory | Extend existing tool | [#224](https://github.com/mas-bandwidth/nova-tools/issues/224#issuecomment-5655461854) — Priority slice: bounded source retrieval with context, corrections and continuation |
| 9 | nova-cairn | New tool; draft spec #245 | [#248](https://github.com/mas-bandwidth/nova-tools/issues/248) — nova-cairn: optional checkpoint mechanics without imposing a memory lifecycle |
| 10 | nova-release | New tool; spec #237 | [#229](https://github.com/mas-bandwidth/nova-tools/issues/229#issuecomment-5655462044) — Priority slice: one release dossier from reviewed source to verified artifacts |

The highest expected savings come from the first five. nova-tokens is the measurement dependency and should gain a small retained-event baseline early, even though its direct savings rank sixth. nova-test starts read-only; nova-cairn starts with optional append/index mechanics. Broader scheduling, daemon installation and memory lifecycle changes do not hide inside these slices.

## Complete improvement inventory and issue homes

Grouped below to avoid copying the same feature into several independently maintained issues. Top-ten issue contributions provide their concrete proposed verbs, boundaries and acceptance tests. Existing historical examples require current verification before reopening a repaired defect.

| Improvement group | Durable issue home / disposition |
| --- | --- |
| One PR/head/check/owner snapshot; changed-only queries; stop repeated Git refresh and read-backs | #177 (nova-work), #178 (nova-wake), historical #83 |
| Hierarchical S, repo/category focus, dependencies, feature × dimension roadmaps, cell subtasks and generated percentages | #177; spec #231 |
| Record once; generate ROADMAP, user status, bus facts and handoff; expansion/contraction and divergence detection | #177 #187 |
| Safe GitHub import/mapping, preserve external issues and history, idempotent absorption choices, one durable source | #177; no destructive migration started |
| Capacity census, skills/task matching, current offers, shared budgets/reserves, cheap defaults with whole-route evidence | #176 #175 |
| Corrections, reprioritization, rapid stop/cancel; acknowledge task generations and avoid duplicate writers | #179 #177 |
| One coordinator, fenced failover, stale worker exclusion, offered rest, explicit return after credit exhaustion | #180 #178 |
| Upfront decision/readiness packet, autonomous recovery, batched check-ins, resumable outages and exponential backoff | #187 #178 #180 |
| Shared polling/fetch ownership; incremental PR/comment/job/file/report events; meaningful wakes only | #178 #82 #87 |
