"RESULT tools22-pre-238-r1 sha=5298f6be12ea — a pre-read of mas-bandwidth/nova-tools#238 at head a32b149cbe03: Specify nova-redis as local scratch and cache with durable backing
PREREAD 238 claims=7 proven=0 unproven=7 defects=1 high=0

PR 238
HEAD a32b149cbe034d39280c2e1213bfd84eaa6d8dd9
BASE dev
MERGE-BASE f817f65498de708c070dd77190d1683634ca3f6e
BEHIND 725
FILES 1 production, 0 test
LINES +323 -0

CLAIMS
1. A durable backing source is required before caching meaningful content — including unfinished ideas and intermediate results — and Redis never holds the only copy of such content (docs/SPEC-REDIS.md:21-23, 81-91). UNPROVEN — the tool does not exist; the document itself says "nothing here is implemented" (line 3), so no test anywhere can witness this.
2. A content-bearing write is acknowledged only after the owning tool validates a bounded source receipt (artifact identity, immutable revision or digest, declared durability boundary); unsaved stdin or an in-memory result is refused with `source-required` (docs/SPEC-REDIS.md:159-168). UNPROVEN.
3. nova-redis is disposable local scratch and cache only — never a new artifact store and never the one true form of accepted work — so dropping all Redis data leaves S, bus history and raw token accounting intact (docs/SPEC-REDIS.md:12-13, 279-280). UNPROVEN.
4. v1 accepts only an explicitly configured local instance (absolute Unix socket path or literal loopback IP/port); it refuses hostnames, redirects, cluster discovery and remote endpoints, and no install, launch-agent edit, port opening, ACL change or software update is a side effect of a read or a check (docs/SPEC-REDIS.md:38-41, 110-116). UNPROVEN.
5. Only reviewed fixed operations or fixed packaged scripts are allowed; there is no raw `EVAL`, arbitrary command passthrough, `KEYS`, `FLUSHDB` or `FLUSHALL` (docs/SPEC-REDIS.md:147-150). UNPROVEN.
6. Presence expiry means only stale observation; it never proves a worker stopped, frees a slot, refunds spend or transfers a lease, and `SET NX` plus an expiry is explicitly not a fencing design (docs/SPEC-REDIS.md:94-97). UNPROVEN.
7. The reference local instance holds rebuildable cache data with persistence disabled; Redis persistence is optional acceleration for recovery, never the protection against losing thoughts or work (docs/SPEC-REDIS.md:247-249). UNPROVEN.

DEFECT low docs/SPEC-REDIS.md:52-56 vs 264-291 — signals and presence are declared optional later adapters outside the first build slice, yet the "before implementation is called ready" acceptance gates and the first pilot both demand them (the gates test wait-cancellation, duplicate hints, trimmed cursors and expired presence; pilot 2 is "an immutable work-view reader plus a local change wait", and the spec's only change-wait mechanism is `signal wait`) — an implementer scoping the scratch/cache slice could be read as required to deliver signals and presence, and the pilot's wait mechanism is left unspecified — scope the gates and the pilot's wait to the first slice, or explicitly pull signals/presence into first scope.

QUESTIONS
1. The receipt rule (lines 159-168) refuses every content-bearing write without a backing source, including genuinely transient scratch; is that the intended reading, or should truly ephemeral, bounded, expiring values be allowed to spill without a receipt?
2. For pilot 2 (line 290) the change wait has no defined mechanism now that signals are deferred past the first slice — what should the first pilot's wait be?
3. Line 250-251 says restored values on a persistence-enabled provisioned server are "untrusted caches until freshness is reconciled", but reconciliation is only described for signal cursors; how is a restored `spill`/`recall` cache revalidated against its source?
4. Line 68 pins nova-work's "current proposal" to PR #231 at d8fc7d7; given the branch is 725 commits behind dev, is that reference meant to be updated as #231 evolves, or is it historical?

Left owed — The whole diff is one new file (docs/SPEC-REDIS.md, 323 lines) and I read all of it in full; there are no test files. I did not read the referenced docs (SPEC.md#conventions beyond confirming the anchor exists at SPEC.md:78, SPEC-BUS.md, SPEC-WORK.md, SPEC-TOKENS.md) or PR #231's content, since the diff is documentation-only and those are context, not changed paths. No go build/vet was needed: the change adds no Go code.

git status --short:
(empty — clean tree)

git rev-parse HEAD:
d576bf6bbabb39068096a97b4560de9b5e245970===FILE=== card-tools22-pre-238-r1/usage.tsv
job	attempt	started	ended	rc	provider	model	tokens_in	tokens_out	cache_write	cache_read	reasoning	usd
card-tools22-pre-238-r1	1	2026-09-20T19:28:35Z	2026-09-20T19:36:25Z	0	opencode	deepseek-v4-flash	43571	17682	0	645120	0	0.0291
