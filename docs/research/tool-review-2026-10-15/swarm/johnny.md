# nova-swarm AI-usability review, 2026-10-15

Reviewer: mercury-3-preview-1002
Build: cc2f2f8c8148
Score: 9/10 (49/55)

## Rubric
| # | criterion | score | evidence |
|---|---|---|---|
| 1 | cold | 5 | `nova-swarm help` -> `example:` block runs: `nova-swarm template --name card` prints a full card, exit 0 |
| 2 | refusals | 4 | `nova-swarm bogus` -> `REFUSED: unknown verb "bogus"; ... run: nova-swarm help`; but `nova-swarm template` -> `--name is required; it wants one of ...; refusing to guess` names no next command |
| 3 | output | 5 | `nova-swarm lint --card template-card.md --child-rules` -> `LINT OK card=template-card.md checks=45`; stable typed-line grammar; help offers no `--json`, so that clause is n/a |
| 4 | dry-run | 3 | `nova-swarm install disk-guard --dir ./units --dry-run` -> `INSTALL DISK-GUARD DRY-RUN unit=units/nova-swarm-disk-guard.service; nothing was written or loaded`; but `slots take -h` and `slots release -h` list no `--dry-run` for their writes |
| 5 | idempotent | 3 | no write verb's help lists `--op`; `slots take -h` and `slots release -h` have none |
| 6 | no prompts | 5 | none of bare, help, template, lint, version, the refusals or the dry-runs waited on interactive input |
| 7 | exit codes | 5 | `nova-swarm lint --card scratch-card.md --child-rules` -> exit 1 (drift, as help states); `nova-swarm template --name bogus` -> exit 2; `nova-swarm version` -> exit 0 |
| 8 | one word | 5 | verbs are all single words (template, lint, member, native, step, disk-guard, mirror, install, uninstall, worker, verify, doctor, profile, slots, version), no duplicates |
| 9 | push | 4 | slots leases track holder RUNNING state and release checks liveness (`SLOTS KEPT live=<n>`), but member's loop reads the sprint queue each tick (poll); could not test against a live server |
| 10 | no leaks | 5 | no run printed a secret; `install --dry-run` printed only the unit path and ExecStart |
| 11 | true help | 5 | `template --name card` lints `LINT OK checks=45` with placeholder NOTEs exactly as made says; exit codes and refusal shapes match the help tables |

## 10/10 blockers
| # | where | what an AI meets | fix | size |
|---|---|---|---|---|
| 1 | `nova-swarm slots take --store /srv/slots --owner ada --n 1 --for 30m` | the write verbs `slots take`, `slots release`, `native` and `member` have no `--dry-run`; `slots take -h` lists none, so there is no way to preview a lease or a card run before it happens | add `--dry-run` that runs every pre-write check (share, holders, flags) and stops before granting or staging | M |
| 2 | `nova-swarm profile --jobs 'garb/*'` | prints `PROFILE SUMMARY jobs=0 mean_wall=0.0 clone=0.0 deps=0.0 read=0.0 edit=0.0 test=0.0 retry=0.0 result=0.0` and exits 0: a garbage glob reads as a real zero measurement | refuse and exit 2 when a matched path holds no readable timeline.tsv, naming each skipped path and why | S |
| 3 | `nova-swarm template` | prints `nova-swarm template: --name is required; it wants one of capacity, card, ...; refusing to guess` with no `REFUSED` word and no `run:` line, unlike the unknown-flag and unknown-verb refusals | send the missing-flag and bad-value lines through the same refusal printer, one line each, ending `run: nova-swarm help template` | S |

## Nice-to-haves
| # | where | what | fix | size |
|---|---|---|---|---|
| 1 | every verb | no verb takes `--json`, so an AI cannot get a machine-typed result for any inspection | add `--json` to the inspection verbs (`template`, `lint`, `profile`, `worker check`, `slots list`) from the same result value | M |
| 2 | write verbs | no verb takes `--op`, the idempotency id the standard expects of a retry-safe write | add `--op <id>` to `slots take`/`slots release` and record the granted/freed lease under it so a retry is safe | M |
| 3 | `slots list -h` | `state` and `stranded` appear in output but are not defined on the help | define both words on `slots list -h` | S |
| 4 | `install --dry-run` | prints the whole systemd unit string on stdout, heavy for a plan line | print a one-line summary and name the unit file, not its full contents | S |

## Top 3 fixes
1. Add `--dry-run` to `slots take`/`slots release` (and `native`/`member`), so every write can be previewed.
2. Make `profile` refuse a glob that yields no readable timeline as exit 2 instead of printing a zero measurement.
3. Send the missing-flag and bad-value refusals through the refusal printer so every refusal names the next command.

## Good, keep
The banner answers what/how/use and its `example:` block runs as printed: `template --name card`, `template --name worker` and `lint --rules` all work cold with no setup, and the bare command refuses in one line naming the verbs and `run: nova-swarm help`.

Unknown flags and verbs refuse with the cause, the full flag/verb list, and a `run:` command; `lint` reports every drift at once on its own `LINT DRIFT` line and exits 1 as documented, and `template --name card` lints clean with `LINT OK checks=45` plus placeholder NOTEs exactly as the help claims.

The install/uninstall/disk-guard dry-runs really write nothing and print a clear `... DRY-RUN ... nothing was written or loaded` line, and exit codes (0/1/2) held true in every probe; an old defect from the 1.2.0 rating, a missing-flag refusal with no `run:` line (finding 8), is still present and is now blocker 3.

## What could not be tried
- `native`, `member`, `step`: they run a card end to end; this review takes no run and no lease, so they were judged from help and dry-run availability only.
- `slots init`/`take`/`release`/`run`: the card forbids taking a slot or a lease; judged from `-h` and the banner.
- `verify`, `worker check`: need a result file and a worker description; only `-h` was inspected.
- `doctor`, `mirror`, `install`/`uninstall` (real), `disk-guard` (full pass): would probe the host or a network or load units; only dry-runs and help were used.
