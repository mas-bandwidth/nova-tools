# Dogfood: nova-swarm — 2026-10-06, codex

One friend, one tool, cold. I read only `nova-swarm -h`, `nova-swarm help`,
every verb's `-h`, and its page under docs/ (nova-swarm-quickstart.md and the
CLI.md#nova-swarm section it links), then used every verb at least once with
its real flags against a scratch root on a Linux bench over ssh — no server,
no member loop, nothing started on the working machine. Refusals included.
The binary is built from this checkout at df30ce088; `native` was driven
through the repository's own fake harness (the one CLI.md#nova-swarm names as
demonstrating the result and output contract without a live provider), with a
scratch bare repository as the card's REPO and a scratch slot under a scratch
root. About 25 minutes of use.

One event outside the tool, recorded for whoever reads the gate times: the
job's staged checkout on the working machine was reaped twice mid-run by other
machinery (the whole tree of the card disappeared); it was re-staged from the
bench mirror at the same commit with no work lost. Nothing below is a finding
about nova-swarm.

## Findings

1. `nova-swarm native --harness <bench>/fakeharness --model fake/fake-model --card card.md --slot slots/1 --root root --deadline 2m --tokens unmetered --label dogfood1 --identity demo,demo,demo@example.invalid --sandbox ./bin/nova-sandbox`

       NATIVE NOTE catalog: no catalog at <root>/catalog/models.json (a member refreshes it at its start); the harness fetches its own at this start
       NATIVE REFUSED: the child could not be started: fork/exec ./bin/nova-sandbox: no such file or directory; run: nova-swarm native -h

   Expected: a refusal naming the want, the way the missing `--model` one
   does. `--sandbox`'s help says only "the nova-sandbox binary path"; the
   wall runs from another working directory, so a relative path cannot resolve,
   and the raw fork/exec line leaves a stranger to discover that alone. The
   absolute path runs. Grade: NEXT.

2. `nova-swarm step --card step-card.md --dir checkout --sandbox <bench>/nova-sandbox`

       STEP FAILED step 1: broken - program: exit status 125:

   Expected: the why after that last colon. `nova-sandbox -h` says 125
   means the wall refused to start the command and "a SANDBOX REFUSED line on
   stderr says why" — that line exists and `step` drops it. The same program
   passes under `--no-wall`, and `step -h` names no flag that widens the
   wall's readable roots (`native` has `--repo`), so the only remedy a
   stranger can act on is turning the wall off. Grade: NEXT.

3. The dogfood2 run of `native` (the child exited 0; the published
   RESULT.md's line 1 was `# a fake task`, not the card's `RESULT:
   dogfood-native-2 sha=...`)

       NATIVE OK label=dogfood2 job=... rc=0 wall=0.12s sandbox=landlock ... budget=300+/500 spend=input:100,output:200,requests:1,max_prompt:100,cost:0.01,model:fake/fake-model

   Expected, from the quickstart's "the published RESULT.md must repeat that
   contract on line 1 and put its disposition on line 2": the runner holds
   the child to it. It holds presence only — a missing RESULT.md prints
   `NATIVE INCOMPLETE ... why=no-result` — and the contract itself is
   `verify`'s check, a verb the quickstart never names; a standalone
   `native` caller reads OK for a contract-breaking child. Grade: NEXT.

4. `nova-swarm member --as m1 --server 127.0.0.1:1 --harness <bench>/fakeharness --root member-root --once`

       nova-swarm member: tick 1: queue: exit 2: the sprint server at 127.0.0.1:1 did not answer: Post "http://127.0.0.1:1/verbs": dial tcp 127.0.0.1:1: connect: connection refused
       tick 1 acted=0 running=0 ...
       MEMBER OK as=m1 ticks=1

   Expected: a dead server on the only pass to be a failing exit. The exit
   table's letter ("0 it stopped as asked") is met and each failure is named
   on its own line, but a caller reading the exit code alone sees success for
   a pass in which every server call failed. Grade: NEXT.

5. `nova-swarm slots run --store store --owner ada --n 1 --for 1m --label runner -- bash -c "exit 3"`

       (no line at all; the child's exit 3 comes back as the exit code)

   Expected: a SLOTS line naming the lease the verb takes, holds and frees —
   the set's own grammar ("the status word leads every line") and what the
   refused path already prints (`SLOTS REFUSED owner=... holders=...
   remedy=...`); only the success path is silent. Grade: NEXT.

6. `nova-swarm slots init --store store --owner bob --capacity 4 --share 2` (after `slots take --owner bob` was refused with share=0)

       SLOTS REFUSED reason=store_exists store=store: store/shares.tsv is already there and init never overwrites a store, because the leases under it are other processes'; edit shares.tsv by hand to change a capacity, a reserve or a share; run: nova-swarm slots -h

   Expected: a way to add a second owner without a hand edit — the take
   refusal names none (its remedy is `slots list`, which shows what is held,
   not how to grow it). `SLOTS INIT OK` prints `reserve=0` and this refusal
   names "a reserve" as a column, but no flag of `slots init` documents a
   reserve at all. Grade: NEXT.

7. `nova-swarm disk-guard --root member1 --cache cache-a --cache-max-gb 0.5 --logs loops --land land --mirrors mirrors --disk-floor 5 --dry-run`

       nova-swarm disk-guard REFUSED: invalid value for --cache-max-gb: it wants the GiB each Go build cache is held under (default 20); run: nova-swarm help disk-guard

   Expected: the constraint named. Whole values 1 and 2 are accepted, 0.5
   and 0.0001 are refused, and neither the flag help ("the GiB each Go
   build cache is held under") nor the refusal says which class of value it
   wants. Grade: NEXT.

8. `nova-swarm profile --jobs 'nope/*'`

       PROFILE SUMMARY jobs=0 mean_wall=0.0 clone=0.0 deps=0.0 read=0.0 edit=0.0 test=0.0 retry=0.0 result=0.0

   Expected: a glob that matches nothing to be answered, the way an unknown
   verb is ("the verbs are ..."), not summarized as a green pass over zero
   jobs; a mistyped path reads as success. Grade: NEXT.

9. `nova-swarm step --card step-card.md --dir checkout --dry-run` (after writing the card by the `script-step` rule)

       STEP PLAN step=1 lang=go paths=README.md posts=1 wall=<bench>/nova-sandbox

   Expected: `step -h` to say where a script-step card's shape is defined.
   It says only "a card whose every work step is a script step"; the shape
   (SCRIPT:, the fenced program, POST:, COMMIT:, VERDICT:) is stated in
   `lint --rules`' script-step rule, and the program's built path
   (`<work>/bin/step-<n>/step`) is stated nowhere — a first POST that
   guesses a source path fails on a file that does not exist. Grade: NEXT.

## What worked (no finding, kept short)

Every verb answers `-h` at 0; the bare command and an unknown verb each
name the door, the whole verb list and a starting verb in one line. `lint`
reports every drift at once, each with its remedy (45 checks green on a
card built by following them); `template`, `--rules` and `--fleet` print
what they promise. `native` refused three times in a row — nothing staged, no
pool identity, no wall — each line naming the next command, and following
them landed a green run with the wall up (landlock), live token accounting
from the harness's own database (`budget=300+/500`, `cost:0.01`), a
per-turn timeline, and results published under the root with the report and
usage.tsv; `verify` refused with the exact line 1 named, wrote the receipt on
the OK path, and named both missing flags in one run; `doctor` matched its
docs table exactly (OK, DRIFT, REFUSED with the copy-or-PATH remedy);
`install`/`uninstall` dry-runs print the unit and load nothing; the
`mirror-refresh` refusal is exactly as documented; `disk-guard --dry-run`
judged the login's own cache and removed nothing; `slots init`, `take`,
`list` and `release` keep their holders and remedies.

## Gate

    go test -count=1 -timeout 600s ./internal/docs ./internal/ci

    ok  	github.com/mas-bandwidth/nova-tools/internal/docs	2.021s
    ok  	github.com/mas-bandwidth/nova-tools/internal/ci	11.336s

READ 8/10 — the banner, every verb's -h and the refusal grammar answer a cold
reader fast and truly; the script-step shape lives only in lint --rules, one
flag's constraint is unstated, and the quickstart's RESULT-contract sentence
names no enforcer.
USE 7/10 — every verb ran for real including the refusals, and native ran
green end to end with the wall, live accounting and published results; a
silent slots run success path, an empty why on a walled step failure and
member --once reading OK against a dead server are what it costs.

urgent=0 next=9
