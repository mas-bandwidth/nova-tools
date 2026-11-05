# nova-sandbox — specification

`nova-sandbox` runs one command inside a wall the kernel enforces. The caller names, on the command line, the
directories the command may read and the directories it may write, and whether it may use the network. Everything
else on disk is denied to it. The tool knows nothing about what the command is or why it runs: it turns the flags
into kernel rules, applies them, and `exec`s the command in place.

```
nova-sandbox [--read <dir>]... [--write <dir>]... [--net-deny [--net-allow <host:port>]...] [--gpu] [--unix] [--keep-fds] -- <cmd> <args...>
nova-sandbox show [the same flags] [-- <cmd> <args...>]
nova-sandbox check
nova-sandbox probe
nova-sandbox help [<verb>]
```

| OS | backend | how |
|---|---|---|
| Linux | Landlock (kernel 5.13+, ABI 1-6) and a seccomp filter | the tool restricts its own thread, sets `no_new_privs`, installs the filter, and `execve`s the command |
| macOS | `sandbox-exec` | a profile generated for this run is passed inline with `-p`; `sandbox-exec` is `exec`ed in place and execs the command |
| other | none | every run is refused: the tool never runs a command it cannot contain |

This spec is normative. If the code and this document disagree, one of them has a bug, and the tests decide which.

## The flags

| flag | grant |
|---|---|
| `--read <dir>` | read, list and execute beneath `<dir>`. Repeatable. |
| `--write <dir>` | everything `--read` gives, plus create, write, truncate, rename, link and delete beneath `<dir>`. Repeatable. |
| `--net-deny` | the network is closed. If the kernel cannot enforce that, the wall refuses to start. See [The network](#the-network). |
| `--net-allow <host:port>` | under `--net-deny`, reopen connect to one loopback port. `host` must be `localhost` or a loopback address. Repeatable. See [The network](#the-network). |
| `--gpu` | macOS only: open the `AGXDeviceUserClient` and `IOSurfaceRootUserClient` IOKit clients and the `MTLCompilerService`, CoreMedia `videoencoder` and `videodecoder` services, by name. |
| `--unix` | Linux only: leave AF_UNIX sockets open. This opens the same-uid escape below; use only when the command needs a local socket (the cost of the fence is listed under `--unix` below). |
| `--keep-fds` | do not close inherited descriptors above stderr before exec. |

Everything after `--` is the command and its arguments, verbatim; `argv[0]` is exactly what the caller gave, so a
multi-call binary (`busybox`), a virtualenv `python` or a `cc`→`clang` shim sees the name it was invoked as. On Linux
the file executed is the caller's spelling made absolute with its symlinks kept, so a `#!` script reached through a
symlink sees the link as `$0`. On macOS `sandbox-exec` looks `argv[0]` up on `PATH` itself, inside the wall. Flags may
come in any order before `--`.

Every `--read` and `--write` must be an absolute path to an existing directory. Nothing is created. Each is resolved
through symlinks once, before the wall is built, and the resolved path is what the kernel sees. Neither flag is
required: a run with no `--write` is a read-only run.

The command is looked up on `PATH` outside the wall and checked there: missing is exit 127, present but not executable
is 126. Its symlink-free target must lie under a system root (below), a `--read` or a `--write`; otherwise the run is
refused rather than handing the command's whole directory to the wall. A command at `~/bin/tool` runs only with
`--read ~/bin` named by the caller.

`--net-allow` is refused without `--net-deny`, and a non-loopback host is refused: Landlock filters by port only, so a
remote host could not be honoured, and the macOS profile only accepts `localhost` as a host.

A flag the run form or a verb does not take is refused, never ignored. `-h` or `--help` before `--` prints the usage and
exits 0; after `--` it belongs to the command.

`--gpu` is refused on Linux. A GPU there is a device directory; grant it the same way as any other, for example
`--write /dev/dri`.

`--unix` is Linux-only. Without it a seccomp filter refuses `socket(AF_UNIX)` and every `socketpair(AF_UNIX)` except `SOCK_STREAM`; that
is the only way to fence the escape below, because Landlock does not path-check a unix `connect`. A `SOCK_STREAM` pair
is allowed: it is connected to itself, `connect` and `sendto` elsewhere fail with `EISCONN`, and every event loop
(Python `asyncio`, Go, libuv) makes one. A `SOCK_DGRAM` pair can reconnect or `sendto` any datagram path, and a
`SOCK_SEQPACKET` pair was measured to `sendto` a datagram path as well, so both are refused.

The cost of the fence: nothing that calls `socket(AF_UNIX)` works inside it. That includes `/dev/log` and `syslog(3)`,
NSS lookups through a socket (nscd, sssd, systemd-userdb; plain `/etc/passwd` and DNS over UDP/TCP still work), D-Bus,
ssh-agent and gpg-agent, and any local daemon. `--unix` is the opt-out; with seccomp present it still installs the filter
without the AF_UNIX rules, so `ptrace` and `io_uring` stay refused. On a kernel with no seccomp filter mode the default
wall refuses to start (`no_seccomp`), so it is never silently weaker than it says; `--unix` is then the only way to run,
and the wall is Landlock alone: unix sockets, `ptrace` and `io_uring` are all open.

## The network

Without `--net-deny` nothing is promised about the network, on either OS.

**Linux, `--net-deny`.** Closed means closed. Two layers do it:

- Landlock (ABI 4+) refuses every TCP connect and bind.
- The seccomp filter refuses `socket(2)` for every family but `AF_UNIX` and `AF_NETLINK`. So no TCP, UDP, raw, ICMP,
  packet, SCTP, MPTCP, SMC, vsock or `AF_ALG` socket can be made. A `socketpair(2)` of any family but `AF_UNIX` is
  refused too.

DNS therefore works only from `/etc/hosts`. A name outside it fails to resolve.

`AF_NETLINK` stays open, but only as `NETLINK_ROUTE`. It is kernel-local and reaches no other host. It was measured:
glibc `getaddrinfo` of an `/etc/hosts` name works without netlink, with and without `AI_ADDRCONFIG`. But listing
interfaces needs `NETLINK_ROUTE`: `if_nameindex(3)`, `ip addr` and Go's `net.Interfaces` fail with `EPERM` without it.
Every other netlink protocol is refused. That includes `NETLINK_SOCK_DIAG`, which lists every process's TCP and UDP
connections (`ss -tn`), not only the command's own.

**Linux, `--net-deny --net-allow host:port`.** Landlock reopens TCP connect to each named port. The filter then allows
`AF_INET` and `AF_INET6`, but only as `SOCK_STREAM` with protocol 0 or `IPPROTO_TCP`, which is what Landlock checks.
UDP, raw, ICMP, SCTP and MPTCP stay closed, and so does every other family. DNS over UDP does not work: name resolution
must come from `/etc/hosts`, or from a resolver on an allowed TCP port. A `--net-allow` port is open on every address,
not only loopback: Landlock filters by port.

Two TCP paths go around Landlock's hooks, so the filter closes them under `--net-allow`:

- **TCP Fast Open.** `sendto`, `sendmsg` and `sendmmsg` with `MSG_FASTOPEN` connect inside the send, and Landlock's
  connect hook never sees it. They are refused (`EPERM`) when the flags argument carries `MSG_FASTOPEN`.
- **`listen` without `bind`.** The kernel picks a port on every address, and Landlock's bind hook never sees it. So
  `listen`, `accept` and `accept4` are refused on every socket. seccomp cannot tell a unix socket from a TCP one, so
  this covers unix sockets too.

The rule is: under `--net-allow` nothing can serve. A command that serves a unix socket runs with `--unix` and
`--net-deny` alone, with no `--net-allow`; there no TCP socket exists, so `listen` stays open. An explicit TCP
`bind` is refused by Landlock in both modes.

**Linux, no seccomp.** `--net-deny` refuses to start (`net_unenforceable`: "net-deny needs seccomp on this kernel").
Landlock alone fences TCP only, and the wall never stands weaker than it says. The same refusal comes below Landlock
ABI 4.

**macOS, `--net-deny`.** The profile grants no outbound IP and not the `mDNSResponder` socket. So no TCP or UDP leaves,
and DNS fails. Inbound and bind are denied with or without `--net-deny`.

**macOS, `--net-allow localhost:port`.** Outbound to `localhost` on that port is granted, for TCP and UDP both: the
profile's `remote ip` rule does not tell them apart. Nothing else is reopened. DNS stays closed.

**macOS, without `--net-deny`.** Outbound IP, of any protocol, and the DNS socket are open.

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
  and `/dev/tty` are writable, and so is `/dev/shm`, which POSIX shared memory (Python multiprocessing and others)
  needs. `/dev/shm` is shared with every process on the host: inside the wall the command can read, overwrite and
  delete what any same-uid process keeps there, and they can do the same to its files.
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
- Inherited descriptors above stderr are closed before exec unless `--keep-fds`.
- The wall cannot be lifted from inside. Landlock and the seccomp filter are inherited across `fork` and `execve` and
  `no_new_privs` stops a setuid binary from escaping them; a `sandbox-exec` profile is inherited the same way.
- Under `--net-deny`, the network is closed as [The network](#the-network) says: on Linux no socket of any family but
  `AF_UNIX` and `NETLINK_ROUTE` (TCP client sockets come back only for a `--net-allow` port, with no listen, accept or
  Fast Open); on macOS no outbound IP and no DNS.
- No tool process stays behind: the command's exit status is its own.

Linux only:

- Unless `--unix`, no unix socket that can be pointed at a path can be created (`socket(AF_UNIX)` and every non-stream pair
  are refused; see `--unix` for what is allowed and the cost), so a same-uid socket cannot be reached by path: the escape
  that would otherwise let the command read `/proc/net/unix`, dial any local socket, and have a service outside the
  wall act for it (`systemd-run --user`). x32 syscall numbers are killed, so they cannot bypass the filter.
- The seccomp filter also refuses `ptrace` (attaching to another process) and `io_uring` (whose submission queue runs
  operations the kernel does not re-check through seccomp), at every argument and even under `--unix`, whenever the
  kernel has seccomp.
- Signals: Landlock ABI 6 scopes signals and abstract unix sockets to the wall (the command cannot signal a process
  created outside it or connect to an abstract socket). **Below ABI 6 a signal can reach a process outside the wall**,
  and abstract sockets are not fenced (AF_UNIX creation is refused by seccomp, which covers abstract sockets too,
  unless `--unix`).
- Every filesystem right the running ABI defines is handled, so nothing the kernel can check is left unchecked: refer
  (cross-directory rename and link) from ABI 2, truncate from ABI 3, device ioctl from ABI 5.

macOS only:

- The default is deny. Without `--net-deny` the network is IP-only outbound plus the DNS resolver socket; see
  [The network](#the-network). Inbound and bind are denied with or without `--net-deny`.
- Unix sockets: the profile fences unix `connect`, not creation. A unix socket can be created, and connected only to a
  socket inside a `--write`, or, without `--net-deny`, to the `mDNSResponder` resolver socket.
- Mach service lookup is narrowed to four global names (directory services, TLS trust, logging, certificate
  evaluation); the clipboard and Apple Events are denied. IOKit is closed. `--gpu` adds the two Metal IOKit client
  classes and three XPC service names (`MTLCompilerService`, CoreMedia `videoencoder`, `videodecoder`), so the mach
  lookups are then those four plus three.

## What the wall does not guarantee

- **Network without `--net-deny`.** Nothing is promised. On Linux it is open; on macOS outbound IP and DNS are open.
- **Which host a `--net-allow` reaches on Linux.** The rule is a port: a connect to that port on any address passes.
- **UDP to a `--net-allow` port on macOS.** That port is open to `localhost` for UDP too.
- **Netlink (Linux).** `NETLINK_ROUTE` stays open under `--net-deny`; it is kernel-local. A command can read the interface
  list and addresses. The other netlink protocols, `sock_diag` among them, are closed. Without `--net-deny` all are
  open, so `ss -tn` can list other processes' connections.
- **Unix sockets under `--unix`, or without seccomp (Linux).** With `--unix` the AF_UNIX fence is off and Landlock does not path-check a
  unix `connect`, so the same-uid escape is open again. Keeping the socket's directory outside the grants does not
  help, because the connect is not a filesystem open.
- **Signals below Landlock ABI 6 (Linux).** They can reach processes outside the wall; see above.
- **Reads under `/proc`.** It is a system root on Linux; `/proc/net/unix` lists socket paths, which is why the AF_UNIX
  fence, not path hiding, is what closes the escape. What `/proc` exposes about other processes is the kernel's own
  ptrace policy.
- **Anything a `--write` can be used for.** A `--write` is full read and write beneath it: a socket the command itself
  creates there, a file it hands to another of its own processes, a FIFO. The wall bounds where, not what.
- **`/dev/shm` (Linux).** Shared with every process on the host; see the system roots.
- **`ptrace` and `io_uring` on a kernel without seccomp (Linux).** Only `--unix` runs there, with no filter at all.
- **Inherited descriptors under `--keep-fds`.** Then an already-open fd outside the grants stays usable.
- **Resources.** No limit on CPU, memory, processes or disk; no PID, user or mount isolation. Running as root is not
  refused and the wall still applies, but root has many other ways out.
- **macOS future.** Apple marks `sandbox-exec` deprecated. The wall is the profile it applies; `check` says whether it
  applies here.

## check

`check` says which backend this host has and whether it works. It applies nothing to a command.

```
CHECK OK backend=landlock abi=6 net=enforceable note=landlock abi 6; seccomp available; net-deny landlock_tcp+seccomp; signals scoped to the wall at abi 6+
CHECK OK backend=sandbox-exec abi=- net=enforceable note=sandbox-exec is deprecated by Apple; the wall is the profile it applies (/usr/bin/sandbox-exec)
CHECK FAILED backend=landlock abi=- reason=no landlock in this kernel: below 5.13, not compiled in, or not in the boot-time lsm= list
```

`net=unavailable` means `--net-deny` will be refused here (Linux below ABI 4, kernel 6.7, or without seccomp). The Linux note also says
whether seccomp is available and what the fence costs: without seccomp the default wall refuses to start (the AF_UNIX
fence cannot be built), and `--unix` is required, with `ptrace` and `io_uring` then unfenced too. On macOS `check` applies a trivial profile, so it also fails inside a sandbox that forbids
nesting. Exit 0 on `CHECK OK`, 1 on `CHECK FAILED`.

## show

`show` prints the wall a flag set would apply here, and runs nothing. It takes the run flags. The command is optional.
An AI can check the wall before it runs a command.

```
nova-sandbox show --read /x --write /y --net-deny
```

The output is one block on stdout, in a stable order. Each line is a kind, then `key=value` fields. A value with a space,
a quote or an `=` is quoted.

| line | Linux | macOS |
|---|---|---|
| `wall` | `backend=landlock abi=<n> seccomp=on\|off` | `backend=sandbox-exec bin=<path>` |
| `fs` | the handled filesystem rights | - |
| `path` | one per rule: `access=read\|write path=<p> from=root\|--read\|--write`, and `state=absent` for a root not here | - |
| `param` | - | one per `-D`: `name=READ0 path=<p>` |
| `net` | `rule=open\|closed\|tcp_allow`, what closes each kind, the ports | `rule=open\|closed\|localhost_allow`, the ports |
| `seccomp` | one per rule, in the order the filter applies them: `action=kill\|eperm\|allow rule=<name>` | - |
| `scope` | signal and abstract-socket scope (ABI 6), or `none` | - |
| `fds` | `close=above_stderr`, or `keep=all` under `--keep-fds` | the same |
| `command` | `exec=<path> argv=[...]`, when a command is given | the same |
| `profile` | - | last; the generated profile text follows, verbatim |

The Linux block for the example above on an ABI 4 kernel:

```
wall backend=landlock abi=4 seccomp=on
fs handled=execute,write_file,read_file,read_dir,remove_and_make,refer,truncate
path access=read path=/usr from=root
...
path access=write path=/dev/shm from=root
path access=read path=/x from=--read
path access=write path=/y from=--write
net rule=closed tcp=deny by=landlock,seccomp udp=deny raw=deny packet=deny by=seccomp dns=/etc/hosts_only
seccomp action=kill rule="another architecture's syscall (i386, x32 compat)"
seccomp action=kill rule="an x32 syscall number"
seccomp action=eperm rule=ptrace
seccomp action=eperm rule="io_uring_setup, io_uring_enter, io_uring_register"
seccomp action=eperm rule=socket(AF_UNIX)
seccomp action=eperm rule="socket of every family but AF_UNIX and AF_NETLINK (no TCP, UDP, raw or packet socket)"
seccomp action=eperm rule="socket(AF_NETLINK) unless NETLINK_ROUTE (no sock_diag, which lists other processes' connections)"
seccomp action=eperm rule="socketpair of any family but AF_UNIX"
seccomp action=eperm rule="socketpair(AF_UNIX) unless SOCK_STREAM"
seccomp action=allow rule="everything else"
scope none note=below landlock abi 6 a signal can reach a process outside the wall
fds close=above_stderr
```

Exit 0 when the wall would start here. Exit 1 when it would refuse here: the `SANDBOX REFUSED` lines go to stderr and
stdout is empty. Exit 2 for a malformed invocation, including a path that does not exist. A command that is missing or
not executable is 127 or 126, as for a run.

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
| `tcp_allow` | `--write in --net-deny --net-allow` that listener, the same connect | allow |
| `udp_control` | `--write in`, one datagram to a UDP listener the probe owns | allow |
| `tcp_other_deny` | `--write in --net-deny --net-allow` that listener, connect to a second listener on another port | deny |
| `udp_deny` | `--write in --net-deny`, the same datagram | deny |
| `udp_deny_allowmode` | `--write in --net-deny --net-allow` the TCP listener, the same datagram | deny |
| `tfo_control` | `--write in`, a TCP Fast Open send to the second listener (Linux only) | allow |
| `tfo_deny` | `--write in --net-deny --net-allow` the first listener, the same send (Linux only) | deny |
| `listen_control` | `--write in`, `listen` on a TCP socket never bound (Linux only) | allow |
| `listen_deny` | `--write in --net-deny --net-allow` the first listener, the same `listen` (Linux only) | deny |
| `dns_control` | `--write in`, a lookup of `example.com` | allow |
| `dns_deny` | `--write in --net-deny`, the same lookup | deny |

The UDP, `tcp_other_deny` and TCP Fast Open steps are judged by the listener as well: a datagram or connection that
arrives was let through, whatever the sender says. When Fast Open does not work on the host at all (`tfo_control`
fails), `tfo_deny` reports `got=untested`. The DNS
steps run only when the probe itself, unwalled, can resolve `example.com`. Otherwise `dns_deny` reports
`got=untested` with a note and does not fail: a host with no DNS proves nothing.

One line per step, then a verdict:

```
PROBE STEP name=write_outside expect=deny got=deny
PROBE STEP name=net_deny expect=deny got=deny
PROBE OK backend=landlock abi=6
```

A step that went the wrong way ends ` FAIL` and the verdict is `PROBE FAILED backend=<b> abi=<n> failed=<count>`.
Where `--net-deny` cannot be enforced, every step that uses it reports `got=wall_refused` with a note and does not fail. A setup
error is `PROBE FAILED reason=<error>`. Exit 0 on `PROBE OK`, 1 otherwise.

The network steps run this binary's hidden verb `probe-client <kind> <arg>` inside the wall, which exits 0 when the act
succeeds. The kinds:

- `dial`: a connect within three seconds, to a TCP address, or to a unix socket when the argument is an absolute path.
- `udp`: one datagram sent.
- `resolve`: a lookup through the system resolver. On macOS that is libSystem's `getaddrinfo`; on Linux, Go's resolver,
  which sends the same DNS queries.
- `tfo`: a `MSG_FASTOPEN` send (Linux).
- `listen`: `listen` on an unbound TCP socket (Linux).

No shell or tool present on every host can open a connection. The binary's directory is granted with `--read` for those
steps.

## Refusals and exit codes

Before anything runs, every problem is reported at once, on stderr.

A malformed invocation is one line, in the shape of CLI-STYLE (f), and exit 2. It names every problem, each as
`<reason>: <text>`:

```
nova-sandbox: bad_read: --read /x is not an existing directory; none is created. no_command: no --; ...; run: nova-sandbox -h
nova-sandbox check: check takes no flags or arguments and got --x; run: nova-sandbox check -h
nova-sandbox: unknown verb x; the verbs are show check probe help (see nova-sandbox help), or flags then -- <cmd>; run: nova-sandbox -h
```

The malformed reasons are `bad_flag`, `no_command`, `bad_read`, `bad_write`, `bad_net` and `bad_gpu`. A command
that is missing or not executable joins that line when it comes with one of them.

Every other refusal is a runtime one: one line each, then 125 (the wall refused), or 126 or 127 (only the command):

```
SANDBOX REFUSED reason=<reason>: <text>
```

| reason | when |
|---|---|
| `bad_flag` | an unknown flag, a flag with no value, an unknown verb, or a flag or argument a verb does not take |
| `no_command` | no `--`, or nothing after it |
| `bad_read`, `bad_write` | a path that is relative, missing, not a directory, unresolvable, or holds a character the generated rules cannot carry (a control character; on macOS also `"` `\` `(` `)`); `bad_read` also for a command outside every grant |
| `bad_net` | a `--net-allow` that is not loopback `host:port`, or one without `--net-deny` |
| `bad_gpu` | `--gpu` on Linux |
| `not_found` | the command is on no `PATH` entry, or the path given does not exist |
| `not_executable` | the command is not an executable file |
| `no_sandbox` | no backend on this host |
| `no_seccomp` | Linux with no seccomp filter mode and no `--unix`: the AF_UNIX fence cannot be built |
| `net_unenforceable` | `--net-deny` on a Landlock ABI below 4, or on a kernel without seccomp |
| `sandbox_failed` | the kernel refused a rule, the restriction, the seccomp filter, or the exec |

| exit | meaning |
|---|---|
| the command's | the wall stood and the command ran; its status is the tool's |
| 2 | the invocation is malformed (one `nova-sandbox:` line on stderr) |
| 125 | the wall refused to start on this host (`SANDBOX REFUSED` on stderr) |
| 126 | the only problem is a command that is not an executable file (shell convention) |
| 127 | the only problem is a command on no `PATH` entry or a path that does not exist |
| 0 / 1 | `check`, `probe` and `show`: yes / no; `help` and every `-h` are 0 |

A command may exit 2, 125, 126 or 127 itself. The stderr line tells them apart: the tool's own refusals always start
`nova-sandbox` or `SANDBOX REFUSED`, and nothing runs after one.

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
No socket can reach the network: no TCP connect, no UDP, no DNS. A toolchain outside the system roots (for example
`~/sdk/go`) is named with one more `--read`. Its exit status is `go build`'s. The same line, with `go` replaced by something that
reaches outside, prints the kernel's denial from the command itself and the tool adds nothing.

To see the wall before running it, and that it works on the host before relying on it:

```
nova-sandbox show --write /work/src --write /work/scratch --read /home/me/go/pkg/mod --net-deny
nova-sandbox check && nova-sandbox probe
```

## Tests

Unit tests use no real time, sockets or network, and run on every OS: flag parsing into the internal spec; the checks
and refusals of `build` (on scratch directories), for both `linux` and `darwin` path rules; the Landlock ruleset for
each ABI built from a spec; the macOS profile text built from a spec, including that `--read` and `--write` paths enter
only as parameters; the seccomp program, built for both amd64 and arm64 syscall numbers and interpreted against synthetic `seccomp_data`
for every rule under every flag set, on every OS: the kill for a wrong architecture and an x32 number, each socketpair
type, and under `--net-deny` and `--net-allow` each socket kind (TCP, UDP, ICMP, raw, SCTP, MPTCP, packet, SMC, vsock,
`AF_ALG`, each netlink protocol), `MSG_FASTOPEN` in each send's flags argument, and `listen`/`accept`/`accept4`;
that the native numbers match the kernel headers (Linux); the rule names `show` prints; the
`show` text for both OSes; the Linux plan and its refusals; exit codes and the one-line refusals through `run`,
2/125/126/127.

Functional tests (build tag `functional`) build the binary, wall real children and probe them. They run on Linux with
Landlock: `check` and `probe` (including `unix_deny`/`unix_allow`), a write inside and outside, a read outside and
named, exit status pass-through, the 125/126/127 refusals, environment and working directory pass-through, `argv[0]`
verbatim through a symlink and a symlinked `#!` script, the fence's cost (`asyncio`, socketpair types, `logger`,
`gpg-connect-agent`), the AF_UNIX fence and `--unix`, the `systemd-run --user` escape (skipped where systemd
`--user` is absent), `--keep-fds`, `--net-deny` with `--net-allow`, a UDP datagram under `--net-deny` and
`--net-allow` (the listener judges), every socket kind from Python under both, the TCP Fast Open and `listen`-without-`bind` holes under `--net-allow`
(with a unix server under `--unix --net-deny` still working), DNS through Go and through glibc
`getent` under `--net-deny` (with `/etc/hosts` still answering), the `show` block against a snapshot, and the worked
example above, as written. The macOS wall is covered at unit level by the
generated profile text only.
