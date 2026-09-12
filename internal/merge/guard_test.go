package merge

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Demanded test 4: the four spellings, in any argument, refused BEFORE the command is
// built -- and the one lease the publication step is allowed, refused from anywhere else.

// watcher is a runner that records what it was asked to run and runs nothing. A guard
// that refuses after the subprocess started is not a guard.
type watcher struct{ seen [][]string }

func (w *watcher) Run(ctx context.Context, dir, name string, args ...string) (string, error) {
	w.seen = append(w.seen, append([]string{name}, args...))
	return "", nil
}

func TestTheMutationGuardRefusesBeforeTheCommandIsBuilt(t *testing.T) {
	lease := "--force-with-lease=refs/heads/main:" + strings.Repeat("a", 40)
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"--auto", []string{"pr", "merge", "--auto"}, "does not queue on this host"},
		{"--force", []string{"push", "origin", "--force"}, "never force-pushes"},
		{"-f", []string{"push", "-f", "origin"}, "never force-pushes"},
		{"bare --force-with-lease", []string{"push", "origin", "--force-with-lease"}, "is not a compare-and-swap"},
		{"a lease with no sha", []string{"push", "origin", "--force-with-lease=refs/heads/main"}, "names a ref and no sha"},
		{"a lease with a short sha", []string{"push", "origin", "--force-with-lease=refs/heads/main:cbde1fc6ba10"}, "wants the full 40-character sha"},
		{"the allowed lease from anywhere else", []string{"push", "origin", lease}, "was not built by the publication step"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := &watcher{}
			g := NewGit(t.TempDir(), 0, w)
			_, err := g.Run(tc.args...)
			if err == nil {
				t.Fatal("the guard must refuse this")
			}
			if _, ok := AsGuardError(err); !ok {
				t.Errorf("the refusal is the guard's, so it is exit 1 rather than a failure to run: %v", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("the refusal must say why, got %v", err)
			}
			if len(w.seen) != 0 {
				t.Errorf("the guard runs BEFORE the command is built; the runner saw %v", w.seen)
			}
		})
	}
}

func TestTheOneAllowedLeaseIsBuiltByPublishAndNowhereElse(t *testing.T) {
	w := &watcher{}
	g := NewGit(t.TempDir(), 0, w)
	sha := strings.Repeat("a", 40)
	merge := strings.Repeat("b", 40)
	if _, err := g.Publish("origin", "main", sha, merge); err != nil {
		t.Fatalf("the publication step's own lease must pass the guard: %v", err)
	}
	if len(w.seen) != 1 {
		t.Fatalf("one publication is one command, got %v", w.seen)
	}
	want := "--force-with-lease=refs/heads/main:" + sha
	if !contains(w.seen[0], want) {
		t.Errorf("the push must carry %q, got %v", want, w.seen[0])
	}
	if !contains(w.seen[0], merge+":refs/heads/main") {
		t.Errorf("the object pushed is the gated object by sha, got %v", w.seen[0])
	}
	// And the lease is closed again the moment the publication is over.
	if _, err := g.Run("push", "origin", want); err == nil {
		t.Error("the lease is open for one publication and closed after it")
	}
}

// A SOURCE TEST, because "one call site" is a property of the code and not of any output:
// the lease spelling is built in exactly one place, and it is Publish.
func TestTheLeaseSpellingHasOneCallSite(t *testing.T) {
	sites := 0
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(".", e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(raw), "\n") {
			if !strings.Contains(line, `"--force-with-lease="+`) && !strings.Contains(line, `"--force-with-lease=" +`) {
				continue
			}
			sites++
			t.Logf("%s:%d: %s", e.Name(), i+1, strings.TrimSpace(line))
		}
	}
	if sites != 1 {
		t.Errorf("the lease is built at %d call sites, want exactly 1 (rule 21's publication step)", sites)
	}
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
