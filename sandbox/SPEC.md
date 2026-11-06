# nova-sandbox: tests and models

The [guide](README.md) is how to use the tool. This page is for changing it.
Build and test commands for every tool are in [TESTING.md](../docs/TESTING.md).

Unit tests cover argument parsing, path checks, generated rules,
the macOS profile text and exit codes. Linux tests run the seccomp program
against synthetic system-call data. Functional tests run child processes on
Linux with Landlock. The macOS tests read the profile as text, so macOS
enforcement needs its own check on a Mac. `nova-sandbox probe` checks the wall
on any host.

## What the command receives

`PATH`, `HOME`, `LANG` and `TERM` are kept. `--env <name>` passes another
variable by name; the value is never an argument. A name that is not letters,
digits and underscores, or that is unset on a run, is `bad_env` (exit 2), and
it is reported together with every other problem. `show` checks the name and
does not require it to be set. The guide's Go build passes `TMPDIR`, `GOCACHE`,
`GOMODCACHE` and `GOPROXY` with `--env`. `TMPDIR`, `GOCACHE` and `GOPROXY` point
inside a `--write`; `GOMODCACHE` points inside a `--read`. `HOME` is kept.

## What a run prints

Once the wall has been built, stderr gets one `WALL` line before the command,
and the same line when the working directory is refused. `grants=none` means no
`--read` and no `--write`. Otherwise the line lists `read=` and `write=`.
`net=open` unless `--net-deny` was set, in which case it is `net=denied`.
System roots are not listed.

A working directory outside the system roots and every grant is `cwd` (exit 125,
not 2). The text is `<cwd> is outside the wall; cd <granted dir> or add --read <cwd>`.
A missing `--write` includes `mkdir -p` of that path. A path that exists and is
not a directory keeps the old text and does not say mkdir.

The parent stays for the command's lifetime and in the same process group as
the command, so a terminal read is not stopped. The child is a re-exec of this
binary (`wall-apply`, not a public verb) that applies the wall and execs in
place. A command killed by signal N makes the parent print
`nova-sandbox: command killed by signal N (exit 128+N)` and exit 128+N.
SIGINT, SIGTERM, SIGHUP and SIGQUIT delivered to the waiter are forwarded to
the command's pid.

On Linux the child is started with `Pdeathsig` SIGKILL. When the waiter dies
for any reason, including SIGKILL, the kernel kills the command. On macOS
there is no `Pdeathsig`. The four signals above are forwarded only while the
waiter is still alive to do it. SIGKILL of the waiter is not caught, and the
command can keep running.

`check` OK carries the field `next=nova-sandbox\x20probe` before `note=`.
The `\x20` is a space, escaped the way cairn escapes a field.

## System roots

The command can always read and run these paths.

| OS | Paths |
|---|---|
| Linux | `/usr /bin /sbin /lib /lib64 /etc /opt /dev /proc /run/systemd/resolve` and the directory of `resolv.conf` |
| macOS | `/System /usr /bin /sbin /opt/homebrew /opt/local /Library /private/etc /private/var/select /private/var/db/timezone /dev` and the `xcode-select` directory |

On Linux each Landlock ABI adds a rule: 2 rename across directories, 3
truncate, 4 TCP, 5 ioctl on devices, 6 signal and abstract-socket scope.

## Linux network and seccomp rules

`--net-deny` needs Landlock ABI 4 or later and seccomp filter support. Landlock
restricts TCP connections and binds; seccomp blocks creation of other network
sockets. With no allowed port, IP sockets are denied. Local `NETLINK_ROUTE`
queries remain available for interface information. Unix-domain sockets
follow the separate rules below.

With `--net-allow`, TCP client connections to the named ports are permitted
on every destination address. UDP, raw sockets and other network protocols
remain denied. Listening, accepting connections and TCP Fast Open are also
refused in this mode. The listen and accept restrictions apply to Unix sockets
too. Use `--unix` with `--net-deny` alone when a command needs to serve a local
Unix socket.

Under `--net-deny`, ordinary DNS over UDP is unavailable. Names in
`/etc/hosts` work; with an allowed port, a command can use a resolver that
supports TCP on that port. Without `--net-deny`, network access is unrestricted.

From ABI 6, signals and abstract Unix sockets are scoped to processes created
inside the wall. Earlier ABIs do not provide that scope. The default seccomp
socket restriction still applies on those hosts.

By default, seccomp refuses Unix-domain socket creation except connected
stream socket pairs. Local services that need Unix-domain sockets will fail.
This includes syslog, socket-based NSS lookups, D-Bus and agents. Plain
`/etc/passwd` lookups remain available. DNS also depends on the network flags.

`--unix` relaxes that restriction. Landlock does not check a Unix connection
against filesystem grants, so keeping a service's socket outside a grant does
not isolate it. Use this flag only when the command can be trusted with the
local services it can reach.

When seccomp is available, it also refuses `ptrace` and `io_uring`, including
under `--unix`. Without seccomp, the default wall refuses to start.
`--unix` permits a Landlock-only run on such a host; those extra restrictions
are then absent. Such a run cannot use `--net-deny`.

## Probe steps

Each step prints `PROBE STEP name=<step> expect=allow|deny got=<what happened>`.

The probe makes temporary `in` and `out` directories and runs a small set of
checks through the same binary. It removes those directories afterward.

| Step | Expected result |
|---|---|
| `write_outside_control` | An unwalled write succeeds. |
| `write_inside` | A write inside the grant succeeds. |
| `write_outside` | A write outside the grant is denied. |
| `read_outside` | A read outside the grant is denied. |
| `read_named` | An added read grant permits the read. |
| `unix_deny` | A Unix-domain connection outside the grants is denied. |
| `unix_allow` | On Linux, the same connection succeeds with `--unix`. |
| `net_control` | A connection to the probe's TCP listener succeeds. |
| `net_deny` | The connection is denied under `--net-deny`. |
| `tcp_allow`, `tcp_other_deny` | The selected TCP port is allowed; another port is denied. |
| `udp_control`, `udp_deny`, `udp_deny_allowmode` | UDP works in the control case and is denied at the ungranted port in the restricted cases. |
| `tfo_control`, `tfo_deny` | Linux TCP Fast Open control and denial checks. |
| `listen_control`, `listen_deny` | Linux listening control and denial checks under an allowed-port rule. |
| `dns_control`, `dns_deny` | DNS works in the control case and is denied with network restrictions. |

Each step prints its expected and observed result. A mismatch adds `FAIL`.
The final line is `PROBE OK` or `PROBE FAILED`; exit codes are 0 and 1.
Setup failures also exit 1 with a reason. Linux has up to 20 steps; platform
support and control results determine which can run. The probe uses local
listeners and performs a DNS lookup.

Where TCP restrictions are unavailable, `net_deny` reports `got=wall_refused`
with a note and does not fail the probe. Read the step results as well as the
exit code. A failed DNS or Fast Open control makes the corresponding denial
check `untested`, with an explanation. These checks cover their named
operations, not every possible use.
