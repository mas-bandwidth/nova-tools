# nova-secrets USE rating, nova-tools 1.1.0

Rater: a large language model, cold to this tool
Build: 99e4a903966c
Score: 8/10

## Reasons

The first successful run was `nova-secrets keygen --as trial --key ./keys/trial.key --age-keygen "$(command -v age-keygen)"` after chmod 700 on the key directory; it exits 0, creates the key mode 0600, prints the exact .sops.yaml rule block and `SECRETS KEYGEN OK as=trial key=... pub=...`, then a two-line next step. The first time it was run with the key directory mode 0755 it refused with `SECRETS REFUSED: key directory keys mode is 0755; expected 0700; run: chmod 700 keys`, which is exactly the kind of one-command remedy the tool promises. A second, different job: `nova-secrets names --store ./store --as trial` on a git-initialised store with no seat file refuses with `seat file trial.yaml is absent ... seal writes a seat's first value: run: nova-secrets seal ...`, and the `--json` form returns the same why in one JSON object.

The four provoked refusals all behave: a missing `--key` says `missing --key <path>; run: nova-secrets keygen -h`; an unknown flag lists the verb's flags; an unknown verb lists all verbs; a bad seat name gives `invalid seat name "bad name" for --as: must match [A-Za-z0-9_-]+; run: nova-secrets keygen -h`. All exit 2. `names --json` returns `{"result":{"verb":"names","status":"refused","exit":2,"why":[...]}}` with the same refusal text, so a program can parse it without guessing.

What costs the score: `nova-secrets seal --dry-run` on a valid but uncommitted store fails with `SECRETS SEAL FAIL git rev-parse failed...` before any plan, while the flag help says dry-run prints the plan and writes nothing; the help does not say the plan needs a committed store first. The verbs that need a sealed store (exec, check, gate, seal, seat add, seat inject, place against a real file) could not be tried here, so their ratings rest on help and refusal paths only.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-secrets seal --dry-run --store ./store --as trial --key ./keys/trial.key --sops /opt/homebrew/bin/sops --name TOKEN --stdin` | on a valid but uncommitted store it prints `SECRETS SEAL FAIL git rev-parse failed...`, not the PLAN lines and DRY-RUN OK the --dry-run help promises | state the committed-store prerequisite in seal's dry-run help | S |
| 2 | `nova-secrets names --store ./store --as trial` | the refusal names the next command but not that the store must also be committed for check and exec | add the store prerequisite to the names refusal or its help | S |
| 3 | `nova-secrets keygen --as trial --key ./keys/trial.key --age-keygen "$(command -v age-keygen)"` | the OK line is buried after four SECRETS RULE lines; the eye lands on the rule block, not the result | print SECRETS KEYGEN OK first, then the rule block | S |

## Good, keep

The chmod/seat-name/flag refusals that each name the problem and the next command in one line. The keygen output that hands the user the exact .sops.yaml block and the public key to send. The JSON refusal that carries the why array unchanged.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| the bad-value refusal lacks a next command | FIXED | `nova-secrets keygen --as 'bad name' ...` exits 2 with `...; run: nova-secrets keygen -h` |
| place accepts the gate's registry format and plans a garbage path | CHANGED | `nova-secrets place --dry-run ... --machines ./machines.tsv` refuses the invalid store first, before any plan is printed |
