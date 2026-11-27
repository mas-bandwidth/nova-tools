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
| nova-fleet windows-runners retires an admin runner whose entry left windows_admin_runners: its GitHub registration (removal token), service, directory and marked local account | ❌ |
| nova-bus hook --service writes a nova-config service record (seat, keys NOVA_BUS_PASSWORD, env) instead of a launchd or systemd unit holding the bus password in plain text; the services play then owns the unit | ❌ |
| nova-bus hook backs off while its wake target is absent (no Monitor on the wake file, no harness server) instead of exiting 1 and being restarted every 10 s by its supervisor (16,953 restarts in one log, 2026-11-12) | ❌ |
| nova-reserve hands freed slots to waiters in arrival order, so a long waiter is not passed by later ones (captain 2026-11-12: a waiter since 17:13Z was passed by three later arrivals) | ❌ |
| nova-fleet tailscale: the Tailscale SSH switch's revert survives a reboot or a killed job inside its window (a boot-time check of a pending switch), so a power loss there cannot leave Tailscale SSH off without a proven key login | ❌ |
| nova-fleet: the Tailscale SSH switch's functional test writes its runs as traces that a SSHOffTrace.tla checks are behaviours of SSHOff.tla, binding the model to the code | ❌ |
| Approved by the owner 2026-11-13, in progress: rented machines as first-class fleet members, built in order: (1) the machine record gains provider, provider id, hourly price, purpose, rented-by and a return-by time, and one command lists rented boxes with their cost and overdue state; (2) nova-fleet rent and return verbs for Latitude and AWS: rent creates the box, checks its hardware against the ask (NIC model, cores) and deletes it on a mismatch, joins it and writes the record; return removes its runners, tailnet entry, record and the box; AWS temporary boxes stay off the tailnet with no seat or secrets; (3) an idle report of rented boxes with no load for N minutes (2026-11-13: four Latitude boxes rented with the wrong NIC, rented boxes billing idle after their drivers died, teardown programs living in a session scratch folder) | ❌ |
| Gap, needs the owner's go-ahead: a red scheduled (nightly) run notifies nobody and nothing shows the fleet's red scheduled runs in one place, so moo's nightly failed every day 2026-11-10 to 2026-11-21 at one commit (d6a57e8c) unseen until an unrelated PR hit the same error | ❌ |
| Unfinished, needs the owner's go-ahead: find the cause of every scheduled workflow that was red on 2026-11-21 | ❌ |
| Gap, needs the owner's go-ahead: nothing pins or records the image and compiler of GitHub-hosted jobs, so an ubuntu-latest move (24.04 to 26.04, GCC 15 rejecting a buffer under -Werror, moo#76) shows only as a red job; options, not chosen: pin the image version in workflows, or record the toolchain version per run and alert on a change | ❌ |
| Gap, needs the owner's go-ahead: no check finds per-commit jobs whose cold build exceeds the runner hook's 120 s limit, which overrides a job's timeout-minutes, so each fails only when GitHub evicts its cache (serialize.moo 2026-11-21, cache unused 7 days, worked around by a daily run in serialize.moo#35) | ❌ |
| Measurement for ci-cold-build-limit: under fleet load with bench work on the CI hosts, schema's lang-clean (elixir) was cancelled by the 120 s limit in 5 of 11 per-commit runs on 2026-11-21 (one after its last test printed OK); its green runs took 78-109 s | ❌ |
| Question for the owner: a runner service's environment exports MOO and MOO_ANALYZE (~/sdk/moo-v1.3.0), which a repository's Makefile takes with ?= over the moo its job just built, so serialize.moo's ARM64 job tested the runner's moo on 2026-11-21; should runner-wide variables exist for tools repositories build themselves | ❌ |
| nova-roadmap meets the command-line contract: one-line refusals ending in run: nova-roadmap <verb> -h, no Go flag-package text, an entry in toolSpecs and a faults_roadmap_test.go; this changes its stderr, never a rendered page | ❌ |
| The fleet's automatic-reboot window (hand-set 2026-11-05 in /etc/apt/apt.conf.d/52nova-reboot-window, one host per 15 minutes, not yet in any play) fires only when the host is idle: no nova-reserve holder and no runner job running, else it defers; when it lands as a play, remove the temporary hold files /etc/apt/apt.conf.d/53nova-no-auto-reboot-hold put on 2026-11-13 on amd-1, amd-2, amd-3, hulk, hetzner1, hetzner2, intel and space (hulk rebooted itself mid-work at 09:00 UTC) | ❌ |
| complete | 0% |

[Source data](roadmap.sexp)

<!-- nova-work:future:end -->
