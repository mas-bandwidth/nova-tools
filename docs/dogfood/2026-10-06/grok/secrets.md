# nova-secrets: a cold dogfood run, 2026-10-06 (grok)

Tool: `nova-secrets`, built from `sprint/mechanical-2026-10-02` at `0920be8a2`
(`nova-secrets v1.0.1-0.20261007150459-0920be8a2fbf linux/amd64`). Read cold from
`nova-secrets -h`, `nova-secrets help`, every verb's `-h`, and `docs/SPEC-SECRETS.md`;
used against a throwaway store in this job's scratch directory on a Linux bench with
sops 3.13.3 and age-keygen v1.3.2. Every value is invented; no real key, token or
password was sealed or read. In the commands below, the scratch directory is written
`<scratch>`, and its `store/`, `keys/`, `receipts/` and `fleet.tsv` are the fixtures
the run made.

## Findings

### 1. The help's own first-run setup cannot create the store's first `.sops.yaml` — URGENT

Command, exactly as the `setup:` block of `nova-secrets help` gives it, in a store
directory that has `.git` and `recovery.pub` but no `.sops.yaml` (the state the block
itself creates just before this line):

```
nova-secrets keygen --as ada --key keys/ada.key --age-keygen "$(command -v age-keygen)" \
  --store store | sed -n 's/^SECRETS RULE   //p' > store/.sops.yaml
```

Printed (first 3 lines; the verb printed one line and exited 2):

```
SECRETS KEYGEN REFUSED: store <scratch>/store has no .sops.yaml; run: git clone <store url> <scratch>/store (a new store: git init it, then write its .sops.yaml from the rule block nova-secrets keygen prints)
```

Expected: the pipe writes the printed rule block, with the second recipient read out
of `recovery.pub`, which is what the same block says this line does. The refusal's own
remedy is circular: it says to write `.sops.yaml` "from the rule block `keygen` prints",
but `keygen --store` refuses until `.sops.yaml` exists, and the block `keygen` prints
without `--store` carries the literal `<recovery key>` instead of the store's key. The
only road left is `keygen` without `--store`, then hand-substituting the recovery key —
which is not the documented first run.

Grade: URGENT

### 2. `check` calls the verb's own clear mark an unencrypted secret in a freshly written, untracked seat file — URGENT

Commands: `seat add` leaves its output in the working copy (exit 0), then `check` runs
on the same store, on a branch with its upstream, before the commit:

```
nova-secrets seat add --store store --as dave --pub age14cv3yc4vs9xysk4hxdrf2797jky83mephr58xr2pr59l4akcscxqpdr3fn --from ada --only GH_TOKEN --key keys/ada.key --sops "$(command -v sops)"
nova-secrets check --store store --as dave --key keys/dave.key --sops "$(command -v sops)"
```

Printed (first 3 lines of `check`):

```
SECRETS CHECK FAIL dave.yaml: untracked file contains unencrypted secret
SECRETS CHECK FAIL .sops.yaml: working copy differs from HEAD commit tree (working copy blob differs from HEAD tree); uncommitted modifications in .sops.yaml
SECRETS CHECK FAIL .sops.yaml: working copy differs from git index; uncommitted changes in .sops.yaml
```

Expected: `dave.yaml` is sops-sealed, and its one line outside the ciphertext is
`NOVA_SECRETS_WRITTEN_BY: seat add dev`, the mark `seat add` puts in the clear and the
new rule's `unencrypted_regex` admits. `docs/SPEC-SECRETS.md` says the mark "is never a
'plain value' to the gate, check or seat inject". The two `.sops.yaml` lines are the
expected uncommitted-state answer; the `dave.yaml` line is a false "unencrypted secret"
on a sealed file. A reader who checks between `seat add` and the commit is told a file
holds a plaintext secret when it does not.

Grade: URGENT

### 3. The absent-seat refusal sends a new seat to `seal`, which refuses the same case — URGENT

Command:

```
nova-secrets names --store store --as zzz
```

Printed (first 3 lines; one line):

```
SECRETS NAMES REFUSED: seat file zzz.yaml is absent in store <scratch>/store; its seats are ada, bo: pass --as one of them; seal starts a new seat: run: nova-secrets seal --store <scratch>/store --as zzz --key <path> --sops <path> --name <NAME>
```

Following the remedy:

```
nova-secrets seal --store store --as zzz --key keys/ada.key --sops "$(command -v sops)" --name GH_TOKEN --stdin --no-pr
SECRETS SEAL REFUSED: zzz.yaml does not exist in the store; a new seat is given its first values by seat add, never by seal (SPEC-SECRETS rule 12); run: nova-secrets seal -h
```

Expected: the new-seat remedy names `seat add`, the verb the model says gives a new
seat its first values and the one `seat inject`'s absent-target refusal already names.
The clause "seal starts a new seat" is false, and the command it prints is refused by
the same tool with a pointer back to rule 12.

Grade: URGENT

### 4. `placed` prints its verdict before its items — NEXT

Command:

```
nova-secrets placed --machine bench-a --receipts receipts
```

Printed (first 3 lines):

```
SECRETS PLACED OK machine=bench-a count=1
SECRETS PLACED ITEM machine=bench-a secret=DEEPSEEK_API_KEY path=<scratch>/remotehome/.config/nova-secrets/DEEPSEEK_API_KEY.env file=ada.yaml head=9e5ef21 blob=4cf65be546d02c35ac6d01d4b381759f4fbbf25c stamp=2026-10-07T15:09:22Z
```

Expected: the items first, the verdict last, as `names` does (`SECRETS NAME` lines then
`SECRETS NAMES OK`) and as `docs/SPEC-SECRETS.md`'s own output grammar states — "Every
`SECRETS` line a verb prints comes before its `OK` line", and rule 11, "the `OK` line is
the last machine-readable line a verb prints". A caller that stops at the verdict reads
none of the receipts, though the page's `place`/`placed` sketch also shows `OK` first,
so the page and the rule disagree.

Grade: NEXT

### 5. Some refusals drop the `; run:` remedy field — NEXT

Commands and their first lines:

```
nova-secrets place --store store --as ada --key keys/ada.key --sops "$(command -v sops)" --machine no-such --secret DEEPSEEK_API_KEY --machines fleet.tsv --receipts receipts --ssh fakessh
SECRETS PLACE REFUSED: machine no-such is not in the fleet registry <scratch>/fleet.tsv; add it there

nova-secrets put
SECRETS REFUSED: run 'sops <store>/<name>.yaml' or 'sops set'; then git add, git commit, and a pull request the other collaborator approves.

nova-secrets delete
SECRETS REFUSED: run 'sops unset' or 'git rm', and rotate whatever the deleted value was.
```

Expected: one refusal grammar, `... REFUSED: <reason>; run: <remedy>`, so a machine
reader can take the remedy as a field. These three carry the remedy only as prose
inside the reason, and the `place` line names no command at all for adding the machine.
`docs/SPEC-SECRETS.md`'s grammar gives every refusal a `run:` field.

Grade: NEXT

### 6. A `--json` refusal carries `why`, not a `remedy` — NEXT

Command:

```
nova-secrets names --store store --as nope --json
```

Printed (first 3 lines; one line):

```
{"result":{"verb":"names","status":"refused","exit":2,"why":["seat file nope.yaml is absent in store <scratch>/store; its seats are ada, bo: pass --as one of them; seal starts a new seat: run: nova-secrets seal --store <scratch>/store --as nope --key <path> --sops <path> --name <NAME>"]},"facts":{}}
```

Expected: the page says the JSON `result` carries "on a refusal its reason and remedy",
and the one-output structure names the field `remedy`. The actual object has a `why`
array of prose and no `remedy`, so a JSON caller cannot read the remedy without parsing
the sentence; and the same false new-seat remedy of finding 3 rides in it.

Grade: NEXT

### 7. The help `example:` block does not run as printed on a Linux bench — NEXT

Commands, as the `example:` block of `nova-secrets help` prints them:

```
nova-secrets keygen --as ada --key ~/.config/nova-secrets/ada.key --age-keygen /opt/homebrew/bin/age-keygen
nova-secrets seat add --store ./secrets --as bo --pub $BO_PUB --from ada --only GH_TOKEN,DEEPSEEK_API_KEY --key ~/.config/nova-secrets/ada.key --sops /opt/homebrew/bin/sops
```

Printed (first 3 lines):

```
SECRETS KEYGEN REFUSED: age-keygen binary /opt/homebrew/bin/age-keygen is absent or not executable; run: brew install age
SECRETS SEAT ADD REFUSED: unexpected argument "ada"; seat add takes flags only; run: nova-secrets seat add -h
```

Expected: the onboarding standard's `example:` lines run as printed from the binary
alone. Two things stop them here: the age-keygen and sops paths are hardcoded to the
macOS Homebrew prefix on a Linux bench, and `$BO_PUB` is a variable the block never
defines, so the shell drops it and `seat add` reads `ada` as a positional argument. The
`setup:` block just above uses `"$(command -v …)"` and runs, so the example block can
use the same door.

Grade: NEXT

READ 7/10: the banner, per-verb help and the spec page answered what each verb is and
what it touches, with real refusals and remedies, but the first run the banner names is
the one that cannot work (finding 1) and the new-seat door it names is refused by the
tool itself (finding 3).
USE 7/10: every verb ran and did its job on a scratch store — exec delivered exactly the
`--only` names and passed the command's status through, check and gate judged a real
store and two real diffs, seal/seat add/seat inject committed to their branches, and
place wrote a receipt with no value on any line — but check lied about a freshly written
seat file (finding 2) and placed's output order hides its own items (finding 4).

urgent=3 next=4
