package swarm

import (
	"os"
	"path/filepath"
	"testing"
)

// 37ae3125 moved ParseRoutes and PickRoute into this package so pulse launch
// and nova-swarm route share one function. Reverting route.go kept this
// package green: the verb tests stayed in cmd/nova-swarm and still passed
// against the inlined copy.
func TestParseRoutesAndPickRoute(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	muse := filepath.Join(dir, "muse.json")
	paid := filepath.Join(dir, "paid.json")
	mid := filepath.Join(dir, "mid.json")
	def := filepath.Join(dir, "default.json")
	path := filepath.Join(dir, "routes.tsv")
	body := "fix\t1\t" + muse + "\tpublic\nfix\t1\t" + paid + "\tpaid\nfeat\t1\t" + muse + "\tpublic\nfeat\t2\t" + mid + "\tpublic\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	rows, err := ParseRoutes(path)
	if err != nil {
		t.Fatalf("ParseRoutes: %v", err)
	}
	if got := PickRoute(rows, "fix", 1, 0.1, def); got != muse {
		t.Fatalf("fix/1 public card picked %q, want %q", got, muse)
	}
	if got := PickRoute(rows, "fix", 1, 0.9, def); got != paid {
		t.Fatalf("private fix/1 card picked %q, want paid %q", got, paid)
	}
	if got := PickRoute(rows, "feat", 3, 0.1, def); got != mid {
		t.Fatalf("feat/3 with no row picked %q, want lower %q", got, mid)
	}
	if got := PickRoute(rows, "port", 4, 0.1, def); got != def {
		t.Fatalf("unknown kind picked %q, want default %q", got, def)
	}
}

func TestParseRoutesRefusesAnEmptyTable(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "empty.tsv")
	if err := os.WriteFile(path, []byte("\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseRoutes(path); err == nil {
		t.Fatal("an empty routes table is a refusal, never a guess")
	}
}
