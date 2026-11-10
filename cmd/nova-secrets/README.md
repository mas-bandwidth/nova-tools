# nova-secrets

nova-secrets keeps encrypted values in a git repository and hands a command
only the ones it asks for, in its environment. sops and age do the encryption.

The **store** is a directory of encrypted files, a git
repository of its own. A **seat** is a name for one identity that reads values:
it has one file, `<seat>.yaml`, and one age key. A **recipient** is an age
public key allowed to decrypt a file. The **recovery key** is one more age key,
kept offline, that is a recipient of every seat file; `recovery` is not a seat
name. The **control seat** is a seat,
usually the control node's, that copies its own values into other seats with
`grant`; its key never opens what it writes there. A **pin** is a public key the
control node keeps outside the store, so that no edit to the store can change
who a grant reaches.

## Install

[Install one tool](../../docs/README.md#install-one-tool), choosing `tool=secrets`.
The [shared guide](../../docs/README.md) covers choosing tools and trying them together.
You need sops 3.x, age-keygen and git, with a git commit identity configured.

For a local trial, start at [init and seat add](#init-and-seat-add): those blocks
use explicit temporary paths and example values. The fresh-node shortcut below
creates the permanent store and keys at their defaults.

## Use

### A fresh node: one set makes it all

On a node with nothing at the defaults (no `~/.config/nova-secrets/store`, no
`<short hostname>.key`, no `recovery.key`, no `pins/recovery.pub`, and no
`NOVA_SECRETS_STORE`), the first `set` makes them before it sets: a recovery
key at `~/.config/nova-secrets/recovery.key` (0600) with its public half pinned
in `pins/recovery.pub`, the store (as `init`), this node's key (0600) and its
seat (as `seat add`). It prints one line first, and prints it even when a later
step is refused, since the recovery key exists from then on:

```text
SECRETS SET NEW store=/home/you/.config/nova-secrets/store seat=studio key=/home/you/.config/nova-secrets/studio.key: back up /home/you/.config/nova-secrets/recovery.key, the recovery key, off this machine now: it opens every seat if a key is lost
```

On a terminal `set` asks for the value and does not show it:
`nova-secrets set TAILSCALE_API_KEY`, paste, enter. With no store but any of
the others there it refuses, `reason=partial`, and writes nothing: a store made
then would not match them, and nothing that exists is overwritten. A `set` that
stopped at a failed commit is finished by the same `set` again, with the keys it
made.

The blocks below share one shell session. This one makes a temporary store and key
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
nova-secrets init --store "$secrets_dir" --pins "$work/pins" --recovery "$(age-keygen -y "$key_dir/recovery.key")"
nova-secrets seat add web "$(age-keygen -y "$key_dir/web.key")" --store "$secrets_dir"
```

Output (paths vary):
```text
SECRETS INIT OK store=/tmp/tmp.k3Jd9x/store
next: age-keygen -o /home/you/.config/nova-secrets/web.key
SECRETS SEAT ADD OK seat=web committed
next: nova-secrets set DB_PASSWORD --as web --store /tmp/tmp.k3Jd9x/store --pins /tmp/tmp.k3Jd9x/pins
```

Each OK line is followed by `next: <command>`, the command to run next,
shell-quoted. After `init` it is this
node's `seat add` (the seat `$NOVA_SECRETS_SEAT`, else the short hostname, with
the public half of `$NOVA_SECRETS_KEY` or `~/.config/nova-secrets/<seat>.key`),
or the `age-keygen -o` that makes that key while there is none. After `seat add`
it is the `set` as the seat just added, on the `--store` given, with `--pins`
from `$NOVA_SECRETS_PINS`, else `pins` beside the store when it holds this
store's recovery pin (as `init --pins` writes it above), else
`~/.config/nova-secrets/pins`. It carries `--key`
only when that seat's key file (`~/.config/nova-secrets/<seat>.key`, or
`$NOVA_SECRETS_KEY` for this node's own seat) is on this machine and is the key
just added; otherwise it has no `--key`, and `set` reads `$NOVA_SECRETS_KEY`.

`init` takes an absent or empty directory whose parent exists, and
writes `.sops.yaml`, `recovery.pub` and one commit; when that commit fails
(git has no commit identity, for one), the same `init` again commits them. `--pins` also writes the
recovery key to `<dir>/recovery.pub` outside the store: the **pin** that `set`
and `grant` seal to, whatever the store's copy says. Every machine that writes
the store has a pin directory; `set --recovery <key>` writes the pin on one that
has none. `seat add` adds a rule
naming the seat's key and the recovery key, and commits `.sops.yaml`. Keep the
recovery private key offline.

### set
```sh
printf 'example-value\n' | nova-secrets set TOKEN --store "$secrets_dir" --pins "$work/pins" --key "$key_dir/web.key" --as web
printf 'example-label\n' | nova-secrets set LABEL --store "$secrets_dir" --pins "$work/pins" --key "$key_dir/web.key" --as web
```

```text
SECRETS SET OK as=web name=TOKEN file=web.yaml committed; run: git -C /tmp/tmp.k3Jd9x/store push
SECRETS SET OK as=web name=LABEL file=web.yaml committed; run: git -C /tmp/tmp.k3Jd9x/store push
```

The value is one non-empty line on stdin. `set` encrypts it into `web.yaml`,
to the public half of `--key` and the recovery pin only, with sops told to
ignore `.sops.yaml`, and commits that file. The seat's rule must name those two
keys, so that `exec` on another copy agrees; a rule whose seat key was replaced
is refused as `key-changed`, and nothing is written. Pushing is yours.

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

`names` reads the plaintext keys of one seat's file and its grants file; no key is needed and no
value is decrypted. `seat list` prints one line per seat that has a rule or a
file. The `SECRETS ... OK` lines go to stderr.

### exec
```sh
nova-secrets exec --only TOKEN,LABEL --store "$secrets_dir" --key "$key_dir/web.key" --as web -- /bin/sh -c 'printf "%s %s\n" "${#TOKEN}" "${#LABEL}"'
```

```text
SECRETS EXEC STARTING as=web keys=2 cmd=sh
13 13
```

The `SECRETS EXEC STARTING` line, and any refusal, goes to stderr; stdout is the
command's alone, byte for byte, so a command that prints JSON parses through `exec`.

`exec` decrypts the seat file, and the seat's grants file when there is one, and starts the command, with `--only` values
added to its environment. The command's environment is the caller's, except
that names the seat holds are removed, sops identity variables and
`NOVA_SECRETS_KEY`, `NOVA_SECRETS_STORE` and `NOVA_SECRETS_SEAT` are removed,
and the selected values are added. The command never learns where the key is.
On Unix `exec` replaces the tool and sets the core-file size limit to zero,
so the `STARTING` line is the last thing the tool prints: the command's exit
status is yours to read, and a command killed by a signal is reported by the
shell as 128+N, as for any process. The exit status is the command's.

### Defaults and help
`--store` may come from `NOVA_SECRETS_STORE`, `--key` from `NOVA_SECRETS_KEY`,
`--pins` from `NOVA_SECRETS_PINS`, and `--as` from `NOVA_SECRETS_SEAT` (then the
short hostname). Unset, every verb that takes them uses
`~/.config/nova-secrets/store`, `~/.config/nova-secrets/<seat>.key` and
`~/.config/nova-secrets/pins`. `--sops` is
`sops` on PATH.

```sh
export NOVA_SECRETS_STORE="$secrets_dir" NOVA_SECRETS_KEY="$key_dir/web.key" NOVA_SECRETS_SEAT=web NOVA_SECRETS_PINS="$work/pins"
nova-secrets exec --only TOKEN -- /bin/sh -c 'test -n "$TOKEN"'
nova-secrets help exec
```

`nova-secrets help [verb]` and `<verb> -h` print usage with exit 0. `help`
takes no flags: `help -h` exits 2.

## A fleet: the control node fills every machine's seat

In a fleet each machine has its own seat, and its key never leaves it. The
control node's seat holds the values every machine needs, such as
`NOVA_BUS_PASSWORD`. `grant` copies named values from that seat into a
machine's grants file, `<seat>.grants.yaml`, with the control node's own key.
The recovery key stays offline, and the control node's key cannot open what it
wrote; the last `exec`, as `db` with the control key, is refused.

This continues the session above: `web` plays the control node, and `db` is a
machine that joins afterwards. On a real fleet `db`'s key is made on the `db`
machine, and the control node reads only its public half, over SSH.

```sh
printf 'example-bus-password\n' | nova-secrets set NOVA_BUS_PASSWORD --store "$secrets_dir" --pins "$work/pins" --key "$key_dir/web.key" --as web
age-keygen -o "$key_dir/db.key"
chmod 600 "$key_dir/db.key"
nova-secrets seat add db "$(age-keygen -y "$key_dir/db.key")" --store "$secrets_dir"
nova-secrets grant NOVA_BUS_PASSWORD --to db --pub "$(age-keygen -y "$key_dir/db.key")" --pins "$work/pins" --store "$secrets_dir" --key "$key_dir/web.key" --as web
nova-secrets grant NOVA_BUS_PASSWORD --to db --pins "$work/pins" --store "$secrets_dir" --key "$key_dir/web.key" --as web
nova-secrets exec --only NOVA_BUS_PASSWORD --store "$secrets_dir" --key "$key_dir/db.key" --as db -- /bin/sh -c 'printf "%s\n" "${#NOVA_BUS_PASSWORD}"'
nova-secrets exec --only NOVA_BUS_PASSWORD --store "$secrets_dir" --key "$key_dir/web.key" --as db -- true || echo "exit $?"
```

```text
SECRETS SET OK as=web name=NOVA_BUS_PASSWORD file=web.yaml committed; run: git -C /tmp/tmp.k3Jd9x/store push
SECRETS SEAT ADD OK seat=db committed
next: nova-secrets set DB_PASSWORD --as db --store /tmp/tmp.k3Jd9x/store --pins /tmp/tmp.k3Jd9x/pins
SECRETS GRANT OK from=web to=db names=1 changed=1 committed; run: git -C /tmp/tmp.k3Jd9x/store push
SECRETS GRANT OK from=web to=db names=1 changed=0 unchanged
SECRETS EXEC STARTING as=db keys=1 cmd=sh
20
SECRETS EXEC REFUSED reason=not-recipient: key age1... is not the key in .sops.yaml for db (age1...): wrong --key/--as, or the key was replaced; ...
exit 125
```

`grant` reads the named values from the granting seat (`--as`) and writes the
target's grants file whole, keeping the names this node granted before, so
granting a second name does not drop the first. Which names those are comes
from the ledger beside the pins, never from the file in the store: a file whose
names differ from the ledger's is refused as `grants-changed`, so a key someone
plants in it is never filled. It encrypts the file to two keys only, the
target's pin and the recovery pin, with sops told to ignore `.sops.yaml`, and
before writing it checks that sops sealed the file to exactly those two keys.
A grant that would change nothing writes and commits nothing, so running it
again is safe, as the second `grant` shows; the ledger, `<seat>.ledger`, holds
HMACs keyed by the granting key, never a plain hash of a value, and the digest
of the file this node wrote, so a grants file a pusher re-sealed to the same
keys with other values is rewritten, not reported unchanged.

The pins live in the `--pins` directory (or `NOVA_SECRETS_PINS`) on the control
node: `recovery.pub` and one `<seat>.pub` per machine. The directory is outside
the store and outside any git work tree (a pin a pusher could write is no pin),
and it and each pin must be yours and writable by no one else. The first grant to a seat needs
`--pub` with the machine's public key, read from the machine itself; later
grants check the pin. `grant` refuses with `key-changed` when the pin and the
seat's rule in `.sops.yaml` disagree, or when the store's `recovery.pub` is not
the recovery pin. A rebuilt machine with a new key is a person's to re-pin, by
removing its pin file and granting with `--pub`.

A name lives in one file: `set` refuses a name the seat holds in its grants
file, and `exec` and `names` refuse a seat whose two files share a name. A seat
file that is a symlink or anything but a regular file inside the store is
refused as `not-regular`, so no edit to the store can make a grant rewrite
another seat's file. Pushing the store, or copying it to each machine, is yours;
the tool needs no remote.

### seat remove
```sh
nova-secrets seat remove db --store "$secrets_dir" --pins "$work/pins" || echo "exit $?"
rm "$work/pins/db.ledger" "$work/pins/db.pub"
nova-secrets seat remove db --store "$secrets_dir" --pins "$work/pins"
nova-secrets seat list --store "$secrets_dir"
```

```text
SECRETS SEAT REMOVE REFUSED reason=other: this node granted db values (/tmp/tmp.k3Jd9x/pins/db.ledger); delete that ledger and the pin /tmp/tmp.k3Jd9x/pins/db.pub first, then run again
exit 2
SECRETS SEAT REMOVE OK seat=db rule=removed files=1 committed; run: git -C /tmp/tmp.k3Jd9x/store push
web file=yes rule=yes
SECRETS SEAT LIST OK seats=1
```

`seat remove` takes a machine's rule and both its files out of the store in one
commit, for a machine that has left the fleet. It is refused while
`<pins>/<seat>.ledger` says this node granted the seat values, and for this
node's own seat while any ledger remains, so a seat is never removed under a
standing grant by accident; the two files to delete are named. Its key on the
machine is not this tool's to find. A single value is removed from a seat with
sops, which seals the file again to its own recipients:
`sops unset "$secrets_dir/web.yaml" '["LABEL"]'`.

## Clone a private repository on a fleet machine

On a machine `nova-fleet join` set up, every shell of the login, including a
plain `ssh <machine> '<cmd>'`, has `NOVA_SECRETS_STORE` (the pushed store) and
`NOVA_SECRETS_SEAT` (the machine's seat, its inventory name), so a bare `exec`
opens that seat.
This is the one way to clone a private repository there. The token reaches git
only through a credential helper that reads it from the environment: it is
never on argv, in a file, in git's config or in the output.

```sh
nova-secrets exec --only GH_GATE_TOKEN -- git -c credential.helper= -c credential.helper='!f() { test "$1" = get && echo username=x-access-token && echo "password=$GH_GATE_TOKEN"; }; f' clone https://github.com/<owner>/<repo>.git
```

`git ls-remote` in place of `clone` checks access without fetching anything.
A later `fetch` or `pull` in that clone takes the same two `-c` options inside
the same `exec`. Through `ssh`, send the recipe on stdin so your local shell
expands nothing and its single quotes stay intact:

```sh
ssh <machine> sh -s <<'END'
nova-secrets exec --only GH_GATE_TOKEN -- git -c credential.helper= -c credential.helper='!f() { test "$1" = get && echo username=x-access-token && echo "password=$GH_GATE_TOKEN"; }; f' ls-remote https://github.com/<owner>/<repo>.git
END
```
`unset NOVA_SECRETS_SEAT` or `--as` is never needed for this; a refusal names
the store or seat it tried.

## Refusals

Runtime refusals print one line, `SECRETS <VERB> REFUSED reason=<reason>: <why>`,
on stderr and exit 2 (`exec`: 125, and the command never starts, including a
command not found). A bad invocation prints
`nova-secrets <verb>: <why>; run: nova-secrets <verb> -h` and exits 2.

The reason is one word, and neither it nor the rest of the line holds a value.
`no-store` means the store directory is absent, `partial` that `set` found no
store at the default but a key, the recovery key or the recovery pin there, `no-seat` that the seat has no
file or no rule, `no-key` that the key file is missing or not private, and
`not-recipient` that the key is not among the seat's recipients, in the rule or
in the file. `no-name` means a selected name is absent from the seat,
`untrusted-rule` that `set` was refused because the seat's rule is not the one
`seat add` writes or `.sops.yaml` uses YAML anchors, aliases or merge keys,
`wrong-recipients` that sops sealed a file to other keys than expected and
nothing was written, `no-pin` that a pin is missing or not private,
`key-changed` that a pin or `--key` disagrees with the store, `grants-changed`
that a grants file's names are not the ledger's, `duplicate-name` that a name
is in both of a seat's files, `not-regular` that a seat file is not a regular
file, `recovery-key` that `--key` is the recovery key, which is for recovery
only, `no-sops`
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
| Key file must be mode 0600 and owned by you | `chmod 600` the file. |
| Key is not the key in `.sops.yaml` for the seat (`not-recipient`) | Use the right `--key` and `--as`. The control node's key opens no machine's grants file, by design. To change a seat's key, edit its `age:` in `.sops.yaml`, then re-key the file with a key it lists. |
| Key is in the rule but not among the file's recipients (`not-recipient`) | The file was sealed before the rule changed. Re-key it once with the recovery key, as the line says. |
| The rule is not `path_regex` and `age:` with the seat key then the recovery key, or `.sops.yaml` uses anchors, aliases or merge keys (`untrusted-rule`) | Someone edited `.sops.yaml` by hand. Fix it, commit, and run again. |
| sops sealed the file to other keys (`wrong-recipients`) | Nothing was written. Check the sops version and any sops configuration in the environment. |
| No pin for a seat, or the pin directory is not private (`no-pin`) | Pass `--pub` with the machine's public key on the first grant, `--recovery` (or `init --pins`) for the recovery pin; `chmod 700` the directory. |
| A pin disagrees with `.sops.yaml` or `recovery.pub` (`key-changed`) | The store was edited, or the machine has a new key. Restore the store, or remove the seat's pin and grant with `--pub` after checking the key on the machine. |
| A name is in both `<seat>.yaml` and `<seat>.grants.yaml` (`duplicate-name`) | Remove it from `<seat>.yaml` with sops, or stop granting it. |
| A seat file is a symlink or not a regular file (`not-regular`) | Remove it and run again. |
| A grants file's names are not what this node's ledger granted (`grants-changed`) | The store was edited, or the ledger is lost. Remove the file from the store, commit, and grant again: an absent file is written again from the ledger. |
| `--key` is the recovery key (`recovery-key`) | The recovery key is for recovery only; use the seat's own key. |
| `set: empty or multi-line value on stdin; the value must be one non-empty line` | Pipe one non-empty line without a NUL byte; a terminal is refused. |
| `nova-secrets set: value is over 65536 bytes; a secret is a key or token` (exit 2) | Store a key or token, not a file. Nothing was written. |
| Unknown selected secret | Check `names`, then fix `--only` or `set` it. |
| `<NAME> is empty (null or "") in <seat>'s files` | `exec` never hands a command an empty value, exit 125. `set` it, as the line says. |
| `"bad-key" is not an environment variable name, or its value holds a NUL` | `exec` refuses the whole file, exit 125; `names` lists the other names and counts it in `refused=`. Remove the key with sops. |
| sops or git failed | The output is withheld because it can hold a value. Run the `run:` command shown in the error; after a git failure inspect `git status`, since files may be written. Two sops exits are named: 51, the file changed since it was sealed (restore it with `git checkout -- <file>`, or seal it again); 128, no key given opens it. |
| `git not found on PATH` or `sops not found at <path>` (`git`, `no-sops`) | The install line for this OS is in the message; `--sops <path>` names a sops elsewhere. |
| `key file K does not exist` (`no-key`) | Make the seat's key once, on its machine: `age-keygen -o K && chmod 600 K`. |

## Guarantees and limits

Guarantees:

- No argument, progress line or store file holds a plaintext value; sops and git output is withheld from errors.
- `set` sends the value to sops on stdin. In `exec`, sops writes the decrypted file to a pipe the tool reads.
- `grant` holds values in memory only: they pass from one sops decrypt to one sops encrypt on its stdin.
- No verb needs the recovery private key to fill a machine's seat.
- A grants file is sealed to the target's pinned key and the pinned recovery key and nothing else, whatever
  `.sops.yaml` or `recovery.pub` in the store say; the granting key is not among them.
- `set` seals a seat file to the public half of `--key` and the recovery pin, with sops told to ignore
  `.sops.yaml`, and only when the seat's rule says the same; after sops it checks the file is sealed to exactly
  those keys.
- `grant` fills only names the control node was asked for; a name planted in a grants file refuses the grant.
- No verb follows a symlinked seat file.

Limits:

- The command can read, print, save or send its values, and so can its children.
- The control node's seat holds every fleet value, so its key opens them all in that seat; keep it on that machine.
- Anyone with push access to the store can write any file in it: a pushed `control.grants.yaml` holding a fleet name
  stops the control node's `exec` (`duplicate-name`) and its grants until it is removed, and a re-sealed grants file
  feeds a machine the pusher's value until the next grant rewrites it. Push access is the trust boundary.
- Recipients are public keys, so anyone with push access can write a valid file for any seat: a pusher can replace
  the value a seat reads from its own file, and a grants file until the next grant rewrites it. The store's history
  is the record; review pushes to it like code, and keep push access to the people who hold values.
- A pin is only as good as the key read for it: read the machine's public key from the machine, over SSH whose host
  key you checked.
- `.sops.yaml` and `recovery.pub` in the store are what `exec` and the refusal lines read; review changes to
  them like code, since a wrong rule stops a seat until it is fixed.
- A value is at most 65536 bytes; `set` refuses a longer one and writes nothing.
