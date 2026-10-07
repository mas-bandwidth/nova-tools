# nova-secrets USE rating, nova-tools 1.1.0

Rater: Grok
Build: 86a5fb91119e
Score: 7/10

## Reasons

The first successful run is `nova-secrets keygen` for a new seat. It exits 0, writes the key at mode 0600, and prints the rule, a NEXT line, the OK line, then two plain lines that say where the key is and what to do with the public half. No private key is printed.

The job the tool exists for works, once the store is on the branch exec reads. A throwaway store, a local bare remote, and two sealed names: `exec --only` the first name makes the child report that name MATCH and the other UNSET; `--only` the second does the opposite; `--only all` reports both MATCH. The tool's own lines contain no value. `names` and `names --json` agree, including a MORE line when `--max 1` hides one of two names. `check` prints one OK line with the counts.

Four refusals: a bare `keygen` names every missing flag and ends `run: nova-secrets keygen -h`. A bare `exec` names every missing flag and the missing command, with an example. `names --bogus` names the flag and every flag that verb takes, then `-h`. `frobnicate` names the verb and lists the verbs. A bad seat name names the pattern and `-h`. A bad `--max` names what the flag wants and does not give a next command. `gate` with no refs names both missing refs in one line. `gate` with two refs that name no commit, and a short machines file, prints only the base line.

`--json` on `names` is the same facts as the lines, and a refusal is that object with a remedy. `exec --json` is refused as an unknown flag, which matches the root help (names only). `seal --dry-run` and `place --dry-run` print PLAN lines and DRY-RUN OK and write nothing. `seal --no-pr --dry-run` says push none. `place --dry-run` on a machines file `gate` accepts plans the remote path under the os/arch column and still says DRY-RUN OK.

Guessed: the first keygen rule contains the placeholder `<recovery key>`, and `keygen --store` refuses until `.sops.yaml` already exists, so the rule has to be edited by hand before it is a rule. `seal --no-pr` then says the next step is a push and a pull request. The offline upstream in the exec help is a local bare remote, which cannot open one, so the value stays off the branch until a fast-forward the receipt does not spell. Also guessed that `seat -h` would be the seat verbs; it prints the root banner. `help seat add` is the verb.

Not tried against a real service: `seal` and `seat inject` without `--no-pr` or `--dry-run` (they push and call gh), and `place` without `--dry-run` (it runs ssh). `seat add` was not run; it needs a source seat that already holds values. Those verbs are judged from their help and from `--dry-run`.

A 10 would make the first hand-off finish on a local bare remote without a hand edit and without a pull request, would refuse a machines file whose third field is not a directory, would say a wrong key cannot open the file, and would end every bad value with the next command. This is 7 because the delivery itself is exact and the way into it is not.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-secrets seal --store $SCR/store --as trial --key $SCR/keys/trial.key --sops $SOPS --name TRIAL_TOKEN --stdin --no-pr` | The OK line names a seal branch, and the NOTE says to push it and open a pull request. exec on the tracked branch then refuses the new name. The first keygen, with no store, prints `<recovery key>`, and keygen --store refuses until .sops.yaml exists, so the rule the banner says to paste is not yet a rule. | On a store whose only remote is local, make --no-pr leave the value on the branch exec reads, and let keygen --store write the rule from recovery.pub when .sops.yaml is absent. | M |
| 2 | `nova-secrets place --store $SCR/store --as trial --key $SCR/keys/trial.key --sops $SOPS --machine bench --secret TRIAL_TOKEN --machines $SCR/logs/machines-valid.tsv --receipts $SCR/receipts --dry-run` | The machines file is one gate accepts (GATE APPROVE). This dry run plans the remote path under darwin/arm64, the os/arch column, and prints DRY-RUN OK. No ssh runs, and the plan is still a path nobody asked for. | Refuse a machines file that has the registry's seven columns, or read the home from a column the registry names as a home. | M |
| 3 | `nova-secrets exec --store $SCR/store --as trial --key $SCR/keys/other.key --sops $SOPS --only TRIAL_TOKEN -- true` | A key that is a real age file and is not this seat's key prints sops failed: exit 128, withholds the transcript, and says to run sops -d. It does not say the key cannot open the file. | Say this key cannot open the seat file, and give the next command as keygen or the right --key. Keep the transcript withheld. | S |
| 4 | `nova-secrets gate --store $SCR/store --base nope --head nope --machines $SCR/logs/machines-place.tsv` | Two refs that name no commit, and a machines file of three columns, produce one line, about --base only. The missing-flag case does name both missing refs together. | When the refs and the machines file can all be judged, print every problem in that one line. | M |
| 5 | `nova-secrets names --store $SCR/store --as trial --max nope` | The refusal says what --max wants and that the value is not one. It does not end with a next command. A bad seat name on the same verb does end with run: nova-secrets names -h. | Append run: nova-secrets names -h to the bad-value line, as the seat-name refusal already does. | S |

## Good, keep

exec puts the --only names in the child and no others, and neither stdout nor stderr carries the value. The MATCH and UNSET lines come from the child, not from the tool.

names --json is the same facts as the text lines, and a refused names --json is one object with a remedy.

A bare keygen or a bare exec names every missing flag in one line, and an unknown flag lists the flags that verb takes.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| place accepts the gate's registry format and plans a garbage path | STILL THERE | `nova-secrets place --dry-run` on the seven-column file prints path=darwin/arm64/.config/nova-secrets/TRIAL_TOKEN.env and DRY-RUN OK, and `nova-secrets gate` on that file prints GATE APPROVE |
| the gate names one problem at a time | CHANGED | missing --base and --head are one line; `nova-secrets gate --base nope --head nope` with a three-column machines file still prints only the --base line |
| a wrong key gives sops failed: exit 128 | STILL THERE | exec with the other seat's key prints SECRETS EXEC FAIL sops failed: exit 128 (transcript withheld: run sops -d) |
| the bad-value refusal lacks a next command | CHANGED | a bad seat name ends with run: nova-secrets names -h; `nova-secrets names --max nope` still has no run: |
