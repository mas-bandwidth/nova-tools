# nova-up dogfood, 2026-10-06 (codex)

Tool: nova-up. Build: `nova-up v1.0.1-0.20261006204015-cb5fb8d4c329 darwin/arm64 go1.26.6`,
built from the card's base `cb5fb8d4c`. Run cold, from the binary's own help and
`docs/SPEC-UP.md` only. Every door and both verbs (`up`, `version`) ran, with the refusals.
The rules forbid starting a service on this machine, so no full apply was run past the redis
step; the fresh-machine apply below reached its wall before any unit was written, and no
redis-server was started.

## 1. A fresh setup cannot pass the redis step, which refuses on the empty seat store its own secrets step leaves — URGENT

**Command:**

    nova-up up --local --root <job>/scratch/work/root-fresh

**Printed:**

    UP FAILED root=<job>/scratch/work/root-fresh steps=6 changes=4 applied=3: step redis: <bin>/nova-secrets names --store <job>/scratch/work/root-fresh/secrets --as coordinator --max 0: exit status 2: SECRETS NAMES REFUSED: seat file coordinator.yaml is absent: … the steps before it are applied, and a run again starts from what they left
    UP platform ok darwin: loops are launchd agents
    UP dirs create <job>/scratch/work/root-fresh: . stores keys logs smoke

exit 1. The redis step's own line reads `UP redis change 127.0.0.1:6390 loop=redis-local for
nova-bus: the seat's names could not be read (…); apply seals what is not there`.

**Expected:** the secrets step leaves a valid store with a key and `.sops.yaml` and no seat file
yet, which is exactly the state its own plan names `create`. The redis step says it will seal the
four passwords ("passwords to seal=4 … apply seals what is not there"), so I expected it to plan
`change`, seal them, and let seat and smoke apply, ending `UP OK … applied=8`; then a second run
`UP UNCHANGED`. Instead the redis plan's `nova-secrets names` read refuses on the empty store, so
seat and smoke never run, and every re-run fails at the same line (`secrets ok`, `redis change`,
the same refusal). A stranger is handed back the manual `nova-secrets seal …` command — the very
hand steps this tool exists to replace — and there is no flag that skips or precedes the redis
step. The tool cannot set up a fresh machine at all.

**Grade:** URGENT

## 2. `--root` that is a regular file plans `dirs ok`, then fails a later step with a remedy that cannot run — URGENT

**Command:**

    touch <job>/scratch/work/notadir
    nova-up up --local --dry-run --root <job>/scratch/work/notadir

**Printed:**

    UP OK root=<job>/scratch/work/notadir steps=8 changes=4 applied=0 dry_run=true
    UP NOTE dry run: nothing written; run it without --dry-run to apply
    UP platform ok darwin: loops are launchd agents
    UP dirs ok <job>/scratch/work/notadir

exit 0.

**Expected:** `--root` wants "the directory everything is written under", so a regular file at
that path is knowable with no write: I expected `UP REFUSED: --root wants a directory …` at
exit 2, or at least `dirs change`. Instead `dirs` reports `ok` on a file. The real run then gets
past dirs and sprint and fails one step later:

    UP FAILED root=<job>/scratch/work/notadir steps=5 changes=1 applied=0: step secrets: … nova-secrets keygen …: exit status 2: SECRETS KEYGEN REFUSED: key directory <job>/scratch/work/notadir/keys is absent; run: mkdir -m 700 -p <job>/scratch/work/notadir/keys; the steps before it are applied …
    UP dirs ok <job>/scratch/work/notadir
    UP sprint ok mem:<job>/scratch/work/notadir/stores/sprint.twin

The remedy cannot run, because the parent is a file; the failure is blamed on a missing `keys`
directory while the real problem is the root. A false plan line plus a dead end.

**Grade:** URGENT

## 3. `help --json` answers with the `up` verb's help, in lines, at exit 0 — URGENT

**Command:**

    nova-up help --json

**Printed:**

    usage: nova-up up [flags]
    from `nova-up help`:
      nova-up up -h

exit 0. `nova-up help version --json` prints the version verb's help the same way, and
`nova-up help --bogus` prints two `UP REFUSED: unknown flag` lines, for `--bogus` and for
`--help`.

**Expected:** the banner `nova-up help` prints, or a help result as JSON: the banner says
"Every verb takes --json: the same result as one JSON object on stdout". Instead a `--json`
flag on the help door silently reroutes it to the `up` verb and prints that page as lines, so a
reader asking for the tool's help with `--json` is answered with a different page and no
refusal. The other help doors (`nova-up -h`, `nova-up up -h`, `nova-up help`, `nova-up help up`)
are consistent; only a flag after `help` is wrong.

**Grade:** URGENT

## 4. `--root ""` silently falls back to the default `~/nova` — NEXT

**Command:**

    nova-up up --local --dry-run --root ""

**Printed:**

    UP OK root=<home>/nova steps=8 changes=6 applied=0 dry_run=true
    UP NOTE dry run: nothing written; run it without --dry-run to apply
    UP platform ok darwin: loops are launchd agents

exit 0.

**Expected:** the default is for `--root` being absent, so an explicitly empty value is a missing
input and I expected `UP REFUSED: --root needs a value: it wants the directory everything is
written under …` at exit 2 (the same line a bare `--root` gets). Instead an empty value is
treated as absent and the run plans `~/nova`; a caller whose `--root "$dir"` expanded empty is
sent at the default root rather than told to fix the call.

**Grade:** NEXT

## 5. The unknown-verb refusal advertises a file argument the tool does not have — NEXT

**Command:**

    nova-up bogus

**Printed:**

    UP REFUSED: "bogus" is no verb and no file; the verbs are up, version, and a file is given by its path (./bogus); run: nova-up help

exit 2.

**Expected:** the refusal to name the verbs and the help door, without a file form: the banner,
`up -h` and the example block describe one mode (`--local`) and no file input, and a file path is
refused:

    nova-up ./setup.conf
    UP REFUSED: --local is required; it wants nothing after it: …
    UP REFUSED: takes no positional arguments, got "./setup.conf"; the root is --root <dir>; run: nova-up help

So the "given by its path" clause invents an interface: a stranger with an unknown verb is told
to pass a path, and the tool then refuses the path for a different reason. The same sentence
prints for the bare `nova-up`, where there is no path to give.

**Grade:** NEXT

## 6. The banner's usage omits the `up` verb it names elsewhere and promises a listing that does not exist — NEXT

**Command:**

    nova-up -h

**Printed:**

    nova-up: sets up nova on one machine, from nothing to a first sprint

    how it works: steps in order: platform, dirs, binaries, sprint, secrets, redis, seat, smoke.

exit 0. The usage block is `nova-up --local [--root <dir>] [--dry-run] [--json]`,
`nova-up version`, `nova-up help [<verb>]`; the bare refusal says "the verbs are up, version";
the example block's second line is `nova-up up -h`; and the paragraph says "A verb that lists
takes --max <n> (default 20, 0 lists all) and says MORE for the rest", while `up` refuses
`--max 2` as an unknown flag and no verb lists anything.

**Expected:** one story. The usage should show the `up` verb the refusal and the example use
(`nova-up up --local …`), and the banner should not promise `--max` and a `MORE` line when the
tool has no listing verb. A stranger cannot tell whether the tool is `nova-up --local`,
`nova-up up --local`, or both (both run), and looks for a listing verb that never comes.

**Grade:** NEXT

READ 5/10 — the banner answers what, how and first-run, both verbs answer `-h` at exit 0 and
name their effect class and exit table, the example block runs as printed, and the missing
program refusal names the install command and applies nothing; the score is held down by the
usage that omits the `up` verb, the `--max`/`MORE` promise with no listing verb, the file-path
clause in the unknown-verb refusal, and `help --json` answering the wrong page.

USE 3/10 — `up` is the tool's one job and it cannot finish it: a fresh apply always dies at the
redis step on the state the secrets step just wrote, and the file `--root` case gives a false
`dirs ok` then an unactionable remedy; the dry run, the missing-program path and `version` are
clean, but a stranger who runs the banner's first line for real never reaches `UP OK`.

urgent=3 next=3
