package merge

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func setupAncestryRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	g := NewGit(dir, time.Minute, nil)

	for _, args := range [][]string{
		{"init", "--quiet", "-b", "main", "."},
		{"config", "user.name", "Nova Test"},
		{"config", "user.email", "nova-test@example.invalid"},
	} {
		if out, err := g.Run(args...); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}

	commitFile(t, dir, "initial.txt", "initial\n", "initial commit on main")
	return dir
}

func commitFile(t *testing.T, dir, filename, content, msg string) {
	t.Helper()
	g := NewGit(dir, time.Minute, nil)
	path := filepath.Join(dir, filename)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", filename, err)
	}
	if out, err := g.Run("add", filename); err != nil {
		t.Fatalf("git add %s: %v\n%s", filename, err, out)
	}
	if out, err := g.Run(Identity("commit", "--quiet", "-m", msg)...); err != nil {
		t.Fatalf("git commit -m %q: %v\n%s", msg, err, out)
	}
}

func TestOrderByAncestry(t *testing.T) {
	repo := setupAncestryRepo(t)
	g := NewGit(repo, time.Minute, nil)

	// Set up stacked branches: main -> feat-A -> feat-B -> feat-C
	if out, err := g.Run("checkout", "-b", "feat-A"); err != nil {
		t.Fatalf("checkout feat-A: %v\n%s", err, out)
	}
	commitFile(t, repo, "a.txt", "feat-a content\n", "commit A")

	if out, err := g.Run("checkout", "-b", "feat-B"); err != nil {
		t.Fatalf("checkout feat-B: %v\n%s", err, out)
	}
	commitFile(t, repo, "b.txt", "feat-b content\n", "commit B")

	if out, err := g.Run("checkout", "-b", "feat-C"); err != nil {
		t.Fatalf("checkout feat-C: %v\n%s", err, out)
	}
	commitFile(t, repo, "c.txt", "feat-c content\n", "commit C")

	// Set up independent branches: main -> feat-X, main -> feat-Y
	if out, err := g.Run("checkout", "main"); err != nil {
		t.Fatalf("checkout main: %v\n%s", err, out)
	}
	if out, err := g.Run("checkout", "-b", "feat-X"); err != nil {
		t.Fatalf("checkout feat-X: %v\n%s", err, out)
	}
	commitFile(t, repo, "x.txt", "feat-x content\n", "commit X")

	if out, err := g.Run("checkout", "main"); err != nil {
		t.Fatalf("checkout main: %v\n%s", err, out)
	}
	if out, err := g.Run("checkout", "-b", "feat-Y"); err != nil {
		t.Fatalf("checkout feat-Y: %v\n%s", err, out)
	}
	commitFile(t, repo, "y.txt", "feat-y content\n", "commit Y")

	t.Run("stacked branches arbitrary input order sorted to base first", func(t *testing.T) {
		want := []string{"feat-A", "feat-B", "feat-C"}

		permutations := [][]string{
			{"feat-C", "feat-A", "feat-B"},
			{"feat-B", "feat-C", "feat-A"},
			{"feat-C", "feat-B", "feat-A"},
			{"feat-A", "feat-C", "feat-B"},
			{"feat-B", "feat-A", "feat-C"},
			{"feat-A", "feat-B", "feat-C"},
		}

		for _, input := range permutations {
			got, err := OrderByAncestry(repo, input)
			if err != nil {
				t.Fatalf("OrderByAncestry(%v) error: %v", input, err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("OrderByAncestry(%v) = %v, want %v", input, got, want)
			}
		}
	})

	t.Run("independent branches maintain deterministic order", func(t *testing.T) {
		// feat-X and feat-Y are independent branches branching directly off main.
		// Neither is an ancestor of the other. Their relative order must follow input order.
		gotXY, err := OrderByAncestry(repo, []string{"feat-X", "feat-Y"})
		if err != nil {
			t.Fatalf("OrderByAncestry(X, Y) error: %v", err)
		}
		wantXY := []string{"feat-X", "feat-Y"}
		if !reflect.DeepEqual(gotXY, wantXY) {
			t.Errorf("OrderByAncestry(X, Y) = %v, want %v", gotXY, wantXY)
		}

		gotYX, err := OrderByAncestry(repo, []string{"feat-Y", "feat-X"})
		if err != nil {
			t.Fatalf("OrderByAncestry(Y, X) error: %v", err)
		}
		wantYX := []string{"feat-Y", "feat-X"}
		if !reflect.DeepEqual(gotYX, wantYX) {
			t.Errorf("OrderByAncestry(Y, X) = %v, want %v", gotYX, wantYX)
		}
	})

	t.Run("mixed stacked and independent branches", func(t *testing.T) {
		// Interleaved independent and stacked branches
		input := []string{"feat-Y", "feat-C", "feat-X", "feat-A", "feat-B"}
		got, err := OrderByAncestry(repo, input)
		if err != nil {
			t.Fatalf("OrderByAncestry mixed error: %v", err)
		}

		// feat-A must precede feat-B, and feat-B must precede feat-C
		idxA := indexOf(got, "feat-A")
		idxB := indexOf(got, "feat-B")
		idxC := indexOf(got, "feat-C")
		idxX := indexOf(got, "feat-X")
		idxY := indexOf(got, "feat-Y")

		if idxA >= idxB || idxB >= idxC {
			t.Errorf("stacked branches not in ancestry order: got %v", got)
		}
		// Since feat-Y came before feat-X in input and both are independent of the stack,
		// feat-Y must appear before feat-X.
		if idxY >= idxX {
			t.Errorf("independent branches did not maintain input order: got %v", got)
		}
	})

	t.Run("empty and single branch inputs", func(t *testing.T) {
		empty, err := OrderByAncestry(repo, []string{})
		if err != nil {
			t.Fatalf("empty error: %v", err)
		}
		if len(empty) != 0 {
			t.Errorf("expected empty, got %v", empty)
		}

		single, err := OrderByAncestry(repo, []string{"feat-A"})
		if err != nil {
			t.Fatalf("single error: %v", err)
		}
		if !reflect.DeepEqual(single, []string{"feat-A"}) {
			t.Errorf("expected [feat-A], got %v", single)
		}
	})

	t.Run("nonexistent branch returns error", func(t *testing.T) {
		_, err := OrderByAncestry(repo, []string{"feat-A", "nonexistent-branch"})
		if err == nil {
			t.Fatal("expected error for nonexistent branch, got nil")
		}
	})

	t.Run("branches at identical commit maintain relative order", func(t *testing.T) {
		// Create feat-A-alias at same commit as feat-A
		if out, err := g.Run("branch", "feat-A-alias", "feat-A"); err != nil {
			t.Fatalf("create feat-A-alias: %v\n%s", err, out)
		}

		got1, err := OrderByAncestry(repo, []string{"feat-A", "feat-A-alias"})
		if err != nil {
			t.Fatalf("identical commit order 1 error: %v", err)
		}
		if !reflect.DeepEqual(got1, []string{"feat-A", "feat-A-alias"}) {
			t.Errorf("got %v, want [feat-A, feat-A-alias]", got1)
		}

		got2, err := OrderByAncestry(repo, []string{"feat-A-alias", "feat-A"})
		if err != nil {
			t.Fatalf("identical commit order 2 error: %v", err)
		}
		if !reflect.DeepEqual(got2, []string{"feat-A-alias", "feat-A"}) {
			t.Errorf("got %v, want [feat-A-alias, feat-A]", got2)
		}
	})
}

func indexOf(slice []string, val string) int {
	for i, v := range slice {
		if v == val {
			return i
		}
	}
	return -1
}
