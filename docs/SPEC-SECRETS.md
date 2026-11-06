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
`recovery.pub`, and one commit. `S` must be missing (its parent must exist) or empty. A directory with files in it
is refused, so a project's README is never overwritten. `S` inside another git work tree is refused too: make it a
repository of its own first. It refuses a store that exists.

```
nova-secrets init --store S --recovery <recovery public key>
SECRETS INIT OK store=S next: nova-secrets seat add <seat> <age-pubkey> --store S
```

**2. seat add** adds the seat's rule to `.sops.yaml`: the seat's key and the recovery key may open `<seat>.yaml`.
It commits `.sops.yaml`, and only that file.

```
nova-secrets seat add web <seat public key> --store S
SECRETS SEAT ADD OK seat=web committed; next: the seat's own key runs: nova-secrets set NAME --as web --store S --key <file>
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
needed, and no value is read. It lists the top-level keys and skips the `sops:` block. A key that is not an
environment variable name, or whose value is not a single value, is never printed: it is counted as `refused=<n>`.

```
nova-secrets names --store S --as web
DB_PASSWORD
SECRETS NAMES OK as=web names=1 refused=0
```

The names are the payload on stdout, one per line. The OK line goes to stderr.

## The model

```
<store>/.sops.yaml     creation rules: one per seat, path_regex ^<seat>\.yaml$, age: <seat key>,<recovery key>
<store>/recovery.pub   the recovery public key every rule also names
<store>/<seat>.yaml    the seat's secrets, sops-encrypted, one NAME: value per line
<store>/<seat>.grants.yaml  values granted to the seat, sealed to its pin and the recovery pin only
<pins>/recovery.pub    on the control node, outside the store: the recovery key grant trusts
<pins>/<seat>.pub      on the control node: the seat's key, read from the machine when it joined
<pins>/<seat>.ledger   on the control node: HMACs of what was granted, so a grant that changes nothing writes nothing
<key file>             the seat's private age key, mode 0600, owned by the user
                       (Windows: owned by the user, access list only the user, SYSTEM and Administrators)
```

A seat opens only its own files with its own key. Nothing in the store is trusted to say who a grant reaches:
the pins are. The key is a file given by `--key`. No identity from the caller's
environment or home directory reaches sops.

`--require NAME` is kept so existing units keep working; use `--only`.

`--store` is required for every verb, `--key` for `set`, `grant` and `exec`, and `--pins` for `set` and `grant`, as
flags or as `NOVA_SECRETS_STORE`, `NOVA_SECRETS_KEY` and `NOVA_SECRETS_PINS`. There is no default and the current directory is never assumed. `--key` is made absolute before
its mode and owner are checked and before sops sees it. `--as` defaults to `$NOVA_SECRETS_SEAT`, else the short
hostname. `--sops` defaults to `sops` on PATH.

`<verb> -h` and `help <verb>` print that verb's paragraph of the help and exit 0.

## The verbs

```
nova-secrets init --store <dir> --recovery <age-public-key> [--pins <dir>]
nova-secrets seat add <seat> <age-public-key> --store <dir>
nova-secrets seat list --store <dir>
nova-secrets set NAME --store <dir> --key <file> --pins <dir> [--as <seat>] [--recovery <age1>] [--sops <path>]
nova-secrets grant NAME[,NAME] --to <seat> --pins <dir> --store <dir> --key <file> [--as <seat>] [--pub <age1>] [--recovery <age1>]
nova-secrets names --store <dir> [--as <seat>]
nova-secrets exec --only NAME[,NAME] --store <dir> --key <file> [--as <seat>] [--sops <path>] -- cmd [args...]
nova-secrets help [verb]
```

**exec** decrypts `<seat>.yaml`, and `<seat>.grants.yaml` when it exists, and becomes `cmd` (exec(2); on Windows it runs `cmd` and exits with its status).
The child's environment is the inherited one with every name the seat holds removed, so a secret not asked for cannot
arrive another way, and every sops identity variable and `NOVA_SECRETS_KEY`, `NOVA_SECRETS_STORE` and `NOVA_SECRETS_SEAT` removed (the
child has no business knowing where the key is), plus the `--only` names and their values. A name the
seat does not hold is refused, by name, and the command never runs. No core file is written after the decrypt.

**set** creates `<seat>.yaml` when it is new. It seals the file with `sops --config /dev/null -e --age <the public
half of --key>,<the recovery pin>`, so nothing in the store chooses a recipient, and only when the seat's rule in
`.sops.yaml` names exactly those two keys (`key-changed` or `untrusted-rule` otherwise, and nothing is written), so
that `exec` on any copy of the store agrees with it. The value is one non-empty line of at most 65536 bytes; a longer one is refused as soon as it is seen to be over, never cut. An empty stdin, such as
`/dev/null`, is refused as an empty value.

**grant** runs on the control node. It decrypts the granting seat's files (`--as`), takes the named values, and
writes `<to>.grants.yaml` whole, keeping the names this node's ledger says it granted (a file whose names differ
is refused as `grants-changed`), with `sops --config /dev/null -e --age
<pin>,<recovery pin>`, so `.sops.yaml` chooses no recipient. It refuses before sops runs when a pin is missing or not
private (`no-pin`), or when the seat's rule does not name the pin first or the store's `recovery.pub` is not the
recovery pin (`key-changed`), and before writing when sops sealed the file to any other keys or key sources
(`wrong-recipients`). `--pub` and `--recovery` write a pin that is absent and refuse one that differs. The granting
key is never a recipient of what it writes. `set` refuses a name the seat holds in its grants file, and `exec` and
`names` refuse a seat whose two files share a name (`duplicate-name`). Every seat file is read only when it is a
regular file inside the store (`not-regular`).

**seat add** parses `.sops.yaml` and writes it back with the new rule. sops uses the first rule that matches a file,
so the rule goes before the first existing rule that would match the seat's file (a catch-all). Every existing rule
is kept. A file that does not parse is refused and left alone. The write is a temporary file and a rename. A seat or a
key that already has a rule is refused. Comments are kept. An empty `creation_rules: []` becomes a block list, written
with a 2-space indent so it reads and diffs; a list that already has rules keeps its style, and blank lines between
rules are dropped.

**seat list** is read-only. It prints every seat that a rule in `.sops.yaml` names or that has a file in the store,
one `<seat> file=yes|no rule=yes|no` per line (an orphan file shows `rule=no`), and `SECRETS SEAT LIST OK seats=<n>` on stderr. There is no `seat remove` and no
`unset`: a seat or a name is removed with git and sops.

```
nova-secrets seat list --store S
web file=yes rule=yes
```

## Refusals say what to run

A malformed invocation is one line on stderr and exit 2, for `exec` too: `nova-secrets <verb>: <what is wrong>; run: nova-secrets <verb> -h`.
It names every missing flag at once, for example `nova-secrets exec: missing --only NAME[,NAME], --store <dir> (or NOVA_SECRETS_STORE), --key <file> (or NOVA_SECRETS_KEY), a command after -- (no default; the current directory is never assumed); run: nova-secrets exec -h`.
An unknown verb is named and listed, and points at `nova-secrets help`.

A runtime refusal is one line on stderr: `SECRETS <VERB> REFUSED reason=<reason>: <why>; run: <next command>`. The reason is one word: `no-store`, `no-seat`, `no-key`, `not-recipient`, `no-name`, `no-sops`, `no-command`, `sops`, `git` or `other`, and for `grant` and the files it reads `no-pin`, `key-changed`, `untrusted-rule`, `wrong-recipients`, `grants-changed`, `duplicate-name` or `not-regular`. Neither it nor the rest of the line holds a value, so a caller that hides a command's output (an Ansible task with `no_log`) can show the line when exec exits 125 or 2: the command never ran, and the line is all the tool printed. These are the common ones.

| refusal | next command |
| --- | --- |
| `store S exists` | `seat add` |
| `key <pub> is not the key in .sops.yaml for <s> (<rulepub>)` | edit the seat's `age:` in `.sops.yaml`, then `cd S && SOPS_AGE_KEY_FILE=<recovery.key> sops updatekeys -y <s>.yaml` |
| `key <pub> is the key in .sops.yaml for <s> but not among <s>.yaml's recipients` | the same `updatekeys` command |
| `sops not found at <path>; install sops 3.x` | install sops, or pass `--sops <path>` |
| `unknown secret name(s) X: ... holds no such name` | `nova-secrets names --store S --as <s>` |
| `S is not a git repository` | `git -C S init && git -C S add -A && git -C S commit -m store` |
| `no <s>.yaml in S: wrong --as? seats here: a,b` | the right `--as`, or `nova-secrets set NAME --as <s> ...` |
| `no rule for <s>.yaml in S/.sops.yaml` | `nova-secrets seat add <s> <public key> --store S` |
| `S/recovery.pub is missing` | `age-keygen -y <recovery.key> > S/recovery.pub`, then commit |
| `S/.sops.yaml is missing` | create it with the line `creation_rules: []` |
| `sops -d failed (exit n); its output is withheld; ran: ...` | rerun the printed command |

The key check compares the public half of `--key` (`age-keygen -y`, which must be on PATH) with the seat's rule and with
the file's recipients, which sops keeps in the clear. No value is read. When sops itself fails, the line carries the
command that ran: a scratch `HOME`, and for a decrypt `--output /dev/null`, so pasting it prints no value. Its output
is never shown.

## The guarantee

A secret value never appears in argv, in a log line, in an error, or in a file in the clear, so never in `ps`. A value
travels stdin to sops, sops to the store file (encrypted), and the store file through sops to the child's environment
only. sops and git output is never shown, because it can carry a value. A failure names the program and its exit
status. Progress lines carry names and counts, never values.

## Exit codes

| code | meaning |
| --- | --- |
| 0 | `init`, `seat add`, `set`, `grant`, `names` or help done |
| the command's own | `exec` ran the command; its status is exec's |
| 2 | refused: a malformed invocation (bad or missing flags, unknown verb, bad name), or a runtime refusal outside `exec` (key file not private, sops or git failed) |
| 125 | `exec` refused at run time and the command never ran: an unknown secret name, a decrypt failure, a command not found. The units rely on it. |
