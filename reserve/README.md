# nova-reserve

nova-reserve: reservations on a machine's shared resources so people and AIs can coordinate. A command, or a hold,
reserves one of the machine's N slots, so builds, test runs, benches and CI jobs queue instead of piling onto one box
and timing each other out. The slots are OS file locks in one directory (flock on Linux, macOS and the BSDs,
LockFileEx on Windows): the kernel frees the slot of a holder that ends or dies, SIGKILL included. There is no daemon,
no server and no PID file to clean up. The directory (default `/var/tmp/nova-reserve`, `C:\ProgramData\nova-reserve`
on Windows) must be on a local disk.

A machine has one capacity, N, stored in `<dir>/capacity`. The first caller stores its `--slots` (or the CPU count /
4); a caller whose `--slots` differs is refused (exit 2) and told to run `nova-reserve set --slots N`, which is refused
while any slot is held (exit 1).

The full contract (verbs, flags, exit codes, the files in the directory, platforms) is
[docs/SPEC-RESERVE.md](../docs/SPEC-RESERVE.md).

## Usage

```
nova-reserve [run] [--slots N | --exclusive] [--wait D] [--why TEXT] [--dir DIR] -- <cmd> [args...]
nova-reserve hold [--for D] [--slots N | --exclusive] [--wait D] [--why TEXT] [--dir DIR]
nova-reserve status [--dir DIR]
nova-reserve set --slots N [--dir DIR]
nova-reserve help [run|hold|status|set]
```

- **run** finds the command, waits for a slot, runs it, frees the slot, and exits with its status.
- **hold** holds a slot until SIGTERM, SIGINT or SIGHUP, or `--for D`; it never reads stdin. Without `--for` it also
  ends when its parent exits. It prints `HELD ...`, then `RELEASED reason=signal|for|parent_exit held_for=...`.
- **status** prints the capacity, each slot's holder (pid, since, why, cmd), the `gate` and `all` locks, and
  `free=<n>/<capacity>` (0 during an `--exclusive` run). It takes no lock: it reads the holder files whose pid is alive.
- **set** stores the capacity.

Exit: 0 done; 1 refused (no slot within `--wait`, capacity in use); 2 could not run (a bad flag, value or verb, a
`--slots` that differs from the capacity, a corrupt capacity file, `--wait 0`); 126 not executable; 127 not found,
before any slot is taken; else the command's status. A flag a verb does not take is a refusal, never ignored.

## Build and test

```
go build -o nova-reserve .
go test .                       # unit tests: an in-memory lock table, a fake clock, a fake command
go test -tags functional .      # real processes on real locks
```

## Models

`tla/SlotCount.tla` is the guarantee: at most N shared holders, an exclusive holder alone. `tla/SlotFiles.tla` is the
lock layout for a fixed N (gate, all, slot1..slotN, the scan over them, crashes anywhere) and refines it.
`tla/Capacity.tla` is the capacity file beside the slot array, the gate and all: every holder was let in under the one
stored capacity, never more holders than it, set changes it only when no one holds, and two first takers both get
slots even while holders never finish (`CapacityTwoFirst.cfg`; liveness in `CapacityLive.cfg` runs on two callers,
since three take too long under strong fairness). The `*Broken*.cfg` files are
reversed witnesses TLC must fail: a slot taken without its lock, a shared holder that skips `all`, an exclusive taker
that skips the gate, a crashed holder whose locks are never freed, a crashed holder whose command runs on (macOS and
the BSDs on SIGKILL), a count that is not checked, a taker that counts its own `--slots` (the per-caller bug), two
first takers that both write the capacity, a set that writes while slots are held, a claimer that never sees the
stored capacity, and a status that probes slots by locking them. `CASES.tsv` lists them;
`tla/check.sh <jar> reserve/tla` runs them.
