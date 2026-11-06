# nova-cairn: tests and models

The [guide](README.md) is how to use the tool. This page is for changing it.
Build and test commands for every tool are in [TESTING.md](../docs/TESTING.md).

The store is modelled in `cairn/tla/CairnStore.tla`: an open, an open
that crashes before its log line, an append, and an append that crashes after
its link. TLC checks that an entry is never overwritten, that the same words
twice make one entry, that an OK is printed only after the data is on disk,
that opening a session twice with the same policy changes nothing, and that a
session has one policy. Four broken cases must fail.

Run the models with TLC, with Java on PATH and the jar pinned in
[tests/tla/tla2tools.sha256](../tests/tla/tla2tools.sha256), at an absolute path:

```sh
tests/tla/check.sh /opt/tla/tla2tools.jar cairn/tla
```

Each line starts with `ok` when the case came out as expected:
`MCCairnStore.cfg` passes and each `Broken` configuration ends in a violation.
