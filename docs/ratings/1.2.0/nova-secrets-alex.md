# nova-secrets READ and USE rating, nova-tools 1.2.0

Rater: openrouter/poolside/laguna-s-2.1 in the opencode harness, a friend on a re-rate card
Build: 5eca256c24cb
READ: 6.5/10
USE: 6/10

No v1.2.0 tag exists on the forge yet, so this rates the release candidate at the head of
sprint/mechanical-2026-10-02. `nova-secrets version` prints `nova-secrets v1.0.1-0.20261007223151-5eca256c24cb linux/amd64 go1.26.6`.
The tool's own code (cmd/nova-secrets, pkg/secrets, pkg/nsprint/verbflag) and
docs/SPEC-SECRETS.md are byte-identical between this head and 1b2c8aea22f1, where the trial
below ran, so every observation below holds at this head.
Built and run on a Linux bench machine, in a scratch directory made for the trial: real sops
3.13.3 and age-keygen 1.3.2 placed in the job's own bin (the host's `/home` sops/age are
sandbox-blocked there, so none were borrowed), HOME pointed into a scratch dir, and the store
plus its bare remote made there with /usr/bin/git — git on PATH is a sprint shim that would
redirect a push, so the store used a local bare remote and never reached a server; a stub ssh
stood in for one real `place`. No live store, no server, no gh, no network. The trial ran
`version`, every verb's `-h`, `keygen` (recovery + ada), `names`, `check`, `exec` delivering
one value to a child, `seal --dry-run`, `place` for real (stub ssh) then `placed`, `seat add`
re-sealing a new seat, `seat inject --dry-run`, and `gate` on a real seat-add diff; plus the
refusals a wrong key, a missing sops, a new seat via `seal`, an absent seat, an unknown verb and
flag, a dash-ref base and a 3-field `--machines`.

## Reasons

READ. The banner's first line is the README row's sentence and its next four lines state the
whole contract: a store is a git working copy holding `.sops.yaml` (one rule per seat naming
recipients), a `recovery.pub`, and one sops-encrypted `<seat>.yaml` per seat; `exec` decrypts
only `--only` names into one child's environment, `names` reads names without decrypting, and no
value is ever printed; the `first run:` line names `setup:` and `first value:`. Every verb
answers `-h` with its usage line, its example, an `effect:` line (inspection, local write, store
write, or delivery), a flag list naming what each flag wants and marking the required ones, and —
for `exec` and `check` — the full store prerequisite with why it holds and the remedy for each
refusal; an unknown flag or verb is answered with the valid names and the remedy. Refusals are
one grammar, `SECRETS <VERB> REFUSED: <why>; run: <next>`, and `names --max` keeps totals with a
runnable MORE line.

What keeps READ at 6.5. The runnable `first value:` and `example:` lines hard-code
`/opt/homebrew/bin/sops` and `/opt/homebrew/bin/age-keygen`, so the first sitting fails on Linux
as printed (finding 1), and the remedy offered is `brew install sops` / `brew install age`
(finding 7). The package doc still claims four verbs and the spec still claims "about two hundred
lines of Go" while the code is 6804 non-test lines (findings 20, 21). The CLI section opens on a
gate example with no `### First run` block (finding 21), every verb's `-h` carries the whole
exit-code paragraph including `check`'s failures and `exec`'s 125 (finding 9), `seat -h` dumps
the root banner (finding 10), `-h` excerpts strip continuation indentation so a usage line reads
as its own (finding 11), and `keygen --store` refuses a store with no `.sops.yaml` even though
`setup:` writes that file from keygen's own output (finding 2).

A 10 would make the first sitting runnable on any OS (portable `command -v` paths in `first value:` and `example:`), make the package doc and spec counts follow the code, open the CLI
section with `setup` + `first value` + `exec` from a `### First run` block, give each verb its
own exit lines on `-h` and `seat -h` its own subverb help, and accept a store that has only
`recovery.pub`.

USE. Once a seat file existed, the core loop is solid. `exec` put exactly the named value in one
child's environment (a child that tests `$GH_TOKEN` and prints `delivered-ok`), the child's own
exit passed through, and `exec` refused a missing `--sops` before running the child (125) and an
empty or unknown `--only` with the missing name and a `run:` remedy. `names` read the two keys
(both sealed) and `--json` returned one object; `--max 1` printed a MORE line with totals and a
runnable widening command; an unknown `--max` value said what `--max` wants but no remedy.
`seal --dry-run` planned the replace, named the recipients (both age public keys), the `seal/`
branch and commit, the rule change adding the mark, and the PR push, then `DRY-RUN OK` with
nothing written and no value read. `place` for real (stub ssh) wrote a 6-field receipt — file,
blob hashing the ciphertext, head and stamp, never the value — and `placed` read it back as
`count=1` with one ITEM line. `seat add` re-sealed `GH_TOKEN` from `ada` into `bo.yaml` with the
mark in the clear, and `gate` approved the two-file diff (`GATE APPROVE files=2 machines=-`).
One run named every problem at once: a store on a named branch with an upstream tracking ref
where HEAD equals it (`exec` and `check` read `.git` as files, no network); a wrong key, a
missing sops and a missing `--only` were each refused with a remedy; and the store must be
committed and clean before `seal`/`inject`.

What keeps USE at 6. The first seat's first value has no runnable verb: `seal` refuses a new
seat and `seat add` needs a `--from` source that does not exist yet, so a fresh store is
bootstrapped only by a hand `sops -e` (finding 3), and the absent-seat remedy still points to
that refusing `seal` (finding 4). `check` does not tie `--key` to `--as` (finding 5): a key
that opens no file still answers `CHECK OK`. A key that is not a recipient of `<as>.yaml` gives
the generic `sops failed: exit 128` line whose remedy needs `SOPS_AGE_KEY_FILE` (finding 6).
`--json` is on `names` only (finding 13), `--machines` is three fields to `place` and seven to
`gate` under one flag (finding 8), `placed` on an absent receipts dir says `count=0` (finding 15),
and `seat inject --no-pr` prints no NOTE that `exec` cannot see the value yet (finding 17).

A 10 would give a new store's first seat its first value in one printed line that runs after
`setup:`, make `check` prove that `--key` opens `--as`'s file, route every refusal through the
one grammar with a `run:` line, give each verb a `--json` form, let `place` and `gate` share one
`--machines` schema, and print the no-see-yet NOTE from `seat inject --no-pr`.

## Findings

| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | cmd/nova-secrets/main.go:116,120-130 | The banner's runnable `first value:` (line 116) and `example:` (lines 120-130) hard-code `/opt/homebrew/bin/sops` and `/opt/homebrew/bin/age-keygen`; on Linux the first sitting fails as printed and the remedy is `brew install ...` | Use `$(command -v sops)` and `$(command -v age-keygen)` in every runnable line | M |
| 2 | pkg/secrets/keygen.go:47,87 | `keygen --store` runs age-keygen only after refusing a store whose `.sops.yaml` is absent, yet `setup:` pipes keygen's rule into that same `.sops.yaml`; the store is built only because the shell creates the empty file first | Accept a store with `recovery.pub` present and no `.sops.yaml` | S |
| 3 | pkg/secrets/seal.go:31,200,513; docs/SPEC-SECRETS.md:1275 | No verb gives a new seat its first value: `seal` refuses a new seat ("a new seat is given its first values by seat add, never by seal") and `seat add` needs a `--from` source that does not exist yet, so the only way on is a hand `sops -e` | Let `seat add` with no `--from` (or `setup`) write an empty sealed file for a seat whose key the caller holds, and put that line in `setup:`/`first value:` | M |
| 4 | pkg/secrets/seatfile.go:190 | `names` and `check` point an absent seat to `seal` ("seal starts a new seat: run: nova-secrets seal ..."), but `seal` refuses new seats, so the remedy is un-runnable | One remedy for an absent seat file, the verb from finding 3, printed by all three | S |
| 5 | pkg/secrets/invariants.go:239-270,556 | `check` does not tie `--key` to `--as`: a key that opens no file (or only another seat) still answers `SECRETS CHECK OK` (`mine=1 foreign=1`) though it opens `--as`'s file; `check -h` says it "proves it opens" | Fail when `--key`'s public half is not a recipient of `<as>.yaml`, or when `<as>.yaml` does not decrypt with it | S |
| 6 | pkg/secrets/sops.go:59-71,173-177 | A key that is not a recipient of `<as>.yaml` is answered with `sops failed: exit 128 (transcript withheld: run 'sops -d <file>' to inspect)`; the remedy needs `SOPS_AGE_KEY_FILE` the reader is never told to set (the `no identity matched` class is not matched) | Compare the key's public line with the file's recipients first and say `this key is not a recipient of <as>.yaml` | S |
| 7 | pkg/secrets/sops.go:76,108 | A missing sops or age-keygen is refused with `run: brew install sops` / `brew install age` on every platform | Name the binary and its project page, or choose the line by GOOS | S |
| 8 | pkg/nsprint/verbflag/verbflag.go:120-127 | `names --max nope` says what `--max` wants but ends with no `run:` line, unlike every other refusal | Append the verb's `run:` line to bad-value refusals | S |
| 9 | cmd/nova-secrets/main.go:95 | The exit-code paragraph (incl. `check`'s failures and `exec`'s 125) is printed under every verb's `-h`, so `version -h`/`keygen -h`/`names -h` describe failures that do not apply to them | Pass each verb its own exit lines to `verbflag.Print` | S |
| 10 | cmd/nova-secrets/seat.go:26 | `seat -h` prints the whole root banner instead of the two subverbs | Print the two subverb usage lines and `run: nova-secrets seat add -h` | S |
| 11 | pkg/nsprint/verbflag/verbflag.go:316,518 | Every verb's `-h` excerpt trims the banner's continuation lines to the usage indent, so `--only <NAME,...|all> [--require <NAME>]... -- <cmd>` reads as a usage line of its own | Keep the banner's relative indentation in the excerpt | S |
| 12 | cmd/nova-secrets/exec.go:77 | `exec` refuses a `redis-cli` write of another tool's `friend:<name>:width` key — a rule about a different tool's state whose comment carries an issue number — on exec's command path, before the command starts | Move the refusal to the tool that owns the key | M |
| 13 | pkg/secrets/keygen.go:15 | `keygen --as recovery` (no store) prints three `SECRETS RULE` lines but its NEXT says "add these two lines"; its NOTE points to `seat add`, which `setup:` itself does not run | Say `these lines`; when `--as` is recovery, print only the public key and the recovery.pub step | S |
| 14 | pkg/secrets/place.go:408,446 | `placed --receipts <absent dir>` answers `SECRETS PLACED OK count=0`; an absent receipts directory is not refused | Refuse when an explicit `--receipts` is given but absent, or say `receipts=absent` on the OK line | S |
| 15 | cmd/nova-secrets/main.go:68-69; cmd/nova-secrets/gate.go:21; pkg/secrets/place.go:190-206; pkg/fleet/registry.go:233-239 | `--machines` is three tab-fields (name, ssh target, home) for `place` but seven (name, ssh, os/arch, roles, seat, cores, notes) for `gate`, under one flag | Refuse a row whose third field is not an absolute directory, or give the two files two flag names | M |
| 16 | pkg/secrets/seatinject.go:177-186 | `seat inject --no-pr` returns its OK line with no NOTE that `exec` cannot read the value yet, unlike `seal`, which prints `SECRETS SEAL NOTE exec and check read the store's own branch ...` | Print the same NOTE from one helper both verbs share | S |
| 17 | pkg/secrets/place.go:273 | An unknown `--machine` is refused as `machine nope is not in the fleet registry ...; add it there` with no `run:` line and no list of the machines the file holds | Name the machines the file holds and end with `run: nova-secrets place -h` | S |
| 18 | pkg/secrets/git.go:107 | One commit ahead of upstream is answered `run: git -C ./secrets pull --ff-only`; for ahead that does nothing (ahead needs a push, not a pull) | Say which: behind gets `pull --ff-only`, ahead gets `push` (with its warning) | S |
| 19 | cmd/nova-secrets/main.go:64 | `--json` exists on `names` only; `check`, `exec`, `gate`, `seal`, `seat`, `place` and `placed` refuse `unknown flag --json` | Give every verb's result a `--json` form, or say in each `-h` why not | M |
| 20 | pkg/secrets/secret.go:2 | The package doc says the package provides four verbs (exec, names, check, keygen); the banner lists eleven verbs and two seat subverbs | Name the verbs the package now carries, or say it carries the store-reading and store-writing verbs | S |
| 21 | docs/SPEC-SECRETS.md:34 | The spec says "about two hundred lines of Go"; `cmd/nova-secrets` and `pkg/secrets` are 6804 non-test lines | Drop the count or state the measured one | L |

## Good, keep

exec is the right shape for an AI: one named value goes only into one child's environment, the
OK line names the seat, file and store head without the value, the child's exit passes through,
exec itself refuses at 125 and never runs the child on a missing `--sops` or `--only`.

`seal --dry-run` and `place --dry-run` and `seat inject --dry-run` print PLAN lines (file,
recipients, branch, commit, push/PR, or machine, remote path, ssh target, receipt) and write
nothing, read no new value, run no ssh/git-write/gh call; seal's plan even shows the rule change
adding the mark.

One refusal grammar across every verb, each naming a runnable next command as printed: `seal`
refuses a new seat pointing at `seat add`, `seat add` refuses an existing seat, `keygen` refuses
an existing key, an empty or unknown `--only` names the missing keys and the seat's names.

`seat add` re-seals from a source seat and writes the rule + the mark in the clear, and `gate`
approves a clean seat-add diff (`GATE APPROVE files=2 machines=-`); `place`'s receipt keys the
value by file, blob (a hash of the ciphertext, never the value) and head, and `placed` reads it
back without hashing a value. `names --max` keeps totals with a runnable MORE line, and
`names --json` is one object.

## Compared with earlier ratings

The earlier rating is `nova-secrets-zhi.md` (1.2.0), built at dd75bc2c9f80, which is an
ancestor of this head (5eca256c24cb), so the comparisons below are against the code as the
earlier rating found it.

| earlier | now | evidence |
|---|---|---|
| seal refuses a new seat; no verb gives the first seat its first value; a hand `sops -e` is the only way on | STILL THERE | `nova-secrets seal --store ./secrets --as ghost --key ... --sops ... --name GH_TOKEN --stdin` refused: "ada.yaml does not exist ... never by seal, use seat add"; `seat add` needs a --from source |
| check does not tie --key to --as | STILL THERE | `check --as ada --key <bo.key>` (bo not a recipient of ada.yaml) answered `SECRETS CHECK OK ... mine=1 foreign=1` |
| keygen --store refuses a store with no .sops.yaml | STILL THERE | `keygen --as zoe --key ... --store ./store2` (recovery.pub present, no .sops.yaml) refused: "store ... has no .sops.yaml" |
| examples and `first value:` hard-code /opt/homebrew/bin/sops | STILL THERE | `check ... --sops /opt/homebrew/bin/sops` (the banner's line) refused as absent; remedy `brew install sops` |
| missing sops/age-keygen offers `brew install` on every platform | STILL THERE | missing --sops → `run: brew install sops`; missing --age-keygen → `run: brew install age` |
| wrong key on exec gives sops exit 128; remedy needs SOPS_AGE_KEY_FILE | STILL THERE | `exec --as ada --key <bo.key> ...` → `sops failed: exit 128 (transcript withheld: run 'sops -d ... to inspect)` |
| contradicting remedies for an absent seat | CHANGED | names/check now give one remedy ("seal starts a new seat: run: nova-secrets seal ..."), but it is still un-runnable since seal refuses new seats (see first value) |
| keygen NEXT mis-counts and its NOTE points to seat add | STILL THERE | `keygen --as recovery` prints three RULE lines, NEXT says "two lines", NOTE points to `seat add` |
| -h excerpts strip continuation indentation | STILL THERE | exec -h shows `--only <NAME,...|all> [--require <NAME>]... -- <cmd> [args...]` as its own usage line |
| seat -h prints the whole root banner | STILL THERE | `seat -h` printed the full banner |
| exit-code paragraph under every verb's -h | STILL THERE | every verb's -h ends with the 0/1/2/125 paragraph incl. check failures and exec 125 |
| place -h --dry-run line shared with seal and seat inject | STILL THERE | place -h --dry-run says "seal and seat inject read the store at HEAD" |
| --machines is three fields to place and seven to gate | STILL THERE | `gate --machines fleet.tsv` wants 7; `place --machines fleet.tsv` wants 3 |
| gate in-run refusal grammar (`GATE REFUSE rule=`) | FIXED | `--base nope` → `SECRETS GATE REFUSED: --base nope does not name a commit ...; run: nova-secrets gate -h`; a 3-field registry → `SECRETS GATE REFUSED: the machines registry does not read ...` |
| placed on an absent receipts dir answers OK count=0 | STILL THERE | `placed --machine bench-a --receipts ./nodir` → `SECRETS PLACED OK machine=bench-a count=0` |
| unknown machine: no run: line, no list of machines | STILL THERE | `place --machine nope ...` → `machine nope is not in the fleet registry ...; add it there` (no run:) |
| names --max nope: no run: line | STILL THERE | `names --max nope` → `invalid value for --max: it wants n names ...` (no run:) |
| ahead-of-upstream remedy is `pull --ff-only`, which does nothing for ahead | STILL THERE | store one commit ahead → `run: git -C ./secrets pull --ff-only` |
| exec refuses a redis-cli write of friend:width | STILL THERE | cmd/nova-secrets/exec.go:77 still refuses `redis-cli writing friend:<name>:width` |
| --json on names only | STILL THERE | `check --json` → `unknown flag --json` |
| package doc says four verbs | STILL THERE | pkg/secrets/secret.go:3 still says four verbs |
| spec says about two hundred lines of Go | STILL THERE | docs/SPEC-SECRETS.md:34; 6804 non-test lines |
| its own refusal grammar is one line | FIXED | every verb's refusal prints `SECRETS <VERB> REFUSED: <why>; run: <next>` |
