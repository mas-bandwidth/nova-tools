# nova-secrets

nova-secrets keeps secrets sops-encrypted in a git working copy (the store) and hands the values a command asks for
by name to that command alone, in its environment. sops and age do the cryptography; nova-secrets is the thin part
around them: which file, which key, which names. No verb prints a value.

## The store

```
<store>/.sops.yaml      one creation rule per seat: path_regex ^<seat>\.yaml$, age: <seat key>,<recovery key>
<store>/recovery.pub    the recovery public key every seat file is also sealed to
<store>/<seat>.yaml     a seat's values, sops-encrypted YAML, one NAME: value per line
```

A seat's private age key is a file of its own (by convention `~/.config/nova-secrets/<seat>.key`), mode 0600 and
owned by you; on Windows, owned by you with an access list that allows only you, SYSTEM and Administrators. Every verb
that decrypts refuses any other key file before sops runs.

## Verbs

```
nova-secrets exec --store <dir> --as <seat> --key <path> --sops <path> --only <NAME,...|all> [--require <NAME>]... -- <cmd> [args...]
nova-secrets names --store <dir> --as <seat>
nova-secrets check --store <dir> --as <seat> --key <path> --sops <path>
nova-secrets seal --store <dir> --as <seat> --key <path> --sops <path> --name <NAME> [--stdin]
nova-secrets seat add --store <dir> --as <new seat> --pub <age1...> --from <seat> --only <NAME,...> --key <path> --sops <path>
nova-secrets keygen --as <seat> --key <path> --age-keygen <path> [--store <dir>]
nova-secrets version
```

**exec** decrypts `<seat>.yaml` and becomes `<cmd>` (exec(2); on Windows it runs it and exits with its status), its
environment the inherited one with every name the seat holds and every sops identity variable removed, then the
`--only` values added. Every `--only` and `--require` name must be in the seat, and every `--require` in `--only`.
It prints `SECRETS EXEC OK as=<seat> keys=<n> cmd=<cmd>` on stderr; the exit status is the command's, and 125 when
exec refused and the command never ran. No core file is written after the decrypt.

**names** lists the names in `<seat>.yaml` without decrypting it, one `SECRETS NAME key=<NAME> clear=<bool>` line
each, then `SECRETS NAMES OK as=<seat> keys=<n>`.

**check** proves `--key` opens `<seat>.yaml`: `SECRETS CHECK OK as=<seat> keys=<n>`, or exit 1.

**seal** reads one line from stdin (never a terminal: pipe it in), sets `NAME` in `<seat>.yaml`, making the file
under its `.sops.yaml` rule when it is new, and commits the file on the store's current branch. Pushing is yours.

**seat add** appends the new seat's rule (`--pub` and the recovery key) to `.sops.yaml` and writes
`<new seat>.yaml` holding the `--only` values of `--from`, which `--key` must open. It never overwrites a seat, a rule
or a key another rule names, and puts `.sops.yaml` back when the encrypt fails. It commits nothing.

**keygen** makes a new age key with age-keygen, never over a file, sets it to 0600 and prints its rule:

```
SECRETS RULE   creation_rules:
SECRETS RULE     - path_regex: ^<seat>\.yaml$
SECRETS RULE       age: <public key>,<recovery key from --store, or a placeholder>
SECRETS KEYGEN OK as=<seat> key=<path> mode=0600 pub=<public key>
```

Refusals are one line on stderr, `SECRETS <VERB> REFUSED: <why>`, exit 2 (exec: 125). `--no-pr` and `--max` are
accepted and ignored, for older callers.

## Where a value goes

stdin -> sops -> the store file, encrypted; and the store file -> sops -> the child's environment. A value is never
an argument, a log line, an error or a file in the clear; sops's own output is never shown, because it can carry one.
sops runs with `SOPS_AGE_KEY_FILE=<--key>` and a HOME and config directory of its own, so no identity of the caller's
is read.

## Build and test

```
go build -o nova-secrets ./secrets
go test ./secrets                          # unit tests: a fake sops, git and age-keygen, a store in the test directory
go test -tags functional ./secrets         # real sops, age-keygen and git, keys and store in the test directory (skipped without them)
```
