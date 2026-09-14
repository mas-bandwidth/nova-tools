# Captured-revision state export — proposed contract

*Proposal against SPEC-WORK draft 28 at 7db3b95. It closes the missing session export --at revision surface (2358–60) and full-round-trip/old-history requirements (3344–47). It preserves request-bundle export unchanged. It does not define merge, import, ownership transfer, transport, or savepoint replacement.*

## Separate products and commands

Current nova-work session export --session <path> --into <path> exports accepted requests since session base for later session replay. Its request format, base, expectations, live validation, and fenced/offline journal path remain exactly as lines 398–417 specify. Offline request export reads only the locked journal under caller bounds; starts no session, reads no repository, validates no request, and takes base and expectations from the newest clip-boundary record using its commit SHA, never its base SHA. A journal-only bundle is never state. Adding --at must not change any of that.

Add the explicit state form:

    nova-work session export (--session <path> | --snapshot <path> --cache <path>)
      --state --at <revision>
      --closed-history <none|all|range> [--from <UTC-stamp> --to <UTC-stamp>]
      --into <new-directory> --max-bytes <n> --max-depth <n> --max-nodes <n>
      --max-output-bytes <n>

--state is mandatory with --at; request export refuses --at. 'range' requires both --from and --to; 'none' and 'all' refuse them. The range is half-open [from,to), using the existing query stamp grammar, so adjacent ranges do not overlap. Resident state export is the existing bounded long-operation form: capture and pin the revision under the owning engine, then stage archive traversal and output I/O outside its mutation loop. It does not reacquire OWNER or write canonical events/indexes. The fenced-session export exception still applies; recording an export operation does not confer mutation authority. A fenced session may export any still-recoverable local revision, including accepted unshared work, without mutation or repository read.

--at is one accepted local revision. Under the single writer, the session resolves it to its savepoint/snapshot plus exact journal prefix and pins immutable source members until output succeeds or fails. A revision outside recoverable retention refuses naming the revision and member; it is never approximated by a later snapshot. For the isolated `--snapshot` source, --at must equal that snapshot's captured
revision; it uses the supplied verified archive closure and cache, never a live
session or repository. This is the explicit offline counterpart to the resident
form, not a change to ordinary --session query performance. Open state at capture
is always primary content. Closed-history none has no primary C records; all selects every reachable retained C partition; range selects primary C records in its declared interval. The manifest declares omitted primary history, so no scoped result claims full backup.

The resident form uses the existing framed JSON request/reply envelopes, negotiation,
request correlation and line routing. For example (ordinary common author, expectation,
clock and deadline fields omitted here only for readability):

    {"op":"session.export","request":"q-1","args":{"state":true,"at":"42",
     "closed_history":{"kind":"range","from":"2026-09-14T00:00:00Z","to":"2026-09-15T00:00:00Z"},
     "into":"out","max_bytes":"1048576","max_depth":"32","max_nodes":"50000","max_output_bytes":"67108864"}}

The resident request promptly acknowledges `OPERATION OK id=<id>
op=session.export state=<queued|running>` and returns. The request ID is the
ordinary response correlation ID; the operation ID is distinct. Completion is
retrieved through the existing `operation status|list|wait|cancel` protocol, not
a second synchronous response or a new polling API. `operation wait` emits the
terminal `EXPORT OK state=true operation=<id> at=42 manifest=<sha256>
members=17 bytes=8123 complete=<true|false>` or the corresponding `EXPORT FAIL`
with operation and captured revision. Lines travel in the ordinary framed reply
with request/exit/rev/pushed and decimal-string JSON integers; no member bytes
travel on the wire. Queue, stage and result retention use the parent bounds.

Pinning covers the exact source members throughout traversal and publication; a
concurrent clip/retention pass cannot reclaim them until terminal disposition and
release of the pin. Cancellation acknowledges the request, then reconciles whether
output publication happened; it never promises to undo a published directory.
Disconnect does not cancel the operation. Recovery resolves its existing ID,
captured sources and output identity before any retry, never blindly republishes.
Atomic mutation batches refuse export as an external-effect long operation.

The explicit offline snapshot form has no resident mutation loop or live session.
It is a finite isolated process that waits for its own staged export and prints
the terminal grammar with `operation=-`; it does not register a resident operation
or expose a second live-session export path. That offline grammar distinction is
an explicit integration decision for review. Its bounds and publication barriers
remain identical, and a caller cannot use it to bypass live-session capture.

## Durable artifact and closure

The immutable output directory publishes MANIFEST.sexp, in the existing restricted canonical S-expression format; JSON is wire only. Its exact v1 form is:

    (:version "nova-work-state-export-v1"
     :kind :captured-state
     :captured (:revision 42
                :snapshot (:sha256 "<64-lower-hex>" :revision 40
                           :journal "<journal-id>"
                           :replay-cut (:sequence 25 :sha256 "<record-hash>"))
                :journal-end (:sequence 27 :sha256 "<record-hash>")
                :verification-cache "<64-lower-hex>")
     :schema (:id "work-v1" :sha256 "<64-lower-hex>")
     :scope (:open :all :closed-history (:range :from "2026-09-14T00:00:00Z" :to "2026-09-15T00:00:00Z"))
     :members ((:path "state/root.sexp" :kind :state-root :bytes 8123 :sha256 "<64-lower-hex>"))
     :omissions ((:kind :primary-closed-history :identity "2026-09-13" :reason :outside-selected-range)))

Integers use canonical unsigned base-10 atoms (0 or nonzero digits only), never sign, leading zero, float, exponent, or JSON coercion. Stamps are exact RFC 3339 UTC strings ending Z and satisfy parse-and-roundtrip validation. Optional values use the unambiguous restricted S-expression sentinel (:absent), never prose. Members sort bytewise by path. The manifest does not list itself; an artifact identity hashes its exact canonical MANIFEST.sexp bytes separately.

The snapshot reference binds the exact base image and its revision B, logical
journal ID, and complete-record replay cut using WORK-RECOVERY-BOUNDARY's proposed
identity scheme. Its absent initial cut is legal only under that scheme's initial
header/image rule. B must not exceed the requested revision R. `:journal-end
(:absent)` is legal only when B equals R and the image itself is the exact capture;
no journal tail is then applied. When B is less than R, journal-end is mandatory
and identifies one complete envelope by sequence and hash in the same logical
journal, with resulting local revision exactly R. R cannot split an envelope.
The selected immutable members must cover the base image and every complete
record strictly after the base cut through that end, including intervening clip
markers, with an unbroken sequence/hash chain. Physical rotation cannot alter
logical identity. Load verifies this coverage and the before/after revision chain
before exposing state; it applies only that interval, never a later available
tail. A missing cut, gap, wrong end revision/hash, split envelope, or absent end
with B < R refuses. The final shared physical reference codec remains a lock gate.

Paths are clean relative slash paths: no absolute path, dot segment, NUL, duplicate, case-fold alias, symlink, special file, or escape. Each member byte count and lower-case SHA-256 is exact. The primary selection above is distinct from required closure. Closure always adds every canonical internal dependency of selected open or closed records: structure/scope/lease events, indexes/counter provenance, roadmaps, evidence references, CONFIG/ACTIVE observations, models/rates/accounting records, schema bytes, and any canonical record required to resolve an internal id. Missing, corrupt, or redacted mandatory internal closure is an export refusal; it cannot yield a queryable valid-empty load. Omission entries describe only intentionally unselected primary history, rebuildable derived caches, or declared external provenance references. A scoped artifact is incomplete only with respect to declared primary history/external facts, never internally dangling.

Full state export includes private canonical work; renderer audience filtering does not apply. It preserves stored non-secret CONFIG, profile, resolver-command, and cache-provenance text as inert canonical data, including literal empty/absent distinctions. Export and load never read any path named by that text, environment, credential store, resolver, or executable profile, and never execute it. Credential values and secret-store content are excluded. If a canonical record cannot be represented losslessly without redaction, export refuses rather than calling the artifact full. Canonical bytes preserve order where meaningful, Unicode code points, integers, multiline text, links, and (:absent), (), and empty string. Exact member-path taxonomy and record codecs remain review decisions, not permission to serialize in-memory objects differently.

## Bounds and durable publication

Reads stream under --max-bytes. The restricted lexer/reader checks depth and node
bounds before descending or allocating the next form, not after an unbounded parse. --max-output-bytes is an exact running sum of member plus manifest bytes. A hash pass rereads pinned immutable bytes or retains a verified immutable handle before copy. Changed identity, malformed data, cycle, any bound breach, or nonregular input refuses; no truncation marker represents a complete member.

The writer uses an owned private sibling staging directory, creates members exclusively, hashes/counts during copy, syncs members, writes and syncs MANIFEST.sexp last, and syncs staging directory metadata. It then uses a no-replace directory commit into --into and syncs the destination parent directory. Success is reported only after that final publication barrier. An existing destination refuses; unsupported filesystems refuse rather than pretending check-then-rename is CAS. A crash before final commit can leave owned staging with either no manifest or a fully valid synced manifest: it has valid bytes but is un-published. Recovery may retain it for inspection or clean it only by owned nonce; it never auto-publishes it.

## Isolated read-only load

Add:

    nova-work state load --from <export-directory> --into <new-readonly-session>
      --max-bytes <n> --max-depth <n> --max-nodes <n>

`state load` is an offline finite subprocess, not a request to a running daemon.
It prints ordinary `STATE OK`/`STATE FAIL` lines; success names the captured revision,
manifest hash, and materialized snapshot/cache paths. It verifies manifest version,
every declared byte count/digest, member set, closure and schema under the stated
bounds, materializes an exclusively created isolated directory, then exits. It
starts no daemon and retains no socket. The materialized snapshot/cache and indexed
archive closure use the existing snapshot/cache schemas; their exact portable file
layout is part of the remaining codec lock. No writable live journal or OWNER is
created. Incomplete output remains staging, never a successful load.

The existing bounded `query --snapshot <path> --cache <path>` form reads the loaded
snapshot. The new export's `--snapshot` source re-exports it through a fresh isolated
reader, rebuilding the canonical model rather than merely copying an unchecked
archive. Neither operation starts a coordinator. Mutation/replay/clip/handoff and
execution commands cannot use the snapshot as a live --session endpoint. Both
refuse resolver/provider/network/repository access and execute no stored profile.
Existing live --session calls still reload nothing; only explicit offline snapshot
reads pay parsing costs. Existing savepoint meanings remain unchanged.

Verification facts need special care: qualification caches and derived counters
may rebuild, but raw resolver observations, their source identities and captured
verification-cache identity must be included where they are the only proof data.
Omitting that sole copy cannot be excused by calling it a derived cache. Historical
export must use the observations associated with its captured revision. If that
association or its retained bytes are unavailable, export refuses with an explicit
missing-capture/proof gap; it must not substitute current observations. A captured
cache that already recorded unknown evidence is valid; losing a formerly recorded
fact and inventing unknown is not. The
revision/cache binding belongs in the exact capture codec before lock.

## Required witnesses and remaining decisions

1. Capture R while R+1 is accepted; export bytes describe exactly R. Exercise exact-snapshot B=R with absent end and B<R with a multi-record prefix across rotation. Reject absent end for B<R, missing/interchanged records, wrong end hash/revision, and a cut inside a multi-event envelope; a present later tail is never replayed.
2. Export old C outside resident window with all; fresh load reproduces proof. Range/none use [from,to) and declare primary omissions while retaining required closure.
3. Fresh isolated load re-exports same capture and compares fields, ids, Unicode, literal/empty/absent values, roles, CONFIG, ACTIVE, O/C, roadmaps, and accounting provenance.
4. Missing mandatory member, changed digest, dangling internal reference, path escape, symlink, output overrun, or corrupt S-expression refuses without a valid load.
5. Existing output, concurrent publisher, and output failure never overwrite a prior artifact; staged manifest before final commit is not published.
6. Instrumented export/load proves no ownership change, worker dispatch, replay, merge, resolver execution, network or repository mutation. Only the declared exclusively created output/staging paths are written.
7. `state-export-is-one-long-operation`: an export blocked on archive I/O still promptly acknowledges its operation; status/cancel and unrelated mutation remain responsive. Wait returns the same operation/captured revision after publication or refusal.
8. `state-export-pin-survives-clip`: capture R, run clip/retention at R+1 during copy, then complete exactly R or name a recovery gap; no pinned member reclaimed, no current bytes substituted.
9. `state-export-disconnect-and-cancel`: lost client, restart, and cancellation around no-replace publication retain the same operation/output identity; no duplicate directory publication or claim to reverse it.

Review must still pin path taxonomy/record codecs, no-replace platform support/recovery, and signing/audience policy for distribution. None permits reinterpreting request export or adding a live-state import.
