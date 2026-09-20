RESULT tools22-rule-toolwork-6-L589 sha=5298f6be12ea — does the code at this base do what docs/SPEC-TOOLWORK.md rule 6 says?
CONFORMS internal/hygiene/hygiene.go:379
SPEC docs/SPEC-TOOLWORK.md:589 rule 6
PKG internal/hygiene
ASK An implementation must scan every added line in a diff against key shape patterns from an embedded data file, never against key values, report secret findings with path/line only (matched text never printed), quarantine the job directory, and write one HUMAN line.
DECIDING LINES
- internal/hygiene/hygiene.go:379-419 `checkAddedLines`: scans added lines against shapes via `s.re.MatchString(added)`, creates `Finding{Token: "secret", At: at, Why: "an added line with the shape of a ...; the text is not printed"}` at line 409-414
- internal/hygiene/hygiene.go:47-55 Finding struct and String: Token, At, Why only — never prints matched text
- internal/hygiene/data.go:15-16 embeds keyshapes.txt; data.go:75-94 parseKeyShapes loads patterns
- internal/hygiene/keyshapes.txt: all listed shapes (PEM, age-secret-key, forge tokens, provider key prefixes)
- internal/pulse/harvest_secret.go:170-188 `refuse`: prints HARVEST REFUSED secret-shape, calls quarantineJob at line 179
- internal/pulse/harvest_secret.go:239-251 `secretHumanLine`: writes one HUMAN line with shapes, quarantine path, remedy
- internal/keyshape/keyshape.go:88-103 Finding.String: "file=%s line=%d shape=%s" — never carries matched value
GUARDED-BY internal/hygiene/hygiene_test.go:363 TestHygieneRejectsAKeyShapeAndNeverPrintsIt
GUARDED-BY internal/pulse/harvest_secret_test.go:54 TestASecretQuarantinesAndNeverDeletes
GUARDED-BY internal/pulse/harvest_secret_test.go:85 TestASecretWritesOneHumanLine
GUARDED-BY cmd/nova-check/hygiene_test.go:147 TestHygieneVerbNeverPrintsTheKey
GREPS: "reason=secret" across *.go → cmd/nova-check/hygiene_test.go:160, internal/pulse/harvest_secret.go:248; "secret" in internal/hygiene/*.go → hygiene.go:10,140-146,362-365,409-414, hygiene_test.go:363-387,389-398,402-416; keyshapes.txt read in both internal/hygiene/ and internal/keyshape/; internal/pulse/harvest_secret.go read in full
Left owed: nothing — the code fully implements the rule.
git status --short