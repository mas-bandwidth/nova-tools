package guard_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/land/guard"
)

// fixtureRepo is a small git repository with a lua directory, a Go file, a
// docs page and an allowlist, committed, so every guard has a tree to read.
func fixtureRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	root := t.TempDir()
	write := func(rel, body string) {
		t.Helper()
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(guard.LuaDir+"/00_ping.lua", "local function ping() return 'PONG' end\nredis.register_function('ns_ping', ping)\nNS.ping = ping\n")
	write(guard.LuaDir+"/01_use.lua", "local ping = NS.ping\nlocal function pong() return ping() end\nredis.register_function('ns_pong', pong)\n")
	write("internal/x/x.go", "// Package x reads internal/x/x.go and docs/X.md.\npackage x\n")
	write("docs/X.md", "# X\n\nSee internal/x/x.go.\n")
	write(guard.NamedPathsAllowlist, "# none\n")
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q", "-b", "dev")
	git("add", "-A")
	git("commit", "-q", "-m", "fixture")
	return root
}

func one(t *testing.T, root, name string) guard.Result {
	t.Helper()
	rs := guard.Run(context.Background(), root, name)
	if len(rs) != 1 || rs[0].Name != name {
		t.Fatalf("Run(%s) = %+v", name, rs)
	}
	if rs[0].Err != nil {
		t.Fatalf("%s: %v", name, rs[0].Err)
	}
	return rs[0]
}

// TestGuardsPassOnACleanFixture: the fixture tree passes every guard the
// fixture can carry (the catalog guard needs the real map and is covered by
// TestGuardsOnThisRepository).
func TestGuardsPassOnACleanFixture(t *testing.T) {
	t.Parallel()

	root := fixtureRepo(t)
	for _, name := range []string{"lua-locals", "lua-crossfile", "one-parser", "named-paths", "tracked-files"} {
		r := one(t, root, name)
		if !r.OK {
			t.Errorf("%s", r.Row())
		}
		if !strings.HasPrefix(r.Row(), "PASS "+name) {
			t.Errorf("row %q", r.Row())
		}
	}
}

// TestGuardCatchesA201LocalLuaFile is the DONE-WHEN control for the 8d12fb19
// shape: one file with 201 top-level locals fails lua-locals and the row
// names the file.
func TestGuardCatchesA201LocalLuaFile(t *testing.T) {
	t.Parallel()

	root := fixtureRepo(t)
	var b strings.Builder
	for i := 0; i < 201; i++ {
		fmt.Fprintf(&b, "local v%d = %d\n", i, i)
	}
	b.WriteString("redis.register_function('ns_big', function() return v0 end)\n")
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(guard.LuaDir), "02_big.lua"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	r := one(t, root, "lua-locals")
	if r.OK || r.File != guard.LuaDir+"/02_big.lua" || !strings.Contains(r.Why, "201") {
		t.Fatalf("want FAIL naming 02_big.lua with 201, got %s", r.Row())
	}
	if !strings.HasPrefix(r.Row(), "FAIL lua-locals "+guard.LuaDir+"/02_big.lua: ") {
		t.Fatalf("row %q", r.Row())
	}
}

// TestGuardCatchesABareCrossFileName: the a42285c5 shape, a file that calls
// another file's local by its bare name.
func TestGuardCatchesABareCrossFileName(t *testing.T) {
	t.Parallel()

	root := fixtureRepo(t)
	src := "local function pong2() return ping() end\nredis.register_function('ns_pong2', pong2)\n"
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(guard.LuaDir), "02_bare.lua"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	r := one(t, root, "lua-crossfile")
	if r.OK || r.File != guard.LuaDir+"/02_bare.lua" || !strings.Contains(r.Why, `"ping"`) {
		t.Fatalf("want FAIL naming 02_bare.lua reading ping, got %s", r.Row())
	}
}

// TestGuardCatchesAMissingNamedPath is the DONE-WHEN control: a comment that
// names a path the tree does not have fails named-paths at that line.
func TestGuardCatchesAMissingNamedPath(t *testing.T) {
	t.Parallel()

	root := fixtureRepo(t)
	src := "// Package y follows internal/gone/y.go.\npackage y\n"
	p := filepath.Join(root, "internal", "y", "y.go")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	r := one(t, root, "named-paths")
	if r.OK || r.File != "internal/y/y.go:1" || !strings.Contains(r.Why, "internal/gone/y.go names no file") {
		t.Fatalf("want FAIL at internal/y/y.go:1 for internal/gone/y.go, got %s", r.Row())
	}
	// The allowlist parks it, as internal/ci's rule does.
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(guard.NamedPathsAllowlist)), []byte("internal/gone/y.go planned\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if r := one(t, root, "named-paths"); !r.OK {
		t.Fatalf("allowlisted name still fails: %s", r.Row())
	}
}

// TestGuardCatchesASecondParser: a new non-test file that reads who= and
// verdict= is not in the allow list.
func TestGuardCatchesASecondParser(t *testing.T) {
	t.Parallel()

	root := fixtureRepo(t)
	src := "package z\n\nfunc parse(s string) bool { return s == \"who=\" || s == \"verdict=\" }\n"
	p := filepath.Join(root, "internal", "z", "z.go")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	r := one(t, root, "one-parser")
	if r.OK || r.File != "internal/z/z.go" {
		t.Fatalf("want FAIL naming internal/z/z.go, got %s", r.Row())
	}
}

// TestGuardCatchesTrackedScratchAndOversized: a tracked scratch/ file and a
// tracked file over the cap each fail tracked-files.
func TestGuardCatchesTrackedScratchAndOversized(t *testing.T) {
	t.Parallel()

	root := fixtureRepo(t)
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if err := os.MkdirAll(filepath.Join(root, "scratch"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "scratch", "junk.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "-f", "scratch/junk.txt")
	r := one(t, root, "tracked-files")
	if r.OK || r.File != "scratch/junk.txt" {
		t.Fatalf("want FAIL naming scratch/junk.txt, got %s", r.Row())
	}
	git("rm", "-q", "--cached", "scratch/junk.txt")
	big := make([]byte, guard.MaxTrackedBytes+1)
	if err := os.WriteFile(filepath.Join(root, "docs", "big.bin"), big, 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "docs/big.bin")
	r = one(t, root, "tracked-files")
	if r.OK || r.File != "docs/big.bin" {
		t.Fatalf("want FAIL naming docs/big.bin, got %s", r.Row())
	}
}

// TestRunReportsAnUnknownGuardAsAFailRow: a typo in a lander's list is a
// row, never a silent skip.
func TestRunReportsAnUnknownGuardAsAFailRow(t *testing.T) {
	t.Parallel()

	rs := guard.Run(context.Background(), t.TempDir(), "no-such-guard")
	if len(rs) != 1 || rs[0].Err == nil || !strings.HasPrefix(rs[0].Row(), "FAIL no-such-guard") {
		t.Fatalf("got %+v", rs)
	}
	if guard.AllOK(rs) {
		t.Fatal("AllOK on an error row")
	}
}

// TestGuardsOnThisRepository runs the whole registry over the repository
// this test is in: dev is green by construction, so every row is PASS, and a
// red row here is a real finding on the tree under test.
func TestGuardsOnThisRepository(t *testing.T) {
	t.Parallel()

	if testing.Short() {
		t.Skip("walks the whole tree")
	}
	root, err := filepath.Abs(filepath.Join("..", "..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Skipf("no repository root at %s", root)
	}
	rs := guard.Run(context.Background(), root)
	if len(rs) != len(guard.Registry) {
		t.Fatalf("%d rows for %d guards", len(rs), len(guard.Registry))
	}
	for _, r := range rs {
		t.Log(r.Row())
		if r.Err != nil || !r.OK {
			t.Errorf("%s", r.Row())
		}
	}
}
