# nova-secrets dogfood — dsh (zhi), 2026-10-06

Read as a stranger: only `nova-secrets -h`, `nova-secrets help`, `nova-secrets <verb> -h` and the page under `docs/`. Built from the staged checkout at
6ec8bb02edc83283630f2e10e21bd035b3336f65 (cross-built on a Linux bench, no
code changes) and used as `nova-secrets devel darwin/arm64 go1.26.6`: every
verb at least once with its real flags against a throwaway store inside the job
directory (a local git working copy with its own bare remote, keygen-made age
keys, sops 3.13.3 and age-keygen 1.3.2), the refusals and the boundary calls
too, `place` over ssh to a scratch machine row in a scratch registry. Every
value sealed or read was invented for the run; no real key, token or password
was touched. Quoted paths show `<user>` for the bench user. About 20 minutes of
use.

## Findings

1. The help's own `first run:` block ends in a refused step: its "first value"
   line cannot run on the store its setup lines just built.
   - as printed by the banner (setup lines followed exactly, `~` resolving to
     the scratch home):
     ```
     printf '%s' 'a-token-value' | nova-secrets seal --store ./secrets --as ada \
       --key ~/.config/nova-secrets/ada.key --sops /opt/homebrew/bin/sops \
       --name GH_TOKEN --stdin --no-pr
     ```
     ``` SECRETS SEAL REFUSED: ada.yaml does not exist in the store; a new seat is given its first values by seat add, never by seal (SPEC-SECRETS rule 12); run: nova-secrets seal -h ```
   - I expected the recipe's own step to succeed (exit 0, `SECRETS SEAL OK`).
     The setup block builds ada's rule but no `ada.yaml`, and no verb can make
     the store's *first* seat file: `seal` refuses as above, `seat add`
     refuses `source seat file ada.yaml is absent`, and the `example:` block's
     `names`/`check`/`exec` lines against the same state all exit 2 for the
     same reason. The only exit I found was a hand `sops --encrypt` step the
     page reserves for a person — which the gate then refuses on a changed
     file unless the `NOVA_SECRETS_WRITTEN_BY` mark is copied in by hand too.
   - Grade: URGENT.

2. `names` and `check` print a remedy `seal` itself refuses: three verbs cycle
   with no exit on a new store.
   - `nova-secrets names --store ./secrets --as ada`
     ``` SECRETS NAMES REFUSED: seat file ada.yaml is absent: store ./secrets holds no seat file yet; seal writes a seat's first value: run: nova-secrets seal --store ./secrets --as ada --key <path> --sops <path> --name <NAME> ```
   - Running exactly that seal prints the rule-12 refusal of finding 1, whose
     named remedy (`seat add`) refuses `source seat file ada.yaml is absent`;
     `check` prints the same sentence as `names`. I expected one verb whose
     printed remedy the next verb accepts: either `seal` writes a seat's first
     value (and every help text says so), or `names`/`check` name the verb
     that can actually run on a store with no seat file yet. As it stands each
     refusal hands the stranger a command that exits 2.
   - Grade: URGENT.

3. `seal --no-pr` prints its next step after the verdict.
   - `printf '%s' 'scratch-redis-pw-03' | nova-secrets seal --store scratch/store --as ada --key scratch/keys/ada.key --sops /opt/homebrew/bin/sops --name NOVA_REDIS_BENCH_PASSWORD --stdin --no-pr`
   - printed (last two lines of six):
     ```
     SECRETS SEAL OK name=NOVA_REDIS_BENCH_PASSWORD seat=ada committed branch=seal/ada-NOVA_REDIS_BENCH_PASSWORD-20261006-202504
     SECRETS SEAL NOTE exec and check read the store's own branch, which does not hold this value yet; next: git -C scratch/store push -u origin seal/ada-NOVA_REDIS_BENCH_PASSWORD-20261006-202504, then open and merge its pull request
     ```
   - I expected the next-step line inside the machine-readable block, above the
     OK line, as SPEC-SECRETS "Output grammar" states ("every `SECRETS` line a
     verb prints comes before its `OK` line"; `keygen` is named as the one
     verb that prints past its verdict), and as `seat add` does with its
     `NEXT:` line. A caller that stops parsing at the OK line misses the
     push/PR step; `seat inject --no-pr`, on the same road, prints no such
     note at all.
   - Grade: NEXT.

4. `--machines` is one flag with two file formats, and `place` accepts the
   gate's registry with a plausible-but-wrong plan.
   - `nova-secrets place --store scratch/store --as ada --key scratch/keys/ada.key --sops /opt/homebrew/bin/sops --machine bench-a --secret GH_TOKEN --machines scratch/machines-gate.tsv --receipts scratch/receipts --dry-run`
     (the file is the gate's registry: `name, ssh, os/arch, roles, seat, cores, notes`)
   - printed (first 3 lines):
     ```
     SECRETS PLACE PLAN machine=bench-a secret=GH_TOKEN path=darwin/arm64/.config/nova-secrets/GH_TOKEN.env mode=0600 file=ada.yaml head=5a9d164b907acf635b5d66c04eac0db16f8d4d15 blob=566dfd31d5b847081fe8bb4f0225d19fc455c956
     SECRETS PLACE PLAN ssh=ssh target=localhost writes=darwin/arm64/.config/nova-secrets/GH_TOKEN.env the value travels on stdin, never in an argument
     SECRETS PLACE PLAN receipt=scratch/receipts/bench-a.receipt action=add
     ```
   - `place` reads `name, ssh, home` (third column); the gate's registry
     carries `os/arch` there, so the plan writes under a directory literally
     named `darwin/arm64` on the machine. I expected `place` to refuse a
     registry row whose third column is not a home path, or one file format
     to serve both verbs. The dry run made the mistake visible; a real run
     would have written it.
   - Grade: NEXT.

5. A failed full `seal` withholds the `gh` transcript and points nowhere.
   - `printf '%s' 'scratch-smtp-08' | nova-secrets seal --store scratch/store --as ada --key scratch/keys/ada.key --sops /opt/homebrew/bin/sops --name SMTP_PASSWORD --stdin`
     (the store's remote is not GitHub, so the pull request step cannot succeed)
   - printed (last two lines of six):
     ```
     seal: opening the pull request
     SECRETS SEAL REFUSED: gh pr create failed: exit status 1 (transcript withheld); run: nova-secrets seal -h
     ```
   - I expected enough to act on in one turn: why `gh` failed (its stderr
     holds no plaintext this tool sealed), or a pointer to the already-pushed
     `seal/` branch that holds the value — the `--no-pr` NOTE names exactly
     that, the failure path prints nothing about it. The store was returned to
     its branch clean and `check` stayed green, which is right; only the
     diagnosis is missing. `run: nova-secrets seal -h` cannot answer it.
   - Grade: NEXT.

## What the tool got right

- Every refusal names what the input WANTS with a remedy that runs: missing
  `--require`s were listed sorted in one run (`NOPE2,NOPE3`), an unknown verb
  or flag named the alternatives, key file mode `0644` and key directory `0755`
  were each refused with the exact `chmod`, and `names --as nope` listed the
  seats that exist.
- `exec` set exactly the `--only` names (a probe printed `GH=set SMTP=`), a
  planted `SOPS_AGE_KEY_FILE` holding the wrong key changed nothing (the sops
  child's built environment), and the command's own exit 7 came back as 7.
- `check` caught a stale working copy (HEAD one commit ahead of its
  remote-tracking ref) at exit 1 on `check` and 125 on `exec`, both naming
  `git -C <store> pull --ff-only`, and went green after the push.
- `gate` refused exactly as the page says: a rule naming a seat file the diff
  does not carry, a hand-sealed seat file with no mark (check 2), a recipient
  no registry row vouched for (check 4), a ref that names no commit, a ref
  shaped like an option, a flag given twice; a verb-written diff approved with
  `files=1`, an empty diff with `files=0`.
- `place` wrote mode 0600 on the machine over ssh with the value on stdin; the
  receipt carries `blob`/`head`, a repeat dry-run said `action=unchanged`, and
  a hand-written old-format receipt listed as `identity=unknown` with the NOTE
  remedy exactly as SPEC-SECRETS "Receipts from an older build" prints it.
- `keygen` never overwrites, and its pasted rule block (with `--store`)
  satisfied `check` green; the bootstrap pipeline `keygen --store | sed >
  .sops.yaml` is a genuinely nice first-run step.
- `seat add` and `seat inject` re-sealed only the `--only` names, the second
  seat's key opened its file and not the first's (`mine=1 foreign=2` green),
  and every seal branch the verbs left behind gated clean.
- `get` was refused before anything was read, quoting `sops -d` in a person's
  hands; no verb printed a value anywhere in the session.

## Gate

    go test -count=1 -timeout 600s ./internal/docs
    ok  	github.com/mas-bandwidth/nova-tools/internal/docs	2.384s

    go test -count=1 -timeout 600s ./internal/ci
    ok  	github.com/mas-bandwidth/nova-tools/internal/ci	22.801s

    go test -count=1 -timeout 600s ./internal/docs -run TestDocsTreeIsConsistent
    ok  	github.com/mas-bandwidth/nova-tools/internal/docs	0.020s [no tests to run]

The card's named test does not exist at this tip; the docs-tree guard that
does exist (`TestCommittedMapMatchesTree`) runs inside the green
`./internal/docs` above. No code changes in this card.

READ 8/10 — the banner answers what it does, how it works and the first run,
every verb's `-h` states its effect and exit codes, and the page under `docs/`
is unusually honest about what the tool refuses; the bootstrap dead-end
(findings 1 and 2) is the one place the help and the binary disagree, and it is
the first thing a stranger meets.

USE 8/10 — every verb ran against a throwaway store with invented values, each
refusal carried a remedy that ran in one turn, and the gate's numbered checks
read clearly; the first-value dead-end and the `--machines` format split are
the stumbles.

urgent=2 next=3
