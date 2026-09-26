package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fleetbuild"
)

// verbRollFake answers dev's tip and the play, the rest as verbRelFake.
type verbRollFake struct {
	*verbRelFake
	playDir, playEnv string
}

func (f *verbRollFake) Run(ctx context.Context, dir string, env []string, argv []string) (string, error) {
	switch {
	case argv[0] == "git" && argv[1] == "ls-remote":
		f.mu.Lock()
		f.calls = append(f.calls, strings.Join(argv, " "))
		f.mu.Unlock()
		return verbSha + "\trefs/heads/dev\n", nil
	case argv[0] == "ansible-playbook":
		f.mu.Lock()
		f.calls = append(f.calls, strings.Join(argv, " "))
		f.playDir, f.playEnv = dir, strings.Join(env, " ")
		f.mu.Unlock()
		return "PLAY RECAP ***\nhulk : ok=9 changed=0 unreachable=0 failed=0\n", nil
	}
	return f.verbRelFake.Run(ctx, dir, env, argv)
}

func rollPlayDir(t *testing.T) string {
	t.Helper()
	d := t.TempDir()
	for _, f := range []string{"inventory.py", "tools.yml"} {
		if err := os.WriteFile(filepath.Join(d, f), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return d
}

func rollDeps(f *verbRollFake, env map[string]string, sleeps *int) releaseDeps {
	return releaseDeps{Runner: f, Home: func() (string, error) { return f.home, nil },
		Getenv: func(k string) string { return env[k] }, UID: 501, GOOS: "darwin", Open: openFleetStore,
		Sleep: func(context.Context, time.Duration) error { *sleeps++; return nil }}
}

// TestFleetRollVerb drives `fleet roll` end to end with fakes: dev's tip,
// the release, the play from the play directory with the registry, the
// verify, and FLEET ROLL OK last, exit 0; a bench left behind is exit 1 with
// the list on the last line.
func TestFleetRollVerb(t *testing.T) {
	t.Parallel()
	mr := miniredis.RunT(t)
	mr.SAdd("benches", "space", "hulk")
	mr.HSet("bench:hulk:beat", "build", "nova-sprint "+verbVersion+" linux/amd64 go1.25.1")
	reg := releaseRegistry(t)
	dir := rollPlayDir(t)
	f := &verbRollFake{verbRelFake: &verbRelFake{home: t.TempDir()}}
	sleeps := 0
	var out, errOut bytes.Buffer
	code := runFleetRollWith(context.Background(),
		[]string{"--redis", mr.Addr(), "--machines", reg, "--benches", "hulk", "--wait", "0s"},
		&out, &errOut, rollDeps(f, map[string]string{"NS_ADMIN": "pw", fleetbuild.PlayDirEnv: dir}, &sleeps))
	if code != 0 || errOut.Len() != 0 {
		t.Fatalf("code=%d\n%s\nerr=%s", code, out.String(), errOut.String())
	}
	if f.calls[0] != "git ls-remote git@github.com:mas-bandwidth/nova-tools.git refs/heads/dev" || f.playDir != dir ||
		f.playEnv != "ANSIBLE_NOCOWS=1 ANSIBLE_HOST_KEY_CHECKING=True FLEET_REGISTRY="+reg ||
		!strings.Contains(strings.Join(f.calls, "\n"), "ansible-playbook -i inventory.py tools.yml --forks 16 --diff -e nova_build="+verbVersion+" --limit hulk") {
		t.Errorf("calls %v dir=%s env=%s", f.calls, f.playDir, f.playEnv)
	}
	s := out.String()
	if !strings.Contains(s, "RECAP hulk : ok=9 changed=0 unreachable=0 failed=0\nPLAY OK tools.yml version="+verbVersion+"\nVERIFY hulk want="+verbVersion+" have="+verbVersion+" ok\n") ||
		!strings.HasSuffix(s, "FLEET ROLL OK version="+verbVersion+" commit=c8178673f5e1 fn=ok play=ok benches=1 behind=-\n") || sleeps != 0 {
		t.Fatalf("out:\n%s sleeps=%d", s, sleeps)
	}

	// space has no beat: behind after the wait (60 s default at 5 s is 12
	// pauses, all faked), exit 1 naming it; --to and --play-dir given.
	f = &verbRollFake{verbRelFake: &verbRelFake{home: t.TempDir()}}
	out.Reset()
	code = runFleetRollWith(context.Background(),
		[]string{"--to", verbSha, "--redis", mr.Addr(), "--machines", reg, "--play-dir", dir},
		&out, &errOut, rollDeps(f, map[string]string{"NS_ADMIN": "pw"}, &sleeps))
	if code != 1 || !strings.Contains(out.String(), "VERIFY space want="+verbVersion+" have=none behind\n") ||
		!strings.HasSuffix(out.String(), "behind=space\n") || sleeps != 12 || strings.HasPrefix(f.calls[0], "git ls-remote") {
		t.Fatalf("behind: code=%d sleeps=%d calls=%v\n%s", code, sleeps, f.calls, out.String())
	}
}

// TestFleetRollUsage: usage is exit 2 and a refusal exit 1, both one line on
// standard error before any child runs.
func TestFleetRollUsage(t *testing.T) {
	t.Parallel()
	mr := miniredis.RunT(t)
	reg := releaseRegistry(t)
	dir := rollPlayDir(t)
	for _, tc := range []struct {
		args []string
		env  map[string]string
		code int
		want string
	}{
		{[]string{"deadbeef"}, nil, 2, "takes no positional arguments"},
		{[]string{"--wait", "-1s"}, nil, 2, "--wait must be >= 0"},
		{[]string{"--play", "../x.yml"}, nil, 2, "--play names a play file"},
		{[]string{"--nope"}, nil, 2, "flag provided but not defined"},
		{[]string{"--redis", mr.Addr()}, nil, 1, "FLEET ROLL REFUSED: the play's inventory reads the machines registry"},
		{[]string{"--redis", mr.Addr(), "--machines", filepath.Join(t.TempDir(), "none.tsv")}, nil, 1, "FLEET ROLL REFUSED: machines registry:"},
		{[]string{"--redis", mr.Addr(), "--machines", reg, "--play-dir", t.TempDir()}, nil, 1, "FLEET ROLL REFUSED: the fleet play directory"},
		{[]string{"--redis", mr.Addr(), "--play-dir", dir, "--to", "nothex"}, map[string]string{fleetbuild.MachinesEnv: reg}, 1, "FLEET ROLL REFUSED: \"nothex\" is not a commit sha"},
	} {
		f := &verbRollFake{verbRelFake: &verbRelFake{home: t.TempDir()}}
		sleeps := 0
		var out, errOut bytes.Buffer
		code := runFleetRollWith(context.Background(), tc.args, &out, &errOut, rollDeps(f, tc.env, &sleeps))
		if code != tc.code || !strings.Contains(errOut.String(), tc.want) || len(f.calls) != 0 {
			t.Errorf("%v: code=%d out=%q err=%q calls=%v", tc.args, code, out.String(), errOut.String(), f.calls)
		}
	}
}
