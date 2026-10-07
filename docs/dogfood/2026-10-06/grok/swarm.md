# Dogfood: nova-swarm — 2026-10-06, grok

Read cold as a stranger: only `nova-swarm -h`, `nova-swarm help`, every
`nova-swarm help <verb>` / `<verb> -h`, and the tool's page under `docs/`
(`docs/SPEC-SWARM.md`, `docs/CLI.md`'s `## nova-swarm` section and its
`### First run`). No source was read before or during the sitting. Built from
the checkout at `34db18e07dff90aba3d2f8dd4b699e72e969bec8` and used as
`nova-swarm v1.0.1-0.20261007150632-34db18e07dff linux/amd64 go1.26.6`: every
verb ran at least once with its real flags against scratch directories and a
scratch slot store, the refusals too — `template` (every name), `lint`
(`--rules`, `--card`, `--typed`, `--fleet`, `--decide`), `worker check`,
`profile`, `verify` (contract, capture signatures, run-record), `doctor`
(agree, drift, unreadable), `slots` (init/take/release/list/run and the share
refusals), `disk-guard` (`--dry-run` and a real pass), `install`/`uninstall`
(`--dry-run`), `step` (`--dry-run`, a real script step, `--remainder`), and
`native` end to end against a fake harness (result, nonzero exit, missing
harness, missing identity, numeric budget, `--sweep-now`). No server was
started and no member loop was run: `member` was exercised only through its
help and its missing-flag/flag refusals, which the card allows. No code was
changed; every finding below is recorded, never fixed here.

## Findings

1. `verify`'s help says it writes nothing, and every run writes a
   `RESULT.md.receipt`. — URGENT

       $ nova-swarm verify --result RESULT.md --contract 'RESULT: dogfood-x sha=abc123' --label job1
       RESULT OK "job1" line2="DONE"
       $ ls
       RESULT.md  RESULT.md.receipt

   `nova-swarm help verify` prints `effect: inspection: reads the job's RESULT.md and checks its line 1, writes nothing`, and the receipt appears on
   both the OK and the `RESULT REFUSED` path. `docs/SPEC-SWARM.md` (verb 3) says
   `verify` "writes a `.receipt` file", so the `-h` effect line is the one that
   is wrong: a reader who trusts "writes nothing" does not expect a file beside
   the result.

   **Expected:** the effect line says local write (a receipt), or no receipt is
   written. A tool's effect line is a contract; this one is false.

   **Grade:** URGENT

2. `worker check` calls a description with an absent, empty, `0644` or
   two-line `key_file` `WORKER OK`, and `native --worker` launches it to
   `NATIVE OK` with no key in the child — while the banner's exit table lists a
   key file that is absent or empty under exit 2. — URGENT

       $ nova-swarm worker check w_missing.json
       WORKER OK w model=deepseek-v4 provider=deepseek class=paid
       $ nova-swarm native --harness ./h_ok --model deepseek/deepseek-v4 --card cardA.md \
           --slot rA/slots/1 --root rA --deadline 30s --tokens unmetered --no-wall \
           --label key1 --identity owner,Name,owner@example.com --worker w_missing.json
       STAGE OK bench=<bench> repo=https://github.com/mas-bandwidth/nova-tools.git base=3baf154b secs=1 clone=0.8 fetch=0.0 checkout=0.1
       NATIVE NOTE catalog: no catalog at rA/catalog/models.json (a member refreshes it at its start); the harness fetches its own at this start
       NATIVE OK label=key1 job=rA/slots/1/jobs/key1 tmp=rA/slots/1/tmp/key1 rc=0 wall=0.01s sandbox=none-by-flag card_sha256=4f771857... budget=unmetered usage=none reason=no-store path=rA/slots/1/data/opencode/opencode.db

   The same `WORKER OK` came from a `0600` key file, a `0644` key file, an
   empty key file, a two-line key file and a missing one; `--env` changed
   nothing. A harness that dumped its own environment in the good case printed
   `DEEPSEEK_API_KEY=[UNSET]`, and the written `opencode.json` carried
   `"apiKey": "{env:DEEPSEEK_API_KEY}"` with no value anywhere, so the key the
   `key_file` names never reached the run. The banner says `2 could not run: ... a key file that is absent or empty ...` and `key_file is read as data, never sourced: one line, a bare key or NAME=<key>, mode 0600`.

   **Expected:** `worker check` reports the missing/empty/`0644`/multi-line key
   as a drift, and `native` refuses at exit 2 the way the banner says. A
   pre-flight check must not certify a description whose key cannot be read;
   that is a wrong result and a documented refusal that never fires.

   **Grade:** URGENT

3. Verb-level refusals drop the `REFUSED` status word and the `run:` remedy. —
   NEXT

       $ nova-swarm lint
       nova-swarm lint: --card is required; it wants the path to the card file whose shape is checked before any spend; ... refusing to guess
       $ nova-swarm template --name nope
       nova-swarm template: --name wants one of capacity, card, drift, fix, fix-card, models.tsv, probe-row, read, read-pr, replay, result, setup, text, tone, worker, got "nope"
       $ nova-swarm profile
       nova-swarm profile: --jobs is required; it wants a glob of job directories (or timeline.tsv files), ... refusing to guess

   `slots take --for bogus` (`nova-swarm slots take: --for wants a positive duration such as 30m, got "bogus"`), `slots list` without `--store`,
   `slots release` without `--label/--all`, `verify` without `--result`,
   `native --card nope.md` and `step --card` all use the same bare
   `nova-swarm <verb>: <want>` shape, while `doctor --bogus`,
   `worker check missing.json` and `slots list --store <missing>` lead with
   `REFUSED:` and end in `run:`.

   **Expected:** one refusal grammar, `VERB REFUSED: <reason>; run: <remedy>` (docs/STANDARD.md, "The status word leads every line"); a reader
   matching the grammar misses half of this tool's refusals.

   **Grade:** NEXT

4. No verb takes `--json`. — NEXT

       $ nova-swarm verify --result r.md --contract 'RESULT: sig sha=abc123' --label s1 --json
       nova-swarm verify REFUSED: unknown flag --json; the flags of verify are --card, --contract, --label, --max, --result, --run-record, --usage; run: nova-swarm help verify
       $ nova-swarm lint --card r.md --json
       nova-swarm lint REFUSED: unknown flag --json; ...

   `doctor`, `profile`, `slots list`, `worker check`, `template`, `install`,
   `uninstall` and `version` refuse it too; the word `--json` appears in no
   usage line. `docs/SPEC-SWARM.md`'s output grammar and the repository standard
   ("Every verb accepts `--json`", "One output structure, two renderings") say
   the typed line and the JSON rendering come from the same result value.

   **Expected:** one `--json` rendering of the same result value per verb; an
   AI that parses JSON everywhere else must not special-case nova-swarm.

   **Grade:** NEXT

5. `lint --rules` ignores `--max`. — NEXT

       $ nova-swarm lint --rules --max 3
       LINT RULE clone-step remedy=...
       LINT RULE deadline remedy=...
       LINT RULE deadline-p95 remedy=...
       ... (all 45 rule lines; no MORE line)

   `--max` is documented as "at most this many item lines, 0 for all", and the
   banner says "Every listing is a cap and a count ... one MORE line naming the
   remedy", but `--rules` printed all 45 rules under `--max 3` and under
   `--max 0`. `slots list` carries no `--max` at all.

   **Expected:** `--max 3` prints three rules and `MORE shown=3 total=45`; a
   listing with a cap is a contract, and a listing that silently ignores the cap
   is not bounded.

   **Grade:** NEXT

6. `step`'s refusals print the program name glued to the verb. — NEXT

       $ nova-swarm step --card missing.md --dir checkout
       nova-swarmstep REFUSED: --card wants a readable card file: open missing.md: no such file or directory; run: nova-swarm help step
       $ nova-swarm step --card plain.md --dir checkout --dry-run
       nova-swarmstep REFUSED: the card has no script step, or has model steps too; ... run: nova-swarm help step

   Every other verb writes `nova-swarm <verb> REFUSED:`, so a reader or a log
   matcher keying on the exact program name sees a different one here.

   **Expected:** `nova-swarm step REFUSED: ...`.

   **Grade:** NEXT

7. A failed `step` POST prints `exit status 125:` with an empty reason and no
   hint that the wall refused the command. — NEXT

       $ nova-swarm step --card card.md --dir checkout --work work --result RESULT.md
       STEP FAILED step 1: broken - post: exit0 grep -q '^hello ' base.txt: exit status 125:
       $ cat checkout/base.txt
       hello base

   The regex step rewrote `base.txt` as intended, then the POST's `grep` exited
   125 (nova-sandbox's "could not run") and the line ended at a bare colon with
   nothing after it. `docs/SPEC-SWARM.md` names a POST command "a command that
   is no interpreter"; nothing says which commands the wall can execute.

   **Expected:** the failure line says the wall refused the command (the
   sandbox's own words), not an empty `exit status 125:`. An empty reason is an
   unreadable statement.

   **Grade:** NEXT

8. `doctor --local <missing>` returns `DOCTOR OK`, while `--path <missing>`
   returns `DOCTOR UNREADABLE`. — NEXT

       $ nova-swarm doctor --path /path/to/nova-swarm --local ./missing-binary
       DOCTOR OK stamp=nova-swarm v1.0.1-0.20261007150632-34db18e07dff linux/amd64 go1.26.6
       $ nova-swarm doctor --path ./missing-binary --local /path/to/nova-swarm
       DOCTOR UNREADABLE reading the version of path=./missing-binary: not found; the other binary, ... reported stamp=...; run `./missing-binary version` by hand ...

   A `--local` file that was explicitly named and cannot be read is silently
   treated as "no local copy", and the line does not say so.

   **Expected:** the same rule for both sides: the named file is read, and a
   named file that is absent says so (or a NOTE that the local copy is not
   installed). The asymmetry hides a typo in `--local`.

   **Grade:** NEXT

9. `install <kind> --dry-run` prints the unit as a Go-quoted one-line string. —
   NEXT

       $ nova-swarm install disk-guard --dry-run --every 15m --dir ./units
       INSTALL DISK-GUARD DRY-RUN unit=./units/nova-swarm-disk-guard.service; nothing was written or loaded
       "[Unit]\nDescription=nova disk-guard: the machine's disk upkeep, one pass every --every (nova-swarm disk-guard)\nStartLimitIntervalSec=0\n\n[Service]\nExecStart=\"...\" \"disk-guard\"\nRestart=always\nRestartSec=900\n\n[Install]\nWantedBy=default.target\n"

   The example is `nova-swarm install disk-guard --dry-run --every 15m --dir ./no-unit-here`, whose point is to show the unit before writing it; a
   `\n`-escaped quoted blob cannot be read as a unit or copied.

   **Expected:** the dry run prints the unit as its real multi-line text (it
   writes nothing either way).

   **Grade:** NEXT

10. `slots take --kind bogus` is accepted and charged as a read. — NEXT

        $ nova-swarm slots take --store S --owner zhi --n 1 --for 30m --kind bogus --label b1
        SLOTS OK owner=zhi granted=1 held=2 share=4 free=2
        $ nova-swarm slots list --store S
        SLOT zhi-...-b8cbcd1b owner=zhi pid=... label=b1 until=... state=live kind=bogus stranded=1

   `docs/SPEC-SWARM.md` "Bench slot leases" gives the weights: `schema` and
   `fix-red` weigh 4, `read` weighs 1, and "a schema card is refused at take
   when the remaining share fits only a read". An unknown `--kind` silently
   takes the read weight, so a misspelled `schema` admission is granted where
   the documented card should be refused.

   **Expected:** an unknown `--kind` is refused with the known kinds named, or
   the help names the weights. Nothing hidden and no default that stands in for
   a missing input.

   **Grade:** NEXT

11. A deadline under the 5 s `--usage-interval` default is refused with only
    the help as the remedy. — NEXT

        $ nova-swarm native --harness ./h_sleep --model provider/model --card cardA.md \
            --slot r3/slots/1 --root r3 --deadline 2s --tokens unmetered --no-wall --label slow1
        nova-swarm native: --usage-interval is shorter than --deadline, got 5s against a deadline of 2s; at or past the deadline no sample would ever run and the budget could not fire; run: nova-swarm native -h

    The refusal names the conflict, but the remedy is the whole help page; the
    flag that fixes it (`--usage-interval 1s`) is not on the line, though the
    help lists a minimum of `1s`.

    **Expected:** `run: nova-swarm native --usage-interval 1s ...`, a paste, not
    a search. Recovery in one turn is the standard.

    **Grade:** NEXT

## What the tool got right

- The first run works with no setup: `template --name read-pr`, `template --name worker` and `lint --rules` run as printed from an empty directory, and
  `lint --rules` prints 45 checks each with its remedy.
- `native` takes a card end to end with no store: it staged from the card's
  `REPO:`/`BASE:`, took a job lease, wrote `harness-output.log`, published
  `RESULT.md`, `usage.tsv` and `report` under `--results-root`, and its
  `NATIVE OK`/`NATIVE INCOMPLETE`/`why=` line matched the child's rc
  (`rc=3` gave `NATIVE INCOMPLETE ... why=rc`, exit 3); `--sweep-now` deleted
  the job directory only after publishing.
- `verify` does scan the run's `harness-output.log` beside the result: each of
  the six documented signatures produced `ABSTAIN ... reason=signature sig="..." class=...` at exit 1, a contract mismatch produced `RESULT REFUSED`
  at exit 1, and a clean capture produced `RESULT OK` at exit 0.
- `doctor` compared two real binaries and refused a shadowing one with the
  drift lines and the paste-ready remedy, exit 2; `slots` refused an over-share
  take with the holder list and a remedy; `disk-guard --dry-run` judged with no
  writes and a real pass ended `DISK-GUARD OK`; `install mirror-refresh` was
  refused because no mirror verb exists; `step --remainder` refused a
  non-40-hex `--land`.
- `lint` holds real defects: a one-line card produced eight `LINT DRIFT` lines
  with remedies, `--typed` named each missing typed header, and `--fleet`
  caught a bash-4 `[[ ]]` and two unquoted expansions.

READ 7/10 — the banner answers what it does, how it works in five lines and
where its state lives, `help <verb>` carries flags, effect, example and exit
codes, and every example printed runs; the false `verify` effect, the missing
key-file refusal the banner promises, the half-refusals without `REFUSED`, and
the absent `--json` keep it off a 10.

USE 6/10 — a stranger can lint a card, print a template, lease a slot store,
run `disk-guard`, and drive one card through `native` with only a harness and a
scratch root; the `key_file` credential path silently delivers nothing, the key
file is never checked, `--json` is absent everywhere, and a failed `step` POST
reports an empty reason.

urgent=2 next=9
