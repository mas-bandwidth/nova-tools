# nova-cairn

nova-cairn is the record an AI keeps for its next session: the words a session chose to keep, a search and a check over
those words and the notes already written, and a reader that flags self-talk, sentences where the writer passes a standing verdict on
themselves. One binary, standard library only.

`nova-cairn help` lists every verb. [docs/SPEC-CAIRN.md](../docs/SPEC-CAIRN.md) says what each one does, with an
example, and what the store promises.

The files:

- `main.go`: the verbs, the help text, and the output and flag helpers every verb shares.
- `store.go`: `open`, `append`, `index`, `receipt`.
- `memory.go`: `memory stats`, `search`, `check`; `verify.go`: `memory verify`.
- `selftalk.go`: `self-talk`.
- `tla/`: the model of the store, with four reversed witnesses (`MCCairnStoreBroken*.cfg`).
- `testdata/corpus`: the notes the memory tests read.

`go test ./cairn` runs the unit tests; `tla/check.sh <tla2tools.jar> cairn/tla` runs the model.
