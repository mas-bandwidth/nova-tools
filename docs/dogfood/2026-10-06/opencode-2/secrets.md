# nova-secrets dogfood, 2026-10-06 (opencode-2)

Tool: nova-secrets. Build: `nova-secrets v1.0.1-0.20261007153756-abb9bfecc729 darwin/arm64 go1.26.6`
(the tree's `sprint/mechanical-2026-10-02` tip; binary built on a Linux bench, the store and
every run inside the job directory). Read cold, from `nova-secrets help`, every verb's `-h` and
`docs/SPEC-SECRETS.md` only, with no source read. Every verb ran with its real flags against one
scratch store — a git working copy with a local bare `origin`, one seat `ada` and then a second
`bo`, invented values only (`GH_TOKEN`, `API_KEY`). `place` ran against a stand-in `ssh` that
writes the value it receives on stdin to a file, because no fleet machine was reachable and none
should have been touched; nothing else was simulated. The store could only be given its first
value by running `sops` by hand, because the tool's own documented first-value command refuses
(finding 1); every later seal, seat and gate step went through the tool. No repository file was
changed by the run; this report is the only file it adds. 3 urgent, 6 next.

## 1. The documented cold start cannot put a first value in a new store — URGENT

**Command** (the `first value:` block of `nova-secrets help`, run right after its `setup:` block,
which itself ran as printed):

    printf '%s' 'a-token-value' | nova-secrets seal --store ./helpstore --as ada \
      --key keysH/ada.key --sops /opt/homebrew/bin/sops --name GH_TOKEN --stdin --no-pr

**Printed:**

    SECRETS SEAL REFUSED: ada.yaml does not exist in the store; a new seat is given its first values by seat add, never by seal (SPEC-SECRETS rule 12); run: nova-secrets seal -h

exit 2.

**Expected:** the banner says `first value: seals one with --stdin, the path a harness takes`, and
the usage calls `seal` the store write that folds a value into a seat file; the `setup:` block
above it just made `./secrets` with `ada`'s rule. This line should put `GH_TOKEN` in the new store.
It refuses instead, and the door it names does not open either: `seat add` refuses because it needs
a `--from` seat whose file already holds values, and a store that has only run `keygen` has no seat
file at all (finding 2). SPEC-SECRETS calls the bootstrap "the circle it breaks", but nothing in
the documented first run breaks it: a stranger following `help` top to bottom reaches the end with
no way to put a first value in.

**Grade:** URGENT

## 2. The absent-seat refusal sends the reader to `seal`, which refuses that call — URGENT

**Command** (the remedy `names` printed on the fresh store from finding 1):

    nova-secrets names --store ./helpstore --as ada

**Printed:**

    SECRETS NAMES REFUSED: seat file ada.yaml is absent: store ./helpstore holds no seat file yet; seal writes a seat's first value: run: nova-secrets seal --store ./helpstore --as ada --key <path> --sops <path> --name <NAME>

exit 2. With one seat already present, `check` for a second prints the same loop:

    SECRETS CHECK REFUSED: seat file bo.yaml is absent in store ./secrets; its seats are ada: pass --as one of them; seal starts a new seat: run: nova-secrets seal --store ./secrets --as bo --key <path> --sops <path> --name <NAME>

**Expected:** a remedy that runs. `seal` answers exactly that printed command with finding 1's
refusal ("a new seat is given its first values by seat add, never by seal"), and `seat add` answers
with `SECRETS SEAT ADD REFUSED: source seat file ada.yaml is absent in ./helpstore; run:
nova-secrets seat add -h`. Both lines assert that `seal` writes or starts a new seat; it does not,
and the refusal that would fix the call is a command that cannot. The reader is handed a loop
instead of a remedy.

**Grade:** URGENT

## 3. `gate` prints `GATE FAILED` at exit 1 where the spec and `docs/CLI.md` promise `GATE REFUSE` at exit 2 — URGENT

**Command:**

    nova-secrets gate --store ./secrets --base main --head gate-extra

**Printed:**

    GATE FAILED rule=0 check=3 file=notes.txt: only .sops.yaml, README.md and seat .yaml files may change

exit 1. The other refusals agree (`GATE FAILED rule=1 check=2 file=ada.yaml: the seat file was not
written by a nova-secrets verb; ...`, `GATE FAILED rule=0 check=5 file=ada.yaml: the seat file is in
the store at the base and gone at the head; ...`), all exit 1.

**Expected:** SPEC-SECRETS, the normative page, gives the refusal line as
`GATE REFUSE rule=<n> check=<k> file=<f>: <why>` and says the gate "refuses, at exit 2" for every
numbered check; `docs/CLI.md` repeats it: "It prints `GATE APPROVE files=<n> machines=<registry|->`
at exit 0, or one `GATE REFUSE rule=<n> check=<k> file=<f>: <why>` line at exit 2." The binary and
its own `-h` exit table instead say `GATE FAILED` and exit 1. A CI job written to the store's own
spec — `test "$?" -eq 2`, or a grep for `GATE REFUSE` — reads a real refusal as a pass. One of the
two contracts is wrong, and the stranger codes to the page.

**Grade:** URGENT

## 4. `exec --only` names a key the file lacks and leaves its own "the names the seat holds:" clause empty — NEXT

**Command:**

    nova-secrets exec --store ./secrets --as ada --key keys/ada.key \
      --sops /opt/homebrew/bin/sops --only NOPE -- /usr/bin/printenv GH_TOKEN

**Printed:**

    SECRETS EXEC REFUSED: --only names key(s) not in secrets/ada.yaml: NOPE; the names the seat holds: run: nova-secrets names --store ./secrets --as ada

exit 125.

**Expected:** the seat holds `API_KEY`, `GH_TOKEN` and `NOVA_SECRETS_WRITTEN_BY`; the refusal
promises "the names the seat holds" and then prints none, so a reader reads it as an empty seat.
The sentence should carry the names it promises (or drop the clause), because the whole point of
the one-turn refusal is to make the corrected call pasteable; the remedy command does still work.

**Grade:** NEXT

## 5. The `example:` block carries a placeholder (`$BO_PUB`) and files the setup never makes, so it does not run as printed — NEXT

**Command:**

    nova-secrets help

**Printed** (the `example:` block, first three lines after the heading):

    example:
      nova-secrets keygen --as ada --key ~/.config/nova-secrets/ada.key --age-keygen /opt/homebrew/bin/age-keygen
      nova-secrets names  --store ./secrets --as ada

**Expected:** the onboarding standard says an `example:` block carries no placeholder and its lines
run as printed. `$BO_PUB` is undefined, so `seat add ... --pub $BO_PUB` refuses on `--pub is not an
age public key ... got 0` when pasted; `place ... --machines ./fleet.tsv` refuses because nothing
makes `./fleet.tsv`; `~/.config/nova-secrets/worker.key` and `lead.key` are never made. The `setup:`
block does run as printed (verified), so the fix is to move the example onto the store `setup:`
makes and give the reader the key values, or drop the lines that need a second bench.

**Grade:** NEXT

## 6. `--json` is on `names` only; `placed`, `check` and `exec` refuse the flag — NEXT

**Command:**

    nova-secrets placed --machine bench-a --receipts ./receipts --json

**Printed:**

    SECRETS PLACED REFUSED: unknown flag --json; the flags of placed are --machine, --receipts; run: nova-secrets placed -h

exit 2. `check --json` prints `unknown flag --json; the flags of check are --as, --key, --max,
--sops, --store`, and `exec --json` the same under its own name. The top-level help does say
`--json names only`, so the tool is consistent with itself.

**Expected:** the repository's one-shape rule is that every verb builds one result value and offers
it as lines or as `--json`; nova-secrets is the one tool with a special case, and `placed` (a
listing, with `items` and a total) is the verb that most looks like it has one. A caller parsing
results must special-case the tool, and the standard's `TestEveryCommandMeetsTheOnboardingStandard`
family has no home for that. Either the help should say why only `names` has the second rendering,
or the other verbs should take it.

**Grade:** NEXT

## 7. `exec` with a missing `--only` does not also report a bad key-file mode — NEXT

**Command:**

    nova-secrets exec --store ./secrets --as ada --key keys/bad.key \
      --sops /opt/homebrew/bin/sops -- /usr/bin/printenv

**Printed:**

    SECRETS EXEC REFUSED: missing --only <names|all>; example: nova-secrets exec --store ./secrets --as worker --key ~/.config/nova-secrets/worker.key --sops "$(command -v sops)" --only GH_TOKEN -- gh api user; run: nova-secrets exec -h

exit 125. `keys/bad.key` is `ada.key` at mode 0644, and every other refusal names that mode.

**Expected:** SPEC-SECRETS says one run reports every independent problem it can reach, in a
deterministic order — flags first, then the store, the key, the binary, the contents. The missing
`--only` and the key's mode are independent and both reachable here (the missing `--sops` case does
report both flags at once), so the reader should get both lines and fix the call in one turn,
instead of hitting the key mode on the next run.

**Grade:** NEXT

## 8. `seat -h` prints the whole banner, not the `seat` group's help — NEXT

**Command:**

    nova-secrets seat -h

**Printed:**

    nova-secrets: encrypted secrets in a git repository, handed to one command at a time

    how it works: the store is a git working copy holding a .sops.yaml (one rule per
    seat naming its recipients), a recovery.pub (the recovery key every file is also

exit 0, and the rest is the top-level help: all eleven verbs and all flags, none of the `seat add`
/ `seat inject` flags. `nova-secrets help seat` prints the same.

**Expected:** every verb and group answers `-h` with its own help; a reader who knows only the
`seat` group is sent the whole tree instead of the two subverbs and their flags (`--pub`, `--from`,
`--only`), so `nova-secrets seat add -h` has to be guessed. The exit 0 is right; the scope is not.

**Grade:** NEXT

## 9. `exec --only all` hands the bookkeeping mark `NOVA_SECRETS_WRITTEN_BY` to the child — NEXT

**Command:**

    nova-secrets exec --store ./secrets --as ada --key keys/ada.key \
      --sops /opt/homebrew/bin/sops --only all -- /usr/bin/env

**Printed:**

    SECRETS EXEC OK as=ada keys=3 only=all required=0 file=secrets/ada.yaml head=8172f50 cmd=/usr/bin/env
    GH_TOKEN=invented-gh-token-9271
    API_KEY=invented-api-key-5522
    NOVA_SECRETS_WRITTEN_BY=seal dev

exit 0.

**Expected:** `--only all` is described as "the key names to put in the command's environment ...
or all", and the seat holds two values plus the mark every verb writes into the file. The mark is
provenance for the gate, not a credential the caller sealed, so a harness that takes `all` (the
wide choice the spec makes a caller type out) receives a variable it never asked the store for and
the tool's own version in its environment. The names a reader sees with `names` and the names that
arrive should be separable, by excluding the mark from `all` or by naming it as other software.

**Grade:** NEXT

READ 5/10 — SPEC-SECRETS is unusually complete and every verb's `-h` names its effect class and
exit codes, but the first run the help prints cannot produce a value, the absent-seat remedy is a
loop, and the gate's documented line and exit code are not what the binary does.

USE 6/10 — once a store holds a seat every verb works cold, the dry runs are honest, and the
refusals are mostly one-turn; a brand-new store cannot be started by the documented path, and a CI
written to the gate's own contract reads its verdict as a pass.

urgent=3 next=6
