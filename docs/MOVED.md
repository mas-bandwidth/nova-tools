# Moved out of nova-tools

nova-tools keeps six pieces: bus, config, sandbox, secrets, slot and fleet (README.md). Everything below
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

Nothing is left on main because a kept tool imports it: the kept tools import nothing else in this repository.

## To rowan-tools

- cmd/nova-decide, pkg/decidewire, pkg/decide
- cmd/nova-fuse, internal/fuse
- cmd/nova-check, internal/check
- release cutting: pkg/releasewire, tools/ghrelease, .github/workflows/release.yml and pkg/release

## Retired

- cmd/nova-redis, pkg/redisacl, pkg/redisfn
- cmd/nova-bus, pkg/bus/bustest, pkg/buswire/cmd/bus-driver: replaced by bus/
- cmd/nova-ci, internal/ci, internal/cireceipt, internal/scaffold, internal/shippedsmoke, internal/docs, internal/ghevent, pkg/ci, pkg/tlc, pkg/secretcheck, pkg/roadmap, pkg/sexp: the repository's own CI machinery
- tools/: agentsmap, analyzers, benchstandard, ci, clidoc, fardelay, functionalrun, newrule, newverb, notes20261002, preflight, roadmap, sandboxcheck, sessiontrace, testmanifest, tlacheck
- Makefile, AGENTS.md maps, the certification, nightly-slow, revert-on-red and tlc workflows: replaced by one plain ci.yml (TESTING.md)
- pkg/pkgselect: CI's package selection
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

## nova-secrets

nova-secrets is now `secrets/`: a thin wrapper over sops and age on the same store (`.sops.yaml`, `recovery.pub`,
one `<seat>.yaml` per seat). Kept: `exec`, `names`, `check`, `seal`, `seat add`, `keygen`, `version`.

| Was | Now |
|---|---|
| `gate`, the clear `NOVA_SECRETS_WRITTEN_BY` mark | dropped; a seal drops an old mark |
| `place`, `placed` | dropped |
| `seat inject` | dropped; `seal` sets one value, `seat add` starts a seat |
| `seal`'s branch, push, pull request and merge | dropped; `seal` commits on the current branch, pushing is the caller's |
| `seal`'s hidden terminal prompt | dropped; the value is piped on stdin |
| the store's upstream, clean-tree and invariant checks; the sops and age version probes | dropped |
| `names --json`, `--max`'s MORE line; `--dry-run` | dropped; `--max` and `--no-pr` are accepted and ignored |
| `cmd/nova-secrets`, `docs/SPEC-SECRETS.md` | deleted |
| `pkg/secrets`, `pkg/seatcred`, `pkg/winacl`, `pkg/onboarding`, `pkg/testkit` first-run helper | deleted (2026-11-05, below) |

## nova-cairn (now in rowan-tools)

| Was | Now |
|---|---|
| `nova-cairn`, as it was | rowan-tools `cmd/cairn/`, as `nova-cairn open`, `append`, `index`, `receipt` |
| `nova-self-talk` | rowan-tools `cmd/cairn/`, as `nova-cairn self-talk` |
| `nova-memory` | rowan-tools `cmd/cairn/`, as `nova-cairn memory stats`, `search`, `check`, `verify`; dropped: `quickstart`, `eval`, `boot`, `version`, the `trigram` channel, `--json`, `--whole` |

## nova-config

| was | now |
|---|---|
| `cmd/nova-config`, `pkg/config`, `pkg/configwire`, `schemas/config`, `docs/SPEC-CONFIG.md` | `config/`: machine, loop and fleet rows and the inventory, same tables; the friend, sprint, provider, route and tier kinds, `apply`, `migrate`, `status`, `login`, `loop run` and `machine width/self` are dropped (v1.6.29 carries them) |

## Replaced

- nova-update: replaced by the Ansible role fleet/roles/nova-tools-install (play fleet/nova-tools-install.yml)

## Removed 2026-11-05 (the pare to six pieces)

nova-tools is now bus/, config/, sandbox/, secrets/, slot/ and fleet/. Nothing outside those is built from this repository.

| Removed | Went to |
|---|---|
| `cairn/`, `docs/SPEC-CAIRN.md`, cairn's `tla/` | rowan-tools: `cmd/cairn/`, `docs/SPEC-CAIRN.md` |
| `cmd/nova-version`, `internal/update`, `docs/SPEC-UPDATE.md`, `docs/SPEC-RELEASE.md` | retired (install is fleet/nova-tools-install.yml) |
| `internal/nsprint`, `pkg/nsprint`, `pkg/swarm`, `pkg/member`, `pkg/harness`, `pkg/cardcontract`, `pkg/cardcost`, `pkg/cardhdr`, `pkg/cardtree`, `pkg/dogfood`, `pkg/provbalance`, `pkg/sprintwire`, `docs/SPEC-SWARM.md`, `docs/SPEC-CARD-CONTRACT.md`, `docs/SPEC-ISA.md` | nova-sprint (code at tag v1.6.29) |
| `docs/SPEC-NOVA-DECIDE.md` | rowan-tools (nova-decide) |
| `pkg/bus`, `pkg/buswire`, `pkg/config`, `pkg/configwire`, `pkg/redisfn`, `pkg/secrets`, `pkg/seatcred`, `pkg/winacl`, `pkg/onboarding`, `pkg/testkit`, `pkg/filelock` and every other `pkg/` package, `internal/testverbhelp` | retired; `bus/`, `config/` and `secrets/` replaced them |
| `tla/` models (Bus2, Bus2Receipts, BusSendOnce, BenchStage, CardContract, FileLock, FinishRefused, FirstConn, LoopRedial, ProviderRetry, RedisFn, StopCancels, TypedFrames, UpdateApply) and their `CASES.tsv` and README; `tla/check.sh` stays and now runs only directories that hold a module | retired with the code they modelled; `bus/tla` and `slot/tla` remain |
| `docs/CLI.md`, `docs/SPEC-LOGS.md`, `docs/TESTS.md`, `docs/ASSET-PROVENANCE.md`, `schemas/`, `infra/`, `assets/`, `.github/ISSUE_TEMPLATE/`, `FIXES.md` | retired |
