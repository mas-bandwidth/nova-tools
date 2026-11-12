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
| Idea, the owner's decision: the tailnet admins' Tailscale SSH into tag:nova and tag:runner as root takes a check instead of accept; the policy merge only adds rules, so it also needs the merge to replace the old accept rule | ❌ |
| nova-cairn open: two first opens of one session at the same time settle on one open record; CairnStore.tla's Open becomes a header step and a log step first, then the code follows | ❌ |
| nova-reserve: several runs in one terminal process group (make -j, a script, a PTY harness) share the terminal and job-control stops; JobControl.tla is extended to more than one run first | ❌ |
| nova-sandbox on macOS: a --read or --write reached through a chain of symlinks or a symlinked ancestor is granted as a one-hop link is | ❌ |
| Idea, the owner's decision: nova-bus hook --service carries the caller's PATH into the unit for --exec, as it does for --codex-thread | ❌ |
| nova-config machine records carry a per-machine firewall allow-in list that the tailscale role reads, so a machine's extra ufw rule (space2/space3: in on eno2 proto udp from 10.77.0.0/24) no longer depends on passing -e nova_firewall_allow_in on each up or tailscale run | ❌ |
| nova-bus hook --service writes a nova-config service record (seat, keys NOVA_BUS_PASSWORD, env) instead of a launchd or systemd unit holding the bus password in plain text; the services play then owns the unit | ❌ |
| nova-bus hook backs off while its wake target is absent (no Monitor on the wake file, no harness server) instead of exiting 1 and being restarted every 10 s by its supervisor (16,953 restarts in one log, 2026-11-12) | ❌ |
| complete | 0% |

[Source data](roadmap.sexp)

<!-- nova-work:future:end -->
