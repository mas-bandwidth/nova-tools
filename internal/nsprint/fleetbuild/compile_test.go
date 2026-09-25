package fleetbuild

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// fakeBuilder stands in for every git and go child of Compile. `go build`
// writes a file with the platform's binary header and a body that depends only
// on the tool, the platform and the version, so a rebuild is byte-identical to
// the reference the way the real recipe is. No test here reaches a host.
type fakeBuilder struct {
	t        *testing.T
	tools    []string
	mu       sync.Mutex
	calls    [][]string
	goEnvs   [][]string // extra env of every go child
	badPlat  string     // this platform gets the wrong header
	failTool string     // go build of this tool fails
}

var fakeHeaders = map[string][]byte{
	"linux-amd64":  {0x7f, 'E', 'L', 'F', 2, 1, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 2, 0, 0x3e, 0},
	"linux-arm64":  {0x7f, 'E', 'L', 'F', 2, 1, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 2, 0, 0xb7, 0},
	"darwin-arm64": {0xcf, 0xfa, 0xed, 0xfe, 0x0c, 0, 0, 0x01, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
	"darwin-amd64": {0xcf, 0xfa, 0xed, 0xfe, 0x07, 0, 0, 0x01, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
}

func envOf(extra []string, name string) string {
	v := ""
	for _, e := range extra {
		if k, val, ok := strings.Cut(e, "="); ok && k == name {
			v = val
		}
	}
	return v
}

func (f *fakeBuilder) Exec(ctx context.Context, dir string, extra []string, argv []string) (string, error) {
	f.mu.Lock()
	f.calls = append(f.calls, argv)
	if argv[0] == "go" {
		f.goEnvs = append(f.goEnvs, extra)
	}
	f.mu.Unlock()
	switch {
	case argv[0] == "git" && argv[1] == "init":
		return "", os.MkdirAll(filepath.Join(argv[3], ".git"), 0o755)
	case argv[0] == "git" && argv[3] == "fetch":
		return "", nil
	case argv[0] == "git" && len(argv) > 5 && argv[5] == "checkout":
		for _, t := range f.tools {
			if err := os.MkdirAll(filepath.Join(argv[2], "cmd", t), 0o755); err != nil {
				return "", err
			}
		}
		return "", nil
	case argv[0] == "git" && argv[3] == "rev-parse":
		return testC + "\n", nil
	case argv[0] == "git" && argv[3] == "status":
		return "", nil
	case argv[0] == "go" && argv[1] == "env":
		return "go1.26.6\n", nil
	case argv[0] == "go" && argv[1] == "build":
		out, tool := argv[len(argv)-2], strings.TrimPrefix(argv[len(argv)-1], "./cmd/")
		plat := envOf(extra, "GOOS") + "-" + envOf(extra, "GOARCH")
		if tool == f.failTool {
			return "cmd/" + tool + ": undefined: x\n", errors.New("exit status 1")
		}
		h := fakeHeaders[plat]
		if plat == f.badPlat {
			h = fakeHeaders["linux-amd64"]
		}
		body := append(append([]byte{}, h...), []byte(tool+" "+plat+" "+argv[4])...)
		return "", os.WriteFile(out, body, 0o755)
	}
	f.t.Errorf("unexpected child %q", argv)
	return "", errors.New("unexpected")
}

func newCompile(t *testing.T, home string, f *fakeBuilder, plats ...string) (*Compile, *bytes.Buffer) {
	t.Helper()
	if len(plats) == 0 {
		plats = strings.Split(DefaultPlatforms, ",")
	}
	var out bytes.Buffer
	return &Compile{Home: home, Version: testV, Commit: testC, Platforms: plats, RepoURL: DefaultRepoURL,
		Exec: f.Exec, Toolchain: func(string) (string, error) { return "go1.27.1", nil }, Out: &out}, &out
}

func tempHome(t *testing.T) string {
	t.Helper()
	home := filepath.Join(t.TempDir(), "home", "u")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	return home
}

// TestCompileBuildsWithItsOwnGoCaches is the DONE-WHEN of #4080 for the
// release build: every go child it starts runs with GOMODCACHE and GOCACHE
// under <home>/nova-bench/space-build/go/ and GOTOOLCHAIN pinned, never the
// bench user's $HOME/go/pkg/mod or $HOME/.cache/go-build that a CI runner on
// the same machine uses, and the REFERENCE BUILT and BUILT lines name the
// caches. The build then publishes every platform, and a second run builds
// nothing.
func TestCompileBuildsWithItsOwnGoCaches(t *testing.T) {
	home := tempHome(t)
	f := &fakeBuilder{t: t, tools: []string{"nova-card", "nova-sprint"}}
	c, out := newCompile(t, home, f)
	if err := c.Run(context.Background()); err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	mod := filepath.Join(home, "nova-bench", "space-build", "go", "mod")
	build := filepath.Join(home, "nova-bench", "space-build", "go", "build")
	if len(f.goEnvs) != 2+2*3+1 { // reference (2 tools), 3 platforms x 2 tools, go env GOVERSION
		t.Fatalf("go children = %d, want 9", len(f.goEnvs))
	}
	for i, e := range f.goEnvs {
		if envOf(e, "GOMODCACHE") != mod || envOf(e, "GOCACHE") != build || envOf(e, "GOTOOLCHAIN") == "" {
			t.Errorf("go child %d env %q: want GOMODCACHE=%s GOCACHE=%s and GOTOOLCHAIN set", i, e, mod, build)
		}
		for _, v := range e {
			if strings.Contains(v, filepath.Join(home, "go", "pkg", "mod")) || strings.Contains(v, filepath.Join(home, ".cache", "go-build")) {
				t.Errorf("go child %d shares the bench user's cache: %s", i, v)
			}
		}
	}
	text := out.String()
	for _, want := range []string{
		"REFERENCE BUILT linux-amd64 " + testV + " files=2 toolchain=go1.26.6 from=go on PATH dir=" + filepath.Join(home, "nova-bench", "build", testV) + " gomodcache=" + mod + " gocache=" + build + "\n",
		"BUILT " + testV + " platforms=linux-amd64,darwin-arm64,darwin-amd64 tools=2 toolchain=go1.27.1 gomodcache=" + mod + " gocache=" + build + " log=",
		"IDENTICAL linux-amd64 files=2",
		"PUBLISHED darwin-arm64 files=2 sums=",
		"FLEET COMPILE OK " + testV + " commit=" + testC + " platforms=" + DefaultPlatforms + " built=" + DefaultPlatforms + " go=" + filepath.Join(home, "nova-bench", "space-build", "go") + "\n",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("output lacks %q:\n%s", want, text)
		}
	}
	for _, p := range strings.Split(DefaultPlatforms, ",") {
		if err := verifyDir(filepath.Join(home, ReleaseRoot, testV, p)); err != nil {
			t.Errorf("published %s: %v", p, err)
		}
	}
	if exists(filepath.Join(home, WorkDir, "out", testV)) {
		t.Error("out/<v> left behind after publishing")
	}

	f2 := &fakeBuilder{t: t, tools: f.tools}
	c2, out2 := newCompile(t, home, f2)
	if err := c2.Run(context.Background()); err != nil || len(f2.calls) != 0 ||
		!strings.HasSuffix(out2.String(), "FLEET COMPILE OK "+testV+" commit="+testC+" platforms="+DefaultPlatforms+" built=none\n") {
		t.Fatalf("second run: err=%v calls=%q\n%s", err, f2.calls, out2)
	}
}

// TestGoEnvNamesOnlyTheBuildsOwnDirectories holds the environment lines
// themselves: both caches under <home>/nova-bench/space-build/go/, the
// toolchain pinned (local when none was read), and a build child adds only
// cgo off and its platform.
func TestGoEnvNamesOnlyTheBuildsOwnDirectories(t *testing.T) {
	home := "/home/ubuntu"
	want := []string{
		"GOMODCACHE=/home/ubuntu/nova-bench/space-build/go/mod",
		"GOCACHE=/home/ubuntu/nova-bench/space-build/go/build",
		"GOTOOLCHAIN=local",
	}
	if got := GoEnv(home, ""); strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("GoEnv = %q, want %q", got, want)
	}
	got := BuildEnv(home, "go1.27.1", "darwin-arm64")
	want = []string{want[0], want[1], "GOTOOLCHAIN=go1.27.1", "CGO_ENABLED=0", "GOOS=darwin", "GOARCH=arm64"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("BuildEnv = %q, want %q", got, want)
	}
}

// TestCompileRefusesADigestMismatch: a reference whose linux-amd64 digest
// differs from the rebuild is not the declared build, and nothing is published.
func TestCompileRefusesADigestMismatch(t *testing.T) {
	home := tempHome(t)
	ref := filepath.Join(home, ReferenceRoot, testV)
	if err := os.MkdirAll(ref, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"nova-card", "nova-sprint"} {
		if err := os.WriteFile(filepath.Join(ref, n), []byte("other"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	f := &fakeBuilder{t: t, tools: []string{"nova-card", "nova-sprint"}}
	c, out := newCompile(t, home, f)
	err := c.Run(context.Background())
	if !errors.Is(err, ErrRefused) || !strings.Contains(err.Error(), "linux-amd64 nova-card is not identical") {
		t.Fatalf("err = %v\n%s", err, out)
	}
	if exists(filepath.Join(home, ReleaseRoot, testV)) {
		t.Error("a refused build published")
	}
}

// TestCompileRefusesAWrongHeaderAndAFailedBuild covers steps 5 and 6.
func TestCompileRefusesAWrongHeaderAndAFailedBuild(t *testing.T) {
	home := tempHome(t)
	f := &fakeBuilder{t: t, tools: []string{"nova-sprint"}, badPlat: "darwin-arm64"}
	c, _ := newCompile(t, home, f)
	if err := c.Run(context.Background()); !errors.Is(err, ErrRefused) || !strings.Contains(err.Error(), "darwin-arm64 file nova-sprint is not a Mach-O arm64 binary") {
		t.Fatalf("wrong header: err = %v", err)
	}
	home = tempHome(t)
	f = &fakeBuilder{t: t, tools: []string{"nova-sprint"}, failTool: "nova-sprint"}
	c, _ = newCompile(t, home, f)
	if err := c.Run(context.Background()); !errors.Is(err, ErrRefused) || !strings.Contains(err.Error(), "undefined: x") {
		t.Fatalf("failed build: err = %v", err)
	}
}

// TestCompileDryRunAndBadInput: --dry-run and a bad argument start no child.
func TestCompileDryRunAndBadInput(t *testing.T) {
	home := tempHome(t)
	f := &fakeBuilder{t: t}
	c, out := newCompile(t, home, f, "linux-amd64")
	c.DryRun = true
	if err := c.Run(context.Background()); err != nil || len(f.calls) != 0 ||
		!strings.Contains(out.String(), "WOULD linux-amd64 build "+testV) || !strings.Contains(out.String(), "go="+filepath.Join(home, GoDir)) {
		t.Fatalf("dry run: err=%v calls=%q\n%s", err, f.calls, out)
	}
	for name, mut := range map[string]func(*Compile){
		"version":  func(c *Compile) { c.Version = "v1" },
		"commit":   func(c *Compile) { c.Commit = "c8178673" },
		"mismatch": func(c *Compile) { c.Commit = strings.Repeat("a", 40) },
		"platform": func(c *Compile) { c.Platforms = []string{"windows-amd64"} },
		"url":      func(c *Compile) { c.RepoURL = "x;rm" },
		"home":     func(c *Compile) { c.Home = "/" },
	} {
		c, _ := newCompile(t, home, f)
		mut(c)
		if err := c.Run(context.Background()); !errors.Is(err, ErrRefused) {
			t.Errorf("%s: err = %v, want a refusal", name, err)
		}
	}
	if len(f.calls) != 0 {
		t.Errorf("a refusal started children: %q", f.calls)
	}
}
