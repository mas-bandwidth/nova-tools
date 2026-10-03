# nova-cairn USE rating, nova-tools 1.1.0

Rater: OpenAI gpt-5.6-sol
Build: 711c26d8fc10
Score: 6.5/10

## Reasons

The help-led first run is fast, local, and unusually legible. Open, append, index, and receipt worked immediately; exact file bytes, inherited source pointers, duplicate detection, conflict exit 1, JSON parity, and an executable missing-session remedy all behaved well. The decisive loss is that the advertised same-ID conflict guarantee fails under two ordinary concurrent invocations: both returned OK and one silently replaced the other's durable entry. Re-opening also prints the new caller stamp and policy although it changes nothing, so a successful line can describe state that was never stored. Index reports how many sessions exist but lists entries only, so it cannot identify an empty session among several records. A 10 would make the first writer win atomically, return a conflict to the loser, report stored metadata on an idempotent open, and include session rows in the index.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-cairn append --store ./rerun-race-cairns --session race --entry same --text alpha --publish never --now 2026-01-01T00:01:00Z & nova-cairn append --store ./rerun-race-cairns --session race --entry same --text beta --publish never --now 2026-01-01T00:02:00Z & wait` | From the job scratch directory, both different same-ID appends returned OK with duplicate=false; receipt retained only beta, so alpha was silently overwritten instead of receiving the promised conflict. | Claim the entry path exclusively or serialize same-entry writes, then compare the losing request with the winning bytes and return duplicate or conflict. | M |
| 2 | `nova-cairn open --store ./rerun-cairns --session s1 --publish never --now 2030-01-01T00:00:00Z` | From the job scratch directory, re-opening the existing manual-policy session returned OK with publish=never and the supplied 2030 stamp even though open is a no-op and those values were not stored. | Read and return the existing open metadata and add an already-open fact so the result describes the operation performed. | M |
| 3 | `nova-cairn index --store ./rerun-cairns` | After opening an empty session beside s1, index reported sessions=2 but printed only s1's entry; it gave no session row or other way to identify which record was empty. | Emit bounded session rows, including zero-entry sessions, or add a session-listing verb whose output keeps the total. | S |

## Good, keep

Keep the four-command local example and the typed status-first lines. Keep the separate persisted and published facts, executable missing-session remedy, byte-preserving file input, source inheritance, and matching JSON representation. Keep conflict at exit 1 and usage or missing state at exit 2.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| 2026-10-01 USE 8 to 9 | CHANGED | The help-led happy path, exact retrieval, JSON and remedies remain strong, but the concurrent same-ID probe now demonstrates a silent overwrite severe enough to lower the score. |
| 2026-10-02 USE 9: index cannot list sessions | STILL THERE | `nova-cairn index --store ./rerun-cairns` prints sessions=2 and only s1's entry, so the empty session cannot be identified. |
| 2026-10-02 USE 9: a re-open looks like a first open | STILL THERE | The re-open command returned the caller's new 2030 stamp and never policy with no already-open or duplicate marker. |
| 2026-10-02 USE 10: exact retrieval, duplicates, conflict remedy and JSON worked | CHANGED | Sequential probes still passed all four behaviors, but two concurrent different writes both returned OK and the later bytes replaced the earlier entry. |
