# Moved out of nova-tools

nova-tools keeps only small, self-contained tools (README.md). Everything below
left main; its code is at tag v1.6.29.

## To nova-sprint

- cmd/nova-swarm: the one-task worker
- cmd/nova-friend, pkg/friend, pkg/friendfilewire, internal/friend
- cmd/nova-hook, hook/, pkg/hookwire, pkg/hookgate, pkg/pongwire
- cmd/nova-table, pkg/ntable, internal/tablemodel, pkg/tablemodelwire, pkg/tty
- cmd/nova-tokens, internal/tokens
- cmd/nova-local
- cmd/nova-doctor, internal/doctor
- cmd/nova-up, internal/up, internal/converge
- pkg/codexdesktop, pkg/dshdesktop, pkg/swarmwire, pkg/harness/notice
- pkg/seatrecwire, pkg/ledgerwire, pkg/cardlimits, pkg/binstamp, pkg/gocache, pkg/units, internal/nogh, internal/yield
- pkg/bench: bench runs of the worker
- internal/record: the token ledger of nova-tokens
- infra/functional-image
- the models of the code above: Friend*, Hook*, Reach, Table*, MemberTable, EpochMemberTable, CodexDesktopWake, CodexHookRecovery, DshNativeWake, GrokHookNotice, HarnessNoticeUnknown, BenchRun, WakeCursor

Still on main for now, because a kept tool imports them; each goes when that
tool's rewrite drops it: pkg/swarm, pkg/member, pkg/harness, pkg/cardcontract,
pkg/cardcost, pkg/cardhdr, pkg/cardtree, pkg/dogfood, pkg/provbalance,
pkg/sprintwire, pkg/nsprint, internal/nsprint, pkg/onboarding, pkg/bus,
pkg/buswire. cmd/nova-version stays with internal/update, which implements it.

## To rowan-tools

- cmd/nova-decide, pkg/decidewire (pkg/decide still imported by pkg/config)
- cmd/nova-fuse, internal/fuse
- cmd/nova-check, internal/check
- release cutting: pkg/releasewire, tools/ghrelease, .github/workflows/release.yml (pkg/release still imported by internal/update)

## Retired

- cmd/nova-redis, pkg/redisacl (pkg/redisfn still imported by pkg/config)
- cmd/nova-bus, pkg/bus/bustest, pkg/buswire/cmd/bus-driver: replaced by bus/
- cmd/nova-ci, internal/ci, internal/cireceipt, internal/scaffold, internal/shippedsmoke, internal/docs, internal/ghevent, pkg/ci, pkg/tlc, pkg/secretcheck, pkg/roadmap, pkg/sexp: the repository's own CI machinery
- tools/: agentsmap, analyzers, benchstandard, ci, clidoc, fardelay, functionalrun, newrule, newverb, notes20261002, preflight, roadmap, sandboxcheck, sessiontrace, testmanifest, tlacheck
- Makefile, AGENTS.md maps, the certification, nightly-slow, revert-on-red and tlc workflows: replaced by one plain ci.yml (TESTING.md)
- pkg/pkgselect: CI's package selection (still imported through pkg/cardcontract)
- the models whose code is gone: FuseBox, and the TLC run records (RUNS.tsv, RUNS-HISTORY.tsv, COVERAGE.tsv)
- docs and ledgers that described only removed tools: the specs, release notes, ratings, research, audits, RESOLUTIONS.md, docs/roadmap.sexp and docs/fixes.sexp

## nova-sandbox

nova-sandbox is now `sandbox/`: the wall only (`--read`, `--read-noexec`, `--write`, `--cwd`, `--tmp`,
`--net-deny`, `--net-allow`, `--gpu`, then `-- <cmd>`), plus `check` and `probe`. macOS and Linux.

| Was | Now |
|---|---|
| `nova-sandbox run`, `reap` (disposable APFS volumes) | nova-sprint |
| `nova-sandbox worktree` | nova-sprint |
| `nova-sandbox egress plan/apply/check/drop` (nftables) | nova-sprint |
| `--name`, `--acl` (Windows container and ACLs) | nova-sprint; no Windows wall in nova-tools |
| `--max-procs`, `--max-mem` (tree caps, SANDBOX RUNAWAY) | nova-sprint |
| `--net-listen`, `--json`, `--secret`, `--max` | dropped; inbound is never granted, `probe` takes no flags |
| `policy` verb, lane profiles (`LaneProfile`), `--deny` | dropped (the lane profile was nova-friend's) |
| `pkg/sandbox`, `profiles/`, `tools/sandboxcheck`, `cmd/nova-sandbox` | deleted; the darwin profile template is `sandbox/darwin.sb.tmpl` |
