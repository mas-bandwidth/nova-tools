# nova-sandbox

nova-sandbox runs one command inside a wall the kernel enforces. You name the directories the command may read and
write; everything else on disk is denied to it. macOS uses a `sandbox-exec` profile, Linux uses Landlock.

```
nova-sandbox --read <dir>... --write <dir>... [--net-deny] [--cwd <dir>] -- <cmd> <args...>
nova-sandbox check
nova-sandbox probe
```

## The wall

| Flag | |
|---|---|
| `--read <dir>` | readable, recursively, and executable. Repeatable. |
| `--read-noexec <dir>` | readable and not executable: a module cache, a data tree. Repeatable. |
| `--write <dir>` | readable and writable. Repeatable; at least one is required. The first is the default working and temp directory. |
| `--cwd <dir>` | the working directory; must be inside a `--write`. |
| `--tmp <dir>` | `TMPDIR` for the command; must be inside a `--write`. Default `<first --write>/.nova-sandbox-tmp`, the one directory the tool makes. |
| `--net-deny` | the network is closed, or the wall refuses to start (Linux needs Landlock ABI 4, kernel 6.7). Without it nothing is promised about the network. |
| `--net-allow host:port` | under `--net-deny`, open one loopback port back up. Repeatable. |
| `--gpu metal` | macOS: open the named Metal clients and services, nothing wider. |

Beyond the system roots a command needs to run (`/usr`, `/bin`, `/lib`, `/etc` and the like; on macOS also Homebrew,
the Xcode developer directory and the command's own directory), only what you name is reachable. Paths must be absolute
and exist; none is created. `HOME` must be set to a directory inside a `--write`, because tools derive paths from it and
the real `HOME` is denied. A command that lives in a home directory is refused unless you `--read` that directory
yourself, so the wall cannot hand over `~/.ssh` by accident. `SSH_AUTH_SOCK`, `SSH_AGENT_*`, `GPG_AGENT_INFO` and
`*_AGENT_PID/INFO/SOCK` are removed from the command's environment; `TMPDIR`, `TMP`, `TEMP` and `TMPPREFIX` point at the
wall's temp directory.

The tool applies the wall and then `exec`s the command in place, so nothing of the tool is left running and the exit
status is the command's. `125`: the wall refused to start (`SANDBOX REFUSED reason=...` lines on stderr say why, one per
problem). `127`: the command is on no `PATH` entry.

## check and probe

`check` prints the backend and whether it works here, exit 0 or 1.

```
CHECK OK backend=landlock abi=6 net=enforceable note=...
```

`probe` proves the wall by running it, through this same binary, in a scratch directory: a write outside is refused, a
write inside is allowed, a read outside is refused, and a connect under `--net-deny` is refused (an unwalled write and
a walled connect without `--net-deny` are the controls). Exit 0 if every step went as expected.

## Limits

Landlock restricts TCP only; UDP is open at every ABI. A Landlock wall cannot take execute back from a parent that
grants it, so a `--read-noexec` under any executable root is refused.

The wall's design is in `darwin.sb.tmpl` (the profile, with its measured reasons) and `rules.go` (the Landlock ruleset).
