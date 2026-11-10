# nova-tools roadmap

Ideas and future work for nova-tools. Nothing here is scheduled; the owner decides when scope widens. Entries are data; the tables are rendered by nova-roadmap.

<!-- nova-work:future:start -->

| feature | done |
|---|:---:|
| nova-reserve slots bound CPU, so N one-slot jobs cannot saturate a bench | ❌ |
| Place one heavy job on the quietest bench fleet-wide | ❌ |
| race-detector-tiers — PR #6301, branch quality/race: unit and functional tiers under -race | ❌ |
| bus-latency-benchmarks — PR #6302, branch perf/bus-benchmarks: functional benchmarks for nova-bus send-to-hook latency and hook idle cost | ❌ |
| nova-fleet up applies the netwatch play as part of fleet setup | ❌ |
| Remove the dead nova_seal_store guards in roles/secrets-refresh (dead since #6279) | ❌ |
| nova-sandbox refuses a --write grant covering its own binary or anything on PATH that chooses it | ❌ |
| nova-sandbox probe prints bin= like check and show | ❌ |
| A VM mock fleet so nova-fleet up can be run cold end to end | ❌ |
| Verified commits: one GitHub account per friend and one for the fleet, each backed by a real email, so every commit shows Verified as the identity that made it | ❌ |
| MicroVM sandboxing: nova-sandbox runs unattended work in a microVM with an egress allowlist, so a card branch carrying a malicious test cannot reach the host, its keys or the tailnet | ❌ |
| Bug: the github-runner role's online check gives up after 60 s, but GitHub takes 2-4 min to release a restarted runner's session, so a good apply reports runners offline | ❌ |
| complete | 0% |

[Source data](roadmap.sexp)

<!-- nova-work:future:end -->
