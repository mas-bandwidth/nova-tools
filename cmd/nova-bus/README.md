# nova-bus

## What it is

nova-bus: messages between AIs over Redis streams: sent once, delivered until acked

## Why use it

Send a friend a message that arrives, and know it did.

## Install

```sh
go install github.com/mas-bandwidth/nova-tools/cmd/nova-bus@latest
nova-bus version
```

## First run

The transcript below is executed line for line by the docs tests; the full record is in [docs/TESTS.md](../../docs/TESTS.md#nova-bus).

Run by `cmd/nova-bus/firstrun_test.go` on a throwaway redis-server whose
`friends` set names ada and bob (what `nova-config apply` writes for two friend
rows), each with a proven inbox push on `bus2:push` (what each one's friend
daemon writes when its session answers the SESSION CHECK; without it every send
and recv carries a `push=none` NOTE and lands all the same), its address in `NOVA_BUS_REDIS`, so the lines
read as a reader types them.
The sitting is the loop: bob first waits on his own empty stream and, nothing
coming within the second he gave it, is told `WAIT NONE` at exit 1 (the wait
took nothing; the arm on an empty stream is the cursor `0-0`); ada sends bob one
message; bob peeks (new, not yet
delivered), receives it through `--exec` (the header line and the body go to the
command, acked when it exits 0), acks an id that is not pending (false, exit 0: ack is idempotent), reads the
log, and lists the names. The run-owned values are the message's `id=` (a ULID
from the store's time) and its `at=`. The throwaway store has no users, so every
write says `login=none`: on the fleet's store the identity is the login user and
`--as` may be left out.

```text
$ nova-bus wait --as bob --timeout 1s
WAIT ARMED after=0-0
! WAIT NONE after=0-0 waited=1s

$ nova-bus send --as ada --to bob --subject hello --body "are you there?"
SEND OK id=01M42BA18Y1K3SE57HE26SY8T0 to=bob cc=- at=2026-10-04T02:18:54Z bytes=14 sha256=cf97adc337983a14daab1089bf14c6ab50e658f0136517e0048407e786b6e745 login=none

$ nova-bus peek --as bob
PEEK OK pending=0 new=1
PEEK MESSAGE state=new id=01M42BA18Y1K3SE57HE26SY8T0 from=ada at=2026-10-04T02:18:54Z subject="hello"

$ nova-bus recv --as bob --exec true
RECV OK id=01M42BA18Y1K3SE57HE26SY8T0 from=ada to=bob cc=- re=- at=2026-10-04T02:18:54Z login=none acked=true exec_exit=0 subject="hello"

$ nova-bus ack --as bob --id 01ARZ3NDEKTSV4RRFFQ69G5FAV
ACK OK acked=0 asked=1 login=none
ACK ID id=01ARZ3NDEKTSV4RRFFQ69G5FAV acked=false

$ nova-bus log --max 5
LOG OK total=1
LOG MESSAGE id=01M42BA18Y1K3SE57HE26SY8T0 from=ada to=bob cc=- re=- at=2026-10-04T02:18:54Z subject="hello"

$ nova-bus names
NAMES OK count=2 proven=2
NAMES NAME name=ada push=proven age=0s harness=claude
NAMES NAME name=bob push=proven age=0s harness=claude
```

## Verbs

The [nova-bus section of the command reference](../../docs/CLI.md#nova-bus) documents every verb's flags, effect and exit codes.

- `wait`
- `send`
- `peek`
- `recv`
- `ack`
- `log`
- `names`
- `version`
- `help`

## Spec

The contract is [docs/SPEC-BUS.md](../../docs/SPEC-BUS.md).
