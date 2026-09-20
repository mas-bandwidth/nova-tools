RESULT tools22-rule-version-9-L62 sha=5298f6be12ea
ABSENT
SPEC docs/SPEC-VERSION.md:62 rule 9
PKG cmd/nova-version / internal/update
ASK A missing `--sha`, `--repo` or `--bin` is refusing to guess, naming it; an unresolved revision names it and the fetch; a `cmd/*` that does not build names the package and the revision.
The `apply --sha` verb is nowhere in the binary: no `--sha`, `--repo` or `--bin` flag on apply, no code that builds `./cmd/...` from a git revision into a staged bin dir, no postflight stamp verification of a whole set.
The spec header (docs/SPEC-VERSION.md:11) declares the verb line:
  nova-update apply --sha <sha> --repo <dir> --bin <dir> [--timeout <d>]
and rules 4-12 define its full behaviour, but internal/update/cli.go's apply verb takes only --file and --version (cli.go:213-216) and writes manifest-based per-tool updates via the function at cli.go:421.
Three greps proved absence:
  grep -rn "apply.*--sha\|--sha" --include='*.go' .
  → only hit snapverb.go:189 which mentions "nova-update apply --sha" in an error message text suggesting what to do; no code handles it.
  grep -rn "\"sha\"\|'sha'\|\b--sha\b\|\.sha\b" --include='*.go' .
  → no match in update package, only unrelated SHA references in merge/release/ci packages.
  grep -rn 'func Test' --include='*_test.go' . | grep ApplySha
  → zero results; tests 5-8 in the spec's red-test section are unimplemented.
The closest thing is release build (internal/release/build.go) but its flags are --source/--version/--out/--platform and it produces artifact directories, not a stamped --bin staging directory.

GUARDED-BY UNGUARDED (also GAP: rules 4-8 of SPEC-VERSION.md share this same absent implementation)

git status --short
