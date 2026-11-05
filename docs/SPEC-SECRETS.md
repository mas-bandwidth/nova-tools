# nova-secrets

A thin wrapper over sops, age and git. The secrets live in a sops-encrypted file in a git repository (the store).
A seat is a machine or a person with an age key. `exec` decrypts only the secrets it is told to name, puts them in
the environment of one child process, and becomes that process. Nothing else is nova-secrets' business: sops and age
do the cryptography, git carries the files.

## The model

```
<store>/.sops.yaml     creation rules: one per seat, path_regex ^<seat>\.yaml$, age: <seat key>[,<recovery key>]
<store>/recovery.pub   required by `seat add`: the recovery public key every rule also names
<store>/<seat>.yaml    the seat's secrets, sops-encrypted, one NAME: value per line
<key file>             the seat's private age key, mode 0600, owned by the user
                       (Windows: owned by the user, access list only the user, SYSTEM and Administrators)
```

A seat can open only its own file with its own key. A secret a seat should hold is set under that seat's name.
The key is a file given by `--key`; no identity from the caller's environment or home directory reaches sops.

`--store` and `--key` are required, as flags or as `NOVA_SECRETS_STORE` and `NOVA_SECRETS_KEY`; there is no default
and the current directory is never assumed (a store found by cwd could be any repository). `--key` is made absolute
before its mode and owner are checked and before sops sees it, so the file checked is the file read. `--as` defaults
to `$NOVA_SECRETS_SEAT`, else the short hostname; `--sops` to `sops` on PATH.

A seat key is made with `age-keygen -o <file>` and `chmod 600 <file>`; its public key is what `seat add` takes.

## The verbs

```
nova-secrets exec --only NAME[,NAME] --store <dir> --key <file> [--as <seat>] [--sops <path>] -- cmd [args...]
nova-secrets set NAME --store <dir> --key <file> [--as <seat>] [--sops <path>]
nova-secrets seat add <seat> <age-public-key> --store <dir>
nova-secrets help
```

**exec** decrypts `<seat>.yaml` and becomes `cmd` (exec(2); on Windows it runs `cmd` and exits with its status).
The child's environment is the inherited one with every name the seat holds removed (a secret not asked for must not
arrive another way) and every sops identity variable removed, plus the `--only` names and their values. A name the
seat does not hold is refused, by name, and the command never runs. No core file is written after the decrypt.
`--require NAME` is accepted for the units that pass it: it only checks the name is in `--only`, which already
implies it is present.

```
nova-secrets exec --only DB_PASSWORD,API_KEY --store /srv/secrets --key ~/.config/nova-secrets/web.key --as web -- ./server --port 8080
```

**set** reads the value from stdin, one non-empty line, never a terminal and never an argument, sets `NAME` in
`<seat>.yaml` (creating the file under its rule if new) through sops, and commits that file on the store's current
branch. Pushing is the caller's. `seal` is kept as a hidden alias of `set`, with `--name NAME`, for one release.

```
pass show db | nova-secrets set DB_PASSWORD --store /srv/secrets --key ~/.config/nova-secrets/web.key --as web
git -C /srv/secrets push
```

**seat add** parses `.sops.yaml` and writes it back with a rule for `<seat>`: `path_regex: ^<seat>\.yaml$`, `age:` the
seat's public key and the store's `recovery.pub`. The store must have a valid `recovery.pub`, else it refuses. sops
uses the first rule that matches a file, so the new rule goes before the first existing rule that would match the
seat's file (a catch-all), and every existing rule is kept. A file that does not parse is refused and left alone; the
write is a temporary file and a rename. It writes no seat file and commits nothing, and refuses a seat or a key that
already has a rule. Comments in `.sops.yaml` are kept; formatting is normalised (2-space indent, blank lines between rules dropped). The seat then makes its own file with `set`.

```
nova-secrets seat add web age1... --store /srv/secrets
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
