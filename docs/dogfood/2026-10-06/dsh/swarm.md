# Dogfood: nova-swarm — 2026-10-06, dsh (zhi)

One friend, one tool, cold. I read only `nova-swarm -h`, `nova-swarm help`, every verb's
`-h`, the group and subverb helps of `slots` and `worker`, and the tool's own pages under
`docs/` (`SPEC-SWARM.md`, `nova-swarm-quickstart.md`), then used every verb at least once
with its real flags against scratch directories, a scratch slot store, a scratch native
root and a fake `opencode` harness. Built from the card's base
`051068768266591538fb304ed6822e4d03b363d8` and used as the staged tip,
`nova-swarm v1.0.1-0.20261007144949-051068768266 darwin/arm64 go1.26.6`. No server and no
member loop was started: `member` answered only its help and its refusals, and every other
verb ran against a scratch dir. 15–40 minutes of use; no code changed, and a finding is
recorded here and never fixed here.

## Findings

1. `verify` lets a run whose own record says it failed pass as `RESULT OK`.

       $ nova-swarm verify --result RESULT.md --contract 'RESULT: dogfood sha=abcdef123456' --label card1 --run-record rc1.json
       RESULT OK "card1" line2="LANDED"
       (exit 0; the receipt written beside it holds `exit_code=1`)

   `rc1.json` is `{"rc":1,"ended":"2026-10-06T18:00:00Z","survivors":0,"nonce":"abcdef123456"}`.
   I expected a refused or abstained verdict, because `verify -h` calls `--run-record` "the
   job's exit.json file: the harness's exit code joins the verdict", and the exit table says
   1 is "a verify whose contract line mismatched or whose run carries a failure signature".
   The receipt proves the code read `rc=1` and then printed `RESULT OK` above it; a caller
   trusting the exit status of `verify` accepts a card the supervisor recorded as failed.
   Grade: URGENT.

2. `worker check` reports an unreadable description as a drifting one, with drift lines
   invented from the zero value of a struct that was never parsed.

       $ nova-swarm worker check bad.json          # bad.json is `{ not json`
       WORKER DRIFT worker: <abs>/bad.json is not a worker description this tool can read (invalid character 'n' looking for beginning of object key string); the fields are name, provider, model, base_url, env_var, key_file, secret, usage, class, harness, harness_args, worker_dir, deadline, board, max_turns, max_cache_read, read_roots, input_limit_phrases, launch_grace
       WORKER DRIFT harness: harness is empty; it wants the harness command to run
       WORKER DRIFT harness_args: harness_args must carry {prompt} and {model}, so the prompt file and the model this description names both reach the harness
       (exit 1)

   I expected exit 2, which `worker -h` gives "it cannot be read", and no drift lines at
   all: `harness`, `harness_args` and `worker_dir` are declared only because the parse
   failed, so the tool manufactures three problems from a file it just said it could not
   read. A caller that branches on 1 (description read, drifts) against 2 (could not run)
   takes the wrong branch.
   Grade: URGENT.

3. No verb accepts `--json`, though the tool's own repository standard promises every verb
   two renderings of one value.

       $ nova-swarm lint --card card.md --json
       nova-swarm lint REFUSED: unknown flag --json; the flags of lint are --base-check, --card, --child-rules, --child-rules-file, --decide, --decide-answers, --decide-record, --fleet, --legs, --lineup, --max, --member-injects, --p95, --repo, --rules, --trust and 1 more; run: nova-swarm help lint

   The same refusal came from `version`, `doctor`, `template`, `profile`, `worker check`,
   `disk-guard`, `step`, `slots list` and `native`. Nothing in any `-h` names `--json`
   either, so a stranger cannot know whether the tool is line-only by design or missing the
   flag. I expected a machine-readable rendering of at least the inspections (`version`,
   `doctor`, `slots list`, `profile`, `worker check`), because the standard every command in
   this tree is held to says every verb accepts `--json` and builds one value with two
   renderings. Grade: NEXT.

4. `install` and `uninstall` accept the kind only before the flags; after a flag the same
   token is refused with a false reason.

       $ nova-swarm install --dry-run --dir ./units --every 15m disk-guard
       nova-swarm install REFUSED: takes no positional arguments, got 1: ["disk-guard"] (every input is a flag); run: nova-swarm help install

   I expected the documented call `nova-swarm install disk-guard --dry-run ...` to work with
   its arguments in any order, or at least a refusal that says the kind comes first. "takes
   no positional arguments" is false: the verb does take one, and its own usage and example
   show it. The same holds for `uninstall --dry-run --dir ./units disk-guard`. Grade: NEXT.

5. `install --dry-run` prints the unit as one escaped Go string, so the plan cannot be read.

       $ nova-swarm install disk-guard --dry-run --every 15m --dir ./no-unit-here
       INSTALL DISK-GUARD DRY-RUN unit=no-unit-here/nova-swarm.disk-guard.plist; nothing was written or loaded
       "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<!DOCTYPE plist PUBLIC \"-//Apple//DTD PLIST 1.0//EN\" \"http://www.apple.com/DTDs/PropertyList-1.0.dtd\">\n<plist version=\"1.0\">\n<dict>\n\t<key>Label</key>..."

   I expected the unit's text as text (or the unit named with the flags that define it), the
   way `disk-guard --dry-run` prints its plan, because `install -h` says `--dry-run` "print
   the unit and write and load nothing". A reader cannot see the `RunAtLoad`, `KeepAlive`,
   `ThrottleInterval` or the log path they are about to install. Grade: NEXT.

6. Two `step` refusals misname the verb as `nova-swarmstep`.

       $ nova-swarm step --card model.md --dir repo --no-wall
       nova-swarmstep REFUSED: the card has no script step, or has model steps too; the executor runs a card whose every work step is a script step; run: nova-swarm help step

   The same line came from `step --card nope.md --dir repo --no-wall`. I expected
   `nova-swarm step REFUSED:` from the one grammar every other verb follows; the missing
   separator reads as a different tool's name and breaks a caller grepping for the verb.
   Grade: NEXT.

7. `slots` with an unknown subcommand does not name the subcommands there are.

       $ nova-swarm slots bogus
       nova-swarm slots REFUSED: unknown subcommand "bogus"; run: nova-swarm help slots

   I expected the five names, as the no-subcommand refusal prints them: "wants a
   subcommand: init (make a store), take (grant leases), release (free them), list (print
   them) or run (execute under a lease)". The remedy points at `help slots`, but the
   unknown-verb rule is that the answer names what there is. Grade: NEXT.

8. `slots release --all` prints a success word on stdout while its refusal arrives on
   stderr with exit 2.

       $ nova-swarm slots release --store ./slots --owner zhi --all
       SLOTS RELEASED owner=zhi released=0 held=1 live=1          # stdout
       SLOTS KEPT owner=zhi live=1: a lease whose holder is still running is not freed, ...   # stderr
       (exit 2)

   I expected the refused operation to carry one status word on the channel a caller reads:
   `SLOTS KEPT`, as `slots release -h` documents ("a lease whose holder is still RUNNING is
   KEPT: SLOTS KEPT, live=<n>, exit 2"). A script reading stdout sees RELEASED for a release
   that did not happen. Grade: NEXT.

9. `verify`'s failure-signature scan never reads `RESULT.md`, only `harness-output.log`
   beside it.

       $ nova-swarm verify --result RESULT-sig.md --contract 'RESULT: dogfood sha=abcdef123456' --label card1
       RESULT OK "card1" line2="done"
       (exit 0; RESULT-sig.md's body holds `permission denied`)

   The same signature placed in `harness-output.log` beside a report gives
   `ABSTAIN "card1" reason=signature sig="permission denied" class=permission`, exit 1. I
   expected the documented signatures ("Known mechanical failure signatures are classified
   deterministically by `verify`") to be found in the report the worker wrote, because the
   report is where a stranger looks and the log is not named anywhere in the help or in the
   page. Grade: NEXT.

10. `native` admits a card with no `RESULT:` contract line and calls the run OK.

        $ nova-swarm native --harness ./opencode --model fake/model --card native-nocontract.md --slot ./root/slot3 --root ./root --deadline 30s --tokens unmetered --no-wall --identity zhi,Zhi,zhi@example.test
        STAGE OK bench=<bench> repo= base= secs=0 clone=0.0 fetch=0.0 checkout=0.0
        NATIVE OK label=native-nocontract job=.../slot3/jobs/native-nocontract ... rc=0 ... harness=ok budget=unmetered ...
        (exit 0)

    The card is `just a task with no contract\nFAKE-PWD\n`, and its published report is
    `pwd=...`, not the contract line. The quickstart says "Each card needs a RESULT contract
    as its first line; the published RESULT.md must repeat that contract on line 1", so I
    expected admission to refuse a card with no line 1 contract, or a refusal at the
    published result. Instead `native` never looks at either. Grade: NEXT.

11. `lint`'s unknown-flag refusal truncates the flag list and names no nearest flag.

        $ nova-swarm lint --bogusflag
        nova-swarm lint REFUSED: unknown flag --bogusflag; the flags of lint are --base-check, --card, --child-rules, --child-rules-file, --decide, --decide-answers, --decide-record, --fleet, --legs, --lineup, --max, --member-injects, --p95, --repo, --rules, --trust and 1 more; run: nova-swarm help lint

    I expected every flag, or the 16 named plus the missing one by name, and the same
    "did you mean" hint `disk-guard --bogus` prints ("did you mean --logs?"). "and 1 more"
    makes the reader run a second command to learn one word. Grade: NEXT.

12. `slots take --kind` multiplies the demand without saying so.

        $ nova-swarm slots take --store ./slots --owner zhi --n 1 --for 30m --kind schema --label c3
        SLOTS REFUSED owner=zhi want=4 held=1 share=2 free=3 holders=zhi:1 remedy="nova-swarm slots list --store ./slots"

    I typed `--n 1` and read `want=4`, and `slots take -h` only says `--kind <kind>` is "the
    card's kind, charged at its admission weight". I expected the weight in the line
    (`n=1 kind=schema weight=4`) so the number can be checked against the card's kind rather
    than taken on faith. Grade: NEXT.

13. `verify`'s contract-mismatch refusal carries no remedy and does not print the expected
    line.

        $ nova-swarm verify --result RESULT.md --contract 'RESULT: other sha=abcdef123456' --label card1
        RESULT REFUSED "card1" line 1 is "RESULT: dogfood sha=abcdef123456"
        (exit 1, stderr empty)

    I expected the refusal to name the expected contract line (the tool holds it) and a next
    action, the grammar every other refusal in the tool uses (`...; run: ...`). With only the
    found line, a reader must re-read their own command to see what was wanted.
    Grade: NEXT.

14. `worker check`'s drift lines carry no remedy.

        $ nova-swarm worker check worker.json          # the template, placeholders unfilled
        WORKER DRIFT harness: harness <the harness command on PATH> is neither on PATH nor a path that exists
        (exit 1)

    I expected a remedy beside the problem, as every `LINT DRIFT` line prints
    (`remedy=...`): which of the two fixes is wanted (put the harness on PATH, or give a path
    that exists) and what the field's unit is. A cold reader is told what is wrong and not
    what to do. Grade: NEXT.

READ 7/10 — the banner answers the three questions, every verb and subverb answers `-h` at
exit 0, the lint and disk-guard refusals are models (multiple problems at once, each with a
remedy, one with a nearest flag), and the slot and native lines carry real evidence; it
loses points for the `nova-swarmstep` verb name, the false "takes no positional arguments",
`--rules`/JSON silence in the help, and `install`'s escaped dry-run plan.

USE 6/10 — every primary path ran for real against scratch dirs (a slot store taken,
released and run under; a native card through a fake harness with and without the wall; a
script `step` that renamed and committed; `disk-guard --dry-run`; `profile`), and the
refusals are honest about missing flags; it loses points because `verify --run-record`
returns OK over its own `exit_code=1`, `worker check` calls an unreadable file a drift, no
verb offers `--json`, and `slots release` prints RELEASED on a kept lease.

urgent=2 next=12
