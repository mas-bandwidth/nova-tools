# nova-cairn

nova-cairn keeps a session's words as plain files, and carries two small readers of your own markdown notes. One
binary, three groups of verbs, standard library only.

```
nova-cairn open    --store <dir> --session <id> [--source <ptr>] --publish <never|manual|deferred|immediate>
nova-cairn append  --store <dir> --session <id> --entry <id> (--text <words> | --file <path|->) [--source <ptr>] [--publish <policy>]
nova-cairn index   --store <dir> [--session <id>] [--max <n>]
nova-cairn receipt --store <dir> --session <id> --entry <id> [--text]

nova-cairn self-talk [--skip <basename>]... [--rule-doc <basename>]... [--max <n>] <file|->...

nova-cairn memory stats  --root <dir>... [--exclude <glob>]...
nova-cairn memory search --root <dir>... --channels bm25 --k <n> [--exclude <glob>]... <words>...
nova-cairn memory check  --root <dir>... --channels bm25 --k <n> [--exclude <glob>]... <file|->
nova-cairn memory verify --root <dir> --links <gate|info> [--coverage <A:B>]... [--frontmatter <glob>]...
                         [--exempt <prefix>]... [--exclude <glob>]... [--fail-max <n>]
```

## The store (open, append, index, receipt)

A store is a directory of plain files, synced to disk before OK. `open` starts a session and records its `--publish`
policy; `append` keeps an entry's exact words (never trimmed) with a stamp, an id and a source pointer. The same id with the same
words is a duplicate (`duplicate=true`, exit 0); the same id with other words is a conflict (exit 1), never an overwrite. A
re-open naming another policy or source is a conflict. `--publish` records your policy only: nothing is sent, and every line says
`published=false`. Ids name one file each and are refused if a path would read them as something else.

On disk (unchanged from earlier releases, so older stores read as they are):

```
sessions/<session>.md        readable record (header is convention)
entries/<session>/<id>.json  one file per entry, the source of truth
log.jsonl                    append-only: the opens and the appends
```

A flat record, `<store>/<session>.md` with a `## <rfc3339> — <entry>` section per entry, is read by `index` and `receipt` and
never written. Exit codes: 0 done, 1 a conflict, 2 usage or could not run. Refusals and conflicts print on stderr.

## self-talk

Reads files (`-` is stdin) sentence by sentence and flags a first-person claim carrying a word of failure (`STANDING`: *I cannot
check*, *bad at*, *worst*), or a standing verdict in neutral words (`RANKING`, `FORECLOSURE`, `VERDICT-IDIOM`, `TRAIT`: *I will never
be a good planner*, *dead as a practice*, *I tend to rush*). A claim with a date or a measurement word is `DATED`, a record, counted
and never flagged; an instrument (`RULE:`), an aspiration (*I want to*), an imperative and a quotation are licensed. Findings print on
stderr, one line each; the count line and notes on stdout. Exit 1 on findings. It matches shapes only: a green clears the known
shapes, never the file. `--rule-doc` prints a banner above a file's findings; `--skip` leaves a file unscanned and says so.

## memory

Builds a BM25 index over the paragraphs of the `.md` files under `--root` in memory every run; nothing is written, nothing goes
stale. `search` prints the k best files for some words, `check` the k best for each paragraph of a draft (it never judges),
`stats` the size of the index, and `verify` gates a tree: `--coverage A:B` (every file matching glob A is named or linked from a file
matching B, and B's links resolve), `--frontmatter` (a `name:` in the frontmatter), and `[[wikilinks]]` (`--links gate` fails on an
unresolved one, `info` lists them). Symlinks are never followed. Exit 1 on a gating finding.

## Tests

`go test ./cairn` runs unit tests on fixtures in `cairn/testdata` and temporary directories. The store's state machine is modelled
in `tla/CairnStore.tla` (three reversed witnesses: `MCCairnStoreBroken*.cfg`); run it with
`java -cp tla2tools.jar tlc2.TLC -config MCCairnStore.cfg -deadlock MCCairnStore.tla`.
