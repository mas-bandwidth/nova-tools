# nova-secrets

Give a command selected values from an encrypted git store. sops and age
encrypt the files. The command receives selected values in its environment.

A **seat** names the identity a machine or AI uses to decrypt its file. Each
seat has an age key. A recovery key can also open files that name it as a
recipient.

Start with [creating a store](../docs/SPEC-SECRETS.md#create-the-store). The
store is the directory of encrypted files in git. You need sops, age-keygen and
git, with a commit identity configured. Keep private keys outside that directory.

Create the store, add a named key, set a value, then start a command with the
names it needs:

```text
nova-secrets init --store <dir> --recovery <age-public-key>
nova-secrets seat add <seat> <age-public-key> --store <dir>
nova-secrets set NAME --store <dir> --key <file> [--as <seat>]
nova-secrets names --store <dir> [--as <seat>]
nova-secrets exec --only NAME[,NAME] --store <dir> --key <file> [--as <seat>] -- cmd [args...]
nova-secrets help [verb]
```

Pipe the value into `set`; it commits the encrypted file. Use git to push it.
`names` lists the names available for one seat without decrypting the values.
`exec` adds selected values to the command's environment.

`seat add` edits recipient rules. Commit that change, then use `set` to create
the seat's first encrypted file. Each rule includes the recovery public key.

The [spec](../docs/SPEC-SECRETS.md) covers defaults, key checks, output and
refusals. The command receiving the values can use or disclose them.

## Build and test

From the repository root, on a build machine:

```sh
build_dir=$(mktemp -d)
go build -o "$build_dir/nova-secrets" ./secrets
"$build_dir/nova-secrets" help
go test ./secrets
go test -tags functional ./secrets
```

Unit tests substitute sops, git and command execution. Functional tests use
real sops, age-keygen and git in a temporary store. They skip when those tools
are absent. See [TESTING.md](../docs/TESTING.md) for the shared checks.
