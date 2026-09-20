RESULT tools22-rule-version-10-L89 sha=5298f6be12ea — does the code at this base do what docs/SPEC-VERSION.md rule 10 says?
GAP internal/update/cli.go:421
SPEC docs/SPEC-VERSION.md:89 rule 10
PKG internal/update (verb dispatched by cmd/nova-version via update.Main)
ASK An apply --sha implementation would need to build every cmd/* binary at a given SHA into a staging directory, verify they all share one stamp and match prior-set expectations, then atomically swap the --bin symlink to the staged set while writing source metadata; on failure it must roll back and leave the prior set untouched.

The spec text (docs/SPEC-VERSION.md:38-47, rule 5 of "nova-version moved and nova-update apply --sha") describes apply --sha as a build-mode that: reads no --file, takes no name, builds ./cmd/... at the revision under one stamp into a new staging dir, reads back every binary's version, verifies the exact postflight set (every expected tool at one stamp plus any prior-set tools now absent), switches the --bin link to the staged set in one step, records repository/revision/build host/go version/time into the manifest, and verified-rollbacks to the prior set on any failure. "--sha given with --file is a refusal naming both flags."

The existing apply() function at internal/update/cli.go:421 implements ONLY file-based apply: it loads entries from --file manifest, finds the named entry, runs its Apply argv with {version} substitution, checks installed version matches target. It has no --sha flag (--sha is not defined in options struct lines 30-36 or registered at lines 210-221). No code anywhere builds binaries from source, no code swaps symlinks in --bin, no code writes repository/revision/build host/go version/time metadata. The only mention of "apply --sha" is snapverb.go:189 where a snapshot error message suggests the user "rebuild the set under one stamp with nova-update apply --sha" — a feature referenced but not implemented.

No test TestApplySha* exists in any *_test.go under internal/update/ (grep confirmed zero results). The only apply test is TestApplyOnlyNamedEntryAndExactTarget at update_test.go:324 which covers the --file mode only.

GREPS RAN:
  grep -rn "apply.*--sha\|--sha.*apply\|applySha\|ApplySha\|atomically" --include="*.go" .
  grep -rn "func Test" --include="*_test.go" /repo/internal/update/ | grep -i "apply\|sha\|atom"
  grep -rn "sha" --include="*.go" /repo/internal/update/cli.go
  cat -n /repo/internal/update/cli.go (full file read, 486 lines)

git status --short
(no output)
