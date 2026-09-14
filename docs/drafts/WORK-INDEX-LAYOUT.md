# Closed and dedup index layout — proposed durable codec

*Proposal against [SPEC-WORK draft 28](https://github.com/mas-bandwidth/nova-tools/blob/7db3b95cd4c16b1eb34b1c77c0fc224755cc7a64/docs/SPEC-WORK.md), especially retention/index rules 470–641, C partitioning 535–48, recovery 584–616, and cost rules 2760–91. It proposes restricted-S-expression immutable indexes only; no scheduler, cache truth, or JSON durable format is added.*

## One atomic clip graph

A clip stages immutable closed pages, day manifests, detail chunks, dedup pages, and the snapshot header. It verifies each newly written object while streaming, then writes one commit containing every object and the header references. Only after that commit can a reader resolve the header's closed root, dedup root, and retention archive range. The header has no self-hash: parents carry child path/hash references, while an external caller hashes the header bytes if it needs an identity. A failed push leaves the prior shared graph intact and local journal work durable.

All index objects use restricted canonical S-expressions and the same configured page-byte/page-record limits, including roots, internal directories, day manifests, leaf pages, detail chunks, and locator pages. The enclosing O/retained-history snapshot keeps its separate source `--max-bytes` bound; this does not force the whole snapshot into an index page. Session start preflights every fixed-arity root/manifest/directory wrapper, not just a two-child internal page, against both limits and the shared reader depth/node/byte bounds. Root references count as records too. Exact minimum values remain a codec decision; two records alone cannot hold the four-reference closed root. Growing numeric metadata and split offsets are included in candidate representability checks. A page that cannot fit one indivisible canonical locator is not written; the owning candidate admission must refuse before acknowledgement. This is an explicit amendment to the source sentence that normal pages split rather than make a clip fail: growing records split/chunk; an indivisible unpublishable record is rejected before it enters canonical history.

## Prefix-free key and page codec

No composite key is hashed. Opaque text uses B(s): every input byte becomes bit 1 followed by its eight bits, followed by terminal bit 0. B is prefix-free and preserves raw byte lexical order: b sorts after aa, while a prefix sorts before its extension. A nonnegative revision uses N(n): unary 1 repeated for its canonical decimal digit count, then 0, then those ASCII digits; canonical integer text is 0 or nonzero-leading digits. N is prefix-free and numeric ordered by digit count then digits, so 2 precedes 10 without a 64-bit cap. Key kind fixes tuple arity, so concatenated B and N components require no extra terminator.

Keys are transported inside S-expressions as lower-case hex of MSB-first packed bytes plus the exact bit length; unused low bits in the final byte must be zero and extra bytes are refused. Readers recover exact bits before routing. The codecs are:

- day: B(YYYY-MM-DD);
- event identity: N(event revision), B(node id), the source stable key event-rev:id;
- by-id: B(id), N(revision);
- by-revision: N(revision), B(id);
- by-repo: B(repo), N(revision), B(id);
- dedup: B(request id).

Each input still obeys its source reader field bound. Additionally its encoded key plus one locator/detail reference must fit a leaf; otherwise candidate or intake refuses naming the input/bound. This supports arbitrary bounded opaque identifiers and unbounded source integers without a fabricated counter limit.

Every tree is a compact binary radix tree. An internal page records only its split-bit index, its key kind, and two child references; it repeats neither unbounded key ranges nor raw prefixes. Each child reference holds path, child hash, and record count. A leaf holds ordered complete encoded keys and values. To partition, collect the leaf bucket's complete serialized entries. If it passes both limits, emit one leaf. Otherwise find the longest common bit prefix and split at the first subsequent bit with both values; recurse. A one-key bucket exceeding a limit is indivisible and is refused by the pre-ack policy. This skips arbitrarily long single-child common-prefix chains and yields one deterministic tree for one logical key set and page configuration, independent of clip batching or insertion order. An insertion/backdated closure rewrites only its affected bucket and ancestors; it never globally repacks later keys. Different page limits require an explicit full reindex root and cannot mix under one header.

Concrete pages are:

    (:version "work-index-v1" :kind :radix-internal :key-kind :closed-by-id
     :bit 149 :records 2
     :zero (:path "closed/pages/a.sexp" :sha256 "<64-hex>" :records 71)
     :one  (:path "closed/pages/b.sexp" :sha256 "<64-hex>" :records 64))

    (:version "work-index-v1" :kind :radix-leaf :key-kind :closed-by-id
     :records 1
     :entries ((:key "..." :bits 231
                :value (:day "2026-09-14" :segment "closed/segments/x.sexp"
                        :sha256 "<64-hex>" :event (:revision 812 :id "T-7")))))

Internal references count toward page records and bytes. Content-addressed paths allow full untouched leaves to be reused while unreferenced prior pages remain immutable provenance.

## Closed root, days, canonical segments

Closed root is bounded fixed arity:

    (:version "work-index-v1" :kind :closed :revision 1200
     :page-bytes 65536 :page-records 128
     :days <page-ref> :by-id <page-ref> :by-revision <page-ref> :by-repo <page-ref>)

The day tree maps B(UTC date) to bounded day manifests. At a committed root, an exact lookup that follows the routed path, validates its pages, compares the complete leaf key and finds no day is a complete absent day, meaning zero closure rows. A named missing/corrupt child or manifest is a coverage gap, never empty history.

A day manifest has no flat segment list:

    (:version "work-index-v1" :kind :closed-day :day "2026-09-14"
     :segments <page-ref> :records 381 :first-revision 801 :last-revision 1181
     :min-stamp "2026-09-14T00:00:00Z" :max-stamp "2026-09-14T23:59:59Z")

Its segments reference the same canonical radix leaves as the day-local event-identity tree. A segment is therefore a bounded radix leaf of complete closed row headers keyed by N(revision), B(id), not an arbitrary clip-sized file. Fixed logical rows and bounds yield the same segment leaves regardless of clip boundary. Recorded stamp selects day and [from,to) filtering; revision/id is authoritative order and cursor. Each segment reference additionally carries record count, first/last revision and min/max recorded stamp. These summaries count toward serialized bounds. Day/segment min/max stamp may skip a disjoint leaf only as optimization, never as time-order proof. Updating a segment rewrites locators for its affected bounded bucket and affected ancestors; it does not rewrite all index locators. Every prior row value remains unchanged in new segment layouts and old referenced segment objects remain available.

A row header preserves the required closure fields:

    (:event (:revision 812 :id "T-7") :transition :settle :id "T-7" :under "F-2"
     :repo "o/r" :kind :task :category "bug" :disposition :done :state :done
     :generation 3 :scope-revision 9 :source-revision "abc" :required-leaves 1
     :revived (:absent) :settles 1
     :stamp "2026-09-14T12:00:00Z" :by "a" :request "q-9"
     :evidence (:root <page-ref> :records 4 :sha256 "<64-hex>"))

The newest row carries the source `revived=<rev|->` and `settles=<n>` output facts as `:revived` and `:settles`; a new transition writes their new values while prior rows retain their historical values. The example uses the source `(:absent)` form for no revive.

Every row carries its transition kind (`:settle` or `:revive`), so the newest row can determine branch membership. Source event fields referring to an earlier identity remain present; the index never infers transition kind from the request id.

Evidence detail leaves contain the five required fields plus complete canonical event identity/common provenance: revision/id, by, stamp, clock, request, generation-owner, pointer, criterion, against, and generation. Repeatable evidence is chunked behind the detail root; its rows are ordered by event identity. Other growing closure detail uses the same root/chunk rule. No row is silently shortened.

The by-id/by-revision/by-repo trees carry only bounded locators to these row headers. Current-C lookup routes exact id then takes greatest N(revision); historical/repository queries route their corresponding keys. Settle and revive append independent rows under their own recorded day and event identity, never rewrite an older row.

## Dedup, replay overlays, and queries

Published dedup contains one entry per accepted request before the retention boundary:

    (:request "q-9" :applied-revision 812 :payload-sha256 "<64-lower-hex>")

The digest is the existing requester-payload digest: it excludes session stamp, clock, request-assigned fields, and generated envelope events exactly as source canonicalization requires. Generated settle/revive/release/undo events sharing a request do not create entries. Published lookup same digest refuses already-applied; differing digest refuses reused-with-different-payload.

This does not replace local retry behavior. A local journal record retaining the original OK answers a same-payload retry with that OK. Retained snapshot events cover their request ids/payload digests together with the published pre-boundary index; they do not invent an original OK absent from the source. A hit without the retained original reply uses the source already-applied refusal. Different-payload reuse always refuses. The precise journal/reply-cache retention boundary must be integrated with recovery before locking this codec. Recovery replays journal after newest boundary into bounded local overlays: closed rows/locators merge with published roots, and local dedup preserves original OK. Overlays use bounded paged scratch storage with the same shared resident-cache limit; pages can be rebuilt from the durable journal during recovery, not by replaying the journal on each query. Overlay eviction cannot erase journal truth; the next clip incorporates it. Startup loads no whole C or dedup index.

A time query routes only intersecting day keys, then must merge their revision/id streams; it must never concatenate days. For up to configured merge fan-in F, it keeps one bounded leaf cursor per day and a revision/id heap. For more days, it creates operation-local bounded temporary locator runs and performs deterministic F-way external merge passes. Those runs are not canonical, are discarded on completion/cancel, and a page-budget exhaustion returns a query work-continuation with captured root, merge-run identity, and per-run cursor; its later page continues the same root. No row is emitted until the merge can establish that no unvisited selected stream has an earlier eligible row. Initialization may return progress/continuation with no rows. Output --max caps emitted rows only. Page-budget caps pages/segments read even when filters reject rows, so an empty filtered answer cannot scan unbounded history while claiming a small output. Exact query work-continuation wire and F policy remain an integration decision; until pinned, no bounded historical range latency claim is made.

Caches hold at most index-cache pages across roots/manifests and never establish truth. A clip verifies newly staged objects and their immediate parent references; reused immutable child references rely on prior committed parent attestations and are always hash-checked again when read. It does not rescan whole history each clip. Missing root-named members yield coverage-gap listings or required historical refusal, never fabricated empty results.

Required witnesses: a backdated stamp stays in its recorded day yet merges in revision/id order; absent day differs from named missing manifest; a busy day uses canonical bounded leaves across different clip batching; request envelopes with generated events yield one published dedup record while retained retries return original OK; huge evidence chunks; indivisible oversize field pre-ack refusal; crash yields old roots plus overlay or one new complete graph; filtered historical query reaches page-budget continuation without confusing it with output max.

## Recovery boundary companion

[The savepoint/journal proposal](WORK-RECOVERY-BOUNDARY.md) specifies a complete
envelope replay cut, logical journal identity across physical rotation, and
retained original replies until exact committed dedup coverage is accessible.
It addresses the local-retry integration gap above without changing event keys
or treating a savepoint as a clip. Physical record/header/reference forms and
snapshot-isolation mechanics still require joint integration and review.

## Review decisions before a codec lock

- Freeze exact record field order, empty roots, path derivation, minimum wrapper sizes, metadata counts, detail/segment reference forms, and the shared pre-ACK representability check. The forms above are illustrative, not a complete executable schema. No arbitrary integer cap is adopted; representability remains bounded by configured serialized resource limits.
- Freeze merge fan-in, temporary-run bounds/lifetime and work-continuation wire, including restart/expiry behavior. A page budget is a proposed addition; existing output max does not bound selective query work.
- Integrate original-reply retention/recovery and the complete manifested-range declaration. An absence answer is authoritative only within root-declared coverage through its accepted revision, never beyond it or from a partial index.
- Reconcile source `history-grows-startup-does-not` with its own permitted index-depth cost. An unrelated insertion can deepen a cold lookup path even without a whole-history scan. The witness should measure/read out that depth and exclude global scans, not promise identical page counts for every possible history distribution. Unchanged-query parses/replays remain zero.

## Finite design witness

[The finite witness](fixtures/work-index-ordering-witness.py) checks 11,004 natural integer keys (including values beyond 64 bits), 159 opaque keys, 795 composite keys, and 30 insertion permutations of 200 entries. The abstract compressed-radix tree is identical across those permutations. It also demonstrates why concatenating recorded-day streams violates revision order. This validates finite key-order/prefix and abstract record-count partition cases only. It does not test canonical file bytes, variable byte-limit splitting, locators, crash recovery, production correctness or operational token savings.
