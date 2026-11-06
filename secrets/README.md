# nova-secrets

nova-secrets keeps encrypted values in a git repository and hands a command
only the ones it asks for, in its environment. sops and age do the encryption.

The **store** is a directory of encrypted files, a git
repository of its own. A **seat** is a name for one identity that reads values:
it has one file, `<seat>.yaml`, and one age key. A **recipient** is an age
public key allowed to decrypt a file. The **recovery key** is one more age key,
kept offline, that is a recipient of every seat file.

## Install

You need sops 3.x, age-keygen and git, with a git commit identity configured.

```sh
git clone -q https://github.com/mas-bandwidth/nova-tools
go build -C nova-tools -o "$PWD/bin/nova-secrets" ./secrets
export PATH="$PWD/bin:$PATH"
```

## Use

The blocks share one shell session. This one makes a temporary store and key
directory; for real use choose permanent places, keys outside the store. Seat
names are letters, digits, `-` and `_`; `web` is one example.

### init and seat add
```sh
work=$(mktemp -d)
secrets_dir="$work/store"
key_dir="$work/keys"
mkdir -p "$key_dir"
age-keygen -o "$key_dir/web.key"
age-keygen -o "$key_dir/recovery.key"
chmod 600 "$key_dir/web.key" "$key_dir/recovery.key"
nova-secrets init --store "$secrets_dir" --recovery "$(age-keygen -y "$key_dir/recovery.key")"
nova-secrets seat add web "$(age-keygen -y "$key_dir/web.key")" --store "$secrets_dir"
```

Output (paths vary):
```text
SECRETS INIT OK store=/tmp/tmp.k3Jd9x/store next: nova-secrets seat add <seat> <age-pubkey> --store /tmp/tmp.k3Jd9x/store
SECRETS SEAT ADD OK seat=web committed; next: the seat's own key runs: nova-secrets set NAME --as web --store /tmp/tmp.k3Jd9x/store --key <file>
```

`init` takes an absent or empty directory whose parent exists, and
writes `.sops.yaml`, `recovery.pub` and one commit. `seat add` adds a rule
naming the seat's key and the recovery key, and commits `.sops.yaml`. Keep the
recovery private key offline.

### set
```sh
printf 'example-value\n' | nova-secrets set TOKEN --store "$secrets_dir" --key "$key_dir/web.key" --as web
printf 'example-label\n' | nova-secrets set LABEL --store "$secrets_dir" --key "$key_dir/web.key" --as web
```

```text
SECRETS SET OK as=web name=TOKEN file=web.yaml committed; run: git -C /tmp/tmp.k3Jd9x/store push
SECRETS SET OK as=web name=LABEL file=web.yaml committed; run: git -C /tmp/tmp.k3Jd9x/store push
```

The value is one non-empty line on stdin. `set` encrypts it into
`web.yaml` and commits that file. Pushing is yours.

### names and seat list
```sh
nova-secrets names --store "$secrets_dir" --as web
nova-secrets seat list --store "$secrets_dir"
```

```text
LABEL
TOKEN
SECRETS NAMES OK as=web names=2 refused=0
web file=yes rule=yes
SECRETS SEAT LIST OK seats=1
```

`names` reads the plaintext keys of one seat's file; no key is needed and no
value is decrypted. `seat list` prints one line per seat that has a rule or a
file. The `SECRETS ... OK` lines go to stderr.

### exec
```sh
nova-secrets exec --only TOKEN,LABEL --store "$secrets_dir" --key "$key_dir/web.key" --as web -- /bin/sh -c 'printf "%s %s\n" "${#TOKEN}" "${#LABEL}"'
```

```text
SECRETS EXEC OK as=web keys=2 cmd=sh
13 13
```

`exec` decrypts the seat file and starts the command, with `--only` values
added to its environment. The command's environment is the caller's, except
that names the seat holds are removed, sops identity variables and
`NOVA_SECRETS_KEY`, `NOVA_SECRETS_STORE` and `NOVA_SECRETS_SEAT` are removed,
and the selected values are added. The command never learns where the key is.
On Unix `exec` replaces the tool and sets the core-file size limit to zero.
The exit status is the command's.

### Defaults and help
`--store` may come from `NOVA_SECRETS_STORE`, `--key` from `NOVA_SECRETS_KEY`,
and `--as` from `NOVA_SECRETS_SEAT` (then the short hostname). `--sops` is
`sops` on PATH.

```sh
export NOVA_SECRETS_STORE="$secrets_dir" NOVA_SECRETS_KEY="$key_dir/web.key" NOVA_SECRETS_SEAT=web
nova-secrets exec --only TOKEN -- /bin/sh -c 'test -n "$TOKEN"'
nova-secrets help exec
```

`nova-secrets help [verb]` and `<verb> -h` print usage with exit 0. `help`
takes no flags: `help -h` exits 2.

## Refusals

Runtime refusals print one line, `SECRETS <VERB> REFUSED reason=<reason>: <why>`,
on stderr and exit 2 (`exec`: 125, and the command never starts, including a
command not found). A bad invocation prints
`nova-secrets <verb>: <why>; run: nova-secrets <verb> -h` and exits 2.

The reason is one word, and neither it nor the rest of the line holds a value.
`no-store` means the store directory is absent, `no-seat` that the seat has no
file or no rule, `no-key` that the key file is missing or not private, and
`not-recipient` that the key is not among the seat's recipients, in the rule or
in the file. `no-name` means a selected name is absent from the seat, `no-sops`
that sops was not found, and `no-command` that exec's command was not found.
`sops` and `git` mean that program failed, and `other` covers the rest. A caller
that hides a command's output, such as an Ansible task with `no_log`, can show
this line by matching it (`SECRETS EXEC REFUSED`, `nova-secrets exec:`): the
match is the guard, never the exit code, since `exec` passes the command's own
status through.

| Refusal | Next step |
|---|---|
| Missing `--store`, `--key` or command | Pass the flag or set its variable. |
| Store exists, is not empty, or is inside another git work tree | Use a new directory with an existing parent. |
| `--recovery` is not an age public key | Pass `age-keygen -y <file>` output. |
| `usage: seat add <seat> <age1...>; the seat is letters, digits, - and _` | Use a valid seat name and a full `age1` key (`age1` and 58 lowercase letters and digits). A seat refuses the recovery key (`that key is the recovery key, not a seat's`). |
| No rule or no seat file | `seat add`, then `set` with the seat's key. |
| Key file must be mode 0600 and owned by you | `chmod 600` the file. On Windows the owner may be you, SYSTEM or Administrators, and only those may have access. |
| Key is not the key in `.sops.yaml` for the seat | Use the right `--key` and `--as`. To change a seat's key, edit `.sops.yaml` and run `sops updatekeys` with the recovery key, as the refusal shows. |
| `set: empty or multi-line value on stdin; the value must be one non-empty line` | Pipe one non-empty line without a NUL byte; a terminal is refused. |
| `nova-secrets set: value is over 65536 bytes; a secret is a key or token` (exit 2) | Store a key or token, not a file. Nothing was written. |
| Unknown selected secret | Check `names`, then fix `--only` or `set` it. |
| `"bad-key" is not an environment variable name, or its value holds a NUL` | `exec` refuses the whole file, exit 125; `names` lists the other names and counts it in `refused=`. Remove the key with sops. |
| sops or git failed | The output is withheld because it can hold a value. Run the command shown in the error; after a git failure inspect `git status`, since files may be written. |

## Guarantees and limits

Guarantees:

- No argument, progress line or store file holds a plaintext value; sops and git output is withheld from errors.
- `set` sends the value to sops on stdin. In `exec`, sops writes the decrypted file to a pipe the tool reads.

Limits:

- The command can read, print, save or send its values, and so can its children.
- A value is at most 65536 bytes; `set` refuses a longer one and writes nothing.

How the tool is tested is in [TESTING.md](../docs/TESTING.md).
