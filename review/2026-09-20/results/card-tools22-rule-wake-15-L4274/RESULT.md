RESULT tools22-rule-wake-15-L4274 sha=5298f6be12ea — does the code at this base do what docs/SPEC-WAKE.md rule 15 says?
GAP internal/wake/entry.go:210
SPEC docs/SPEC-WAKE.md:4274 rule 15
PKG internal/wake
ASK The code must accept bare PR numbers via `--prs` paired with a single `--repo`, AND entry names must include their repository (the latter is enforced; the former is absent).

Deciding lines:

internal/wake/entry.go:210-213 — SplitEntry enforces that entry names include the repo:
```go
func SplitEntry(name string) (repo, number string, err error) {
	repo, number, ok := strings.Cut(name, "#")
	if !ok || repo == "" || number == "" || strings.Count(repo, "/") != 1 {
		return "", "", fmt.Errorf("an entry is <owner>/<repo>#<number>, the repository included, as in mas-bandwidth/schema#942")
	}
```

internal/wake/entry.go:28 — Entries.Names typed as `<repo>#<n>`:
```go
	Names   []string // <repo>#<n>, in the order the caller gave them
```

cmd/nova-wake/main.go:680 — flag is `--pr` (not `--prs`), takes full names not bare numbers:
```go
	fs.Var(&prs, "pr", "")
```

cmd/nova-wake/main.go:756-759 — prs validation uses SplitPR requiring `<owner>/<repo>#<n>`, not bare numbers:
```go
	for _, name := range prs {
		if _, _, err := wake.SplitPR(name); err != nil {
			p.add("--pr "+name+" is not a pull request", "  "+oneline.Err(err)+"\n")
		}
	}
```

No `--prs` or `--repo` flags exist anywhere in the codebase. The prototype's approach (`--prs` bare numbers + one `--repo`) was replaced by embedded-repo naming (`--entry <owner>/<repo>#<n>`, `--pr <owner>/<repo>#<n>`).

GUARDED-BY UNGUARDED

Grep evidence:
$ grep -rn "\\-\\-prs" --include='*.go' cmd/nova-wake/ internal/wake/
internal/wake/entry.go:16:// the repository is part of the name -- the prototype took --prs 942,951
$ grep -rn "\"prs\"" --include='*.go' cmd/nova-wake/ internal/wake/
internal/wake/pr.go:75: func (p *PRs) Name() string         { return "prs" }
cmd/nova-wake/main.go:993: out = append(out, "prs")
cmd/nova-wake/main.go:1245: w.changed["reports"], w.changed["lines"], w.changed["prs"], w.changed["runs"],
$ grep -rn "\-\-entry\|SplitEntry" --include='*.go' cmd/nova-wake/ internal/wake/
internal/wake/entry.go:209: func SplitEntry(name string) (repo, number string, err error) {
cmd/nova-wake/main.go:677: fs.Var(&entries, "entry", "")
cmd/nova-wake/main.go:800: if _, _, err := wake.SplitEntry(name); err != nil {

Left owed

git status --short
