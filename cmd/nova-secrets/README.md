# nova-secrets

## What it is

nova-secrets: encrypted secrets in a git repository, handed to one command at a time

## Why use it

Give a command the credentials it needs.

## Install

```sh
go install github.com/mas-bandwidth/nova-tools/cmd/nova-secrets@latest
nova-secrets version
```

## First run

The transcript below is executed line for line by the docs tests; the full record is in [docs/TESTS.md](../../docs/TESTS.md#nova-secrets).

Fixture: a throwaway secrets store git working copy and age private keys, as in
SPEC-SECRETS.md. The seat names `example` and `reader` are
fixture identities. Replace `/path/to/home` with your fixture home and
`/path/to/bin` with the directory holding your `age-keygen` and `sops` binaries.
Run the commands from that fixture home. The key directory already exists with
mode 0700. The store at `./secrets` lives
under that home, holds the reader seat and its recovery recipient, and is on a
clean branch equal to its upstream ref. Its sealed `GH_TOKEN` is synthetic;
`gh` on the fixture's PATH is a stand-in that prints `fake-gh` without making a
network call. Public keys and the commit id below belong to the recorded run.

```text
$ nova-secrets keygen --as example --key /path/to/home/.config/nova-secrets/example.key --age-keygen /path/to/bin/age-keygen --store ./secrets
SECRETS RULE   creation_rules:
SECRETS RULE     - path_regex: ^example\.yaml$
SECRETS RULE       age: age1ql3z7hjy54pw3hyww5ayyfg7zqgvc7w3j2elw8zmrj2kg5zhspjqwh35pk,age1s6kpww894xpuylmck9f2g5kz2007a8nuy6guqrjj39s0gaqf6pkqydlata
SECRETS RULE NEXT: add these two lines to .sops.yaml (or run `nova-secrets seat add`)
SECRETS KEYGEN OK as=example key=/path/to/home/.config/nova-secrets/example.key mode=0600 pub=age1ql3z7hjy54pw3hyww5ayyfg7zqgvc7w3j2elw8zmrj2kg5zhspjqwh35pk
Done. Your new key is at /path/to/home/.config/nova-secrets/example.key. Nothing failed.
Next: send this public key to whoever seals your seat: age1ql3z7hjy54pw3hyww5ayyfg7zqgvc7w3j2elw8zmrj2kg5zhspjqwh35pk

$ nova-secrets check --store ./secrets --as reader --key /path/to/home/.config/nova-secrets/reader.key --sops /path/to/bin/sops
SECRETS CHECK OK as=reader recipients=2 files=1 sealed=1 mine=1 foreign=0 clear=0 head=9750ba9

$ nova-secrets names --store ./secrets --as reader
SECRETS NAME key=GH_TOKEN clear=false
SECRETS NAMES OK as=reader keys=1 shown=1 sealed=1 clear=0

$ nova-secrets exec --store ./secrets --as reader --key /path/to/home/.config/nova-secrets/reader.key --sops /path/to/bin/sops --only GH_TOKEN --require GH_TOKEN -- gh api user --jq .login
! SECRETS EXEC OK as=reader keys=1 only=1 required=1 file=secrets/reader.yaml head=9750ba9 cmd=gh
fake-gh
```

## Verbs

The [nova-secrets section of the command reference](../../docs/CLI.md#nova-secrets) documents every verb's flags, effect and exit codes.

- `version`
- `exec`
- `names`
- `check`
- `gate`
- `keygen`
- `place`
- `placed`
- `seal`
- `seat`
- `help`

## Spec

The contract is [docs/SPEC-SECRETS.md](../../docs/SPEC-SECRETS.md).
