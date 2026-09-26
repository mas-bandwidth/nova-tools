package fleetbuild

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/table"
)

// playRecap is ansible-playbook's real output (ansible-core 2.21, the
// profile_roles callback on), recorded from a throwaway play on local
// connections: batman ran every role (tools changed, an ignored failure in
// bench), hetzner was unreachable at facts, hulk stopped in bench, space ran
// both plays (bench changed, then the second play's own task).
func playRecap(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "play-recap.txt"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

const (
	playTop = "/home/rowan/rowan-working/rowan-tools"
	playSha = "0123456789abcdef0123456789abcdef01234567"
)

// playFake is the play's ExecRunner: the four git reads of the clone check
// and the play itself. Nothing starts a process or reaches a host.
type playFake struct {
	porcelain  string // git status --porcelain
	behind     string // git rev-list --count answer
	noUpstream bool
	notClone   bool
	playOut    string
	playErr    error
	mu         sync.Mutex
	calls      []call
}

func (f *playFake) Run(ctx context.Context, dir string, env []string, argv []string) (string, error) {
	f.mu.Lock()
	f.calls = append(f.calls, call{dir, env, argv})
	f.mu.Unlock()
	switch strings.Join(argv[:2], " ") {
	case "git rev-parse":
		if f.notClone {
			return "fatal: not a git repository (or any of the parent directories): .git\n", errors.New("exit status 128")
		}
		return playTop + "\n" + playSha + "\n", nil
	case "git status":
		return f.porcelain, nil
	case "git fetch":
		return "", nil
	case "git rev-list":
		if f.noUpstream {
			return "fatal: no upstream configured for branch 'main'\n", errors.New("exit status 128")
		}
		if f.behind == "" {
			return "0\n", nil
		}
		return f.behind + "\n", nil
	}
	if argv[0] == "ansible-playbook" {
		return f.playOut, f.playErr
	}
	return "", errors.New("unexpected child " + strings.Join(argv, " "))
}

func (f *playFake) plays() []call {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []call
	for _, c := range f.calls {
		if c.argv[0] == "ansible-playbook" {
			out = append(out, c)
		}
	}
	return out
}

var playMachines = []Machine{{Name: "batman"}, {Name: "hetzner"}, {Name: "hulk"}, {Name: "space"}, {Name: "studio"}}

func newPlay(t *testing.T, f *playFake, c *redis.Client) (*Play, *strings.Builder) {
	t.Helper()
	var out strings.Builder
	dir := t.TempDir()
	for _, n := range []string{PlayInventory, "bench.yml"} {
		if err := os.WriteFile(filepath.Join(dir, n), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return &Play{Runner: f, Client: c, PlayDir: dir, Tag: "bench", Registry: "/reg/machines.tsv",
		Machines: playMachines, Out: &out,
		Now: func() time.Time { return time.UnixMilli(1790000000000) }}, &out
}

// TestParsePlayReadsTheRecordedRecap: every bench of the recap, every role
// it ran in order, the state per role (an ignored failure is ok, an
// unreachable bench stops at facts) and each role's time from ROLES RECAP
// (a play's own task under tasks).
func TestParsePlayReadsTheRecordedRecap(t *testing.T) {
	t.Parallel()
	got := ParsePlay(playRecap(t))
	var lines []string
	var results []string
	for _, h := range got {
		for _, r := range h.Roles {
			lines = append(lines, r.Line())
		}
		results = append(results, h.Host+" "+h.Last()+" "+h.Result())
	}
	want := []string{
		"FLEET PLAY batman facts ok ms=550",
		"FLEET PLAY batman bench ok ms=630",
		"FLEET PLAY batman tools changed ms=290",
		"FLEET PLAY hetzner facts failed ms=550",
		"FLEET PLAY hulk facts ok ms=550",
		"FLEET PLAY hulk bench failed ms=630",
		"FLEET PLAY space facts ok ms=550",
		"FLEET PLAY space bench changed ms=630",
		"FLEET PLAY space tools ok ms=290",
		"FLEET PLAY space tasks ok ms=240",
	}
	if !reflect.DeepEqual(lines, want) {
		t.Errorf("receipts:\n%s\nwant:\n%s", strings.Join(lines, "\n"), strings.Join(want, "\n"))
	}
	wantResults := []string{"batman tools ok", "hetzner facts failed:facts", "hulk bench failed:bench", "space tasks ok"}
	if !reflect.DeepEqual(results, wantResults) {
		t.Errorf("results %q, want %q", results, wantResults)
	}
}

// TestParsePlayEdges: a rescued failure (recap clean) is ok; a recap failure
// the task lines never showed stops the bench at its last role; no ROLES
// RECAP is ms=-; no PLAY RECAP is no benches.
func TestParsePlayEdges(t *testing.T) {
	t.Parallel()
	out := "TASK [tools : install] ****\n" +
		"fatal: [hulk]: FAILED! => {}\n" +
		"changed: [hulk -> localhost]\n" +
		"ok: [space]\n" +
		"\nPLAY RECAP ****\n" +
		"hulk                       : ok=2    changed=1    unreachable=0    failed=0    skipped=0    rescued=1    ignored=0\n" +
		"space                      : ok=1    changed=0    unreachable=0    failed=1    skipped=0    rescued=0    ignored=0\n"
	var lines []string
	for _, h := range ParsePlay(out) {
		for _, r := range h.Roles {
			lines = append(lines, r.Line())
		}
		lines = append(lines, h.Result())
	}
	want := []string{"FLEET PLAY hulk tools changed ms=-", "ok", "FLEET PLAY space tools ok ms=-", "failed:tools"}
	if !reflect.DeepEqual(lines, want) {
		t.Errorf("got %q, want %q", lines, want)
	}
	if got := ParsePlay("ERROR! the playbook: bench.yml could not be found\n"); len(got) != 0 {
		t.Errorf("no recap parsed as %+v", got)
	}
}

// TestPlayRunsThroughTheReleaseRunner is #4356 C's DONE-WHEN on the recorded
// play: the clone checked (clean, not behind) before the play, the play run
// with fleet release's argv and env plus the profile callback, one receipt per
// bench and role, each bench's receipt in bench:<b>:play, and FAIL last with
// the benches that stopped.
func TestPlayRunsThroughTheReleaseRunner(t *testing.T) {
	t.Parallel()
	mr := miniredis.RunT(t)
	c := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer c.Close()
	f := &playFake{playOut: playRecap(t), playErr: errors.New("exit status 4")}
	p, out := newPlay(t, f, c)
	p.Limit = []string{"batman", "hetzner", "hulk", "space"}

	res, err := p.Run(context.Background())
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out.String())
	}
	if res.OK() || !reflect.DeepEqual(res.Failed(), []string{"hetzner:facts", "hulk:bench"}) {
		t.Fatalf("result %+v", res)
	}
	var heads []string
	for _, cl := range f.calls {
		heads = append(heads, cl.dir+" "+strings.Join(cl.argv, " "))
	}
	wantHeads := []string{
		p.PlayDir + " git rev-parse --show-toplevel HEAD",
		playTop + " git status --porcelain",
		playTop + " git fetch -q",
		playTop + " git rev-list --count HEAD..@{u}",
		p.PlayDir + " ansible-playbook -i inventory.py bench.yml --forks 16 --diff --limit batman,hetzner,hulk,space",
	}
	if !reflect.DeepEqual(heads, wantHeads) {
		t.Errorf("children:\n%s\nwant:\n%s", strings.Join(heads, "\n"), strings.Join(wantHeads, "\n"))
	}
	if env := strings.Join(f.plays()[0].env, " "); env != "ANSIBLE_NOCOWS=1 ANSIBLE_HOST_KEY_CHECKING=True FLEET_REGISTRY=/reg/machines.tsv ANSIBLE_CALLBACKS_ENABLED=ansible.posix.profile_roles" {
		t.Errorf("play env %q", env)
	}
	s := out.String()
	if !strings.HasPrefix(s, "FLEET PLAY batman facts ok ms=550\n") || !strings.Contains(s, "FLEET PLAY hulk bench failed ms=630\n") ||
		strings.Count(s, "\n") != 10 {
		t.Errorf("receipts:\n%s", s)
	}
	if line := res.Line(); line != "FLEET PLAY FAIL tag=bench sha=0123456789ab benches=4 failed=hetzner:facts,hulk:bench" {
		t.Errorf("last line %q", line)
	}
	for host, want := range map[string]map[string]string{
		"batman":  {"at": "1790000000000", "tag": "bench", "sha": playSha, "role": "tools", "result": "ok"},
		"hetzner": {"at": "1790000000000", "tag": "bench", "sha": playSha, "role": "facts", "result": "failed:facts"},
		"hulk":    {"at": "1790000000000", "tag": "bench", "sha": playSha, "role": "bench", "result": "failed:bench"},
		"space":   {"at": "1790000000000", "tag": "bench", "sha": playSha, "role": "tasks", "result": "ok"},
	} {
		got, err := c.HGetAll(context.Background(), PlayKey(host)).Result()
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("%s = %v %v, want %v", PlayKey(host), got, err, want)
		}
	}
	if BehindRole("failed:bench") != "bench" || BehindRole("ok") != "" {
		t.Error("BehindRole")
	}
}

// TestPlayOKAndDryRun: a play where every bench ran every role is OK; the
// dry run is ansible's --check, needs no store and writes nothing.
func TestPlayOKAndDryRun(t *testing.T) {
	t.Parallel()
	good := "TASK [bench : x] ****\nok: [batman]\nchanged: [space]\n\nPLAY RECAP ****\n" +
		"batman : ok=1 changed=0 unreachable=0 failed=0\nspace : ok=1 changed=1 unreachable=0 failed=0\n"
	mr := miniredis.RunT(t)
	c := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer c.Close()
	p, out := newPlay(t, &playFake{playOut: good}, c)
	res, err := p.Run(context.Background())
	if err != nil || !res.OK() || res.Line() != "FLEET PLAY OK tag=bench sha=0123456789ab benches=2 failed=-" {
		t.Fatalf("ok play: %v %s\n%s", err, res.Line(), out.String())
	}
	if got := mr.HGet(PlayKey("space"), "result"); got != "ok" {
		t.Errorf("space result %q", got)
	}

	f := &playFake{playOut: good}
	p, out = newPlay(t, f, nil)
	p.DryRun = true
	res, err = p.Run(context.Background())
	if err != nil || !res.OK() || !strings.HasSuffix(res.Line(), " check=yes") {
		t.Fatalf("dry run: %v %s\n%s", err, res.Line(), out.String())
	}
	if argv := strings.Join(f.plays()[0].argv, " "); !strings.HasSuffix(argv, "--diff --check") {
		t.Errorf("dry run argv %q", argv)
	}
	if out.String() != "FLEET PLAY batman bench ok ms=-\nFLEET PLAY space bench changed ms=-\n" {
		t.Errorf("dry run receipts:\n%s", out.String())
	}
}

// TestPlayWithNoRecapFailsAndWritesNothing: a play that dies before any
// bench (a missing collection, a syntax error) is PLAY ERROR and FAIL, and no
// receipt is written.
func TestPlayWithNoRecapFailsAndWritesNothing(t *testing.T) {
	t.Parallel()
	mr := miniredis.RunT(t)
	c := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer c.Close()
	p, out := newPlay(t, &playFake{playOut: "ERROR! couldn't resolve module/action 'x'\n", playErr: errors.New("exit status 4")}, c)
	res, err := p.Run(context.Background())
	if err != nil || res.OK() || res.Err != "exit_status_4" {
		t.Fatalf("%v %+v", err, res)
	}
	if out.String() != "PLAY ERROR bench.yml err=exit_status_4 last=ERROR! couldn't resolve module/action 'x'\n" ||
		res.Line() != "FLEET PLAY FAIL tag=bench sha=0123456789ab benches=0 failed=-" {
		t.Errorf("out %q line %q", out.String(), res.Line())
	}
	if keys := mr.Keys(); len(keys) != 0 {
		t.Errorf("keys written: %v", keys)
	}
}

// TestPlayRefusesBeforeThePlay: a dirty clone, a clone behind its upstream,
// one with no upstream, a play directory outside a clone, an unknown
// --limit bench, a bad tag, a missing play file and no store each refuse
// with the remedy, and the play never runs.
func TestPlayRefusesBeforeThePlay(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		fake  *playFake
		edit  func(p *Play)
		match string
	}{
		{"dirty", &playFake{porcelain: " M fleet/bench.yml\n?? bin/x.before\n"}, nil,
			"the rowan-tools clone " + playTop + " is dirty (2 paths, first M fleet/bench.yml): commit and push it, or git -C " + playTop + " stash -u"},
		{"behind", &playFake{behind: "5"}, nil,
			"the rowan-tools clone " + playTop + " is 5 commits behind its upstream: git -C " + playTop + " pull --ff-only"},
		{"no upstream", &playFake{noUpstream: true}, nil, "has no upstream branch: git -C " + playTop + " switch main"},
		{"not a clone", &playFake{notClone: true}, nil, "is not in a git clone of rowan-tools"},
		{"limit", &playFake{}, func(p *Play) { p.Limit = []string{"hulkk"} }, "--limit hulkk is not a machine in the registry /reg/machines.tsv"},
		{"tag", &playFake{}, func(p *Play) { p.Tag = "../x" }, `"../x" is not a play name`},
		{"no play file", &playFake{}, func(p *Play) { p.Tag = "tools" }, "has no tools.yml"},
		{"no store", &playFake{}, func(p *Play) { p.Client = nil }, "--redis <addr>, or --dry-run"},
		{"no registry", &playFake{}, func(p *Play) { p.Registry = "" }, "--machines <file>, or NOVA_FLEET_MACHINES"},
	} {
		f := tc.fake
		mr := miniredis.RunT(t)
		c := redis.NewClient(&redis.Options{Addr: mr.Addr()})
		p, _ := newPlay(t, f, c)
		if tc.edit != nil {
			tc.edit(p)
		}
		_, err := p.Run(context.Background())
		c.Close()
		if !errors.Is(err, ErrRefused) || !strings.Contains(err.Error(), tc.match) {
			t.Errorf("%s: err = %v, want a refusal naming %q", tc.name, err, tc.match)
		}
		if len(f.plays()) != 0 || len(mr.Keys()) != 0 {
			t.Errorf("%s: the play ran or a receipt was written", tc.name)
		}
	}
}

// TestPlayReceiptSpellingIsTheTables: the table reads the receipt this
// package writes under its own spelling (fleetbuild's tests import table,
// so table cannot import fleetbuild); the two agree.
func TestPlayReceiptSpellingIsTheTables(t *testing.T) {
	t.Parallel()
	if table.BenchPlayKey("hulk") != PlayKey("hulk") {
		t.Errorf("table key %q, fleetbuild key %q", table.BenchPlayKey("hulk"), PlayKey("hulk"))
	}
	for _, h := range []HostPlay{{Host: "hulk"}, {Host: "hulk", Failed: "tools"}, {Host: "hulk", Failed: FactsRole}} {
		if got, want := table.PlayBehind(h.Result()), BehindRole(h.Result()); got != want || got != h.Failed {
			t.Errorf("result %q: table %q, fleetbuild %q", h.Result(), got, want)
		}
	}
}
