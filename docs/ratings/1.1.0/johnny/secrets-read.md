# nova-secrets READ rating, nova-tools 1.1.0

Rater: Grok
Build: 86a5fb91119e
Score: 7/10
README: 7.5/10

## Reasons

README line: README.md:30. The sentence matches the banner: encrypted secrets in a git repository, handed to one command at a time. The first command is a real keygen, and the cell says listing and decrypting need a sealed store and sops.

Confused, before any code: README.md:30. The cell ends when the rule is printed. It does not say where those lines go, or which command comes next, so the first success has nowhere to land.

Bored, before any code: README.md:44. After the table, the page says to pick the row again and then leaves this tool until a later sentence. Nothing new is learned.

Doubted, before any code: README.md:83. It says the prerequisites are part of choosing the tool, not a surprise after installation. The row's own first command cannot list a name or hand a value to a child, so the store is the surprise.

The banner in the command, read as text, is the essay the README only starts. It names the store's three files, the seat, the key mode, and the first run as keygen, then git init, a recovery file, the rule, a branch with an upstream, then seal. Each verb has an effect line: inspection, local write, store write, or delivery. Exit codes are in the banner, including 125 for a refusal before the child runs. The type that holds a value closes every format path and has one way back out, a callback. Required flags are collected and named in one error. The spec's numbered tests exist as tests, and the recorded transcript matches the keygen printer: rule, next step, OK, then two plain lines.

A 10 would let a stranger trust the first page they are sent to. The command reference is not that page. It opens on the review verb, and it says the keygen receipt ends on the OK line, which the printer and the transcript both contradict. The spec's own six-line sitting still needs a package-manager prefix and a step another person finishes. The package comment still says four verbs. The spec still says about two hundred lines of Go; the command and the package are 6146 lines before tests. Refusals use three shapes. Exec also carries one other tool's working-count law. The same machines flag is two file formats. Those are why this is 7, not 9. The banner, the value type, and the collected refusals are why it is not 6.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | docs/CLI.md:1825 | Says the keygen receipt ends on the OK line. The printer appends two plain closing lines after that line, and the recorded transcript keeps them. A reader who treats the last line as the answer, which the spec says a reader does, disagrees with this section. | Say that two plain lines follow the OK line, as the printer and the transcript already do. | S |
| 2 | docs/CLI.md:1710 | The section opens on the review verb and a throwaway git store that never seals a value or starts a child. The job the README names is not the first example, and this section has no first-run heading. | Open on the recorded first run, plus the local steps that build the store that run assumes. | M |
| 3 | cmd/nova-secrets/main.go:202 | A flag refusal prints SECRETS REFUSED. Exec prints SECRETS EXEC FAIL. The review verb prints GATE REFUSE and drops the tool name. One caller matching one line misses the other two. | Print one refusal line for every verb: tool, verb, REFUSED, every problem, and the next command. | M |
| 4 | internal/secrets/secret.go:2 | The package comment says this package provides four verbs. The banner also lists gate, place, placed, seal, seat add, and seat inject. | Name every verb the package provides, in the banner's order. | S |
| 5 | docs/SPEC-SECRETS.md:35 | The opening says this is about two hundred lines of Go over two binaries. The command and the package are 6146 lines of Go before tests. | Delete the line count, or replace it with a count a test pins. | S |
| 6 | cmd/nova-secrets/main.go:284 | Exec refuses one redis-cli write of another tool's working-count key. That law is not a secret and not this store. It is a special case in the hand-off. | Name that one foreign refusal in the banner, or move it to the tool that owns the key. | M |
| 7 | internal/secrets/place.go:189 | Place reads column three of the machines file as a home directory. Gate reads the same flag as a fleet registry, whose third column is a platform token. The gate example is that wider row, so reusing it plans a remote path under a platform token. | Use two flag names, and refuse a row whose third field is not a directory. | M |

## Good, keep

The value type closes every format path and returns a value only through a callback. That rule is the one the package comment states, and it is the right one to keep.

The banner answers what the tool does, how the store is shaped, and how a first run starts, and each verb states its effect.

Required flags are named together in one error, so a bad call is one repair.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| a table special case inside exec | STILL THERE | cmd/nova-secrets/main.go:284 still refuses one redis-cli write of another tool's working-count key |
| a stale package doc | STILL THERE | internal/secrets/secret.go:2 still says the package provides four verbs |
| its own refusal grammar | STILL THERE | cmd/nova-secrets/main.go:202 prints SECRETS REFUSED, and internal/secrets/gate.go:252 prints GATE REFUSE |
| the advertised first sitting is incomplete and platform-specific | STILL THERE | docs/SPEC-SECRETS.md:1113 still passes a package-manager prefix, and line 1114 is a comment that another person finishes the store |
