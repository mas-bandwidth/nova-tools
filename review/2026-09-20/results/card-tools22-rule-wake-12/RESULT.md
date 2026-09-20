RESULT tools22-rule-wake-12 sha=5298f6be12ea
CONFORMS cmd/nova-wake/main.go:346
SPEC docs/SPEC-WAKE.md:4265 rule 12
PKG internal/wake
ASK The binary must provide a `help` verb that prints its usage banner to stdout and exits 0, rather than extracting comments from its own source file with `sed`.
QUOTE cmd/nova-wake/main.go:346-348
    case "help", "-h", "--help":
        fmt.Fprint(stdout, usage)
        return 0
GUARDED-BY cmd/nova-wake/firstrun_test.go:44 TestUsageBannerExamplesRun (plus cmd/nova-wake/issue1451_test.go:18 TestIssue1451EveryRefusalNamesTheDoor)
GREPS:
  grep -rn "help" --include='*.go' internal/wake/ → 0 matches (no help code lives in internal/wake, all in cmd/nova-wake)
  grep -rn "Usage" --include='*.go' internal/wake/ → 0 matches
  grep -rn "help" --include='*.go' cmd/nova-wake/ → confirmed the help case at main.go:346
  sed -n '4245,4291p' docs/SPEC-WAKE.md → rule 12 at line 4265
Left owed: none

git status --short: (clean, nothing printed)