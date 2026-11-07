# nova-sandbox

Run one command inside a kernel-enforced wall. You name the directories the
command may read and write, and you can close its network; everything else is
out of reach. Linux uses Landlock and seccomp. macOS uses `sandbox-exec`.
Other systems are refused. Cd into a directory you grant, and set `TMPDIR`
inside a `--write`, before you run; pass it with `--env TMPDIR`.

A denial is the kernel's EPERM in the command's own output. The command replaces
the walled process. nova-sandbox waits until it exits, so a signal can be
reported: `nova-sandbox: command killed by signal N (exit 128+N)`, and the
status is 128+N. `nova-sandbox probe` confirms the wall before you trust a denial.

Words used below: the **wall** is the set of kernel restrictions applied to the
command. A **grant** is a directory you name with `--read` or `--write`. The
**system roots** are paths the command can always read and run, such as `/usr`,
`/bin`, `/lib` and `/etc` (`nova-sandbox show` lists them all).

## Install

```sh
git clone -q https://github.com/mas-bandwidth/nova-tools
go build -C nova-tools -o "$PWD/bin/nova-sandbox" ./sandbox
export PATH="$PWD/bin:$PATH"
```

## Run a command

```text
nova-sandbox [--read <dir>]... [--write <dir>]... [--net-deny [--net-allow <host:port>]...] [--gpu] [--unix] [--keep-fds] [--env <name>]... -- <cmd> <args...>
```

| Flag | Effect |
|---|---|
| `--read <dir>` | Read, list and run files beneath the directory. |
| `--write <dir>` | Also create, change, rename and delete beneath it. |
| `--net-deny` | Close the network, or refuse to start if the host cannot. |
| `--net-allow <host:port>` | Under `--net-deny`, allow connections to one loopback port. |
| `--gpu` | macOS only: allow Metal and video services. On Linux, use `--write /dev/dri`. |
| `--unix` | Linux only: leave Unix-domain sockets open. |
| `--keep-fds` | Keep inherited descriptors above stderr open (they are closed by default). |
| `--env <name>` | Pass this variable by name. `PATH`, `HOME`, `LANG` and `TERM` pass without it. Repeatable. An unset name is refused on a run. |

Flags go before `--`; everything after it is the command. Directories must be
absolute and exist. A missing `--write` names `mkdir -p` of that path. A path
that exists and is not a directory keeps its refusal and does not name mkdir.
The command must lie under a system root or a grant, so for a program in
`~/bin`, add `--read ~/bin`. The working
directory has to be a system root or a grant. Otherwise the run refuses with
exit 125, not 2: `<cwd> is outside the wall; cd <granted dir> or add --read <cwd>`.
Before the command starts, and on that refusal, stderr gets one line.
`WALL grants=none net=open` means you named no directory and the network is open.
`net=denied` means you passed `--net-deny`. Reads and writes are listed when you
named them. The environment kept is `PATH`, `HOME`, `LANG` and `TERM`.
`--env <name>` passes another by name; the value stays in the environment and is
never an argument. An unset name is refused. Point `HOME` and `TMPDIR` inside a
`--write`, and pass `TMPDIR` with `--env`.

```sh
work_dir=$(mktemp -d)
cd "$work_dir"
nova-sandbox --write "$work_dir" --net-deny -- /bin/sh -c 'printf "hello\n" > result.txt'
cat result.txt
```

`cat` prints `hello`. The exit status is the command's own. A Go toolchain outside the system roots
needs `--read "$(go env GOROOT)"`. A Go build also needs `--env TMPDIR --env GOCACHE --env GOMODCACHE --env GOPROXY`,
each set to a path inside a `--write` (`GOMODCACHE` inside a `--read`). `HOME` is kept.

## Preview the wall: `show`

`show` takes the run flags and prints the rules without starting anything.

```sh
nova-sandbox show --write "$work_dir" --net-deny --net-allow localhost:5432 | grep -E '^(wall|net|fds)'
```

```text
wall backend=landlock abi=4 seccomp=on
net rule=tcp_allow tcp_connect=allow ports=5432 tcp_bind=deny by=landlock listen=deny accept=deny fastopen=deny udp=deny raw=deny packet=deny by=seccomp dns=/etc/hosts_or_a_resolver_on_an_allowed_tcp_port note=landlock filters by port: an allowed port is open on every address
fds close=above_stderr
```

Exit 0 means the wall can start here, 1 means it would be refused.

## Check the host: `check`

```sh
nova-sandbox check
```

```text
CHECK OK backend=landlock abi=4 net=enforceable note=landlock abi 4; seccomp available; net-deny landlock_tcp+seccomp; signals scoped to the wall at abi 6+
next: nova-sandbox probe
```

Exit 1 prints `CHECK FAILED ... reason=...`. `net=unavailable` means
`--net-deny` will be refused here.

## Test the wall: `probe`

```sh
nova-sandbox probe | tail -3
```

```text
PROBE STEP name=dns_control expect=allow got=allow
PROBE STEP name=dns_deny expect=deny got=deny
PROBE OK backend=landlock abi=4
```

`probe` walls test children in a scratch directory and prints one
`PROBE STEP name=<step> expect=allow|deny got=<result>` line per step (20 on
Linux). `got` can also be `wall_refused` or `untested` (with a `note=`, and it
proves nothing). The last line is `PROBE OK` (exit 0) or `PROBE FAILED` (exit 1).

## Refusals

`nova-sandbox`, `help`, `-h` and `help -h` print usage and exit 0. `help <verb>` and
`<verb> -h` print that verb's usage. Any other argument to `help` exits 2.
A malformed command line exits 2 with one line naming every problem:

```text
nova-sandbox: <reason>: <text>; run: nova-sandbox -h
```

```sh
nova-sandbox --read -- true || echo "exit $?"
```

```text
nova-sandbox: bad_value: --read wants an absolute path, got "--"; run: nova-sandbox -h
exit 2
```

A wall that cannot start prints `SANDBOX REFUSED reason=<reason>: <text>`, for
example `SANDBOX REFUSED reason=not_found: nosuchcmd is on no PATH entry or does not exist`.
If the command is killed by signal N, stderr gains
`nova-sandbox: command killed by signal N (exit 128+N)` and the status is 128+N.

| Reason | Exit | Cause and next step |
|---|---|---|
| `bad_flag` | 2 | Unknown flag or missing value. Read `nova-sandbox -h`. |
| `bad_value` | 2 | A flag's value starts with `--`, so the flag took the next flag or `--`. Give the flag its value. |
| `no_command` | 2 | No `--`, or nothing after it. Put the command after `--`. |
| `bad_read`, `bad_write` | 2 | Directory is relative, missing or has unsupported characters, or the command is outside every grant. A missing `--write` names `mkdir -p`. Create it, use its absolute path, or `--read` the command's directory. |
| `cwd` | 125 | The working directory is outside the system roots and every grant. cd into a granted directory, or add `--read` of the working directory. Exit 2 is the tool's usage code, so this refusal is not 2. |
| `bad_net` | 2 | Bad loopback `host:port`, or `--net-allow` without `--net-deny`. |
| `bad_gpu` | 2 | `--gpu` on Linux. Use `--write /dev/dri`. |
| `bad_env` | 2 | `--env` is not a name of letters, digits and underscores, or the name is unset on a run. Set it in the environment; the value is never an argument. |
| `not_found` | 127 | Command is on no PATH entry. Fix PATH or the path. |
| `not_executable` | 126 | Not an executable file. Fix its permissions. |
| `no_sandbox`, `no_seccomp`, `net_unenforceable`, `sandbox_failed` | 125 | The host cannot apply the wall. Run `check` and use a host that can. |

## Guarantees

- Outside the system roots and your grants, the command cannot open, list, run, create, rename or delete files. It can write only beneath `--write` grants, plus `/dev/null`, `/dev/tty` and, on Linux, `/dev/shm`.
- Restrictions pass to every child process.
- Without `--net-deny` the network is unrestricted. With it, Linux allows no TCP, UDP or raw sockets, and macOS no outbound IP or DNS socket. Names in `/etc/hosts` resolve on Linux. If the host cannot enforce this, the tool refuses to start.
- With `--net-allow`, Linux allows TCP connect to that port on every address; listen and accept are refused. macOS allows TCP and UDP to localhost on it.
- Linux also refuses `ptrace`, `io_uring` and, unless you pass `--unix`, Unix-domain sockets (syslog, D-Bus and agents fail).

## Limits

- Needs Linux 5.13 or later (Landlock; `--net-deny` needs ABI 4). `check` reports yours.
- `--unix` lets the command reach any local socket by path, so use it only for trusted commands.
- A kept descriptor (`--keep-fds`) can carry access past the grants.
- CPU, memory, processes and disk use are not limited.
- nova-sandbox waits for the command, in the same process group, so a terminal read keeps working. SIGINT, SIGTERM, SIGHUP and SIGQUIT are forwarded to the command. On Linux the command is killed when the waiter dies. On macOS, SIGKILL of the waiter can leave the command running. See Reference below.

## Reference

### What the command receives

`PATH`, `HOME`, `LANG` and `TERM` are kept. `--env <name>` passes another
variable by name; the value is never an argument. A name that is not letters,
digits and underscores, or that is unset on a run, is `bad_env` (exit 2), and
it is reported together with every other problem. `show` checks the name and
does not require it to be set. A Go build passes `TMPDIR`, `GOCACHE`,
`GOMODCACHE` and `GOPROXY` with `--env`. `TMPDIR`, `GOCACHE` and `GOPROXY` point
inside a `--write`; `GOMODCACHE` points inside a `--read`. `HOME` is kept.

### What a run prints

Once the wall has been built, stderr gets one `WALL` line before the command,
and the same line when the working directory is refused. `grants=none` means no
`--read` and no `--write`. Otherwise the line lists `read=` and `write=`.
`net=open` unless `--net-deny` was set, in which case it is `net=denied`.
System roots are not listed.

A working directory outside the system roots and every grant is `cwd` (exit 125,
not 2). The text is `<cwd> is outside the wall; cd <granted dir> or add --read <cwd>`.
A missing `--write` includes `mkdir -p` of that path. A path that exists and is
not a directory gets a different text that does not say mkdir.

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

`check` OK is followed by the line `next: nova-sandbox probe`.

### System roots

The command can always read and run these paths.

| OS | Paths |
|---|---|
| Linux | `/usr /bin /sbin /lib /lib64 /etc /opt /dev /proc /run/systemd/resolve` and the directory of `resolv.conf` |
| macOS | `/System /usr /bin /sbin /opt/homebrew /opt/local /Library /private/etc /private/var/select /private/var/db/timezone /dev` and the `xcode-select` directory |

On Linux each Landlock ABI adds a rule: 2 rename across directories, 3
truncate, 4 TCP, 5 ioctl on devices, 6 signal and abstract-socket scope.

### Linux network and seccomp rules

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

### Probe steps

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
