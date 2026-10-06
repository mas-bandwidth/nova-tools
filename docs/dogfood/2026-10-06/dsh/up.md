# nova-up dogfood — Johnny Grok, 2026-10-06

Reviewer: Johnny Grok.

Read as a stranger: `nova-up -h`, `nova-up help`, `nova-up <verb> -h`, and the nova-up page under `docs/` (`docs/SPEC-UP.md`, and the nova-up section of `docs/SETUP.md`, which points at that page). Binary built on hetzner from `cb5fb8d4c3290f710d22a21a86aa8d229e4db905`: `nova-up v0.0.0-20261006204015-cb5fb8d4c329 linux/amd64 go1.26.6`. Before this note was committed, `origin/sprint/mechanical-2026-10-02` moved to `d762f545478f8c5118ed9342f69fd23d8b2d5042`. `cmd/nova-up/main.go`, `internal/up`, `docs/SPEC-UP.md` and `docs/SETUP.md` are the same blobs at both commits. The installed `nova-up v1.2.0-dev.d165b531` was not used: its build info says `vcs.modified=true`.

Working directory: `~/rowan-working/friends/johnny/jobs/dogfood-dsh-up-b.w1~15.g3/scratch/work`. `HOME` was the scratch home beside it, so a default `~/nova` was that home's `nova`, not `/home/nova/nova`. Every command below is `nova-up` with that binary first on `PATH`.

Verbs are `up` and `version`. Both ran. `up` ran as `nova-up up --local` and as the banner's `nova-up --local`, with `--dry-run`, `--json` and `--root`. Refusals ran too: no verb, an unknown verb, an unknown flag, a missing `--local`, a missing program, an empty `--root`, two `--root` flags, and a `--root` that is a file.

Not done: a full apply. `up -h` says apply writes a unit file and starts it, and SPEC-UP says that unit runs `nova-redis serve --bind 127.0.0.1 --port 6390`. This card does not start a server and does not connect to 6390. No listener was opened on 6380, 6381 or 6390, nothing was pointed at 100.76.29.55, and `nova-loop-redis-local.service` was not created. A missing `redis-server` and a missing `age-keygen` each exited 1 with `applied=0` and created no directory. One apply was started against a scratch root and stopped by a safety wrapper at `nova-sprint init`, after the dirs step had created the root (`keys` mode 0700). That wrapper line is not a finding.

## Findings

1. `nova-up up --local --dry-run --root ./not-a-dir`

`./not-a-dir` is a 2-byte text file. Printed (stdout, exit 0), first 3 lines:

```
UP OK root=/home/nova/rowan-working/friends/johnny/jobs/dogfood-dsh-up-b.w1~15.g3/scratch/work/not-a-dir steps=8 changes=4 applied=0 dry_run=true
UP NOTE dry run: nothing written; run it without --dry-run to apply
UP platform ok linux: loops are systemd user units
```

The next lines include `UP dirs ok` of that same path, and `UP sprint ok mem:<that path>/stores/sprint.twin`. `ls` of the twin says `Not a directory`. An empty directory at `./empty-root`, same flags, prints `UP dirs create ...: stores keys logs smoke`, not `ok`. I expected a refusal that `--root` is a file. SPEC-UP says `ok` means already as the step wants it, and the dirs step wants the root and `stores/`, `keys/`, `logs/` and `smoke/`. A file is none of those, and the sprint twin is not there. Secrets and redis on this run say `change`, not a refusal. Dry-run wrote nothing. I did not apply it.

Grade: URGENT.

2. `nova-up help --json`

Printed (stdout, exit 0), first 3 lines:

```
usage: nova-up up [flags]
from `nova-up help`:
  nova-up up -h
```

I expected one JSON object. The banner says every verb takes `--json` and that the result is one JSON object on stdout. `help` is a verb. This is `up`'s text help, the same page as `nova-up up -h`, and it is not JSON. `nova-up version --json` and `nova-up up --local --dry-run --json --root ./nova-try-json` are JSON, so the flag works on those two verbs.

Grade: URGENT.

3. `nova-up help --json version`

Printed (stdout, exit 2), the only line:

```
{"result":{"verb":"up","status":"refused","exit":2,"remedy":"nova-up help","why":["--local is required; it wants nothing after it: the mode that sets up this one machine with no config","takes no positional arguments, got \"version\"; the root is --root <dir>"]},"facts":{}}
```

I expected help for `version`, as `nova-up help version` prints, and as JSON if `--json` is honored. The verb in the object is `up`. `version` was taken as a positional argument. `nova-up help version --json` (the flag after the verb) prints `version`'s text help and ignores `--json`, exit 0.

Grade: URGENT.

4. `nova-up up --local --dry-run --root ./nova-no-redis-apply`

The tool had created this root, and `keys` was then mode 0755 (`stat` said `755`). Printed (stdout, exit 0), first 3 lines:

```
UP OK root=/home/nova/rowan-working/friends/johnny/jobs/dogfood-dsh-up-b.w1~15.g3/scratch/work/nova-no-redis-apply steps=8 changes=5 applied=0 dry_run=true
UP NOTE dry run: nothing written; run it without --dry-run to apply
UP platform ok linux: loops are systemd user units
```

The next line is `UP dirs ok` of that root. I expected a `change` that would put `keys` back to 0700. SPEC-UP's dirs step says `keys/` is 0700, and `ok` means nothing is run. The same page's idempotence section says a plan reads a directory's presence, not its mode. The create path did use 0700: that directory was 0700 before the chmod. A later dry-run will not repair 0755.

Grade: NEXT.

5. `nova-up up --local --dry-run --root ./root-a --root ./root-b`

Printed (stdout, exit 0), first 3 lines:

```
UP OK root=/home/nova/rowan-working/friends/johnny/jobs/dogfood-dsh-up-b.w1~15.g3/scratch/work/root-b steps=8 changes=6 applied=0 dry_run=true
UP NOTE dry run: nothing written; run it without --dry-run to apply
UP platform ok linux: loops are systemd user units
```

`up -h` shows one `--root`. I expected a refusal that it was given twice. The plan names only `root-b`. `root-a` is not mentioned. The printed root is the one that would be used, so the choice is not hidden.

Grade: NEXT.

6. `nova-up up --local --dry-run --root ''`

The argument after `--root` was an empty string. Printed (stdout, exit 0), first 3 lines:

```
UP OK root=/home/nova/rowan-working/friends/johnny/jobs/dogfood-dsh-up-b.w1~15.g3/scratch/home/nova steps=8 changes=6 applied=0 dry_run=true
UP NOTE dry run: nothing written; run it without --dry-run to apply
UP platform ok linux: loops are systemd user units
```

That path is `~/nova` under the scratch `HOME`. I expected a refusal that `--root` is empty. The default is what you get when the flag is absent (`nova-up up --local --dry-run` prints the same root). The line does name the directory. Nothing was written there.

Grade: NEXT.

7. `nova-up version --max 1`

Printed (stderr, exit 2), the only line:

```
VERSION REFUSED: unknown flag --max; the flags of version are --json; run: nova-up version -h
```

The banner says a verb that lists takes `--max` (default 20, 0 lists all) and says MORE for the rest. Neither verb lists. `nova-up up --local --dry-run --root ./nova-try --max 1` refuses `--max` the same way and names the flags of `up`. I expected the banner not to offer `--max`, or a verb that honors it.

Grade: NEXT.

8. `nova-up nosuch`

Printed (stderr, exit 2), the only line:

```
UP REFUSED: "nosuch" is no verb and no file; the verbs are up, version, and a file is given by its path (./nosuch); run: nova-up help
```

I expected the two verbs and `nova-up help`. The usage block has no file mode. `nova-up ./note.txt`, an existing file, does not enter one. It prints two lines, exit 2: `--local` is required, and `./note.txt` is a positional argument. The parenthetical path is not a way in.

Grade: NEXT.

## What held

- `nova-up version` prints `nova-up v0.0.0-20261006204015-cb5fb8d4c329 linux/amd64 go1.26.6`. `nova-up version --json` is one JSON object with that line as `payload`.
- `nova-up --local --dry-run --root ./nova-try-banner` and `nova-up up --local --dry-run --root ./nova-try` both exit 0 with `steps=8 changes=6 applied=0 dry_run=true`, the eight steps in the page's order, and `UP redis create 127.0.0.1:6390`. A second dry-run of `./nova-try` matched the first. Neither directory was created.
- With `redis-server` not on `PATH`, `nova-up up --local --root ./miss-redis-apply` exits 1, `applied=0`, and the binaries line is `missing redis-server not on PATH or not answering its version; install: sudo apt-get install -y redis-server`. No directory was created. With `age` on `PATH` and `age-keygen` not, the line names `age-keygen` and `sudo apt-get install -y age`, and again nothing was written.
- An unknown flag names that verb's flags. A missing `--local` is refused before a write. `nova-up up --json` without `--local` is one JSON object on stdout, exit 2.

READ 7/10 — SPEC-UP, the nova-up section of SETUP.md, and `up -h` name the same eight steps, the same dry-run, and the same root, but the banner says every verb's `--json` is one JSON object and that a listing verb takes `--max`, and neither is true here.

USE 6/10 — a dry-run against a scratch root and a missing program both stop where the page says they stop, but a `--root` that is a file is planned `ok` for dirs and for a sprint twin that is not there, and `help --json` does not help.

urgent=3 next=5
