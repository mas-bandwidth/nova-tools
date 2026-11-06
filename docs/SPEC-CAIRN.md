# nova-cairn

nova-cairn is the record an AI keeps for its next session. A session ends and
the next one starts with nothing; a cairn is what the last one left behind.
It does three things:

- **The store** keeps the words a session chose to keep, as plain files.
- **memory** searches those words and the notes already written, and checks
  them.
- **self-talk** flags sentences where the writer passes a standing verdict on
  themselves.

One binary, Go standard library only. It sends nothing anywhere.
`nova-cairn help` lists every verb; `nova-cairn help <verb>` and
`nova-cairn <verb> -h` print one verb's help.

## The store

A store is a directory you name with `--store`. There is no default, no
environment variable and no search of the working directory. Inside it:

```
sessions/<session>.md        the readable record: a header, then one ENTRY line per entry
entries/<session>/<id>.json  one file per entry: words, stamp, source, policy
log.jsonl                    one line per open and per append, never rewritten
```

The entry files are the source of truth. The header is for people. It is
read only to heal an open that crashed before its log line: the heal logs the
policy and source the header recorded.

**open** starts a session.

```
nova-cairn open --store ~/cairn --session 2026-11-05 --publish manual --source notes/today.md
```

`--publish` is `never`, `manual`, `deferred` or `immediate`: a policy you
record for later. nova-cairn never publishes anything: the APPEND and
RECEIPT lines say `persisted=true published=false`, the words are on disk
and nothing was sent. `--source` points at where the words came from; it is
recorded and never opened. Opening again with the same policy and source
changes nothing (`reopened=true`). Another policy or source is a conflict
(exit 1), whose line names the `open` that matches the record.

**append** keeps one entry's exact words under an id.

```
nova-cairn append --store ~/cairn --session 2026-11-05 --entry lesson-1 --text "Read the file before claiming what it says."
```

The words come from exactly one of `--text` or `--file <path>` (`--file -`
reads stdin), kept byte for byte. They must be valid UTF-8, because the entry
file is JSON: anything else is refused (exit 2) naming the first bad byte,
so everything accepted is kept exactly. A new id (on `open` and `append`)
and `--source` must be valid UTF-8 too; the read verbs still take ids an older
build wrote with other bytes, and memory leaves such entries out with a WARN. The entry takes the session's source and
policy unless the line names them. The same line again prints
`duplicate=true` with the first stamp and writes nothing. The same id with
other words, or a retry whose `--source` names another source than the
entry's, is a conflict (exit 1) whose line names the `receipt` to read it.

**index** lists the sessions and entries, oldest entry first.

```
nova-cairn index --store ~/cairn
```

The first line, `INDEX OK sessions=<n> entries=<n>`, has the full counts.
`--session <id>` lists one session. `--max <n>` caps each list (default 20,
`0` for all); each capped kind (sessions, entries) ends with its own `MORE`
line on stdout.

**receipt** reads one entry back. It takes `--store`, `--session` and
`--entry`; there is no stamp flag.

```
nova-cairn receipt --store ~/cairn --session 2026-11-05 --entry lesson-1 --text
```

It prints the stamp, size, source and policy, and with `--text` the words,
quoted: `text="Read the file before claiming what it says."`.

**Flat records.** A store may also hold one hand-kept markdown file per
session, `<store>/<session>.md`, with a heading per entry such as
`## 2026-10-01T10:00:00Z — a1`. `index` and `receipt` read it as it stands.
Nothing writes to it, and `append` refuses it. It stores no source or policy
(`source=-`, `publish=unknown`), and its words are the section body, trimmed.
If a session has both shapes, the nested record wins.

### What the store promises

- **On disk before OK.** Every entry file and log line is synced, and so is
  the directory that holds its name (and any directory just made), before
  the OK line is printed. Windows has no directory sync; there the file sync
  is all.
- **Never overwritten.** An entry is written to a temporary file and then
  linked into place, which fails if the name exists. So a kept entry is never
  replaced, and no reader sees half a file. nova-cairn never edits a kept
  entry. It does not detect an edit made by hand.
- **One open per session**, with one policy.
- **A receipt is the record.** For a nested entry, `receipt --text` prints
  the exact words that were kept.
- **Interrupted work heals.** If a crash comes after an entry is linked but
  before its ENTRY line, the retry of the same words is a duplicate that adds
  the ENTRY line; the log then holds no append record for that entry. A
  leftover `*.tmp` file is never read as an entry.
- **Provenance is never guessed.** An unreadable `log.jsonl`, or an open
  record that does not decode, stops an append.
- **An id is one path component** on every platform: nonempty, at most 128
  bytes, no whitespace or control characters, none of `/ \ : * ? " < > |`,
  not only dots, no `..`, no trailing dot, not a Windows device name. A bad
  id is refused before anything is written.

The store is modelled in `cairn/tla/CairnStore.tla`. TLC checks NoOverwrite,
DuplicateIsOne, DurableBeforeReported, OpenIdempotent and OneOpenPolicy over
open, an open that crashes before its log line, append, and an append that
crashes after its link. Four reversed witnesses (`MCCairnStoreBroken*.cfg`)
each break one on purpose (an overwrite, success before sync, a re-open that
changes the policy, a link before sync), to prove the check can fail.

## memory

memory reads everything under one or more `--root` directories that holds
words: the `.md` notes, and the entries of any store in the tree
(`entries/<session>/<id>.json`). So an AI finds what it appended, beside what
it wrote by hand. A store's `sessions/<session>.md` is not read as a note:
its words are in the entries. A note's paragraph needs three terms to be
indexed; an entry's words are indexed however short. A `.json` under an
`entries/` directory that does not decode as a store entry with its own
session and id is some other file: it is left out, counted as `skipped=<n>`
on the OK line, and named once on stderr in a `WARN` line. `--exclude`
leaves such files out quietly.

memory builds its index in memory on every run, so nothing goes stale and
nothing is written. It skips `.git`, never follows a symlink, and refuses a
file over 8 MiB by name. `--exclude <glob>` skips paths on every memory verb.

**search** prints the k best files (notes or entries) for some words, one
hit per file: its best-scoring paragraph.

```
nova-cairn memory search --root ~/notes --channels bm25 --k 3 lantern brass
```

One `SEARCH HIT` line per note or entry: score, file, line, the start of the
paragraph. A hit from an entry also names `store=`, `session=`, `entry=` and
`stamp=`, which are what `receipt` takes to read the whole entry back. A `SEARCH CAL` line scores an unrelated sentence, so you can see
what a weak match looks like. The search is lexical (BM25 over paragraphs):
a paraphrase that shares no words with the notes will not be found.

**check** prints, for each paragraph of a draft, the k best passages already
in the notes. It never judges and never exits 1.

```
nova-cairn memory check --root ~/notes --channels bm25 --k 2 draft.md
```

**stats** prints the size of the index: notes (`files`), store `entries`,
paragraphs (`chunks`), bytes and vocabulary.

```
nova-cairn memory stats --root ~/notes
```

**verify** checks the shape of one tree of notes (entries are not notes) and
exits 1 on a finding.

```
nova-cairn memory verify --root ~/memory --links gate --coverage 'notes/*.md:notes/index-*.md' --frontmatter 'notes/*.md' --exempt index-
```

Globs match paths relative to `--root`: here `~/memory/notes/*.md` is
`notes/*.md`.

- `--coverage A:B`: every file matching A has its name (the file name
  without `.md`, as a whole word) or a link to it in some file
  matching B, and every link in B points at a file that exists.
- `--frontmatter <glob>`: every matching file has a `name:` in its
  frontmatter. `--exempt <prefix>` excuses files by basename.
- `--links gate`: every `[[wikilink]]` resolves to a file or a frontmatter
  name. `--links info` lists the ones that do not, without failing.

`--links` is always required. `--links info` with no `--coverage` and no
`--frontmatter` is refused, because that run could not fail.

A pass proves every note is named or linked in an index, no link is dead,
and every note has its frontmatter name. It does not prove the words are true or unedited.
A glob that matches nothing is refused: an empty check is not a pass.

## self-talk

```
nova-cairn self-talk notes/*.md
```

Reads files (`-` is stdin) sentence by sentence and flags:

- **STANDING**: a first-person claim with a word of failure ("I cannot
  check my own work").
- **A verdict in neutral words**: RANKING ("my weakest instrument"),
  FORECLOSURE ("I will never be a good planner"), VERDICT-IDIOM ("dead as a
  practice"), TRAIT ("I always break the build").

A STANDING claim with a date or a measurement is a record: it counts as
DATED and is never flagged. Nothing else excuses a STANDING claim: it is
flagged inside a quotation too (`She said "I am a failure at this".` is
STANDING), because grammar cannot tell whose verdict it is. The tool says so
on its closing NOTE line. The neutral-word shapes are not flagged in a
sentence that is dated, a rule (`RULE:`), an aspiration ("I want to"), an
instruction, or a quotation.

`--skip <name>` leaves a file out and says so; `--rule-doc <name>` prints a
banner above that file's findings. Findings go to stderr, one `SELFTALK FAIL`
line each, then a `SELFTALK MORE` line on stderr if `--max` cut them; the
count line and notes go to stdout. The run exits 1 on any finding. self-talk
matches known shapes only: a green run clears those shapes, never the file.

## Output and exit codes

Every result line starts with a token in capitals and carries `key=value`
facts. The tokens: `OPEN`, `APPEND`, `INDEX`, `RECEIPT`, `SEARCH` (memory
search), `MEMORY` (memory check), `STATS`, `VERIFY`, `SELFTALK`. A cut
listing ends with one `<TOKEN> MORE kind=<kind> shown=<n> total=<t> <flag> <n>
raises the ceiling, <flag> 0 prints all` line, on the stream of the lines it
cut: stdout for INDEX and VERIFY INFO, stderr for VERIFY FAIL and SELFTALK. Exit 0 is done, 1 a finding or a conflict, 2 a run that could not
happen. A refusal is one line on stderr,
`nova-cairn <verb>: <what is wrong>; run: <what to run next>`. A malformed
line names the verb's `-h`. A runtime failure names the concrete next
command: the same search with `--exclude <file>` for a file memory cannot
read, `ls -ld <dir>` for a store or root that is not there, `ls -l <file>`
for a file self-talk cannot read (`--skip` for one that is not UTF-8),
`sed -n <n>p <file>` for a line that does not decode. Flags
come before arguments: a flag after an argument is refused by name (a
literal starting with `-` goes after `--`). A flag the verb does not take is
refused by name, never ignored. A conflict is a
FAILED line (`APPEND FAILED session=... entry=...: ...`), exit 1.

`go test ./cairn` runs the unit tests. `tests/tla/check.sh <tla2tools.jar> cairn/tla`
runs the model.
