# Dogfood: nova-secrets — 2026-10-06, codex

One friend, one tool, cold. I read only `nova-secrets -h`, `nova-secrets help`,
every verb's `-h`, and the tool's own pages under docs/ (the `## nova-secrets`
section of docs/CLI.md and docs/SPEC-SECRETS.md), then used every verb at least
once with its real flags against a scratch store and temp dirs, built from this
checkout at df30ce088: `nova-secrets v1.0.1-0.20261006202650-df30ce088344
darwin/arm64 go1.26.6`. Every value sealed or read was invented in the scratch
store — no real key, token or password was touched. Refusals included. `place`
was driven end to end through a fake `ssh` on PATH (a script that execs the
remote line locally): no server, no network. `exec` was driven with
`/usr/bin/printenv` and `/usr/bin/env`.

## Findings

1. The help's own first run cannot be followed: `setup` is not a verb and `seal`
   refuses the store's first value — URGENT.

   **Command:**

       nova-secrets help
       printf '%s' 'a-token-value' | nova-secrets seal --store ./secrets --as ada \
         --key ./keys/ada.key --sops /opt/homebrew/bin/sops --name GH_TOKEN --stdin --no-pr

   (the second line is the help's `first value:` block, run after its `setup:`
   block verbatim; the first line's banner says `first run: setup: makes a store
   from an empty directory`).

   **Printed:**

       SECRETS SEAL REFUSED: ada.yaml does not exist in the store; a new seat is given its first values by seat add, never by seal (SPEC-SECRETS rule 12); run: nova-secrets seal -h

   exit 2. `nova-secrets setup` prints `SECRETS REFUSED: unknown verb "setup";
   the verbs are exec, names, check, gate, keygen, place, placed, seal, seat add,
   seat inject, version, help; run: nova-secrets help` at exit 2: the help's
   named first step is not a verb at all.

   **Expected:** the help's first run to work as printed — setup makes a store
   from an empty directory and the next block seals the first value with
   `--stdin`, "the path a harness takes". Instead the store the setup block
   builds has no `<seat>.yaml`, and `seal` refuses to create one. `seat add`,
   the verb `seal` names, cannot break the circle either: it requires `--from` a
   seat this machine can open, and in a new store there is no such file. The
   only road left is hand `sops`, which the store's gate refuses. The refusal is
   not an isolated wrong turn: `nova-secrets names --store ./store --as nope`
   prints `...; seal starts a new seat: run: nova-secrets seal --store ./store
   --as nope --key <path> --sops <path> --name <NAME>`, sending the reader to
   the very command that then refuses. A first run a stranger cannot finish and
   two verbs that send each other in a circle is the defect this release exists
   to close, before v1.1.0.

   **Grade:** URGENT

2. A rule printed by `keygen` makes `seal`'s own commit fail the store's own
   gate — URGENT.

   **Command:**

       nova-secrets gate --store ./store --base 9909ae0 --head 4bbd60a

   (`4bbd60a` is `seal`'s commit, `seal GH_TOKEN into ada.yaml`; `9909ae0` is the
   setup commit the keygen rule came from.)

   **Printed:**

       GATE REFUSE rule=1 check=2 file=ada.yaml: the seat file was not written by a nova-secrets verb; seal it with nova-secrets seal or seat add, never by hand

   exit 2.

   **Expected:** `GATE APPROVE files=1`, the diff being exactly the file `seal`
   wrote. The rule `keygen` prints — the one the help's setup block pastes into
   `.sops.yaml` — carries only `path_regex` and `age`; it omits
   `unencrypted_regex: ^NOVA_SECRETS_WRITTEN_BY$`, so sops seals the
   `NOVA_SECRETS_WRITTEN_BY` mark that `seal` adds, and the gate, which reads
   that mark in the clear, cannot find it and calls a verb's file "not written by
   a nova-secrets verb". Rules `seat add` writes do carry the regex; a rule a
   person pastes from `keygen`, as the help instructs, does not. The documented
   cold path therefore writes a file the store's own gate rejects, with a remedy
   telling the reader to run the verb that already wrote it.

   **Grade:** URGENT

3. `keygen --store` refuses a store whose `recovery.pub` is good but whose
   `.sops.yaml` is absent, with a circular remedy — URGENT.

   **Command:**

       nova-secrets keygen --as ada --key ./keys/ada.key \
         --age-keygen "$(command -v age-keygen)" --store ./store

   (a fresh `git init` store holding a valid `recovery.pub`, no `.sops.yaml`).

   **Printed:**

       SECRETS KEYGEN REFUSED: store ./store has no .sops.yaml; run: git clone <store url> ./store (a new store: git init it, then write its .sops.yaml from the rule block nova-secrets keygen prints)

   exit 2.

   **Expected:** the rule block, because `keygen -h` says `--store <dir>` is the
   "dir of the store, whose `recovery.pub` fills the rule's recovery key" and its
   help calls that the command's purpose; SPEC-SECRETS calls it "its one read of
   the store". Printing the block is how the store gets a `.sops.yaml` at all, so
   the refusal's own remedy — "write its `.sops.yaml` from the rule block
   `nova-secrets keygen` prints" — is the thing being refused. The help's setup
   line works only by accident of the shell: the `> ./secrets/.sops.yaml` redirect
   creates an empty file before `keygen` checks for one, so the check passes.
   Run the same argument list without the redirect and it refuses; a reader who
   follows `keygen -h` rather than the redirect gets no rule block and no way to
   make one.

   **Grade:** URGENT

4. docs/CLI.md says `keygen` ends on its `OK` line; the binary closes with two
   prose lines after it — NEXT.

   **Command:**

       nova-secrets keygen --as bo --key ./keys/bo.key --age-keygen "$(command -v age-keygen)"

   **Printed (last three lines):**

       SECRETS KEYGEN OK as=bo key=./keys/bo.key mode=0600 pub=age1nt3a2k89f8v5tt43g6hkfv3h5s0k6r93aqa3s30kan7h4epkwq0q792grw
       Done. Your new key is at ./keys/bo.key. Nothing failed.
       Next: send this public key to whoever seals your seat: age1nt3a2k89f8v5tt43g6hkfv3h5s0k6r93aqa3s30kan7h4epkwq0q792grw

   exit 0.

   **Expected:** the CLI page's keygen paragraph says `SECRETS KEYGEN OK` is
   "**last**" and "A run that ends on the OK line succeeded; a `NEXT:` line above
   it is the next step, not a failure." SPEC-SECRETS's output grammar (rule 11)
   says the opposite: the OK line is the last *machine-readable* line and the
   verb deliberately closes with two plain lines for a person. The binary follows
   the spec. A caller who trusts the CLI page and reads the last line for the
   receipt gets prose, and the page never mentions the closing lines. The two
   pages in the same docs tree disagree about the binary's own output.

   **Grade:** NEXT

5. `check`'s `clear=` counts something other than the clear keys `names` counts,
   so the two numbers disagree on the same file — NEXT.

   **Command:**

       nova-secrets check --store ./store --as ada --key ./keys/ada.key --sops /opt/homebrew/bin/sops

   **Printed:**

       SECRETS CHECK OK as=ada recipients=2 files=1 sealed=1 mine=1 foreign=0 clear=1 head=154d1d1

   exit 0. For the same store and file,
   `nova-secrets names --store ./store --as ada` prints
   `SECRETS NAMES OK as=ada keys=4 shown=4 sealed=4 clear=0`, and its per-key
   line for the mark reads `SECRETS NAME key=NOVA_SECRETS_WRITTEN_BY clear=false`.

   **Expected:** the two `clear=` counts to agree, both being the documented
   decomposition of the file's keys. ada.yaml's rule permits no clear key and its
   only unsealed-looking line, `NOVA_SECRETS_WRITTEN_BY`, is encrypted by sops
   (there is no `unencrypted_regex`), which `names` reports correctly as
   `clear=false`. `check` counts that same encrypted mark as clear, so `check`'s
   `clear=` is one higher than `names`' on every seat that carries the mark —
   with both files present `check` said `clear=2` where `names` said `clear=1`.
   The number a reader would use to catch a plaintext key cannot be told apart
   from the tool's own bookkeeping.

   **Grade:** NEXT

6. `exec --only all` hands the verb's internal write-mark to the command's
   environment — NEXT.

   **Command:**

       nova-secrets exec --store ./store --as ada --key ./keys/ada.key \
         --sops /opt/homebrew/bin/sops --only all -- /usr/bin/env

   **Printed (the command's environment, first three relevant lines):**

       DEEPSEEK_API_KEY=invented-deepseek-key-abc
       GH_TOKEN=replacement-gh-token-456
       NOVA_SECRETS_WRITTEN_BY=seal dev

   plus `SECRETS EXEC OK as=ada keys=4 only=all required=0 file=store/ada.yaml
   head=154d1d1 cmd=/usr/bin/env` on stderr, exit 0.

   **Expected:** only the seat's values. `NOVA_SECRETS_WRITTEN_BY` is the mark
   `seal`, `seat add` and `seat inject` write into the file, and both the gate
   and check treat it as bookkeeping rather than a value; `--only all` delivers
   it to a child as if it were a provider key. Neither the help's `--only all`
   description nor the docs page says the command receives the tool's own
   version mark.

   **Grade:** NEXT

7. `exec` names only the first flag problem, not every independent one — NEXT.

   **Command:**

       nova-secrets exec --store ./store --as ada --key ./keys/ada.key \
         --sops /opt/homebrew/bin/sops --only NOPE --require NOPE2 -- /usr/bin/true

   **Printed:**

       SECRETS EXEC REFUSED: --only names key(s) not in store/ada.yaml: NOPE; the names the seat holds: run: nova-secrets names --store ./store --as ada

   exit 125.

   **Expected:** both independent flag problems in the one run — `--only NOPE`
   names no key in the file and `--require NOPE2` names another — because
   SPEC-SECRETS says exec "reports every independent problem it can reach, in a
   deterministic order: flags first, sorted; then the store; the key; the binary;
   the contents". Only the `--only` problem is named; a caller fixes it, reruns,
   and only then learns about `--require`. With one bad flag and one valid flag
   the refusal is right (`--only NOPE` alone, and `--only GH_TOKEN --require
   DEEPSEEK_API_KEY` refusing the contradiction), so the shortfall is only when
   two flag refusals stand at once.

   **Grade:** NEXT

## What worked (no finding, kept short)

Every verb ran at least once for real: `version` and `--version`; `keygen`
(recovery, ada, bo, carol); `names` (line, `--json`, `--max 1` with its MORE
line); `check` (green, and red on a dirty working copy and on a three-recipient
rule, naming file and repair); `exec` (success, `--only all`, and the refusals
below); `gate` (the docs page's own throwaway-store recipe printed exactly
`GATE APPROVE files=1 machines=../machines.tsv`, the seat-add and seat-inject
diffs approved, and refs beginning with `-`, unknown refs, a repeated flag,
three recipients and a recipient no machine vouched for were each refused with
the promised line); `place` (dry run, a real placement through the fake `ssh`
with the value on stdin only, `action=unchanged` on the second dry run, and the
missing machine and missing secret refusals); `placed` (`count=1` and `count=0`);
`seal` (dry run add/replace, a real `--no-pr` replacement that a later `check`
and `exec` confirmed, and the dirty-store, bad-name, empty-value and multi-line
refusals); `seat add` and `seat inject` (`--no-pr`), both landing files a later
`check`, `names` and `exec` opened under the new seat's own key. Refusals were
one line, named the remedy, and left the store byte-for-byte unchanged
(`seat add`'s five refusals all left `.sops.yaml` identical). `place`'s receipt
recorded the sealed file, blob and head and never a value; `exec` left no
wrapper behind; the mark was allowed in the clear exactly where the rule named
it.

The card's named test `TestDocsTreeIsConsistent` does not exist in
`./internal/docs` at this tip: `go test ./internal/docs -run
TestDocsTreeIsConsistent` reports `[no tests to run]` at exit 0. The docs class
test that does read this tree, `TestCommittedMapMatchesTree`, is green here
because `docs/dogfood` already has its `internal/docs/catalog.go` row.

## Gate

    go test -count=1 -timeout 600s ./internal/docs ./internal/ci
    ok  	github.com/mas-bandwidth/nova-tools/internal/docs	2.7s
    ok  	github.com/mas-bandwidth/nova-tools/internal/ci	19.7s

    go test -count=1 -timeout 600s ./internal/docs -run TestDocsTreeIsConsistent
    ok  	github.com/mas-bandwidth/nova-tools/internal/docs	0.3s [no tests to run]

Both packages pass; the named test does not exist at this tip.

READ 6/10 — the verb helps, the flag list and the refusal grammar are complete
and honest enough to find every verb cold, and the docs page's gate recipe runs
exactly as printed, but the first-run banner names a `setup` verb that is not
one, the CLI page contradicts the spec and the binary about `keygen`'s last
line, and `check`'s `clear=` number is not the count its sibling prints.
USE 6/10 — every verb did its job against a scratch store, refusals were one
line with the next command and left the store untouched, and `exec`/`gate`/
`place` are careful about where a value may travel, but the documented cold path
cannot put the first value in a store at all, and the rule `keygen` prints makes
the store's own gate reject the file `seal` writes.

urgent=3 next=4
