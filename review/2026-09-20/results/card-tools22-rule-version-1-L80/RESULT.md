RESULT tools22-rule-version-1-L80 sha=5298f6be12ea — does the code at this base do what docs/SPEC-VERSION.md rule 1 says?
ABSENT
SPEC docs/SPEC-VERSION.md:80 rule 1
PKG cmd/nova-version
ASK For rule 1 to be true, `nova-version moved` must read each revision's built binaries' `help` output (never a hand-written list), so a flag that `--to`'s help prints but `--from`'s does not is reported `added=<flag>`, and a flag no help prints is never announced.

Nowhere to quote deciding lines: the `moved` verb does not exist in this tree, so there is no behavior to conform or gap.

Where I looked:
- `cmd/nova-version/main.go:19` — delegates everything to `internal/update.Main`.
- `internal/update/cli.go` — the whole verb dispatch in `Run` (cli.go:138-200). The only verbs are help/version, `snapshot` (cli.go:165), `diff` (cli.go:171), `send` (implied `report`, cli.go:177), `release`, `adoption`, and `report`/`check`/`apply`/`watch` (cli.go:200). `apply` is `apply --file <path> <name>` (cli.go:215), never `apply --sha`. No `moved` case exists; it falls through to the `unknown verb` refusal at cli.go:200.
- `internal/update/cli.go:79-83` — `versionVerbs` help banner prints only snapshot/diff/report/send, contradicting the spec's own "help prints these four lines, byte for byte" header (SPEC-VERSION.md:7-12) which lists `nova-version moved` and `nova-update apply --sha`. The code's help is pinned to SPEC-UPDATE.md's verbs block by `TestHelpIsTheSpecsVerbsBlock` (internal/update/rules_test.go:351), so the divergence is intentional and enforced.
- No `TestMoved*` test anywhere: `grep -rn "TestMoved" --include='*_test.go' .` returned nothing.

Greps run:
- `grep -rn "moved\|MOVED" internal/update/*.go` — only unrelated "moved the remote" prose in join_test.go.
- `grep -rn "\"moved\"" --include='*.go' .` — nothing.
- `grep -rn "TestMoved" --include='*_test.go' .` — nothing.
- `grep -rn "added=" --include='*.go' .` — only nova-review/review prose, no `added=` output.
- `grep -rn "MOVED OK\|added=<" --include='*.go' .` — nothing.
- `grep -rn "\"apply\"\|\"moved\"" --include='*.go' internal/ cmd/` — apply only as `apply --file` (update) and nova-sandbox/review apply.
- `grep -rln "decide" --include='*.go' .` — in internal/update only read.go:33, an unrelated comment; no `--decide` flag anywhere near nova-version.

Files read: cmd/nova-version/main.go; internal/update/cli.go (dispatch + help banners); docs/SPEC-VERSION.md (lines 1-200); internal/update/rules_test.go (verb-block test); ls cmd/nova-version/ (no moved file; only main.go + tests).

`spec > code`: SPEC-VERSION.md's whole `moved`/`apply --sha` section (rules 1-12 and red tests 1-11) describes verbs the code does not implement at all — the spec's header banner and the code's help already disagree, and the code is the one pinned to a different spec (SPEC-UPDATE.md).

Left owed: nothing.