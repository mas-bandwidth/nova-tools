RESULT tools22-pre-2005-r2 sha=5298f6be12ea — a pre-read of mas-bandwidth/nova-tools#2005 at head d5ead3ec5843: nova-play view: browse shared moments without rewriting the record (#223)

PREREAD 2005 claims=10 proven=9 unproven=1 defects=1 high=0

PR 2005
HEAD d5ead3ec58438b2a70f83b7b63ee5efd8422476a
BASE dev
MERGE-BASE 3666f40487a97447c1632e4bebf2a40585077e4f
BEHIND 5
FILES 3 production, 1 test
LINES +484 -0

## CLAIMS

1. `view` renders an explicitly selected sample of Markdown records into a static timeline, one card per record linked back to its source, without writing anything (browsing never edits a record).
   PROVEN-BY internal/play/view_test.go:92-108 TestViewRendersTwoLayoutsWithoutRewritingSources — checks every source file hash is unchanged after View, and each source path appears as `source=` in output.

2. `--exclude` takes a glob matched against each path as given and its base name (repeatable); `--max` caps cards printed (default 20, 0 prints every card); the summary line always carries totals.
   PROVEN-BY internal/play/view_test.go:55,79,99-102 — calls View with `{"draft*"}`, max=0, checks excluded record absent from output.

3. A card shows the record's date, author, kind (echoed, never inferred) and source.
   PROVEN-BY internal/play/view_test.go:62-69 — checks `date=2026-09-18`, `author=Emma`, `kind=human`, source filename, and title in output.

4. A missing or unparseable date or author prints as `unknown` rather than a guess.
   PROVEN-BY internal/play/view_test.go:86-88 — asserts `author=unknown` and `date=unknown` present in output for the unfiled record.

5. Two layouts are read: a leading `---` fence with lowercase `author:`, `date:`, `kind:`, `supersedes:` keys, and `Author:`, `Date:`, `Kind:`, `Supersedes:` trailer keys anywhere else in the file.
   PROVEN-BY internal/play/view_test.go:28-50,62-69 — creates a frontmatter file and a trailer file; both appear with correct metadata in output.

6. Cards sort chronologically with undated records last.
   PROVEN-BY internal/play/view_test.go:91-99 — checks moment-one (2026-09-18) before moment-two (2026-09-19) before unfiled (unknown date) in output.

7. A `supersedes:` value stays on the card so corrections remain discoverable.
   PROVEN-BY internal/play/view_test.go:71 — checks `"supersedes=The\x20brass\x20fitting"` in output.

8. An excluded record is never even opened.
   UNPROVEN — test verifies the excluded record is absent from output, but does not verify the file was never read (no syscall tracing or access-count check).

9. A summary is listed beside the record, never in place of it.
   PROVEN-BY internal/play/view_test.go:55-78 — the trailer file carries `Supersedes: The brass fitting` and gets its own CARD line; the superseded file is a separate record that also gets its own CARD line.

10. No default file, no directory walk — every record is named on the command line.
    PROVEN-BY-EXISTING cmd/nova-play/main.go:217-219 — cmdView refuses zero arguments.

## DEFECTS

DEFECT low internal/play/view.go:154 — cardTitle searches all lines including frontmatter, so a `#` placed inside YAML frontmatter would be picked as the title before any body heading — extremely unlikely in real records, but the function could receive `rest` (body after fence) instead of `lines`.

## QUESTIONS

1. The doc says "a summary is listed beside the record, never in place of it" — is a record with `kind: summary` expected to share its `supersedes` target's date in the sort order, or is it sorted by its own (possibly different) date?

2. The `--exclude` glob is validated with `path.Match` (slash-only separator). What is the expected behavior on Windows, where users might type `.\moments\*.md` — should `filepath.Match` be used instead?

3. Are there documented output format contracts (the `VIEW OK` and `CARD` key=value lines) that tools downstream of `nova-play view` parse? The existing `read` doc shows exact output; the `view` section describes behaviour but not wire format.

## Left owed

None — all 4 changed files read in full; oneline and bounded packages read for context.

git status --short: (nothing, clean working tree)
git rev-parse HEAD: d576bf6bbabb39068096a97b4560de9b5e245970===FILE=== card-tools22-pre-2005-r2/usage.tsv
job	attempt	started	ended	rc	provider	model	tokens_in	tokens_out	cache_write	cache_read	reasoning	usd
card-tools22-pre-2005-r2	1	2026-09-20T19:26:51Z	2026-09-20T19:42:10Z	0	openrouter	deepseek/deepseek-v4-flash	195737	8651	0	805120	20545	0.0148
