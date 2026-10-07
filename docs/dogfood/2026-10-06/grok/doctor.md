# Dogfood: nova-doctor — 2026-10-06, grok

Read cold as a stranger: `nova-doctor -h`, `nova-doctor help`, `nova-doctor run -h`,
`nova-doctor version -h`, and the tool's page under `docs/` (`docs/SPEC-DOCTOR.md`).
Built from the checkout at e8f70f600 as
`nova-doctor v1.0.1-0.20261007032034-e8f70f600ebf linux/amd64 go1.26.6`, and used
for real on the Linux bench with `GOCACHE=$HOME/zhi-bench/.cache/go-build`,
`GOFLAGS=-mod=readonly` and `NOVA_TEST_NO_HOST=1`: every verb and flag at least
once, a scratch secrets directory, and the refusals. About 30 minutes of use. A
home path in a quoted line is written `/home/<user>/` so the record carries no
machine name.

## Findings

1. `nova-doctor run extra` (the same with `./go.mod`, `/etc/hostname`, `.`,
   `./nonexistent-xyz`)

       DOCTOR gosdk ok go 1.26.6, go.mod toolchain 1.26.6, GOCACHE /home/<user>/zhi-bench/.cache/go-build writable, GOFLAGS -mod=readonly
       DOCTOR providers fail the seat's secrets store could not be read: NOVA_SECRETS_STORE and NOVA_SECRETS_SEAT do not name the seat's store fix: nova-secrets names --store <dir> --as <seat> (nova-up --local writes the seat's store and its environment)
       DOCTOR self fail nova-loop, nova-loop-migrate, nova-push-credential did not answer `version` with a version line fix: nova-update apply --file <manifest> nova-loop
       exit=2

   Expected: `RUN REFUSED: takes no positional arguments, got "extra" (flags come
   before arguments)`, the refusal `nova-doctor version extra` prints; instead the
   operand is dropped and the checks run, so a mistyped word or path is answered
   with a plausible result the caller did not ask for. The same defect is on the
   bare dispatcher: `nova-doctor frobnicate` refuses with `"frobnicate" is no verb
   and no file; the verbs are run, version, and a file is given by its path
   (./frobnicate)`, advertising a file operand that `nova-doctor ./go.mod` then
   ignores. Grade: URGENT.

2. `set -a; . ~/nova/seat.env; set +a; nova-doctor --check tailnet`

       DOCTOR tailnet fail the machine inventory could not be read: nova-config machine list: exit status 2 fix: source the seat file (`set -a; . ~/nova/seat.env; set +a`), then run nova-doctor again (docs/SETUP.md, dep-tailnet-b.w4)
       exit=2

   and the step the fix line names, run by hand:

       nova-config machine list REFUSED: --pg is required: postgres://user@host:5432/db (or NOVA_PG_DSN), or --file <path> for a local file with no database, or a login recorded by nova-config login; run: nova-config machine list -h

   Expected: the one line the result gives — source the seat file, then run the
   check again — to clear `tailnet`. It does not: the seat file is already
   sourced, the same check prints the same evidence at exit 2, and the input the
   reading actually wants (`--pg` or `--file`) appears nowhere in the fix line.
   Grade: URGENT.

3. `nova-secrets names --store $PWD/.scratch/secrets --as coordinator` (typing the
   providers fix line, with a fresh scratch store)

       SECRETS NAMES REFUSED: store /home/<user>/zhi-bench/dogfood-grok-doctor-b.w1~15.g7/repo/.scratch/secrets has no .git directory and no .sops.yaml; run: git clone <store url> /home/<user>/zhi-bench/dogfood-grok-doctor-b.w1~15.g7/repo/.scratch/secrets (a new store: git init it, then write its .sops.yaml from the rule block nova-secrets keygen prints)
       exit=2

   and the check it is meant to fix:

       DOCTOR providers fail the seat's secrets store could not be read: NOVA_SECRETS_STORE and NOVA_SECRETS_SEAT do not name the seat's store fix: nova-secrets names --store <dir> --as <seat> (nova-up --local writes the seat's store and its environment)

   Expected: a fix line that makes the store `providers` wants. `nova-secrets
   names` is a listing verb; it refuses a directory with no store, so pasting the
   fix line as printed cannot make the check ok, and the setup the refusal itself
   describes (`git init`, `.sops.yaml`, `nova-secrets keygen`) is not the command
   the fix line names. Grade: URGENT.

4. `nova-doctor --local` (the one and only line of `nova-doctor help`'s `example:`
   block)

       DOCTOR gosdk ok go 1.26.6, go.mod toolchain 1.26.6, GOCACHE /home/<user>/zhi-bench/.cache/go-build writable, GOFLAGS -mod=readonly
       DOCTOR self fail nova-loop, nova-loop-migrate, nova-push-credential did not answer `version` with a version line fix: nova-update apply --file <manifest> nova-loop
       DOCTOR local skipped=providers,tailnet (checks only a fleet needs; run without --local to include them)
       exit=2

   Expected: an `example:` block a stranger can paste whose lines answer 0 or 1.
   The block has one line, and on a machine missing what it checks that line exits
   2, so the first run the help prints is a failure; the onboarding standard asks
   for three runnable lines. Grade: NEXT.

5. `nova-doctor run -h`

       usage: nova-doctor run [flags]
       checks: gosdk, providers, self, tailnet
       example: nova-doctor --local

   Expected: the page under `docs/` to name every check and verb. `The checks` in
   `docs/SPEC-DOCTOR.md` has one entry, `self`, and `The command` names only
   `run`; `gosdk`, `providers`, `tailnet`, `version` and `help` are undocumented,
   so a stranger who reads the page before the binary is told about one of the
   tool's four checks. Grade: NEXT.

6. `env -u GOFLAGS nova-doctor --check gosdk`

       DOCTOR gosdk warn go 1.26.6, toolchain 1.26.6, GOCACHE /home/<user>/zhi-bench/.cache/go-build; GOFLAGS does not carry -mod=readonly fix: export GOFLAGS=-mod=readonly before any go command on a bench (docs/SETUP.md, dep-go-sdk-b.w2)
       exit=0

   Expected: a fix line a stranger can act on, without the internal branch id
   `dep-go-sdk-b.w2`; `tailnet`'s fix line carries `dep-tailnet-b.w4` the same
   way. The reader does not have that branch, and nothing in the line says what it
   is. Grade: NEXT.

7. `nova-doctor --check self`

       DOCTOR self fail nova-loop, nova-loop-migrate, nova-push-credential did not answer `version` with a version line fix: nova-update apply --file <manifest> nova-loop
       exit=2

   Expected: a fix line covering every tool the evidence names. Three tools are
   named and the fix names one (`nova-loop`), so a reader fixes one, re-runs, and
   is turned away twice more; the spec's fix shape also carries `--version
   <release>`, which the printed line drops. Grade: NEXT.

8. `nova-doctor run -h` and `nova-doctor --check`

       --check <help run>  run only this check (repeatable); the names are in help run
       RUN REFUSED: --check needs a value: it wants run only this check (repeatable); the names are in help run; run: nova-doctor run -h
       exit=0 (the `-h`) / exit=2 (the missing value)

   Expected: the flag's value to read `<name>`, as `docs/SPEC-DOCTOR.md` writes it,
   and the refusal's "it wants ..." to read the flag's description. Both read
   `<help run>` and "it wants run only this check", which name neither the value
   nor a noun a reader can act on. Grade: NEXT.

9. `nova-doctor run --max 5`

       RUN REFUSED: unknown flag --max; the flags of run are --check, --json, --local, --strict; run: nova-doctor run -h
       exit=2

   Expected: the top-level help's sentence `A verb that lists takes --max <n>
   (default 20, 0 lists all) and says MORE for the rest` to name no flag this tool
   has. `run` is the only work verb and it refuses `--max`, so the sentence sends
   a reader to a flag that is not there. Grade: NEXT.

10. `nova-doctor help --json`

        usage: nova-doctor run [flags]
        checks: gosdk, providers, self, tailnet
        example: nova-doctor --local
        exit=0

    Expected: the banner `nova-doctor help` prints without `--json`. The output
    flag changes which help page is shown, so the same verb answers two different
    pages depending on an unrelated flag. Grade: NEXT.

11. `nova-doctor -v` and `nova-doctor --version`

        RUN REFUSED: unknown flag --v; the flags of run are --check, --json, --local, --strict; run: nova-doctor run -h
        nova-doctor v1.0.1-0.20261007032034-e8f70f600ebf linux/amd64 go1.26.6
        exit=2 / exit=0

    Expected: `-v` to be named as typed (the refusal rewrites it to `--v`, a flag
    no run lists) and `--version`, which answers the version line, to be named in
    the usage or refused with a nearest guess, as an unknown verb is. Grade: NEXT.

12. `set -a; . ~/nova/seat.env; set +a; nova-doctor --check gosdk`

        DOCTOR gosdk warn go is on PATH at /home/<user>/go/bin/go 1.26.6: the coordinator's machine runs no go build or test (the bench rule) fix: build and test on a bench, and install a released binary here with `nova-update apply --file <manifest> <tool>` (docs/SETUP.md, dep-go-sdk-b.w2)
        exit=0

    Expected: `gosdk` to report the same machine before and after the `tailnet`
    remedy. Without the seat file it is `ok` with `GOCACHE ... writable, GOFLAGS
    -mod=readonly`; sourcing the seat file that `tailnet`'s fix line prints flips
    it to `warn` and says this is the coordinator's machine, a branch its evidence
    does not explain and `docs/SPEC-DOCTOR.md` does not mention. Grade: NEXT.

## What worked

The banner answers what the tool does, how it works, where its state is (nowhere)
and its exit table, and every verb answers `-h` at exit 0 before reading anything.
A bare `nova-doctor` runs the checks, and the `first run:` line says so. An unknown
check name is refused in one line naming all four checks (`RUN REFUSED: no check
named "nope"; the checks are gosdk, providers, self, tailnet`), an unknown flag
names every flag of `run`, and `--check` with no value is refused rather than
defaulted. `--local` prints the spec's `DOCTOR local skipped=providers,tailnet`
line and runs the same checks minus those two. `--json` is the same value in one
object (`{"exit":N,"results":[...],"skipped":[...]}`) and its `exit` field tells
the truth: 0 for a warn, 1 for the same warn under `--strict`, 2 for a fail,
matching the line form in every run. A fail does not stop the others: the bare run
printed all four. The tool found real defects in this machine's environment on its
first run: three PATH tools that do not answer `version` (`nova-loop`,
`nova-loop-migrate`, `nova-push-credential`), the unset seat secrets store, and
the unreadable machine inventory.

## Gate

    go test -count=1 -timeout 600s ./internal/docs ./internal/ci

    ok  	github.com/mas-bandwidth/nova-tools/internal/docs	2.353s
    ok  	github.com/mas-bandwidth/nova-tools/internal/ci	12.005s

    go test -count=1 -timeout 600s ./internal/docs -run TestDocsTreeIsConsistent

    ok  	github.com/mas-bandwidth/nova-tools/internal/docs	0.011s [no tests to run]

The card's named test `TestDocsTreeIsConsistent` does not exist in
`./internal/docs` at this tip, so that run passes with nothing to run; the docs
package's own walk (terminology, links, maps, transcripts) is the check this
report answers, and both packages are green.

## Not done

The card's STEP 2 (write the failing test first) and STEP 3 (implement at the
cause) are not done: the card's STOP says a finding is never fixed here, only
recorded, and no code changes are in this card. Only
`docs/dogfood/2026-10-06/grok/doctor.md` was added.

READ 6/10 — the banner is honest about what the tool is and does, every verb
answers `-h` with its exit table, and the refusals name their inputs; the page
under `docs/` documents one of the tool's four checks, the `--check` value and its
refusal read `<help run>`, the top-level help advertises a `--max` no verb has,
and `help --json` shows a different page than `help`.

USE 6/10 — the tool found real environment defects on the first run, the four
checks run without stopping each other, `--local`/`--strict`/`--json` behave as
their help says, and the JSON exit field matches the line exits; two of the four
fix lines do not fix their check (tailnet's remedy was already applied and the
check still fails; the providers fix line is a listing verb that refuses a fresh
store), `run` silently drops an operand the dispatcher says is a file, and the
`self` fix names one of the three tools it just named.

urgent=3 next=9
