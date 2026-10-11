# nova-redis READ and USE rating, nova-tools 1.2.0

Rater: z-ai/glm-5.3-flashx in opencode (a friend's re-rate card)
Build: 19f9370930b6
READ: 8/10
USE: 8/10

Rated cold from `nova-redis help`, the `-h` of every group and verb, and
docs/SPEC-REDIS.md, on the release candidate at the head of the rework branch
built from source (`nova-redis v1.0.1-0.20261007230721-19f9370930b6 linux/amd64 go1.26.6`). No store was started and no live store was dialled: every
store-free path ran for real (version, acl render, every `--dry-run`, every
refusal class), and every dialling verb was provoked against a dead loopback
port and a missing socket path, which nothing answers. This head's tree is the
content two earlier attempts of this card rated, and every finding those
ratings carried was re-verified line for line at this build; nothing was
dropped on trust.

## Reasons

READ. The banner answers the three onboarding questions before its usage
lines: what the tool is, how it works (serve runs the store on loopback or
tailnet addresses, spill and recall keep short-lived named values in it, fn
load and fn check manage the function library, the password is read from a
variable, never an argument), and where a first run starts — a throwaway
recipe that needs nothing but `redis-server` and `redis-cli` on PATH, written
as lines a reader can paste. Every verb's `-h` repeats its usage line, names
every flag with what it wants and its default, states its effect class and
quotes the exit table. The refusal grammar is the best part: a spill missing
owner, name, TTL and value reports all four problems in one run, each saying
what the input WANTS; an unknown flag names the verb's full flag set and the
nearest alternative; an unknown verb names every verb. `acl render` opens no
store and prints the key families and the `ACL SETUSER` lines, so the ACL can
be planned before any store exists. Passwords reach the tool only as variable
names, and the store's default user is seeded with the password's SHA-256.

What keeps READ at 8. The normative spec still teaches the deprecated
`--addr <host:port>` spelling only — never the current `--redis`, never the
Unix-socket form the binary and the banner's own recipe use — and its verb
block omits the four service verbs the binary ships (findings 6, 7). The
banner's example block uses `--addr`, and its second spill line writes a real
store on Redis's own default port if pasted (finding 3). `serve -h`'s example
is the literal `version`, so the block shows no serve line at all (finding 4).
The top help promises `--max` for a listing verb, and no verb here lists
(finding 8); five verbs carry an empty or placeholder example (finding 9); and
`version --json` hides the version in one unparsed `payload` string with empty
`facts` (finding 11).

USE. Everything that needs no store ran for real and behaved: `spill --dry-run` prints the key, TTL, expiry, bytes, store and `written=0` as lines
and as one JSON object of the same facts, dialling nothing; `acl apply --dry-run` prints four `ACL WOULD-SET` lines and `ACL APPLY OK dry-run=true users=4 set=0 would=4` at exit 0 with nothing listening; `serve --dry-run`
validates the binding, port and absolute `--dir` shape and refuses a wildcard
bind and a relative dir in one run, both named, nothing created; `install store --dry-run` prints the whole unit and writes nothing; `uninstall store --dry-run` names the unit and touches nothing. Refusals are clean at exit 2
before any dial: a missing `--value`, a bad owner prefix, a zero TTL, a
missing address, each named with its reason. A dead store is answered as
`unreachable` with the dial error in the line (spill, recall), exit 2, and
spill and recall pick the remedy by cause: start the store or correct the
address.

What keeps USE at 8. `install store --dry-run` (and `install bus`) with a
login flag missing reaches the login refusal before the dry-run branch, and
the skeleton then answers `INSTALL-STORE FAILED: --dry-run was given and the verb never read it; it may have written` at exit 1 — FAILED and a false
may-have-written on a dry run that wrote nothing, and the wrong exit code
(finding 1). A missing address is refused as `--addr is required`, naming the
deprecated flag the reader was never taught here (finding 2). `nova-redis "fn load" -h` answers `unknown verb "fn load"; did you mean fn load?`, a
remedy identical to the input (finding 5). `acl check` and `acl apply` answer
an unreachable store with a login remedy, sending the reader to fix a login
that was never tried (finding 14). The inspection verbs whose answers an AI
acts on (`fn check`, `acl check`, `acl render`, `serve`) take no `--json`
(finding 12); `recall` and `fn load` take no `--dry-run` (finding 13); and
`spill -h`, `fn load -h` and `acl apply -h` call their store writes `local write: writes files on this machine`, misstating the blast radius (finding
10).

## Findings

| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | cmd/nova-redis/install.go:176,203 | `install store --dry-run` (and `install bus`) with any of `--secrets`, `--as`, `--key`, `--sops`, `--secret` missing returns the login refusal at install.go:176 before `c.DryRun()` is read at install.go:203, so the skeleton answers `INSTALL-STORE FAILED: --dry-run was given and the verb never read it (Call.DryRun); it may have written` at exit 1: a refusal rendered FAILED, the wrong exit code, and a false may-have-written on a dry run that wrote nothing | call `c.DryRun()` at the top of the install and uninstall runners, before any refusal, so a dry run's refusals stay clean REFUSED lines at exit 2 | M |
| 2 | cmd/nova-redis/main.go:325 | a missing address is refused as `--addr is required: the store's address as <host:port> ...`, naming the deprecated `--addr` rather than `--redis` and teaching only the TCP form, while the usage, the flag list and the family's one shape say `--redis` with the socket form; the reader must learn the alias to retry (the wording is pinned by docs/TESTS.md) | name `--redis` and `<host:port> or the absolute path of a Unix socket` in the refusal, and move the pin with it | S |
| 3 | cmd/nova-redis/main.go:156,192 | the spill examples and the banner's example block use `--addr`; the block's second spill line writes a real store on Redis's own default port, so a reader who copies the first run spills into whatever answers there | use `--redis` in the examples and make the real write the throwaway Unix-socket recipe the banner's usage note already carries | S |
| 4 | cmd/nova-redis/serve.go:98 | the serve verb's `Example` is the string `version`, so the banner's example block prints `nova-redis version` and shows no serve line at all | replace it with a runnable plan such as `serve --dry-run --bind 127.0.0.1 --port 6390 --dir /tmp/nova-redis-store` | S |
| 5 | pkg/nsprint/verbflag/verbflag.go:111 | `nova-redis "fn load" -h` answers `unknown verb "fn load"; did you mean fn load?`, a remedy identical to the input that never says the verb is two arguments, so a reader who quotes the verb is sent in a circle | say the verb is two words and give the runnable form, or match the joined spelling to the verb | S |
| 6 | docs/SPEC-REDIS.md:18-28 | the spec's verb block lists serve, spill, recall, the fn verbs, the acl verbs, version and help, and omits `install store`, `install bus`, `uninstall store` and `uninstall bus`, which the binary ships and docs/CLI.md documents | add the four service verbs with their effects and dry-run behaviour | S |
| 7 | docs/SPEC-REDIS.md:19-25 | the spec's usage lines teach `--addr <host:port>` only: never the current `--redis`, never the Unix-socket form the binary accepts and the banner's throwaway recipe uses, so a reader of the normative document learns the deprecated spelling and the wrong address shape | write `--redis` first, note `--addr` as the one-release alias, and say `<host:port> or the absolute path of a Unix socket` | S |
| 8 | pkg/tool/tool.go:522 | the top help says a verb that lists takes `--max`; no nova-redis verb lists, and `recall --max 5` is refused as an unknown flag after the help sent the reader after a flag that exists nowhere here | print the `--max` sentence only for a tool that has a listing verb | S |
| 9 | cmd/nova-redis/fn.go:62,77; cmd/nova-redis/acl.go:181,195,210 | `fn load`, `fn check`, `acl check` and `acl apply` declare an empty `Example`, so their `-h` shows no first command though each has a natural one; `install store -h`'s own example (install.go:69) is angle-bracket placeholders that do not run | add one runnable example each, using `--redis` and the `--dry-run` where the verb has it, and make the install example concrete | S |
| 10 | cmd/nova-redis/main.go:157; cmd/nova-redis/fn.go:63; cmd/nova-redis/acl.go:211 | `spill`, `fn load` and `acl apply` say `effect: local write: writes files on this machine`, but each writes the store at `--redis`, which may be another machine, and none writes a local file; an AI judging blast radius from the effect line misjudges it | add a store-write effect class to `pkg/tool` and use it on the three verbs | S |
| 11 | pkg/tool/tool.go:452 | `version --json` prints the version, OS, arch and Go version as one unparsed string in `payload` with empty `facts`, so a consumer must parse the string | render them as individual facts (`version`, `os`, `arch`, `go`) and keep the line | S |
| 12 | cmd/nova-redis/fn.go:80; cmd/nova-redis/acl.go:182,196; cmd/nova-redis/serve.go:99 | `fn check`, `acl check`, `acl render` and `serve` refuse `--json`, so the inspection verbs whose answers an AI acts on are prose only, while spill, recall and version give one object | give every verb `--json` through the shared result value | M |
| 13 | cmd/nova-redis/main.go:188; cmd/nova-redis/fn.go:58 | `recall` and `fn load` take no `--dry-run`, so the read path and the library deploy cannot be planned store-free, unlike spill, serve, acl apply and the install verbs | add `--dry-run` printing the key or the library digest, the store and the login each would use, dialling nothing | M |
| 14 | cmd/nova-redis/acl.go:291 | `acl check` and `acl apply` answer an unreachable store with `remedy=log in as a user that may run ACL GETUSER, ACL CAT and ACL SETUSER ...`, sending the reader to fix a login that was never tried, where spill, recall and fn check choose the remedy by cause | choose the remedy by cause as fn.go does: unreachable gets the address remedy, a refused login the login one | S |

## Good, keep

The store-free surface is real and wide: `spill --dry-run`, `acl apply --dry-run`, `serve --dry-run`, `install store/bus --dry-run` and `uninstall store/bus --dry-run` each print the plan the real run would take, from the
same code path, and change nothing — and `acl render` and `version` need no
store at all. One run names every independent problem, with what each input
wants: a spill missing four inputs reports four reasons in one line group,
`serve --dry-run` refuses a wildcard bind and a relative dir together. Exit
codes tell the truth: 0 on every plan, 2 on every refusal and every dead
store. Passwords never appear in arguments or output — the login flags take
variable names, and the store's default user is seeded with the password's
SHA-256. The bind validation accepts loopback and tailnet addresses only, and
says so in the refusal.

## Compared with earlier ratings

| earlier | now | evidence |
|---|---|---|
| `acl apply --dry-run` dialled the store and said it may have written (1.1.0 USE) | FIXED | `acl apply --dry-run` at a dead port printed four `ACL WOULD-SET` lines and `ACL APPLY OK dry-run=true users=4 set=0 would=4`, exit 0, nothing listening |
| `--addr` refused the Unix-socket form the throwaway recipe makes (1.1.0 USE) | FIXED, undocumented | `spill --dry-run --redis <dir>/redis.sock` printed the plan and dialled nothing; the flag help says `or the absolute path of a Unix socket`, the spec still says `<host:port>` (finding 7) |
| the spec's verb block omitted the ACL verbs (1.1.0 READ) | FIXED | docs/SPEC-REDIS.md:24-25 names acl render, check and apply; the service verbs are still omitted (finding 6) |
| `--max` promised for a verb that lists (1.1.0 USE) | STILL THERE | finding 8 |
| `"fn load"` quoted gives a self-referential remedy (1.1.0 USE) | STILL THERE | finding 5 |
| `recall` has no `--dry-run` (1.1.0 USE) | STILL THERE | finding 13 |
| the inspection verbs an AI acts on take no `--json` (1.1.0 READ) | STILL THERE | finding 12 |
| this card at e174cc87f042: 8 findings, READ 8/10 USE 8/10 | ALL 8 REPRODUCE here, and this pass adds the install dry-run FAILED line, the `--addr`-naming refusal, the `version` serve example, the quoted-verb remedy, the misstated effect class and the login remedy on an unreachable store | every finding re-verified at this head, none taken on trust |
| this card at 0eee1142d7d4: 14 findings, READ 8/10 USE 8/10 | ALL 14 REPRODUCE at this head | the tree content is identical and every line citation was re-checked here |
| the two sibling 1.2.0 ratings of this tool (at b250530c1ab3 and 169a7eef0e30) scored 8/8 and 8/8 | 8/8 here | three raters of this content land on the same pair of scores; the defect set is stable across builds |
