# nova-tools roadmap

Ideas and future work for nova-tools. Nothing here is scheduled; the owner decides when scope widens. Entries are data; the tables are rendered by nova-roadmap.

<!-- nova-work:future:start -->

| feature | done |
|---|:---:|
| nova-reserve slots bound CPU, so N one-slot jobs cannot saturate a bench | ❌ |
| Place one heavy job on the quietest bench fleet-wide | ❌ |
| ci-race-detector — PR #6301, branch quality/race: unit and functional tiers under -race | ❌ |
| bus-latency-benchmarks — PR #6302, branch perf/bus-benchmarks: functional benchmarks for nova-bus send-to-hook latency and hook idle cost | ❌ |
| docs-commands-test — PR #6304, branch quality/readme-test: contract test running every documented command and refusal against the binaries | ❌ |
| nova-fleet up applies the netwatch play as part of fleet setup | ❌ |
| Remove the dead nova_seal_store guards in roles/secrets-refresh (dead since #6279) | ❌ |
| nova-sandbox refuses a --write grant covering its own binary or anything on PATH that chooses it | ❌ |
| nova-sandbox probe prints bin= like check and show | ❌ |
| A VM mock fleet so nova-fleet up can be run cold end to end | ❌ |
| complete | 0% |

[Source data](roadmap.sexp)

<!-- nova-work:future:end -->
