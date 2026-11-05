# nova-reserve

nova-reserve runs a command holding one of N load slots on this machine, so builds, test runs, benches and CI jobs queue
instead of piling onto one box and timing each other out. The slots are OS file locks in one directory (flock on Linux,
macOS and the BSDs, LockFileEx on Windows): the kernel frees the slot of a holder that ends or dies, however it dies,
SIGKILL included. There is no daemon, no server and no PID file to clean up.

## Usage

```
nova-reserve [--slots N] [--wait D] [--exclusive] [--dir DIR] -- <cmd> [args...]
nova-reserve hold [--slots N] [--wait D] [--exclusive] [--dir DIR]
nova-reserve status [--dir DIR]
```

**run** (no verb) waits for a free slot, runs the command with this process's stdin, stdout and stderr, and frees the
slot when the command exits. It exits with the command's status (128 + the signal when a signal ended it); 2 for a
usage error; 3 when the slot could not be taken. Before the command starts it prints one line to stderr:
`nova-reserve: HELD mode=shared slot=2/4 pid=1234`. SIGINT and SIGTERM are passed to the command (to its process group on macOS and the BSDs).

**hold** takes a slot, prints one line to stdout starting `HELD`, and holds it until its stdin closes, then exits 0.
A caller that is not a shell starts it as a child, waits for the line, works, and closes the pipe; a caller that dies
closes the pipe too.

**status** prints one line per slot file: `slot3 free`, or `slot3 held pid=... start=... cmd=...`, and
`all held ...` while an exclusive holder runs.

| Flag | |
|---|---|
| `--slots N` | how many shared holders may run at once (default: CPU count / 4, at least 1) |
| `--wait D` | the longest wait for a slot, as a Go duration (`90s`, `15m`); default no bound |
| `--exclusive` | run alone: wait for every holder to end, and keep new ones out while waiting and running |
| `--dir DIR` | the slot directory; default `$NOVA_RESERVE_DIR`, else `/tmp/nova-reserve` (`C:\ProgramData\nova-reserve` on Windows) |

Every caller on a machine must use the same directory. Callers may pass different `--slots`: each counts slot1..slotN
of its own N. An exclusive taker first holds the file `gate`, so no new shared taker passes while it waits; then it
waits for `all`, which every shared holder holds shared. There is no arrival order beyond that: waiters poll once a
second, and a steady stream of takers may pass a waiting one.

The command does not inherit the lock, and it dies with nova-reserve so it never runs without its slot: on Linux the
kernel SIGKILLs it when nova-reserve dies (Pdeathsig); on Windows it runs in a job object that is killed when nova-reserve's
handle closes. On macOS and the BSDs it runs in its own process group and SIGINT and SIGTERM are passed to the group,
but there is no kernel tie: a SIGKILL of nova-reserve frees the slot and leaves the command running unslotted.

## Build and test

```
go build -o nova-reserve .
go test .                       # unit tests, an in-memory lock table and a fake clock
go test -tags functional .      # real processes on real locks: a SIGKILLed holder frees its slot
```

`tla/SlotCount.tla` is the guarantee: at most N shared holders, an exclusive holder alone. `tla/SlotFiles.tla` is the
layout on disk (gate, all, slot1..slotN, the scan over them, crashes anywhere) and refines it; it also checks that an
exclusive taker past the gate keeps new shared takers out, that no command runs without its slot, and that every
waiter runs once holders finish. The
`*Broken*.cfg` files are reversed witnesses TLC must fail: a slot taken without its lock, a shared holder that skips
`all`, an exclusive taker that skips the gate, a crashed holder whose locks are never freed, a crashed holder whose command runs on
(macOS and the BSDs on SIGKILL), and a count that is not checked.
