# nova-cairn

Save the words of a session, read them back, and search them beside your
Markdown notes. It also checks notes for broken links and scans text for
sentences that turn one event into a lasting judgment of the writer. The files
stay on your machine. Search is by keywords only: a paraphrase that shares
almost no words with an entry is not found.

## Build

```sh
git clone -q https://github.com/mas-bandwidth/nova-tools
go build -C nova-tools -o "$PWD/bin/nova-cairn" ./cairn
export PATH="$PWD/bin:$PATH"
```

The blocks below run in order, in one shell, from a fresh directory:

```sh
cd "$(mktemp -d)"
```

## Keep and read entries

A **store** is a directory you name with `--store`. A **session** is a named
group of entries. Each entry has a **stamp** (the UTC time it was stored), a
**source** (a pointer you give, such as a file name; never opened) and a
**policy** (`never`, `manual`, `deferred` or `immediate`: whether you mean to
publish it later). nova-cairn publishes nothing, whatever the policy.

```sh
nova-cairn open --store store --session 2026-11-05 --publish manual --source notes/today.md
nova-cairn append --store store --session 2026-11-05 --entry lesson-1 --text "Read the file before claiming what it says."
nova-cairn index --store store
nova-cairn receipt --store store --session 2026-11-05 --entry lesson-1 --text
```

```text
OPEN OK session=2026-11-05 store=store source=notes/today.md publish=manual stamp=2026-11-05T23:45:59.429450238Z
APPEND OK session=2026-11-05 entry=lesson-1 source=notes/today.md persisted=true published=false publish=manual duplicate=false stamp=2026-11-05T23:45:59.433149355Z opened=false next=nova-cairn\x20index\x20--store\x20store
INDEX OK sessions=1 entries=1
```

`index` also prints a line per session and entry, and `receipt` the entry's
facts and its quoted words. Stamps vary. A session's first entry ends the
APPEND OK line with `next=<the index command>`. Give words with `--text` or
`--file <path>` (`-` reads stdin). The text is kept byte for byte, and input
that is not valid UTF-8 is refused. The same `append` again gives
`duplicate=true`. `index` takes `--session <id>` and `--max <n>` (default 20,
`0` for all). An id is one path component with no whitespace and none of
`/ \ : * ? " < > |`. A session the store does not hold yet is opened by the
append itself, with publish=manual unless the line names a policy, and the
OK line then ends `opened=true`; a store that does not exist is refused,
because only `open` creates one.

## Search your notes

`memory` reads the `.md` notes under each `--root` and the entries of any
store in the tree, and writes nothing. A **term** is a lowercased word of two
or more letters or digits. A note paragraph needs three terms to be indexed.
Ranking is **BM25**: paragraphs that share more of your terms, and rarer ones,
score higher. At weighting, 34 English function words (the, of, and, for, and
their kin) carry no weight, so shared grammar alone cannot outscore a content
match; a query with no other words falls back to weighing them. Other
languages' function words still weigh. A **frontmatter** block is the YAML-shaped block between `---`
lines at the top of a note.

```sh
mkdir notes
printf '%s\n' '---' 'name: lantern' 'type: note' '---' '' 'The brass lantern lights the harbor at night.' > notes/lantern.md
printf '%s\n' '# Index' '' '[lantern](lantern.md)' > notes/index-main.md
printf '%s\n' 'The brass lantern lights the harbor.' > draft.txt
nova-cairn memory search --root notes --root store --channels bm25 --k 3 brass lantern
nova-cairn memory check --root notes --root store --channels bm25 --k 2 draft.txt
```

```text
SEARCH OK hits=1 k=3 channels=bm25 files=2 entries=1 skipped=0 chunks=2: query="brass lantern"
SEARCH HIT rank=1 score=1.39 score-channel=bm25 class=. name=lantern type=note root=notes: lantern.md:6 "The brass lantern lights the harbor at night."
MEMORY HIT cand=1 rank=1 score=3.02 score-channel=bm25 class=. name=lantern type=note root=notes: lantern.md:6 "The brass lantern lights the harbor at night."
MEMORY HIT cand=1 rank=2 score=0.18 score-channel=bm25 class=entries name=- type=- root=store store=store session=2026-11-05 entry=lesson-1 stamp=2026-11-05T23:45:59.433149355Z: entries/2026-11-05/lesson-1.json:1 "Read the file before claiming what it says."
```

`OK`, `CAND`, `NOTE` and `CAL` lines are partly left out. `search` and `check` need
`--channels bm25` and a positive `--k`, and print at most `k` passages, one per
file (its best paragraph). `check` compares each paragraph of a draft (`-`
reads stdin) and never exits 1. `--exclude <glob>` skips paths and repeats.
`memory stats` prints the counts.

| Field or line | Meaning |
|---|---|
| `CAL` (not shown above) | The score of a fixed unrelated sentence. A hit near it is weak. A `MISS` line means no query term occurs in the corpus. |
| `rank`, `score`, `score-channel` | Place in the list from 1, the BM25 score, and `bm25`. |
| `class` | First directory of the path under the root, or `.`. |
| `name`, `type`, `root` | The note's frontmatter `name:` and `type:` (or `-`), and its `--root`. |
| `store`, `session`, `entry`, `stamp` | Entry hits only: pass the first three to `receipt` to read the entry. |
| `SEARCH WARN`, `MEMORY WARN`, `STATS WARN` | stderr: a `.json` under `entries/` that is not a store entry was skipped. |

## Check notes

`memory verify` takes one `--root` and exits 1 on a finding. `--links` is
always required: `gate` fails on a `[[wikilink]]` that names no file or
frontmatter `name:`, and `info` only lists them. With `--links info`, also
give `--coverage` or `--frontmatter`. Globs match paths relative to `--root`.

```sh
printf '%s\n' 'A stray note about the harbor and its gulls.' > notes/stray.md
nova-cairn memory verify --root notes --links gate --coverage '*.md:index-*.md' --frontmatter '*.md' --exempt index- || echo "exit $?"
rm notes/stray.md
```

```text
VERIFY FAIL coverage stray.md: stem "stray" appears in no file matching index-*.md
VERIFY FAIL frontmatter stray.md: no name: in frontmatter
VERIFY FAIL gating=2 shown=2 info=0 coverage=1 frontmatter=1 links=gate
exit 1
```

`--coverage A:B`: every file matching A is named (file name without `.md`,
as a whole word) or linked in a file matching B, and every Markdown link in B
resolves. `--frontmatter <glob>`: every match has a `name:`; `--exempt
<prefix>` excuses files by base name. `--fail-max <n>` caps lines per kind
(default 20, `0` for all). A glob that matches nothing is refused. Findings go to stderr; a pass prints `VERIFY OK`.

## Scan for self-talk

`self-talk` reads files (`-` is stdin) sentence by sentence. It flags a
**STANDING** claim, a first-person sentence with a word of failure ("I cannot
check my own work"), and verdicts in neutral words, which the summary counts
as `installations`: RANKING ("my weakest instrument"), FORECLOSURE ("I will
never be a good planner"), VERDICT-IDIOM ("dead as a practice") and TRAIT ("I
always break the build"). A STANDING claim with a date like `2026-11-05`, or
the words `measured`, `that day`, `that night` or `first time`, is a record:
counted as dated, never flagged. A quotation does not excuse it.

```sh
printf '%s\n' 'I cannot check my own work.' 'My weakest instrument is patience.' 'On 2026-11-05 I cannot check my own work.' > review.txt
printf '%s\n' 'I will never be a good planner.' > rules.txt
nova-cairn self-talk --rule-doc rules.txt --skip draft.txt review.txt rules.txt draft.txt || echo "exit $?"
```

```text
SELFTALK FAIL review.txt:1: STANDING match="cannot check": I cannot check my own work.
SELFTALK FAIL review.txt:2: RANKING match="My weakest": My weakest instrument is patience.
SELFTALK RULEDOC rules.txt: rule documents: a finding here is a self-verdict to relocate, NEVER a reason to soften a rule
SELFTALK FAIL rules.txt:1: FORECLOSURE match="I will never be a good": I will never be a good planner.
SELFTALK FAIL files=2 claims=2 standing=1 installations=2 dated=1 shown=3
exit 1
```

The `SKIP`, `DATED` and closing `NOTE` lines are left out above. The run exits 1 on any finding. `--skip` and `--rule-doc`
take a base name and repeat; `--rule-doc` adds the `RULEDOC` banner above that
file's findings. `--skip` prints a `SELFTALK SKIP` line, and dated claims a
`SELFTALK DATED` line. `--max` caps findings per class (default 20, `0` for
all) and a cut list ends with a `MORE` line. A file that is not valid UTF-8
stops the run; skip it with `--skip`.

## Refusals

A refusal is one line on stderr, exit 2:
`nova-cairn <verb>: <what is wrong>; run: <what to run next>`. A conflict
prints a FAILED line and exits 1. Put flags before arguments.

| You see | Do next |
|---|---|
| Unknown or late flag, bad id or policy, no session, no `--links`, a glob that matches nothing | Follow the `-h` or the `open` command the line names. |
| `OPEN FAILED` (another policy or source) | Reuse the recorded settings, or use a new session id. |
| `APPEND FAILED` (same id, other words) | Run the printed `receipt`; use a new entry id. |
| Memory file unreadable or over 8 MiB | Run the printed command with `--exclude <file>`. |

## Guarantees and limits

- Every entry file and log line is synced before OK is printed. A kept entry
  is never overwritten; an edit by hand is not detected.
- `search` and `check` find shared words only. `self-talk` matches known
  shapes only.

How the tool is tested is in [SPEC.md](SPEC.md).
