# Dogfood: nova-doctor — 2026-10-06, opencode-2

Read cold as a stranger: `nova-doctor -h`, `nova-doctor help`, `nova-doctor run -h`,
`nova-doctor version -h`, `nova-doctor help run`, and the tool's page under `docs/`
(`docs/SPEC-DOCTOR.md`, beside the section each fix line names in `docs/SETUP.md`). Built
from the checkout at abb9bfecc as
`nova-doctor v1.0.1-0.20261007153756-abb9bfecc729 linux/amd64 go1.26.6`, and used for real
on the Linux bench with `GOCACHE=$HOME/zhi-bench/.cache/go-build`, `GOFLAGS=-mod=readonly`
and `NOVA_TEST_NO_HOST=1`: every verb and flag at least once, a scratch secrets store (a
git working copy with a `.sops.yaml` and a seat file, an age key at mode 0600 in a 0700
directory, and fakes on PATH for `nova-config`, `nova-secrets` and `tailscale` that answer
from fixtures), every check made to pass and to fail, and the refusals. About 30 minutes of
use. A home path is written `/home/<user>/` so the record carries no machine name.

## Findings

1. `nova-doctor run extra` (the same for `nova-doctor run ./go.mod`)

       DOCTOR gosdk warn go 1.26.6, toolchain 1.26.6, GOCACHE /home/<user>/.cache/go-build; GOFLAGS does not carry -mod=readonly fix: export GOFLAGS=-mod=readonly before any go command on a bench (docs/SETUP.md, dep-go-sdk-b.w2)
       DOCTOR providers fail the seat's secrets store could not be read: NOVA_SECRETS_STORE and NOVA_SECRETS_SEAT do not name the seat's store fix: nova-secrets names --store <dir> --as <seat> (nova-up --local writes the seat's store and its environment)
       DOCTOR secrets fail NOVA_SECRETS_KEY is not set, so this machine has no age key to open the store with fix: set NOVA_SECRETS_KEY to this machine's age private key; nova-up --local writes it into seat.env (docs/SETUP.md, dep-secrets-bb.w4)

   Expected: the refusal the sibling verb prints, `VERSION REFUSED: takes no positional
   arguments, got "extra" (flags come before arguments)`, or a `RUN REFUSED` of the same
   shape. Instead the operand is dropped and the five checks run, so a mistyped word or a
   path is answered with a full plausible report the caller did not ask for. The bare
   dispatcher is as loose: `nova-doctor ./go.mod` and `nova-doctor ./nonexistent` also
   run the checks. Grade: URGENT.

2. `nova-doctor frobnicate`

       DOCTOR REFUSED: "frobnicate" is no verb and no file; the verbs are run, version, and a file is given by its path (./frobnicate); run: nova-doctor help

   Expected: an unknown-word refusal naming the verbs there are, as it does, and no other
   door. The line advertises "a file is given by its path (./frobnicate)", but no file
   operand is ever read: `nova-doctor ./go.mod` ignores the file and runs the checks
   (finding 1), and `--check` is the only way to narrow a run. Help that lies. Grade:
   URGENT.

3. `nova-doctor help run extra`

       DOCTOR gosdk warn go 1.26.6, toolchain 1.26.6, GOCACHE /home/<user>/.cache/go-build; GOFLAGS does not carry -mod=readonly fix: export GOFLAGS=-mod=readonly before any go command on a bench (docs/SETUP.md, dep-go-sdk-b.w2)
       DOCTOR providers fail the seat's secrets store could not be read: NOVA_SECRETS_STORE and NOVA_SECRETS_SEAT do not name the seat's store fix: nova-secrets names --store <dir> --as <seat> (nova-up --local writes the seat's store and its environment)
       DOCTOR secrets fail NOVA_SECRETS_KEY is not set, so this machine has no age key to open the store with fix: set NOVA_SECRETS_KEY to this machine's age private key; nova-up --local writes it into seat.env (docs/SETUP.md, dep-secrets-bb.w4)

   Expected: `nova-doctor help run` (the verb's help, exit 0) or a refusal of the extra
   operand. Instead the help request executes the checks and exits 2. A request for
   documentation runs work. Grade: URGENT.

4. `nova-doctor --check gosdk`, run from a directory with no `go.mod`

       DOCTOR gosdk fail no go.mod here: the bench's toolchain is not named fix: run nova-doctor in a checkout of this repository, whose go.mod names the bench's toolchain (docs/SETUP.md, dep-go-sdk-b.w2)

   Expected: `ok`, or at worst a `warn`, on a machine where `go 1.26.6` is installed,
   `GOCACHE` is writable and `GOFLAGS=-mod=readonly` is set — the same machine answers
   that inside the checkout. The verdict depends on the working directory, and the fix
   asks a reader who installed the released binary for a checkout of this repository,
   which is not on their machine. `gosdk` is not fleet-scoped, so `--local` does not skip
   it. Wrong result. Grade: URGENT.

5. `nova-doctor --check` (no value), and the flag list in `nova-doctor run -h`

       RUN REFUSED: --check needs a value: it wants run only this check (repeatable); the names are in help run; run: nova-doctor run -h

   Expected: the flag's value to read `<name>`, the word `docs/SPEC-DOCTOR.md` uses, and
   the "it wants ..." clause to read the flag's description. Both the flag list line
   (`--check <help run>`) and this refusal read as if the value were the help command, so
   a reader cannot tell what to type. Grade: NEXT.

6. `nova-doctor run --max 5`

       RUN REFUSED: unknown flag --max; the flags of run are --check, --json, --local, --strict; run: nova-doctor run -h

   Expected: the banner's sentence "A verb that lists takes `--max <n>` (default 20, 0
   lists all) and says MORE for the rest" to name no flag this tool has. `run` is the only
   work verb and it refuses `--max`; no verb lists anything. Grade: NEXT.

7. `nova-doctor help --json`

       usage: nova-doctor run [flags]
       checks: gosdk, providers, secrets, self, tailnet
       example: nova-doctor --local

   Expected: the banner plain `nova-doctor help` prints (line 1, `how it works:`, usage,
   exit codes, `example:`). `--json`, a flag of the other verbs, changes which help page is
   shown. Grade: NEXT.

8. `nova-doctor -v`

       RUN REFUSED: unknown flag --v; the flags of run are --check, --json, --local, --strict; run: nova-doctor run -h

   Expected: the refusal to name the flag as typed (`-v`), not to rewrite it to `--v`, a
   spelling no verb lists. `nova-doctor --version` answers the version line at exit 0, but
   `--version` appears in no usage line; the usage names `nova-doctor version` only.
   Grade: NEXT.

9. `nova-doctor version -h`

       usage: nova-doctor version [flags]
       from `nova-doctor help`:
         nova-doctor version

   and, below its flags, the exit table it quotes:

       exit codes: 0 every check ok (a warn too, unless --strict), 1 a warn under --strict, 2 a fail, or usage

   Expected: `version`'s own exits (0 done, 2 usage). The verb runs no check, so quoting
   the check exit table in its help is wrong. Grade: NEXT.

10. The page under `docs/` (`docs/SPEC-DOCTOR.md`), read before the binary

    `## The checks` carries one heading, `### self`; the binary registers five
    (`gosdk, providers, secrets, self, tailnet`). `## The command` names `run` and the
    four flags, and the check sources cite spec sections `providers`, `gosdk`, `secrets`
    and `tailnet` that the page does not carry. A stranger who reads the normative page is
    told about one of the tool's five checks. Grade: NEXT.

11. `nova-doctor -h` line 1 against the README's row for the tool

    Banner: `nova-doctor: says what is missing for the nova tools to work, and the one
    line that fixes each`
    README: `says what is missing for the nova tools to work here and, for each thing, the
    one line that fixes it`

    Expected: the same sentence, as the onboarding standard's point 6 holds. Grade: NEXT.

12. `nova-doctor help`, its `example:` block

    The block is one line, `nova-doctor --local`, and on a machine where the fleet checks
    fail that line exits 2: the first run the help prints is a failure. The standard asks
    for three to six runnable lines. Grade: NEXT.

13. `nova-doctor --check providers` over a route whose provider has no known models endpoint

       DOCTOR providers ok 1 enabled route(s) ok; provider opencode has no known models endpoint; route local is listed, not checked; disabled off: paused

    Expected: the tally not to call the route checked. The one enabled route was listed,
    not checked, yet the line counts it (`1 enabled route(s) ok`), so the check's own
    answer and its count disagree. Grade: NEXT.

14. `nova-doctor --check providers` with no `NOVA_SECRETS_STORE`/`NOVA_SECRETS_SEAT`

       DOCTOR providers fail the seat's secrets store could not be read: NOVA_SECRETS_STORE and NOVA_SECRETS_SEAT do not name the seat's store fix: nova-secrets names --store <dir> --as <seat> (nova-up --local writes the seat's store and its environment)

    Expected: the fix line's first command to be the thing that makes the check pass.
    `nova-secrets names` is a listing verb over a store that does not exist; the setter is
    the parenthetical `nova-up --local`, which is named second and without its flags.
    Grade: NEXT.

## What worked

The banner answers what the tool does, how it works, where its state lives (nowhere) and
its exit table, and `help`, `-h` and `--help` all print it at exit 0. Every verb answers
`-h` at exit 0 before reading anything. The refusals are one line in one grammar and name
their inputs: an unknown check names all five (`RUN REFUSED: no check named "nope"; the
checks are gosdk, providers, secrets, self, tailnet`), an unknown flag names all four, a
`--check` with no value is refused rather than defaulted, and `version extra` refuses the
operand finding 1 shows `run` dropping. `--local` prints the spec's
`DOCTOR local skipped=...` line and runs the same checks minus the fleet ones. `--strict`
turns a warn into exit 1 and the `--json` object's `exit` matches the line's exit in every
run; `--json` is the same value as the lines, `skipped` included. Checks run in name order
and a fail never stops the others.

On the scratch store every check reached its `ok` and every failure named its evidence and
one fix line: the missing age key, its mode, its directory's mode, the missing `.sops.yaml`,
the unreadable inventory, the out-of-release tool, the unsealed provider key. The secrets
check prints names and modes only, never a value. On its first real run the tool found real
defects in this machine's environment (three PATH tools that do not answer `version`, the
unset seat secrets store, the unreadable machine inventory). It changes nothing: the
scratch store and key were byte-identical after every run.

## Gate

Run on the Linux bench at this tip:

    go test -count=1 -timeout 600s ./internal/docs ./internal/ci

    ok  	github.com/mas-bandwidth/nova-tools/internal/docs	2.242s
    ok  	github.com/mas-bandwidth/nova-tools/internal/ci	12.069s

    go test -count=1 -timeout 600s ./internal/docs -run TestDocsTreeIsConsistent

    ok  	github.com/mas-bandwidth/nova-tools/internal/docs	0.024s [no tests to run]

The card's named test `TestDocsTreeIsConsistent` does not exist in
`./internal/docs` at this tip, so that run passes with nothing to run; the docs package's
own walk (terminology, links, maps, transcripts) is the check this report answers, and both
packages are green.

## Not done

The card's STEP 2 (write the failing test first) and STEP 3 (implement at the cause) are
not done: the card's STOP says a finding is never fixed here, only recorded, and no code
changes are in this card. Only `docs/dogfood/2026-10-06/opencode-2/doctor.md` was added.
The tool has no store of its own, so every check was driven through the environment and the
scratch secrets directory; no live fleet, tailnet or provider route was used.

READ 5/10 — the banner is honest about what the tool is, every verb answers `-h` at exit 0
with its effect, and the refusals name their inputs and the nearest names; but the
normative page documents one of the five checks, the flag value slot reads `<help run>`,
the banner advertises a `--max` no verb has, `help --json` shows a different page than
`help`, the `example:` block is one line that exits 2, and the README's sentence is not the
banner's line 1.

USE 5/10 — every check reached its `ok` and its `fail` with evidence and one fix line on a
real scratch store, the exit table and the JSON `exit` agreed, and `--local`/`--strict`
behaved as their help says; but operands are silently dropped (`run extra`, `./go.mod`),
`help run extra` runs the checks, the unknown-verb refusal advertises a file doorway that
does not exist, `gosdk` fails a healthy toolchain outside a checkout, and `providers`
counts an unchecked route as checked.

urgent=4 next=10
