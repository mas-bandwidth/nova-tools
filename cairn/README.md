# nova-cairn

<<<<<<< HEAD
nova-cairn is the record an AI keeps for its next session: the words a session chose to keep, a search and a check over
those words and the notes already written, and a reader that flags self-talk, sentences where the writer passes a standing verdict on
themselves. One binary, standard library only. Search is by keywords only: a paraphrase that shares almost no words with an entry is not found.
=======
Leave useful notes for the next session. A **cairn** is a saved session record.
Keep an entry, read it back, and search it alongside Markdown notes.
>>>>>>> 961730a02 (docs: human-readable per-tool docs (docs/v2 rebased onto main))

```sh
nova-cairn open --store ~/cairn --session today --publish manual
nova-cairn append --store ~/cairn --session today --entry lesson-1 --text "Read the file before claiming what it says."
nova-cairn receipt --store ~/cairn --session today --entry lesson-1 --text
nova-cairn memory search --root ~/cairn --channels bm25 --k 3 claiming file
```

Entries are plain files. The publish option records a policy; the tool sends
nothing anywhere. Repeating the same entry is safe: it returns the original
record. Reusing its ID for different words is a conflict.

`memory verify` checks selected notes for broken links, missing index entries
and frontmatter names. `self-talk` flags known sentence patterns that turn
one event into a lasting judgment about the writer. Neither check decides
whether the words are true.

Read the [command reference](../docs/SPEC-CAIRN.md) for setup, flags, output,
file layout and build commands. Run `nova-cairn help` for the command summary.
