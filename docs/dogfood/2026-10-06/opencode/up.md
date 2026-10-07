# nova-up, cold, one friend, one machine: 2026-10-06

Dogfood of `nova-up` at tip `0760eac79c771622c8d16e807b89373f0dcd7a10`, read cold: `nova-up -h`,
`nova-up help`, `nova-up help <verb>`, `nova-up <verb> -h`, and `docs/SPEC-UP.md`; no source read.
Linux, sandboxed: the preinstalled programs under the user's local bin are present but the sandbox
denies executing them, so `redis-server`, `sops` and `age-keygen` could never answer their version
here. Every real run therefore stops at the `binaries` gate; the tool refused with exit 1 exactly as
`docs/SPEC-UP.md` says, and each time the named root stayed absent afterwards (nothing applied).

Used for real against scratch roots inside the job directory: `up` with `--local`, `--root` (good
value, empty value, a value that is an existing file, a relative value), `--dry-run`, `--json`;
`version` plain and `--json`; `help` bare, per verb, unknown; the refusals (bare command, unknown
verb, unknown flag, `--root` without a value, `up` without `--local`, a positional file path); the
missing gate with 1, 2, 6 and 7 programs missing; two consecutive dry runs on one root (identical
plan). Not done: the full apply — the redis step installs a systemd user unit and starts it, and the
card forbids starting a server on this machine — so `UP OK`, `UP UNCHANGED`, the secrets, seat and
smoke applies and the sealed passwords were never observed live.

Findings (in the commands: `$S` is the job's scratch directory, `$P` is a PATH with `git`,
`redis-server` and the four repo tools answering their version — `nova-sprint`, `nova-secrets`,
`nova-redis`, `nova-bus` built fresh from this checkout into the job directory, since the
machine's preinstalled copies cannot be executed here):

1. The plan reports `ok` for a root that is a regular file, and a later step plans `ok` on the same
   impossible path.
   command: `bin/nova-up up --local --root $S/blocker` (with `bin/nova-sprint`, `bin/nova-secrets`,
   `bin/nova-redis`, `bin/nova-bus` answering their version on PATH; `$S/blocker` is an existing
   empty regular file)
   printed (first 3 lines):
   ```
   UP FAILED root=…/scratch/blocker steps=8 changes=5 applied=0: missing: binaries; nothing was applied; install what the missing line names and run again
   UP platform ok linux: loops are systemd user units
   UP dirs ok …/scratch/blocker
   ```
   (further down the same run: `UP sprint ok mem:…/scratch/blocker/stores/sprint.twin` and
   `UP secrets change …: key rules`.) The run stopped only because `binaries` was missing.
   expected: `dirs` to refuse or report the root is a file, never `ok` — `ok` means "already as the
   step wants it", and a file is not; `sprint` planning `ok` on a twin that cannot exist under a
   file is the same lie. On a provisioned machine the run would proceed on these false `ok` lines
   and fail later at `secrets` with a nested tool error, leaving no line that names the real
   mistake (the root is a file).
   grade: URGENT — a wrong plan line; the plan is the machine as the step sees it, and here it lies.

2. `--root ""` silently becomes the default root (`$HOME/nova`), where the empty `--root` next to it
   is refused.
   command: `env PATH=$P HOME=$S/fakehome bin/nova-up up --local --dry-run --root "" --json`
   printed (first 3 lines; the JSON is one line, truncated here):
   ```
   {"result":{"verb":"up","status":"failed","exit":1,…},"facts":{"root":"…/scratch/fakehome/nova",…
   ```
   expected: a refusal, exit 2, as the dangling form gets one line earlier
   (`UP REFUSED: --root needs a value: it wants the directory everything is written under…`); an
   explicitly passed empty value is missing input, and the standard is that no default stands in
   for it. In a real run one empty shell variable (`--root "$UNSET"`) would lay a whole setup —
   including the step-6 unit file, which lands outside the root — on the machine's default
   location, announced only inside a long output the caller did not ask to redirect.
   grade: URGENT — wrong target chosen by silent substitution, and it contradicts the flag's own
   refusal next door.

3. The missing line lumps two different failures and its remedy cannot fit both.
   command: `bin/nova-up --local --dry-run --root ./nova-try` (the help page's own example line, on
   the machine as it is)
   printed (first 3 lines):
   ```
   UP FAILED root=…/scratch/nova-try steps=8 changes=7 applied=0: missing: binaries; nothing was applied; install what the missing line names and run again
   UP platform ok linux: loops are systemd user units
   UP dirs create …/scratch/nova-try: . stores keys logs smoke
   ```
   (the binaries line: `UP binaries missing redis-server,sops,age-keygen,nova-sprint,nova-secrets,nova-redis,nova-bus not on PATH or not answering its version; install: sudo apt-get install -y redis-server && go install github.com/getsops/sops/v3/cmd/sops@latest && sudo apt-get install -y age && go install …`)
   expected: the line to say which failure each program had — "not on PATH" versus "found but did
   not answer its version" — because here all seven are installed and executable by mode and the
   failure is exec denial by the machine. The printed remedy both dies at its first `sudo` on a
   machine without one and reinstalls into the same exec-denied paths, changing nothing; the
   stranger cannot tell "install it" from "it is installed but broken".
   grade: NEXT — the refusal is honest and the run applies nothing; the diagnosis is not precise
   enough to act on in one turn.

4. The bare refusal offers a file operand the tool then refuses.
   command: `bin/nova-up nonsense` and then `bin/nova-up $S/plain.txt` (an existing file, given by
   its path)
   printed (first 3 lines of each, one line each):
   ```
   UP REFUSED: "nonsense" is no verb and no file; the verbs are up, version, and a file is given by its path (./nonsense); run: nova-up help
   ```
   ```
   UP REFUSED: takes no positional arguments, got "…/scratch/plain.txt"; the root is --root <dir>; run: nova-up help
   ```
   expected: one story — `nova-up` takes no positional file (the second refusal says so, and
   `up -h`'s flags agree), so the first refusal should not promise that a file "is given by its
   path". The two lines point at each other and cost a turn.
   grade: NEXT — unclear refusal text, no wrong result.

5. The top-level usage never names the verb `up`.
   command: `bin/nova-up -h`
   printed (the usage block, first 3 lines):
   ```
   usage:
     nova-up --local [--root <dir>] [--dry-run] [--json]
     nova-up version
   ```
   expected: `nova-up up [flags]` named beside it — the tool answers "the verbs are up, version"
   when refused bare, and `nova-up up -h` is in the example block, but the usage itself shows only
   the `--local` spelling, so the verb has to be met in an example or a refusal first.
   grade: NEXT — unclear help; both spellings work once known.

6. The command reference skips the tool: no `## nova-up` section in `docs/CLI.md`, no transcript in
   `docs/TESTS.md`.
   command: `grep -n "nova-up" docs/CLI.md docs/TESTS.md | grep -v "nova-update"`
   printed (first 3 lines): no output — every match in both files is `nova-update` (22 and 4),
   none is the tool.
   expected: a `### First run` section and an executed transcript like every other shipped tool's;
   the transcripts allowlist carries no `nova-up` row to excuse it.
   grade: NEXT — a missing docs section for a tool being cut into v1.1.0.

READ 8/10 — the help and `docs/SPEC-UP.md` predicted everything the runs did (plan-then-apply, the
missing gate applying nothing, the remedy scaling to the missing set, the streams, `--dry-run`
writing nothing) and only the usage's missing verb name and the file-operand clause made me guess.

USE 7/10 — every flag did what its help said and the refusals carried remedies and named the
available flags, but two plan/refusal edge cases told me something false (a file root planned `ok`,
an empty `--root` defaulted) and the success path was unreachable on this machine, so `UP OK` and
`UP UNCHANGED` were never seen live.

urgent=2 next=4