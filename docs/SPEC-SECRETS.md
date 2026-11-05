# nova-secrets

Keep encrypted values in git and give a command the ones it needs.
sops and age handle encryption.

A **seat** names the identity a machine or AI uses to decrypt its file. Each
seat has an age key. A recovery key can also open files that name it as a
recipient.

`exec` opens the chosen seat's file, selects names, and puts their values in
the command's environment.

## Create the store

Install sops 3.x, age-keygen and git. Configure git's commit identity before
using `init` or `set`. The **store** is a directory containing encrypted files
in its own git repository.

Choose a new store directory and a place outside it for private keys:

```sh
secrets_dir="$HOME/.local/share/nova/secrets"
key_dir="$HOME/.config/nova-secrets"
mkdir -p "$(dirname "$secrets_dir")" "$key_dir"
age-keygen -o "$key_dir/web.key"
age-keygen -o "$key_dir/recovery.key"
chmod 600 "$key_dir/web.key" "$key_dir/recovery.key"
seat_public=$(age-keygen -y "$key_dir/web.key")
recovery_public=$(age-keygen -y "$key_dir/recovery.key")
nova-secrets init --store "$secrets_dir" --recovery "$recovery_public"
nova-secrets seat add web "$seat_public" --store "$secrets_dir"
git -C "$secrets_dir" add .sops.yaml
git -C "$secrets_dir" commit -m 'Add web seat'
```

Keep the recovery private key offline after setup. Its public key stays in the
store, allowing the recovery holder to open files that name it as a recipient.

`init` accepts an absent or empty directory whose parent exists. An empty git
repository of its own is also accepted. A directory inside another git work
tree is refused. `init` creates the git repository if needed, writes
`.sops.yaml` with `creation_rules: []` and `recovery.pub`, and commits those two
files. Existing store files are refused.

| Path | Contents |
|---|---|
| `.sops.yaml` | Recipient rules, with one rule per seat. |
| `recovery.pub` | The recovery public key. |
| `<seat>.yaml` | Encrypted values. The first `set` creates this file. |

On Unix, a supplied private key must be a regular file owned by the caller,
with mode 0600. On Windows, its owner must be the caller, and its access list
must contain only the caller, SYSTEM and Administrators.

A seat name selects a file. Its recipient keys control who can decrypt it.
Choosing the name alone grants no access to the values.

## Paths and defaults

Every store operation needs `--store`, or `NOVA_SECRETS_STORE`.
`exec` and `set` also need `--key`, or `NOVA_SECRETS_KEY`.
`init` and `seat add` use public keys. `names` reads plaintext names from an
encrypted file. These three commands need no private key.

There is no default store or private-key path. The tool does not assume the
current directory. It makes the key path absolute before checking it and
passing it to sops.

`--as` defaults to `NOVA_SECRETS_SEAT`, then the short hostname.
`--sops` defaults to `sops` on PATH. The supplied key replaces the caller's
other sops identity settings.

## Commands

```text
nova-secrets init --store <dir> --recovery <age-public-key>
nova-secrets seat add <seat> <age-public-key> --store <dir>
nova-secrets set NAME --store <dir> --key <file> [--as <seat>] [--sops <path>]
nova-secrets names --store <dir> [--as <seat>]
nova-secrets exec --only NAME[,NAME] --store <dir> --key <file> [--as <seat>] [--sops <path>] -- cmd [args...]
nova-secrets help [verb]
```

## Try a value end to end

After the setup above, these commands use public example values in the new
store. `--sops` selects the helper explicitly; omit it to use PATH.

```sh
printf 'example-value\n' | nova-secrets set TOKEN --store "$secrets_dir" --key "$key_dir/web.key" --as web --sops "$(command -v sops)"
printf 'example-label\n' | nova-secrets set LABEL --store "$secrets_dir" --key "$key_dir/web.key" --as web
nova-secrets names --store "$secrets_dir" --as web
nova-secrets exec --only TOKEN --require LABEL --store "$secrets_dir" --key "$key_dir/web.key" --as web -- /bin/sh -c 'test -n "$TOKEN" && test -n "$LABEL" && printf "selected values received\n"'
```

`set` reports the encrypted file it committed. `names` prints `LABEL` and
`TOKEN`. `exec` reports the two selected names' count on stderr; the child
prints its confirmation on stdout. Supply real values from their source on
stdin instead of putting them in shell arguments.

You can reuse the setup through environment variables:

```sh
export NOVA_SECRETS_STORE="$secrets_dir"
export NOVA_SECRETS_KEY="$key_dir/web.key"
export NOVA_SECRETS_SEAT=web
nova-secrets names
nova-secrets exec --require TOKEN --require LABEL -- /bin/sh -c 'test -n "$TOKEN" && test -n "$LABEL"'
nova-secrets help exec
nova-secrets seat add --help
```

The two `--require` flags form one selection. No value is selected implicitly.

## Give values to a command

Name the values with `--only`. The tool decrypts the seat file, checks every
requested name, and starts the command only when all are present.

```sh
nova-secrets exec --only DB_PASSWORD,API_KEY --store /srv/secrets --key ~/.config/nova-secrets/web.key --as web -- ./server --port 8080
```

The command inherits the caller's environment, with two changes. Names held
by the seat are removed, then the selected values are added. The caller's
sops identity variables are also removed. An unselected value cannot arrive
through an inherited variable with the same name.

On Unix, `exec` replaces the tool with the command. It sets the core-file
size limit to zero just before that replacement. This limit does not cover
every earlier process that handled a value. On Windows, the tool runs the
command and exits with its status.

Existing units may pass repeatable `--require NAME`. It adds names to the
selection, including when `--only` is absent. Duplicate names are selected once.

## Set a value

Pipe one non-empty line into `set NAME`. A terminal or multiline value is
refused. The value is not a command-line argument.

```sh
pass show db | nova-secrets set DB_PASSWORD --store /srv/secrets --key ~/.config/nova-secrets/web.key --as web
git -C /srv/secrets push
```

`set` creates or updates the encrypted seat file through sops, then commits
that file on the store's current branch. It does not push. If git fails after
encryption, the file remains written. Inspect `git status` before retrying.

## Add a seat

Put the complete public key from age-keygen in `AGE_PUBLIC_KEY`, then run:

```sh
nova-secrets seat add web "$AGE_PUBLIC_KEY" --store /srv/secrets
```

The store must already contain `.sops.yaml` and a valid `recovery.pub`.
The new rule matches `^web\.yaml$` and names both the seat and recovery keys.
It goes before the first existing rule that would match that filename.
Other rules remain in place.

The tool refuses a seat or key that already has a rule, or YAML it cannot
parse. It writes through a temporary file and rename. Comments are preserved;
formatting changes to two-space indentation without blank lines between rules.

This command writes no seat file and makes no commit. Commit `.sops.yaml`
yourself, then use the seat's private key with `set` to create its first value.

## List names

```sh
nova-secrets names --store "$secrets_dir" --as web
```

Names are plaintext keys in the encrypted file. This command reads one seat's
file, using the usual `--as` default when omitted. It prints valid environment
variable names on stdout, one per line, without decrypting values or running
sops. The `sops` metadata block and old provenance marker are skipped.

A key with an invalid name or a nonscalar value is withheld and counted in
`refused=<n>`. The summary goes to stderr:

```text
SECRETS NAMES OK as=web names=2 refused=0
```

## Diagnosing a refusal

Missing arguments are listed together, with a pointer to the verb's help.
Use `nova-secrets help <verb>` or `<verb> --help` to print that usage with exit 0.

A missing seat file names the seats present and suggests the right `--as` or a
first `set`. A missing rule points to `seat add`. Missing sops or age-keygen is
reported by name.

After a sops failure, key diagnosis compares the supplied key's public half
with the recipient rule and file metadata. It can distinguish a wrong key from
a file that still names an older key. The recovery key can update recipients
through the sops command shown in that refusal.

When sops itself fails, its output is withheld. The error gives the command
that ran; a decrypt diagnostic directs plaintext output to `/dev/null`.
Git failures identify the operation. A failed commit can leave files written,
so inspect `git status` before retrying.

| Refusal | Next step |
|---|---|
| Missing store or private key | Pass the named flag or environment variable. Create a new store with `init` when that is the intended operation. |
| Store is nonempty, nested in another git work tree, or already initialized | Use the existing store, or choose a separate new directory with an existing parent. Do not rerun init over its files. |
| Missing recipient rule or seat file | Check `names --as <seat>` for the chosen seat; add its public-key rule, then use `set` with its private key for the first value. |
| Duplicate seat or public key | Reuse the existing rule, or choose a different identity and key. Inspect `.sops.yaml` before editing it. |
| Invalid name, empty value, terminal input or multiline value | Use a valid environment name and pipe one nonempty line into `set`. |
| Unknown selected secret | Run `nova-secrets names --store <dir> --as <seat>`, then correct `--only` or add the missing value with `set`. |
| Private key permissions or ownership refused | On Unix, use a caller-owned regular file with `chmod 600`; on Windows, correct its owner and access list. |
| Key does not match recipients | Select the right key. If the file needs recipient updates, use the recovery procedure named by the refusal. |
| Invalid YAML or missing store metadata | Inspect the named file and restore the intended structure from the store's git history. |
| Missing helper or command | Install it or correct PATH; `--sops` can name the encryption helper explicitly. |
| sops failed | Run the diagnostic shown in stderr. Its output is withheld from the tool's error report. |
| git failed | Inspect `git -C <store> status`; files may already be written. Correct git identity, permissions or repository state before committing or retrying. |

## Output and limits

The tool's own arguments, progress lines and store files contain no plaintext
values. Values pass through stdin to sops and through sops to the selected
command's environment. sops and git output is withheld because it can contain
a value; failures name the program and its exit status.

The selected command can read, print, save or send its values. Its children
inherit them. OS permissions determine who else can inspect that environment.

`exec` writes its progress to stderr:

```text
SECRETS EXEC OK as=web keys=2 cmd=server
```

| Success line | Meaning |
|---|---|
| `SECRETS INIT OK store=...` | The initial store files were committed; the line suggests adding a seat. |
| `SECRETS SEAT ADD OK seat=...` | The recipient rule was written. The next action is to commit it and set a value with the seat's key. |
| `SECRETS SET OK as=... name=... file=... committed` | The named value was encrypted and the file committed. The suggested push remains a separate action. |
| `SECRETS NAMES OK as=... names=... refused=...` | Counts of printed names and withheld keys; the names themselves are on stdout. |
| `SECRETS EXEC OK as=... keys=... cmd=...` | The selection was prepared for the named child command. Its eventual status still matters. |

`init`, `set` and `seat add` write success lines to stdout. Runtime refusals
go to stderr as `SECRETS <VERB> REFUSED: <why>`. Bad invocations use
`nova-secrets <verb>: <why>; run: nova-secrets <verb> -h`.

| Exit | Meaning |
|---|---|
| 0 | `init`, `set`, `seat add`, `names` or help finished. |
| Command status | `exec` ran the command. |
| 2 | Bad invocation, including for `exec`; or a runtime refusal outside `exec`. |
| 125 | `exec` refused at runtime; the command did not start. |
