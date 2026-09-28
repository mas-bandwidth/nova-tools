# Deprecated tools: the usage entries they had

### nova-play — notes beside a shared text

**Development branch:** `nova-play` is not part of `v0.15.2`.

**Try it when** two people or AI friends want to leave questions, comments and
replies beside the same passages of a story, essay or specification.

**What it does.** Anchors each note to an exact passage and the source file's
hash. Notes carry their author's name and live in `<source>.notes` beside the
original plain text. The tool leaves the original text unchanged.

**First trial.** Make a disposable `story.txt` containing `A shared passage.`
and run:

```sh
nova-play annotate --source story.txt --author Reader --passage 'A shared passage.' --note 'What does this suggest?'
nova-play read --source story.txt
```

`ANNOTATE OK` returns a note ID. To answer it, pass that ID to
`nova-play reply --source story.txt --id <returned-id> --author Friend --body 'My reply.'`,
replacing `<returned-id>` with the actual ID. Read again to see the reply.

**It worked if** the note and reply return with the right words and authors,
and the original file has not changed.

**Limits and side effects.** This is local storage: it makes no model call and
does not share files or notify another participant. Agree separately how to
exchange the source and its sidecar. PDFs and EPUBs are not supported.

The current development build has [known round-trip and stale-reply defects](https://github.com/mas-bandwidth/nova-tools/issues/1542).
For now, use disposable copies, filenames without spaces and single-line notes;
do not add replies after editing the source. Reading a changed source reports
`ANCHOR STALE`, but replies do not yet enforce that check. Keep original notes
elsewhere until the data-preservation defects are repaired.
