# nova-reserve

nova-reserve limits how many jobs run at once on a machine. Each job holds a
**slot**; when every slot is taken, the next job waits its turn. The number of
slots is the machine's **capacity**.

## Install and try

[Install one tool](../../docs/README.md#install-one-tool), choosing `reserve`.
It needs no server or database. Use it to limit cooperating build or test
jobs on one machine; it does not limit memory use, and jobs that do
not reserve a slot are unaffected. `nova-reserve help run` prints the command
syntax and changes nothing.

The examples use a POSIX shell on Linux or macOS. Set a capacity, run
one command through nova-reserve, then use `status` to see the freed slot.

The examples below use a new temporary directory,
set once with `NOVA_RESERVE_DIR`. Everything that should share the limit must
use the same directory. For example, `/var/tmp/nova-reserve` is a good
choice on Linux and macOS (it is the default); use a local disk.

```sh
export NOVA_RESERVE_DIR=$(mktemp -d)
```

You can pass `--dir DIR` to any command instead. The directory is created, but
its parent must already exist.

## Set the capacity

```sh
nova-reserve set --slots 4
```

```text
CAPACITY SET slots=4
```

If you never run `set`, the first reservation stores `--slots`, or the CPU
count divided by four (at least one). Setting the value it already has prints
`CAPACITY SET slots=4 note=unchanged`.

## Run a command

```sh
nova-reserve --wait 15m --why "unit tests" -- sleep 1
```

```text
nova-reserve: HELD mode=shared slot=1/4 pid=811078
```

The command runs once a slot is free, its output passes through, and
nova-reserve exits with its status. The `HELD` line goes to stderr. Process IDs
and times in these examples will differ on your machine.

On Linux the command runs on its slot's share of the CPUs nova-reserve may
use, less the kernel's isolated ones (slot k of N gets the k-th of N
near-equal runs; an exclusive job gets them all), so `nproc`,
`make -j$(nproc)` and Go count that share, and the `HELD` line ends with them,
such as `cpus=2-3`. The pin is advisory: a job can widen its own mask again
(for example with `taskset`), so it steers tools that size themselves by the
CPUs they may use and is not a limit. If they cannot be set, one stderr line
says why and the command runs unpinned; macOS and the BSDs do not pin.

`--wait D` gives up after a time such as `90s` or `15m`; the default waits for
ever. `--why TEXT` labels the job in `status`. Flags take `--wait 1s`,
`--wait=1s`, `-wait 1s` or `-wait=1s`.

## Reserve the whole machine

```sh
nova-reserve --exclusive --wait 1m -- sleep 1
```

```text
nova-reserve: HELD mode=exclusive slot=all pid=811079
```

An exclusive job waits for every running job to finish. While it waits, no
new job can start; once it has run, the others go on.

## Read the state

```sh
nova-reserve status
```

`status` shows the capacity (who stored it and when), each slot as `free` or
`held` with the holder's PID, start time, reason and command, and
`free=<n>/<capacity>`. `gate` and `all` are the locks an exclusive job uses.
It is a snapshot; it can lag a moment behind the locks. Spaces in values show
as `\s`.

## Refusals and what to do next

Every refusal is one line on stderr.

| You see | Exit | What to do |
|---|---|---|
| `no slot within 1s: ...` | 1 | Every slot was held for the whole wait. The line names the holders; run `nova-reserve status`, then retry or wait longer. |
| `this machine's capacity is 8 (...); you asked 3; run: nova-reserve set --slots 3 ...` | 2 | Drop `--slots` to use the stored count, or run `set` while no job holds a slot. |
| `capacity in use (...)` | 1 | `set` needs a free machine. Retry when the holders finish. |
| `NAME: command not found on PATH=...` | 127 | Fix PATH or give the full path. A name with a slash prints `NAME: no such file`. No slot was taken. |
| `NAME: not executable` | 126 | Fix the file's permissions. No slot was taken. |
| `--dir DIR: parent PARENT does not exist; run: mkdir -p PARENT && ...` | 2 | Run the printed `mkdir` and repeat the command. |
| `--dir DIR is a file, not a directory; run: ls -ld DIR` | 2 | Give a directory, or a path that does not exist yet. |
| `... status could not show this hold; run: sudo rm -f FILE && ...` | 2 | A holder file this user can neither remove nor write (another user's). Run the printed command. No slot is kept. |
| `... is not a reservation directory ...` | 2 | `status` found no reservation there. Use the directory your jobs use. |
| `capacity=? # corrupt: ...` | 0 | The capacity file is damaged. Run the printed `set` command while no job holds a slot. |

A bad or repeated flag, a missing command, a number outside 1 to 1024, or a
`--wait` not above zero exits 2; `nova-reserve help run` (or `status`, `set`)
shows the usage. A run that starts returns the
command's own status; if a signal ended it, 128 plus the signal number, and
stderr says so: `nova-reserve: make ended by signal SIGKILL; exit 137`.

## Guarantees and limits

Guarantees:
- No more jobs hold slots than the capacity, and an exclusive job holds all of them.
- The operating system frees a slot when its process ends, even if killed.
- Slots go to whichever waiter polls first after one frees (waiters poll once a
  second); a shared job takes the lowest free number.
- The capacity changes only while no job holds a slot.

Limits:
- Only jobs that use nova-reserve with the same directory share the limit.
- The directory must be on a local disk. Keep it out of places that clean
  temporary files, or the stored capacity is lost.
- On Linux, macOS and the BSDs, a watchdog process (nova-reserve --watch) polls
  once a second and kills the command's process group after a SIGKILL of
  nova-reserve; on Linux, Pdeathsig kills the command itself at once.
- Release builds cover Linux amd64 and arm64 and macOS arm64; on Windows, use
  the Linux build under WSL2.
