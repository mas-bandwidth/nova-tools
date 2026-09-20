RESULT tools22-rule-tokens-3-L1978 sha=5298f6be12ea
CONFORMS internal/tokens/tokens.go:216
SPEC docs/SPEC-TOKENS.md:1978 rule 3
PKG internal/tokens
ASK The implementation must be a single Go binary with one reader function per source kind, one fold function (`Folder.Add`), and tests that exercise the binary's behavior in process — the opposite of the prototype's Python-inside-zsh, multi-script, `--tables`-as-second-code-path architecture.
QUOTE internal/tokens/tokens.go:216 `func (f *Folder) Add(label string, m Message)` — the one fold function
QUOTE internal/tokens/tokens.go:2-4 `one message shape every source produces, the one fold over a stream of them` — package doc stating the architecture
QUOTE internal/tokens/claude.go:94 `func ReadClaude(label, dir string, rules *Rules) *Source` — one reader for claude kind
QUOTE internal/tokens/opencode.go:85 `func ReadOpenCode(label, dbPath, scratch string, timeout time.Duration, rules *Rules) *Source` — one reader for opencode kind
QUOTE internal/tokens/swarm.go:40 `func ReadSwarm(label, pool string, rules *Rules) *Source` — one reader for swarm kind
QUOTE internal/tokens/bus.go:193 `func ReadBus(dir string, rules *Rules, at time.Time) []*Source` — one reader for bus kind
QUOTE internal/tokens/provider.go:95 `func ReadProvider(kind, name, path string, _ *Rules) *Source` — one reader for provider kind
QUOTE cmd/nova-tokens/helpers_test.go:29 `func invoke(t *testing.T, args ...string) result { ... return invokeAt(t, foldStamp, args...) }` — tests run the binary's `run()` in process
GUARDED-BY cmd/nova-tokens/bounded_test.go:138 TestFoldIsBoundedAtTheLargestPlausibleState (exercises full fold pipeline through `run()`)
GUARDED-BY cmd/nova-tokens/demanded_test.go:649 TestIssue268AFoldKeepsARowNoDeclaredSourceWrote (exercises fold merge through `run()`)
GROPS:
  `grep -rn "func Read" --include='*.go' internal/tokens/` — found one reader per source kind
  `grep -rn "func.*Fold\|Folder\|\.Add" --include='*.go' internal/tokens/` — found Folder.Add as the one fold function
  `grep -rn "func Test" --include='*_test.go' internal/tokens/ cmd/nova-tokens/` — found tests exercising fold through `run()`
  `grep -rn "exec.Command\|os/exec" cmd/nova-tokens/*_test.go` — no subprocess-based tests; all test through `run()` in process
  `ls internal/tokens/` — confirmed files for each reader and fold
Left owed: none
git status --short: