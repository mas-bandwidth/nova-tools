# nova-secrets USE rating, nova-tools 1.1.0

Rater: deepseek/deepseek-v4.1-flash
Build: bd7949b97aec
Score: 7/10

## Reasons

The tool is strong where an AI first meets it. `nova-secrets help` opens with what it does, how the store is shaped, and a first run; each verb's `-h` states its effect (inspection, local write, store write, delivery), its required flags, and, for exec and check, the exact store prerequisite (named branch, upstream tracking ref, HEAD equal to the ref) with the three commands that repair each case. Refusals name every missing input at once and end in a remedy that runs: `SECRETS NAMES REFUSED: missing --store <dir>, --as <name>; run: nova-secrets names -h`, and a missing seat lists the seats it does have and the full `seal` line that starts one. `names` and `placed` ran end to end on a scratch store and a scratch receipt, and the bounded listing keeps its total: `SECRETS NAMES MORE kind=key shown=1 total=3 run: ...`. The 2026-10-02 defects are mostly gone: a bad `--max` now carries a next command, the gate now names all three missing flags in one line, and a failed decrypt is `sops failed: exit 128 (transcript withheld: run 'sops -d store3/ada.yaml' to inspect)`.

A 10 would need three things. One registry shape, or a check: `place` takes `--machines` and `gate` takes `--machines`, and they are different formats; `place` silently reads the gate's seven-field row and plans a garbage home, so the same flag means two things. JSON everywhere the standard promises it, not `names` alone: `check`, `exec` and `gate` refuse `--json`, so an AI cannot parse the verb it will run most. And gate's runtime refusal should keep the house grammar and a remedy: it prints `GATE REFUSE rule=0 file=: ...` with no `run:` where every other verb prints `SECRETS <VERB> REFUSED: <reason>; run: <remedy>`.

What could not be tried. This bench carries `sops` and `age-keygen` on PATH but neither is executable here, so `exec`, `check`, `keygen`, `seal`, `place` (real), `seat add` and `seat inject` were judged from their help, from `--dry-run`, and from a scratch stand-in `sops` that answers only the version probe; no real key, store, seat, ssh or network was touched. The stand-in reached the real planning and error paths but not a real decrypt, so a wrong-key decrypt against real `sops` is read from its handling, not run.

Where I had to guess. The gate's `--machines` row shape is not in `gate -h`; the first row I wrote drew `wants 7 tab-separated fields name, ssh, os/arch, roles, seat, cores, notes; got 4`, and the roles drew a second refusal before the file loaded. The `placed` receipt shape is not in `placed -h`; its refusal named the six tab-separated fields, and my first receipt was hopscotch until the output showed `head` and `blob` swapped.

## Findings

| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-secrets place --store ./store3 --as ada --key ./keydir/key --sops ./fakesops2 --machine bench-a --secret GH_TOKEN --machines ./registry7.tsv --dry-run` | the gate's seven-field row is read as place's four-field row, so the os/arch column becomes the home and the plan is `path=linux/amd64/.config/nova-secrets/GH_TOKEN.env`; the same `--machines` flag means two formats and place plans a path it was never given | hold the plan to the row's field count and shape and refuse anything but `name, ssh target, home`, naming the column that is missing or extra | M |
| 2 | `nova-secrets gate --store ./store --base nonesuch --head HEAD` | after it starts, gate prints `GATE REFUSE rule=0 file=: --base nonesuch does not name a commit in the store ./store`; the house shape is `SECRETS GATE REFUSED: <reason>; run: <remedy>` and this line has neither the prefix nor a next command | print the standard refusal line and name the next command, as gate already does for a missing flag | M |
| 3 | `nova-secrets check --store ./store3 --as ada --key ./keydir/key --sops ./fakesops2 --json` | only `names` accepts `--json`; `check`, `exec` and `gate` answer `unknown flag --json`, so the one output value the standard promises as lines or JSON is JSON only for one verb | render the same result value as JSON for every verb, or say in each `-h` why not | M |
| 4 | `nova-secrets help exec` | the `example:` block only prints absolute tool paths for one OS (`/opt/homebrew/bin/sops`, `/opt/homebrew/bin/age-keygen`) while the flag text itself says `command -v sops`; a reader on another host cannot paste the first run | name the tools the way the flags do, so the example runs as printed | S |
| 5 | `nova-secrets seal --store ./store3 --as ada --key ./keydir/key --sops ./fakesops2 --name GH_TOKEN --dry-run` | stderr carries `seal: reading ada.yaml`, a line that is neither a `SECRETS` result nor a `NOTE`; an AI parsing the stream for the grammar meets a line outside it | prefix it as a `NOTE` inside the block or drop it | S |

## Good, keep

The refusal grammar and its remedies: every missing input at once, the seat list, and a `run:` line that is a command to paste, not a search. The bounded listings keep `shown`/`total` in both the line and the JSON `more`, and `names --json` prints the refusal as the same object it prints on success. The exec and check help state the store prerequisite and the three repair commands before any flag, so the hardest first stumble is answered in the help itself.

## Compared with earlier ratings

| earlier | now | evidence |
|---|---|---|
| place accepts the gate's registry format and plans a garbage path | STILL THERE | `nova-secrets place ... --machines ./registry7.tsv --dry-run` prints `SECRETS PLACE PLAN machine=bench-a secret=GH_TOKEN path=linux/amd64/.config/nova-secrets/GH_TOKEN.env mode=0600 ...` |
| the gate names one problem at a time | FIXED | `nova-secrets gate` prints `SECRETS GATE REFUSED: missing --store <dir>, --base <git ref>, --head <git ref>; run: nova-secrets gate -h` in one line |
| a wrong key gives sops failed: exit 128 | CHANGED | `nova-secrets exec ... --sops ./fakesops` prints `SECRETS EXEC REFUSED: sops failed: exit 128 (transcript withheld: run 'sops -d store3/ada.yaml' to inspect)`; the exit stays 128 but the line now withholds the transcript and names the command to inspect it |
| the bad-value refusal lacks a next command | FIXED | `nova-secrets names --store ./store --as ada --max -1` prints `SECRETS NAMES REFUSED: --max -1 is negative; expected non-negative integer; run: nova-secrets names -h` |
