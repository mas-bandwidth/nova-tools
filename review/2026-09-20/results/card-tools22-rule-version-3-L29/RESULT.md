RESULT tools22-rule-version-3-L29 sha=5298f6be12ea — does the code at this base do what docs/SPEC-VERSION.md rule 3 says?
ABSENT
SPEC docs/SPEC-VERSION.md:29 rule 3
PKG cmd/nova-version
ASK The moved command must print "MOVED OK from=<sha> to=<sha> added=<n> deleted=<n> renamed=<n> verbs=<n> file=<path>" to stdout when comparing two commits.
grep -rn "MOVED OK" --include='*.go' . returned nothing; grep -rn "moved" --include='*.go' internal/update/ returned no command implementation; internal/update/cli.go line 79-83 lists only snapshot, diff, report, send, help for nova-version.
Left owed

git status --short
