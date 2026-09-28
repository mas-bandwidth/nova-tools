package fleetbuild

import (
	"bytes"
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

const (
	selfSha = "4eabff79cc2e1b5f0f7a6d3c2b1a09876543210f"
	selfOld = "v0.16.0-dev.c839379e"
	selfNew = "v0.16.0-dev.4eabff79"
)

// selfFake is the ExecRunner of a self update test. It starts no process:
// `go build` writes the stamped version into the -o file, and `<file>
// version` answers what that file holds, so the move is observed through the
// same version line production reads.
type selfFake struct {
	offDev bool   // merge-base --is-ancestor fails
	dirty  bool   // git status --porcelain answers an edit
	head   string // rev-parse HEAD in a --from checkout; "" is selfSha
	fail   string // an argv word that fails
	stamp  string // what go build writes instead of the -X version
	mu     sync.Mutex
	calls  []call
}

func (f *selfFake) Run(ctx context.Context, dir string, env []string, argv []string) (string, error) {
	f.mu.Lock()
	f.calls = append(f.calls, call{dir, env, argv})
	f.mu.Unlock()
	line := strings.Join(argv, " ")
	if f.fail != "" && strings.Contains(line, f.fail) {
		return "boom\n", errors.New("exit status 1")
	}
	switch {
	case argv[0] == "git" && argv[1] == "clone":
		os.MkdirAll(filepath.Join(argv[len(argv)-1], ".git"), 0o755)
		return "", nil
	case argv[0] == "git" && argv[1] == "rev-parse":
		rev := strings.TrimSuffix(argv[len(argv)-1], "^{commit}")
		if rev == "HEAD" && f.head != "" {
			return f.head + "\n", nil
		}
		if rev == "HEAD" || rev == "origin/dev" || strings.HasPrefix(selfSha, rev) {
			return selfSha + "\n", nil
		}
		return "", errors.New("exit status 1")
	case argv[0] == "git" && argv[1] == "merge-base":
		if f.offDev {
			return "", errors.New("exit status 1")
		}
		return "", nil
	case argv[0] == "git" && argv[1] == "status":
		if f.dirty {
			return " M cmd/nova-sprint/main.go\n", nil
		}
		return "", nil
	case argv[0] == "git":
		return "", nil
	case argv[0] == "go":
		var out, v string
		for i, a := range argv {
			if a == "-o" {
				out = argv[i+1]
			}
			if strings.HasPrefix(a, "-X main.version=") {
				v = strings.TrimPrefix(a, "-X main.version=")
			}
		}
		if f.stamp != "" {
			v = f.stamp
		}
		return "", os.WriteFile(out, []byte(v), 0o755)
	case len(argv) == 2 && argv[1] == "version":
		b, err := os.ReadFile(argv[0])
		if err != nil {
			return "", err
		}
		return "nova-sprint " + string(b) + " darwin/arm64 go1.26.6\n", nil
	}
	return "", errors.New("unexpected " + line)
}

func (f *selfFake) ran(word string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		if strings.Contains(strings.Join(c.argv, " "), word) {
			return true
		}
	}
	return false
}

func (f *selfFake) build() (call, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		if c.argv[0] == "go" {
			return c, true
		}
	}
	return call{}, false
}

// selfHome is a home with the live binary answering old ("" for none) and a
// release clone whose go.mod reads gomod.
func selfHome(t *testing.T, old, gomod string) string {
	t.Helper()
	home := t.TempDir()
	bin := filepath.Join(home, ".local", "bin", "nova-sprint")
	must(t, os.MkdirAll(filepath.Dir(bin), 0o755))
	if old != "" {
		must(t, os.WriteFile(bin, []byte(old), 0o755))
	}
	src := filepath.Join(home, filepath.FromSlash(SrcDirRel))
	must(t, os.MkdirAll(filepath.Join(src, ".git"), 0o755))
	must(t, os.WriteFile(filepath.Join(src, "go.mod"), []byte(gomod), 0o644))
	return home
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func binHolds(t *testing.T, home string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(home, ".local", "bin", "nova-sprint"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// noTemp fails when a .nova-sprint.self-update.* file is left in the bin dir.
func noTemp(t *testing.T, home string) {
	t.Helper()
	left, _ := filepath.Glob(filepath.Join(home, ".local", "bin", ".nova-sprint.self-update.*"))
	if len(left) > 0 {
		t.Errorf("temp files left behind: %v", left)
	}
}

const gomodPinned = "module github.com/mas-bandwidth/nova-tools\n\ngo 1.26.6\n\nrequire (\n\tgithub.com/redis/go-redis/v9 v9.22.0\n)\n"

// TestSelfUpdateReplacesTheBinaryByRename is #4337's DONE-WHEN: one command
// replaces the live binary after a landing (dev's tip, built with the
// module's pinned Go, printing old -> new), and it does so by rename: a
// process holding the old binary open still reads the old file, and the path
// now names a different file.
func TestSelfUpdateReplacesTheBinaryByRename(t *testing.T) {
	t.Parallel()
	home := selfHome(t, selfOld, gomodPinned)
	bin := filepath.Join(home, ".local", "bin", "nova-sprint")
	running, err := os.Open(bin)
	must(t, err)
	defer running.Close()
	before, err := running.Stat()
	must(t, err)

	f := &selfFake{}
	var out bytes.Buffer
	s := &SelfUpdate{Runner: f, Home: home, PID: 42, Out: &out}
	res, err := s.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v\n%s", err, out.String())
	}
	if res.Old != selfOld || res.New != selfNew || res.Commit != selfSha || res.Toolchain != "go1.26.6" || res.Skipped {
		t.Fatalf("result %+v", res)
	}
	for _, want := range []string{"SOURCE ", "TOOLCHAIN go1.26.6 ", "BUILT " + selfNew + " ", "MOVED " + bin + " " + selfNew} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("no %q line:\n%s", want, out.String())
		}
	}
	b, ok := f.build()
	if !ok {
		t.Fatal("no go build ran")
	}
	if !contains(b.env, "GOTOOLCHAIN=go1.26.6") || b.dir != filepath.Join(home, filepath.FromSlash(SrcDirRel)) {
		t.Errorf("build ran in %s with env %v; want the release clone with GOTOOLCHAIN=go1.26.6", b.dir, b.env)
	}
	if got := binHolds(t, home); got != selfNew {
		t.Errorf("live binary holds %q, want %q", got, selfNew)
	}
	old, err := io.ReadAll(running)
	must(t, err)
	if string(old) != selfOld {
		t.Errorf("the running binary's open file reads %q; a rename leaves it %q", old, selfOld)
	}
	after, err := os.Stat(bin)
	must(t, err)
	if os.SameFile(before, after) {
		t.Error("the path still names the old file: the binary was written in place, not renamed over")
	}
	noTemp(t, home)
}

// TestSelfUpdateRefusesOffDev: a commit not on origin/dev is refused before
// any build and the live binary is untouched; --allow-branch installs it
// with a -branch version, so it never passes for a dev build.
func TestSelfUpdateRefusesOffDev(t *testing.T) {
	t.Parallel()
	home := selfHome(t, selfOld, gomodPinned)
	f := &selfFake{offDev: true}
	var out bytes.Buffer
	_, err := (&SelfUpdate{Runner: f, Home: home, Sha: selfSha[:8], Out: &out}).Run(context.Background())
	if !errors.Is(err, ErrRefused) || !strings.Contains(err.Error(), "not on origin/dev") || !strings.Contains(err.Error(), "--allow-branch") {
		t.Fatalf("err %v; want a refusal naming --allow-branch", err)
	}
	if f.ran("go build") || binHolds(t, home) != selfOld {
		t.Fatal("a refused run built or installed")
	}

	f = &selfFake{offDev: true}
	res, err := (&SelfUpdate{Runner: f, Home: home, Sha: selfSha[:8], AllowBranch: true}).Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if want := "v0.16.0-branch.4eabff79"; res.New != want || binHolds(t, home) != want {
		t.Errorf("new %q, binary %q; want %q", res.New, binHolds(t, home), want)
	}
}

// TestSelfUpdateFromCheckout: --from builds the checkout as it stands (no
// checkout run in it), refuses edits unless --allow-branch, refuses a --sha
// that is not its HEAD, and reads the toolchain line over the go line.
func TestSelfUpdateFromCheckout(t *testing.T) {
	t.Parallel()
	home := selfHome(t, selfOld, gomodPinned)
	from := t.TempDir()
	must(t, os.WriteFile(filepath.Join(from, ".git"), []byte("gitdir: /elsewhere\n"), 0o644))
	must(t, os.WriteFile(filepath.Join(from, "go.mod"), []byte("module m\n\ngo 1.26.6\n\ntoolchain go1.27.1\n"), 0o644))

	f := &selfFake{dirty: true}
	_, err := (&SelfUpdate{Runner: f, Home: home, From: from}).Run(context.Background())
	if !errors.Is(err, ErrRefused) || !strings.Contains(err.Error(), "uncommitted edits") {
		t.Fatalf("dirty tree: err %v", err)
	}
	if f.ran("go build") {
		t.Fatal("a dirty tree was built without --allow-branch")
	}

	f = &selfFake{head: strings.Repeat("a", 40)}
	_, err = (&SelfUpdate{Runner: f, Home: home, From: from, Sha: selfSha[:8]}).Run(context.Background())
	if !errors.Is(err, ErrRefused) || !strings.Contains(err.Error(), "not "+selfSha[:8]) {
		t.Fatalf("sha mismatch: err %v", err)
	}

	f = &selfFake{dirty: true}
	res, err := (&SelfUpdate{Runner: f, Home: home, From: from, AllowBranch: true}).Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	b, _ := f.build()
	if res.New != selfNew+"-dirty" || res.Toolchain != "go1.27.1" || b.dir != from || !contains(b.env, "GOTOOLCHAIN=go1.27.1") {
		t.Errorf("result %+v, build dir %s env %v", res, b.dir, b.env)
	}
	if f.ran("git checkout") || f.ran("git clone") {
		t.Error("--from ran a checkout or clone; it builds the tree as it stands")
	}
}

// TestSelfUpdateSkipsWhenCurrent: a live binary already answering the
// version is not rebuilt.
func TestSelfUpdateSkipsWhenCurrent(t *testing.T) {
	t.Parallel()
	home := selfHome(t, selfNew, gomodPinned)
	f := &selfFake{}
	var out bytes.Buffer
	res, err := (&SelfUpdate{Runner: f, Home: home, Out: &out}).Run(context.Background())
	if err != nil || !res.Skipped || f.ran("go build") || !strings.Contains(out.String(), "SKIPPED ") {
		t.Fatalf("res %+v err %v built=%t\n%s", res, err, f.ran("go build"), out.String())
	}
}

// TestSelfUpdateInstallsNothingOnABadBuild: a failed build, or a build that
// does not answer its own version, leaves the live binary and no temp file;
// with no binary at all the old side reads none.
func TestSelfUpdateInstallsNothingOnABadBuild(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		f    *selfFake
		want string
	}{
		{"build fails", &selfFake{fail: "go build"}, "go build"},
		{"wrong stamp", &selfFake{stamp: "v0.0.0-dev.00000000"}, "the build answers"},
		{"no go.mod pin", &selfFake{}, "pins no Go"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			gomod := gomodPinned
			if tc.name == "no go.mod pin" {
				gomod = "module m\n"
			}
			home := selfHome(t, selfOld, gomod)
			_, err := (&SelfUpdate{Runner: tc.f, Home: home}).Run(context.Background())
			if !errors.Is(err, ErrRefused) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err %v; want a refusal naming %q", err, tc.want)
			}
			if binHolds(t, home) != selfOld {
				t.Error("the live binary changed on a refused run")
			}
			noTemp(t, home)
		})
	}
	home := selfHome(t, "", gomodPinned)
	res, err := (&SelfUpdate{Runner: &selfFake{}, Home: home}).Run(context.Background())
	if err != nil || res.Old != "none" || binHolds(t, home) != selfNew {
		t.Fatalf("first install: res %+v err %v", res, err)
	}
}

func TestSelfUpdateRefusesABadSha(t *testing.T) {
	t.Parallel()
	f := &selfFake{}
	_, err := (&SelfUpdate{Runner: f, Home: t.TempDir(), Sha: "main"}).Run(context.Background())
	if !errors.Is(err, ErrRefused) || len(f.calls) != 0 {
		t.Fatalf("err %v calls %d", err, len(f.calls))
	}
}

func TestPinnedToolchain(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for _, tc := range []struct{ gomod, want, err string }{
		{"module m\ngo 1.26.6\n", "go1.26.6", ""},
		{"module m\ngo 1.26.6\ntoolchain go1.27.1\n", "go1.27.1", ""},
		{"module m\n", "", "pins no Go"},
		{"module m\ngo 1.26.6\ntoolchain default\n", "", "not a go<x>.<y>"},
	} {
		p := filepath.Join(dir, "go.mod")
		must(t, os.WriteFile(p, []byte(tc.gomod), 0o644))
		got, err := PinnedToolchain(p)
		if got != tc.want || (tc.err == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), tc.err)) {
			t.Errorf("%q: got %q err %v; want %q err %q", tc.gomod, got, err, tc.want, tc.err)
		}
	}
}

// TestSelfUpdateCannotCopyOverTheBinary is the class half of the DONE-WHEN:
// selfupdate.go holds no call that writes a file's bytes (no create, open
// for writing, write-file, copy, link or cp child), and exactly one
// os.Rename, in install. A copy over a running binary is impossible through
// the verb because the verb has no code that could do one.
func TestSelfUpdateCannotCopyOverTheBinary(t *testing.T) {
	t.Parallel()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "selfupdate.go", nil, 0)
	must(t, err)
	banned := map[string]bool{"os.Create": true, "os.OpenFile": true, "os.WriteFile": true, "io.Copy": true,
		"io.CopyN": true, "os.Link": true, "os.Symlink": true, "os.Truncate": true}
	renames := 0
	ast.Inspect(file, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.CallExpr:
			sel, ok := x.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			id, ok := sel.X.(*ast.Ident)
			if !ok {
				return true
			}
			name := id.Name + "." + sel.Sel.Name
			if banned[name] {
				t.Errorf("%s: %s writes bytes; the verb installs by rename only", fset.Position(x.Pos()), name)
			}
			if name == "os.Rename" {
				renames++
			}
		case *ast.BasicLit:
			if x.Value == `"cp"` || x.Value == `"install"` || x.Value == `"ditto"` {
				t.Errorf("%s: %s is a copying child", fset.Position(x.Pos()), x.Value)
			}
		}
		return true
	})
	if renames != 1 {
		t.Errorf("os.Rename calls: %d, want the one in install", renames)
	}
}
