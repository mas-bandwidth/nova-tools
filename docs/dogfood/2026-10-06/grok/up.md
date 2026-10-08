# nova-up dogfood, 2026-10-06 (grok)

Tool: nova-up. Build: `nova-up v1.0.1-0.20261007153756-abb9bfecc729 linux/amd64 go1.26.6`,
built from the card's base branch tip `abb9bfecc729`. Run cold, from the binary's own help and
`docs/SPEC-UP.md` only. Every door and both verbs (`up`, `version`) ran, with the refusals.
The rules forbid starting a service on this machine, so the real applies below stop at the redis
plan's own read, before any unit is written; no redis-server was started.

## 1. A fresh apply cannot pass the redis step: the secrets step leaves a store with no seat file, and the redis plan's own read refuses — URGENT

**Command:**

    nova-up up --local --root <job>/try/fresh

**Printed:**

    UP FAILED root=<job>/try/fresh steps=6 changes=4 applied=3: step redis: <bin>/nova-secrets names --store <job>/try/fresh/secrets --as coordinator --max 0: exit status 2: SECRETS NAMES REFUSED: seat file coordinator.yaml is absent: … run: nova-secrets seal …; the steps before it are applied, and a run again starts from what they left
    UP platform ok linux: loops are systemd user units
    UP dirs create <job>/try/fresh: . stores keys logs smoke

exit 1. The redis step's own line reads `UP redis change 127.0.0.1:6390 loop=redis-local for nova-bus: the seat's names could not be read (…); apply seals what is not there`.

**Expected:** the secrets step leaves a valid store with a key and `.sops.yaml` and no seat file
yet, which is exactly the state its own plan names `create`. The redis step says it will seal the
four passwords ("passwords to seal=4 … apply seals what is not there"), so I expected it to plan
`change`, seal them, and let seat and smoke apply, ending `UP OK … applied=8`; then a second run
`UP UNCHANGED`. Instead the redis plan's `nova-secrets names` read refuses on the empty store, so
seat and smoke never run, and every re-run fails at the same line. A stranger is handed back the
manual `nova-secrets seal …` command — the very hand step this tool exists to replace — and there
is no flag that skips or precedes the redis step. `--json` fails the same way
(`result.status=failed`, `exit=1`). The tool cannot set up a fresh machine at all.

**Grade:** URGENT

## 2. `--root=` (an empty value) silently falls back to the default `~/nova` and performs a real apply there — URGENT

**Command:**

    nova-up --local --root=

**Printed:**

    UP FAILED root=<home>/nova steps=6 changes=4 applied=3: step redis: <bin>/nova-secrets names --store <home>/nova/secrets --as coordinator --max 0: … SECRETS NAMES REFUSED: seat file coordinator.yaml is absent …
    UP platform ok linux: loops are systemd user units
    UP dirs create <home>/nova: stores keys logs smoke

exit 1. `nova-up --local --root` (no value) refuses with `UP REFUSED: --root needs a value: it wants the directory everything is written under, made if absent (default ~/nova); run: nova-up up -h`
at exit 2, but `--root=` does not. It planned `root=<home>/nova`, applied the dirs, sprint and
secrets steps, and wrote `<home>/nova/keys/coordinator.key`, `<home>/nova/stores/sprint.twin`,
`<home>/nova/secrets/` (a git working copy) and `<home>/nova/secrets.git/` before failing at the
redis read. The default root already held a `seat.env` and a `nova-config` from an earlier run;
this run added to it.

**Expected:** the default is for `--root` being absent, so an explicitly empty value is a missing
input and I expected the same `UP REFUSED: --root needs a value: it wants the directory everything is written under …; run: nova-up up -h` at exit 2. A caller whose `--root "$dir"` expanded empty is
sent at the default root rather than told to fix the call, and a real run writes there. The banner
never names a case where an empty value means "use the default".

**Grade:** URGENT

## 3. `--root` that is a regular file plans `dirs ok`, exits 0 on a dry run, and the real run fails with a remedy that cannot run — URGENT

**Command:**

    nova-up --local --dry-run --root <job>/try/notadir

**Printed:**

    UP OK root=<job>/try/notadir steps=8 changes=4 applied=0 dry_run=true
    UP NOTE dry run: nothing written; run it without --dry-run to apply
    UP platform ok linux: loops are systemd user units

exit 0; the `dirs` line under it reads `UP dirs ok <job>/try/notadir`.

**Expected:** `--root` wants "the directory everything is written under", so a regular file at
that path is knowable with no write: I expected `UP REFUSED: --root wants a directory …` at exit
2, or at least `dirs change`. Instead `dirs` reports `ok` on a file. The real run then gets past
dirs and sprint and fails one step later:

    UP FAILED root=<job>/try/notadir steps=5 changes=1 applied=0: step secrets: <bin>/nova-secrets keygen …: exit status 2: SECRETS KEYGEN REFUSED: key directory <job>/try/notadir/keys is absent; run: mkdir -m 700 -p <job>/try/notadir/keys; …
    UP dirs ok <job>/try/notadir
    UP sprint ok mem:<job>/try/notadir/stores/sprint.twin

The remedy cannot run, because the parent is a file; the failure is blamed on a missing `keys`
directory while the real problem is the root. A false plan line plus a dead end.

**Grade:** URGENT

## 4. `help --json` answers with the `up` verb's help, in lines, at exit 0 — URGENT

**Command:**

    nova-up help --json

**Printed:**

    usage: nova-up up [flags]
    from `nova-up help`:
      nova-up up -h

exit 0, followed by the whole `up` page in lines. `nova-up help` alone prints the banner, and
`nova-up help version --json` prints the version verb's help the same way.

**Expected:** the banner `nova-up help` prints, or a help result as JSON: the banner says "Every
verb takes --json: the same result as one JSON object on stdout". Instead a `--json` after `help`
silently reroutes it to the `up` page and prints that page as lines, so a reader asking for the
tool's help with `--json` is answered with a different page and no refusal. `nova-up help --bogus`
compounds it:

    UP REFUSED: unknown flag --bogus; the flags of up are --dry-run, --json, --local, --root; run: nova-up up -h
    UP REFUSED: unknown flag --help; the flags of up are --dry-run, --json, --local, --root; run: nova-up up -h

The tool's own help flag is reported as an unknown flag.

**Grade:** URGENT

## 5. The unknown-verb refusal advertises a file argument the tool does not have — NEXT

**Command:**

    nova-up bogus

**Printed:**

    UP REFUSED: "bogus" is no verb and no file; the verbs are up, version, and a file is given by its path (./bogus); run: nova-up help

exit 2.

**Expected:** the refusal names the verbs and the help door, without a file form: the banner,
`up -h` and the example block describe one mode (`--local`) and no file input, and following the
"given by its path" clause gets a second refusal for a different reason:

    nova-up ./somefile
    UP REFUSED: --local is required; it wants nothing after it: …; run: nova-up help
    UP REFUSED: takes no positional arguments, got "./somefile"; the root is --root <dir>; run: nova-up help

So the clause invents an interface: a stranger with an unknown verb is told to pass a path, and
the tool then refuses the path. The same sentence prints for the bare `nova-up`, where there is no
path to give.

**Grade:** NEXT

## 6. The banner's usage omits the `up` verb and promises a listing that does not exist — NEXT

**Command:**

    nova-up -h

**Printed:**

    nova-up: sets up nova on one machine, from nothing to a first sprint

    how it works: steps in order: platform, dirs, binaries, sprint, secrets, redis, seat, smoke.

exit 0. The usage block is `nova-up --local [--root <dir>] [--dry-run] [--json]`,
`nova-up version`, `nova-up help [<verb>]`; the bare refusal says "the verbs are up, version"; the
example block's second line is `nova-up up -h`; and the paragraph says "A verb that lists takes
--max <n> (default 20, 0 lists all) and says MORE for the rest", while `up` refuses `--max` as an
unknown flag:

    UP REFUSED: unknown flag --max; the flags of up are --dry-run, --json, --local, --root; run: nova-up up -h

**Expected:** one story. The usage should show the `up` verb the refusal and the example use
(`nova-up up --local …`), and the banner should not promise `--max` and a `MORE` line when the tool
has no listing verb. A stranger cannot tell whether the tool is `nova-up --local`,
`nova-up up --local`, or both (both run), and looks for a listing verb that never comes.

**Grade:** NEXT

READ 5/10 — the banner answers what, how and first-run, both verbs answer `-h` at exit 0 and name
their effect class and exit table, the example block runs as printed, and the missing-program
refusal names the install command and applies nothing; the score is held down by `help --json`
answering the wrong page, the file-path clause in the unknown-verb refusal, and the usage that
omits the `up` verb and promises `--max`/`MORE`.

USE 3/10 — `up` is the tool's one job and it cannot finish it: a fresh apply always dies at the
redis step on the state the secrets step just wrote, and an empty or file `--root` writes (or
plans to write) where the caller did not ask; the dry run on a good root, the missing-program path
and `version` are clean, but a stranger who runs the banner's first line for real never reaches
`UP OK`.

urgent=4 next=2
