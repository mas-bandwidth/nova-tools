RESULT tools22-rule-tokens-12-L2009 sha=5298f6be12ea
CONFORMS internal/tokens/bus.go:19
SPEC docs/SPEC-TOKENS.md:2009 rule 12
PKG internal/tokens
ASK The tool reads the bus checkout as files without git pull/fetch and prints freshness by writing `at=` timestamp and `build=` id on the TOKENS FOLD line and in every written day file's version line.
SEE internal/tokens/bus.go:19 — "The tool reads the checkout AS FILES. It never pulls, fetches, pushes, runs git, or talks to a network"
SEE internal/tokens/dayfile.go:81-82 — `nova-tokens v1 day=<d> at=<stamp> build=<id>` on every day file's first line
SEE cmd/nova-tokens/main.go:546 — `TOKENS FOLD at=%s build=%s` prints the stamp and build
SEE cmd/nova-tokens/version.go:3 — "A day file carries the build id of the fold that wrote it (rule 12)"
GUARDED-BY cmd/nova-tokens/demanded_test.go:892 TestRule12TheToolStampsAndNoFlagSetsIt
Left owed
$ git status --short
(no output — tree is clean)