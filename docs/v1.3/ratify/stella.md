OBJECT 939b3b324ee68b34d57bbe3163df302e81d36555

I read all seven files under `docs/spec/v1.3` at this version. The mandatory psychic link, sender and recipient refusal while unhooked, Redis authority, disposable one-second caches, retained uncertain claims and one claim per card are the required foundation. The changes below resolve conflicting transitions before modelling or implementation. This review concerns v1.3 friend reliability and does not expand the fleet-only v1.2.x scope.

Quoted source below preserves the exact wording, with Markdown line wrapping collapsed.

1. **Make the self-test reachable after proof.**

   Why: Layer 30 guarantee 6 refuses send and recv before a self-test whose first action is send-to-self. A newly proven session cannot pass that test. Layer 20 permits bus use after proof, while M2 requires selfTest only for deals and takes. Separate those gates without allowing any unhooked bus traffic.

   File: `docs/spec/v1.3/30-harness-hookin.md`.

   Old: The gate opens only when her own session has proven the fresh challenge and a self-test shows the push landing in that live session: a send-to-self arrives in-turn and is answered from that turn.

   New: The bus gate opens when the session proves the fresh challenge delivered through its link; the work gate opens only after a send-to-self arrives in-turn and is answered from that turn, recording a passed self-test for the current generation.

   File: `docs/spec/v1.3/30-harness-hookin.md`.

   Old: Observation: in a fresh session, a send, a recv and a take before the self-test are each refused with the remedy; after it, each succeeds.

   New: Observation: before proof, send, recv and take are refused; after proof, send-to-self and recv succeed but take remains refused until that self-test is answered from the turn; after the self-test, take succeeds.

2. **Keep renewal from demoting a live proof.**

   Why: Layer 20 guarantee 4 makes every challenge transition to CHALLENGED, but its IssueRenewal transition explicitly preserves PROVEN. Following guarantee 4 unhooks every healthy friend at renewal issuance and refuses sends for the duration of the answer, contrary to M1 liveness. The existing deadline remains authoritative.

   File: `docs/spec/v1.3/20-comms.md`.

   Old: Generating a challenge nonce transitions lease status to `CHALLENGED` with a finite challenge deadline ($T_{challenge}$).

   New: Generating an initial challenge nonce transitions lease status to `CHALLENGED` with a finite challenge deadline ($T_{challenge}$); issuing a renewal challenge preserves `PROVEN` and its existing lease deadline until a timely answer renews it or that deadline expires.

3. **Attribute late evidence to the historical claim without bypassing the link fence.**

   Why: After reassignment, the current holder can differ from the author of the old run. The late report must identify the historical claim and execution fence, not inherit the new holder. Layer 10 forbids stale sessions from reporting new progress and layer 20 refuses unhooked sends; recording evidence must therefore be distinct from advancing current work. A current authenticated session can submit that historical evidence after re-hooking, with ordinary operation receipts preventing duplicate records.

   File: `docs/spec/v1.3/40-friend-lane-client.md`.

   Old: A finish is recorded under the friend the server shows holding the claim, read from the server claim and never from a branch suffix or a name the client supplies.

   New: A finish names its immutable claim ID and execution fence; the server attributes it to that historical claim holder, never to a replacement holder, branch suffix or client-supplied author name.

   File: `docs/spec/v1.3/40-friend-lane-client.md`. Apply to both occurrences.

   Old: A finish that arrives after its claim was released or reassigned is recorded as a late finish under that same friend and held for reconciliation; it is never refused without a record, and it merges nothing on its own.

   New: After the historical holder re-hooks, an authenticated current session may submit evidence for a released, reassigned or superseded execution fence; the server records it idempotently as a late finish under that historical holder and holds it for reconciliation, without advancing the current claim or merging anything; unhooked and stale-session submissions still receive the link or session refusal and may be retried through the current binding.

   File: `docs/spec/v1.3/40-friend-lane-client.md`.

   Old: The result is on the server before the run's process exits, recorded under the friend the server shows holding the claim.

   New: The result is on the server before the run's process exits, attributed by its immutable claim ID and execution fence under guarantee 11.

   File: `docs/spec/v1.3/60-state-machines.md`.

   Old: LateFinish: a finish on a released or reassigned claim is recorded under the server's holder (40 guarantee 11).

   New: LateFinish: a current authenticated, hooked session of the historical holder submits evidence for its immutable claim ID and execution fence; the server records it idempotently under that historical holder and holds it without advancing the current claim (40 guarantee 11).

   File: `docs/spec/v1.3/60-state-machines.md`.

   Old: Every finish is recorded under the friend the server shows holding the claim; no finish is refused without a record (40 guarantee 11).

   New: Every valid finish is attributed to its historical claim holder; released, reassigned or superseded execution evidence is held, while unhooked or stale-session submissions cannot advance work (40 guarantee 11).

4. **Keep deal and take as different transitions.**

   Why: Layer 40 makes Deal move idle to taken, while layer 50 and M6 require that a deal creates no take. The two implementations acquire ownership and consume capacity at different events. Keep the existing dealt offer in the server assignment record and acquire a lane claim only at Take.

   File: `docs/spec/v1.3/40-friend-lane-client.md`.

   Old: `taken`: the server has recorded the deal to this friend, and the run has not started.

   New: `taken`: the server has accepted the lane's take and recorded its claim for this friend, and the run has not started.

   File: `docs/spec/v1.3/40-friend-lane-client.md`.

   Old: `Deal`: `idle` to `taken`. The server does this, only to a hooked friend (guarantee 10). A client with `width` lanes already `taken`, `running` or `needs-reconciliation` is not dealt another.

   New: `Deal` records an offered card in the server assignment and leaves the lane `idle`; `Take` moves `idle` to `taken` only after the server atomically checks the offered assignment, current hooked and self-tested binding, one claim per card and available width, then records the claim. A client with `width` lanes already `taken`, `running` or `needs-reconciliation` is not dealt another.

   File: `docs/spec/v1.3/60-state-machines.md`.

   Old: Deal (to a hooked friend only), start, finish, fail, release (40, state and transitions; guarantees 3 to 5 and 10).

   New: Deal (an offered server assignment, no lane transition), take (atomically acquire the claim for a hooked, self-tested friend), start, finish, fail, release (40, state and transitions; guarantees 3 to 5 and 10).

   File: `docs/spec/v1.3/60-state-machines.md`.

   Old: Take under the claim; capacity claim checked atomically at assign (10, state table; guarantee 2).

   New: Take acquires the claim atomically after checking the offered assignment, current binding, one claim per card and available capacity (10, state table; 40 guarantee 10; 50 guarantee 6).

   File: `docs/spec/v1.3/60-state-machines.md`.

   Old: v1.3 adds only the layer-40 statement of the same states (taken for dealt-and-claimed, finished and failed split) in the vocabulary of layer 40.

   New: v1.3 distinguishes an offered deal from taken after claim acquisition, adds needs-reconciliation and held late finishes, and separates finished from failed in the vocabulary of layer 40.

5. **Define clean verdict carry as source review reuse, never build reuse.**

   Why: Layer 50 explicitly allows clean base-merge carry, yet guarantees 1, 3 and 5 and M7 require a fresh read of every exact head. Keep the chosen carry narrowly: it is an explicit structured source-review carry, with an unchanged diff digest and no touched card files; the entire resulting tree must pass the Linux bench gate again. Otherwise either valid carry is impossible or an ungated integration can merge.

   File: `docs/spec/v1.3/50-sprint-delivery.md`.

   Old: The Linux bench gate runs on the exact head sha that will be merged, and the gate, the cold read and the merge all name that same sha.

   New: The Linux bench gate runs on the exact final head sha that will be merged; source review names that head directly or reaches it through a structured clean base-merge carry naming the original verdict, old and new head, old and new base, and unchanged card diff digest.

   File: `docs/spec/v1.3/50-sprint-delivery.md`.

   Old: Any move of the head — a new commit, a rebase, a base merge that changes the card's own files — invalidates the gate and the verdict, and both run again on the new head.

   New: Every head move invalidates the bench gate and requires it to run on the new full tree; it also invalidates the verdict unless the only move is a clean base merge that leaves the card's own files and diff digest unchanged, verified and recorded at the fence.

   File: `docs/spec/v1.3/50-sprint-delivery.md`.

   Old: There is one fence: nothing crosses to merge on a sha that was not itself gated and read.

   New: There is one fence: the exact final head must be gated, and its source review must be either a direct cold read or the verified structured carry above.

   File: `docs/spec/v1.3/50-sprint-delivery.md`.

   Old: The cold reader's MERGE stands beside a green build of the exact head on the Linux bench: the same sha that was read is the sha that built green.

   New: The cold reader's MERGE, or its verified structured carry under guarantee 1, stands beside a green Linux bench build of the exact final head; no carry reuses an older head's build result.

   File: `docs/spec/v1.3/50-sprint-delivery.md`.

   Old: Authors push candidates; the cold reader records MERGE, FIX or HOLD on the head sha; the coordinator alone performs the merge, and only on a candidate sprint branch whose gate and verdict both name the current head.

   New: Authors push candidates; the cold reader records MERGE, FIX or HOLD on the head sha; the coordinator alone performs the merge, and only on a candidate sprint branch whose gate names the current head and whose verdict either names it directly or has the verified structured carry of guarantee 1.

   File: `docs/spec/v1.3/50-sprint-delivery.md`.

   Old: No later note restates a recorded verdict as a different word; a moved tip is one note naming the new sha, and the gate and read run again under guarantee 1.

   New: No later note restates a recorded verdict as a different word; a moved tip is one note naming the new sha, its gate runs again, and its read runs again unless guarantee 1 permits a verified structured carry.

   File: `docs/spec/v1.3/50-sprint-delivery.md`.

   Old: Recovered by re-gating and re-reading the new head; the old verdict stays recorded against its own sha and merges nothing.

   New: Recovered by gating the new full tree and either re-reading it or recording the clean base-merge carry allowed by guarantee 1; the old verdict remains bound to its original sha.

   File: `docs/spec/v1.3/60-state-machines.md`.

   Old: Head moves; gate and verdict are invalidated and run again (50 guarantee 1).

   New: Head moves; the gate always runs again on the new full tree, and the verdict is invalidated unless the clean base-merge carry of layer 50 guarantee 1 is verified and recorded.

   File: `docs/spec/v1.3/60-state-machines.md`.

   Old: Clean base merge with the card's own diff digest unchanged: a recorded MERGE carries (50, open question 1 default; Inputs of 50).

   New: Clean base merge with the card's own files and diff digest unchanged: record the structured source-review carry of layer 50 guarantee 1, but require a new bench gate on the exact resulting head.

   File: `docs/spec/v1.3/60-state-machines.md`.

   Old: Nothing merges on a sha that was not itself gated and read (50 guarantee 1).

   New: Nothing merges unless the exact final head is gated and its source review is either a direct cold read or the verified structured carry of layer 50 guarantee 1.

Validation: complete specification read, pinned source quotations checked, and report diff checked. No product code changes, builds, live sprint calls, model runs or release claims.
