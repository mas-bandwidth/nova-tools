package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/alicebob/miniredis/v2"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fleetbuild"
)

type verbFakeBench struct {
	home  string
	mu    sync.Mutex
	calls []string
}

func (f *verbFakeBench) Run(ctx context.Context, argv []string) (string, error) {
	f.mu.Lock()
	f.calls = append(f.calls, argv[0])
	f.mu.Unlock()
	const v = "v0.16.0-dev.c8178673"
	switch {
	case argv[0] == "space-build":
		return "SPACE BUILD OK\n", nil
	case argv[0] == "ssh" && strings.Contains(strings.Join(argv, " "), " fleet build compile "):
		return "FLEET COMPILE OK " + v + " go=/home/u/nova-bench/space-build/go\n", nil
	case argv[0] == "ssh":
		return "nova-sprint " + v + " linux/amd64 go1.25.1\n", nil
	case argv[0] == "rsync":
		stage := strings.TrimSuffix(argv[len(argv)-1], "/")
		for _, a := range argv {
			if t, ok := strings.CutPrefix(a, "--include="); ok {
				os.WriteFile(filepath.Join(stage, t), []byte(v), 0o755)
			}
		}
		return "", nil
	case len(argv) > 1 && argv[1] == "version":
		return "nova-sprint " + v + " darwin/arm64 go1.25.1\n", nil
	case len(argv) > 1 && argv[1] == "fn":
		return "FN RECEIPT load=UNCHANGED\n", nil
	}
	return "", errors.New("unexpected " + argv[0])
}

// TestFleetBuildVerbSetDryRunAndDeploy drives the verb end to end with a fake
// bench runner: set writes the plan, a gap is refused (exit 1) before any
// child, --dry-run starts nothing, and the deploy prints FLEET BUILD OK.
func TestFleetBuildVerbSetDryRunAndDeploy(t *testing.T) {
	mr := miniredis.RunT(t)
	mr.SAdd("benches", "hulk", "space")
	f := &verbFakeBench{home: t.TempDir()}
	oldRunner, oldHome := fleetBuildRunner, fleetBuildHome
	fleetBuildRunner = f
	fleetBuildHome = func() (string, error) { return f.home, nil }
	t.Cleanup(func() { fleetBuildRunner, fleetBuildHome = oldRunner, oldHome })

	ctx := context.Background()
	run := func(args ...string) (int, string, string) {
		var out, errOut bytes.Buffer
		code := runFleet(ctx, append(append([]string{"build"}, args...), "--redis", mr.Addr()), &out, &errOut)
		return code, out.String(), errOut.String()
	}
	set := func(kv ...string) (int, string, string) {
		var out, errOut bytes.Buffer
		code := runFleet(ctx, append([]string{"build", "set", "--redis", mr.Addr()}, kv...), &out, &errOut)
		return code, out.String(), errOut.String()
	}

	if code, _, errOut := set("platform:hulk=windows-amd64"); code != 1 || !strings.Contains(errOut, "FLEET BUILD REFUSED") {
		t.Fatalf("bad set: code=%d %q", code, errOut)
	}
	if code, out, errOut := set("version=v0.16.0-dev.c8178673", "commit=c8178673f5e19ffbfe841e11826e611b74b6900d",
		"builder=space", "self=studio", "platform:hulk=linux-amd64", "platform:space=linux-amd64"); code != 0 || out != "FLEET RELEASE SET fields=6\n" {
		t.Fatalf("set: code=%d out=%q err=%q", code, out, errOut)
	}
	if code, _, errOut := run(); code != 1 || !strings.Contains(errOut, "HSET fleet:release platform:studio") {
		t.Fatalf("missing self platform: code=%d %q", code, errOut)
	}
	if len(f.calls) != 0 {
		t.Fatalf("a refusal started children: %v", f.calls)
	}
	if code, _, _ := set("platform:studio=darwin-arm64"); code != 0 {
		t.Fatal("set studio platform")
	}
	code, out, errOut := run("--dry-run")
	if code != 0 || !strings.Contains(out, "WOULD BUILD ssh -n -o BatchMode=yes -o ConnectTimeout=15 space .local/bin/nova-sprint fleet build compile --version v0.16.0-dev.c8178673 --commit c8178673f5e19ffbfe841e11826e611b74b6900d --platform darwin-arm64,linux-amd64\n") ||
		!strings.Contains(out, "WOULD INSTALL hulk platform=linux-amd64 from=space:nova-bench/release/v0.16.0-dev.c8178673/linux-amd64/") ||
		!strings.Contains(out, "FLEET BUILD DRY-RUN version=v0.16.0-dev.c8178673 commit=c8178673f5e1 targets=3") || len(f.calls) != 0 {
		t.Fatalf("dry-run: code=%d calls=%v out=%q err=%q", code, f.calls, out, errOut)
	}
	code, out, errOut = run()
	if code != 0 || !strings.HasSuffix(out, "FLEET BUILD OK version=v0.16.0-dev.c8178673 commit=c8178673f5e1 benches=3 fn=ok\n") {
		t.Fatalf("deploy: code=%d out=%q err=%q", code, out, errOut)
	}
	if got := mr.HGet("bench:hulk", "build"); got != "v0.16.0-dev.c8178673" {
		t.Errorf("bench:hulk build = %q", got)
	}
	if code, _, _ := run("extra"); code != 2 {
		t.Errorf("positional argument: code=%d, want 2", code)
	}
}

// TestFleetBuildCompileExecGivesGoItsOwnCaches (#4080) runs the production
// compile seam against a fake go on PATH: the child sees the build's own
// GOMODCACHE, GOCACHE and GOTOOLCHAIN over whatever the caller exported (the
// bench user's caches), and GOFLAGS is dropped by goenv.Clean.
func TestFleetBuildCompileExecGivesGoItsOwnCaches(t *testing.T) {
	bin := t.TempDir()
	fake := "#!/bin/sh\nenv | grep -E '^(GOMODCACHE|GOCACHE|GOTOOLCHAIN|GOFLAGS|GOOS|GOARCH|CGO_ENABLED)=' | sort\n"
	if err := os.WriteFile(filepath.Join(bin, "go"), []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("GOMODCACHE", "/home/u/go/pkg/mod")
	t.Setenv("GOCACHE", "/home/u/.cache/go-build")
	t.Setenv("GOTOOLCHAIN", "auto")
	t.Setenv("GOFLAGS", "-json")
	out, err := fleetCompileExec(context.Background(), t.TempDir(), fleetbuild.BuildEnv("/home/u", "go1.27.1", "linux-amd64"), []string{"go", "build"})
	want := "CGO_ENABLED=0\nGOARCH=amd64\nGOCACHE=/home/u/nova-bench/space-build/go/build\n" +
		"GOMODCACHE=/home/u/nova-bench/space-build/go/mod\nGOOS=linux\nGOTOOLCHAIN=go1.27.1\n"
	if err != nil || out != want {
		t.Fatalf("child env: err=%v\n got %q\nwant %q", err, out, want)
	}
}

// TestFleetBuildCompileVerb: usage is exit 2, a bad value is refused (exit 1)
// before any child, and --dry-run names the build's Go directory.
func TestFleetBuildCompileVerb(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home", "u")
	oldExec, oldHome := fleetCompileExec, fleetBuildHome
	var calls int
	fleetCompileExec = func(context.Context, string, []string, []string) (string, error) {
		calls++
		return "", errors.New("no child in this test")
	}
	fleetBuildHome = func() (string, error) { return home, nil }
	t.Cleanup(func() { fleetCompileExec, fleetBuildHome = oldExec, oldHome })
	run := func(args ...string) (int, string, string) {
		var out, errOut bytes.Buffer
		code := runFleet(context.Background(), append([]string{"build", "compile"}, args...), &out, &errOut)
		return code, out.String(), errOut.String()
	}
	if code, _, _ := run(); code != 2 {
		t.Errorf("no flags: code=%d, want 2", code)
	}
	if code, _, errOut := run("--version", "v1", "--commit", "c8178673f5e19ffbfe841e11826e611b74b6900d"); code != 1 || !strings.HasPrefix(errOut, "FLEET COMPILE REFUSED: version") {
		t.Errorf("bad version: code=%d %q", code, errOut)
	}
	code, out, errOut := run("--version", "v0.16.0-dev.c8178673", "--commit", "c8178673f5e19ffbfe841e11826e611b74b6900d", "--platform", "linux-amd64", "--dry-run")
	if code != 0 || !strings.Contains(out, "WOULD linux-amd64 build v0.16.0-dev.c8178673") || !strings.Contains(out, "go="+filepath.Join(home, "nova-bench", "space-build", "go")) {
		t.Errorf("dry-run: code=%d out=%q err=%q", code, out, errOut)
	}
	if calls != 0 {
		t.Errorf("started %d children", calls)
	}
}
