# nova-play — specification, draft 1

Rowan, 2026-09-16: *"Read a story, essay, paper or specification together. Keep the original text in view, with each human and AI friend's questions and comments attributed beside the same passages."*

One tool, three verbs, all mechanical. **The tool makes no model call**: it anchors notes to passages in a source text, stores them in a sidecar file, and detects when the source has changed so notes are not silently reassigned.

- `nova-play annotate --source <file> --author <name> --passage <text> --note <text>` anchors a note to an exact passage in the source.
- `nova-play read --source <file>` lists all notes with their anchor status.
- `nova-play reply --source <file> --id <note-id> --author <name> --body <text>` replies to an existing note.
- `nova-play version` / `nova-play help`

## The smallest useful experiment

Two participants, one text. Emma and Stella each annotate the same passage, answer each other, and return in a later session. When the source text is edited between sessions, the tool says `ANCHOR STALE` instead of silently moving notes to the wrong place. Notes live in a sidecar file (`<source>.notes`) beside the source, so the original text is never modified.

## Anchor discipline

Every annotation records the SHA-256 of the source file at the time it was written. On each subsequent operation, the tool recomputes the hash and compares. If they differ, the tool refuses with `ANCHOR STALE` and names both hashes — the stored one and the current one — so the operator can decide whether to migrate notes, discard them, or revert the source.

This is the difference between a note that says *"this passage"* and a note that says *"line 42"*: one survives reflow and renumbering, the other breaks silently. The passage-text anchor is human-readable; the hash anchor is machine-verifiable. Both are required.

## The sidecar file

Annotations for `story.txt` live in `story.txt.notes`, a plain text file beside the source. The format is line-oriented and **versioned**. Everything this tool writes is version 2:

```
ANCHOR <source-path> <sha256>
VERSION 2
NOTE id=<sha12> author=<author> created=<rfc3339>
PASSAGE <escaped passage text>
BODY <escaped annotation text>
REPLY id=<sha12> author=<author> created=<rfc3339>
REPLY_BODY <escaped reply text>
```

`ANCHOR` is always the first line and `VERSION` always the second. Notes follow in the order they were written; each note owns the replies listed under it. Blank lines and lines beginning with `#` are ignored.

**Every record is exactly one physical line.** `PASSAGE`, `BODY` and `REPLY_BODY` carry the whole value on their own line, with three escapes and no others: `\\` for a backslash, `\n` for a line feed, `\r` for a carriage return. A backslash before any other byte is a literal backslash. Nothing else in the value is touched — a trailing space, a tab, a `"`, and prose that happens to begin with `NOTE`, `PASSAGE`, `BODY`, `REPLY` or `REPLY_BODY` all survive unchanged, because a record boundary can only ever be the start of a line. Reading a value is: take everything after the single space that follows the keyword, then unescape.

**The `author` field is framed.** A bare token is written as-is. An author that is empty, or that contains a space, a double quote, a backslash or an unprintable rune, is written as a Go-quoted string (`strconv.Quote`): `author="Ada \"The Reader\" Lovelace"`. The two shapes never collide, since a bare token can contain neither a quote nor a backslash, so the reader tells them apart by the first byte. Field splitting inside a quoted value is escape aware: a backslash escapes the next byte, so an escaped quote does not end the value.

**The source path may contain spaces.** The `ANCHOR` line is split at its *last* space: everything before it is the path, everything after it is the hash.

The note ID is the first 12 hex characters of SHA-256 of `author + passage + note`. The reply ID is the same over `author + body`. This makes IDs deterministic and collision-resistant without requiring a counter or database.

### Reading a legacy sidecar (version 1)

A sidecar whose second line is not `VERSION` is version 1 — the shape written before escaping existed. It is still read, by the version-1 rules and only those:

- There is **no escaping**. A backslash in a stored value is a literal backslash and is never reinterpreted.
- A line that does not begin with a known keyword is a **continuation** of the value above it, joined with a line feed. This is how version 1 stored multi-line text, and it is why version 1 cannot store a line of prose that begins with a keyword: such a line reads back as a new record. That ambiguity is the reason for version 2.
- An `author` value is never unquoted, and an unquoted multi-word author **runs on** to the next `key=value` token, so `author=Test Reader created=...` reads back as `Test Reader`.

Reading never writes: `nova-play read` leaves a version-1 file byte for byte as it found it.

**What the next write does.** The first operation that writes — an `annotate` or a `reply` on that source — rewrites the whole sidecar as version 2. The upgrade is in place, one way, and keeps no backup:

- The `ANCHOR` line is carried over unchanged. An upgrade is not a re-anchor, and it does not by itself make an anchor stale or fresh.
- `VERSION 2` is inserted as the second line.
- Every value read under the version-1 rules is written back under the version-2 rules, escaped onto one line, with authors framed. The values themselves are not reinterpreted: what version 1 said the file held is exactly what version 2 stores.
- Once upgraded the file is stable: writing the same store again produces identical bytes.

An operation that **refuses** — a stale anchor, a missing source, an unknown note ID — writes nothing, so a refused operation also leaves a version-1 sidecar untouched.

## Exit codes

Per SPEC.md conventions: **0** the verb ran successfully; **1** anchor conflict or note not found; **2** bad invocation.

```
ANNOTATE OK id=<sha12> author=<name> created=<timestamp>
ANNOTATE FAIL <reason>
READ OK source=<path> notes=<n>
READ ANCHOR STALE source=<path> stored=<sha> current=<sha> notes=<n>
NOTE id=<sha12> author=<name> created=<timestamp>
  PASSAGE <text>
  BODY <text>
  REPLY id=<sha12> author=<name> created=<timestamp>
    BODY <text>
REPLY OK id=<sha12> author=<name> created=<timestamp>
REPLY FAIL <reason>
```

## What this deliberately is not

- **Not a reader or viewer.** The tool stores and retrieves notes; it does not render the source text or provide a reading interface. The source stays where it is, opened in whatever reader the participants choose.
- **Not a publishing platform.** Notes are local to the machine that creates them. Export (`play.Export`) returns a portable markdown string, but nothing here pushes notes to a server or shares them automatically.
- **Not EPUB/PDF aware.** The source is a plain text file. EPUB and PDF support are deferred until practice demonstrates a need.
- **Not a notification system.** The tool has no always-on runtime, no daemon, no push. Participants check for new notes by running `read`.

## Open questions

1. **Multi-participant note sharing.** The sidecar file is local. How do Emma and Stella see each other's notes? Options: a shared directory (git, a mounted volume, a bus lane), or manual file exchange. The experiment should try both before deciding.
2. **Passage matching tolerance.** Currently exact string match. Should fuzzy matching, line-number fallback, or paragraph-level anchoring be added when exact matches become fragile under editing?
3. **Note migration.** When a source changes and the anchor goes stale, should there be a `migrate` verb that re-anchors notes to the best matching passage in the new source?
