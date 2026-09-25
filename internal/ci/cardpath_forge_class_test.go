package ci

import (
	"fmt"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestNoGitHubReadsOnTheCardPath is the #3967 DONE-WHEN rule (the machine
// is cardpath_forge.go): no forge call on the card path outside the two
// points, and an injected forge read is named by file and function.
func TestNoGitHubReadsOnTheCardPath(t *testing.T) {
	root := repoRoot(t)
	calls, err := cardPathForgeCalls(root)
	if err != nil {
		t.Fatal(err)
	}
	used := make([]bool, len(forgeAllow))
	var bad []string
	for _, c := range calls {
		ok := false
		for i, a := range forgeAllow {
			if a.file == c.file && a.fn == c.fn && (a.calls == "*" || a.calls == c.what) {
				ok, used[i] = true, true
			}
		}
		if !ok {
			bad = append(bad, c.String())
		}
	}
	for i, a := range forgeAllow {
		if !used[i] {
			bad = append(bad, fmt.Sprintf("allow row %s:%s (%s) matches no forge call: delete it", a.file, a.fn, a.point))
		}
	}
	if len(bad) > 0 {
		t.Errorf("forge calls on the card path outside the two points (import to waiting, the landed close; harvest's PR open is the one write): move each onto the record, the mirror, ci:* or ev:github (nova-tools#3967):\n  %s", strings.Join(bad, "\n  "))
	}

	// The rule names the file and function of an injected read.
	dir := t.TempDir()
	src := "package deal\n\nimport \"os/exec\"\n\nfunc peek(repo string) ([]byte, error) {\n\treturn exec.Command(\"gh\", \"api\", \"repos/\"+repo+\"/pulls/1\").Output()\n}\n\ntype lookup struct{ Forge interface{ ReadPR(int) error } }\n\nfunc (l lookup) head(n int) error { return l.Forge.ReadPR(n) }\n"
	if err := os.WriteFile(filepath.Join(dir, "inject.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := forgeCallsIn(token.NewFileSet(), filepath.Join(dir, "inject.go"), "internal/nsprint/deal/inject.go")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, c := range got {
		names = append(names, c.String())
	}
	want := []string{`internal/nsprint/deal/inject.go:peek "gh"`, "internal/nsprint/deal/inject.go:lookup.head ReadPR"}
	if strings.Join(names, "|") != strings.Join(want, "|") {
		t.Errorf("injected forge reads: got %q, want %q", names, want)
	}
}
