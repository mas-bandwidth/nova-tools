RESULT tools22-rule-tokens-4 sha=5298f6be12ea — does the code at this base do what docs/SPEC-TOKENS.md rule 4 says?
CONFORMS internal/tokens/repo.go:155
SPEC docs/SPEC-TOKENS.md:2410 rule 4
PKG internal/tokens
ASK An implementation must place the rules file parser and the single attribution function in repo.go, ensure every source reader calls that function for repo attribution (no reader carries its own regexp for this), and provide tests: a malformed rules line is an error naming it, demanded test 5 (attribution ladder), and a source test checking all readers use the shared function and none carries a repo-attribution regexp.
Deciding lines:
- `internal/tokens/repo.go:155` — `LoadRules`, the rules file parser
- `internal/tokens/repo.go:201` — `Attribute`, the one attribution function
- `internal/tokens/repo.go:238` — `AttributeInputs`, the convenience wrapper that calls `Attribute`, so readers reach attribution through it
- `internal/tokens/tokens_test.go:254` `TestAMalformedRulesLineIsNamed` — a malformed rules line (no tab) is an error naming the line
- `internal/tokens/tokens_test.go:145` `TestTheAttributionLadder` — the attribution ladder: first-match, other, inherit, unknown, remote
- `cmd/nova-tokens/demanded_test.go:205` `TestRule5RepoAttributionAndTheTwoNamedBuckets` — full demanded test 5: attribution across four messages, TOKENS DAY with unknown/other shares, and exit 2 without --repos
- `cmd/nova-tokens/contract_test.go:279` `TestOnlyRepoGoCarriesTheAttributionRule` — source test: every reader (claude.go, opencode.go, swarm.go, bus.go) calls `rules.Attribute`; only repo.go compiles a regexp for attribution; bus.go is exempted because its regexps are for the note grammar, not repo matching
GUARDED-BY internal/tokens/tokens_test.go:254 TestAMalformedRulesLineIsNamed
GUARDED-BY internal/tokens/tokens_test.go:145 TestTheAttributionLadder
GUARDED-BY cmd/nova-tokens/demanded_test.go:205 TestRule5RepoAttributionAndTheTwoNamedBuckets
GUARDED-BY cmd/nova-tokens/contract_test.go:279 TestOnlyRepoGoCarriesTheAttributionRule
Grep searches run:
- `grep -rn "\\.Attribute\\|\\.AttributeInputs" --include='*.go' internal/tokens/` — found all call sites in claude.go, opencode.go, swarm.go, bus.go
- `grep -rn '"regexp"' --include='*.go' internal/tokens/` — found only bus.go and repo.go; bus.go's regexps are for note grammar, not repo attribution (exempted by test)
- `grep -rn "func Test" --include='*_test.go' internal/tokens/` — all test names
- `grep -rn "carries.*regexp\|source.*test\|every.*source.*reader\|called from every" --include='*.go' .` — found TestOnlyRepoGoCarriesTheAttributionRule
- `sed -n '2038,2065p' docs/SPEC-TOKENS.md` — the demanded test 5 specification
Left owed: none
git status --short: (nothing)