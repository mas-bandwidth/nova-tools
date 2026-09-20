RESULT tools22-rule-tokens-4-L2410 sha=5298f6be12ea
CONFORMS internal/tokens/repo.go:201
SPEC docs/SPEC-TOKENS.md:2410 rule 4
PKG internal/tokens
ASK `internal/tokens/repo.go` must contain the rules file parser (`LoadRules`) and a single attribution function (`Attribute`) called by every source reader; no reader may carry its own regexp for repo attribution; tests must cover a malformed line naming it, demanded test 5, and a verification that all readers use the single function.

Deciding lines:
- `internal/tokens/repo.go:153–155` — `LoadRules` is the rules file parser: "A malformed line is an error naming the line"
- `internal/tokens/repo.go:187` — "Attribute is THE statement of the rule, and every reader reaches a repo name through it"
- `internal/tokens/repo.go:201` — `func (r *Rules) Attribute(tokens []string, prev string) string` is the one attribution function; `AttributeInputs` at line 238 delegates to it
- `internal/tokens/repo.go:236–239` — "A reader that carried its own regexp would be the second copy of the rule, so the extraction is here too" — all readers call through this single path

All source readers (`claude.go:152`, `opencode.go:163`, `swarm.go:126`, `bus.go:441`) call `rules.Attribute` or `rules.AttributeInputs`; the only other files that import `regexp` are `repo.go` (the rule itself) and `bus.go` (note grammar — not repo attribution).

GUARDED-BY internal/tokens/tokens_test.go:254 TestAMalformedRulesLineIsNamed
GUARDED-BY cmd/nova-tokens/demanded_test.go:205 TestRule5RepoAttributionAndTheTwoNamedBuckets
GUARDED-BY cmd/nova-tokens/contract_test.go:279 TestOnlyRepoGoCarriesTheAttributionRule

Grep patterns run: `\.Attribute`, `\.AttributeInputs`, `LoadRules`, `regexp\.`, `func Test.*[Aa]ttrib`, `func Test.*[Rr]egexp` in `internal/tokens/` and `cmd/nova-tokens/`. Files read: `repo.go`, `tokens_test.go`, `bus.go`, `claude.go`, `opencode.go`, `swarm.go`, `provider.go`, `codex.go`, `contract_test.go`, `demanded_test.go`.

Left owed: none.