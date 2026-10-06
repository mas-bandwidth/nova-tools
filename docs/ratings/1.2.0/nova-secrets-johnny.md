# nova-secrets READ and USE rating, nova-tools 1.2.0

Rater: Claude Opus 5.5 in Claude Code, a sprint worker on a friend's re-rate card; the rating is this worker's, not the friend's
Build: 32357608f331
READ: 6.5/10
USE: 6/10

No v1.2.0 tag exists yet. The build rated is the head of sprint/mechanical-2026-10-02, built and run on a Linux bench with real sops 3.13.3 and age, in a scratch directory with HOME pointed into it: no live store, no server, no ssh, no gh call. `$SCR` below is that scratch directory.

## Reasons

READ. The banner says what the tool is in one line, then how the store is shaped (a git working copy, `.sops.yaml`, `recovery.pub`, one sealed `<seat>.yaml` per seat) and what a seat is. It gives a `setup:` block, a `first value:` line and an example per verb. Every verb answers `-h` with its usage, its example, an `effect:` line naming the kind of act (inspection, local write, store write, delivery), its flags with `(required)` marked, and the store prerequisite spelled out with the why and the commands that fix each refusal. Required flags are named together; unknown flags list the verb's flags; unknown verbs list the verbs. That is the shape an AI wants.

What costs the score. The page leads a cold reader into a wall. The banner's `first run:` line says setup makes a store and the first value is sealed with `--stdin`; run in that order, the `first value:` line is refused, because setup leaves no `ada.yaml` and seal never writes a new seat (cmd/nova-secrets/main.go:114, internal/secrets/seal.go:31). The refusal says to use `seat add`, which needs a source seat that already holds values, and `names` and `check` on the same store say the opposite, `seal writes a seat's first value` (internal/secrets/seatfile.go:190). Three verbs give two contradicting next steps and neither one runs. The test that pins the `first value:` line runs it against a store that already holds the seat (cmd/nova-secrets/verbhelp_test.go:102), so the gap between the two blocks is never run. Every example after `setup:` hard-codes `/opt/homebrew/bin/sops` (cmd/nova-secrets/main.go:116), which the `setup:` block itself does not use, and the missing-binary refusal says `run: brew install sops` (internal/secrets/sops.go:46) on Linux. `seat -h` still prints the root banner. The exit-code paragraph is repeated on every verb, so `names -h` and `keygen -h` describe exit 1 as `check found the store red` and exit 125 for exec. The `--dry-run` line on `place -h` talks about seal and seat inject (cmd/nova-secrets/main.go:186). The package comment still says four verbs (internal/secrets/secret.go:2), and the spec still says about two hundred lines of Go (docs/SPEC-SECRETS.md:34); the command and package are 6122 lines before tests.

A 10 is a `setup:` block and a `first value:` line that run one after the other, as printed, on any platform, with one answer to "how does a new seat get its first value".

USE. The delivery is exact. With a seat file in place, `exec --only GH_TOKEN` gave the child `GH_TOKEN` and left `API_KEY` unset; `--only all` gave all three; the tool's own lines carry no value, `--require` excluded by `--only` is refused before the child runs (exit 125), and `sh -c "exit 7"` comes back as exit 7. `names` and `names --max 1 --json` agree, the JSON carrying a `more` row with the remedy. `check` prints one OK line with the counts, and one commit ahead of upstream turns it red, exit 1. `keygen` refuses to overwrite a key. `seat add` re-sealed a value from one seat into a new one, the new seat's key opened it through `exec`, and `gate` approved the change. `seat inject --dry-run` and `place --dry-run` print PLAN lines and write nothing. A multi-line value is refused at `seal`.

What costs the score. There is no road from an empty directory to a first value with the tool alone. After setup, `seal` refuses the absent seat file, and `seat add --from recovery` refuses because `recovery.yaml` is absent too. The hand road, a `{}` seat file made with `sops --encrypt`, then `seal`, fails as `sops encrypt failed: exit 2 ... the usual cause is a recipient ... such as keygen's <recovery key> placeholder`: the recipients were right; seal edits the YAML line by line and appends `GH_TOKEN: ...` after `{}` (internal/secrets/seal.go:633), which is not YAML, and the message points at the wrong cause. Only a seat file seeded with a dummy key took a seal. `seal --no-pr` then leaves the value on a `seal/` branch; `exec` straight after refuses `--only names key(s) not in secrets/ada.yaml: GH_TOKEN`, and the NOTE's next step is a pull request a local bare remote cannot open, so the value reached `exec` only after a hand `git merge --ff-only` and push. `seat inject --no-pr` prints its OK line and no such NOTE at all. A key that is not this seat's still fails as `sops failed: exit 128 (transcript withheld: run 'sops -d ...')` and never says the key cannot open the file. `names --max nope` still ends with no `run:` line. `gate --base nope --head nope` still prints `GATE REFUSE rule=0 file=: --base nope ...`, one problem and its own grammar. `place --dry-run` on a seven-column registry still plans `path=linux/amd64/.config/nova-secrets/GH_TOKEN.env` and says DRY-RUN OK. `check` one commit ahead says `run: git -C ./secrets pull --ff-only`, which does nothing on a store that is ahead. `placed` on a receipts directory that does not exist prints `count=0`, exit 0.

Not tried: `seal` and `seat inject` without `--no-pr` or `--dry-run` (they push and call gh), `place` without `--dry-run` (it runs ssh). Judged from their help and their dry runs.

A 10 keeps this delivery and these refusals, gives a new store its first seat in one verb that runs after `setup:`, writes the seat file with a YAML encoder, finishes `--no-pr` on a local store, and names a wrong key as a wrong key.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | cmd/nova-secrets/main.go:114 | The banner's `first value:` line, run right after its `setup:` block, is refused: `ada.yaml does not exist in the store; a new seat is given its first values by seat add, never by seal`. `seat add` needs a source seat that holds values, and the store has none, so no verb gives the first seat its first value. | Give the first seat a road: `seat add` with no `--from` writes an empty sealed file for a seat the caller holds the key of, or setup writes it; put that line in `setup:`. | M |
| 2 | internal/secrets/seatfile.go:190 | `names` and `check` on a seat with no file say `seal writes a seat's first value: run: nova-secrets seal ...`; `seal` on the same store says never by seal, use seat add (internal/secrets/seal.go:31). Two verbs, two contradicting next steps, neither runs. | One remedy for an absent seat file, the verb from finding 1, printed by all three. | S |
| 3 | cmd/nova-secrets/verbhelp_test.go:102 | The test that runs the `first value:` line builds a store already holding the seat, so the setup-then-first-value gap in finding 1 is never run. | Run `first value:` in the directory `TestTheHelpSetupBlockRunsAsPrinted` leaves, with real sops and age where present. | S |
| 4 | internal/secrets/seal.go:633 | `sealApply` edits the decrypted YAML line by line and appends `NAME: value`. A seat file holding `{}` becomes `{}` plus a key, which sops refuses (exit 2). | Parse the decrypted file as a YAML map, set the key, and marshal it. | S |
| 5 | internal/secrets/seal.go:627 | The encrypt failure in finding 4 says `the usual cause is a recipient ... such as keygen's <recovery key> placeholder`; the recipients were correct, and the transcript is withheld, so the reader is sent to the wrong file. | Validate the plaintext before the encrypt and say what is wrong with it; keep the recipient hint only when a recipient fails age1 parsing. | S |
| 6 | cmd/nova-secrets/main.go:116 | The `first value:` line and every example hard-code `/opt/homebrew/bin/sops` and `/opt/homebrew/bin/age-keygen`, while `setup:` and every flag line say `command -v`. On Linux the printed `first value:` fails as `sops binary /opt/homebrew/bin/sops is absent`. | Use `"$(command -v sops)"` everywhere, as the exec refusal's own example already does. | S |
| 7 | internal/secrets/sops.go:46 | A missing sops or age-keygen says `run: brew install sops` (and `brew install age`, line 78) on every platform. | Name the binary and its project page, or pick the line by GOOS. | S |
| 8 | internal/secrets/seal.go:210 | `seal --no-pr` leaves the value on a `seal/` branch, and `exec` right after refuses `--only names key(s) not in secrets/ada.yaml`. The NOTE's next step is a pull request a local bare remote cannot open; only a hand `git merge --ff-only` and push reached exec. | On a store whose remote is local, print the fast-forward and push lines, or add `--merge` that fast-forwards the store's branch. | M |
| 9 | internal/secrets/seatinject.go:180 | `seat inject --no-pr` prints `SECRETS SEAT INJECT OK ... committed branch=seal/...` and no NOTE that exec does not see the value yet, where seal prints one. | Print seal's NOTE from the one helper both verbs share. | S |
| 10 | internal/secrets/sops.go:142 | A real age key that is not this seat's fails as `sops failed: exit 128 (transcript withheld: run 'sops -d secrets/ada.yaml' to inspect)`; it never says the key cannot open the file. | Compare the key's public line with the file's recipients first and say `this key is not a recipient of ada.yaml`, naming the right `--key` or keygen. | S |
| 11 | internal/nsprint/verbflag/verbflag.go:126 | `names --max nope` says what `--max` wants and ends with no `run:` line; a bad seat name on the same verb ends `run: nova-secrets names -h`. | Append `run: nova-secrets <verb> -h` to the bad-value line. | S |
| 12 | internal/secrets/gate.go:261 | `gate --base nope --head nope` prints `GATE REFUSE rule=0 file=: --base nope does not name a commit`, one problem, in its own grammar, beside `SECRETS GATE REFUSED` for a missing flag. | Print `SECRETS GATE REFUSED` with every problem the run can judge and a `run:` line; keep the rule number as a field. | M |
| 13 | internal/secrets/place.go:179 | `place --dry-run` on the seven-column registry `gate` reads plans `path=linux/amd64/.config/nova-secrets/GH_TOKEN.env` and prints DRY-RUN OK: column three is a home for place and os/arch for gate, under one flag name. | Two flag names, and refuse a row whose third field is not an absolute directory. | M |
| 14 | internal/secrets/git.go:107 | One commit ahead of upstream, `check` says `run: git -C ./secrets pull --ff-only`, which does nothing on a store that is ahead; `exec -h` gives the same line for behind and ahead. | Say which: behind gets `pull --ff-only`, ahead gets `push` or `reset --hard <upstream>` with the warning. | S |
| 15 | cmd/nova-secrets/seat.go:26 | `seat -h` prints the root banner, not the seat subverbs. | Print the two subverb usage lines and `run: nova-secrets seat add -h`. | S |
| 16 | cmd/nova-secrets/main.go:95 | The one exit-code paragraph is printed under every verb, so `names -h` and `keygen -h` describe `check found the store red` and exec's 125. | Print each verb's own exit codes. | S |
| 17 | cmd/nova-secrets/main.go:186 | The `--dry-run` line on `place -h` says `seal and seat inject read the store at HEAD`. | One dry-run line per verb, about that verb. | S |
| 18 | internal/secrets/place.go:262 | An unknown machine is refused as `machine nope is not in the fleet registry m3.tsv; add it there`, with no `run:` line and no list of the machines the file holds. | Name the machines the file holds and end with `run: nova-secrets place -h`. | S |
| 19 | internal/secrets/place.go:397 | `placed --receipts ./receipts` on a directory that does not exist prints `SECRETS PLACED OK machine=bench count=0`, exit 0. | Say the receipts directory is absent on the OK line, or refuse when `--receipts` was given and is absent. | S |
| 20 | internal/secrets/secret.go:2 | The package comment says the package provides four verbs; the banner lists eleven. | Name every verb in the banner's order, or none. | S |
| 21 | docs/SPEC-SECRETS.md:34 | The spec says about two hundred lines of Go; the command and package are 6122 lines before tests. | Delete the count or pin it with a test. | S |
| 22 | cmd/nova-secrets/exec.go:77 | exec still refuses one redis-cli write of another tool's working-count key, a special case that is not about secrets. | Move the refusal to the tool that owns the key, or name it in the banner. | M |
| 23 | docs/CLI.md:2067 | The nova-secrets section still opens on `gate`, a throwaway store that never seals a value or starts a child. | Open on setup, first value and exec, the same lines the banner prints. | M |

## Good, keep

`exec` puts exactly the `--only` names in the child, prints no value on either stream, passes the child's exit code through, and refuses with 125 before the child runs.

Every verb's `-h` carries an `effect:` line naming the kind of act, and `exec -h` and `check -h` spell out the store prerequisite, why it exists and the git lines for each refusal.

`names --json` is the text lines as one object, with a `more` row carrying the remedy.

`seat add` writes the rule and the re-sealed file, opens only for the new key and the recovery key, and says what to commit next; `gate` approves that change offline.

Required flags, unknown flags and unknown verbs are each named in one line with the full list and a `run:` line.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| the first keygen rule carries `<recovery key>` and keygen --store refuses until .sops.yaml exists | CHANGED | the `setup:` block writes recovery.pub first and keygen --store prints a full rule; the next line, `first value:`, is now refused instead |
| seal --no-pr leaves the value off the branch exec reads | STILL THERE | `exec --only GH_TOKEN` after `seal --no-pr` refuses `--only names key(s) not in secrets/ada.yaml: GH_TOKEN` |
| a wrong key gives sops failed: exit 128 | STILL THERE | exec with another seat's key prints `sops failed: exit 128 (transcript withheld: run 'sops -d secrets/ada.yaml' to inspect)` |
| place plans a path under the os/arch column | STILL THERE | `place --dry-run` on a seven-column file prints `path=linux/amd64/.config/nova-secrets/GH_TOKEN.env` and DRY-RUN OK |
| the gate names one problem at a time in its own grammar | STILL THERE | `gate --base nope --head nope` prints only `GATE REFUSE rule=0 file=: --base nope does not name a commit` |
| the bad-value refusal lacks a next command | STILL THERE | `names --max nope` ends `(default 20)` with no `run:` |
| seat -h prints the root banner | STILL THERE | `nova-secrets seat -h` prints the full banner |
| a stale package doc | STILL THERE | internal/secrets/secret.go:2 says four verbs |
| two hundred lines of Go | STILL THERE | docs/SPEC-SECRETS.md:34; 6122 lines before tests |
| a table special case inside exec | STILL THERE | cmd/nova-secrets/exec.go:77 |
| CLI.md opens on the review verb | STILL THERE | docs/CLI.md:2067 `### Gate a seat pull request` is the first heading |
| three refusal shapes | CHANGED | flag, exec and seat refusals now all read `SECRETS <VERB> REFUSED`; only gate's `GATE REFUSE` is left |
