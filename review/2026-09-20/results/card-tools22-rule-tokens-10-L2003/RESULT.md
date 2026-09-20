RESULT tools22-rule-tokens-10-L2003 sha=5298f6be12ea — does the code at this base do what docs/SPEC-TOKENS.md rule 10 says?
CONFORMS internal/tokens/repo.go:201
SPEC docs/SPEC-TOKENS.md:2003 rule 10
PKG internal/tokens
ASK The path-to-repo matching rule must live in exactly one place — one caller-written rules file (no built-in list) and one attribution function — with every source reader reaching that same function, so a repo like `serialize`, `rowan` or `freddy` matches identically whichever source reads it.
CONFORMS internal/tokens/repo.go:201
Deciding lines:
- internal/tokens/repo.go:12-17: "Repo attribution: ONE rule, ONE function, ONE statement. // The prototype had two copies of this rule in two scripts with two different regexp // tables, and they disagreed about `serialize`, `rowan` and `freddy`. So the table is the // caller's file — there is no built-in list, and `--repos` is required — and the ladder // below is written once and called from every source reader."
- internal/tokens/repo.go:187: "Attribute is THE statement of the rule, and every reader reaches a repo name through it:"
- internal/tokens/repo.go:201: "func (r *Rules) Attribute(tokens []string, prev string) string {" — the one function; `LoadRules` (repo.go:155) reads the caller's file and `regexp.Compile` (repo.go:175) is the only rules-table compilation in the package.
- Every reader routes through it: claude.go:152,154; opencode.go:163,167; swarm.go:126; bus.go:441 — all call `rules.Attribute`/`rules.AttributeInputs`. No source carries its own regexp table.
GUARDED-BY internal/tokens/tokens_test.go:145 TestTheAttributionLadder
Greps: grep -rn "serialize\|rowan\|freddy" --include='*.go' . ; grep -rn "\.Attribute\|AttributeInputs\|LoadRules\|regexp\.Compile\|regexp\.MustCompile" --include='*.go' internal/tokens/ | grep -v _test.go ; grep -rn "repo\|Repo\|rules" --include='*.go' internal/tokens/{claude,opencode,codex,grok,swarm,bus,cost,provider}.go ; grep -rn "func Test" --include='*_test.go' internal/tokens/ | grep -i "repo\|attribut\|rule"
Left owed: none.