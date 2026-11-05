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
  `nova-reserve set: capacity in use: slot1 held by pid 102933 since ...; retry when free`
- `--exclusive` takes every slot, whatever N is. It does not take `--slots`.

Before this rule each caller counted its own `--slots`, so a `--slots 1` holder and a `--slots 4` caller both ran.
`reserve/tla/Capacity.tla` models the rule; its witness `CapacityBrokenPerCaller.cfg` is that bug.

## Verbs

**run** (the default verb; `run` may be written or left out). Wait for a slot, run the command, free the slot when it
exits. Exit with the command's status.

```
nova-reserve --wait 15m --why "unit tests" -- go test ./...
```

Before the command starts, one line goes to stderr: `nova-reserve: HELD mode=shared slot=2/4 pid=1234`. The command
is found on PATH before any slot is taken. The command dies with nova-reserve on Linux and Windows. On macOS and the
BSDs a SIGKILL of nova-reserve leaves the command running without its slot. SIGINT and SIGTERM are passed on to it.

**hold**. Take a slot and keep it until SIGTERM, SIGINT or SIGHUP, or until `--for` has passed. Stdin is never read, so
a tool call with no stdin holds as long as asked.

```
nova-reserve hold --for 2h --why "bench sweep" &
...
kill %1
```

It prints `HELD ...` to stdout, then on exit `RELEASED reason=signal signal=SIGTERM held_for=12m3s` or
`RELEASED reason=for held_for=2h0m0s`. With no `--why` it records its parent's command line, where it can be read
(Linux, macOS).

**status**. Print the capacity, each slot, the two inner locks, and how many slots are free.

```
$ nova-reserve status
capacity=4 set_by=pid\s123\sat\s2026-11-05T22:00:00Z
slot1 held pid=102933 since=2026-11-05T22:03:32Z why=unit\stests cmd=go\stest\s./...
slot2 free
slot3 free
slot4 free
gate free # held by an --exclusive run while it waits, so no new holder passes
all free # the exclusive lock; held only by an --exclusive run
free=3/4
```

Field values are escaped onto one word: `\s` space, `\t` tab, `\n` newline, `\\` backslash, `\xHH` other control
bytes. A directory that does not exist is a refusal (exit 2), never "all free".

**set**. Store the capacity.

```
nova-reserve set --slots 4
```

**help**. `nova-reserve help`, `nova-reserve help <verb>`, `nova-reserve <verb> -h` and `--help` print usage, exit 0,
and touch nothing.

## Flags

| flag | verbs | wants |
|---|---|---|
| `--slots N` | run, hold, set | 1..1024; the capacity you expect (run, hold) or store (set) |
| `--exclusive` | run, hold | no value; take every slot |
| `--wait D` | run, hold | a Go duration; the longest wait for a slot; default no bound |
| `--for D` | hold | a Go duration above 0; release after it |
| `--why TEXT` | run, hold | a label, shown by status and in refusals |
| `--dir DIR` | all | the reservation directory |

A flag a verb does not take is refused, never ignored. A bad value is one line:
`nova-reserve: --slots wants 1..1024, not "abc"; run: nova-reserve -h`. A waiting taker polls once a second; the last
sleep is cut short so `--wait` is a bound, not a minimum.

## Exit codes

| code | meaning |
|---|---|
| 0 | done |
| 1 | the tool said no: no slot within `--wait`, or `set` while a slot is held |
| 2 | could not run: a bad flag, value or verb; a `--slots` that differs from the capacity; a missing `--dir` for status |
| 126 | the command was found but is not executable; no slot was taken |
| 127 | the command was not found; no slot was taken |
| other | the command's own status; 128 + the signal when a signal ended it |

A timed-out take names who holds what:
`nova-reserve: no slot within 1s: slot1 held by pid 102933 since 2026-11-05T22:03:32Z (why: bench); run: nova-reserve status`

## The directory

`<dir>` is `--dir`, else `$NOVA_RESERVE_DIR`, else `/tmp/nova-reserve` (`C:\ProgramData\nova-reserve` on Windows).
Every caller on a machine must use the same one. nova-reserve creates it (not its parents) and makes it world-writable
and sticky.

| file | what it is |
|---|---|
| `capacity` | the slot count on line 1, then `pid <n> at <time>` of whoever set it |
| `slot1` .. `slotN` | a shared holder's lock, taken exclusive, one per holder |
| `all` | taken shared by every shared holder; taken exclusive by an `--exclusive` run once they are gone |
| `gate` | taken exclusive by an `--exclusive` run while it waits; shared takers pass it shared and drop it once they hold |
| `<lock>.holder` | `pid= since= why= cmd=` of the lock's holder, for status; the lock, not this file, is the truth |

A shared taker passes the gate, reads the capacity, scans `slot1..slotN` for a free one, then takes `all` shared. A
first taker that finds no capacity takes `gate` and `all` exclusive, writes the file if it is still absent, and starts
again. `set` writes only while it holds both. So the capacity cannot change under a holder. Slot files left from a
larger old capacity show in status as "beyond capacity"; no new holder takes them.

There is no arrival order: waiters poll, and a steady stream of takers may pass a waiting one.

## Models

`reserve/tla/`: `SlotCount.tla` is the guarantee (at most N shared holders; an exclusive one alone). `SlotFiles.tla`
is the lock layout for a fixed N. `Capacity.tla` is the capacity file beside the slot array, gate and all: one N for
every caller, set only when free. Each `*Broken*.cfg` is a reversed witness TLC must fail; `CASES.tsv` lists them.
Run them with `tla/check.sh <tla2tools.jar> reserve/tla`.

## Platforms

Linux, macOS and Windows are tested. The release workflow builds linux/amd64, linux/arm64, darwin/arm64 and
windows/amd64. On the BSDs, build from source: `go build -o nova-reserve ./reserve`. On Windows there are no signals to
pass to the command; Ctrl+C reaches it directly, and `hold` ends on Ctrl+C or `--for`.
