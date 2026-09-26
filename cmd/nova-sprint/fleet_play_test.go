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

// verbPlayFake answers the clone check and the play; nothing else runs.
type verbPlayFake struct {
	porcelain string
	playOut   string
	mu        sync.Mutex
	calls     []string
}

func (f *verbPlayFake) Run(ctx context.Context, dir string, env []string, argv []string) (string, error) {
	f.mu.Lock()
	f.calls = append(f.calls, strings.Join(argv, " "))
	f.mu.Unlock()
	switch strings.Join(argv[:2], " ") {
	case "git rev-parse":
		return dir + "\n" + verbSha + "\n", nil
	case "git status":
		return f.porcelain, nil
	case "git fetch":
		return "", nil
	case "git rev-list":
		return "0\n", nil
	case "ansible-playbook -i":
		return f.playOut, nil
	}
	return "", errors.New("unexpected child " + strings.Join(argv, " "))
}

func (f *verbPlayFake) played() bool {
	for _, c := range f.calls {
		if strings.HasPrefix(c, "ansible-playbook") {
			return true
		}
	}
	return false
}

func playDeps(f *verbPlayFake, env map[string]string) releaseDeps {
	return releaseDeps{Runner: f, Home: func() (string, error) { return "/home/none", nil },
		Getenv: func(k string) string { return env[k] }, Open: openFleetStore}
}

func toolsPlayDir(t *testing.T) string {
	t.Helper()
	d := t.TempDir()
	for _, f := range []string{"inventory.py", "tools.yml"} {
		if err := os.WriteFile(filepath.Join(d, f), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return d
}

const verbPlayOut = "TASK [tools : install] ****\nchanged: [hulk]\nok: [space]\n" +
	"TASK [tools : verify] ****\nok: [space]\nfatal: [hulk]: FAILED! => {}\n" +
	"\nPLAY RECAP ****\nhulk : ok=1 changed=1 unreachable=0 failed=1\nspace : ok=2 changed=0 unreachable=0 failed=0\n" +
	"\nROLES RECAP ****\ntools ----- 12.35s\ntotal ----- 12.35s\n"

// TestFleetPlayVerb drives `fleet play tools` end to end with fakes: the
// clone check, the play from the play directory narrowed by --limit, one
// receipt per bench and role, bench:<b>:play written, and FLEET PLAY FAIL
// last naming the bench that stopped, exit 1; an all-ok play is exit 0.
func TestFleetPlayVerb(t *testing.T) {
	t.Parallel()
	mr := miniredis.RunT(t)
	reg := releaseRegistry(t)
	dir := toolsPlayDir(t)
	f := &verbPlayFake{playOut: verbPlayOut}
	var out, errOut bytes.Buffer
	code := runFleetPlayWith(context.Background(), []string{"--play", "tools", "--bench", "hulk,space", "--redis", mr.Addr(), "--machines", reg},
		&out, &errOut, playDeps(f, map[string]string{fleetbuild.PlayDirEnv: dir}))
	if code != 1 || errOut.Len() != 0 {
		t.Fatalf("code=%d\n%s\nerr=%s", code, out.String(), errOut.String())
	}
	if want := "FLEET PLAY hulk tools failed ms=12350\nFLEET PLAY space tools ok ms=12350\n" +
		"FLEET PLAY FAIL tag=tools sha=c8178673f5e1 benches=2 failed=hulk:tools\n"; out.String() != want {
		t.Errorf("out:\n%s\nwant:\n%s", out.String(), want)
	}
	if !strings.Contains(strings.Join(f.calls, "\n"), "ansible-playbook -i inventory.py tools.yml --forks 16 --diff --limit hulk,space") {
		t.Errorf("calls %q", f.calls)
	}
	if mr.HGet("bench:hulk:play", "result") != "failed:tools" || mr.HGet("bench:space:play", "result") != "ok" ||
		mr.HGet("bench:space:play", "sha") != verbSha || mr.HGet("bench:space:play", "tag") != "tools" {
		t.Errorf("receipts: %v", mr.Keys())
	}

	f = &verbPlayFake{playOut: "PLAY RECAP ****\nspace : ok=2 changed=0 unreachable=0 failed=0\n"}
	out.Reset()
	code = runFleetPlayWith(context.Background(), []string{"--play", "tools.yml", "--dry-run", "--play-dir", dir, "--machines", reg},
		&out, &errOut, playDeps(f, nil))
	if code != 0 || !strings.HasSuffix(out.String(), "FLEET PLAY OK tag=tools sha=c8178673f5e1 benches=1 failed=- check=yes\n") {
		t.Fatalf("dry run: code=%d\n%s err=%s", code, out.String(), errOut.String())
	}
}

// TestFleetPlayUsage: usage is exit 2 and a refusal exit 1, each one line on
// standard error, and the play never runs.
func TestFleetPlayUsage(t *testing.T) {
	t.Parallel()
	mr := miniredis.RunT(t)
	reg := releaseRegistry(t)
	dir := toolsPlayDir(t)
	for _, tc := range []struct {
		args  []string
		env   map[string]string
		dirty string
		code  int
		want  string
	}{
		{nil, nil, "", 2, "wants --play <tag>"},
		{[]string{"tools"}, nil, "", 2, "takes flags, not positional arguments"},
		{[]string{"--play", "tools", "--nope"}, nil, "", 2, "--nope is not a flag of nova-sprint fleet play"},
		{[]string{"--play", "tools", "--limit", "hulk"}, nil, "", 2, "--limit is not a flag of nova-sprint fleet play"},
		{[]string{"--play", "tools", "--redis", mr.Addr()}, nil, "", 1, "FLEET PLAY REFUSED: the play's inventory reads the machines registry"},
		{[]string{"--play", "tools", "--machines", filepath.Join(t.TempDir(), "none.tsv")}, nil, "", 1, "FLEET PLAY REFUSED: machines registry:"},
		{[]string{"--play", "tools", "--redis", mr.Addr(), "--machines", reg, "--play-dir", t.TempDir()}, nil, "", 1, "FLEET PLAY REFUSED: the fleet play directory"},
		{[]string{"--play", "tools", "--redis", mr.Addr(), "--play-dir", dir, "--bench", "batman"}, map[string]string{fleetbuild.MachinesEnv: reg}, "", 1,
			"FLEET PLAY REFUSED: --bench batman is not a machine in the registry"},
		{[]string{"--play", "tools", "--redis", mr.Addr(), "--play-dir", dir, "--machines", reg}, nil, "?? stray\n", 1,
			"FLEET PLAY REFUSED: the rowan-tools clone " + dir + " is dirty (1 paths, first ?? stray): commit and push it, or git -C " + dir + " stash -u"},
	} {
		f := &verbPlayFake{porcelain: tc.dirty}
		var out, errOut bytes.Buffer
		code := runFleetPlayWith(context.Background(), tc.args, &out, &errOut, playDeps(f, tc.env))
		if code != tc.code || !strings.Contains(errOut.String(), tc.want) || strings.Count(errOut.String(), "\n") != 1 || f.played() {
			t.Errorf("%v: code=%d out=%q err=%q calls=%v", tc.args, code, out.String(), errOut.String(), f.calls)
		}
	}
	if keys := mr.Keys(); len(keys) != 0 {
		t.Errorf("a refusal wrote %v", keys)
	}
}
