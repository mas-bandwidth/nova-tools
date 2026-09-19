# Coordination scripts, for review (correctness and safety)

These are the shell scripts the coordinator bench has been running the fleet with. They were written fast, used by one person, and
under-tested; a pit stop on 2026-09-17 found real defects in them by simply running them as a user would, with hostile inputs
(ledger: the coordinator's home repo, reports/pitstop-tests-2026-09-17.md). They are here so other eyes can read ALL of the code.
They are not proposed for merge as they stand: each is slated to become a nova-tools verb (#1142), and review findings feed those verbs.

What to look for: command injection (anything interpolated into `ssh host "..."` or a heredoc), deletion (every `rm`, every path
built from a variable, symlinks, `..`, empty variables, HOME), secrets (a key in argv, a file, a log line or output), races and
fixed waits, silent failure (a refusal that exits 0, an error swallowed by `2>/dev/null`), loops without a deadline, anything that
kills processes, anything that behaves differently on macOS and Linux (BSD seq/sed/stat/date), and assumptions about the working directory.

| Script | What it does | Test |
|---|---|---|
| bench-hygiene.sh | the only deleter on a bench: verbs over validated names below two roots; per-bench log | tests/bench-hygiene-hostile.sh (40 cases, canary) |
| safe-rm.sh | `safe_rm` for the coordinator's own scripts: below two roots, never a symlink or a root | tests/safe-rm-hostile.sh (26 cases, macOS and Linux) |
| flash-native-bench.sh, muse-native-bench.sh | launch one card on a bench over ssh; capacity guard; key by nova-secrets exec | tests/launcher-refusals.sh (27 cases; a hostile label used to execute on the bench) |
| fill-loop2.sh | deal ready cards across benches within each bench's allowance | tests/fill-loop-deal.sh (15 cases, fake launcher) |
| harvest-bench.sh, harvest-loop.sh | fetch a card's branch from the bench, push with force-with-lease, open the PR | run for real about 40 times; refusal reasons added; no fake-based test yet |
| sweep-loop.sh, enqueue.sh | enqueue green PRs; skip list; hold flag | tests/sweep-skip-hold.sh (7 cases, fake gh; the skip list used to be SOURCED from /tmp) |
| rebase-loop.sh | one rebase card per DIRTY head | tests/rebase-marker.sh (6 cases) |
| status-page.sh | the fleet dashboard, counts only | numbers compared with live readings; no automated test |
| bench-mirror.sh | bare mirrors for card clones | offline clone proven by hand |
| bench-standard.sh | assert the bench standard ON a Linux bench; can kill stray runner listeners | guarded tonight (refuses off-bench and any argument); still has raw rm on probe dirs and a stale hard-coded version: NEEDS WORK |
| adopt.sh, nova-upgrade-loop.sh | rebuild on a new dev sha, adoption checks | adopt hung four times on a vanished clone (fixed); over ten minutes; the loop never installed tools on the benches |
| fleet-install-tools.sh | build once on one bench, cache by sha, install atomically everywhere, verify | refusal cases by hand; first real run 2026-09-17 |
| with-secrets.sh, with-jev.sh | run a child with sealed keys; nothing printed | checked by hand: keys present, no key-like output, missing store refuses |
| ts-join-one.sh | join a machine to the tailnet with a ten-minute key minted over the API, piped over stdin | used for real on two machines |
| local-gate.sh | the schema fast lane on the Studio: slots, gates, legs | tests/local-gate-slots.zsh (slot release leaked: fixed) |
| child-clone.sh | one scratch clone per child of the schema repo; --gc deletes it | --gc printed "removed" for a refused delete: fixed; hostile names by hand |
| nova-log.sh | fleet log reader, ten lines | run by hand; its --grep filter is dead (Stella F26): NEEDS WORK |
| fleet-wake.sh, fleet-sleep.sh | Mac benches: magic packet, dark-to-full wake, runners online as the test; sleep after 30 idle min | full cycle run three times on batman (wake 3 to 6 min) |
| macos/setup-fleet-mac.sh, macos/provision-mac-bench.sh | first touch and full provisioning of a Mac bench | run for real on two iMac Pros; a deletion guard written as a glob let `../..` through (now an anchored regex) |

## Second snapshot, 2026-09-18 00:20Z, after the reviews

Fixed since the first snapshot (Emma E1..E5, Stella F02/F03/F14/F22/F23 and the /tmp findings, Johnny's /tmp findings): control files moved from /tmp to ~/rowan-working/queue/control (enqueue, sweep, rebase); harvest-loop no longer executes from /tmp; fill-loop2 claims a card by the mv's exit status and returns a refused launch to ready; ts-join-one validates host and binary path; setup-fleet-mac appends the key instead of overwriting; every test exits non-zero on a failed assertion; safe_rm returns failure on refusal or a symlink; child-clone --gc dies with the reason; local-gate slot release; bench-hygiene refuses off Linux; hygiene test takes HYGIENE_SCRIPT; with-secrets/with-jev find sops; bench-standard probe dirs removed by a guarded helper, pinned version gone, refuses off-Linux and any argument; adopt.sh re-creates its bus clone and wake config, runs route probes in parallel in distinct slots, and its timeout kills the process group; nova-upgrade-loop installs on every Linux bench through fleet-install-tools.

Verb requirements, not fixed in shell (the scripts are sketches; Glenn 2026-09-18: only Go with tests): F01 root identity in cleanup dispatch; F02 eligibility inside each destructive verb; F04/F05 done; F06 native exit status masked by tail; F07 card copied per attempt; F08 keep failed attempts; F09 harvested marker only after the PR exists; F10 push exit status not stderr; F11 lease against the worker's recorded revision; F12 CI evidence tied to the PR head; F13 install fails on any failed cp/mv (in the nova-pulse install card 9341); F15/F16/F17/F18/F19/F20/F21/F24/F25/F26/F27 as listed inline. Each becomes a test in the replacing verb's PR.
