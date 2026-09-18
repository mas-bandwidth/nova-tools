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

Annotations for `story.txt` live in `story.txt.notes`, a plain text file beside the source. Its format is line-oriented:

```
ANCHOR <source-path> <sha256>
NOTE id=<sha12> author=<name> created=<rfc3339>
PASSAGE <exact passage text>
BODY <annotation text>
REPLY id=<sha12> author=<name> created=<rfc3339>
REPLY_BODY <reply text>
```

The note ID is the first 12 hex characters of SHA-256 of `author + passage + note`. The reply ID is the same over `author + body`. This makes IDs deterministic and collision-resistant without requiring a counter or database.

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
