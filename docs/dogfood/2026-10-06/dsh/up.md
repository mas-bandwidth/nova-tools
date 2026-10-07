# nova-up dogfood — dsh (zhi), 2026-10-06

Read as a stranger: only `nova-up -h`, `nova-up help`, `nova-up <verb> -h` and its page under
`docs/` (`docs/SPEC-UP.md`), nothing else. Built from the base
`051068768266591538fb304ed6822e4d03b363d8` as
`nova-up v1.0.1-0.20261007144949-051068768266 linux/amd64 go1.26.6`.
Every verb ran with its real flags against scratch roots on a bench: `up` (real and `--dry-run`,
lines and `--json`), `version`, and the `help` door, with the refusals. The rules forbid starting
a server, so every real apply ran with `HOME` and `XDG_CONFIG_HOME` redirected into the scratch
and `DBUS_SESSION_BUS_ADDRESS` at a dead socket; each such run stops at the redis step before a
unit is written, and no service was started (checked after each run). No code changed; a finding
is recorded, never fixed here. In the quoted output, `$S` stands for the job's scratch
directory on the bench.

## Findings

1. `nova-up --local --root $S/work/root-apply` (a fresh root, full `PATH`)

   ```
   UP FAILED root=$S/work/root-apply steps=6 changes=4 applied=3: step redis: …/nova-secrets names --store $S/work/root-apply/secrets --as coordinator --max 0: exit status 2: SECRETS NAMES REFUSED: seat file coordinator.yaml is absent: store $S/work/root-apply/secrets holds no seat file yet; seal writes a seat's first value: run: nova-secrets seal …; the steps before it are applied, and a run again starts from what they left
   UP platform ok linux: loops are systemd user units
   UP dirs create $S/work/root-apply: . stores keys logs smoke
   ```

   The run applies `dirs`, `sprint` and `secrets` (`applied=3`), then the redis step re-plans and
   calls `nova-secrets names` on the very store the secrets step just made; the store has a key
   and `.sops.yaml` but no `coordinator.yaml` seat file, so the read refuses and the setup stops.
   A second run over what was left fails at the same line with `applied=0`, and a third does too;
   `seat` and `smoke` are never reached. Expected: the secrets step leaves a state its own next
   step can read (or the redis step seals the seat's first value itself, as its detail says,
   "apply seals what is not there"), the run ends `UP OK … applied=8`, and a second run says
   `UP UNCHANGED`. A stranger is handed the manual `nova-secrets seal …` command — the hand steps
   this tool exists to replace — and no flag skips or precedes the redis step. The tool cannot
   set up a fresh machine at all. (This is the same defect the codex dogfood recorded on
   `cb5fb8d4c329`; it is still present at this tip.)
   Grade: URGENT

2. The remedy finding 1 prints, followed to the letter

   ```
   SECRETS SEAL REFUSED: coordinator.yaml does not exist in the store; a new seat is given its first values by seat add, never by seal (SPEC-SECRETS rule 12); run: nova-secrets seal -h
   SECRETS SEAT ADD REFUSED: missing --from <source-seat> (a seat this machine can already open), --pub <age1…> (the new seat's public key, from its own keygen receipt), --sops <path>, --only <NAME,…> (seat add carries the values it is told to carry and no others); run: nova-secrets seat add -h
   SECRETS NAMES REFUSED: seat file coordinator.yaml is absent: … seal writes a seat's first value: run: nova-secrets seal …
   ```

   The command nova-up prints is
   `nova-secrets seal --store $S/work/root-apply/secrets --as coordinator --key <path> --sops <path> --name <NAME>`.
   `nova-secrets seal` refuses it and points to `seat add`; `seat add` then refuses because it
   needs `--from <source-seat>` "a seat this machine can already open", which a fresh machine
   does not have. The `<path>` placeholders are also a trap: read as the store's `.sops.yaml`
   the command answers `SECRETS SEAL REFUSED: sops binary $S/work/root-apply/secrets/.sops.yaml is absent or not executable; run: brew install sops` (on Linux, where `sops` is on `PATH`).
   Expected: a remedy that runs on the machine the run just described — a fresh one — in one
   turn. As printed, the refusal's recovery is impossible on that machine: it names a verb the
   named tool forbids, then a verb that needs a source seat that cannot exist yet.
   Grade: URGENT

3. `nova-up --local --dry-run --root $S/work/root-apply` then the same without `--dry-run`

   ```
   UP OK root=$S/work/root-apply steps=8 changes=3 applied=0 dry_run=true
   UP NOTE dry run: nothing written; run it without --dry-run to apply
   UP platform ok linux: loops are systemd user units
   …

   UP FAILED root=$S/work/root-apply steps=6 changes=1 applied=0: step redis: … SECRETS NAMES REFUSED: seat file coordinator.yaml is absent: …
   ```

   Both commands ran on the same root, at the same moment, from the same binary. The dry run
   reports `UP OK` at exit 0 and even continues to plan `seat create` and `smoke create`; the
   real run reports `UP FAILED` at exit 1 at the redis line the dry run printed as `change` with
   the same read error embedded in its detail. `up -h` says `--dry-run` "print what the verb
   would write"; the banner says it prints "the plan the real run would take, from the same code
   path". A dry run that says OK for a run that cannot proceed, and exits 0 while doing it, is a
   wrong result: it is exactly the read an AI uses to decide whether to run the real thing.
   Grade: URGENT

4. `nova-up --local --dry-run --root $S/work/notadir` then the same without `--dry-run`, where `notadir` is a regular file

   ```
   UP OK root=$S/work/notadir steps=8 changes=4 applied=0 dry_run=true
   UP NOTE dry run: nothing written; run it without --dry-run to apply
   UP platform ok linux: loops are systemd user units
   UP dirs ok $S/work/notadir
   …

   UP FAILED root=$S/work/notadir steps=5 changes=1 applied=0: step secrets: …/nova-secrets keygen …: exit status 2: SECRETS KEYGEN REFUSED: key directory $S/work/notadir/keys is absent; run: mkdir -m 700 -p $S/work/notadir/keys; …
   UP platform ok linux: loops are systemd user units
   UP dirs ok $S/work/notadir
   ```

   `--root` wants "the directory everything is written under", so a file at that path is knowable
   with no write. Instead the plan says `dirs ok`, `sprint ok` and the dry run calls the machine
   `UP OK` at exit 0; the real run then fails one step later with a remedy that cannot run
   (`mkdir …/keys` under a file). Expected: the plan refuses at the root, naming what `--root`
   wants, before claiming any step is satisfied. A false plan line plus a dead end.
   Grade: URGENT

5. `nova-up help --json` and `nova-up help up --json`

   ```
   usage: nova-up up [flags]
   from `nova-up help`:
     nova-up up -h
   ```

   Both print verb help as lines at exit 0; `--json` is accepted and silently dropped. The
   banner says "Every verb takes `--json`: the same result as one JSON object on stdout", and
   `version --json` and `up --json` honor it. Expected: `help --json` either renders the help as
   the one JSON value or refuses the flag naming what it wants; an AI that asks every verb for
   JSON gets prose from this one with no note.
   Grade: NEXT

6. `nova-up --local --dry-run --max 3 --root $S/work/root-max`, and the banner line that advertises it

   ```
   UP REFUSED: unknown flag --max; the flags of up are --dry-run, --json, --local, --root; run: nova-up up -h
   ```

   The banner says "A verb that lists takes `--max <n>` (default 20, 0 lists all) and says MORE
   for the rest", but no nova-up verb lists anything and `--max` is an unknown flag for the only
   verb. The line is inherited boilerplate that describes a shape this tool never has; a reader
   who reaches for the documented bound is refused. Expected: help that describes this tool
   only. A missing flag the help promises.
   Grade: NEXT

7. `nova-up` (no arguments) and `nova-up ./work/misc/dummy.txt` (an existing file)

   ```
   UP REFUSED: no verb and no file given; the verbs are up, version; run: nova-up help
   ```

   ```
   UP REFUSED: --local is required; it wants nothing after it: the mode that sets up this one machine with no config; run: nova-up help
   UP REFUSED: takes no positional arguments, got "./work/misc/dummy.txt"; the root is --root <dir>; run: nova-up help
   ```

   The first refusal tells the reader "a file is given by its path (./bogus)" as if nova-up
   accepts a plan file; passing an existing path proves there is no file input — it is refused as
   a positional argument, and neither the banner nor `up -h` documents one. Expected: a refusal
   that lists the doors that exist. The unknown-verb path (`nova-up help bogus`) repeats the
   same phantom file mode. Help that lies about an input.
   Grade: NEXT

8. `nova-up --local --dry-run --root '~/zhi-nova-try'`

   ```
   UP OK root=$S/~/zhi-nova-try steps=8 changes=6 applied=0 dry_run=true
   UP NOTE dry run: nothing written; run it without --dry-run to apply
   UP platform ok linux: loops are systemd user units
   ```

   A quoted or programmatic `~` is taken literally: the plan would make a directory named `~`
   under the caller's working directory. An AI caller passes arguments without a shell, so this
   is the normal way it would write `~/nova`. Expected: `--root` expands a leading `~` (or the
   help says it does not), rather than silently choosing a surprising path.
   Grade: NEXT

9. `nova-up help up version`

   ```
   UP REFUSED: --local is required; it wants nothing after it: the mode that sets up this one machine with no config; run: nova-up help
   UP REFUSED: takes no positional arguments, got "version"; the root is --root <dir>; run: nova-up help
   ```

   Asking for help dispatches the `up` verb's flag validation, so the two problems are reported
   under `UP` even though the door asked was `help`, and the answer is a pair of flag refusals
   instead of "help takes one verb". Expected: a help door that teaches `help` and names the one
   argument it wants. The same dispatch makes `nova-up help bogus` answer in the `UP` voice.
   Grade: NEXT

10. `nova-up up -h` beside `nova-up version -h`

    ``` usage: nova-up up [flags] from `nova-up help`: nova-up up -h ```

    ``` usage: nova-up version [flags] from `nova-up help`: nova-up version ```

    The `up` help's second line is the command just typed (`nova-up up -h`), a loop; `version`'s
    is its usage line, and the top-level banner's is `nova-up --local …`. Expected: the "from
    `nova-up help`" line points at the documented invocation, not at itself. Small, but a cold
    reader following it learns nothing.
    Grade: NEXT

11. `nova-up --local --dry-run --json --root $S/work/root-dryj`

    ``` {"result":{"verb":"up","status":"ok","exit":0},"facts":{"root":"…/root-dryj","steps":8,"changes":6,"applied":0,"dry_run":true},"notes":["dry run: nothing written; run it without --dry-run to apply"],"payload":"UP platform ok linux: …\nUP dirs create …\n…"} ```

    The eight step lines arrive as one escaped `payload` string, not as typed `items`, so a JSON
    consumer must re-parse the same prose the line rendering prints to learn which step is
    `create` and which is `missing`. Expected: the one result value the standard names, with the
    steps as bounded typed rows. Friction for the caller the tool is for.
    Grade: NEXT

READ 6/10: the banner states the steps, flags and exit table plainly and each verb's help is
complete enough to run every door, but it also promises a `--max` bound and a file input that no
verb accepts, `help` ignores `--json`, and `up -h`'s cross-reference points at itself.

USE 3/10: `version`, the flag refusals and the `--dry-run` plan of a fresh machine are
dependable, but a real fresh `--local` run cannot get past the redis step, its printed recovery
is refused by the very tool it names, and the dry run calls that same machine OK — only the
inspections can be trusted.

urgent=4 next=7
