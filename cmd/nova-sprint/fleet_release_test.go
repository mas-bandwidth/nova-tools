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
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

const (
	verbSha     = "c8178673f5e19ffbfe841e11826e611b74b6900d"
	verbVersion = "v0.16.0-dev.c8178673"
)

// verbRelFake is the verb test's ExecRunner: every step answers, the built
// binary is written, and the calls are kept for the sequence.
type verbRelFake struct {
	home  string
	mu    sync.Mutex
	calls []string
}

func (f *verbRelFake) Run(ctx context.Context, dir string, env []string, argv []string) (string, error) {
	f.mu.Lock()
	f.calls = append(f.calls, strings.Join(argv, " "))
	f.mu.Unlock()
	bin := filepath.Join(f.home, ".local", "bin", "nova-sprint")
	switch {
	case argv[0] == "git" && argv[1] == "clone":
		os.MkdirAll(filepath.Join(argv[len(argv)-1], ".git"), 0o755)
		return "", nil
	case argv[0] == "git" && argv[1] == "rev-parse":
		return verbSha + "\n", nil
	case argv[0] == "git", argv[0] == "launchctl", argv[0] == "systemctl":
		return "", nil
	case argv[0] == "go":
		os.WriteFile(argv[len(argv)-2], []byte(verbVersion), 0o755)
		return "", nil
	case argv[0] == bin && argv[1] == "version":
		b, err := os.ReadFile(bin)
		return "nova-sprint " + string(b) + " darwin/arm64 go1.25.1\n", err
	case argv[0] == bin && argv[1] == "fn":
		return "FN RECEIPT at=t store=s load=UNCHANGED sha=x version=" + verbVersion + " ping=PONG\n", nil
	case argv[0] == "ssh" && strings.Contains(strings.Join(argv, " "), " fleet build compile "):
		return "FLEET COMPILE OK " + verbVersion + "\n", nil
	case argv[0] == "ssh" && strings.HasPrefix(argv[len(argv)-1], "cat "):
		return strings.Repeat("a", 64) + "  nova-sprint\n", nil
	case argv[0] == "ssh" && strings.Contains(strings.Join(argv, " "), "bench-beat"):
		return "", nil
	case argv[0] == "ssh":
		return "nova-sprint " + verbVersion + " linux/amd64 go1.25.1\n", nil
	}
	return "", errors.New("unexpected " + strings.Join(argv, " "))
}

func releaseRegistry(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "machines.tsv")
	body := "space\tspace\tlinux/x64\tbench,services\nhulk\thulk\tlinux/x64\tbench\nstudio\tlocalhost\tdarwin/arm64\tcoordination\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func verbDeps(f *verbRelFake, env map[string]string) releaseDeps {
	return releaseDeps{Runner: f, Home: func() (string, error) { return f.home, nil },
		Getenv: func(k string) string { return env[k] }, UID: 501, GOOS: "darwin", Open: openFleetStore}
}

// TestFleetReleaseShaVerb drives `fleet release <sha>` with fakes: the
// receipts in order, the store's release fields written, the rolled bench
// receipted, and the FLEET RELEASE OK line last.
func TestFleetReleaseShaVerb(t *testing.T) {
	t.Parallel()
	mr := miniredis.RunT(t)
	mr.SAdd("benches", "space", "hulk")
	f := &verbRelFake{home: t.TempDir()}
	reg := releaseRegistry(t)
	var out, errOut bytes.Buffer
	code := runFleetReleaseWith(context.Background(),
		[]string{verbSha[:8], "--redis", mr.Addr(), "--machines", reg, "--benches", "hulk", "--admin-password-env", "MY_ADMIN"},
		&out, &errOut, verbDeps(f, map[string]string{"MY_ADMIN": "pw"}))
	if code != 0 || errOut.Len() != 0 {
		t.Fatalf("code=%d\n%s\nerr=%s", code, out.String(), errOut.String())
	}
	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	heads := make([]string, 0, len(lines))
	for _, l := range lines {
		heads = append(heads, strings.Fields(l)[0])
	}
	want := "SOURCE BUILT MOVED KICKSTARTED KICKSTARTED KICKSTARTED FN FLEET CONVERGED BUILD MANIFEST OK PROBE FN BEAT ROLLED FLEET"
	if strings.Join(heads, " ") != want {
		t.Fatalf("receipts:\n%s\nwant heads: %s", out.String(), want)
	}
	if last := lines[len(lines)-1]; last != "FLEET RELEASE OK version="+verbVersion+" commit=c8178673f5e1 studio=built fn=ok rolled=1 skipped=0" {
		t.Errorf("last line %q", last)
	}
	if mr.HGet("fleet:release", "version") != verbVersion || mr.HGet("bench:hulk", "build") != verbVersion {
		t.Errorf("store not written: %s", mr.Dump())
	}
	if !strings.Contains(strings.Join(f.calls, "\n"), "fn deploy --redis "+mr.Addr()) {
		t.Errorf("fn deploy not run against --redis: %v", f.calls)
	}

	// Without the password the FN step alone is refused, the rest runs,
	// and the verb ends FAIL, exit 1.
	f = &verbRelFake{home: t.TempDir()}
	out.Reset()
	code = runFleetReleaseWith(context.Background(),
		[]string{"--redis", mr.Addr(), "--machines", reg, "--studio-only", verbSha},
		&out, &errOut, verbDeps(f, nil))
	if code != 1 || !strings.Contains(out.String(), "FN REFUSED store="+fleetAddr(mr.Addr())+" reason=no-admin-password env=NS_ADMIN remedy=export NS_ADMIN=$(ssh space 'sudo cat /var/lib/nova-redis/admin.pass'), then rerun nova-sprint fleet release "+verbSha) ||
		!strings.HasSuffix(out.String(), "FLEET RELEASE FAIL version="+verbVersion+" commit=c8178673f5e1 studio=built fn=refused rolled=0 skipped=0\n") {
		t.Fatalf("no password: code=%d\n%s", code, out.String())
	}

	// A refusal inside a step is one FLEET RELEASE REFUSED line, exit 1.
	f = &verbRelFake{home: t.TempDir()}
	out.Reset()
	code = runFleetReleaseWith(context.Background(), []string{"--studio-only", "--machines", reg, verbSha}, &out, &errOut,
		releaseDeps{Runner: f, Home: func() (string, error) { return f.home, nil }, Getenv: func(string) string { return "pw" },
			UID: 501, GOOS: "plan9", Open: openFleetStore})
	if code != 1 || !strings.HasPrefix(errOut.String(), "FLEET RELEASE REFUSED: no loop restart for plan9") {
		t.Fatalf("plan9: code=%d err=%q", code, errOut.String())
	}
}

// TestFleetReleaseUsage: the held-bench form and the sha form share one
// flag set, and each refuses the other's flags in one line, exit 2.
func TestFleetReleaseUsage(t *testing.T) {
	t.Parallel()
	f := &verbRelFake{home: t.TempDir()}
	opened := false
	deps := verbDeps(f, nil)
	deps.Open = func(ctx context.Context, addr string) (*store.Store, error) {
		opened = true
		return nil, errors.New("no store")
	}
	for _, tc := range []struct {
		args []string
		want string
	}{
		{nil, "wants a <sha> (the deploy) or --bench <b>"},
		{[]string{"--benches", "hulk"}, "wants a <sha> with --benches"},
		{[]string{verbSha, "--bench", "hulk"}, "does not take --bench"},
		{[]string{verbSha, "deadbeef"}, "wants one <sha>"},
		{[]string{verbSha, "--studio-only", "--benches-only"}, "leave nothing to do"},
		{[]string{"--nope", verbSha}, "flag provided but not defined"},
	} {
		var out, errOut bytes.Buffer
		code := runFleetReleaseWith(context.Background(), tc.args, &out, &errOut, deps)
		if code != 2 || out.Len() != 0 || !strings.Contains(errOut.String(), tc.want) || len(f.calls) != 0 || opened {
			t.Errorf("%v: code=%d out=%q err=%q calls=%v opened=%v", tc.args, code, out.String(), errOut.String(), f.calls, opened)
		}
	}
	// A bad sha or a missing registry refuses before any child, exit 1.
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--studio-only", "nothex"}, "is not a commit sha"},
		{[]string{"--studio-only", "--machines", filepath.Join(t.TempDir(), "none.tsv"), verbSha}, "machines registry:"},
	} {
		var out, errOut bytes.Buffer
		code := runFleetReleaseWith(context.Background(), tc.args, &out, &errOut, deps)
		if code != 1 || !strings.HasPrefix(errOut.String(), "FLEET RELEASE REFUSED: ") || !strings.Contains(errOut.String(), tc.want) || len(f.calls) != 0 {
			t.Errorf("%v: code=%d err=%q calls=%v", tc.args, code, errOut.String(), f.calls)
		}
	}
	// The held form still dials the store with --bench alone.
	var out, errOut bytes.Buffer
	if code := runFleetReleaseWith(context.Background(), []string{"--bench", "hulk"}, &out, &errOut, deps); code != 5 || !opened {
		t.Errorf("held form: code=%d opened=%v err=%q", code, opened, errOut.String())
	}
	_ = fleetbuild.DefaultAdminEnv
}
