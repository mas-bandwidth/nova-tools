# The house style for every nova-* tool

One grammar, one vocabulary, one receipt shape, so a verb learned on one tool
is known on all of them (nova-tools#4352 A; Glenn 2026-09-26: "Make it feel
natural. Fix the inconsistencies. Make everything predictable"). The class
test `internal/ci/clistyle_class_test.go` reads every `cmd/nova-*` verb's flag
set from the source and holds it here. nova-sprint is on it entirely; every
other tool's findings of today are rows of
`internal/ci/testdata/cli-style_allowlist.txt`, a list that only shrinks, one
card per tool to empty it.

## The line

```
<tool> <noun> <verb> [--flags] [positionals]
```

A noun is the thing (`card`, `task`, `stream`, `worker`, `fleet`); a verb is
what happens to it (`deal`, `push`, `order`, `pause`, `release`). A tool with
one thing may drop the noun (`nova-version`), never the verb. Flags come
after the verb; `--seat <name>` (or `NOVA_SEAT`) may come anywhere before a
`--`. Positionals are only files (`card push a.md b.md`, `result check -`) and
what follows `--` (`redis-cli -- ZCARD k`): a named object (a worker, a card,
a stream, a sprint, a sha, a pull request) is a flag, so it never depends on
its place on the line and a batch is the same line with a longer list.

## The vocabulary

The same flag means the same thing on every verb of every tool, with the one
help line `internal/nsprint/verbflag` carries for it (`verbflag.Vocabulary`):

| flag | meaning | default |
|---|---|---|
| `--redis <host:port>` | the sprint store | the seat's, else `NOVA_SPRINT_REDIS` |
| `--sprint <S>` | the sprint | the open sprint (#4352 I) |
| `--as <friend:f\|bench:b>` | the worker the verb acts as or on | the seat's own worker |
| `--ids <a,b>` | the ids the verb acts on; `@<file>` or `@-` reads them one per line | |
| `--stream <a,b>` | the work stream(s) | the card's stream (#4352 I) |
| `--why <text>` | the reason a write records | |
| `--n <N>` | how many | |
| `--to <friend:f\|bench:b>` | the target worker | |
| `--from <path\|->` | the input: a file, a directory of files, or `-` for stdin | |
| `--ref <repo>#<n>` | a forge ref: an issue or a pull request | |
| `--pr <n>` | a pull request number | |
| `--sha <hex>` | a commit | |
| `--repo <owner/name>` | the repository | |
| `--idem <key>` | an idempotency key: the same key twice is one write, the second ALREADY | |
| `--dry-run` | print what this would write, in receipt form, and write nothing | |
| `--since <10m\|2h\|1d>` | how far back | |
| `--json` | one JSON object per line instead of the table | |
| `-v` | the seat line and the rest of the chatter; success is one line without it | |

Lists are commas (`--ids a,b,c`, `--stream swarm-cards,console`), never a
pipe and never a repeated flag; a value that may hold a comma (`KEY=VALUE`)
is the one exception, and its help says so. A worker is `friend:<f>` or
`bench:<b>`; a verb whose noun already says the kind (`capacity friend`)
takes the bare name too. Stream names are slugs (`swarm-cards`), so nothing
needs quoting.

The actor a receipt records is the seat: `NOVA_FRIEND` (every harness
exports it), else the `--seat` name, else the login user. It is never a flag.

## Retired spellings

A spelling the grammar retired is refused naming the current one and never
kept as an alias (`verbflag.Retired`; the class test refuses any verb that
defines one):

| retired | now |
|---|---|
| `--actor`, `--by`, `--consumer`, `--who`, `--friend` | `--as` |
| `--id`, `--label` | `--ids` |
| `--reason` | `--why` |
| `--streams`, `--to-stream`, `--scope` | `--stream` |
| `--to-friend` | `--to` |
| `--addr`, `--store` | `--redis` |

```
$ nova-sprint card work --actor friend:rowan
nova-sprint card work: --actor is spelled --as; run: nova-sprint help
```

## Help

`<verb> -h` prints `usage: <tool> <noun> <verb> [flags]`, one example line,
one line per flag (`--name <type>  <one sentence>`; every flag has one, the
class test refuses an empty help) and the exit codes, on stdout, exit 2,
without dialling anything. `<tool> help` lists the nouns with one line each.

## Receipts

One line on stdout: the verb path in caps, then `OK|REFUSED|DRIFT|FENCED`,
then `key=value` pairs, `ms=` last; exit 0 ok, 1 refused, 2 usage, 3 fenced.
Every `REFUSED` carries `remedy="<the exact command>"`, copy-pasteable, never
`run: <tool> help`. Success is one line; a failure is the receipt and the
remedy and nothing else (#4352 C and Q own the receipt shape and the quiet).

```
CARD WORK OK as=friend:rowan n=2 ids=ci-tiers~1,console-a~1 ms=4
CARD CANCEL REFUSED ids=ci-tiers why="no task card ci-tiers; did you mean ci-tiers~1 (copy)?" remedy="nova-sprint card cancel --ids ci-tiers~1 --why ..." ms=1
```

## Time

Ages read as a human does (`3m`, `2h`, `1d`) in every table and receipt;
clock times are Eastern; `--since 10m` on every listing verb (#4352 J).

## Headers

A card's header keys are uppercase with hyphens, `BASE-SHA:` like `BASE:`
and `DONE-WHEN:`; a lowercase spelling (`base-sha:`) is refused by every
input naming the uppercase one, while the bench readers still read both.

## Bringing a tool over

Run `go test ./internal/ci/ -run TestCLIStyle`: its failure lines are the
allowlist rows the tool still owes, `tool rule verb flag`, one per finding
(`empty-help`, `retired`, `vocabulary`, `alias`, `root-flags`, `positional`).
Fix the verb, delete its row, and the test holds the line from then on; a
row whose finding has left is red too, so the list can only get shorter.
Build the flag set with `verbflag.New` so a retired spelling is refused with
the new one named, and give a vocabulary flag its `verbflag.Help*` constant so
the help is the same sentence everywhere.
