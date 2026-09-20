RESULT tools22-rule-version-11-L90 sha=5298f6be12ea
ABSENT
SPEC docs/SPEC-VERSION.md:90 rule 11
PKG cmd/nova-version
ASK The stamp-check step of `apply --sha` must verify that every built binary's source metadata (repository, revision, dirty flag, build host) matches the manifest, exiting 2 naming the binary that disagrees or has missing metadata.
I looked in cmd/nova-version/ (main.go and all test files), internal/update/ (cli.go, snapverb.go, manifest.go, all test files), and internal/release/ (install.go, all test files). There is no `apply --sha` verb, no `TestStampCheckAlsoVerifiesSourceMetadata` test, no `--sha` flag, no stamp-check source-metadata verification code anywhere. The only mention of `apply --sha` in the tree is a remedy message in internal/update/snapverb.go:189. None of the eleven test names from the spec section (TestMoved*, TestApplySha*, TestStampCheck*) exist in the codebase.
Left owed

`git status --short` (in repo/, prints nothing)