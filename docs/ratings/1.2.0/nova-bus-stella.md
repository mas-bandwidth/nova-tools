# nova-bus READ and USE rating, nova-tools 1.2.0

Rater: Claude Opus 5.5 in Claude Code, a sprint worker on a friend's re-rate card
Build: 7a61cf1d1ff9
READ: 7.5/10
USE: 6.5/10

No v1.2.0 tag exists on the forge yet, so this rates the release candidate at the head of sprint/mechanical-2026-10-02. `nova-bus version` prints `nova-bus v1.0.1-0.20261006144045-7a61cf1d1ff9 linux/amd64 go1.26.6`. Built and run on a Linux bench machine. The store was a throwaway Redis made for the trial inside the job directory, listening on a Unix socket only (no TCP port), with no persistence, and stopped at the end. No live store was touched.

## Reasons
READ. `nova-bus help` and every verb's `-h` were read cold, then docs/SPEC-BUS.md (320 lines). The help is short and good. It opens with the loop a harness runs, names the streams and the log, and says that send and recv refuse a deaf name. Each verb's `-h` states its output line, its refusals and its JSON. The spec is clear about at-least-once delivery, the fifteen-minute hold, the push proof and the identity, and it says plainly what the ACL cannot stop. The first place of confusion is the help's "first run: a Redis naming ada and bob" (cmd/nova-bus/main.go:178). It never says how a store comes to name them: the sets `friends` and `machines` and a proof on `bus2:push`, which only nova-config and a friend daemon write. So the example's `send` line cannot work on a fresh store. The first place of doubting a claim is the shared help line "A verb that lists takes --max <n> (default 20, 0 lists all)" (pkg/tool/tool.go:512). `peek` lists and refuses `--max`, and `recv --max` means "take n", not a listing ceiling. `wait --json` is documented with a different shape from every other verb's JSON. The `wait` cursor is a stream id while every message id is a ULID, and the refusal explains this well when the two are mixed up.

USE. Used for real on the throwaway store. I made the roster by hand (`SADD friends ada bob`, `SADD machines m1`) and wrote push proofs by hand on `bus2:push` (proven, down and stale). I ran send (with `--cc`, `--stdin`, `--re`, `--kind`, `--dry-run` and `--json`), peek, recv (plain, `--exec`, `--max`, `--all --ack`, `--kind`, `--dry-run`, and `--forever` stopped by SIGTERM), ack (with `--dry-run`, and twice), wait (`--after`, `--timeout`, `--wake-file`, the default PING skip, `--json`), log (`--bodies` and `--max`) and names. I also tried a login user made with ACL SETUSER, with its password from an env var, a wrong password, and spoofing `--as`. About 25 refusals were provoked. The core delivery is right: a message on every stream or none, a body's bytes and sha256 on SEND OK, a trailing newline kept, `--exec` acking on exit 0 and leaving a failure pending, `--forever` delivering in order, an idempotent ack, deaf, stale and down names refused with the remedy, a login spoof refused, and a non-tailnet address refused before any dial.

The score is held at 6.5 by three things a cold AI meets.
- Every refusal that comes before a verb reads its dry-run flag is replaced by `FAILED: --dry-run was given and the verb never read it (Call.DryRun); it may have written`, at exit 1. That covers a dead store, a bad `--kind` and an `--as` that is not the login. The real reason is lost, and the caller is told a write may have happened when none did.
- A plain recv that printed a message and was not acked, or an `--exec` that failed, holds that message for fifteen minutes. During that time `recv` says only `RECV NONE: nothing for bob`, while `peek` shows `pending=2`.
- The help's own example cannot be run cold. Making the roster and the push proof takes another tool's daemon, or raw Redis commands that the help never names.

A 10 needs:
- refusals kept as refusals under `--dry-run`;
- RECV NONE and RECV FAILED naming the held messages and when they come back;
- a first-run path, or a trial mode, that a cold AI can follow from the help;
- one JSON shape for every verb;
- every problem with a send named in one pass.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | pkg/tool/tool.go:579 | under `--dry-run`, any refusal returned before the verb calls `c.DryRun()` becomes `FAILED: --dry-run was given and the verb never read it ... it may have written` at exit 1: `send --dry-run --redis 127.0.0.1:1`, `recv --dry-run --kind bogus`, and `recv`/`ack --dry-run --as <not the login>` all print it; the real reason is lost and a write is wrongly suggested | keep a Refused outcome as it is, and fail only when the verb returned OK without reading the dry-run flag; or read `c.DryRun()` first in send, recv and ack (cmd/nova-bus/main.go:454, :532, :652) | S |
| 2 | cmd/nova-bus/main.go:577 | `RECV NONE: nothing for bob` at exit 1 while `peek` shows `pending=2`: messages printed by a plain recv, or left by a failed `--exec`, are held fifteen minutes and recv says nothing of them | print `held=<n> next_claim=<RFC3339>` on RECV NONE when pending entries exist, and say so in recv -h | S |
| 3 | cmd/nova-bus/main.go:600 | `RECV FAILED ... so the message stays pending` does not say the message cannot be received again for fifteen minutes (ClaimAfter); `recv --all --ack` right after it skipped that message | add `held_until=<time>` to the line, and name `ack --id` as the way to clear it | S |
| 4 | cmd/nova-bus/main.go:178 | "first run: a Redis naming ada and bob" never says how a store names them (the sets friends and machines) or how a name gets proven (bus2:push, written only by a friend daemon), so the help's example `send` is refused as deaf on any trial store | name the keys and the nova-config and nova-friend lines in the first-run line, or ship a trial roster and proof verb for a store with no users | M |
| 5 | pkg/bus/bus.go:333 | send names problems in two passes: with `--to bob,zed,Bad_Name --body ""` it names Bad_Name and the empty body but not the unknown zed, which appears on the next run; an empty `--subject` alone stops at the flag check (cmd/nova-bus/main.go:244) before any other problem | read the roster in the first pass and add unknown names to the same problem list; let the bus check own the subject | S |
| 6 | pkg/bus/bus.go:507 | `peek --as zed` (not on the roster) answers `PEEK OK pending=0 new=0` at exit 0, though the spec says a name the roster does not hold is refused; `ack --as zed` is the same | refuse an unknown name in peek and ack as send, recv and wait do | S |
| 7 | cmd/nova-bus/main.go:549 | `recv --as zed --dry-run` for a name not on the roster is refused as `deaf: zed ...` with the friend-daemon remedy, not as an unknown name with the nova-config remedy | check the roster before the push proof in the dry-run path | S |
| 8 | pkg/tool/tool.go:512 | the help says "A verb that lists takes --max <n> (default 20, 0 lists all)", but `peek` lists and refuses `--max`, and `recv --max` means "take n" and refuses 0 | say only log takes the listing --max, or give peek the listing --max | S |
| 9 | cmd/nova-bus/main.go:828 | `wait --json` prints `{"status":"ok","word":"NONE",...}` at exit 1, while every other verb prints `{"result":{"verb":..,"status":..,"exit":..},"facts":..,"items":..}` | render wait through the shared result value, so status and exit agree | M |
| 10 | pkg/bus/bus.go:301 | `send --re nonsense` is accepted and stored; `--re` is not checked as a ULID or against the log | refuse a --re that is not a ULID, and note when it is not on the log | S |
| 11 | pkg/bus/bus.go:570 | the default `--skip-subject PING,PONG` is a prefix match, so a real message whose subject starts with "ping" (e.g. "ping2", "pinging about the deploy") is skipped silently and only moves the cursor | match the whole first word, or print a skipped count on WAIT OK and WAIT NONE | S |
| 12 | cmd/nova-bus/main.go:715 | `log --max 2` shows the two oldest messages, then MORE; an AI checking recent traffic must list everything | add `--since <id>` or `--tail <n>`, or list newest first under --max | S |
| 13 | cmd/nova-bus/main.go:1002 | `WAIT MESSAGE ... subject=hello` is unquoted, while RECV, PEEK and LOG print `subject="hello"` | print the subject with tool.Text as the other verbs do | S |
| 14 | pkg/bus/pushproof.go:90 | a name written down 0s ago reads `deaf: m1 has no proven push since 0s`, which says the opposite of what happened | say `push down since <age>` for a down proof, and keep `since` for stale and none | S |

## Good, keep
The three-line loop at the top of help, and `effect:` on every verb saying whether it reads, writes or delivers.
SEND OK carrying the body's bytes and sha256 as the store holds it, and the trailing newline kept end to end.
Refusals that name the rule and the remedy: the tailnet-only address check before any dial, the login spoof (`drop --as, or log in as bob`), the wrong password naming the env var and never the secret, and the `--after` refusal explaining stream ids versus ULIDs.
recv's flag-combination refusals (`--forever` without `--exec`, `--ack` with `--exec`, `--max` with `--all`), each saying why.
`--json` on every verb, with refusals as a JSON `why` list.
ack being idempotent with a per-id `acked=true|false`, and `--dry-run` on ack showing what is pending.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| no verb offers --json (1.1.0 bus-use #1, bus-read #2) | FIXED | `nova-bus names --json` and `peek --json` print the shared result object; `send --json` prints refusals as `why` |
| inbox reads a stale checkout / --advance cursor behind (1.1.0 bus-use #2) | GONE | the git bus was removed on 2026-10-04 (docs/SPEC-BUS.md:7); the Redis bus has no checkout |
| receipt --note path scope (1.1.0 bus-use #3) | GONE | no receipt verb or note path; ack takes ids |
| help example not runnable cold (1.1.0 bus-use #4) | STILL THERE | on a fresh store the example's send is refused: first `ada is no known name`, then `deaf: ada has no proven push since never` (finding 4) |
| --host prints as `<host=>` (1.1.0 bus-use #5) | FIXED | every verb's -h prints `--redis <string>  the Redis address, host:port` |
| main.go 3823 lines, 467-line inboxListing (1.1.0 bus-read) | FIXED | cmd/nova-bus/main.go is 1007 lines, on pkg/tool |
| retired --beat flags still declared (1.1.0 bus-read) | FIXED | `nova-bus wait -h` lists its flags and no --beat or --beat-lease is among them |
| REFUSED printed at exit 1 (1.1.0 bus-read) | FIXED | every REFUSED seen exits 2; but see finding 1 for FAILED at exit 1 under --dry-run |
| not every missing input named on the first run (1.1.0 bus-use, send) | STILL THERE | finding 5 |
