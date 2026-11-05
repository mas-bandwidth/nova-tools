# nova-secrets

sops + age + git: secrets live in a sops-encrypted file per seat in a git store; `exec` hands the named ones to one
command's environment. A new store takes four commands (init, seat add, set, exec); `docs/SPEC-SECRETS.md` starts with them.

```
nova-secrets init --store <dir> --recovery <age-public-key>
nova-secrets seat add <seat> <age-public-key> --store <dir>
nova-secrets set NAME --store <dir> --key <file> [--as <seat>]     # value on stdin
nova-secrets names --store <dir> [--as <seat>]
nova-secrets exec --only NAME[,NAME] --store <dir> --key <file> [--as <seat>] -- cmd [args...]
nova-secrets help [verb]
```

```
go build -o nova-secrets ./secrets
go test ./secrets                     # unit tests: a fake sops, git and exec
go test -tags functional ./secrets    # real sops, age-keygen and git, in the test directory (skipped without them)
```
