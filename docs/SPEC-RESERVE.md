# nova-reserve

Limit how many cooperating jobs run on one machine. Each job holds a **slot**,
a place in the machine's concurrent job limit. When all slots are held, the
next job waits. An exclusive reservation waits for the machine to be free,
then prevents other cooperating jobs from starting.

Reservations use OS file locks. Every caller must use the same directory.
Programs that do not use nova-reserve are outside this limit.

## Set the capacity and run a command

```sh
nova-reserve set --slots 4
nova-reserve --wait 15m --why "unit tests" -- go test ./...
nova-reserve status
```

The directory holds one capacity, shared by every caller. The first shared reservation
sets it from `--slots`, or from the CPU count divided by four, with a minimum
of one. Later callers use the stored value. A different `--slots` value is
refused; change it with `set` when no reservation is held.

The command is found before a reservation is taken. Once acquired, a `HELD`
line goes to stderr and the command runs. Its output passes through, and
nova-reserve returns its exit status.

## Commands

```text
nova-reserve [run] [--slots N | --exclusive] [--wait D] [--why TEXT] [--dir DIR] -- <cmd> [args...]
nova-reserve hold [--for D] [--slots N | --exclusive] [--wait D] [--why TEXT] [--dir DIR]
nova-reserve status [--dir DIR]
nova-reserve set --slots N [--dir DIR]
nova-reserve help [run|hold|status|set]
```

`hold` keeps a reservation until a signal or its `--for` duration expires.
Without `--for`, it also releases when its parent exits, checked once a second.
Use `--for` when the reservation must outlive the shell that started it.
It does not read stdin. With parent PID 1, an untimed hold is refused; give
`--for` when starting it from init, systemd or launchd. A parent outside the
process namespace, reported as PID 0, cannot be watched.

```sh
nova-reserve hold --for 2h --why "bench sweep" &
reservation_pid=$!
# Do the work covered by this reservation.
kill "$reservation_pid"
wait "$reservation_pid"
```

The acquisition and release lines go to stdout. Release reports
`reason=signal`, `reason=for` or `reason=parent_exit`, the time held, and
the signal when applicable.
On Unix, `hold` handles SIGTERM, SIGINT and SIGHUP. On Windows, use Ctrl+C or
`--for`.

`status` shows capacity, each slot's state and holder, the internal `gate`
and `all` locks, and `free=<n>/<capacity>`. Holder details include the PID,
start time, an optional reason, and the child command for a run. It reads
holder records and checks whether their PIDs are alive, without probing the
locks. This is an advisory snapshot; records can lag lock changes. A missing directory is an error, not an
empty reservation table.

`set` stores a capacity between 1 and 1024. A change is refused while a
reservation is held. Setting the existing value succeeds with `note=unchanged`. Root and verb help accept `-h` or `--help`, return 0 and touch no
reservation files.

## Flags

| Flag | Commands | Meaning |
|---|---|---|
| `--slots N` | run, hold, set | Expected capacity for run or hold; new capacity for set. Range 1–1024. |
| `--exclusive` | run, hold | Reserve the whole capacity; cannot be combined with `--slots`. |
| `--wait D` | run, hold | Positive maximum wait, as a Go duration such as `100ms` or `15m`; default unbounded. |
| `--for D` | hold | Release after a positive duration. |
| `--why TEXT` | run, hold | Describe the work in status and refusal output. |
| `--dir DIR` | all | Select the shared reservation directory. |

A flag that does not belong to a command is refused. Waiters poll once a
second, shortening the last sleep to the remaining wait. There is no FIFO
queue, so repeated arrivals can delay a waiter.

## Try the flags in a private directory

These POSIX-shell commands use a separate capacity for the example:

```sh
reserve_dir=$(mktemp -d)
nova-reserve set --dir "$reserve_dir" --slots 2
nova-reserve run --dir "$reserve_dir" --slots 2 --wait 1s --why 'example job' -- /usr/bin/printf 'child ran\n'
nova-reserve --dir "$reserve_dir" --exclusive --wait 1s --why 'exclusive example' -- /usr/bin/true
nova-reserve hold --dir "$reserve_dir" --for 100ms --why 'timed example'
nova-reserve status --dir "$reserve_dir"
nova-reserve help hold
```

The run prints `HELD` on stderr and the child's line on stdout. The timed
hold prints `HELD`, then `RELEASED reason=for`. Status ends with `free=2/2`
after the examples finish. On Windows, use the same flags with a native
command and Windows paths.

## Output and exits

Status values escape spaces as `\s`, tabs as `\t`, newlines as `\n`,
backslashes as `\\`, and other control bytes as `\xHH`.

| Output | Meaning |
|---|---|
| `CAPACITY SET slots=N` | Capacity was stored. The first shared reservation can print this before `HELD`; explicit `set` prints it on stdout. `note=unchanged` means the stored value already matched. |
| `HELD mode=shared slot=K/N pid=P` | This process acquired slot K out of N. |
| `HELD mode=exclusive slot=all pid=P` | This process acquired the whole capacity. |
| `RELEASED reason=... held_for=D` | A hold ended; the reason is its duration, parent's exit or a named signal. A command run returns the child's status without this line. |
| `capacity=N set_by=...` | Status read the stored count and its writer's PID and time. `capacity=unset` means no count has been stored. |
| `capacity=? # corrupt: ...` | Status could not read a valid count; it still shows holders and a repair command, and returns 0 if that report was written. This is not a healthy-capacity result. |
| `slotK free` or `slotK held ...` | The holder record and PID check for that slot. A held line includes `pid`, `since`, and optional `why` and `cmd`. |
| `gate` and `all` | An exclusive caller holds gate while waiting and running, then all while running. Their comments explain those roles. |
| `free=M/N` | Reported free slots within capacity. An exclusive holder makes M zero; unknown capacity prints `free=?`. This is a snapshot, not a reservation. |

A timeout names the holders and suggests `nova-reserve status`. A capacity
mismatch reports the stored count and the `set` command needed to change it.

| Exit | Meaning |
|---|---|
| 0 | Done. |
| 1 | No reservation within the wait, or capacity is in use during set. |
| 2 | Bad invocation, capacity mismatch, or an operation could not run. |
| 126 | Command found but not executable, or failed to launch. Lookup failures take no reservation; launch failures release it. |
| 127 | Command not found; no reservation taken. |
| Child status | The command's status after launch; a signal uses 128 plus its number. |

## Recover from a refusal

| Problem | Next step |
|---|---|
| Bad flag, duplicate flag, missing command or invalid count/duration | Use the named verb's help. Counts are 1–1024; explicit `--wait` and `--for` must be positive. |
| Capacity differs from `--slots` | Omit the expectation to use the stored count, or use `set` to change it once free. |
| Capacity file is corrupt | Inspect the reported file, then repair the count with the printed `set` command while free. |
| No reservation within the wait | Use `status` to see reported holders. Retry later or choose a longer wait. |
| Capacity in use during set | Wait for holders to finish before changing it. Setting the existing value is an unchanged success. |
| Untimed hold has parent PID 1 | Supply `--for` so the hold has an end condition. |
| Missing status directory | Select the directory your callers use. `set` or the first reservation creates a new one. |
| Directory or lock-file operation failed | Check the named path, local-disk location, ownership and permissions. Do not remove active lock files. |
| Command missing or not executable | Correct PATH, the executable path or permissions. Lookup failure acquires no reservation; a launch failure after acquisition releases it. |
| Child returned nonzero | Read the child's output. The wrapper returns that status and releases its reservation. |

## Files and process lifetime

Choose the directory with `--dir`, then `NOVA_RESERVE_DIR`. The default is
`/var/tmp/nova-reserve`, or `C:\ProgramData\nova-reserve` on Windows. The tool
needs a local disk. It creates that directory, but not its parents, and uses
world-writable sticky
permissions on Unix.

| File | Purpose |
|---|---|
| `capacity` | Count, followed by the PID and time of the caller that set it. |
| `slot1` … `slotN` | One file lock per slot. |
| `gate` | Stops new shared callers while an exclusive caller waits. |
| `all` | Shared by ordinary holders; held exclusively by a whole-machine reservation. |
| `<lock>.holder` | Holder details for display; the OS lock determines ownership. |

Capacity changes take the gate and all locks exclusively. Files left from a
larger capacity appear as “beyond capacity” in status and are not allocated.

Leave the directory and lock files in place while callers use them.
The capacity file persists between runs and across reboots when the chosen
directory does. Temporary-file cleaners can remove an unused directory.
Choose a directory outside their cleanup rules when capacity must persist.

The kernel releases locks when their process ends. Linux uses flock;
Windows uses LockFileEx. macOS and BSD use flock too. On Linux and Windows,
the child command dies with the wrapper. On macOS and BSD, killing the wrapper
with SIGKILL can leave its child running after its reservation is released.
SIGINT and SIGTERM are forwarded on Unix; Windows delivers Ctrl+C directly.
Windows detects a parent's exit by PID liveness only. If another process
reuses that PID, an untimed hold can remain held.

## Build, test and models

From the repository root, on a build machine:

```sh
build_dir=$(mktemp -d)
go build -o "$build_dir/nova-reserve" ./reserve
go test ./reserve
go test -tags functional ./reserve
tla/check.sh <tla2tools.jar> reserve/tla
```

Unit tests use fake locks and time; functional tests use processes and real
locks. The models describe the maximum holder count, exclusive access,
lock lifetime and shared capacity. Broken variants check that the model
checks detect the mistakes they represent. These results cover the model's
assumptions; process-lifetime limits still depend on the platform.

Release assets cover Linux amd64/arm64, macOS arm64 and Windows amd64.
Build from source on BSD.
