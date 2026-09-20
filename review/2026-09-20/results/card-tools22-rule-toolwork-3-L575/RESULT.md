RESULT tools22-rule-toolwork-3-L575 sha=5298f6be12ea — does the code at this base do what docs/SPEC-TOOLWORK.md rule 3 says?
CONFORMS internal/hygiene/hygiene.go:166
SPEC docs/SPEC-TOOLWORK.md:575 rule 3
PKG internal/hygiene
ASK Verify every commit in `<base>..<head>` has both author and committer matching an allowed identity, and no commit is a merge, returning `identity at=<sha12>` on violation.
DECIDING LINES: internal/hygiene/hygiene.go:166-219 — `checkIdentity` calls `git log` with `%an%ae%cn%ce%P`, parses each row into sha/author/committer/parents, rejects merges via parent count > 1 at line 201-204, rejects mismatched author/committer at lines 206-216, and formats `At` as 12-char sha via `short(sha)` at lines 202/208/212.
GUARDED-BY internal/hygiene/hygiene_test.go:116 TestHygieneRejectsAForeignCommitter
GUARDED-BY internal/hygiene/hygiene_test.go:140 TestHygieneRejectsAForeignAuthor
GUARDED-BY internal/hygiene/hygiene_test.go:161 TestHygieneRejectsAMergeCommit
GREPS: `grep -rn "identity" --include='*.go' internal/ | grep -v _test.go` landed on `internal/hygiene/hygiene.go:166`; `grep -rn "hygiene" --include='*.go' internal/` found `internal/hygiene/hygiene.go` as the package; read the full file (561 lines) and full test file (794 lines).
Left owed: none
git status --short: (no output — repo tree is clean)