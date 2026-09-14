# Prioritise — proposed noun and verb

*Proposal against SPEC-WORK draft 28 at 7db3b95, covering the missing verb at line 2351, baseline/focus at 1445–59, ready at 1642, events at 800–905, and undo at 2293–2307. It records explicit coordinator ordering intent, whether prompted by a human or an agreed scheduling policy. It does not dispatch, interrupt, cancel, assign, replan, or change task truth.*

## Canonical data, verb, and event

Add one fixed canonical field to every node snapshot:

    :priority (:self <rank-or-absent> :subtree <rank-or-absent>)

The order is fixed. A rank is a restricted S-expression unsigned integer atom from 0 through 999999999999999999; absent is exactly (:absent). The reader checks this proposed 18-digit bound before numeric conversion, bounding allocation even on a runtime that uses a bignum for the value. JSON wire represents rank as its exact decimal string, never a JSON number; rank 2 precedes 10. There is no stored default rank. Only these local contexts are canonical; effective rank, ordering, and latest-change lookup are derived.

Add:

    nova-work prioritise --session <path> <write flags> --node <id>
      (--set <rank> | --clear) [--context <self|subtree>] --reason <text>

Context defaults self. Set/clear are exclusive; clear refuses rank. The verb addresses only O. On settle the local priority field is retained with the node in C/history; reopen restores it. No canonical priority is erased merely because a node closed. Root work-set subtree context is the fast durable way to steer a repository subtree; a narrower context can refine it without copying rank to descendants. This is one unshipped schema amendment: pre-amendment draft fixtures are explicitly unsupported until a lossless migration is approved with the PR293 schema/no-effect boundary. No draft data is silently reinterpreted as priority.

Request JSON args are node, change (set or clear), context (self or subtree), rank (decimal string or null only for clear), and reason, with existing version-first, request, expectation, fencing, and envelope fields. The requester event is :prioritise, with common event fields then own fields in order: :change, :context, :rank, :reason. Canonical missing rank is (:absent). It is neither structure nor scope: it changes no containment, dependency, acceptance, state, generation, baseline, required set, O/C membership, W, lease, capacity, count, or percentage.

For a non-no-op, engine-derived envelope data records a fixed map `:before (:value V :change <event-id-or-absent>) :after (:value V :change <this-event-id>)` outside the requester digest. V is the rank atom or (:absent); an absent prior change is (:absent). It identifies the relevant effective-change event by its existing immutable event identity (revision plus event position/request identity); the derived per-slot index maps that identity and is rebuildable from the log. No global priority counter is added.

## Inheritance, ready ordering, and query

Effective rank is the nearest active context: node self wins; otherwise deepest subtree context at that node or an ancestor; otherwise implicit default. Clear reveals the next context. A move re-evaluates the new containment path without cloned priority events, so move must invalidate affected derived priority keys through its normal containment/index work. Priority creates no second parent.

Pin the usable ask:

    nova-work query <existing source flags> --ask ready --node <id>
      --order <discovery|priority>

Discovery is default. Priority is accepted only with ready and refuses for all other asks. Ready first applies its existing dependency, scope, acceptance, ownership, availability, resource-limit, and blocker/resolver predicate. Priority sorts only rows already eligible; it cannot grant capacity, bypass approval, take a lease, change responsibility, select a worker, start or interrupt work. Ready rows print priority=<rank|default>, priority-source=<node|default>, and priority-context=<self|subtree|default>. Priority order is explicit ranks ascending, implicit defaults afterward, then bytewise stable node id. The rank tie-break itself is independent of timestamps, clocks, arrival and rendering. Blocked rows are not filtered away: in priority mode they follow eligible rows in discovery order, retaining every existing blocker/resolver field and normal capped-page continuation. Discovery mode keeps its existing order for all rows.

Steering never rewrites baseline/discovery order or roadmap rows; source line 1448 already requires that. Priority changes only explicit ready ordering and no work/evidence truth.

## Derived index, no-op, and undo

The resident model stores reverse containment and derived priority-order keys by captured revision, ready scope, filter, and the captured readiness/lease-time watermark; it never stores copied effective rank on every descendant or a second authoritative priority table. Set/clear updates affected indexed keys or invalidates intersecting contexts. A first unseen filter may collect k eligible candidates and cost O(k log k). Thereafter its revision-pinned order index serves bounded cursor pages without sorting the candidate set on every page/query. A new query processes the existing clock/readiness barrier and never reuses eligibility from an older watermark merely because node ranks stayed fixed. Availability, hold, dependency and evidence changes invalidate affected eligibility entries. Cursor pages identify their captured revision and watermark rather than claiming the current state. Cache eviction can make a later ask first-unseen again. A root context can affect its queried subtree, never require recomputing all O or C on ordinary reads. Recovery rebuilds live priority indexes from O node fields and retained latest-change index records; it never loads all C. Historical priority/undo queries use bounded indexed history for their selected nodes.

Following proposed PR293 convention, same-value set or clear-absent appends :prioritise with derived :effect :no-change, changed=0, revision, and normal OK. Its before/after maps are identical and retain the prior effective-change identity. It changes neither slot nor that identity and invalidates no index, but produces a durable lost-reply disposition. Exact shared no-effect wire placement remains for the PR293 lock.

Undo of a non-no-op restores its exact preimage only when the current slot's latest effective-change event identity equals this event's after identity. Thus set 2, set 9, set 2 still refuses undo of the first set: same value is not same history. The compensating before/after fields are engine-derived outside requester digest. Subtree undo never rewrites inherited descendants. No-op undo-plan has no compensating state effect.

## Witnesses and review decisions

1. A blocked rank 0 task remains blocked with its existing reason/resolver while a rank 9 ready sibling ranks first among eligible rows.
2. A root subtree rank changes ready priority order without lease, attempt, dispatch, state, O/C, W, counter, baseline, or roadmap mutation.
3. Child self overrides parent subtree; clear reveals parent; settle/reopen preserves slots; moving recomputes inheritance without cloned events.
4. Rank 2 precedes 10; equal/default rows order by id across restart, handoff, cursor continuation, and skewed clocks.
5. First unseen priority filter costs O(k log k); later pages read a pinned derived order; subtree invalidation affects no unrelated scope or C scan.
6. Same-value set and clear-absent yield durable no-change receipts; a later ABA change refuses undo even if the old rank returns.
7. Capacity loss, approval withdrawal, dependency change, or hold is rechecked before ranking and never starts or interrupts work.

Friend review still decides rank-policy range, default lane position, priority display on other projections, and exact PR293 no-effect/migration codec. No consensus is claimed.

The spec integration must add this node field, event and output grammar together. Neither clearing a slot nor a no-effect event discards its earlier history.
