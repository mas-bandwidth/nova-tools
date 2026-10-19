The stable tools point release after v1.6.6, carrying one fix to the provider path: a provider's transient refusal (a 429 rate or concurrency limit, a 503, a 529, overloaded) is now sent again by the read-deadline proxy instead of ending the card, and direct providers go through that proxy too, so Ultramarine reads stop ending with no verdict on "The request exceeds your organization's requests per minute. Retry in 1 s." nova-sprint stays at v1.6.5.

Install: download the assets for your platform from this release, then run `nova-update release install --from <release root> --version v1.6.7 --bin <dir>` with the nova-sprint v1.6.5 binaries staged beside them (see docs/SPEC-RELEASE.md).

## What changed in v1.6.7

- The provider proxy keeps each request's body and sends it again on a transient refusal, after the provider's own wait (Retry-After, retry-after-ms, or "retry in N s" in the body) or a doubling backoff from 1 s capped at 60 s, whichever is longer, plus up to a quarter at random. After 8 sends the last refusal is handed to the harness whole. Out of funds is never retried. (#6017)
- A direct (uncatalogued) provider's custom block is now proxied like any other provider, so its refusals are retried and a limited direct route (`nova-sprint routes limit`) takes its budget on every request. (#6017)
- A limited route keeps its stall for limit refusals, with the provider's hint now a floor under its backoff. Its other transient refusals take the bounded retry. (#6017)
- The harness's own header and request clocks are off for every proxied provider; the proxy's header wait and body silence still bound each send, and a backoff counts as a stall for the idle watch. (#6017)
- `tla/ProviderRetry.tla` models the bounded retry, with reversed witnesses for the old hand-it-on-first behaviour and for an unbounded retry. (#6017)
