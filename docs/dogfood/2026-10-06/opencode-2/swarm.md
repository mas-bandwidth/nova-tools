# nova-swarm dogfood — opencode-2, 2026-10-06

One tool, one sitting, read cold as a stranger: only `nova-swarm -h`, `nova-swarm help`, every
verb's and group's `-h` (`slots init|take|release|list|run`, `worker check`), and the tool's own
pages `docs/SPEC-SWARM.md` and `docs/nova-swarm-quickstart.md`. Built on a Linux bench from the
staged checkout at `5eca256c24cb113dda1055406c18fd76e6abbfc0` with
`go build -o $JOB/bin/nova-swarm ./cmd/nova-swarm` (never the installed binary), it printed
`nova-swarm v1.0.1-0.20261007223151-5eca256c24cb linux/amd64 go1.26.6`. Every verb ran at least
once with its real flags against scratch directories (a temp directory and a scratch slot store):
`native` end to end through a fake harness (with and without the wall, a failing harness, a silent
harness, a card with no contract, a numeric budget, `--sweep-now`, a worker description, the
missing-flag refusals), `step` a real regex card, its dry run and its remainder, and the refusals
too. No server and no member loop was started, so `member` answered only its help, its missing-flag
refusals and one `--once` pass against a dead address. No code changed; a finding is recorded here
and never fixed here.

## Findings

1. `/tmp/nvsw/bin/nova-swarm verify --result /tmp/nvsw/scratch/verify/rc1/RESULT.md --contract 'RESULT: dogfood sha=abcdef123456' --label card1 --run-record /tmp/nvsw/scratch/verify/rc1/exit.json`
   Printed:
   ```
   RESULT OK "card1" line2="DONE"
   (one line printed)
   ```
   I expected a refusal or an abstain: `verify -h` calls `--run-record` "the job's exit.json file:
   the harness's exit code joins the verdict", and the receipt written beside it holds
   `exit_code=1`, so a run whose own record says it failed must not pass as OK.
   Grade: URGENT (wrong result: a failed run accepted as OK, exit 0)

2. `/tmp/nvsw/bin/nova-swarm worker check /tmp/nvsw/scratch/worker/bad.json`
   Printed:
   ```
   WORKER DRIFT worker: /tmp/nvsw/scratch/worker/bad.json is not a worker description this tool can read (invalid character 'n' looking for beginning of object key string); the fields are name, provider, model, base_url, env_var, key_file, secret, usage, class, harness, harness_args, worker_dir, deadline, board, max_turns, max_cache_read, read_roots, input_limit_phrases, launch_grace
   WORKER DRIFT harness: harness is empty; it wants the harness command to run
   WORKER DRIFT harness_args: harness_args must carry {prompt} and {model}, so the prompt file and the model this description names both reach the harness
   ```
   I expected exit 2 and no drift lines: `worker -h` says exit is "2 it cannot be read, or a bad
   invocation", and the tool's own first line says it cannot read the file, so `harness`,
   `harness_args` and `worker_dir` are invented from a parse that failed.
   Grade: URGENT (wrong result: a parse failure reported as a readable description that drifts)

3. `/tmp/nvsw/bin/nova-swarm install mirror-refresh --dry-run --dir /tmp/nvsw/scratch/units`
   Printed:
   ```
   nova-swarm install mirror-refresh REFUSED: nova-swarm has no mirror verb for the unit to run; nothing was written; run: nova-swarm help install
   (one line printed)
   ```
   I expected the refusal to name the real reason: `/tmp/nvsw/bin/nova-swarm mirror --repos
   nova-tools --base file:///tmp/nvsw/scratch/mirrorbase --dir /tmp/nvsw/scratch/mirrors` printed
   `MIRROR OK nova-tools` and `uninstall mirror-refresh --dry-run` is accepted, so the verb the unit
   would run does exist.
   Grade: URGENT (a refusal that lies: the claim "has no mirror verb" is false)

4. `/tmp/nvsw/bin/nova-swarm disk-guard --cache /tmp/nvsw/scratch/disk2/cache --cache-max-gb 1 --modcache-max-gb 1 --logs /tmp/nvsw/scratch/disk2/logs --land /tmp/nvsw/scratch/disk2/land --mirrors /tmp/nvsw/scratch/disk2/mirror --disk-floor 0`
   Printed:
   ```
   TRIMMED go-build ~/.cache/go-build freed=0 size=2208931209 cap=1073741824
   KEPT go-mod ~/go/pkg/mod: a go command is running (~/go/bin/go test -p 2 -count=1 -timeout 600s ./internal/sprint ./internal/sprint/store ./internal/config ./cmd/nova-config ./cmd/nova-sprint ./internal/ci ./internal/docs)
   DISK-GUARD OK freed=0 free=1568073637888
   ```
   I expected `KEPT` or a line saying the cache could not be reduced: `disk-guard -h` says every Go
   build cache is "held under --cache-max-gb", the tool prints `KEPT go-mod ...` when a cache is
   left alone, and a `TRIMMED` line with `freed=0` and `size` still over `cap` is a trim that did
   not happen; the same command printed the same `TRIMMED ... freed=0` line again on the next pass.
   Grade: URGENT (wrong result: TRIMMED read as a cache brought under its cap)

5. `/tmp/nvsw/bin/nova-swarm slots release --store /tmp/nvsw/scratch/slots3 --owner ada --label held`
   Printed:
   ```
   SLOTS RELEASED owner=ada released=0 held=1 live=1
   SLOTS KEPT owner=ada live=1: a lease whose holder is still running is not freed, because freeing it does not stop the holder -- it only lets a second run take the same seat; stop the holder (slots list names its pid) or pass --force
   ```
   I expected the refusal word alone on the refused run, exit 2: the standard is "no OK word on a
   non-zero exit", and `slots release -h` documents `SLOTS KEPT`, so a script reading stdout sees
   `RELEASED` for a release that did not happen.
   Grade: URGENT (wrong result: a success word on a kept lease, exit 2)

6. `cd /tmp/nvsw/scratch && /tmp/nvsw/bin/nova-swarm native --bench bench --harness ./relharness --model fake/model --card /tmp/nvsw/scratch/ncards/native.md --slot /tmp/nvsw/scratch/native-root/slot9 --root /tmp/nvsw/scratch/native-root --deadline 30s --tokens unmetered --no-wall --identity ada,boss,boss@example.test --label nrel`
   Printed:
   ```
   STAGE OK bench=bench repo= base= secs=0 clone=0.0 fetch=0.0 checkout=0.0
   NATIVE REFUSED: the child could not be started: fork/exec ./relharness: no such file or directory; run: nova-swarm native -h
   ```
   I expected the relative path its own example prints (`--harness ./harness`) to run: the path is
   checked against the caller's directory but the child starts with the job directory as its working
   directory, so every relative harness path is a path that cannot launch, while an absolute path
   runs the same card to `NATIVE OK ... rc=0`.
   Grade: URGENT (help that lies: its own first-run example cannot run)

7. `/tmp/nvsw/bin/nova-swarm native --bench bench --harness /tmp/nvsw/scratch/harness/ok --model fake/model --card /tmp/nvsw/scratch/ncards/native.md --slot /tmp/nvsw/scratch/native-root2/slot1 --root /tmp/nvsw/scratch/native-root2 --deadline 30s --tokens 1000 --no-wall --identity ada,boss,boss@example.test --label nnum`
   Printed:
   ```
   STAGE OK bench=bench repo= base= secs=0 clone=0.0 fetch=0.0 checkout=0.0
   NATIVE NOTE catalog: no catalog at /tmp/nvsw/scratch/native-root2/catalog/models.json (a member refreshes it at its start); the harness fetches its own at this start
   NATIVE NOTE: no harness store: looked at /tmp/nvsw/scratch/native-root2/slot1/data/opencode/opencode.db and /tmp/nvsw/scratch/native-root2/slot1/data/.local/share/opencode/opencode.db
   ```
   I expected exit 2: the exit table under `docs/SPEC-SWARM.md` says a numeric `--tokens` on a native
   whose usage source is `none` could not run, and the tool refuses that same condition when a worker
   description says `usage: none`, yet this run printed `NATIVE OK ... budget=-/1000 usage=none
   reason=no-store` at exit 0 and wrote a `usage.tsv` holding `-` in every token column.
   Grade: URGENT (wrong result: a budget nothing can observe is reported OK)

8. `/tmp/nvsw/bin/nova-swarm lint --card /tmp/nvsw/scratch/nope.md`
   Printed:
   ```
   nova-swarm lint: --card wants a readable file of the card text: open /tmp/nvsw/scratch/nope.md: no such file or directory
   (one line printed)
   ```
   I expected the one refusal grammar to end `; run: <remedy>`: the same verb's missing-flag refusal
   carries a remedy (`nova-swarm lint: --card is required; ... refusing to guess`), while this
   file-open refusal stops after the operating system's words, with nothing a stranger can act on
   beyond the path they already typed, and `verify --result` prints the same shape.
   Grade: URGENT (a refusal with no remedy)

9. `/tmp/nvsw/bin/nova-swarm doctor --json`
   Printed:
   ```
   nova-swarm doctor REFUSED: unknown flag --json; the flags of doctor are --local, --path; run: nova-swarm help doctor
   (one line printed)
   ```
   I expected every verb to accept `--json`, the second rendering of its one value (the standard's
   "one output structure, two renderings", `AGENTS.md` "One shape across the set"): the same refusal
   came from `version`, `lint`, `profile`, `worker check`, `disk-guard`, `mirror`, `template`,
   `verify` and `slots list`, and no `-h` names `--json` either, so a machine reader cannot tell a
   line-only design from a missing flag.
   Grade: NEXT (a missing flag on every verb)

10. `/tmp/nvsw/bin/nova-swarm verify --result /tmp/nvsw/scratch/verify/nocontract/RESULT.md --label l`
    Printed:
    ```
    nova-swarm verify: --contract is required; it wants the card's contract line, which line 1 of RESULT.md must equal exactly; refusing to guess
    (one line printed)
    ```
    I expected `nova-swarm verify REFUSED: ...; run: <remedy>`, the one grammar the bare and
    unknown-verb refusals follow (`nova-swarm REFUSED: unknown verb "compute"; ... run: nova-swarm
    help`); `lint`, `native`, `member`, `profile`, `slots init|release|run`, `template`, `mirror` and
    `disk-guard` print the same status-less shape, so a script matching `REFUSED` misses every
    missing-flag refusal.
    Grade: NEXT (a grammar break a caller greps for)

11. `/tmp/nvsw/bin/nova-swarm slots take --store /tmp/nvsw/scratch/slots2 --owner ada --n 1 --for 30m --kind schema --label c3`
    Printed:
    ```
    SLOTS OK owner=ada granted=1 held=4 share=8 free=4
    (one line printed)
    ```
    I expected the grant line to name the kind and its weight, as the later `slots list` line does
    (`kind=schema weight=4`), so a reader can check why `--n 1` became `held=4`.
    Grade: NEXT (a result that does not show the multiplier it applied)

12. `/tmp/nvsw/bin/nova-swarm slots bogus`
    Printed:
    ```
    nova-swarm slots REFUSED: unknown subcommand "bogus"; run: nova-swarm help slots
    (one line printed)
    ```
    I expected the five names, as the no-subcommand refusal prints them (`wants a subcommand: init
    ... take ... release ... list ... run ...`); the unknown-name rule is that the answer names what
    there is.
    Grade: NEXT (an unclear refusal)

13. `/tmp/nvsw/bin/nova-swarm install --dry-run --dir /tmp/nvsw/scratch/units --every 15m disk-guard`
    Printed:
    ```
    nova-swarm install REFUSED: takes no positional arguments, got 1: ["disk-guard"] (every input is a flag); run: nova-swarm help install
    (one line printed)
    ```
    I expected the documented call `nova-swarm install disk-guard --dry-run ...` to work with its
    inputs in any order, or a refusal that says the kind comes first; "takes no positional arguments"
    is false, because the verb takes exactly one and its own usage and example show it.
    Grade: NEXT (a false reason in a refusal)

14. `/tmp/nvsw/bin/nova-swarm install disk-guard --dry-run --dir /tmp/nvsw/scratch/units --every 15m --log /tmp/nvsw/scratch/units/dg.log`
    Printed:
    ```
    INSTALL DISK-GUARD DRY-RUN unit=/tmp/nvsw/scratch/units/nova-swarm-disk-guard.service; nothing was written or loaded
    "[Unit]\nDescription=nova disk-guard: the machine's disk upkeep, one pass every --every (nova-swarm disk-guard)\nStartLimitIntervalSec=0\n\n[Service]\nExecStart=\"/tmp/nvsw/bin/nova-swarm\" \"disk-guard\"\nRestart=always\nRestartSec=900\n\n[Install]\nWantedBy=default.target\n"
    ```
    I expected the unit's text as text, the way `disk-guard --dry-run` prints its plan, because
    `install -h` says `--dry-run` will "print the unit and write and load nothing"; a reader cannot
    see the `ExecStart`, `Restart` or log path they are about to install.
    Grade: NEXT (a dry run whose plan cannot be read)

15. `/tmp/nvsw/bin/nova-swarm profile --jobs '/tmp/nvsw/scratch/nope-*'`
    Printed:
    ```
    PROFILE SUMMARY jobs=0 mean_wall=0.0 clone=0.0 deps=0.0 read=0.0 edit=0.0 test=0.0 retry=0.0 result=0.0
    (one line printed)
    ```
    I expected a NOTE telling a glob that matched nothing from a glob whose job directories hold no
    `timeline.tsv`: `profile --jobs '/tmp/nvsw/scratch/native-root/slot1/jobs/*'`, where the runs did
    happen, printed the same zero summary.
    Grade: NEXT (silent about having read nothing)

16. `/tmp/nvsw/bin/nova-swarm verify --result /tmp/nvsw/scratch/verify/usagebad/RESULT.md --contract 'RESULT: dogfood sha=abcdef123456' --label it --usage /tmp/nvsw/scratch/verify/usagebad/usage-bad.tsv`
    Printed:
    ```
    RESULT OK "it" line2="DONE"
    (one line printed)
    ```
    I expected the file refused with the columns it wants, or the zeros explained: `usage-bad.tsv` is
    `nonsense`, yet the run passed and the receipt it wrote holds `tokens_in=0 tokens_out=0 usd=-`,
    and no `-h` names the `usage.tsv` shape.
    Grade: NEXT (an input accepted and silently zeroed)

17. `/tmp/nvsw/bin/nova-swarm verify --result /tmp/nvsw/scratch/verify/ok/RESULT.md --contract 'RESULT: dogfood sha=abcdef123456' --label card1`
    Printed:
    ```
    RESULT OK "card1" line2="DONE"
    (one line printed)
    ```
    I expected `-` or no field when no `--card` is given: the receipt written beside it holds
    `card_sha256=e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855`, the SHA-256 of
    the empty string, for a card the tool never read.
    Grade: NEXT (evidence that reads as if a card was checked)

18. `PATH=/tmp/nvsw/scratch/doctor2:$PATH /tmp/nvsw/bin/nova-swarm doctor`
    Printed:
    ```
    DOCTOR DRIFT path=/tmp/nvsw/scratch/doctor2/nova-swarm stamp=nova-swarm v9.9.9-test test/test go9.9.9
    DOCTOR DRIFT local=~/.local/bin/nova-swarm stamp=nova-swarm v1.2.0-dev.28f2e920 linux/amd64 go1.27.1
    DOCTOR REFUSED /tmp/nvsw/scratch/doctor2/nova-swarm shadows ~/.local/bin/nova-swarm; copy the ~/.local/bin binary over the PATH one, or fix PATH so ~/.local/bin comes first
    ```
    I expected a NOTE naming the running build's difference from `PATH`'s: the invoking binary is
    `nova-swarm v1.0.1-0.20261007223151-5eca256c24cb linux/amd64 go1.26.6`, a third stamp the check
    never compares, so a freshly built binary refuses under an environment it is not part of.
    Grade: NEXT (a comparison that omits the binary doing the asking)

19. `/tmp/nvsw/bin/nova-swarm native --bench bench --harness /tmp/nvsw/scratch/harness/ok --model fake/model --card /tmp/nvsw/scratch/ncards/nocontract.md --slot /tmp/nvsw/scratch/native-root4/slot1 --root /tmp/nvsw/scratch/native-root4 --deadline 30s --tokens unmetered --no-wall --identity ada,boss,boss@example.test --label nc1`
    Printed:
    ```
    STAGE OK bench=bench repo= base= secs=0 clone=0.0 fetch=0.0 checkout=0.0
    NATIVE NOTE catalog: no catalog at /tmp/nvsw/scratch/native-root4/catalog/models.json (a member refreshes it at its start); the harness fetches its own at this start
    NATIVE NOTE: no harness store: looked at /tmp/nvsw/scratch/native-root4/slot1/data/opencode/opencode.db and /tmp/nvsw/scratch/native-root4/slot1/data/.local/share/opencode/opencode.db
    ```
    I expected admission to refuse the card, or the publication to: the quickstart says "Each card
    needs a RESULT contract as its first line", and `nocontract.md`'s line 1 is `just a task with no
    contract`, yet the run printed `NATIVE OK ... rc=0` and published the harness's own `RESULT.md`.
    Grade: NEXT (a card admitted without the contract it is run against)

20. `/tmp/nvsw/bin/nova-swarm step --card /tmp/nvsw/scratch/ncards/native.md --dir /tmp/nvsw/repo --no-wall`
    Printed:
    ```
    nova-swarmstep REFUSED: the card has no script step, or has model steps too; the executor runs a card whose every work step is a script step; run: nova-swarm help step
    (one line printed)
    ```
    I expected `nova-swarm step REFUSED:` from the grammar every other verb follows; the missing
    separator reads as a different tool's name and breaks a caller grepping for the verb.
    Grade: NEXT (a grammar break in the printed identity)

21. `/tmp/nvsw/bin/nova-swarm mirror --repos missing --base file:///tmp/nvsw/scratch/mirrorbase --dir /tmp/nvsw/scratch/mirrors`
    Printed:
    ```
    MIRROR FAILED missing: exit status 128
    (one line printed)
    ```
    I expected the URL it tried and a remedy: `mirror -h` says a failure "names it and the cause", and
    a bare `exit status 128` from git names neither the repository URL nor what a reader can do.
    Grade: NEXT (a failure with no cause a reader can act on)

## What held

`version` and `--version` print the one identity line; `doctor` answers the agree, drift and
unreadable paths; every `template --name` writes its template; `lint --rules`, `lint --card` (clean,
typed, `--child-rules`, `--trust`, `--base-check`, `--lineup`), `lint --fleet` and `lint` on a
missing file each answer with a rule, a remedy or a refusal; `verify` matches a contract, writes
`RESULT.md.receipt`, refuses a mismatch, abstains on a permission signature and honours `--max`;
`worker check` reads a filled description, a `--max` bound and an unset `--env` secret; `slots init`,
`take`, `list`, `release` and `run` lease, refuse an over-share take with the holders, keep a live
lease then free it with `--force`, and propagate a command's exit status; `disk-guard` refuses a cap
of zero, judges a dry run, WARNs under a high `--disk-floor` and STOPs at exit 3 under a high
`--stop-floor`; `mirror` against a local bare base prints `MIRROR OK`, and the refused cases name the
missing flag; `install disk-guard --dry-run` and both `uninstall --dry-run` paths write nothing;
`native` runs a fake harness end to end with and without the wall (`sandbox=landlock`), reports a
failing harness (`NATIVE INCOMPLETE ... rc=3`) and a silent one (`why=no-result`), sweeps with
`--sweep-now`, reads a worker description, and refuses the missing-flag and missing-harness paths
with a remedy; `profile` reads a glob and its summary; `step` runs a real regex card (dry run, `STEP
OK`, the commit made) and draws its `--remainder`; every `-h` and `help` line read here exits 0.
`member` could not be run as a loop: no server may be started on this machine, so only its help, its
missing-flag refusals and one `--once` pass against a dead address ran, and that pass printed the
refused queue and stopped; `native` could not be run through a real provider, which this card does
not supply.

READ 7/10 — the banner answers the three questions, every verb answers `-h` at exit 0, the refusals
mostly name what they want, the unknown-flag answers name the nearest flags, and the slot, native and
step lines carry real evidence; it loses points for `nova-swarmstep`, the status-less verb refusals,
the false `install mirror-refresh` reason, the `install` exit table's stale "no mirror verb yet", and
the silent `--json` across every verb.

USE 6/10 — every primary path ran for real against scratch dirs (a slot store inited, taken, listed,
released and run under; a native card through a fake harness with and without the wall, with
`--sweep-now`; a real regex `step`; `disk-guard` dry and real; `mirror` from a local base; every
template; `lint` over real cards; `verify` over real reports), and the missing-flag refusals are
honest and complete; it loses points because `verify --run-record` returns OK over its own
`exit_code=1`, `worker check` calls an unreadable file a drift, `disk-guard` reports a trim it did not
do, and a numeric `--tokens` runs under a usage source it cannot read.

urgent=8 next=13
