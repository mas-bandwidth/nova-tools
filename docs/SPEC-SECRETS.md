# nova-secrets

A thin wrapper over sops, age and git. The secrets live in a sops-encrypted file in a git repository (the store).
A seat is a machine or a person with an age key. `exec` decrypts only the secrets it is told to name, puts them in
the environment of one child process, and becomes that process. Nothing else is nova-secrets' business: sops and age
do the cryptography, git carries the files.

## The model

```
<store>/.sops.yaml     creation rules: one per seat, path_regex ^<seat>\.yaml$, age: <seat key>[,<recovery key>]
<store>/recovery.pub   optional: a recovery public key every new rule also names
<store>/<seat>.yaml    the seat's secrets, sops-encrypted, one NAME: value per line
<key file>             the seat's private age key, mode 0600, owned by the user
                       (Windows: owned by the user, access list only the user, SYSTEM and Administrators)
```

A seat can open only its own file with its own key. A secret a seat should hold is set under that seat's name.
The key is a file given by `--key`; no identity from the caller's environment or home directory reaches sops.

Defaults, so a unit file stays short: `--store` is the current directory; `--as` is `$NOVA_SECRETS_SEAT`, else the
short hostname; `--key` is `~/.config/nova-secrets/<seat>.key`; `--sops` is `sops` on PATH.

## The verbs

```
nova-secrets exec --only NAME[,NAME] [--store <dir>] [--as <seat>] [--key <file>] [--sops <path>] -- cmd [args...]
nova-secrets set NAME [--store <dir>] [--as <seat>] [--key <file>] [--sops <path>]
nova-secrets seat add <seat> <age-public-key> [--store <dir>]
nova-secrets help
```

**exec** decrypts `<seat>.yaml` and becomes `cmd` (exec(2); on Windows it runs `cmd` and exits with its status).
The child's environment is the inherited one with every name the seat holds removed (a secret not asked for must not
arrive another way) and every sops identity variable removed, plus the `--only` names and their values. A name the
seat does not hold is refused, by name, and the command never runs. No core file is written after the decrypt.
`--require NAME` is accepted for the units that pass it: it only checks the name is in `--only`, which already
implies it is present.

```
nova-secrets exec --only DB_PASSWORD,API_KEY --as web -- ./server --port 8080
```

**set** reads the value from stdin, one non-empty line, never a terminal and never an argument, sets `NAME` in
`<seat>.yaml` (creating the file under its rule if new) through sops, and commits that file on the store's current
branch. Pushing is the caller's. `seal` is kept as a hidden alias of `set`, with `--name NAME`, for one release.

```
pass show db | nova-secrets set DB_PASSWORD --as web
git -C /srv/secrets push
```

**seat add** appends a rule for `<seat>` to `.sops.yaml`: the seat's public key, and the recovery key when the store
has a `recovery.pub`. It writes no seat file and commits nothing. It refuses a seat or a key that already has a rule.
The seat then makes its own file with `set`.

```
nova-secrets seat add web age1...  --store /srv/secrets
```

## The guarantee

A secret value never appears in argv, in a log line, in an error, or in a file in the clear, and so never in `ps`.
A value travels stdin to sops, sops to the store file (encrypted), and the store file through sops to the child's
environment only. sops and git output is never shown (it can carry a value); a failure names the program and its
exit status. Progress lines (`SECRETS EXEC OK as=<seat> keys=<n> cmd=<name>`) carry names and counts, never values.

## Exit codes

| code | meaning |
| --- | --- |
| 0 | `set` or `seat add` done |
| the command's own | `exec` ran the command; its status is exec's |
| 2 | refused: bad flags, unknown verb, bad name or value, key file not private, sops or git failed |
| 125 | `exec` refused and the command never ran (including an unknown secret name) |

A refusal is one line on stderr: `SECRETS <VERB> REFUSED: <why>`.
