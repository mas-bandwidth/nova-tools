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
	case argv[0] == "ssh" && strings.HasPrefix(argv[len(argv)-1], "cat "):
		return strings.Repeat("a", 64) + "  nova-sprint\n" + strings.Repeat("b", 64) + "  nova-merge\n", nil
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
	if code != 0 || !strings.Contains(out, "WOULD BUILD space-build --host space") ||
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
	// --redis anywhere on the line (#4050): after the pairs, before the
	// subverb, as --redis=<addr>; set refuses a flag that is not its own.
	for _, args := range [][]string{
		{"build", "set", "self=studio", "--redis", mr.Addr()},
		{"build", "--redis", mr.Addr(), "set", "self=studio"},
		{"build", "set", "--redis=" + mr.Addr(), "self=studio"},
	} {
		var out, errOut bytes.Buffer
		if code := runFleet(ctx, args, &out, &errOut); code != 0 || out.String() != "FLEET RELEASE SET fields=1\n" {
			t.Errorf("%q: code=%d out=%q err=%q", args, code, out.String(), errOut.String())
		}
	}
	var o, e bytes.Buffer
	if code := runFleet(ctx, []string{"build", "set", "--dry-run", "self=studio", "--redis", mr.Addr()}, &o, &e); code != 2 || !strings.Contains(e.String(), "does not take --dry-run") {
		t.Errorf("set --dry-run: code=%d err=%q", code, e.String())
	}
}
