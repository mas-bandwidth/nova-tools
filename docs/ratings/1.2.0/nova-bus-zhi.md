# nova-bus READ and USE rating, nova-tools 1.2.0

Rater: deepseek/deepseek-v4 in dsh, the DeepSeek Harness headless runner
Build: 169a7eef0e30
READ: 8/10
USE: 7/10

No v1.2.0 tag is on the forge, so this rates the release candidate at the head of the
sprint base `169a7eef0e30`, read cold from `nova-bus help`, every verb's `-h`,
docs/SPEC-BUS.md and cmd/nova-bus, then built with go1.26.6 linux/amd64 and used for
real against a throwaway Redis on a high loopback port in the job's scratch directory
(stopped with `shutdown nosave`; no live store was touched, no server ran on the
workstation). The used half: send, recv, ack, peek, log, names, receipts, overdue,
wait, the token retry, the three dry runs, and the refusals of a dead store, a refused
address, a bad flag, a bad name and a bad kind.

## Reasons

READ. The banner answers the three questions in order: one line of what (the same
sentence as the README row), five lines of how (the loop a harness runs, the deaf-name
rule, the streams and the log, where a first run's store comes from), and an `example:`
block of seven lines. The tool is a full member of the family: dispatch, banner, help,
refusals and the output envelope are pkg/tool's (cmd/nova-bus/main.go:8), every
verb takes `--json`, the listing verbs take `--max` with a `MORE` line, and every `-h`
carries usage, the banner excerpt, every flag, an exit table and an `effect:` line.
SPEC-BUS.md is the spec a cold reader wants: the keys, at-least-once delivery said
plainly, the token-retry rule, the push-proof gate, the receipts, the ACL table and a
round-trip budget per verb; where spec and code meet they agree. The 1.1.0 READ findings
are largely answered — main.go is 1,159 lines on the shared skeleton instead of 3,823
hand-rolled ones, `REFUSED` no longer rides exit 1, the retired `--beat` flags are gone
with the git bus, and SPEC-BUS.md is now the one spec of a tool that exists.

What keeps READ at 8. The exit table is told wrong on most pages: every `-h` quotes the
tool-wide table whose exit 1 names `recv` and `wait` outcomes, and that parenthetical is
printed unchanged on send, peek, ack, log, names, receipts and version, seven verbs that
can never produce them (cmd/nova-bus/main.go:179); wait and overdue each carry a table of
their own, so three shapes live in one tool. overdue's own table says `1 BUS OVERDUE`
while the line it prints is `OVERDUE OVERDUE count=<n>` (cmd/nova-bus/main.go:388). The
banner promises a listing verb takes `--max`, but peek and names refuse it
(pkg/tool/tool.go:522). `version -h` alone has no `example:` line
(pkg/tool/tool.go:450). The store address is told three ways: the banner's first-run
line says "(else NOVA_BUS_REDIS)" and stops, eight flags add "else the fleet row's bus
from the sprint store", and wait's drops "from the sprint store"
(cmd/nova-bus/main.go:212). And `--kind`'s help names the five kinds but not its default,
which only the spec states (cmd/nova-bus/main.go:254).

USE. Everything that could run ran, and the refusal grammar is the best part of the tool,
all of it witnessed: a bare `nova-bus` and an unknown verb name all ten verbs and point at
`nova-bus help`; an unknown flag names the flags there are and points at that verb's page
(`run: nova-bus send -h`); a missing required flag is named with what it wants and
`refusing to guess`, both missing flags in one run; every bad value says the shape it
wants (`--after` wants `<ms>-<seq>`; `--timeout -1s` wants `at least 0, 0 for ever`;
`--max 0` wants `a count of at least 1`); `--forever` without `--exec` and `--ack` with
`--exec` are each refused with the reason. The address policy holds before any dial: a
public address is refused naming the rule and the address, an unresolvable name says so,
and a relative socket path is refused with the two forms it accepts. A dead store answers
one line with the address, the login, the cause and the next step; `--json` renders the
same refusal as one object; an empty password variable is refused before the dial. Exit
codes held everywhere I looked: 0 on help, version and every success line, 2 on every
refusal, 1 on `wait --timeout` and on `overdue --older 0s`. The live half worked exactly
as the spec says: a send fanned out to a group and to the log in one transaction, recv
took and held, ack was idempotent, the token retry printed the first send's id byte for
byte and refused changed arguments, receipts moved delivered, `--dry-run` on send wrote
nothing (the log length was unchanged), and `--max` said MORE with the total.

What keeps USE at 7. The declared dry runs are the worst answer the tool gives. A
`--dry-run` on send, recv or ack against a store that does not answer prints
`SEND FAILED: --dry-run was given and the verb never read it (Call.DryRun); it may have written` at exit 1, witnessed against a dead loopback port: a run that wrote nothing and
never dialled is told it may have written, at the exit code that means the verb ran and
said no, and the real reason (the store, the address) is dropped
(pkg/tool/tool.go:587). Nothing beyond help and version runs without a store, dry
runs included, so a cold reader cannot try one core line — no SEND draft, no RECV plan —
before adopting the tool. The message checks sit behind the dial: on a dead store,
`send --kind bogus`, `send --to "BAD NAME"`, `send --body ""` and `recv --kind bogus` all
answer the store's error instead of the flag's, so one run does not name every problem it
can find. An unknown name is believed by two verbs: `peek --as nobody` prints
`PEEK OK pending=0 new=0` and `ack --as nobody --id 0-0` prints `ACK OK acked=0` at exit
0, while send, recv and wait refuse the same name. `--re` is not checked (`send --re not-an-id` is SEND OK and the log stores it) and neither is `--id` (`ack --id to`
prints `ACK ID id=to acked=false`), a subject with an embedded newline is accepted, the
text `WAIT NONE` goes to stderr while the JSON NONE goes to stdout, and `wait --json` is
a bare object with `"status":"ok"` at exit 1 where every other verb prints the one
envelope.

A 10 needs: the dry runs to answer a store that is down with the store's own refusal and
no false write warning; a store-free dry-run path that checks and prints the message
alone; the kind, name and body checks moved before the dial; peek and ack checking the
roster; `--re` and `--id` validating their shape; one exit table per verb; the overdue
table written as the line prints it; the wait text result on the same stream as its other
lines and its JSON through the one envelope; and one wording for the store address.

## Findings

| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | pkg/tool/tool.go:587 | a `--dry-run` whose verb refused before it read the flag (a store that did not answer) is replaced by `SEND FAILED: --dry-run was given and the verb never read it (Call.DryRun); it may have written` at exit 1, for a run that wrote nothing and never dialled, and the store's own refusal is lost; the tripwire fires because send, recv and ack read `c.DryRun()` only after `w.bus` has dialled (cmd/nova-bus/main.go:564, :641, :759) | read `c.DryRun()` before the store opens, or answer a refusal that is already a refusal before the tripwire | M |
| 2 | cmd/nova-bus/main.go:549 | no verb but version and help runs without a store, dry runs included: every core verb opens the store first, so a cold reader cannot try one real line before adopting the tool | give the dry runs a store-free form that checks and prints the message alone, `dialled=0 written=0`, as a sibling tool's dry run does | M |
| 3 | cmd/nova-bus/main.go:638 | the message checks sit behind the dial: on a store that does not answer, `--kind bogus`, `--to "BAD NAME"` and an empty `--body` answer the store's error, not the flag's, so one run does not name every problem it can find | judge kinds, the name's shape and the body in the flag Check at parse time, before the store opens | S |
| 4 | cmd/nova-bus/main.go:179 | the tool-wide exit table names recv and wait outcomes, and send, peek, ack, log, names, receipts and version quote it verbatim in `-h`, so seven pages promise exit-1 outcomes their verb cannot produce | give every verb its own ExitTable and print the tool's only where it is true | S |
| 5 | cmd/nova-bus/main.go:388 | overdue's exit table says `1 BUS OVERDUE`; the line the verb prints at exit 1 is `OVERDUE OVERDUE count=<n> older=<d>` (cmd/nova-bus/main.go:908) | write the table as the line prints it | S |
| 6 | cmd/nova-bus/main.go:1126 | the text `WAIT NONE` result is printed on stderr while `WAIT ARMED` and the `WAIT OK` lines go to stdout, so a caller that captures stdout sees the arming line but not the terminal line; `wait --json` puts its NONE on stdout | print the text result on the same stream as the wait's other lines, or state the split where the grammar is stated | S |
| 7 | cmd/nova-bus/main.go:1122 | `wait --json` is a bare object with `"status":"ok"` at exit 1, while every other verb renders the one envelope `{"result":{...},"facts":...}`; a consumer that parses the envelope breaks on wait | render wait through the one envelope, with status none at exit 1 | M |
| 8 | pkg/tool/tool.go:522 | the banner says a verb that lists takes `--max <n>` and says MORE, but `peek --max 1` and `names --max 1` are unknown flag, and both verbs list | add `--max` and a MORE line to peek and names, or name the verbs that take it | S |
| 9 | cmd/nova-bus/main.go:678 | `recv --json` carries `payload":"\nbody"`: the text form's blank separator is inside the payload, so every JSON body gains a leading newline the message does not have | carry the body alone in the payload | S |
| 10 | cmd/nova-bus/main.go:716 | `recv --all --json` prints one JSON object per message, not the one object the banner promises, so a consumer of stdout gets a concatenated stream | wrap the batch in one object with items, or say a batch prints one object per line | M |
| 11 | cmd/nova-bus/main.go:779 | an unknown name is believed: `peek --as nobody` prints `PEEK OK pending=0 new=0` and `ack --as nobody --id 0-0` prints `ACK OK acked=0` at exit 0, while send, recv and wait refuse the same name | read the roster in peek and ack (one SMEMBERS) and refuse as recv does | S |
| 12 | cmd/nova-bus/main.go:253 | `--re` is not checked: `send --re not-an-id` prints SEND OK and the log stores `re=not-an-id` | refuse a `--re` that is not a message id, naming the shape it wants | S |
| 13 | cmd/nova-bus/main.go:355 | ack accepts any word as an id: `ack --id to` prints `ACK ID id=to acked=false` at exit 0, the same line a real id already acked gets | refuse an `--id` that is not an entry id, naming the bad id | S |
| 14 | cmd/nova-bus/main.go:250 | a subject with an embedded newline is accepted and stored as `two\nlines`, against the flag's one line | refuse a subject holding a newline or another control character | S |
| 15 | pkg/bus/pushproof.go:90 | a proof written now with `up:false` reads `deaf: <name> has no proven push since 1s`, which says it was proven a moment ago, and an empty reason prints `()` | say `since never` when the name was never up, and name the daemon's reason when there is one | S |
| 16 | cmd/nova-bus/main.go:254 | `--kind`'s help names the five kinds but not its default; an absent `kind=` is a status, which only the spec states | add `(default: status)` to the flag's help | S |
| 17 | cmd/nova-bus/main.go:212 | the store address is told three ways: the banner's first-run line says "(else NOVA_BUS_REDIS)" and stops, eight flags say "else the fleet row's bus from the sprint store", and wait's drops "from the sprint store" | state the chain once, the same words everywhere | S |
| 18 | cmd/nova-bus/main.go:293 | recv and ack print `effect: delivery: sends beyond this machine`, though neither sends anything beyond this machine (recv moves a message to pending; ack clears it) | give recv and ack a store-write effect line that says what each writes | S |
| 19 | pkg/tool/tool.go:450 | `version -h` has no `example:` line, and `version --json` prints `"facts":{}` with the version only as free text, where the release's own metadata would fit facts | give version the example the other verbs have, and render its identity as facts | S |

## Good, keep

The refusal grammar, all of it witnessed: every problem of a run named at once, each with
what the input wants and `refusing to guess`; an unknown verb answered with all ten names;
an unknown flag answered with the flags there are and the verb's own page. The address
policy enforced before any dial, one line naming the rule, the address and what it
resolved to. The dead-store line that names the address, the login, the cause and the next
step, and the empty-password refusal that names the variable to export before anything
dials. `--json` on every verb rendering the same refusal as one object. The token-retry
contract in send's help where the caller meets it, and its live proof: the second send
returned the first send's `id` and `at` byte for byte, and changed arguments were refused.
The banner and README saying the same one sentence. The bounded listing that keeps its
total. The three dry runs that write nothing when the store answers.

## Compared with earlier ratings

| earlier | now | evidence |
|---|---|---|
| main.go 3,823 lines, hand-rolled dispatch, not one family skeleton (READ 1.1.0) | FIXED | cmd/nova-bus/main.go is 1,159 lines on pkg/tool; banner, help, refusals and the envelope are the skeleton's |
| `REFUSED` printed at exit 1 (READ 1.1.0) | FIXED | every refusal here exits 2; the exit-1 lines left are the dry-run tripwire (finding 1) and wait's NONE |
| retired `--beat`, `--beat-lease` still declared (READ 1.1.0) | FIXED | the git bus is removed and its flags with it; the tool is the Redis bus (docs/SPEC-BUS.md:1-8) |
| one contract split across three SPEC-BUS files, one saying "not implemented" (READ 1.1.0) | FIXED | SPEC-BUS.md is the one spec of a tool that exists and matches the verbs |
| no `--json` on any verb (USE 1.1.0) | FIXED | every verb takes `--json`; a refusal renders as one object of the same value |
| a read that silently missed notes already pushed (USE 1.1.0) | NOT RE-SEEN | the git walk is gone with the git bus; the store reads need a live store, and the throwaway one behaved as the spec says |
| `--bodies --advance` does not advance (USE 1.1.0) | NOT RE-SEEN | the flags are gone with the git bus |
| the receipt path scope undocumented (USE 1.1.0) | NOT RE-SEEN | receipts are a verb of their own; the id path is exact and the happy path needs a live store |
| the prose promising more than bounded retries deliver (READ 1.1.0) | NOT RE-SEEN | the claim rode the git bus's push wording, which is gone; the token section states what a retry answers |
