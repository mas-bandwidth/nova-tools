# nova-secrets READ and USE rating, nova-tools 1.2.0

Rater: Zhi (deepseek/deepseek-v4.1-flash, dsh), a one-shot on a friend's re-rate card
Build: 81b318639963
READ: 6.5/10
USE: 5.5/10

No v1.2.0 tag exists on the forge yet, so this rates the release candidate at the head of
sprint/mechanical-2026-10-02. `nova-secrets version` prints `nova-secrets v1.0.1-0.20261007220135-81b318639963 linux/amd64 go1.26.6`. Built and run on a Linux
bench with real sops 3.13.3 and age-keygen 1.3.2, with HOME, the store and its bare remote
made under a throwaway directory; no live store, no server. Every verb ran: keygen, seal
(`--stdin --no-pr` and `--dry-run`), seat add, seat inject (`--no-pr`), check, exec, names,
gate, place (`--dry-run` and a stub ssh) and placed. No value was printed on any line. One
step was hand-made: the first seat file had to be written with `sops -e -i`, because the
banner's own `first value:` line cannot make one (finding 1).

## Reasons

READ. Line 1 of the banner and the README's row are the same sentence, and `how it works:`
names a store, a seat and a recovery key before the first verb. Every verb's `-h` carries
its usage, its `effect:` line (inspection, local write, store write, delivery), and its
flags with what each wants; `exec -h` and `check -h` carry the whole store prerequisite
with the three commands that repair it. Refusals are one grammar, `SECRETS <VERB> REFUSED: <why>; run: <next>`, and a run with several missing flags names them all at once (finding
13 is the one exception). The safety model reads true: `exec` puts only the `--only` values
in one child's environment, the child's exit status passes through, `exec`'s own refusals
are 125, and no verb prints a value.

What keeps READ at 6.5. The first run the banner prints does not run, and on a fresh store
it cannot: `seal` refuses an absent seat file, while `seat add` needs a source seat that
already holds values (finding 1). Every printed example names `/opt/homebrew/bin/sops` and
`/opt/homebrew/bin/age-keygen`, which the reader's own platform does not have (finding 2).
`--json` is advertised on `names` alone, so the one output value the standard promises as
lines or JSON exists for one verb (finding 5). The command reference opens on a gate
example, not a `### First run`, and describes a gate verdict word and exit the binary no
longer prints (findings 6 and 7). The spec still calls the tool about two hundred lines of
Go (finding 9) and the package doc still says four verbs (finding 8). The `--machines` flag
means two different registry shapes (finding 12). The exit paragraph under every verb
describes check and exec only (finding 11).

USE. Once a seat file exists the core is right. `exec` decrypted exactly the named values
into one child and printed `SECRETS EXEC OK as=ada keys=1 only=1 required=1 file=... head=... cmd=sh`; a name not in the file, a missing command and a wrong key each refused at 125.
`names` read two names with no key and `names --json` printed the same result as one object
with a `more` line when `--max 1` cut it. `check` found the store green (`SECRETS CHECK OK as=ada recipients=2 files=1 sealed=1 mine=1 foreign=0 clear=0 head=...`) and refused an
absent seat at exit 2. `seat add` re-sealed a value to a new seat, and the new seat's own
key opened it through `exec` after the hand commit. `seal --stdin --no-pr` wrote a new name
and printed the NOTE that exec does not see it yet with the push that carries it. `place --dry-run` printed the plan and ran no ssh, the real `place` put the value on ssh's stdin
and wrote a receipt, and `placed` listed it. `gate` approved the CLI reference's own gate
example and printed the line the document promises.

What keeps USE at 5.5. A new store never reaches a first value without a hand `sops -e -i`,
and the two verbs that name the next command for that state both name `seal`, which refuses
it (findings 1 and 3). `check` reports OK for a key that opens nothing at all (`mine=0`,
finding 4), against its own effect line. Only `names` renders JSON (finding 5), so the verb
an AI runs most cannot be parsed. `place` gives an unknown machine no remedy and `placed`
calls an explicitly absent receipts directory `OK count=0` (findings 13 and 14), and a bad
`--max` value is refused without the `run:` line every other refusal carries (finding 15).
`seal` writes progress lines to stderr in no grammar (finding 17).

A 10 needs a first run that reaches a first value on any operating system, a `check` that
ties `--key` to `--as`, JSON on every verb, one registry shape under `--machines`, a
command reference that opens with `### First run` and states the word and exit the binary
prints, and one output grammar on every stream.

## Findings

| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | cmd/nova-secrets/main.go:114 | The `first value:` line cannot run on the store `setup:` makes: `seal` refuses an absent seat file (`pkg/secrets/seal.go:200`; typed at `pkg/secrets/seal.go:31`), and `seat add` refuses a missing `--from` (`cmd/nova-secrets/seat.go`), a source seat that already holds values, which a fresh store has none of. The only way to a first value was a hand `sops -e -i ada.yaml`. | Let `seat add` with no `--from` (or `setup`) write an empty sealed file for a seat whose key the caller holds, and put that line in `setup:` and above `first value:`. | M |
| 2 | cmd/nova-secrets/main.go:116 | The `first value:` line and every `example:` line hard-code `/opt/homebrew/bin/sops` and `/opt/homebrew/bin/age-keygen`; on Linux the first run ends `SECRETS SEAL REFUSED: sops binary /opt/homebrew/bin/sops is absent or not executable; run: brew install sops`, while `setup:` itself says `command -v`. | Use `"$(command -v sops)"` and `"$(command -v age-keygen)"` in every printed line, as `setup:` does. | S |
| 3 | pkg/secrets/seatfile.go:190 | `names` and `check` on a store with no seat file say `seal writes a seat's first value: run: nova-secrets seal ...`; `seal` refuses that state (`pkg/secrets/seal.go:31`). The same file's second branch (`pkg/secrets/seatfile.go:193`) says `seal starts a new seat`. | Print one remedy that runs for that state, the verb from finding 1, from all three. | S |
| 4 | pkg/secrets/invariants.go:556 | `check --store ./secrets --as ada --key zed.key`, a key that is no recipient of any file and opens nothing, prints `SECRETS CHECK OK as=ada ... mine=0 foreign=2` at exit 0, while `check`'s effect line (`cmd/nova-secrets/main.go:161`) says it decrypts this seat's file with `--key` to prove it opens. | Fail when `--key`'s public half is not a recipient of `<as>.yaml`, or when that file does not decrypt with it. | M |
| 5 | cmd/nova-secrets/main.go:64 | `--json` is documented and implemented on `names` only: `check --json`, `gate --json`, `place --json`, `seal --json` and `version --json` each answer `unknown flag --json`, so the one result value the standard promises as lines or JSON has a JSON rendering for one verb. | Render every verb's result value as JSON, or say in each `-h` why not. | M |
| 6 | docs/CLI.md:2275 | The command reference says a gate verdict is `GATE REFUSE rule=<n> check=<k> file=<f>: <why>` at exit 2; the binary prints `GATE FAILED rule=<n> check=<k> file=<f>: <why>; additional findings=<n>: ...` at exit 1, and gate's own `-h` states neither the word nor the exit. | State the word and exit the binary prints in `GATE FAILED`, or make the binary match the reference. | S |
| 7 | docs/CLI.md:2253 | The command reference opens the tool on `### Gate a seat pull request`, not the `### First run` the onboarding standard requires; the first commands a stranger needs are in the banner, not where the README sends them. | Open with `### First run` and move the gate example below it. | M |
| 8 | pkg/secrets/secret.go:2 | The package comment says it `provides four verbs -- exec, names, check, and keygen`; the tool ships eleven verbs and two seat subverbs, so the first line a code reader meets is wrong. | Name the verbs the package carries now, or say it carries the store-reading and store-writing verbs. | S |
| 9 | docs/SPEC-SECRETS.md:34 | The spec calls the tool `About two hundred lines of Go over two binaries it did not write`; `cmd/nova-secrets` and `pkg/secrets` are 6877 non-test lines. | State the measured size and what the lines are for, or drop the count. | S |
| 10 | cmd/nova-secrets/seat.go:26 | `nova-secrets seat -h` prints the whole root banner rather than the group's two subverbs; `nova-secrets seat` bare itself gives the one line with `run: nova-secrets seat add -h`. | Print the two subverb usage lines and their `-h`, as a group's help should. | S |
| 11 | cmd/nova-secrets/main.go:95 | The exit paragraph (check's failures, exec's 125) is repeated under every verb, including `version -h`, `names -h`, `keygen -h` and `gate -h`, whose verbs cannot fail those ways. | Give each verb its own exit lines, or omit the table where the verb has none. | S |
| 12 | pkg/secrets/place.go:206 | `--machines` means two shapes: `gate` reads a seven-field row `name, ssh, os/arch, roles, seat, cores, notes` (`cmd/nova-secrets/gate.go:21`), `place` reads two to four `name, ssh, home`, and `place` refuses the gate's registry with `(the gate registry has 7 fields)`. | Give the two files two flag names, or one registry shape both verbs read. | M |
| 13 | pkg/secrets/place.go:273 | An unknown machine prints `SECRETS PLACE REFUSED: machine <name> is not in the fleet registry <file>; add it there`, with no `run:` line and without listing the machines the file does hold. | Name the machines present and end with `run: nova-secrets place -h`. | S |
| 14 | pkg/secrets/place.go:408 | `placed --machine bench-a --receipts ./nodir` on an explicitly named directory that does not exist is `SECRETS PLACED OK machine=bench-a count=0` at exit 0; `readReceipts` (`pkg/secrets/place.go:446`) treats an absent file as zero receipts. | Refuse an explicit absent `--receipts`, or say `receipts=absent` on the OK line. | S |
| 15 | pkg/nsprint/verbflag/verbflag.go:126 | `names --max nope` ends `SECRETS NAMES REFUSED: invalid value for --max: it wants n names to list before a MORE line; 0 lists all`, with no `run:` line, while every other refusal ends `run: nova-secrets <verb> -h`. | Append the verb's `run:` line to bad-value refusals. | S |
| 16 | cmd/nova-secrets/main.go:352 | `version --json` refuses `version takes no flags and no arguments, got 1`, not naming `--json`. | Name the argument it got. | S |
| 17 | pkg/secrets/seal.go:78 | `seal` writes progress lines to stderr (`seal: reading ada.yaml`, `seal: encrypting ...`, `seal: committing ...`, `seal: returning ...`) that are outside the tool's `SECRETS ...` grammar, so a reader parsing the stream meets lines it cannot classify. | Prefix them as `SECRETS SEAL NOTE` inside the block, or drop them. | S |
| 18 | cmd/nova-secrets/main.go:186 | The `--dry-run` help is one string shared by `place`, `seal` and `seat inject`; `place -h` prints `seal and seat inject read the store at HEAD`, a sentence about the other two verbs. | Give each verb its own `--dry-run` line. | S |

## Good, keep

`exec` is the right shape for an AI: the value goes only into one child's environment, the
OK line names the seat, file and store head without the value, the child's exit passes
through, and exec's own refusals are 125, apart from any child's status. The refusal
grammar is one shape and, but for findings 3, 13 and 15, every refusal ends in a command a
reader can paste. `exec -h` and `check -h` state the store prerequisite and the three
repair commands before the flags, so the hardest first stumble is answered in the help
itself. `names` reads names without a key and keeps `shown`/`total` in the line and in the
JSON `more`. `seat add` and `gate` together make adding a seat a reviewable commit, and
gate refuses a rule that dropped the recovery key or added an unvouched recipient. `seal --dry-run` prints the plan, writes nothing and reads no value, and `place` puts the value
on ssh's stdin, never in an argument, and writes a receipt that names the sealed bytes and
the store head, never a hash of the value.

## Compared with earlier ratings

| earlier | now | evidence |
|---|---|---|
| the spec's two-hundred-line claim | STILL THERE | docs/SPEC-SECRETS.md:34; cmd/nova-secrets and pkg/secrets are 6877 non-test lines |
| a stale package doc | STILL THERE | pkg/secrets/secret.go:2 still says four verbs |
| the CLI reference opens on gate | STILL THERE | docs/CLI.md:2253 `### Gate a seat pull request` |
| the advertised first run is incomplete and platform-specific | STILL THERE | cmd/nova-secrets/main.go:114-116: the `first value:` seal is refused on a fresh store and the line names `/opt/homebrew/bin/sops` |
| place reads the gate's seven-field registry and plans a garbage path | FIXED | pkg/secrets/place.go:206 now refuses a seven-field row (`the gate registry has 7 fields`) instead of planning `path=linux/amd64/...` |
| gate's runtime refusal lacks the house grammar and a `run:` | FIXED | `gate --base nope` prints `SECRETS GATE REFUSED: --base nope does not name a commit in the store ./secrets; run: nova-secrets gate -h` |
| only `names` accepts `--json` | STILL THERE | cmd/nova-secrets/main.go:64; `check -h`, `gate -h`, `place -h` and `seal -h` do not list it and the verbs refuse it |
| the example names one OS's binary paths | STILL THERE | cmd/nova-secrets/main.go:120 and :122 `/opt/homebrew/bin/...` |
| seal writes stderr lines outside the result grammar | STILL THERE | pkg/secrets/seal.go:78 |
| a bad-value refusal lacks a next command | STILL THERE | `names --max nope` at pkg/nsprint/verbflag/verbflag.go:126 has no `run:` line |
