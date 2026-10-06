# nova-bus READ and USE rating, nova-tools 1.2.0

Rater: Grok, in Grok Build
Build: 32357608f331
READ: 8/10
USE: 7/10

Question: is this a good tool for an AI to use?

## Reasons

READ. `nova-bus help` is one screen: what it is, five lines of how it works, one usage line per verb, the exit table, and an example block. Every verb's `-h` is the same bytes as `help <verb>` and exits 0 before a dial. docs/SPEC-BUS.md is present tense and matched the runs: the first-run shape in docs/TESTS.md, the deaf line, the exit codes, `kind=` left off a status, and `login=none` on a store with no users. What keeps it from 10 is text an AI would paste or compare that is not the bytes the binary prints. The example block has no `--redis`, so those lines exit 2 as pasted; the how line says the address is `--redis` or `NOVA_BUS_REDIS`, and the README sample includes `--redis`. `send -h` says a dry run prints the line with no id; the line is `id=-`. One subject is quoted one way on peek and log and hex-escaped another way on wait. docs/STANDARD.md:43 still calls this tool messages over git. The banner's first-run sentence and the README row say a Redis that names ada and bob; a store that only has those names still refuses send as deaf. The sentence above the first-run line does state the deaf rule, so the banner is not silent, and the two shorter texts are.

USE. On a throwaway Redis bound to loopback, with no live store and no note sent to anyone, the documented loop worked once ada and bob were on the friends set and each had a fresh proof. `wait --timeout 1s` on an empty stream printed `WAIT ARMED after=0-0` and, on stderr, `WAIT NONE after=0-0 waited=1s`, exit 1. send printed `SEND OK` with `bytes=14` and the sha256 of `are you there?`. peek showed `new=1`. `recv --exec true` acked it. ack of the example id printed `acked=false`, exit 0. log and names matched the transcript. `--stdin` kept the trailing newline (`bytes=5`, sha256 equal to the file). `send --dry-run` printed `dry_run=true` and added no key. A `kind=request` stayed out of `peek --kind status` and out of `recv --kind status`, and `recv --kind request --ack` took it. A subject `PING` was skipped and the cursor moved past it. `--wake-file` returned `WAIT WAKE` when a line was appended. A reply with `--re` cleared that one field of the recipient's owed hash and left the others. A message left pending for eighteen minutes was the next recv after the proof was renewed. `recv --exec false` exited 1 with the id and `the message stays pending`. A proof seventeen minutes old was refused with the age and the remedy, on send and on recv, and nothing was written. Missing flags, an unknown name, a bad kind, and an unknown verb each name the whole set and exit 2. An address off loopback and the tailnet was refused before a dial. With the bus and sprint variables unset, `names` refused and did not read a fleet row.

What holds USE at 7 is three answers that look done and are not. `peek --as zed` and `ack --as zed --id <a real id>` exit 0: peek says `pending=0 new=0`, ack says `acked=false`, and zed is not on the roster. `wait` and `recv` refuse that name. No key was written. A plain `recv` of the only new message printed the body and left it pending; the next `recv`, a second later, printed `RECV NONE: nothing for bob`, exit 1, while peek showed `pending=1` for that same id. `wait` from `0-0` printed five `WAIT MESSAGE` lines and `WAIT OK` and did not say that three more were past the cursor; the next wait, re-armed with that `after=`, printed them. A wait started after `gate open` was already sent armed at the tail and exited 1 with `WAIT NONE`. Help does say the cap is five and that the cursor starts at the tail. The lines do not, and `log`, cut the same way, prints `LOG MORE`.

A 10 refuses an unknown name on peek and ack the way wait already does, makes `RECV NONE` name a message that is held, and makes `WAIT OK` and `WAIT NONE` say what they did not show. The example block then runs as printed.

Not tried: a store with a login user (no password was set), `recv --forever` through a signal, a body over 1 MiB, and a real friend daemon. The proof on the throwaway was written the way the first-run fixture writes it, not by nova-friend.

## Findings

| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | internal/bus/bus.go:507 | `peek --as zed` exits 0 with `PEEK OK pending=0 new=0`. `ack --as zed --id <a real id>` exits 0 with `acked=false`. zed is not on the roster, and no key is written. `wait` and `recv` refuse that name (internal/bus/bus.go:590). | Refuse a name the roster does not hold in Peek and in ack, with the same `unknown` line WaitArm uses. | M |
| 2 | cmd/nova-bus/main.go:577 | A plain recv prints the body and leaves the message pending. The next recv prints `RECV NONE: nothing for bob`, exit 1, while peek shows `pending=1` for that id. | When nothing is claimable and a message is still pending, say `pending=<n>` and the id, and that it stays held until it has been idle fifteen minutes. | S |
| 3 | cmd/nova-bus/main.go:1004 | `wait` from `0-0` printed five messages and `WAIT OK`, and the next wait from that `after=` printed three more. A wait started after a send armed at the tail and printed `WAIT NONE` (cmd/nova-bus/main.go:972). The cap is WaitMax (internal/bus/bus.go:557). Neither line says what was left out. | Print `WAIT MORE` when the cap stops the walk, as log does, and make `WAIT NONE` say nothing new arrived after that id and point at peek. | S |
| 4 | cmd/nova-bus/main.go:1002 | The same subject is `subject="say\n\"hi\""` on peek (cmd/nova-bus/main.go:709) and on log (cmd/nova-bus/main.go:728), and `subject=say\x0a"hi"` on wait. A space is `gate open` on peek and `gate\x20open` on wait. | Pass `tool.Text` for the wait subject, the way peek and log already do. | S |
| 5 | cmd/nova-bus/main.go:185 | The example block (`wait --as bob --timeout 1s` and the six lines under it) has no `--redis`. Pasted with the bus variable unset, `names` exits 2 and names the variable. README.md:24's sample includes `--redis`. | Put `--redis <addr>` on the example lines, or one `NOVA_BUS_REDIS=<addr>` prefix above them. | S |
| 6 | cmd/nova-bus/main.go:178 | The first-run sentence says a Redis naming ada and bob. README.md:24 says the same. After those two names were added and no proof was written, send refused both as deaf and wrote nothing. | Say in that sentence and in the README row that each name needs a proof under ten minutes. The deaf rule is already the sentence above, at cmd/nova-bus/main.go:176. | S |
| 7 | cmd/nova-bus/main.go:239 | `send -h` says a dry run prints the line with no id. The run printed `SEND OK id=- ... dry_run=true`, because the id fact is set on every send (cmd/nova-bus/main.go:484). | Leave the id fact off the dry-run line. | S |
| 8 | docs/STANDARD.md:43 | The standard still says a rating is a note on the bus, "nova-bus, messages over git". This binary is Redis streams, and docs/SPEC-BUS.md:8 says the git bus was removed. | Name Redis streams, sent once and delivered until acked. | S |
| 9 | internal/tool/tool.go:621 | A missing flag, a deaf name, and an unknown name end `run: nova-bus help`. An unknown flag ends `run: nova-bus names -h` (internal/tool/tool.go:540). The verb page is the one with the flags. | Point a verb's refusal at `nova-bus <verb> -h`. | S |

## Good, keep

`nova-bus help` is one screen, and `help <verb>` matches `<verb> -h`.

The documented first run matches docs/TESTS.md once the store is the one that page describes: empty wait, send with the body's sha256, peek, recv through `--exec`, ack of an id that is not pending, log, names. `--stdin` keeps the trailing newline and the digest matches the file.

A deaf name is refused for every party at once, with the age and the remedy, and nothing is written. A proof seventeen minutes old says `past 10m0s`. Peek and ack still run, so a deaf name can look and clean up. An address outside loopback and the tailnet is refused before a dial, and a missing store name does not fall through to a fleet row when that row cannot be read.

`send --dry-run` writes no key. `log --max 1` prints `LOG MORE` and the total. `recv --exec false` names the id and leaves it pending. A pending message idle past fifteen minutes is the next recv. `--re` clears that one owed field. `PING` is skipped and the cursor moves past it. `--wake-file` returns `WAIT WAKE` with the line.

## Compared with earlier ratings

The earlier Johnny ratings are the git bus, docs/ratings/1.1.0/johnny/bus-read.md (READ 6.5/10) and bus-use.md (USE 7/10), build 2c02b2aa2042. This binary is the Redis bus. docs/SPEC-BUS.md:8 says it took the name on 2026-10-04, when the git bus was removed. Those findings do not describe this code.

| earlier | now | evidence |
|---|---|---|
| help is a memoir through cmd/nova-bus/main.go:331 | GONE | `nova-bus help` is one screen; `wait -h` is 31 lines and matches `help wait` |
| inbox reads a stale checkout as empty, exit 0 | GONE in that form | recv and wait read the store; a proof seventeen minutes old is refused with the age, and nothing is written |
| a quiet empty inbox | STILL, a new shape | `peek --as zed` prints `PEEK OK pending=0 new=0`, exit 0; a second recv prints `RECV NONE: nothing for bob` while peek shows `pending=1` |
| `--beat` and `--beat-lease` still declared | GONE | `wait -h` does not list them |
| two refusal sentences, and the remedy opens the whole banner | CHANGED | one `REFUSED` line; a bad input says `run: nova-bus help`, an unknown flag says `run: nova-bus <verb> -h` |
| the push sentence is larger than the retry | GONE | there is no git push; a rejected address is refused before a dial |
