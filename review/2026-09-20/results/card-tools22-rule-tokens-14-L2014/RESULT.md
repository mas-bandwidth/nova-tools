RESULT tools22-rule-tokens-14-L2014 sha=5298f6be12ea
CONFORMS internal/tokens/dayfile.go:26
SPEC docs/SPEC-TOKENS.md:2014 rule 14
PKG internal/tokens
ASK The tool must write `nova-tokens v1` as the first line of every day file, and `check` must report a finding when a day file's first line is not `nova-tokens v1`.
  internal/tokens/dayfile.go:26: const Version = "nova-tokens v1"
  internal/tokens/dayfile.go:79-83: Render() writes Version as the first line of every day file
  internal/tokens/dayfile.go:280-281: ParseDayFile() returns a Finding{Line:1} when the first line is not Version+" "
  internal/tokens/check.go:108-109: Check() propagates ParseDayFile's findings (including version-line findings) into its result
  internal/tokens/sum.go:126-127: SumMonth returns a BadDayFile error for files whose Line:1 finding indicates a bad/missing version line
GUARDED-BY internal/tokens/tokens_test.go:40 TestAnUnversionedFileRefuses
grep'd for "nova-tokens v1" across all Go files; read dayfile.go, check.go, sum.go; read tokens_test.go
Left owed: none