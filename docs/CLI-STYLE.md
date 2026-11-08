# Using the command line

Start with `<tool> help`. Use `<tool> help <verb>`, `<tool> <verb> -h` or
`<tool> <verb> --help` for one operation. Help prints to stdout, returns 0
and makes no changes. `help` itself takes no flags: on bus, sandbox, secrets
and reserve, `help -h` is refused with exit 2. Each guide shows setup and a
working example.

| Tool | Guide | Main operations |
|---|---|---|
| `nova-bus` | [Bus](../bus/README.md) | `send`, `hook`, `who`, `pending` |
| `nova-config` | [Config](../config/README.md) | `machine`, `service`, `history`, `schema`, `inventory` |
| `nova-sandbox` | [Sandbox](../sandbox/README.md) | a command after `--`, `show`, `check`, `probe` |
| `nova-secrets` | [Secrets](../secrets/README.md) | `init`, `seat add`, `set`, `names`, `exec` |
| `nova-reserve` | [Reserve](../reserve/README.md) | `run`, `hold`, `status`, `set` |
| `nova-cairn` | [Cairn](../cairn/README.md) | `open`, `append`, `index`, `receipt`, `memory`, `self-talk` |

## Arguments

Follow the order in the tool's examples. Bus and cairn take flags before
positional arguments. Config accepts a row name before its flags. Secrets
accepts `set NAME` or `seat add SEAT KEY` before flags.

Sandbox, secrets `exec` and reserve use `--` to separate their options from
the command and its arguments. Quote each argument for your shell. Bus accepts
`-` as its body to read stdin. Secrets `set` reads its value from stdin,
keeping it out of command arguments.

An unknown verb, unknown flag, missing required argument or malformed value
returns 2. The error names the problem and points to help. A flag belonging
to another operation is refused rather than ignored. Required inputs are
reported together where the command can identify them before reading a store.

## Output

The bus receiver process, called a **hook**, prints messages as tab-separated
lines. Backslashes, tabs and line breaks within fields are escaped.

Config prints records. `list` and `show` accept `--json` for typed row output;
`history` prints changes as text. `inventory` prints Ansible inventory JSON.

Secrets `exec` puts progress on stderr, leaving stdout for the command.
`init`, `set` and `seat add` print success lines on stdout. `names` prints
names on stdout and its summary on stderr.

Reserve prints acquisition on stderr for `run`, and on stdout for `hold`.
`status` shows capacity, holders and the number of free slots.

Cairn prints named result lines and counts. An entry's `receipt --text`
includes its saved words in a quoted field. Check findings use stderr;
read the final status as well as any earlier results.

Each tool has its own output format. Parse the one its guide describes. A
successful item can precede a later failure.

An OK line may be followed by one final line `next: <command>`: the command to
run next, built with the tool's command helper so it is shell-quoted, with real
spaces and no escaping. It is a real command with a real example value, such as
`next: nova-cairn append --store /tmp/c --session s1 --entry first`, so it pastes
and runs. It is never a `next=` field, and nothing follows it.

## Exit codes

| Code | Meaning |
|---|---|
| 0 | The operation succeeded. |
| 1 | The tool refused the operation, found a conflict, or a check found a problem. |
| 2 | The invocation was wrong or the operation could not run. |
| 125 | The sandbox refused to start, or secrets `exec` could not launch its command. |
| 126 | A command was found but could not be executed. |
| 127 | Sandbox or reserve could not find the command. Reserve names the PATH it searched. |
| Child status | A wrapper returns its command's status after launch. The same number can therefore have a different cause. |

Exit 2 covers a wrong invocation and any operation that could not run: the
store is unreachable, the schema is not loaded, a required binary is missing,
or a login failed. Exit 1 means the tool ran and said no: a refused row, a held
name, a policy. A caller that retries on 2 and reads 1 as an answer is then
correct.

Config uses 2 for connection and execution failures. Secrets operations other
than `exec` also use 2 for runtime refusals. Bus uses 1 for Redis-operation
failures. Sandbox `show`, `check` and `probe` use 0/1 for their result;
a macOS profile compilation failure can return the platform's own status.
Each guide lists the full distinctions.

Read stderr as well as the status. Missing input is different from a valid
empty result. An existing empty inbox, search with no matches or idempotent
retry can succeed; a required unreadable store or file cannot.

## Effects

Config writes accept `--dry-run`. Fleet plays support Ansible's `--check`.
These modes can still read inputs or contact a store or release server.
Sandbox `show` prepares and prints rules without starting the supplied command.

A successful bus send stores a message in Redis. A hook acknowledges and
deletes it after writing it to stdout. That does not establish whether a model
read it or did the work. A send to several recipients can fail after reaching
some of them; retrying can deliver duplicates.

A secrets **seat** names a decryption identity and its age key. `exec` and
`set` need a store and key through flags or named environment variables.
`seat add` and `names` need the store. The guide explains how to create them.

A **slot** is one place in a concurrent job limit. Reserve callers share the
capacity recorded in their reservation directory. A **cairn** is a saved
session record; its directory is explicit, and its publish policy records
intent without sending anything.

Defaults belong to each tool. Cairn prints its version with `version`. For
the other tools, record the release tag or source commit you installed when
you report a problem.

## Checks for contributors

The `contract` package (functional tier) builds the tools and checks root and verb help, bad
invocations, missing inputs, exit codes and selected output rules in temporary
directories. Tool tests cover the rest of their behavior. See
[TESTING.md](TESTING.md) for commands and prerequisites.
