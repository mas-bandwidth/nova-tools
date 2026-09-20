RESULT tools22-rule-wake-15 sha=5298f6be12ea
CONFORMS cmd/nova-wake/main.go:756
SPEC docs/SPEC-WAKE.md:4274 rule 15
PKG internal/wake
ASK The tool must refuse bare pull-request numbers and require every entry/PR to be named with its repository (<owner>/<repo>#<n>), with no --prs-bare-numbers-against-one---repo mode.
Deciding lines:
- cmd/nova-wake/main.go:582  "the prototype's --prs 942,951 is exactly the shape this replaces" (comment on the `repeated` flag type; there is no `--prs` and no `--repo` flag registered anywhere in watch -- parseFlags at main.go:1042 refuses any unknown flag such as `--prs`/`--repo`).
- cmd/nova-wake/main.go:756  for _, name := range prs { if _, _, err := wake.SplitPR(name); err != nil { p.add("--pr "+name+" is not a pull request", ...) } } -- every `--pr` value is run through SplitPR at parse time, so a bare number is refused before any poll.
- internal/wake/entry.go:210  func SplitEntry(name string) (repo, number string, err error) { ... strings.Count(repo, "/") != 1 ... err = "an entry is <owner>/<repo>#<number>, the repository included, as in mas-bandwidth/schema#942" } -- the repository is part of the name; a name without it is refused.
- internal/wake/forge.go:119  func SplitPR(name string) ... "a pull request is <owner>/<repo>#<number>, the repository included, as in mas-bandwidth/nova-tools#239" -- PRs share the entry shape.
- internal/wake/pr.go:279  key := "pr:" + name; internal/wake/source.go:171-183  WAKE PR <repo-qualified name> ... -- the printed name is the <owner>/<repo>#<n> name.
GUARDED-BY cmd/nova-wake/lessons_test.go:438 TestFinalOnlySuppressesTheWakeAndNotTheState (asserts the exact line "WAKE ENTRY mas-bandwidth/schema#942 state=OPEN fail=0 pending=0 pass=12 final=true", so the printed name loses its repository and this test goes red); TestEveryWaitIsASource at cmd/nova-wake/events_test.go:55 also runs `--pr o/r#7` and `--entry o/r#942` through the whole path and expects exit 0.
Greps run: `grep -rn "prs" docs/SPEC-WAKE.md`; `grep -rn "prs\|PRs\|--prs" internal/wake/`; `grep -rn -- "--prs" --include='*.go' .` (only two comment mentions, no flag); `grep -rn -- "--pr\b|--prs\b|--repo\b" cmd/nova-wake/`; `grep -rn "SplitPR\|SplitEntry\|SplitRef\|SplitHead" --include='*_test.go' .` (no direct unit test of the splitters); `grep -rn "func Test" cmd/nova-wake/*_test.go`; files read: internal/wake/entry.go, internal/wake/pr.go, internal/wake/forge.go, internal/wake/source.go, cmd/nova-wake/main.go (lines 560-790, 1042-1054), cmd/nova-wake/events_test.go, cmd/nova-wake/lessons_test.go, cmd/nova-wake/firstrun_test.go, cmd/nova-wake/watch_config_test.go.
Left owed: none.
git status --short: