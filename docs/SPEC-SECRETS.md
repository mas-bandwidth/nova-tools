# nova-secrets

A thin wrapper over sops, age and git. Secrets live in sops-encrypted files in a git repository (the store), one file
per seat. A seat is a machine or a person with an age key. `exec` decrypts only the secrets it is told to name, puts
them in the environment of one child process, and becomes that process. sops and age do the cryptography. git carries
the files.

## Create a store

Four commands, in this order. You need `sops` 3.x, `age-keygen`, `git`, and this binary. Below, `S` is the store
directory and `K` is a seat's key file.

**0. Make two keys.** One is the seat's. One is the recovery key: its public half goes in the store, its private half
stays offline.

```
age-keygen -o web.key && chmod 600 web.key
age-keygen -y web.key                       # prints the seat's public key
age-keygen -o recovery.key
age-keygen -y recovery.key                  # prints the recovery public key
```

**1. init** creates the store: `git init` when `S` is in no repository, `.sops.yaml` with `creation_rules: []`,
`recovery.pub`, a `README.md`, and one commit. `S` may be missing (its parent must exist) or an empty directory.
It refuses a store that exists.

```
nova-secrets init --store S --recovery <recovery public key>
SECRETS INIT OK store=S next: nova-secrets seat add <seat> <age-pubkey> --store S
```

**2. seat add** adds the seat's rule to `.sops.yaml`: the seat's key and the recovery key may open `<seat>.yaml`.
It commits nothing.

```
nova-secrets seat add web <seat public key> --store S
SECRETS SEAT ADD OK seat=web next: commit .sops.yaml, then the seat's own key runs: nova-secrets set NAME --as web --store S --key <file>
```

**3. set** reads one value from stdin, never from a terminal or an argument. It writes it to `<seat>.yaml` and commits
that file. Pushing is yours.

```
printf '%s\n' "$VALUE" | nova-secrets set DB_PASSWORD --store S --key web.key --as web
SECRETS SET OK as=web name=DB_PASSWORD file=web.yaml committed; run: git -C S push
```

**4. exec** hands named secrets to one command.

```
nova-secrets exec --only DB_PASSWORD --store S --key web.key --as web -- ./server --port 8080
SECRETS EXEC OK as=web keys=1 cmd=server
```

The OK line of `exec` goes to stderr, because stdout belongs to the command.

## List the names

**names** lists the secret names in a seat file. A sops file keeps its keys in the clear, so no key and no sops are
needed, and no value is read. It lists the top-level keys and skips the `sops:` block.

```
nova-secrets names --store S --as web
DB_PASSWORD
SECRETS NAMES OK store=S seats=1 names=1
```

The names are the payload on stdout, one per line. The OK line goes to stderr. Without `--as`, every seat file is
listed, one `<seat> NAME` per line.

## The model

```
<store>/.sops.yaml     creation rules: one per seat, path_regex ^<seat>\.yaml$, age: <seat key>,<recovery key>
<store>/recovery.pub   the recovery public key every rule also names
<store>/<seat>.yaml    the seat's secrets, sops-encrypted, one NAME: value per line
<key file>             the seat's private age key, mode 0600, owned by the user
                       (Windows: owned by the user, access list only the user, SYSTEM and Administrators)
```

A seat opens only its own file with its own key. The key is a file given by `--key`. No identity from the caller's
environment or home directory reaches sops.

`--store` is required for every verb, and `--key` for `set` and `exec`, as flags or as `NOVA_SECRETS_STORE` and
`NOVA_SECRETS_KEY`. There is no default and the current directory is never assumed. `--key` is made absolute before
its mode and owner are checked and before sops sees it. `--as` defaults to `$NOVA_SECRETS_SEAT`, else the short
hostname. `--sops` defaults to `sops` on PATH.

`<verb> -h` and `help <verb>` print one verb's usage and exit 0.

## The verbs

```
nova-secrets init --store <dir> --recovery <age-public-key>
nova-secrets seat add <seat> <age-public-key> --store <dir>
nova-secrets set NAME --store <dir> --key <file> [--as <seat>] [--sops <path>]
nova-secrets names --store <dir> [--as <seat>]
nova-secrets exec --only NAME[,NAME] --store <dir> --key <file> [--as <seat>] [--sops <path>] -- cmd [args...]
nova-secrets help [verb]
```

**exec** decrypts `<seat>.yaml` and becomes `cmd` (exec(2); on Windows it runs `cmd` and exits with its status).
The child's environment is the inherited one with every name the seat holds removed, so a secret not asked for cannot
arrive another way, and every sops identity variable removed, plus the `--only` names and their values. A name the
seat does not hold is refused, by name, and the command never runs. No core file is written after the decrypt.
`--require NAME` is kept for existing units: it means the same as `--only` and goes in the next release.

**set** creates `<seat>.yaml` under its rule when it is new. The value is one non-empty line. An empty stdin, such as
`/dev/null`, is refused as an empty value.

**seat add** parses `.sops.yaml` and writes it back with the new rule. sops uses the first rule that matches a file,
so the rule goes before the first existing rule that would match the seat's file (a catch-all). Every existing rule
is kept. A file that does not parse is refused and left alone. The write is a temporary file and a rename. A seat or a
key that already has a rule is refused. Comments are kept. Formatting is normalised: 2-space indent, no blank lines
between rules.

## Refusals say what to run

A malformed invocation is one line on stderr and exit 2, for `exec` too: `nova-secrets <verb>: <what is wrong>; run: nova-secrets <verb> -h`.
It names every missing flag at once, for example `nova-secrets exec: missing --only NAME[,NAME], --store <dir> (or NOVA_SECRETS_STORE), --key <file> (or NOVA_SECRETS_KEY), a command after -- (no default; the current directory is never assumed); run: nova-secrets exec -h`.
An unknown verb is named and listed, and points at `nova-secrets help`.

A runtime refusal is one line on stderr: `SECRETS <VERB> REFUSED: <why>; run: <next command>`. These are the common ones.

| refusal | next command |
| --- | --- |
| `store S exists` | `seat add` |
| `seat <s> cannot decrypt <s>.yaml: its key is not among the file's recipients (<n> recipients)` | `nova-secrets seat add <s> <public key> --store S` |
| `sops not found at <path>; install sops 3.x` | install sops, or pass `--sops <path>` |
| `S is not a git repository` | `nova-secrets init --store S --recovery <key>` |
| `seat <s> has no file in S` | `nova-secrets set NAME --as <s> --store S --key <file>` |
| `seat <s> has no rule in S/.sops.yaml` | `nova-secrets seat add <s> <public key> --store S` |
| `store S has no valid recovery.pub` | `nova-secrets init ...` |
| `sops -d failed (exit n); its output is withheld; ran: ...` | rerun the printed command |

The recipients are read from the sops metadata of the file. No value is read for a diagnosis. When sops itself fails,
the line carries the sops command that ran (program, flags, file), never its output.

## The guarantee

A secret value never appears in argv, in a log line, in an error, or in a file in the clear, so never in `ps`. A value
travels stdin to sops, sops to the store file (encrypted), and the store file through sops to the child's environment
only. sops and git output is never shown, because it can carry a value. A failure names the program and its exit
status. Progress lines carry names and counts, never values.

## Exit codes

| code | meaning |
| --- | --- |
| 0 | `init`, `seat add`, `set`, `names` or help done |
| the command's own | `exec` ran the command; its status is exec's |
| 2 | refused: a malformed invocation (bad or missing flags, unknown verb, bad name), or a runtime refusal outside `exec` (key file not private, sops or git failed) |
| 125 | `exec` refused at run time and the command never ran: an unknown secret name, a decrypt failure, a command not found. The units rely on it. |
