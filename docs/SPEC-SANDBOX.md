# nova-sandbox — specification

`nova-sandbox` runs one command inside a wall the kernel enforces. The caller names, on the command line, the
directories the command may read and the directories it may write, and whether it may use the network. Everything
else on disk is denied to it. The tool knows nothing about what the command is or why it runs: it turns the flags
into kernel rules, applies them, and `exec`s the command in place.

```
nova-sandbox [--read <dir>]... [--write <dir>]... [--net-deny [--net-allow <host:port>]...] [--gpu] -- <cmd> <args...>
nova-sandbox check
nova-sandbox probe
nova-sandbox help
```

| OS | backend | how |
|---|---|---|
| Linux | Landlock (kernel 5.13+, ABI 1-6) | the tool restricts its own thread, sets `no_new_privs`, and `execve`s the command |
| macOS | `sandbox-exec` | a profile generated for this run is passed inline with `-p`; `sandbox-exec` is `exec`ed in place and execs the command |
| other | none | every run is refused: the tool never runs a command it cannot contain |

This spec is normative. If the code and this document disagree, one of them has a bug, and the tests decide which.

## The flags

| flag | grant |
|---|---|
| `--read <dir>` | read, list and execute beneath `<dir>`. Repeatable. |
| `--write <dir>` | everything `--read` gives, plus create, write, truncate, rename, link and delete beneath `<dir>`. Repeatable. |
| `--net-deny` | TCP connect and bind are closed. If the kernel cannot enforce that, the wall refuses to start. |
| `--net-allow <host:port>` | under `--net-deny`, reopen TCP connect to one loopback port. `host` must be `localhost` or a loopback address. Repeatable. |
| `--gpu` | macOS only: open the Metal device clients and the Metal compiler and media services, by name. |

Everything after `--` is the command and its arguments, verbatim. Flags may come in any order before `--`.

Every `--read` and `--write` must be an absolute path to an existing directory. Nothing is created. Each is resolved
through symlinks once, before the wall is built, and the resolved path is what the kernel sees. Neither flag is
required: a run with no `--write` is a read-only run.

The command is looked up on `PATH` outside the wall and resolved to an absolute, symlink-free file. It must lie under a
system root (below), a `--read` or a `--write`; otherwise the run is refused rather than handing the command's whole
directory to the wall. A command at `~/bin/tool` runs only with `--read ~/bin` named by the caller.

`--net-allow` is refused without `--net-deny`, and a non-loopback host is refused: Landlock filters by port only, so a
remote host could not be honoured, and the macOS profile only accepts `localhost` as a host.

`--gpu` is refused on Linux. A GPU there is a device directory; grant it the same way as any other, for example
`--write /dev/dri`.

## What passes through unchanged

The tool does not rewrite the command's surroundings. The caller decides them:

- **The environment** passes verbatim. A command that reads `HOME`, `TMPDIR`, `XDG_*` or a cache variable needs that
  variable pointed inside a `--write`, or it gets a denial when it touches the path. A private temp directory is just
  `TMPDIR=<dir inside a --write>`. Variables that address something outside the wall, such as `SSH_AUTH_SOCK`, are
  the caller's to unset (`env -u`).
- **The working directory** is the caller's. On macOS a working directory outside every grant makes `getcwd(3)` fail
  inside the wall, so start the tool from a directory it grants (`cd <dir> && nova-sandbox ...`).
- **Open file descriptors** pass through. A descriptor opened before the wall (stdin, stdout, stderr, or anything the
  caller leaves without close-on-exec) stays usable: the kernels check access when a path is opened, not when an open
  descriptor is used.

## System roots

A command needs the OS to run. These are granted read and execute without being named:

- **Linux:** `/usr`, `/bin`, `/sbin`, `/lib`, `/lib64`, `/etc`, `/opt`, `/dev`, `/proc`, `/run/systemd/resolve`, and
  the directory `/etc/resolv.conf` resolves to when it is elsewhere. A root that does not exist is skipped. `/dev/null`
  and `/dev/tty` are also writable.
- **macOS:** `/System`, `/usr`, `/bin`, `/sbin`, `/opt`, `/Library`, `/private/etc`, `/private/var/select`, `/dev`; the
  symlinks `/etc`, `/tmp`, `/var` and the root directory itself as literals (the link, not its target); the
  `xcode-select` link and, when it lies outside those roots, the developer directory it points at, because the
  `/usr/bin` compiler and `git` shims read it. Every ancestor of a granted path gets stat only (no listing), so `stat`
  and `mkdir` by absolute path reach it. `/dev/null` and `/dev/tty` are also writable.

## What the wall guarantees

On both systems, for the command and every process it starts:

- A file or directory outside the system roots, the `--read`s and the `--write`s cannot be opened, listed, executed,
  created, renamed into, linked or deleted.
- Outside the `--write`s nothing can be modified, except `/dev/null` and `/dev/tty`.
- The wall cannot be lifted from inside. Landlock is inherited across `fork` and `execve` and `no_new_privs` stops a
  setuid binary from escaping it; a `sandbox-exec` profile is inherited the same way.
- Under `--net-deny`, a TCP connect to any address and port, and a TCP bind, are refused, except a connect to a
  `--net-allow` port.
- No tool process stays behind: the command's exit status and signals are its own.

Linux only:

- Landlock ABI 6 also scopes abstract unix sockets and signals to the wall: the command cannot connect to an abstract
  socket or signal a process created outside it.
- Every filesystem right the running ABI defines is handled, so nothing the kernel can check is left unchecked: refer
  (cross-directory rename and link) from ABI 2, truncate from ABI 3, device ioctl from ABI 5.

macOS only:

- The default is deny. Network is IP-only outbound plus the DNS resolver socket; a unix socket is reachable only
  inside a `--write`. Inbound and bind are denied with or without `--net-deny`.
- Mach service lookup is narrowed to four names (directory services, TLS trust, logging, certificate evaluation);
  the clipboard and Apple Events are denied. IOKit is closed unless `--gpu` opens the two Metal client classes.

## What the wall does not guarantee

- **Network without `--net-deny`.** Nothing is promised. On Linux it is open; on macOS outbound IP and DNS are open.
- **UDP on Linux.** Landlock has no UDP rights at any ABI. `--net-deny` closes TCP only.
- **Which host a `--net-allow` reaches on Linux.** The rule is a port: a connect to that port on any address passes.
- **Unix sockets by path on Linux.** Landlock does not check `connect` on a path socket. Unset the variables that name
  one, or keep the socket's directory outside the grants and its descriptor out of the command.
- **Abstract sockets and signals below Landlock ABI 6.**
- **Reads under `/proc`.** It is a system root on Linux; what it exposes about other processes is governed by the
  kernel's own ptrace checks.
- **Anything already open.** See the descriptors above.
- **Resources.** No limit on CPU, memory, processes or disk; no PID, user or mount isolation. Running as root is not
  refused and the wall still applies to the filesystem, but root has many other ways out.
- **macOS future.** Apple marks `sandbox-exec` deprecated. The wall is the profile it applies; `check` says whether it
  applies here.

## check

`check` says which backend this host has and whether it works. It applies nothing to a command.

```
CHECK OK backend=landlock abi=6 net=enforceable note=landlock abi 6: net is TCP bind/connect only (abi 4+); udp is unrestricted
CHECK OK backend=sandbox-exec abi=- net=enforceable note=sandbox-exec is deprecated by Apple; the wall is the profile it applies (/usr/bin/sandbox-exec)
CHECK FAILED backend=landlock abi=- reason=no landlock in this kernel: below 5.13, not compiled in, or not in the boot-time lsm= list
```

`net=unavailable` means `--net-deny` will be refused here (Linux below ABI 4, kernel 6.7). On macOS `check` applies a
trivial profile, so it also fails inside a sandbox that forbids nesting. Exit 0 on `CHECK OK`, 1 on `CHECK FAILED`.

## probe

`probe` proves the wall on this host by running it the way a caller does, through this same binary. It makes a scratch
directory holding `in` and `out` and a file `out/secret`, runs each step walled with `--write in` from `in`, and
removes the scratch directory at the end.

| step | walled with | expect |
|---|---|---|
| `write_outside_control` | nothing (unwalled) | allow |
| `write_inside` | `--write in` | allow |
| `write_outside` | `--write in` | deny |
| `read_outside` | `--write in` | deny |
| `read_named` | `--write in --read out` | allow |
| `net_control` | `--write in`, connect to a listener the probe owns | allow |
| `net_deny` | `--write in --net-deny`, the same connect | deny |

One line per step, then a verdict:

```
PROBE STEP name=write_outside expect=deny got=deny
PROBE STEP name=net_deny expect=deny got=deny
PROBE OK backend=landlock abi=6
```

A step that went the wrong way ends ` FAIL` and the verdict is `PROBE FAILED backend=<b> abi=<n> failed=<count>`.
Where `--net-deny` cannot be enforced, `net_deny` reports `got=wall_refused` with a note and does not fail. A setup
error is `PROBE FAILED reason=<error>`. Exit 0 on `PROBE OK`, 1 otherwise.

The connect steps run this binary's `dial` verb inside the wall (exit 0 if a TCP connect to `host:port` succeeds within
three seconds). It is internal: no shell or tool present on every host can open a TCP connection. Its directory is
granted with `--read` for those two steps.

## Refusals and exit codes

Before anything runs, every independent problem is reported on stderr, one line each:

```
SANDBOX REFUSED reason=<reason>: <text>
```

| reason | when |
|---|---|
| `bad_flag` | an unknown flag, or a flag with no value |
| `no_command` | no `--`, or nothing after it |
| `bad_read`, `bad_write` | a path that is relative, missing, not a directory, unresolvable, or holds a character the generated rules cannot carry (a control character; on macOS also `"` `\` `(` `)`); `bad_read` also for a command outside every grant |
| `bad_net` | a `--net-allow` that is not loopback `host:port`, or one without `--net-deny` |
| `bad_gpu` | `--gpu` on Linux |
| `not_found` | the command is on no `PATH` entry |
| `not_executable` | the command is not an executable file |
| `no_sandbox` | no backend on this host |
| `net_unenforceable` | `--net-deny` on a Landlock ABI below 4 |
| `sandbox_failed` | the kernel refused a rule, the restriction, or the exec |

| exit | meaning |
|---|---|
| the command's | the wall stood and the command ran; its status is the tool's |
| 125 | the wall refused to start (`SANDBOX REFUSED` on stderr) |
| 127 | the only problem is a command on no `PATH` entry |
| 0 / 1 | `check` and `probe`: yes / no; `help` is 0 |

On macOS a command killed by a signal shows as the shell would see it; a profile that fails to compile is
`sandbox-exec`'s own exit 65, which the generator's tests exist to prevent.

## Worked example

Build a Go module that lives in `/work/src`, with its caches and temp directory in `/work/scratch`, the toolchain in
`/usr/local/go` (under `/usr`, so already granted), no network, and the module cache only read:

```
mkdir -p /work/scratch/tmp /work/scratch/home
cd /work/src
HOME=/work/scratch/home TMPDIR=/work/scratch/tmp GOCACHE=/work/scratch/gocache \
  GOMODCACHE=/home/me/go/pkg/mod GOPROXY=off \
  nova-sandbox --write /work/src --write /work/scratch --read /home/me/go/pkg/mod --net-deny -- go build ./...
```

The build reads `/work/src` and the module cache, writes its binary into `/work/src` and its cache and temp files into
`/work/scratch`, and gets `permission denied` for anything else: `~/.ssh`, `~/.config`, the rest of `/home/me`.
A connect anywhere is refused. Its exit status is `go build`'s. The same line, with `go` replaced by something that
reaches outside, prints the kernel's denial from the command itself and the tool adds nothing.

To see that the wall works on the host before relying on it:

```
nova-sandbox check && nova-sandbox probe
```

## Tests

Unit tests use no real time, sockets or network, and run on every OS: flag parsing into the internal spec; the checks
and refusals of `build` (on scratch directories), for both `linux` and `darwin` path rules; the Landlock ruleset for
each ABI built from a spec; the macOS profile text built from a spec, including that caller paths enter only as
parameters; exit codes through `run`.

Functional tests (build tag `functional`) build the binary, wall real children and probe them. They run on Linux with
Landlock: `check` and `probe`, a write inside and outside, a read outside and named, exit status pass-through, the
125/127 refusals, environment and working directory pass-through, and `--net-deny` with `--net-allow`. The macOS wall
is covered at unit level by the generated profile text only.
