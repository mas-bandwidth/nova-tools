# nova-secrets dogfood — antigravity (zhi), 2026-10-06

Read as a stranger: only `nova-secrets -h`, `nova-secrets help`, `nova-secrets
<verb> -h`, `nova-secrets help <verb>`, and the tool's page `docs/SPEC-SECRETS.md`.
Built from the staged checkout at df30ce088344588bf75a86168057f468710ae943 and
used as `nova-secrets v1.0.1-0.20261006202650-df30ce088344 linux/amd64
go1.26.6`: every verb once with its real flags in a scratch store under a scratch
`$HOME`, invented values only, the refusals too. The store was made by running the
banner's `setup:` block as printed (it needed a one-off `git config user.email`
first); because its `first value:` block refuses (finding 1), the first seat file
`ada.yaml` was made by a direct `sops --encrypt --age <ada.pub>,<recovery.pub>`
pipe so the remaining verbs had a store to read.

## Findings

1. `printf '%s' 'a-token-value' | nova-secrets seal --store ./secrets --as ada --key ~/.config/nova-secrets/ada.key --sops "$(command -v sops)" --name GH_TOKEN --stdin --no-pr`
   (the banner's own `first value:` block, run right after the banner's `setup:`
   block made `./secrets`, `.sops.yaml` and the `ada` key)
   Printed:
   ```
   SECRETS SEAL REFUSED: ada.yaml does not exist in the store; a new seat is given its first values by seat add, never by seal (SPEC-SECRETS rule 12); run: nova-secrets seal -h
   ```
   exit 2. I expected the printed first-run path to produce a store with a value:
   the banner says `first run: setup: makes a store from an empty directory`, and
   the `first value:` line then seals one. `setup:` writes only `.sops.yaml` and
   the key, so the seat file `seal` must decrypt first never exists, and the
   `run:` breadcrumb sends the reader to `seal -h` — the help of the verb that
   just refused — instead of the verb its own reason names. A fresh store has no
   path through the tool's verbs to its first seat file.
   Grade: URGENT (help that lies; a refusal whose remedy is a dead end).

2. `nova-secrets names --store ./secrets --as nope`
   Printed:
   ```
   SECRETS NAMES REFUSED: seat file nope.yaml is absent in store ./secrets; its seats are ada: pass --as one of them; seal starts a new seat: run: nova-secrets seal --store ./secrets --as nope --key <path> --sops <path> --name <NAME>
   ```
   exit 2. I expected the breadcrumb to name `seat add`, the one verb that can
   write a new seat file, because `seal` refuses exactly that (finding 1, SPEC
   rule 12). Two refusals in one binary contradict each other about which verb
   creates a seat, and `seat add` itself needs a `--from` source seat the empty
   store does not have, so the advertised first run has no next step at all.
   Grade: URGENT (help that lies; the remedy cannot be run).

3. `printf '%s' 'a-token-value' | nova-secrets seal --store ./secrets --as ada --key ~/.config/nova-secrets/ada.key --sops /opt/homebrew/bin/sops --name GH_TOKEN --stdin --no-pr`
   (the `first value:` line as printed; every verb's `from nova-secrets help:`
   example, `check`, `exec`, `seal`, `place`, `seat add`, `seat inject`, carries
   the same `/opt/homebrew/bin/sops` and `/opt/homebrew/bin/age-keygen`)
   Printed:
   ```
   SECRETS SEAL REFUSED: sops binary /opt/homebrew/bin/sops is absent or not executable; run: brew install sops
   ```
   exit 2. I expected the `example:` block to run as printed (the repo standard's
   ONBOARDING point 1 and 6 hold the example to that), on the Linux bench the
   card names, not only on a Homebrew mac; the sibling `setup:` block already
   writes `$(command -v age-keygen)`, so the two halves of the same banner
   disagree about how a path is named.
   Grade: NEXT (a one-flag substitution fixes it; the flag's own help says
   `as printed by: command -v sops`).

4. `nova-secrets seat -h` (and `nova-secrets help seat`)
   Printed:
   ```
   nova-secrets: encrypted secrets in a git repository, handed to one command at a time

   how it works: the store is a git working copy holding a .sops.yaml (one rule per
   ```
   the whole ~100-line root banner, exit 0. I expected the `seat` group's own
   help naming its two verbs and their effect, the way `<tool> <group> -h` does
   elsewhere and the way `seat add -h` and `seat inject -h` do for the leaves; a
   stranger typing `seat -h` learns only what the root banner's usage list
   already showed.
   Grade: NEXT (unclear help).

5. `printf '%s' 'a-third-value' | nova-secrets seal --store ./secrets --as ada --key ~/.config/nova-secrets/ada.key --sops "$(command -v sops)" --name NEW_KEY --stdin --no-pr`
   Printed:
   ```
   SECRETS SEAL OK name=NEW_KEY seat=ada committed branch=seal/ada-NEW_KEY-20261006-203849
   SECRETS SEAL NOTE exec and check read the store's own branch, which does not hold this value yet; next: git -C ./secrets push -u origin seal/ada-NEW_KEY-20261006-203849, then open and merge its pull request
   ```
   exit 0. I expected the tool's own Output grammar — "every `SECRETS` line a
   verb prints comes before its `OK` line" — so the `next:` instruction would be
   a `NOTE` or `NEXT:` line above `SECRETS SEAL OK`; as printed, a caller that
   stops reading at the verdict line misses the one instruction that makes the
   committed value reachable.
   Grade: NEXT (receipt ordering; the value is committed as promised).

6. `nova-secrets check --store ./secrets --as ada --key ~/.config/nova-secrets/ada.key --sops "$(command -v sops)"`, with an untracked `secrets/notes.yaml` holding one plaintext-looking line
   Printed:
   ```
   SECRETS CHECK FAIL notes.yaml: untracked file contains unencrypted secret
   SECRETS CHECK FAIL as=ada files=1 failed=1 shown=1
   ```
   exit 1. I expected the invariant-7 failure to name the repair as the spec's
   check section promises ("its output naming the file and the repair"), for
   example removing the untracked file or sealing it; the line names the file and
   stops, so a cold reader has a red with no next command.
   Grade: NEXT (a red with no remedy).

7. `nova-secrets check --store ./secrets --as ada --key ~/.config/nova-secrets/ada.key --sops "$(command -v sops)" --json`
   Printed:
   ```
   SECRETS CHECK REFUSED: unknown flag --json; the flags of check are --as, --key, --max, --sops, --store; run: nova-secrets check -h
   ```
   exit 2; `gate --json` and `placed --json` refuse the same way, and the banner's
   flag list says `--json names only`. I expected the repo standard's one shape
   ("every verb accepts `--json`", "one output structure, two renderings") so a
   caller can parse `check`, `gate` and `placed` without a bespoke reader; the
   JSON `names` prints is good, the other inspection verbs have none.
   Grade: NEXT (a missing flag, against the standard the tree states).

8. `nova-secrets placed --machine nope --receipts ./receipts`
   Printed:
   ```
   SECRETS PLACED OK machine=nope count=0
   ```
   exit 0. I expected a `NOTE` or a refusal distinguishing "this machine has no
   receipts" from "no such machine / never placed here"; `OK` for a name that was
   never in a registry and never received a placement reads as a pass, and the
   same line is printed for a real machine with an empty receipts directory.
   Grade: NEXT (friction; the answer is literally true and nothing is hidden).

## What the tool got right

- `nova-secrets bogus` names every verb in one line; an unknown flag names every
  flag of the verb; `version extra` refuses in one line.
- Every verb answers `-h`, `--help` and `help <verb>` at exit 0 before reading
  anything, and every leaf's `-h` carries its effect and the exit table.
- `names --max 1` prints the `MORE shown=1 total=2 run: ... --max 0` line and
  `--max 0` prints all; `--json` renders the same value; `--max -1` refuses with
  the unit.
- `check` is a real wall: green prints `recipients=2 files=1 sealed=1 mine=1
  foreign=0 clear=0 head=<sha>`; an untracked plaintext, a plain key appended
  outside `unencrypted_regex`, and a `HEAD` ahead of its remote-tracking ref each
  go red, and the last names `git -C <store> pull --ff-only`.
- `exec` refuses at 125 before the command starts for a missing `--only`, an
  `--only` name the file lacks, a missing `--require`, and no `--`, each with a
  pasteable remedy; `--only all` and `--only NAME` deliver exactly those keys.
- `keygen` never overwrites and refuses a key directory that is not 0700 naming
  `chmod 700`; `seal`/`place`/`seat inject` `--dry-run` really decrypt, print the
  plan, write nothing and end `DRY-RUN OK`.
- `seat add` writes the rule before the encrypt, names the value count, refuses an
  existing file and a `--pub` another rule already carries; `seat inject` re-seals
  only `--only` from the source and keeps the target's other names; `gate` judged
  the seat-add diff `GATE APPROVE files=2 machines=-`, refused a registry whose
  seat column read `ada` for a `bo` rule (`rule=2 check=4`), and refused a
  3-column registry naming every field it wanted.
- `place --dry-run` prints the ssh target, remote path and `mode=0600` and the
  receipt it would add without running ssh, and `place --machine nope` refuses
  naming the file to add the row to.

READ 6/10 — the banner, each leaf's `-h` and the refusal grammar are dense and
mostly true, and the five-verb model is legible; the `setup:`/`first value:`
path that cannot run, the two verbs that disagree about a new seat, the missing
`seat` group help and the Homebrew-only examples are what keep it low.

USE 7/10 — every verb ran for real on a scratch store, the dry runs are honest
and the one-turn remedies are what a launcher needs; but a stranger following the
printed first run is stopped at the first value with no tool path to a seat file,
and only `names` can be parsed as JSON.

NOT DONE: a real `place` delivery was not exercised — no ssh target answers from
this bench and starting a server is out of scope — so `place`/`placed` are graded
on the dry run, the refusal and an empty receipts directory. The card's named
test `TestDocsTreeIsConsistent` does not exist in `./internal/docs` at this tip
(see the gate below).

urgent=2 next=6
