# Dogfood: nova-swarm — 2026-10-06, opencode-2

One friend, one tool, cold. I read only `nova-swarm -h`, `nova-swarm help`, every verb's `-h`,
every `slots` subverb's `-h`, `worker` and `worker check -h`, and the tool's own pages
`docs/SPEC-SWARM.md` and `docs/nova-swarm-quickstart.md`; then I used every living verb with its
real flags against scratch directories, a scratch slot store, a scratch native root and a fake
harness, the refusals too. Built from the card's base `8804eeba22ea` as
`nova-swarm v1.0.1-0.20261007155547-8804eeba22ea linux/amd64 go1.26.6`. No server and no member
loop was started: `member` answered only its help and its missing-flag refusals, and every other
verb ran against a scratch dir on a Linux bench. About 40 minutes of use; no code changed, and a
finding is recorded here and never fixed here.

## Findings

1. `verify --run-record` calls a run whose own record says it failed `RESULT OK`, exit 0.

       $ nova-swarm verify --result RESULT.md --contract 'RESULT: dogfood sha=abcdef123456' --label card1 --run-record exit.json
       RESULT OK "card1" line2="DONE"
       (exit 0; the receipt written beside it holds `exit_code=1`)

   `exit.json` is `{"rc":1,"ended":"2026-10-06T18:00:00Z","survivors":0}`. I expected a refusal or
   an abstain, because `verify -h` calls `--run-record` "the job's exit.json file: the harness's
   exit code joins the verdict", and its exit table says 1 is a result that does not hold its
   contract. The receipt proves the tool read `rc=1` and printed `OK` above it, so a caller
   trusting the exit status accepts a run the supervisor recorded as failed. Grade: URGENT.

2. `worker check` reports an unreadable description as drift, exits 1, and invents drift lines
   from a struct it never parsed.

       $ nova-swarm worker check bad.json          # bad.json is `{ not json`
       WORKER DRIFT worker: <abs>/bad.json is not a worker description this tool can read (invalid character 'n' looking for beginning of object key string); the fields are name, provider, model, ...
       WORKER DRIFT harness: harness is empty; it wants the harness command to run
       WORKER DRIFT harness_args: harness_args must carry {prompt} and {model}, ...
       (exit 1)

   `worker -h` says exit is "2 it cannot be read, or a bad invocation", and the tool's own first
   line says it cannot read the file. I expected exit 2 and no drift lines: `harness`,
   `harness_args` and `worker_dir` are declared only because the parse failed. A caller branching
   on 1 (read, drifts) against 2 (could not run) takes the wrong branch. Grade: URGENT.

3. `lint --base-check`'s `deadline-p95` refuses the second spelling its own remedy names.

       $ nova-swarm lint --card dl-phrase.md --typed --base-check --repo <repo> --legs legs.txt --p95 p95.txt
       LINT DRIFT card=dl-phrase.md deadline-p95: 1: no DEADLINE: line under the contract line remedy=DEADLINE: is at or above the measured p95 wall of the card's KIND, in seconds (or `finish within <n> minutes`), ...
       (exit 1)

   The card carries `Deadline: finish within 60 minutes.`, which the plain `deadline` rule accepts
   and the drift's own remedy names as an accepted spelling; the same card carrying
   `DEADLINE: 3600` lints `LINT OK`. I expected the check to accept the phrase, or the remedy not
   to name it. A stranger fixes the drift with the words the tool offered and is refused again.
   Grade: URGENT.

4. `disk-guard` prints `TRIMMED` for a cache it freed nothing from and left far over its cap.

       $ nova-swarm disk-guard --cache <scratch-cache> --cache-max-gb 1 --modcache-max-gb 1 ...
       TRIMMED go-build <scratch-cache> freed=1258291200 size=0 cap=1073741824
       TRIMMED go-build <home>/.cache/go-build freed=0 size=8299867040 cap=1073741824
       DISK-GUARD OK freed=2516582400 free=1936257937408

   The scratch cache was really trimmed to zero and reported; the shared cache stayed at 8.0 GiB
   against a 1 GiB cap and reported `TRIMMED` with `freed=0`, twice in a row (a dry run before it
   said `WOULD-TRIM ... freed=0`). The tool has the honest word — it prints `KEPT go-mod ...` when
   a cache is left alone — and used `TRIMMED` for an action that did not happen, so an operator
   reads the line as a trim that met the cap. Grade: URGENT.

5. `verify`'s receipt records the empty-string hash as the card's hash when no `--card` is given.

       $ nova-swarm verify --result RESULT.md --contract 'RESULT: dogfood sha=abcdef123456' --label card1
       RESULT OK "card1" line2="DONE"
       (exit 0; the receipt holds `card_sha256=e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855`)

   That value is the SHA-256 of the empty string. I expected `-` or no field when there is no card
   to hash; a 64-hex "card_sha256" for a card the tool never read is evidence that reads as if a
   card was checked. Grade: NEXT.

6. `verify --usage` accepts a file of any shape and silently records zeros.

       $ nova-swarm verify --result RESULT.md --contract 'RESULT: dogfood sha=abcdef123456' --label it --usage usage-bad.tsv
       RESULT OK "it" line2="DONE"
       (exit 0; `usage-bad.tsv` is `nonsense`, and the receipt holds `tokens_in=0 tokens_out=0 usd=-`)

   The same happened with a plausible file of the wrong columns, and the `-` (absence) in a real
   `native` usage.tsv became `tokens_in=0`. I expected the file to be refused with the columns it
   wants, or the zeros explained; no `-h` names the usage.tsv shape. Grade: NEXT.

7. `verify`'s signature scan reads `harness-output.log` beside the report, never the report's own
   words, and the abstain line does not name the file it read.

       $ nova-swarm verify --result RESULT.md --contract 'RESULT: dogfood sha=abcdef123456' --label card1
       RESULT OK "card1" line2="DONE"
       (exit 0; RESULT.md's body names `permission denied`)

   A file `harness-output.log` holding that same word beside the report gives
   `ABSTAIN "card1" reason=signature sig="permission denied" class=permission`, exit 1. I expected
   the report the worker wrote to be the place the signatures are found, or the abstain line to
   name the file it did read; as it stands a stranger cannot tell the report was read at all.
   Grade: NEXT.

8. `slots release` prints a success word on stdout while its refusal arrives on stderr with exit 2.

       $ nova-swarm slots release --store ./slots --owner ada --label held
       SLOTS RELEASED owner=ada released=0 held=1 live=1                    # stdout
       SLOTS KEPT owner=ada live=1: a lease whose holder is still running is not freed, ...   # stderr
       (exit 2)

   I expected one status word on the channel a caller reads — `SLOTS KEPT`, as
   `slots release -h` documents — because a script reading stdout sees `RELEASED` for a release
   that did not happen. Grade: NEXT.

9. `slots take --kind` multiplies the demand and the result line never says so.

       $ nova-swarm slots take --store ./slots --owner ada --n 1 --for 30m --kind schema --label c3
       SLOTS OK owner=ada granted=1 held=4 share=8 free=4

   I typed `--n 1` and read `held=4`; the weight appears only on a later `slots list` line
   (`kind=schema weight=4`). I expected the weight in the grant line (`n=1 kind=schema weight=4`)
   so the number can be checked against the card's kind. Grade: NEXT.

10. `slots` with an unknown subcommand does not name the subcommands there are.

        $ nova-swarm slots bogus
        nova-swarm slots REFUSED: unknown subcommand "bogus"; run: nova-swarm help slots

    I expected the five names, as the no-subcommand refusal prints them ("wants a subcommand:
    init ... take ... release ... list ... run ..."). The remedy points at `help slots`, but the
    unknown-name rule is that the answer names what there is. Grade: NEXT.

11. `install --dry-run` prints the unit as one escaped string, so the plan cannot be read.

        $ nova-swarm install disk-guard --dry-run --dir ./units --every 15m
        INSTALL DISK-GUARD DRY-RUN unit=./units/nova-swarm-disk-guard.service; nothing was written or loaded
        "[Unit]\nDescription=nova disk-guard: the machine's disk upkeep, one pass every --every (nova-swarm disk-guard)\nStartLimitIntervalSec=0\n..."

    I expected the unit's text as text, the way `disk-guard --dry-run` prints its plan, because
    `install -h` says `--dry-run` "print the unit and write and load nothing". A reader cannot see
    the `ExecStart`, `Restart` or log path they are about to install. Grade: NEXT.

12. `install` and `uninstall` accept the kind only before the flags; after a flag the same token
    is refused with a false reason.

        $ nova-swarm install --dry-run --dir ./units --every 15m disk-guard
        nova-swarm install REFUSED: takes no positional arguments, got 1: ["disk-guard"] (every input is a flag); run: nova-swarm help install

    I expected the documented call `nova-swarm install disk-guard --dry-run ...` to work with its
    inputs in any order, or at least a refusal that says the kind comes first. "takes no positional
    arguments" is false: the verb does take one, and its own usage and example show it. Grade: NEXT.

13. Two `step` refusals misname the verb as `nova-swarmstep`.

        $ nova-swarm step --card card.md --dir repo --no-wall
        nova-swarmstep REFUSED: the card has no script step, or has model steps too; the executor runs a card whose every work step is a script step; run: nova-swarm help step

    I expected `nova-swarm step REFUSED:` from the one grammar every other verb follows; the
    missing separator reads as a different tool's name and breaks a caller grepping for the verb.
    Grade: NEXT.

14. No verb accepts `--json`, though the tree's own standard promises every verb two renderings
    of one value.

        $ nova-swarm slots list --json --store ./slots
        nova-swarm slots list REFUSED: unknown flag --json; the flags of slots list are --store; run: nova-swarm help slots

    The same refusal came from `doctor`, `template`, `profile`, `worker check`, `disk-guard` and
    `version` ("takes no flags and no arguments"). Nothing in any `-h` names `--json` either, so a
    stranger cannot know whether the tool is line-only by design or missing the flag. Grade: NEXT.

15. `profile` answers `jobs=0` and a zero summary for a glob that matched no job and for one that
    matched job directories holding no timeline, and never says which.

        $ nova-swarm profile --jobs 'jobs/*'
        PROFILE SUMMARY jobs=0 mean_wall=0.0 clone=0.0 deps=0.0 read=0.0 edit=0.0 test=0.0 retry=0.0 result=0.0
        (exit 0)

    A `native` run here wrote no `timeline.tsv`, so the directories matched and read as zero; a
    glob matching nothing reads the same. I expected the two told apart with a NOTE. Grade: NEXT.

16. `native` admits a card with no `RESULT:` contract line and calls the run `OK`.

        $ nova-swarm native --harness ./opencode --model fake/model --card nocontract.md --slot ./root/slot5 --root ./root --deadline 30s --tokens unmetered --no-wall
        STAGE OK bench=<bench> repo= base= secs=0 clone=0.0 fetch=0.0 checkout=0.0
        NATIVE OK label=nc job=<root>/slot5/jobs/nc ... rc=0 ... harness=ok budget=unmetered ...
        (exit 0; the published RESULT.md is the harness's own text, not the card's contract)

    The card is `just a task with no contract\nFAKE-PWD\n`, and its published report's line 1 is
    not a contract. The quickstart says "Each card needs a RESULT contract as its first line; the
    published `RESULT.md` must repeat that contract on line 1", so I expected admission to refuse
    the card, or the publication to. Native never reads either. Grade: NEXT.

17. `doctor` and `native`'s pre-launch check compare the binary first on `PATH` with the one under
    the local directory, never the binary doing the invoking.

        $ ./bin/nova-swarm doctor        # this build is v1.0.1-0.2026...8804eeba22ea
        DOCTOR OK stamp=nova-swarm v1.2.0-rc1 linux/amd64 go1.27.1
        DOCTOR HARNESS kind=claude binary=- version=- login=-

    The installed pair agreed with itself while the binary I ran was a different build. I expected
    a NOTE naming the running build's difference from `PATH`'s, so a freshly built binary does not
    report an environment it is not part of as OK. Grade: NEXT.

READ 7/10 — the banner answers the three questions, every verb and subverb answers `-h` at exit 0,
the lint and disk-guard refusals are models (several problems at once, each with a remedy, the
unknown-flag answer names a nearest flag), and the slot and native lines carry real evidence; it
loses points for the `nova-swarmstep` verb name, the false "takes no positional arguments", the
`finish within` remedy the check refuses, `--json` silence across every verb, and the escaped
dry-run unit.

USE 6/10 — every primary path ran for real against scratch dirs (a slot store inited, taken,
listed, released and run under; a native card through a fake harness with and without the wall,
with a worker description, with `--sweep-now`; `disk-guard` dry and real passes; every template;
lint over real cards; `verify` over real reports), and the missing-flag refusals are honest and
complete; it loses points because `verify --run-record` returns OK over its own `exit_code=1`,
`worker check` calls an unreadable file a drift, `disk-guard` reports a trim it did not do, and
`profile` is silent about having read nothing.

urgent=4 next=13
