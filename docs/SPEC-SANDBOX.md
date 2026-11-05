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
| `--gpu` | macOS only: open the `AGXDeviceUserClient` and `IOSurfaceRootUserClient` IOKit clients and the `MTLCompilerService`, CoreMedia `videoencoder` and `videodecoder` services, by name. |
| `--unix` | Linux only: leave AF_UNIX sockets open. This opens the same-uid escape below; use only when the command needs a local socket. |
| `--keep-fds` | do not close inherited descriptors above stderr before exec. |

Everything after `--` is the command and its arguments, verbatim; `argv[0]` is exactly what the caller gave, so a
multi-call binary (`busybox`), a virtualenv `python` or a `cc`→`clang` shim sees the name it was invoked as. Only the
exec target is resolved to a path. Flags may come in any order before `--`.

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

`--unix` is Linux-only (the macOS profile denies a unix socket outside the write set either way). Without it, a command
cannot create an AF_UNIX socket at all; that is the only way to fence the escape below, because neither the kernel nor
this tool can path-check a unix `connect`. On a kernel with no seccomp filter mode the fence cannot be built, and the
wall refuses to start unless `--unix` is given, so it is never silently weaker than it says. `--unix` still leaves
`ptrace` and `io_uring` refused.

## What passes through unchanged

The tool does not rewrite the command's surroundings. The caller decides them:

- **The environment** passes verbatim. A command that reads `HOME`, `TMPDIR`, `XDG_*` or a cache variable needs that
  variable pointed inside a `--write`, or it gets a denial when it touches the path. A private temp directory is just
  `TMPDIR=<dir inside a --write>`. Variables that address something outside the wall, such as `SSH_AUTH_SOCK`, are
  the caller's to unset (`env -u`).
- **The working directory** is the caller's. On macOS a working directory outside every grant makes `getcwd(3)` fail
  inside the wall, so start the tool from a directory it grants (`cd <dir> && nova-sandbox ...`).
- **Stdin, stdout and stderr** pass through. Descriptors above those are a hole in the wall — they are already open, so
  the kernels do not re-check them, and an inherited socket or file outside the grants would stay usable. They are
  closed (set close-on-exec) before the exec, unless `--keep-fds` keeps them.

## System roots

A command needs the OS to run. These are granted read and execute without being named:

- **Linux:** `/usr`, `/bin`, `/sbin`, `/lib`, `/lib64`, `/etc`, `/opt`, `/dev`, `/proc`, `/run/systemd/resolve`, and
  the directory `/etc/resolv.conf` resolves to when it is elsewhere. A root that does not exist is skipped. `/dev/null`
  and `/dev/tty` are writable, and so is `/dev/shm` (RAM only, same-uid — no wider than a `--write`), which Python
  multiprocessing and other tools need.
- **macOS:** `/System`, `/usr`, `/bin`, `/sbin`, `/opt/homebrew`, `/opt/local`, `/Library`, `/private/etc`,
  `/private/var/select`, `/private/var/db/timezone` (without it every program runs in UTC), `/dev`; the symlinks
  `/etc`, `/tmp`, `/var` and the root directory itself as literals (the link, not its target); the `xcode-select` link
  and, when it lies outside those roots, the developer directory it points at, because the `/usr/bin` compiler and
  `git` shims read it. `/opt` is granted only at `/opt/homebrew` and `/opt/local`, not whole, because `/opt/*` is
  user-writable. Every ancestor of a granted path gets stat only (no listing), so `stat` and `mkdir` by absolute path
  reach it. `/dev/null` and `/dev/tty` are also writable.

## What the wall guarantees

On both systems, for the command and every process it starts:

- A file or directory outside the system roots, the `--read`s and the `--write`s cannot be opened, listed, executed,
  created, renamed into, linked or deleted.
- Outside the `--write`s nothing can be modified, except `/dev/null`, `/dev/tty` and (Linux) `/dev/shm`.
- No AF_UNIX socket can be created, so a same-uid socket cannot be reached by path — the escape that would otherwise
  let the command read `/proc/net/unix`, dial any local socket, and have a service outside the wall act for it. On
  Linux this is a seccomp filter and is lifted by `--unix`; on macOS it is the profile's network rule and holds a unix
  socket reachable only inside a `--write`.
- Inherited descriptors above stderr are closed before exec unless `--keep-fds`.
- The wall cannot be lifted from inside. Landlock and the seccomp filter are inherited across `fork` and `execve` and
  `no_new_privs` stops a setuid binary from escaping them; a `sandbox-exec` profile is inherited the same way.
- Under `--net-deny`, a TCP connect to any address and port, and a TCP bind, are refused, except a connect to a
  `--net-allow` port.
- No tool process stays behind: the command's exit status is its own.

Linux only:

- The seccomp filter also refuses `ptrace` (attaching to another process) and `io_uring` (whose submission queue runs
  operations the kernel does not re-check through seccomp), at every argument and even under `--unix`.
- Signals: Landlock ABI 6 scopes signals and abstract unix sockets to the wall (the command cannot signal a process
  created outside it or connect to an abstract socket). **Below ABI 6 a signal can reach a process outside the wall**,
  and abstract sockets are not fenced (AF_UNIX creation is refused by seccomp, which covers abstract sockets too,
  unless `--unix`).
- Every filesystem right the running ABI defines is handled, so nothing the kernel can check is left unchecked: refer
  (cross-directory rename and link) from ABI 2, truncate from ABI 3, device ioctl from ABI 5.

macOS only:

- The default is deny. Network is IP-only outbound plus the DNS resolver socket. Inbound and bind are denied with or
  without `--net-deny`.
- Mach service lookup is narrowed to four names (directory services, TLS trust, logging, certificate evaluation);
  the clipboard and Apple Events are denied. IOKit is closed unless `--gpu` opens the two Metal client classes.

## What the wall does not guarantee

- **Network without `--net-deny`.** Nothing is promised. On Linux it is open; on macOS outbound IP and DNS are open.
- **UDP on Linux.** Landlock has no UDP rights at any ABI. `--net-deny` closes TCP only.
- **Which host a `--net-allow` reaches on Linux.** The rule is a port: a connect to that port on any address passes.
- **Unix sockets under `--unix` (Linux).** With `--unix` the AF_UNIX fence is off and Landlock does not path-check a
  unix `connect`, so the same-uid escape is open again. Keeping the socket's directory outside the grants does not
  help, because the connect is not a filesystem open.
- **Signals below Landlock ABI 6 (Linux).** They can reach processes outside the wall; see above.
- **Reads under `/proc`.** It is a system root on Linux; `/proc/net/unix` lists socket paths, which is why the AF_UNIX
  fence, not path hiding, is what closes the escape. What `/proc` exposes about other processes is the kernel's own
  ptrace policy.
- **Anything a `--write` can be used for.** A `--write` is full read and write beneath it: a socket the command itself
  creates there, a file it hands to another of its own processes, a FIFO. The wall bounds where, not what.
- **Inherited descriptors under `--keep-fds`.** Then an already-open fd outside the grants stays usable.
- **Resources.** No limit on CPU, memory, processes or disk; no PID, user or mount isolation. Running as root is not
  refused and the wall still applies, but root has many other ways out.
- **macOS future.** Apple marks `sandbox-exec` deprecated. The wall is the profile it applies; `check` says whether it
  applies here.

## check

`check` says which backend this host has and whether it works. It applies nothing to a command.

```
CHECK OK backend=landlock abi=6 net=enforceable note=landlock abi 6: net is TCP bind/connect only (abi 4+), udp is unrestricted; signals are confined to the wall only at abi 6+; seccomp available: the unix-socket fence is on by default
CHECK OK backend=sandbox-exec abi=- net=enforceable note=sandbox-exec is deprecated by Apple; the wall is the profile it applies (/usr/bin/sandbox-exec)
CHECK FAILED backend=landlock abi=- reason=no landlock in this kernel: below 5.13, not compiled in, or not in the boot-time lsm= list
```

`net=unavailable` means `--net-deny` will be refused here (Linux below ABI 4, kernel 6.7). The Linux note also says
whether seccomp is available: without it the default wall refuses to start (the AF_UNIX fence cannot be built), and
`--unix` is required. On macOS `check` applies a trivial profile, so it also fails inside a sandbox that forbids
nesting. Exit 0 on `CHECK OK`, 1 on `CHECK FAILED`.

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
| `unix_deny` | `--write in`, connect to a unix socket outside the grants | deny |
| `unix_allow` | `--write in --unix`, the same connect (Linux only) | allow |
| `net_control` | `--write in`, connect to a TCP listener the probe owns | allow |
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

The connect steps run this binary's `dial` verb inside the wall (exit 0 if a connect succeeds within three seconds: a
TCP address, or a unix socket when the argument is an absolute path). It is internal: no shell or tool present on every
host can open a connection. Its directory is granted with `--read` for those steps. `unix_allow` runs only on Linux,
where `--unix` is meaningful.

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
| `no_seccomp` | Linux with no seccomp filter mode and no `--unix`: the AF_UNIX fence cannot be built |
| `net_unenforceable` | `--net-deny` on a Landlock ABI below 4 |
| `sandbox_failed` | the kernel refused a rule, the restriction, the seccomp filter, or the exec |

| exit | meaning |
|---|---|
| the command's | the wall stood and the command ran; its status is the tool's |
| 125 | the wall refused to start (`SANDBOX REFUSED` on stderr) |
| 126 | the only problem is a command that is not an executable file (shell convention) |
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
parameters; the seccomp program's jump arithmetic and verdicts, interpreted against a synthetic `seccomp_data`
(Linux); exit codes through `run`, 125/126/127.

Functional tests (build tag `functional`) build the binary, wall real children and probe them. They run on Linux with
Landlock: `check` and `probe` (including `unix_deny`/`unix_allow`), a write inside and outside, a read outside and
named, exit status pass-through, the 125/126/127 refusals, environment and working directory pass-through, `argv[0]`
verbatim through a symlink, the AF_UNIX fence and `--unix`, the `systemd-run --user` escape (skipped where systemd
`--user` is absent), `--keep-fds`, and `--net-deny` with `--net-allow`. The macOS wall is covered at unit level by the
generated profile text only.
