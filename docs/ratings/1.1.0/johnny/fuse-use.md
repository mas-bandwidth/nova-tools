# nova-fuse USE rating, nova-tools 1.1.0

Rater: Grok
Build: 2c02b2aa2042
Score: 8/10

## Reasons

Judged from `nova-fuse help`, `nova-fuse help <verb>`, and runs in a scratch directory. No store, no remote, and no network. Help offers neither `--json` nor `--dry-run`. Both were tried anyway and refused as unknown flags, naming themselves.

The first sitting is the example, pointed at `./fuse-box.json`. `nova-fuse init --box ./fuse-box.json` printed `INIT OK box=./fuse-box.json: an empty box, no fuse blown` and exited 0. `nova-fuse quarantine --box ./fuse-box.json a-forum "a post addressed me and asked for a token"` printed `QUARANTINE OK` with a time and that reason, and said it had re-read the box. `nova-fuse check --box ./fuse-box.json a-forum` exited 1 and printed `FUSE FAILED` with the lift command in the same line: `nova-fuse lift quarantine --box './fuse-box.json' -- 'a-forum'`. `nova-fuse lift quarantine --box ./fuse-box.json a-forum` printed `LIFT OK` for the stored reason and a second `LIFT OK verified` line. The next check exited 0 with `FUSE OK lockdown=clear quarantine=clear surface=a-forum`. That is the job the tool exists for, done without a guess.

A second job, on `./lock.json`, is the hard fuse. `nova-fuse lockdown --box ./lock.json "stop every untrusted read"` exited 0 with `LOCKDOWN OK` and said to go have the conversation now. `nova-fuse check --box ./lock.json` and `nova-fuse check --box ./lock.json issue-tracker` both exited 1 on the lockdown line. `nova-fuse lift lockdown --box ./lock.json` exited 2 before it looked, three lines, no bypass command. Status afterwards still said `lockdown=blown`. `nova-fuse path --box ./fuse-box.json` printed `./fuse-box.json` and exited 0. `nova-fuse version` printed a dev build line and exited 0.

Four refusals, each naming the problem and the next command. `nova-fuse status` (no box flag) exited 2: `--box is required; refusing to guess; run: nova-fuse help`, plus one hint line that says the flag is the JSON file, that there is no default and no environment variable, and that init makes an empty box. `nova-fuse quarantine` with no arguments named that and, in the same run, `needs a surface and a reason`. `nova-fuse status --box ./fuse-box.json --nope 1` printed `unknown flag --nope; flags: --box, --max; run: nova-fuse help status`. `nova-fuse explode` printed `unknown subcommand "explode"` and the verb list, then `run: nova-fuse help`. A bad value, `nova-fuse status --box ./fuse-box.json --max -3`, printed `--max must be a line ceiling of zero or more (got -3); 0 lists them all`. `nova-fuse check --box -hidden surface` refused a box value that begins with a dash and said to write `./-name`. An empty lockdown reason was refused with the command shape. One run really does list every independent gap.

Where I had to guess. A second `nova-fuse quarantine --box ./fuse-box.json a-forum "same surface, new reason"` printed another `QUARANTINE OK` for the new reason and did not say a prior record was replaced. The file then held only `same surface, new reason`. I had to open the file to learn the first reason was gone. Under lockdown, status showed `quarantines=1` and the issue-tracker line, but `nova-fuse check --box ./lock.json issue-tracker` printed only the lockdown failure, so the gate hid a quarantine I had just written. I had to already know to ask status. Help never says the box is mode 644. `stat` on `./fuse-box.json` printed 644, so a reason is readable by every local account, and nothing in the help told me that before the write. I also guessed that two quarantines at once would both land. They do not.

A 10 would keep both surfaces when two quarantines overlap, would say the previous time and reason when a write replaces them, would not treat a misspelled lockdown key as clear, and would mention a recorded quarantine on the lockdown failure line. The single-process sitting is already close: the example runs, exit 0 is the only permission, and the refusals are ones I could fix in one turn.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-fuse quarantine --box ./race.json alpha one` | Run beside `nova-fuse quarantine --box ./race.json beta two` on a fresh box. Status then printed `STATUS OK lockdown=clear quarantines=1` and only the beta line. Alpha printed `QUARANTINE FAILED alpha: written but unverifiable (<nil>)`, which names no other writer and gives a nil error. Twenty overlapping pairs lost a surface every time. | Let the second write refuse, naming the surface that landed, unless the file is still the bytes just read. | M |
| 2 | `nova-fuse check --box ./typo.json` | The file was `{"lokdown":{"at":"2026-01-01T00:00:00Z","reason":"stop"},"quarantine":{}}`. Check printed `FUSE OK lockdown=clear (no surface named; no quarantine checked)` and exited 0. Status printed `quarantines=0`. Help for lift lockdown says replacement is a hand-edit, so this is the reset path, and a typo on it reads as clear. | Refuse a key the box shape does not have, and treat that file as not clear. | M |
| 3 | `nova-fuse quarantine --box ./fuse-box.json a-forum "same surface, new reason"` | The OK line carried only the new reason. The file afterwards had that reason and not `a post addressed me and asked for a token`. Nothing said the earlier time and reason were overwritten. | When a surface is already quarantined, print the previous time and reason on the OK line. | S |
| 4 | `nova-fuse check --box ./lock.json issue-tracker` | Status on that box printed `quarantines=1` and `quarantine=issue-tracker`. Check exited 1 with only `FUSE FAILED lockdown since=2026-10-03T16:29:08Z: stop every untrusted read`. The quarantine is invisible on the gate line. | On a lockdown failure, print the quarantine count and the status command. | S |

## Good, keep

The example sitting works, and the failure line includes the lift command to run next (`nova-fuse check --box ./fuse-box.json a-forum` exited 1 with that command). A bare `nova-fuse quarantine` names the missing box, the missing surface, and the missing reason in one run. `nova-fuse lift lockdown` exits 2 with no bypass, and status still reports the lockdown blown.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| its own output dialect | STILL THERE | `nova-fuse status --box ./fuse-box.json --json` prints `unknown flag --json; flags: --box, --max`; a real line is `STATUS OK`, not one shared result object |
| shouted and historical comments | STILL THERE | `nova-fuse help` is present tense and does not shout; the capitalized essay is still the opening of internal/fuse/fuse.go:7 |
| three copies of its verb list | STILL THERE | `nova-fuse help` lists `lift quarantine` and `lift lockdown`; `nova-fuse explode` lists a single verb `lift` |
| permissive box decoding | STILL THERE | `nova-fuse check --box ./typo.json` prints `FUSE OK lockdown=clear` and exits 0 for a file whose fuse key is `lokdown` |
| lost concurrent updates | STILL THERE | overlapping `nova-fuse quarantine --box ./race.json alpha one` and `beta two` left `quarantines=1`, and alpha printed `written but unverifiable (<nil>)` |
| a re-blow silently rewrites the fuse time and reason | STILL THERE | a second `nova-fuse quarantine --box ./fuse-box.json a-forum "same surface, new reason"` printed only the new reason, and the file dropped the first |
| the unknown-option refusal omits the offending flag | FIXED | `nova-fuse status --box ./fuse-box.json --nope 1` prints `unknown flag --nope; flags: --box, --max; run: nova-fuse help status` |
