package fleetbuild

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
)

const rollRecap = `PLAY [benches] *****

TASK [tools : install] *****
ok: [hulk]

PLAY RECAP *********************************************************************
batman                     : ok=9    changed=0    unreachable=0    failed=0
hulk                       : ok=9    changed=1    unreachable=0    failed=0
`

// rollFake is the roll's ExecRunner: dev's tip from git ls-remote, the play
// answered with a recap (or a failure), everything else the release fake.
type rollFake struct {
	*relFake
	tip     string // what ls-remote answers
	playErr bool
	mu      sync.Mutex
	plays   []call
}

func (f *rollFake) Run(ctx context.Context, dir string, env []string, argv []string) (string, error) {
	record := func() {
		f.relFake.mu.Lock()
		f.relFake.call = append(f.relFake.call, call{dir, env, argv})
		f.relFake.mu.Unlock()
	}
	switch {
	case argv[0] == "ansible-playbook":
		record()
		f.mu.Lock()
		f.plays = append(f.plays, call{dir, env, argv})
		f.mu.Unlock()
		if f.playErr {
			return rollRecap + "hulk : ok=3 changed=0 unreachable=1 failed=0\n", errors.New("exit status 4")
		}
		return rollRecap, nil
	case argv[0] == "git" && argv[1] == "ls-remote":
		record()
		return f.tip, nil
	}
	return f.relFake.Run(ctx, dir, env, argv)
}

func (f *rollFake) count(prefix string) int {
	n := 0
	for _, h := range f.heads() {
		if strings.HasPrefix(h, prefix) {
			n++
		}
	}
	return n
}

func playDir(t *testing.T) string {
	t.Helper()
	d := t.TempDir()
	for _, f := range []string{PlayInventory, DefaultPlay} {
		if err := os.WriteFile(filepath.Join(d, f), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return d
}

func beatLine(v, osArch string) string { return "nova-sprint " + v + " " + osArch + " go1.25.1" }

func newRoll(t *testing.T, f *rollFake, mr *miniredis.Miniredis) (*Roll, *strings.Builder, *int) {
	t.Helper()
	r, _ := newRelease(t, f.relFake, mr)
	r.Runner = f
	var out strings.Builder
	r.Out = &out
	sleeps := 0
	roll := &Roll{Release: r, PlayDir: playDir(t), Registry: "/reg/machines.tsv", Wait: 10 * time.Second, Poll: 5 * time.Second,
		Sleep: func(context.Context, time.Duration) error { sleeps++; return nil }}
	return roll, &out, &sleeps
}

// TestRollReleasesPlaysAndVerifies is #4332's DONE-WHEN on the good path:
// dev's tip resolved, the release run, the play run through ansible with the
// registry as its inventory, and every bench beat read back at the version.
// The beat that lags is re-read after one pause and is then on it.
func TestRollReleasesPlaysAndVerifies(t *testing.T) {
	t.Parallel()
	mr := miniredis.RunT(t)
	mr.SAdd("benches", "space", "hulk", "batman")
	mr.HSet("bench:space:beat", "build", beatLine(relVersion, "linux/amd64"))
	mr.HSet("bench:hulk:beat", "build", beatLine(relVersion, "linux/amd64"))
	mr.HSet("bench:batman:beat", "build", beatLine("v0.16.0-dev.00000000", "darwin/amd64"))
	f := &rollFake{relFake: &relFake{home: t.TempDir()}, tip: relSha + "\trefs/heads/dev\n"}
	roll, out, sleeps := newRoll(t, f, mr)
	roll.Sleep = func(context.Context, time.Duration) error {
		*sleeps++
		mr.HSet("bench:batman:beat", "build", beatLine(relVersion, "darwin/amd64"))
		return nil
	}

	res, err := roll.Run(context.Background(), "")
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out.String())
	}
	if !res.OK() || res.Play != "ok" || len(res.Behind()) != 0 || *sleeps != 1 || res.Release.Version != relVersion {
		t.Fatalf("result %+v sleeps=%d\n%s", res, *sleeps, out.String())
	}
	if len(f.plays) != 1 {
		t.Fatalf("plays %d", len(f.plays))
	}
	p := f.plays[0]
	if p.dir != roll.PlayDir ||
		strings.Join(p.argv, " ") != "ansible-playbook -i inventory.py tools.yml --forks 16 --diff -e nova_build="+relVersion ||
		strings.Join(p.env, " ") != "ANSIBLE_NOCOWS=1 ANSIBLE_HOST_KEY_CHECKING=True FLEET_REGISTRY=/reg/machines.tsv" {
		t.Errorf("play: dir=%s argv=%q env=%q", p.dir, p.argv, p.env)
	}
	heads := f.heads()
	if heads[0] != "git ls-remote" || heads[1] != "git clone" {
		t.Errorf("dev tip not first: %v", heads)
	}
	s := out.String()
	for _, want := range []string{
		"DEV TIP " + relSha + "\n",
		"FLEET RELEASE OK version=" + relVersion + " commit=c8178673f5e1 studio=built fn=ok rolled=1 skipped=2\n",
		"RECAP batman : ok=9 changed=0 unreachable=0 failed=0\nRECAP hulk : ok=9 changed=1 unreachable=0 failed=0\n",
		"PLAY OK tools.yml version=" + relVersion + "\n",
		"VERIFY space want=" + relVersion + " have=" + relVersion + " ok\n" +
			"VERIFY hulk want=" + relVersion + " have=" + relVersion + " ok\n" +
			"VERIFY batman want=" + relVersion + " have=" + relVersion + " ok\n",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in\n%s", want, s)
		}
	}
	if !strings.HasSuffix(s, "VERIFY batman want="+relVersion+" have="+relVersion+" ok\n") {
		t.Errorf("verify is not last:\n%s", s)
	}
	if got := res.Line(); got != "FLEET ROLL OK version="+relVersion+" commit=c8178673f5e1 fn=ok play=ok benches=3 behind=-" {
		t.Errorf("line %q", got)
	}
}

// TestRollNamesTheBenchesBehind: a bench whose beat stays on another version
// and a bench with no beat are behind after the wait is spent (three reads
// of a 10 s wait at 5 s, two pauses), named on the last line.
func TestRollNamesTheBenchesBehind(t *testing.T) {
	t.Parallel()
	mr := miniredis.RunT(t)
	mr.SAdd("benches", "space", "hulk", "batman")
	mr.HSet("bench:space:beat", "build", beatLine(relVersion, "linux/amd64"))
	mr.HSet("bench:hulk:beat", "build", beatLine("v0.16.0-dev.00000000", "linux/amd64"))
	f := &rollFake{relFake: &relFake{home: t.TempDir()}}
	roll, out, sleeps := newRoll(t, f, mr)
	roll.Release.Benches = []string{"space", "hulk", "batman"}

	res, err := roll.Run(context.Background(), relSha)
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out.String())
	}
	if res.OK() || strings.Join(res.Behind(), ",") != "hulk,batman" || *sleeps != 2 {
		t.Fatalf("result %+v sleeps=%d", res, *sleeps)
	}
	if got := strings.Join(f.plays[0].argv[len(f.plays[0].argv)-2:], " "); got != "--limit space,hulk,batman" {
		t.Errorf("limit %q", got)
	}
	for _, want := range []string{
		"VERIFY space want=" + relVersion + " have=" + relVersion + " ok\n",
		"VERIFY hulk want=" + relVersion + " have=v0.16.0-dev.00000000 behind\n",
		"VERIFY batman want=" + relVersion + " have=none behind\n",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q in\n%s", want, out.String())
		}
	}
	if got := res.Line(); got != "FLEET ROLL BEHIND version="+relVersion+" commit=c8178673f5e1 fn=ok play=ok benches=3 behind=hulk,batman" {
		t.Errorf("line %q", got)
	}
}

// TestRollVerifiesAfterAFailedPlay: a failed play is a FAIL line and the
// verify still reads every beat; the play and the verify never ssh (the
// fleet changes only through ansible, the version only from the store).
func TestRollVerifiesAfterAFailedPlay(t *testing.T) {
	t.Parallel()
	mr := miniredis.RunT(t)
	mr.SAdd("benches", "space", "hulk", "batman")
	for _, b := range []string{"space", "hulk", "batman"} {
		mr.HSet("bench:"+b+":beat", "build", beatLine(relVersion, "linux/amd64"))
	}
	f := &rollFake{relFake: &relFake{home: t.TempDir()}, playErr: true}
	roll, out, sleeps := newRoll(t, f, mr)

	res, err := roll.Run(context.Background(), relSha[:8])
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out.String())
	}
	if res.OK() || res.Play != "failed" || len(res.Behind()) != 0 || len(res.Checks) != 3 || *sleeps != 0 {
		t.Fatalf("result %+v sleeps=%d", res, *sleeps)
	}
	if !strings.Contains(out.String(), "RECAP hulk : ok=3 changed=0 unreachable=1 failed=0\nPLAY FAIL tools.yml version="+relVersion+" err=exit_status_4 last=hulk : ok=3 changed=0 unreachable=1 failed=0\n") {
		t.Errorf("no PLAY FAIL:\n%s", out.String())
	}
	if got := res.Line(); got != "FLEET ROLL FAIL version="+relVersion+" commit=c8178673f5e1 fn=ok play=failed benches=3 behind=-" {
		t.Errorf("line %q", got)
	}
	// Every bench was already on the release, so the release rolled none:
	// no call of this run reached a host by ssh.
	if n := f.count("ssh "); n != 0 {
		t.Errorf("%d ssh calls: %v", n, f.heads())
	}
}

// TestRollRefusesBeforeAnyStep: what would stop the roll halfway is refused
// before the first child, each naming its remedy.
func TestRollRefusesBeforeAnyStep(t *testing.T) {
	t.Parallel()
	mr := miniredis.RunT(t)
	for _, tc := range []struct {
		name string
		edit func(r *Roll)
		want string
	}{
		{"no store", func(r *Roll) { r.Release.Client = nil }, "needs the fleet store"},
		{"studio only", func(r *Roll) { r.Release.StudioOnly = true }, "belong to fleet release"},
		{"no registry", func(r *Roll) { r.Registry = "" }, "reads the machines registry"},
		{"no play dir", func(r *Roll) { r.PlayDir = "" }, "no fleet play directory"},
		{"no play", func(r *Roll) { r.Play = "nope.yml" }, "has no nope.yml"},
		{"no inventory", func(r *Roll) { os.Remove(filepath.Join(r.PlayDir, PlayInventory)) }, "has no inventory.py"},
		{"no benches", func(r *Roll) { r.Release.Machines = nil }, "no benches to roll"},
	} {
		f := &rollFake{relFake: &relFake{home: t.TempDir()}}
		roll, _, _ := newRoll(t, f, mr)
		tc.edit(roll)
		_, err := roll.Run(context.Background(), relSha)
		if !errors.Is(err, ErrRefused) || !strings.Contains(err.Error(), tc.want) || len(f.heads()) != 0 {
			t.Errorf("%s: err=%v calls=%v", tc.name, err, f.heads())
		}
	}
	// A tip that is not one refuses after the one ls-remote, before the release.
	f := &rollFake{relFake: &relFake{home: t.TempDir()}, tip: "fatal: could not read\n"}
	roll, _, _ := newRoll(t, f, mr)
	if _, err := roll.Run(context.Background(), ""); !errors.Is(err, ErrRefused) || !strings.Contains(err.Error(), "--to <sha> names the commit") ||
		strings.Join(f.heads(), ",") != "git ls-remote" {
		t.Errorf("bad tip: err=%v calls=%v", err, f.heads())
	}
}

// TestRollPieces holds the pure parts: the tip parse, the recap, the play
// argv and the verify line.
func TestRollPieces(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		relSha + "\trefs/heads/dev\n":  relSha,
		relSha + "\trefs/heads/main\n": "",
		"deadbeef\trefs/heads/dev\n":   "",
		"":                             "",
	} {
		if got, ok := ParseDevTip(in); got != want || ok != (want != "") {
			t.Errorf("ParseDevTip(%q) = %q %v", in, got, ok)
		}
	}
	if got := Recap(rollRecap); strings.Join(got, "|") != "batman : ok=9 changed=0 unreachable=0 failed=0|hulk : ok=9 changed=1 unreachable=0 failed=0" {
		t.Errorf("recap %q", got)
	}
	if got := Recap("no recap here\n"); got != nil {
		t.Errorf("recap of nothing %q", got)
	}
	if got := strings.Join(PlayArgv("bench.yml", relVersion, []string{"a", "b"}), " "); got != "ansible-playbook -i inventory.py bench.yml --forks 16 --diff -e nova_build="+relVersion+" --limit a,b" {
		t.Errorf("argv %q", got)
	}
	if got := (BeatCheck{Bench: "x", Want: relVersion, Have: "odd build"}).Line(); got != "VERIFY x want="+relVersion+" have=odd build behind" {
		t.Errorf("line %q", got)
	}
	if got := beatVersion("odd  build\n"); got != "odd_build" {
		t.Errorf("beatVersion %q", got)
	}
}
