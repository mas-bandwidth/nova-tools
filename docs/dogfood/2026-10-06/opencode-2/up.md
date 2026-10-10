# nova-up dogfood — opencode-2, 2026-10-06

Read as a stranger: only `nova-up -h`, `nova-up help`, `nova-up help <verb>`, `nova-up <verb> -h`
for every verb, and the tool's page under `docs/` (`docs/SPEC-UP.md`), nothing else. The binary was
built in the staged checkout on a Linux bench with
`go build -o $JOB/bin/nova-up ./cmd/nova-up`, never the installed binary, from the staged commit
`ba68746a3f763a4f22b777bde4edbb15e95a2628`, and the version line it printed is
`nova-up v1.0.1-0.20261010034258-ba68746a3f76 linux/amd64 go1.26.6`. Every verb ran with its real
flags against scratch roots under `$S`: `up` (real and `--dry-run`, lines and `--json`), `version`,
and the `help` door, with the refusals. The card's rules forbid starting a server, so every real
apply stopped at the redis step or earlier; no unit was written and no process started (checked
after each run). This pass carries attempt 6 `b8f4ae944a4560858ed12d34183f9ced5fede263` onto the
current tip and re-verifies its findings; the help door, `version` and every refusal reproduced
verbatim here, and each `--root` and plan defect below was re-checked against the step lines a
`--dry-run` prints on this bench. In the quoted output `$S` is the bench scratch directory, `$JOB`
the job directory, `<bin>` the directory the tools are installed in, and `<cwd>` the directory a
command ran from.

## Findings

1. `nova-up up --local --dry-run --root $S/misc/afile`
   Printed:
   ```
   UP OK root=$S/misc/afile steps=9 changes=4 applied=0 dry_run=true
   UP NOTE dry run: nothing written; run it without --dry-run to apply
   UP platform ok linux: loops are systemd user units
   ```
   I expected `dirs change` or a refusal: `$S/misc/afile` is an existing regular file, not a
   directory, so `stores/`, `keys/`, `logs/` and `smoke/` do not stand and the sprint's twin cannot
   be `ok`; the plan reads only that the path exists and says `UP OK` with `changes=4` for a run
   that cannot work.
   Grade: URGENT (a dry run that says ok for a run that cannot work)

2. `HOME=$S/home nova-up up --local --dry-run --root ""`
   Printed:
   ```
   UP OK root=$S/home/nova steps=9 changes=7 applied=0 dry_run=true
   UP NOTE dry run: nothing written; run it without --dry-run to apply
   UP platform ok linux: loops are systemd user units
   ```
   I expected a refusal naming `--root`'s value: an explicitly empty value is a missing input, not
   the default, and `up -h` says the default `~/nova` is for `--root` being absent. Instead the
   empty value is discarded and the setting is silently `~/nova`, which a caller passing an unset
   variable (`--root "$ROOT"`) sees only in the summary line, or after a run without `--dry-run`
   has written there.
   Grade: URGENT (a default that stands in for a missing input, so the one root the write verb
   touches can silently be an unintended one)

3. `nova-up up --local --dry-run --root "~/nova-alt"`
   Printed:
   ```
   UP OK root=<cwd>/~/nova-alt steps=9 changes=6 applied=0 dry_run=true
   UP NOTE dry run: nothing written; run it without --dry-run to apply
   UP platform ok linux: loops are systemd user units
   ```
   I expected `~` to be expanded the way the default `~/nova` is, or the help to say that a `~` in
   the value is literal; a quoted or scripted `--root ~/nova-alt` silently targets `./~/nova-alt`.
   Grade: NEXT (unclear help; a surprising path is chosen silently)

4. `nova-up up --local --dry-run --root $S/tryj --json`
   Printed:
   ```
   {"result":{"verb":"up","status":"ok","exit":0},"facts":{"root":"$S/tryj","steps":9,"changes":6,"applied":0,"dry_run":true},"notes":["dry run: nothing written; run it without --dry-run to apply"],"payload":"UP platform ok linux: loops are systemd user units\nUP dirs create $S/tryj: . stores keys logs smoke\nUP binaries ok 8 on PATH, each answering its version\nUP sprint create mem:$S/tryj/stores/sprint.twin\nUP secrets create $S/tryj/secrets seat=coordinator\nUP ssh ok ssh configured\nUP redis create 127.0.0.1:6390 loop=redis-local for nova-bus: unit create, passwords to seal=4, acl apply, fn load\nUP seat create $S/tryj/seat.env seat=coordinator\nUP smoke create one card on $S/tryj/smoke/sprint.twin beside the throwaway repository $S/tryj/smoke/repo"}
   (one line printed)
   ```
   I expected the per-step lines as typed rows from the one value (`items`, each with step, status
   and detail), per the one-value-two-renderings rule; a JSON caller has to split `payload` on
   newlines and re-parse the `UP <step> <status> <detail>` text it already had in the line
   rendering.
   Grade: NEXT (friction for the one caller the JSON rendering is for)

5. `nova-up --json`
   Printed:
   ```
   {"result":{"verb":"up","status":"refused","exit":2,"remedy":"nova-up help","why":["--local is required; it wants nothing after it: the mode that sets up this one machine with no config"]},"facts":{}}
   (one line printed)
   ```
   I expected the flag-only invocation to be diagnosed like the bare one, as "no verb given": the
   result claims `verb: up` and that `--local` is required, so adding only `--json` turns a "no
   verb" into a "`up` without `--local`" and points a reader at a mode they did not ask for.
   Grade: NEXT (the two renderings of one result drift)

6. `nova-up up --local --root $S/misc/afile`
   Printed:
   ```
   UP FAILED root=$S/misc/afile steps=5 changes=1 applied=0: step secrets: <bin>/nova-secrets keygen --as coordinator --key $S/misc/afile/keys/coordinator.key --age-keygen <bin>/age-keygen: exit status 2: SECRETS KEYGEN REFUSED: key directory $S/misc/afile/keys is absent; run: mkdir -m 700 -p $S/misc/afile/keys; the steps before it are applied, and a run again starts from what they left
   UP platform ok linux: loops are systemd user units
   UP dirs ok $S/misc/afile
   ```
   I expected the run to refuse the file root at the dirs step it already planned `ok`, not to
   blame a missing `keys` directory: the remedy `mkdir -m 700 -p $S/misc/afile/keys` cannot run
   while the root is a file, so the refusal names no action a reader can take, and the closing
   sentence disagrees with its own count (`applied=0` beside "the steps before it are applied").
   Grade: URGENT (a refusal whose remedy cannot run, and a failure line that disagrees with its own count)

7. `nova-up help`
   Printed:
   ```
   nova-up: sets up nova on one machine, from nothing to a first sprint

   how it works: steps in order: platform, dirs, binaries, sprint, secrets, redis, seat, smoke.
   ```
   I expected the usage, lower in the page, to name the verb the refusals name ("the verbs are up,
   version") and that the example runs (`nova-up up -h`); the usage block reads `nova-up --local
   [--root <dir>] [--dry-run] [--json]`, `nova-up version`, `nova-up help [<verb>]`, so `up` and
   `--local` are one mode but the door's own list omits `up`, and a reader cannot tell whether the
   tool is `nova-up --local` or `nova-up up --local` (both run).
   Grade: NEXT (unclear help: the usage hides a verb the refusals name)

8. `nova-up -v`
   Printed:
   ```
   UP REFUSED: unknown flag --v; the flags of up are --dry-run, --json, --local, --root; run: nova-up up -h
   (one line printed)
   ```
   I expected the refusal to quote the token the reader typed (`-v`), or to say short flags are
   not supported; `--v` was never typed and is not a name to search the help for.
   Grade: NEXT (an unclear refusal that invents the token it refuses)

9. `nova-up up --local --dry-run --root /proc/nova-nope`
   Printed:
   ```
   UP OK root=/proc/nova-nope steps=9 changes=6 applied=0 dry_run=true
   UP NOTE dry run: nothing written; run it without --dry-run to apply
   UP platform ok linux: loops are systemd user units
   ```
   I expected the plan to see that the parent is not writable, so the root cannot be created; a
   dry run that says `UP OK` and `changes=6` for a root it cannot create sends the reader past the
   one thing the plan is for.
   Grade: NEXT (the plan is not the plan the real run takes)

## What held

- `version` (lines and `--json`), the `help` door, and every flag refusal ran clean at exit 0 or 2,
  and the missing-program stop writes nothing: `nova-up up --local --dry-run --root $S/try` with a
  PATH that holds none of the tools prints `UP FAILED ... missing: binaries; nothing was applied;
  install what the missing line names and run again` and makes no directory.
- The full real `up --local` success path (`UP UNCHANGED`, a second run all `ok`, `seat.env`, the
  smoke card landed) was not run: the card forbids starting a server, so every real apply stopped
  at the redis step or earlier, and the runs left no unit file and no process (checked after each).

READ 6/10 — the banner answers what it does, how it works and where its state lives, every door exits 0 with a page complete enough to run the tool, and the `example:` lines run; the score is held down by a `--root` contract that is not true in three ways (an empty value defaults, a `~` is literal, a file root is planned `ok`), a usage block that hides the `up` verb the refusals name, and a `--json` body that hides the steps.

USE 6/10 — the plan-then-apply shape is trustworthy on a fresh root and the missing-program stop writes nothing, but the write verb's one root can silently point somewhere else, a failed apply's remedy cannot run and its last sentence disagrees with its own count, and the JSON a caller would depend on is one opaque string.

urgent=3 next=6
