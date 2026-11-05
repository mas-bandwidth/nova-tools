# nova-reserve

nova-reserve makes reservations on a machine's shared resources, so people and AIs can coordinate. A build, a test
run, a bench or a CI job holds a slot while it runs. When every slot is taken, the next caller waits. Nothing piles
onto one box and times out.

The slots are OS file locks in one directory: flock on Linux, macOS and the BSDs, LockFileEx on Windows. The kernel
frees a holder's locks when it ends, however it ends, SIGKILL included. There is no daemon, no server and no PID file.

## The capacity rule

A machine has one capacity: N slots. It lives in the file `<dir>/capacity`. Every caller counts against the same N.

- The first caller on a machine stores it: its `--slots N`, or the CPU count / 4 (at least 1) when it gave none. It
  prints `CAPACITY SET slots=N`.
- A caller with no `--slots` uses the stored capacity.
- A caller whose `--slots` differs from the stored capacity is refused, exit 2:
  `nova-reserve: this machine's capacity is 1 (set by pid 123 at 2026-11-05T22:03:32Z); you asked 4; run: nova-reserve set --slots 4`
- `nova-reserve set --slots N` changes it. It is refused while any slot is held, exit 1:
  `nova-reserve set: capacity in use (slot1 held pid=102933 since=...); retry when free; run: nova-reserve status`
- `--exclusive` takes every slot, whatever N is. It does not take `--slots`.
- A capacity file that holds no number in 1..1024 is refused, exit 2, with the command that repairs it:
  `nova-reserve: /var/tmp/nova-reserve/capacity holds "", not a capacity of 1..1024; run: nova-reserve set --slots 8  # your choice`.
  The 8 (your `--slots`, else CPU count / 4) is a suggestion; the comment after it is ignored by the shell.

Before this rule each caller counted its own `--slots`, so a `--slots 1` holder and a `--slots 4` caller both ran.
`reserve/tla/Capacity.tla` models the rule; its witness `CapacityBrokenPerCaller.cfg` is that bug.

## Verbs

**run** is the default verb; `run` may be written or left out. It waits for a slot, runs the command, and frees the
slot when the command exits. It exits with the command's status.

```
nova-reserve --wait 15m --why "unit tests" -- go test ./...
```

Before the command starts, one line goes to stderr: `nova-reserve: HELD mode=shared slot=2/4 pid=1234`. The command
is found on PATH before any slot is taken. The command dies with nova-reserve on Linux and Windows. On macOS and the
BSDs a SIGKILL of nova-reserve leaves the command running without its slot. SIGINT and SIGTERM are passed on to it.

**hold** takes a slot and keeps it until SIGTERM, SIGINT or SIGHUP, or until `--for` has passed. Stdin is never read.

```
nova-reserve hold --for 2h --why "bench sweep" &
...
kill %1
```

It prints `HELD ...` to stdout, then on exit `RELEASED reason=signal signal=SIGTERM held_for=12m3s`,
`reason=for` or `reason=parent_exit`. A hold with no `--for` also ends when its parent exits: it checks once a second
whether it was reparented or its parent is gone (on Windows, only whether the parent pid is still running). A hold
with no `--for` whose parent pid is 1 when it starts (launchd, systemd, a container's first process, or a shell such
as `sh -c "nova-reserve hold & exit"` that exited first) has no parent to watch. It is refused before any slot is
taken, exit 2: `nova-reserve hold: no parent to watch (parent pid 1); run: nova-reserve hold --for <duration> ...`.
A hold that must outlive the shell that started it, such as an AI's tool call that backgrounds it, gives `--for`.

**status** prints the capacity, each slot, the two inner locks, and how many slots are free.

```
$ nova-reserve status
capacity=4 set_by_pid=123 set_at=2026-11-05T22:00:00Z
slot1 held pid=102933 since=2026-11-05T22:03:32Z why=unit\stests cmd=go\stest\s./...
slot2 free
slot3 free
slot4 free
gate free # held by an --exclusive run from when it starts waiting until it ends
all free # the exclusive lock: held by an --exclusive run while it runs
free=3/4
```

During an `--exclusive` run, `gate` and `all` show its holder and `free=0/N`. status takes no lock. It reads each
`<lock>.holder` file and believes it while the pid in it is alive, so status never makes a taker skip a free slot.
A SIGKILLed holder's leftover file reads as free. A pid reused after a SIGKILL can read as held until that slot's next
holder rewrites the file. A corrupt capacity file shows as `capacity=? # corrupt: ...` with its repair command, and the
slots are still listed. A directory that does not exist is a refusal (exit 2), never "all free".

Every field is one word. `set_by_pid` and `set_at` are `?` when the file does not say. Only `why=` and `cmd=` carry
free text, and they keep oneline escaping: `\s` space, `\t` tab, `\n` newline, `\\` backslash, `\xHH` other control
bytes.

**set** stores the capacity: `nova-reserve set --slots 4`.

**help**: `nova-reserve help`, `nova-reserve help <verb>`, `nova-reserve <verb> -h` and `--help` print usage, exit 0,
and touch nothing.

## Flags

| flag | verbs | wants |
|---|---|---|
| `--slots N` | run, hold, set | 1..1024; the capacity you expect (run, hold) or store (set) |
| `--exclusive` | run, hold | no value; take every slot |
| `--wait D` | run, hold | a Go duration above 0; the longest wait for a slot. Without it, no bound |
| `--for D` | hold | a Go duration above 0; release after it |
| `--why TEXT` | run, hold | a label, shown by status and in refusals |
| `--dir DIR` | all | the reservation directory |

A flag a verb does not take is refused, never ignored. So is `--wait 0`. A bad value gets one line:
`nova-reserve: --slots wants 1..1024, not "abc"; run: nova-reserve -h`. A waiting taker polls once a second. The last
sleep is cut short, so `--wait` is a bound, not a minimum.

## Exit codes

| code | meaning |
|---|---|
| 0 | done |
| 1 | the tool said no: no slot within `--wait`, or `set` while a slot is held |
| 2 | could not run: a bad flag, value or verb; a `--slots` that differs from the capacity; a corrupt capacity file; a missing `--dir` for status |
| 126 | the command was found but cannot be executed (before any slot is taken, or when it fails to start) |
| 127 | the command was not found; no slot was taken. The line names the PATH searched: `nova-reserve: mkae: command not found on PATH=/usr/bin:/bin` (a name with a slash: `no such file`) |
| other | the command's own status; 128 + the signal when a signal ended it |

A timed-out take names who holds what, in status's words:
`nova-reserve: no slot within 1s: slot1 held pid=102933 since=2026-11-05T22:03:32Z why=bench; run: nova-reserve status`

## The directory

`<dir>` is `--dir`, else `$NOVA_RESERVE_DIR`, else `/var/tmp/nova-reserve` (`C:\ProgramData\nova-reserve` on
Windows). Every caller on a machine must use the same one. nova-reserve creates it (not its parents). A missing parent is refused,
exit 2: `nova-reserve: --dir /nonexistent/x: parent /nonexistent does not exist; run: mkdir -p /nonexistent && nova-reserve ...`
with the whole command line. A parent this user cannot write gets its own line: `... is not writable by this user;
run: ls -ld <parent>`. nova-reserve and makes it
world-writable and sticky, so every user on the machine shares one capacity. `/var/tmp` survives a reboot. A cleaner
of old temporary files (systemd-tmpfiles, after 30 days by default) may still remove it from a machine that went
unused that long; the next taker then stores a new capacity. Point a machine somewhere no cleaner touches with
`NOVA_RESERVE_DIR`. The directory must be on a local disk: flock and LockFileEx over NFS or SMB are not the same locks.

| file | what it is |
|---|---|
| `capacity` | the slot count on line 1, then `pid <n> at <time>` of whoever set it; anything after is ignored |
| `slot1` .. `slotN` | a shared holder's lock, taken exclusive, one per holder |
| `all` | taken shared by every shared holder; taken exclusive by an `--exclusive` run once they are gone |
| `gate` | taken exclusive by an `--exclusive` run from when it starts waiting until it ends; shared takers pass it shared and drop it once they hold |
| `<lock>.holder` | `pid= since= why= cmd=` of the lock's holder, for status |

A shared taker passes the gate, reads the capacity, scans `slot1..slotN` for a free one, then takes `all` shared. A
first taker that finds no capacity takes `gate` and `all` exclusive, without waiting, and writes the file if it is
still absent. It polls that claim, and goes back to the gate as soon as the file appears. `set` writes only while it
holds both. So the capacity cannot change under a holder. A new capacity file is written complete, synced, and renamed
into place. An existing one is rewritten by one write at offset 0, then cut to length, so no kill leaves it empty.
Slot files left from a larger old capacity show in status as "beyond capacity"; no new holder takes them.

There is no arrival order: waiters poll, and a steady stream of takers may pass a waiting one.

## Models

`reserve/tla/`: `SlotCount.tla` is the guarantee: at most N shared holders, and an exclusive one alone.
`SlotFiles.tla` is the lock layout for a fixed N. `Capacity.tla` is the capacity file beside the slot array, gate and
all: one N for every caller, set only when free, and two first takers both get slots even when holders never finish.
Each `*Broken*.cfg` is a reversed witness that TLC must fail; `CASES.tsv` lists them. Run them with
`tla/check.sh <tla2tools.jar> reserve/tla`.

## Platforms

Linux, macOS and Windows are tested. The release workflow builds linux/amd64, linux/arm64, darwin/arm64 and
windows/amd64. On the BSDs, build from source: `go build -o nova-reserve ./reserve`. On Windows there are no signals to
pass to the command; Ctrl+C reaches it directly. `hold` ends on Ctrl+C, `--for`, or its parent's exit, which Windows
detects by pid liveness only: the parent pid stops running. A new process that reuses that pid keeps the hold.
