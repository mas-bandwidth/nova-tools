# nova-secrets READ rating, nova-tools 1.1.0

Rater: glm-5.3-flash
Build: bd7949b97aec
Score: 7.5/10
README: 8/10

## Reasons

The README line is `encrypted secrets in a git repository, handed to one command at a
time` (README.md:31); the banner repeats it word for word (cmd/nova-secrets/main.go:25),
so the one sentence a reader needs is the same in the two places. The model is stated
before the code and the code keeps it: one key per seat, one file per seat, the recovery
key declared rather than counted, and no verb ever prints a value (docs/SPEC-SECRETS.md,
pkg/secrets/secret.go). Refusals are one grammar with a remedy, the exit table says
which number is permission, and the tests read as a contract a stranger can follow.

The first confusion is the README's own trial header: it says the commands are the 1.0.0
set and links that release while the tree under the reader is 1.1.0 (README.md:50). The
first boredom is the tool table itself, which runs sixteen rows before a reader reaches
the one tool; the nova-secrets row is dense but answerable (README.md:31). The first
doubt is the spec's own size claim, that the tool is `about two hundred lines of Go`
(docs/SPEC-SECRETS.md:34); the package it describes is 5287 non-test lines, so the claim
a reader chooses the tool by is the one the code most visibly does not bear out.

What keeps the score at 7.5: the reading of the model, the help and the safety story are
close to what a 10 asks for, but the tool grew far past the thin wrapper its spec still
describes, one other tool's rule sits inside this tool's command path, the package doc
describes a four-verb tool, and the command reference never opens with a first run. A 10
needs the weight the spec claims (or a spec that follows the code), a current package
doc, the foreign rule out of `exec`, a `### First run` in the command reference, and a
first sitting a stranger can finish on any operating system.

## Findings

| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | docs/SPEC-SECRETS.md:34 | The spec calls the tool `about two hundred lines of Go over two binaries it did not write`, but the package is 5287 non-test lines: it parses the sops yaml, the git index, loose objects, packed refs and the git config by hand. A reader who chose the tool for its claimed size meets a system. | State the measured size and what the extra lines are for, or move the store-read machinery behind a library and let the claim follow the code. | L |
| 2 | cmd/nova-secrets/main.go:413 | `exec` refuses a redis-cli write of a sprint table column, a rule about another tool's state with no home in a credential tool, and the comment carries an issue number. It runs in the command path before the command starts. | Move the refusal to the tool that owns the column or to the store's ACL, and drop it from this command path. | M |
| 3 | pkg/secrets/store.go:546 | The git index is parsed by hand, with loose-object, packed-ref and config parsing beside it (store.go:89, git.go:127, git.go:204, git.go:321), and a yaml parser that is not the adopted library; each is code a reader must trust byte for byte. | Prefer the adopted yaml library and the sanctioned git seam, or justify each hand parser where it stands. | L |
| 4 | pkg/secrets/secret.go:2 | The package doc says the package provides four verbs, `exec, names, check, and keygen`; the tool now ships eleven verbs and two seat subverbs, so the first thing a reader of the package sees is wrong. | Name the verbs the package now carries, or say it carries the store-reading and store-writing verbs. | S |
| 5 | docs/CLI.md:1802 | The command reference opens the tool on a gate example that builds a two-commit store; the first run a stranger needs is in the spec and the transcripts, not where the README sends them. | Open the section with a `### First run` block and move the gate example below it. | M |
| 6 | docs/SPEC-SECRETS.md:1113 | The advertised first run is a package-manager path with a fixed binary location, and the block itself says a stranger cannot finish it alone; it cannot be pasted on another operating system. | Lead with the store-free `keygen` trial that names no package manager, and state the wait as a state with its clearing command. | M |
| 7 | docs/SPEC-SECRETS.md:26 | The spec says the tool `links no cryptography`, but the store-pull boundary derives and checks SSH public halves through the standard crypto packages (pkg/secrets/storepull.go:190). | Limit the claim to the store operations, and say the pull boundary parses the seat's SSH key. | S |
| 8 | README.md:50 | The trial section still says `These are the Nova Tools 1.0.0 commands` and links the 1.0.0 release while the tree rated here is 1.1.0. | Say which version the commands are, or update the section in the same change as the release. | S |

## Good, keep

The one-line purpose is identical in the README and the banner, and the banner's `how it
works` is within five lines naming the store's nouns.
The refusal grammar, the exit table and the no-value-printed rule are one shape across
every verb, and the tests read as the contract rather than as coverage.

## Compared with earlier ratings

| earlier | now | evidence |
|---|---|---|
| a table special case inside exec | STILL THERE | cmd/nova-secrets/main.go:291 and its call at cmd/nova-secrets/main.go:413 |
| a stale package doc | STILL THERE | pkg/secrets/secret.go:2 still says four verbs |
| its own refusal grammar | FIXED | cmd/nova-secrets/main.go:210 prints `SECRETS <VERB> REFUSED: <reason>; run: <remedy>` through the shared helper |
| the advertised first sitting is incomplete and platform-specific | STILL THERE | docs/SPEC-SECRETS.md:1113 keeps a package-manager path and a fixed binary location |
