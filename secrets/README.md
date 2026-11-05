# nova-secrets

sops + age + git: secrets live in a sops-encrypted file per seat in a git store; `exec` hands the named ones to one
command's environment. The full description is `docs/SPEC-SECRETS.md`.

```
nova-secrets exec --only NAME[,NAME] [--store <dir>] [--as <seat>] [--key <file>] -- cmd [args...]
nova-secrets set NAME [--store <dir>] [--as <seat>] [--key <file>]       # value on stdin
nova-secrets seat add <seat> <age-public-key> [--store <dir>]
nova-secrets help
```

```
go build -o nova-secrets ./secrets
go test ./secrets                     # unit tests: a fake sops, git and exec
go test -tags functional ./secrets    # real sops, age-keygen and git, in the test directory (skipped without them)
```
