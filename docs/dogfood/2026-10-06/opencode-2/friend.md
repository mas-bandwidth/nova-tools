# nova-friend dogfood — opencode-2, 2026-10-06

Tool: nova-friend. The binary was built in the staged checkout of commit `7acb90e18a764f0e728cd5ed701196a34405a824` (the tip of `sprint/mechanical-2026-10-02`) with `go build -o $JOB/bin/nova-friend ./cmd/nova-friend`, never an installed copy, and printed `nova-friend v1.0.1-0.20261007211151-7acb90e18a76 linux/amd64 go1.26.6`. Read cold: the binary's own help (`nova-friend`, `nova-friend help`, `nova-friend -h`, every verb's `-h`) and its pages under `docs/` (`docs/FRIENDS.md` and the `nova-friend` section of `docs/CLI.md`); no source was read for the tool's behaviour. Run: every verb at least once with its real flags against a scratch store and a scratch directory on a Linux bench, the refusals too — the store a throwaway Redis on the loopback (`127.0.0.1:16617`, its `friends` set naming `ada` and `bob`), the scratch `HOME` and working directory under the job, and `run`, `serve` and `watch` for real until `timeout` or their own timeout stopped them. Below, `$R` is `127.0.0.1:16617`, `$S` the scratch directory, `$B` the built binary, and the install family ran with `HOME=$S/home`.

## Findings

1. `nova-friend check --as ada bob --redis $R`
   Printed:
   ```
   CHECK DAEMON friend=bob agent=none pid=- status=none connection=- challenge=- pong_age=3m8s presence=down seen_age=- proof=none proof_age=-
   CHECK HARNESS friend=bob harness=unknown route=push last=- last_exit=- failed_of_last20=0 deferred=0 broken=- reason=- session_live=- queued=-
   CHECK BUS friend=bob real_since=0 last_real=-
   ```
   I expected `bob` alone: `--redis` is a flag of `check` (its `-h` flags list calls it "the bus store's Redis address"), so the natural order should judge one friend. Instead the flag's two tokens were kept as positional friends — the run printed the same five lines for `friend=--redis` and `friend=127.0.0.1:16617` and ended `CHECK OK friends=3 ok=0 broken=0 deaf=0 silent=0 down=3 untrue=0` at exit 1, two friends that do not exist reported down. `nova-friend check -h` prints the usage line `nova-friend check [--as <coordinator>] [<friend>...] [--since <duration>] [--shown <file|->] [--json]`, which omits `--redis` and puts friends before flags, and is what teaches the order that breaks.
   Grade: URGENT

2. `nova-friend ping --as ada --to bob --dry-run`
   Printed:
   ```
   PING FAILED: --dry-run was given and the verb never read it (Call.DryRun); it may have written
   (one line printed)
   ```
   I expected the one thing that is actually missing to be named: `--dry-run` is documented as "print what the verb would write and write nothing", and with no `--redis` and no `NOVA_BUS_REDIS` the honest answer is the `--redis is required` refusal at exit 2. Instead the verb exits 1 as FAILED and asserts it "may have written" when it read and wrote nothing. `pong --as bob --nonce abc123 --dry-run`, `run --dry-run --as bob --harness dsh --dir $S/work/bob` and `ping --as ada --wake --to-friends --dry-run` print the same line under their own word.
   Grade: URGENT

3. `nova-friend resume --as bob --dir $S/work/bob --dry-run`
   Printed:
   ```
   RESUME FAILED: --dry-run was given and the verb never read it (Call.DryRun); it may have written
   (one line printed)
   ```
   I expected `RESUME OK cleared=none dry_run=true`: `resume -h` documents `--dry-run` ("print what the verb would write and write nothing"), and with a `PAUSED` marker in the state directory the same verb prints `RESUME OK cleared=would pause=out\x20of\x20funds dry_run=true` at exit 0. "Is anything paused?" is the one question a dry run is for, and on the no-marker path the verb fails naming the skeleton's internal guard instead of the state it read.
   Grade: URGENT

4. `nova-friend uninstall --as bob`
   Printed:
   ```
   UNINSTALL OK label=com.nova.friend-bob plist=/home/<user>/zhi-bench/dogfood-opencode-2-friend-bb.w1~15/scratch/home/Library/LaunchAgents/com.nova.friend-bob.plist
   UNINSTALL RAN command="launchctl bootout gui/1000/com.nova.friend-bob"
   ```
   I expected the missing loader named: `command -v launchctl` prints nothing on the bench, so no bootout ran, yet the verb reports `UNINSTALL RAN` for it at exit 0 (the plist was removed and the loaded agent, if any, was not). `nova-friend ping-uninstall --as ada` prints the same `... RAN command="launchctl bootout ..."` shape at exit 0.
   Grade: NEXT

5. `nova-friend install --as bob --harness opencode --dir $S/work/bob --redis $R`
   Printed:
   ```
   INSTALL FAILED plist=/home/<user>/zhi-bench/dogfood-opencode-2-friend-bb.w1~15/scratch/home/Library/LaunchAgents/com.nova.friend-bob.plist: launchctl bootstrap: exec: "launchctl": executable file not found in $PATH: 
   (one line printed)
   ```
   I expected the platform checked before anything is written: install's own effect line is "writes the harness's settings and the launchd agent ... and loads it", and both `launchctl` and `launchd` are macOS-only. The plist and `$S/work/bob/opencode.json` stayed on disk after the failure, so the friend is half-installed and the next `run` reads the plist as drift; the line also ends in a bare `": "` where the failed command's stderr was empty.
   Grade: NEXT

6. `nova-friend ping-install --as ada --every 30s --redis $R`
   Printed:
   ```
   PING-INSTALL REFUSED: launchctl bootstrap: exec: "launchctl": executable file not found in $PATH: ; run: nova-friend help
   (one line printed)
   ```
   I expected a remedy a reader can act on in one turn: `nova-friend help` does not put `launchctl` on the machine and does not name the darwin requirement, so a coordinator following it takes no useful step. The missing executable (or the platform) is the remedy, the way `install --harness nonsense` names the harnesses it wants.
   Grade: NEXT

7. `nova-friend refuse-go --name rm`
   Printed:
   ```
   REFUSE-GO REFUSED: rm is refused: no go command runs on this machine, it is not a build machine; run: rsync the job's clone to a bench, then ssh <bench> 'cd <clone> && rm ...'; run: nova-friend help
   (one line printed)
   ```
   I expected `rm` refused as a name outside the set: `--name`'s own description is "the command that was run: go or gofmt (required)". Instead the verb answers as if `rm` were a go command, writes a bench line for `rm`, and carries two `run:` clauses with the second (`nova-friend help`) a remedy the first is not.
   Grade: NEXT

8. `nova-friend check --as ada --redis $R --json bob`
   Printed:
   ```
   {"friends":[{"friend":"bob","daemon":{"friend":"bob","agent":"none","pid":"-","status":"none","connection":"-","challenge":"-","pong_age":"3m8s","presence":"down","seen_age":"-","proof":"none","proof_age":"-"},"harness":{"friend":"bob","harness":"unknown","route":"push","last":"-","last_exit":"-","failed_of_last20":0,"deferred":0,"delivered":0,"failed":0,"broken":"-","reason":"-","session_live":"-","queued":"-"},"bus":{"friend":"bob","real_since":0,"last_real":"-"},"work":{"friend":"bob","inbox":0,"outbox":0,"newest_outbox":"-","newest_at":"-"},"verdict":{"friend":"bob","verdict":"down","shown":"-","why":"down by presence"}}],"summary":{"friends":1,"ok":0,"broken":0,"deaf":0,"silent":0,"down":1,"untrue":0}}
   (one line printed)
   ```
   I expected the one envelope the banner promises ("Every verb but run, serve takes `--json`: the same result as one JSON object"): `result` with the verb, status and exit, then `facts` and `items`. `status --json` and `wait-pong --json` carry it; `check --json` returns a bare `friends`/`summary` object with no verb, no status and no exit, so a caller cannot tell from the object that the verb exited 1.
   Grade: NEXT

9. `nova-friend wait-pong --from alice --nonce abc123 --timeout 1s --redis $R`
   Printed:
   ```
   WAIT-PONG NONE daemon=false: no pong abc123 from alice within 1s
   (one line printed)
   ```
   I expected an unknown name refused the way `ping --as ada --to alice` refuses it ("alice is no known name; ... add one with nova-config friend add alice ...") at exit 2. Instead a coordinator waiting on a typo waits out the whole timeout and is told no pong came from a name the store does not hold, then has to find the typo itself.
   Grade: NEXT

10. `nova-friend check --harness dsh --as bob --dir $S/work/bob --dry-run`
   Printed:
   ```
   CHECK OK harness=dsh dir=/home/<user>/zhi-bench/dogfood-opencode-2-friend-bb.w1~15/scratch/work/bob within=5m0s dry_run=true
   CHECK PLAN command="/home/<user>/zhi-bench/dogfood-opencode-2-friend-bb.w1~15/bin/nova-friend pong --as bob --nonce o80cng --state-dir /home/<user>/zhi-bench/dogfood-opencode-2-friend-bb.w1~15/scratch/work/bob/.nova-friend --redis  --to ada"
   CHECK NOTE nothing was delivered; the session would run the plan line and its pong would end the check
   ```
   I expected the plan line to be a command that runs: with no `--redis` and no `NOVA_BUS_REDIS`, `--redis` is empty in the plan, so the session that pastes it gets a flag refusal instead of a pong and the delivery check can never be armed. The verb should refuse the missing store or print the flag it actually defaults to (`--redis <addr>` when one is set). The same empty field appears in every `check --harness ... --dry-run` with no store named.
   Grade: NEXT

11. `nova-friend bogus`
   Printed:
   ```
   FRIEND REFUSED: "bogus" is no verb and no file; the verbs are run, beat, install, uninstall, check, host, ping, ping-install, ping-uninstall, pong, wait-pong, watch, status, refuse-go, resume, serve and 1 more, and a file is given by its path (./bogus); run: nova-friend help
   (one line printed)
   ```
   I expected the tool's own name in the refusal: onboarding point 1 is `<tool>[ <verb>] REFUSED: ...`, and line 1 of the banner says `nova-friend`. The prefix is `FRIEND`, a short name a reader never chose. The list also ends `and 1 more`, hiding `version`, in the one line whose job is to name the verbs there are.
   Grade: NEXT

12. `nova-friend install --as bob --harness dsh --dir $S/work/bob --redis $R --dry-run`
   Printed:
   ```
   INSTALL REFUSED: not a real directory: /home/<user>/.dsh/profiles/desktop (desktop-profile) is missing, want a directory; name the real path, install never replaces it; run: nova-friend help
   (one line printed)
   ```
   I expected "name the real path" to be followable from the page the refusal points at: `install -h` has no flag for the dsh profiles directory and the refusal names neither a flag nor an environment variable, so a reader who wants a dsh friend has no turn to take. The refusal teaches the missing directory only by its default path.
   Grade: NEXT

## What held

Every verb ran at least once with real flags against the scratch store and a scratch working directory: `run` (the dry run, and a real daemon for 20 s that wrote `status.json`, `presence.json` and `deliver.log`), `beat` (refused with the server's exact dial error), `install` and `uninstall` (dry runs and real writes under the scratch `HOME`), `check` (the health check, `--settings`, the `--harness` delivery check and its refusals), `host` (dry run, a real tmux session, and the second-run refusal), `ping` (single target, the unknown-name refusal, and both wake-loop refusals), `ping-install` and `ping-uninstall`, `pong`, `wait-pong` (a real pong and its NONE), `watch` (NONE, then a real `WATCH MESSAGE`/`WATCH OK` from a bus message), `status` (NONE, then a live daemon's OK line), `refuse-go`, `resume` (a real cleared marker), `serve` (dry run and a real loop stopped by SIGTERM), `version` and `help`: no verb could not be run. The banner, the per-verb helps and `-h` all exit as their tables say, `help <verb>` and `nova-friend -h` print help at exit 0, an unknown flag names the verb's flags and `-h`, the refusal grammar names every independent problem at once with a remedy, `install` refuses a symlinked `--dir` and writes nothing, and `status --json`, `wait-pong --json` and `host --json` render the same value as their lines.

READ 6/10 — the banner, the per-verb helps and the refusal grammar answer a cold reader truly for most verbs, but `check`'s usage line omits `--redis` and its parse then invents friends, three documented `--dry-run` paths fail the skeleton's own guard, `check --json` has no result envelope, and the launchd family reports work it did not do.

USE 7/10 — every verb ran for real against one scratch store and one scratch directory, the daemon wrote a live status, `watch` took a real bus message and `resume` cleared a real marker, held down by the dry runs that fail on the path that should need no state and by the bootout that exits 0 with no `launchctl`.

urgent=3 next=9
