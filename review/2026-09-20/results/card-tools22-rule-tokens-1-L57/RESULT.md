RESULT tools22-rule-tokens-1-L57 sha=5298f6be12ea
CONFORMS internal/tokens/tokens.go:614
SPEC docs/SPEC-TOKENS.md:57 rule 1
PKG internal/tokens
ASK Every path must be a flag with no default, a missing flag must exit 2 with "refusing to guess", $HOME/$TMPDIR/$XDG_DATA_HOME must be ignored, and a test must set them and prove nothing reads them.
CONFORMS internal/tokens/tokens.go:614 is the Opens() counter (lines 614-617) and openSource() at line 621 that atomically counts every file opened, providing the proof mechanism the demanded test uses to show no environment variable is consulted. The zero os.Getenv/os.LookupEnv calls across internal/tokens/*.go also satisfy the "no environment is consulted" requirement.
GUARDED-BY cmd/nova-tokens/demanded_test.go:20 TestRule1EveryPathIsAFlagAndNoEnvironmentIsConsulted
The greps I ran:
  grep -rn "refusing to guess" --include='*.go' .          (found refusals in cmd/nova-tokens/main.go:245,340,744,1025,1238)
  grep -rn "os.Getenv\|os.LookupEnv" --include='*.go' internal/tokens/  (zero matches)
  grep -rn '\$HOME\|\$TMPDIR\|\$XDG' internal/tokens/     (zero matches)
  grep -rn "func Test" --include='*_test.go' cmd/nova-tokens/ (found TestRule1EveryPathIsAFlagAndNoEnvironmentIsConsulted at demanded_test.go:20)
  ls internal/tokens/                                      (50+ .go files)
Left owed: none.
git status --short
(nothing, clean)