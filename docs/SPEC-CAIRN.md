# nova-cairn

Keep useful words for a later session. A **cairn** is a saved session record.
The tool writes plain files, searches saved entries and Markdown notes,
and checks notes for broken links and missing index entries. Its self-talk
check flags known sentence patterns that turn an event into a lasting
judgment about the writer.

The files stay on your machine. Protect private notes with filesystem
permissions; nova-cairn does not encrypt them or send them anywhere.
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

Supply the words through one of `--text` or `--file <path>` (`--file -`
reads stdin), kept byte for byte. The entry inherits the session's source unless `--source` supplies another
pointer. An explicit `--publish` must match the session's policy. The same line again prints
`duplicate=true` with the first stamp and writes nothing. The same id with
other words, or a retry whose `--source` names another source than the
entry's, is a conflict (exit 1) whose line names the `receipt` to read it.

**index** lists the sessions and entries, oldest entry first.

```
nova-cairn index --store ~/cairn
```

The first line, `INDEX OK sessions=<n> entries=<n>`, has the full counts.
`--session <id>` lists one session. `--max <n>` caps each list (default 20,
`0` for all), and a capped list ends with a `MORE` line on stdout.

**receipt** reads one entry back.

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
changes the policy, a link before sync), to check that the model detects those mistakes. Model results depend on
the assumptions represented there.

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

memory builds its index in memory on every run, and writes
nothing. It skips `.git`, never follows a symlink, and refuses a
file over 8 MiB by name. `--exclude <glob>` skips paths on every memory verb.

**search** prints the k best passages for some words.

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

A pass covers the checks and matching files you selected. Coverage and
frontmatter checks run only for their supplied globs; informational wikilink
findings do not fail the run. The checks do not establish that the words are
true or unedited. A supplied glob that matches nothing is refused.
At least one gating check is required: `--links gate`, `--coverage` or
`--frontmatter`. `--exempt` requires a frontmatter check.

## Try all memory operations

This example creates a small notes directory and a draft outside it. A
**frontmatter** block is the YAML-shaped block between the opening `---`
lines. An **index** here is an ordinary note that names or links other notes.

```sh
notes_dir=$(mktemp -d)
printf '%s\n' '---' 'name: lantern' '---' '' 'The brass lantern illuminates the harbor.' > "$notes_dir/lantern.md"
printf '%s\n' '# Index' '' '[lantern](lantern.md)' > "$notes_dir/index-main.md"
printf '%s\n' 'The brass lantern lights the harbor.' > "$notes_dir/draft.txt"
nova-cairn memory stats --root "$notes_dir"
nova-cairn memory search --root "$notes_dir" --root ~/cairn --exclude 'ignored/*' --channels bm25 --k 3 brass lantern
nova-cairn memory check --root "$notes_dir" --channels bm25 --k 2 "$notes_dir/draft.txt"
nova-cairn memory verify --root "$notes_dir" --links gate --coverage 'lantern.md:index-*.md' --frontmatter '*.md' --exempt index- --fail-max 0
```

Use the store created earlier for the second root. Search and check require
`--channels bm25` and a positive `--k`. BM25 ranks passages by shared words;
`--k` limits the passages returned for each query, not the files read.
`--root` and `--exclude` can repeat. Verify accepts one root and requires at
least one gating check. `--fail-max` defaults to 20 lines per finding kind;
0 prints all. To make wikilinks informational while still checking frontmatter,
replace `--links gate` with `--links info` in this example.

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

For a small file-based run:

```sh
printf '%s\n' 'The measured check passed.' > "$notes_dir/review.txt"
printf '%s\n' 'RULE: Read the evidence before making a claim.' > "$notes_dir/rules.txt"
nova-cairn self-talk --max 20 --rule-doc rules.txt --skip review.txt "$notes_dir/review.txt" "$notes_dir/rules.txt"
printf '%s\n' 'The measured check passed.' | nova-cairn self-talk -
```

`--skip` and `--rule-doc` match basenames and can repeat. `--max` defaults to
20 findings per class across the run: standing claims and other verdict
patterns. 0 prints all. Skipped files are reported. A rule-doc
banner identifies that input when it has findings; it does not change the classifier.

## Other store forms and help

Use `--file` when words are already in a file, or `--file -` for stdin:

```sh
printf '%s\n' 'Keep the original evidence.' > "$notes_dir/entry.txt"
nova-cairn append --store ~/cairn --session 2026-11-05 --entry lesson-2 --file "$notes_dir/entry.txt" --source notes/evidence.md --publish manual
printf '%s\n' 'Check the saved receipt.' | nova-cairn append --store ~/cairn --session 2026-11-05 --entry lesson-3 --file -
nova-cairn index --store ~/cairn --session 2026-11-05 --max 0
nova-cairn receipt --store ~/cairn --session 2026-11-05 --entry lesson-2
nova-cairn help memory search
nova-cairn append --help
nova-cairn version
```

These commands continue the session opened earlier. `--text` and `--file`
are alternatives, not a combined input. `version` prints the build's version
string; a local build may say `dev`.

## Recover from a refusal

| Result | Next step |
|---|---|
| Missing or unknown flags, late flag, bad ID or policy | Follow the verb's help. IDs obey the path-component rules above; policies are the four listed values. |
| Session absent | Run the printed `open` command with the intended policy, then retry append. |
| Session policy or source conflict | Read the existing record; reuse its settings or choose a new session ID. |
| Entry conflict | Run the printed `receipt`; keep the existing entry and use a new ID for different words. |
| Flat record cannot be appended | Edit that hand-kept Markdown record yourself, or open a new nested session. |
| Unreadable store, root or input | Inspect the named path and permissions with the printed listing command. |
| Corrupt open log or entry record | Inspect the named file or line and restore it from known-good data before retrying. |
| Memory input cannot be read or exceeds 8 MiB | Correct the input or exclude the named path. An exclusion changes the corpus being searched or checked. |
| Empty corpus or draft | Choose a root with readable notes or entries, or a draft with a paragraph of at least three terms. |
| Invalid retrieval settings | Use `--channels bm25` and positive `--k`. |
| Verify glob matches nothing, or no gating check selected | Correct the relative globs and select `--links gate`, coverage or frontmatter checking. |
| Verify findings | Repair the reported links, index references or frontmatter, then rerun the same check. |
| Self-talk finding | Read the sentence in context and decide whether to revise it. The tool matches patterns, not intent. |
| Self-talk input is not UTF-8 | Correct its encoding or explicitly skip the named file. |

## Output and exit codes

| Output | Meaning |
|---|---|
| `OPEN OK` | Session, policy and source; `reopened=true` means an idempotent open. |
| `APPEND OK` | Saved entry identity, stamp and policy; `duplicate=true` retains the first record. |
| `INDEX OK`, `INDEX SESSION`, `INDEX ENTRY`, `INDEX MORE` | Full counts, session policy and opening time, entry metadata, then any truncation notice. |
| `RECEIPT OK` | Stored entry metadata, and quoted words with `--text`. |
| `SEARCH OK`, `CAL`, `HIT` or `MISS` | Corpus/query summary, unrelated calibration score, then matches or the reason for no match. |
| `MEMORY OK`, `CAL`, `CAND`, `HIT` or `MISS`, `NOTE` | Draft and corpus counts, calibration, numbered candidate paragraph, its similar passages, and interpretation limits. No truth judgment. |
| `STATS OK` | Notes, entries, skipped files, paragraphs, bytes and vocabulary, followed by per-class counts. |
| `VERIFY OK` or `FAIL` | Counts of gating and informational findings; only selected gating checks determine failure. |
| `SELFTALK FAIL`, `MORE`, summary and `NOTE` | Sentence findings, any display cap, totals and classifier limits. |

Ordinary field values escape spaces, `=` and control characters so they stay
on one line. Receipt text is quoted. `stamp` is the stored time; it is not
refreshed by a duplicate or a read.

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

## Build and test

From the repository root, on a build machine:

```sh
build_dir=$(mktemp -d)
go build -o "$build_dir/nova-cairn" ./cairn
go test ./cairn
tla/check.sh <tla2tools.jar> cairn/tla
```
