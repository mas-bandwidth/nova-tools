# nova-friend: a friend's runtime

`nova-friend` is the one tool for what a friend, or a coordinator, does
about a friend. A friend is a person -- rowan, stella, emma, johnny -- who
exists whether or not a sprint is running. The tool is about the person,
never the work: the work is cards and copies, and they have their own tools.

Two things are kept apart, and the line between them is Glenn's
(2026-09-26): **who exists is configuration; what a friend is doing is
runtime.** The roster -- each friend's name, slots, tiers and roles -- lives
in [nova-config](../nova-config/README.md), in Postgres, and `nova-config
apply` writes it into Redis. `nova-friend` reads that and never writes it:
a name the roster does not hold is `UNREGISTERED` here, with the line to
run. Everything a friend would just know -- the host she is on, her
harness, her login, her load, the copies she holds, whether she is away --
is runtime she reports herself, through `here`, and it lives in Redis
alone. Lose the store: run `nova-config apply`, then every friend runs
`nova-friend here`, and nothing else is owed.

The store is the one fleet Redis; every verb takes `--redis <addr>`
(default `NOVA_SPRINT_REDIS`, then `NOVA_REDIS_ADDR`, then the seat's). `--as`
is you: when `NOVA_FRIEND` is set it is the default, and `--as` must equal
it. Every success is one typed line on standard output; every refusal is one
line on standard error, `nova-friend <verb>: <why>; run: nova-friend help`,
exit 1 when the store said no and 2 when the call could not run.

## The one line each harness runs when its session opens

```
nova-friend here --as <you> --harness <h> [--pid <harness-pid>] [--login <alias>]
```

Start it once and leave it running. It is the one presence process:

1. It registers you (`ns_friend_here`): a name the roster does not hold is
   refused `UNREGISTERED`; a live session of your name on another host is
   refused `BUSY`, naming the host and session; a session whose beat is
   over a minute old is stale and is taken over. Your `--login` aliases (a
   forge account) go on `friends:login`, once.
2. Then once a second: your beat (host, harness, at, load, cpu count) is
   written to `friend:<you>:beat` with **no TTL** -- keys do not expire,
   readers judge the age; the lease of every copy you hold is renewed from
   an observed owner (`life.FriendBeat`: since nova-tools#4415 a friend's
   copy renews only when the process bound to it is seen alive, so give
   `--pid` your harness's pid and `here` binds every copy you pull to it);
   then the queued work routed to you is taken.
3. On SIGINT or SIGTERM it says bye (fenced on its own session: it never
   deletes another session's beat), prints `FRIEND HERE DOWN as=<you>
   session=<id>` and exits 0. Five beats failing in a row: bye, exit 3. A
   beat another session took: exit 3, no bye.

`--once` registers, ticks once and returns, leaving you up: that is the
first run, and what a test or a hand check uses.

```
FRIEND HERE as=rowan host=studio session=s1 slots=4 taken=0
FRIEND HERE OWNER id=q1~1 state=bound as=rowan pid=8123
FRIEND HERE TOOK sprint=fix id=t7 attempt=1 token=...
FRIEND HERE DOWN as=rowan session=s1
```

## The verbs

| verb | what it does | the line |
| --- | --- | --- |
| `here --as <you> [--harness <h>] [--host <h>] [--login <alias>]... [--pid <pid>] [--session <id>] [--sprint <S>] [--once]` | the presence process above | `FRIEND HERE as= host= session= slots= taken=` |
| `bye --as <you>` | DEL your beat, so you read down at once; registration and dealt work stay | `FRIEND BYE as= was=up\|down` |
| `pull --as <you> [--n <k>] [--dir <d>] [--model <m>] [--harness <h>] [--child <id>]` | ready to working for your copies (one call, `ns_cm_work`), one brief per copy under `--dir` (default `~/.nova-friend/<you>/cards`) | `FRIEND PULLED id= leg= token= card=` per copy, then `FRIEND PULL as= n= free= dir= ms=` |
| `done --as <you> --id <copy> --ok --pr <repo>#<n> --head <sha> --repo <checkout> [--test <t>] [--branch <b>]` | the spec gate in your checkout at the head (`GATE` rows), the PR recorded, the copy ended ok; the primary moves on | `FRIEND RECORDED pr= head= branch=`, `FRIEND ENDED id= primary= from= to= next=`, `FRIEND DONE as= n= ms=` |
| `done --as <you> --id <copy> --fail <why>` | the copy ended failed, the why on the record | the same |
| `done --as <you> --id <copy> --score <N>/10 [--gates <g>] [--finding <text>]` | a read copy's score | the same |
| `list` | every registered friend, sorted | `FRIEND name= state=up\|away\|down slots= tiers= roles= host= working= rev=` per friend, then `FRIEND LIST friends= rev=` |
| `show <name>` | one friend with the session's facts | `FRIEND name= state= slots= tiers= roles= host= working= session= harness= load= models= beat=<age> away= rev=` |
| `away <name> --reason <r> --as <actor>` | set `friend:<name>:down`: no push, take or copy until back; the deal duty moves the ready copies | `FRIEND AWAY name= reason= changed=yes\|no` |
| `back <name> --as <actor>` | clear it | `FRIEND BACK name= changed=yes\|no` |

`state` is what the reader judges: `up` is a beat at most a minute old and
no away flag, `away` is the flag, `down` is the rest. `rev` is the friend
revision `nova-config apply` last stamped (`config:decl`, `rev:friend`), so
a line always says which configuration it reads; `-` when none was applied.

**Refusals.** `UNREGISTERED <name>: not in the roster; run: nova-config
friend add <name> --slots <n> --as <you>, then nova-config apply` (here,
show, away, back). `BUSY <name>: a live session is here already on <host>
(session <s>, beat <age> ago); stop it, or wait a minute and it is taken
over`. `NAME-IS-LOGIN`, `LOGIN-IS-FRIEND <alias>`, `LOGIN-TAKEN <alias> is
<friend>'s login already`. `NOTMINE task:<copy> is friend:<other>'s copy,
not yours` and `NOCOPY` (done). A stale `--token` is `FENCED`, exit 3, as
`nova-sprint card end` spells it. A missing flag names what it wants:
`--reason wants why stella is away`, `--pid wants your harness's live pid,
not this process`, `--as wants your friend name (NOVA_FRIEND is empty)`.

There is no `quickstart`: a verb that registered a friend nobody asked for
would write configuration, which is nova-config's, and `here --once` is the
first run.

## The keys it owns

| key | type | writer |
| --- | --- | --- |
| `friend:<f>:beat` | hash: harness, host, session, at, load1, ncpu, cpu, models; no TTL | `here` (`ns_friend_here`, `ns_friend_here_beat`; `life.FriendBeat` for models), `bye` (`ns_friend_bye`) |
| `friends:login` | hash alias -> friend | `here --login` (`ns_friend_here`) |
| `friend:<f>:down` | hash: reason, actor, at | `away`, `back` (`ns_friend_down`) |
| `friend:<f>:cards:{ready,working,ok,fail}`, `task:<copy>` | the copy model's sets and records | `pull`, `done`, through the one move file (`ns_cm_work`, `ns_cm_end`, `ns_cm_owner`) |
| `cap:log` | receipts: friend-up, friend-down, friend-takeover, friend-login | the functions above |

It reads and never writes `friends`, `friend:<f>:desired` (slots, tiers),
`friend:<f>:roles` and `config:decl`: those are `nova-config apply`'s.

## What comes later

Waking a friend (a wake list, a wake kind of unit or human, a wake path)
is not here: Glenn, 2026-09-27, "the wake config can be added later when
we know what we are doing". The here loop still takes an entry off the
existing wake list each tick, as the hello loop did, and prints `FRIEND
HERE WOKEN as= wake=` when it took one; nothing in this tool puts one
there. The nova-sprint `friend` verbs this tool replaces (`hello`, `beat`,
`pull`, `done`, `down`, `up`, and the rest) are gone since 2026-09-27, and
FRIEND-PRESENCE.md, the retired `nova-wake beat` page, went with them.
