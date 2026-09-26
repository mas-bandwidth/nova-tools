package preflight

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeSSHD is the control's sshd: it allows at most maxSessions open at once
// (the Studio sat at two for 40 minutes) and counts every session it accepts.
// It never touches a network; the dry children ack by returning.
type fakeSSHD struct {
	mu          sync.Mutex
	maxSessions int
	open        int
	refuse      bool          // refuse every session (a wedged sshd)
	ackDelay    time.Duration // how long each dry child takes to ack
}

func (d *fakeSSHD) Dial(ctx context.Context, bench string) (Session, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.refuse {
		return nil, errors.New("ssh: refused")
	}
	if d.maxSessions > 0 && d.open >= d.maxSessions {
		return nil, errors.New("ssh: MaxSessions exceeded")
	}
	d.open++
	return &fakeSession{d: d}, nil
}

type fakeSession struct {
	d      *fakeSSHD
	closed bool
}

func (s *fakeSession) LaunchDry(ctx context.Context, card string) error {
	if s.d.ackDelay > 0 {
		select {
		case <-time.After(s.d.ackDelay):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

func (s *fakeSession) Close() error {
	s.d.mu.Lock()
	defer s.d.mu.Unlock()
	if !s.closed {
		s.closed = true
		s.d.open--
	}
	return nil
}

// batchLauncher is the launcher the spec wants: one session carries the batch.
type batchLauncher struct{}

func (batchLauncher) LaunchBatch(ctx context.Context, bench string, cards []string, d Dialer) error {
	s, err := d.Dial(ctx, bench)
	if err != nil {
		return err
	}
	defer s.Close()
	for _, c := range cards {
		if err := s.LaunchDry(ctx, c); err != nil {
			return err
		}
	}
	return nil
}

// perCardLauncher is the defect: a session per card, opened and closed in
// turn, so a two-session sshd lets every card through and only the count
// can see it.
type perCardLauncher struct{}

func (perCardLauncher) LaunchBatch(ctx context.Context, bench string, cards []string, d Dialer) error {
	for _, c := range cards {
		s, err := d.Dial(ctx, bench)
		if err != nil {
			return err
		}
		err = s.LaunchDry(ctx, c)
		s.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

// allLoaded is a fleet whose every collection snapshot was read.
func allLoaded() Loaded {
	return Loaded{Benches: true, Consumers: true, Profiles: true, Workflows: true,
		ReviewReady: true, LandReady: true, Orphans: true, EndedDone: true, Wake: true}
}

func upBench(name string, desired int) BenchState {
	return BenchState{Name: name, Desired: desired, BeatPresent: true, BeatAge: 500 * time.Millisecond,
		Launcher: BatchLauncherName}
}

func TestPreflightOneSessionPerBatch(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	benches := []BenchState{upBench("ctl-a", 50), upBench("ctl-b", 8)}

	t.Run("one session carries the batch: GREEN", func(t *testing.T) {
		in := FleetInput{Loaded: allLoaded(), Benches: benches, Dialer: &fakeSSHD{maxSessions: 2}, Launcher: batchLauncher{}}
		line := CheckOneSessionPerBatch(ctx, in)
		if line.Red {
			t.Fatalf("one session per batch must be green, got %s", line)
		}
		if line.Check != "7.6" || !strings.HasPrefix(line.String(), "GREEN 7.6 ") {
			t.Fatalf("line = %q", line.String())
		}
		if !strings.Contains(line.String(), "58 dry cards over 2 sessions") {
			t.Fatalf("green line must count the cards and sessions: %s", line)
		}
	})

	t.Run("a launcher opening a session per card: RED", func(t *testing.T) {
		in := FleetInput{Loaded: allLoaded(), Benches: benches, Dialer: &fakeSSHD{maxSessions: 2}, Launcher: perCardLauncher{}}
		line := CheckOneSessionPerBatch(ctx, in)
		if !line.Red {
			t.Fatalf("a session per card must be red, got %s", line)
		}
		for _, want := range []string{"RED 7.6 ", "ctl-a: 50 sessions for 50 cards", "ctl-b: 8 sessions for 8 cards"} {
			if !strings.Contains(line.String(), want) {
				t.Fatalf("line %q lacks %q", line.String(), want)
			}
		}
	})

	t.Run("sshd refuses the batch session: RED names the bench", func(t *testing.T) {
		in := FleetInput{Loaded: allLoaded(), Benches: []BenchState{upBench("ctl-wedged", 4)}, Dialer: &fakeSSHD{refuse: true}, Launcher: batchLauncher{}}
		line := CheckOneSessionPerBatch(ctx, in)
		if !line.Red || !strings.Contains(line.String(), "ctl-wedged: ssh: refused") {
			t.Fatalf("a refused session must be red and named: %s", line)
		}
	})

	t.Run("a dry child slower than the ack window: RED", func(t *testing.T) {
		in := FleetInput{Loaded: allLoaded(), Benches: []BenchState{upBench("ctl-slow", 2)}, Dialer: &fakeSSHD{ackDelay: 200 * time.Millisecond},
			Launcher: batchLauncher{}, AckWindow: 50 * time.Millisecond}
		line := CheckOneSessionPerBatch(ctx, in)
		if !line.Red || !strings.Contains(line.String(), "ctl-slow:") || !strings.Contains(line.String(), "timed out") {
			t.Fatalf("a batch past the ack window must be red and named: %s", line)
		}
	})

	t.Run("a launcher that returns without acking every card: RED", func(t *testing.T) {
		lazy := launcherFunc(func(ctx context.Context, bench string, cards []string, d Dialer) error {
			s, err := d.Dial(ctx, bench)
			if err != nil {
				return err
			}
			defer s.Close()
			return s.LaunchDry(ctx, cards[0])
		})
		in := FleetInput{Loaded: allLoaded(), Benches: []BenchState{upBench("ctl-lazy", 3)}, Dialer: &fakeSSHD{}, Launcher: lazy}
		line := CheckOneSessionPerBatch(ctx, in)
		if !line.Red || !strings.Contains(line.String(), "ctl-lazy: 1 of 3 dry children acked") {
			t.Fatalf("missing acks must be red: %s", line)
		}
	})

	t.Run("no launcher configured is MISSING, never green", func(t *testing.T) {
		line := CheckOneSessionPerBatch(ctx, FleetInput{Loaded: allLoaded(), Benches: benches})
		if !line.Red || !strings.Contains(line.String(), "MISSING") {
			t.Fatalf("no launcher must be red MISSING: %s", line)
		}
	})

	t.Run("paused and zero-desired benches are not dealt a dry batch", func(t *testing.T) {
		paused := upBench("ctl-paused", 4)
		paused.Paused = true
		in := FleetInput{Loaded: allLoaded(), Benches: []BenchState{paused, upBench("ctl-zero", 0)}, Dialer: &fakeSSHD{refuse: true}, Launcher: batchLauncher{}}
		line := CheckOneSessionPerBatch(ctx, in)
		if line.Red {
			t.Fatalf("no UP bench wants a batch; must be green: %s", line)
		}
	})
}

type launcherFunc func(ctx context.Context, bench string, cards []string, d Dialer) error

func (f launcherFunc) LaunchBatch(ctx context.Context, bench string, cards []string, d Dialer) error {
	return f(ctx, bench, cards, d)
}

const hostedLinuxGo = `name: ci
on: [push]
jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
      - run: go test ./...
  lint:
    runs-on: ubuntu-latest
    steps:
      - run: echo no go here
`

const matrixGo = `jobs:
  test:
    strategy:
      matrix:
        os: [ubuntu-latest, macos-latest, windows-latest]
    runs-on: ${{ matrix.os }}
    steps:
      - run: go test -race ./...
`

const windowsOnlyGo = `jobs:
  win:
    runs-on: windows-latest
    steps:
      - run: go test ./...
`

const selfHostedLinuxGo = `jobs:
  packages:
    runs-on: [self-hosted, linux, x64, space]
    steps:
      - run: |
          go vet ./...
          go test ./internal/...
`

const scriptedGo = `jobs:
  shard:
    runs-on: [self-hosted, linux, x64]
    steps:
      - run: |
          command -v go >/dev/null || exit 1
          go version
          scripts/shard.sh
`

const unresolvedGo = `jobs:
  test:
    runs-on: ${{ fromJSON(needs.plan.outputs.labels) }}
    steps:
      - run: go test ./...
`

func TestPreflightTwoSchedulersRed(t *testing.T) {
	t.Parallel()

	linuxGo := []BenchProfile{{Bench: "ctl-hulk", OS: "linux", Legs: []string{"go"}}}
	darwinGo := []BenchProfile{{Bench: "ctl-studio", OS: "darwin", Legs: []string{"go"}}}
	both := append(append([]BenchProfile{}, linuxGo...), darwinGo...)

	cases := []struct {
		name     string
		in       FleetInput
		red      bool
		mustHave []string
	}{
		{"a hosted Linux Go row while a bench carries the leg", FleetInput{Profiles: linuxGo,
			Workflows: []Workflow{{Repo: "nova-tools", Path: ".github/workflows/ci.yml", Body: hostedLinuxGo}}},
			true, []string{"RED 7.16 ", "nova-tools .github/workflows/ci.yml job test runs linux go", "ctl-hulk"}},
		{"the same row with no bench carrying the leg", FleetInput{Profiles: darwinGo,
			Workflows: []Workflow{{Repo: "nova-tools", Path: ".github/workflows/ci.yml", Body: hostedLinuxGo}}},
			false, nil},
		{"a matrix naming ubuntu and macos rows, both legs on benches", FleetInput{Profiles: both,
			Workflows: []Workflow{{Repo: "r", Path: ".github/workflows/cert.yml", Body: matrixGo}}},
			true, []string{"job test runs linux go", "job test runs darwin go"}},
		{"a windows-only Go row is runner-only", FleetInput{Profiles: both,
			Workflows: []Workflow{{Repo: "r", Path: ".github/workflows/win.yml", Body: windowsOnlyGo}}},
			false, nil},
		{"a self-hosted linux runner row on a bench is two schedulers", FleetInput{Profiles: linuxGo,
			Workflows: []Workflow{{Repo: "r", Path: ".github/workflows/ci.yml", Body: selfHostedLinuxGo}}},
			true, []string{"job packages runs linux go"}},
		{"a row that finds go and runs a script is a go row", FleetInput{Profiles: linuxGo,
			Workflows: []Workflow{{Repo: "r", Path: ".github/workflows/ci.yml", Body: scriptedGo}}},
			true, []string{"job shard runs linux go"}},
		{"a row named in runner_rows stays on the runner", FleetInput{Profiles: linuxGo, RunnerRows: []string{"nova-tools:test"},
			Workflows: []Workflow{{Repo: "nova-tools", Path: ".github/workflows/ci.yml", Body: hostedLinuxGo}}},
			false, nil},
		{"a Go row whose runs-on cannot be read is not green", FleetInput{Profiles: linuxGo,
			Workflows: []Workflow{{Repo: "r", Path: ".github/workflows/ci.yml", Body: unresolvedGo}}},
			true, []string{"job test runs-on unresolved"}},
		{"a review-ready PR with neither a ci card nor a runner-only record", FleetInput{
			ReviewReady: []HeadRecord{{Repo: "r", PR: 7, Head: "abc1234"}, {Repo: "r", PR: 8, Head: "def5678", CICard: true}}},
			true, []string{"r#7 at abc1234 has no ci card and no runner-only record"}},
		{"a land-ready receipt that came from a check-run", FleetInput{
			LandReady: []LandReceipt{{Repo: "r", PR: 9, Head: "0123abc", Source: "check-run:test (ubuntu-latest)"}, {Repo: "r", PR: 10, Head: "4567def", Source: "ci:r:4567def:gid1"}}},
			true, []string{"r#9 at 0123abc land-ready from check-run:test (ubuntu-latest)"}},
		{"one scheduler, every receipt from the key", FleetInput{Profiles: linuxGo,
			ReviewReady: []HeadRecord{{Repo: "r", PR: 8, Head: "def5678", RunnerOnly: true}},
			LandReady:   []LandReceipt{{Repo: "r", PR: 10, Head: "4567def", Source: "ci:r:4567def:gid1"}}},
			false, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := tc.in
			in.Loaded = allLoaded()
			line := CheckTwoSchedulers(in)
			if line.Red != tc.red {
				t.Fatalf("red=%v want %v: %s", line.Red, tc.red, line)
			}
			if line.Check != "7.16" {
				t.Fatalf("check = %q", line.Check)
			}
			for _, want := range tc.mustHave {
				if !strings.Contains(line.String(), want) {
					t.Fatalf("line %q lacks %q", line.String(), want)
				}
			}
		})
	}
}

func TestPreflightFleetChecksInOrder(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	green := FleetInput{
		Loaded:       allLoaded(),
		Benches:      []BenchState{upBench("ctl-a", 2)},
		Dialer:       &fakeSSHD{},
		Launcher:     batchLauncher{},
		TableCheck:   func(context.Context) error { return nil },
		TableFileAge: time.Second, TableFilePresent: true,
		REST:        RESTBudget{Known: true, Remaining: 4000, CallsPerPass: 1, Cadence: 10 * time.Second},
		OrphanGrace: 5 * time.Minute,
		Wake:        WakeInput{DeclPresent: true, Friends: 2},
	}
	lines := FleetChecks(ctx, green)
	var got []string
	for _, l := range lines {
		got = append(got, l.Check)
		if l.Red {
			t.Errorf("green fleet has a red line: %s", l)
		}
	}
	if strings.Join(got, " ") != "7.4 7.5 7.6 7.8 7.12 7.14 7.16 7.17 7.18" {
		t.Fatalf("checks = %v", got)
	}
	if FleetRed(lines) {
		t.Fatal("FleetRed on an all-green fleet")
	}

	red := func(t *testing.T, mutate func(*FleetInput), check, want string) {
		t.Helper()
		in := green
		in.Benches = append([]BenchState{}, green.Benches...)
		mutate(&in)
		for _, l := range FleetChecks(ctx, in) {
			if l.Check != check {
				continue
			}
			if !l.Red || !strings.Contains(l.String(), want) {
				t.Fatalf("%s: want red with %q, got %s", check, want, l)
			}
			return
		}
		t.Fatalf("no line %s", check)
	}
	t.Run("7.4 per-host launcher", func(t *testing.T) {
		red(t, func(in *FleetInput) { in.Benches[0].Launcher = "studiolaunch" }, "7.4", "ctl-a launcher studiolaunch")
	})
	t.Run("7.5 stale beat", func(t *testing.T) {
		red(t, func(in *FleetInput) { in.Benches[0].BeatAge = 3 * time.Second }, "7.5", "ctl-a beat 3.0s old")
	})
	t.Run("7.5 missing beat without pause", func(t *testing.T) {
		red(t, func(in *FleetInput) { in.Benches[0].BeatPresent = false }, "7.5", "ctl-a beat missing without a pause")
	})
	t.Run("7.8 consumer group pending too long", func(t *testing.T) {
		red(t, func(in *FleetInput) {
			in.Consumers = []ConsumerGroup{{Name: "ok-to-friend", OldestPending: 90 * time.Second}}
		}, "7.8", "ok-to-friend pending 90s")
	})
	t.Run("7.12 table --check fails", func(t *testing.T) {
		red(t, func(in *FleetInput) {
			in.TableCheck = func(context.Context) error { return errors.New("two writers: friend:a:width") }
		}, "7.12", "table --check: two writers: friend:a:width")
	})
	t.Run("7.12 table file stale", func(t *testing.T) {
		red(t, func(in *FleetInput) { in.TableFileAge = 4 * time.Second }, "7.12", "table file 4.0s old")
	})
	t.Run("7.12 table check missing", func(t *testing.T) {
		red(t, func(in *FleetInput) { in.TableCheck = nil }, "7.12", "MISSING")
	})
	t.Run("7.14 REST budget below one hour", func(t *testing.T) {
		red(t, func(in *FleetInput) { in.REST.Remaining = 100 }, "7.14", "REST remaining 100 < 360")
	})
	t.Run("7.14 REST budget unknown", func(t *testing.T) {
		red(t, func(in *FleetInput) { in.REST.Known = false }, "7.14", "MISSING")
	})
	t.Run("7.14 zero REST calls green", func(t *testing.T) {
		in := green
		in.REST.CallsPerPass = 0
		found := false
		for _, l := range FleetChecks(ctx, in) {
			if l.Check == "7.14" {
				found = true
				if l.Red {
					t.Fatalf("7.14 red: %s", l)
				}
				if !strings.Contains(l.What, "0 REST calls per pass") {
					t.Fatalf("7.14 want text containing '0 REST calls per pass', got %s", l.What)
				}
			}
		}
		if !found {
			t.Fatal("7.14 not found")
		}
	})
	t.Run("7.14 negative calls MISSING", func(t *testing.T) {
		red(t, func(in *FleetInput) { in.REST.CallsPerPass = -1 }, "7.14", "MISSING")
	})
	t.Run("7.17 orphan past grace", func(t *testing.T) {
		red(t, func(in *FleetInput) { in.Orphans = []OrphanCard{{ID: "c1", Age: 10 * time.Minute}} }, "7.17", "c1 orphan-effect 600s with no unresolved item")
	})
	t.Run("7.17 reconciled DONE", func(t *testing.T) {
		red(t, func(in *FleetInput) { in.EndedDone = []EndedCard{{ID: "c2", Reason: "reconciled"}} }, "7.17", "c2 ended(DONE) by reconciled")
	})
	t.Run("7.17 orphan inside grace or with an item stays green", func(t *testing.T) {
		in := green
		in.Orphans = []OrphanCard{{ID: "c3", Age: time.Minute}, {ID: "c4", Age: time.Hour, Unresolved: 1}}
		for _, l := range FleetChecks(ctx, in) {
			if l.Check == "7.17" && l.Red {
				t.Fatalf("7.17 red: %s", l)
			}
		}
	})
}

// TestPreflightUnreadInputRefuses is Stella's hold on 1b798eae: an empty
// collection cannot say whether it was read, so a check whose snapshot is
// unread is RED naming it, never GREEN on nothing.
func TestPreflightUnreadInputRefuses(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	t.Run("an unread fleet passes no collection check", func(t *testing.T) {
		for _, l := range []FleetLine{
			CheckBatchLauncher(FleetInput{}),
			CheckBeats(FleetInput{}),
			CheckOneSessionPerBatch(ctx, FleetInput{Dialer: &fakeSSHD{}, Launcher: batchLauncher{}}),
			CheckConsumers(FleetInput{}),
			CheckTwoSchedulers(FleetInput{}),
			CheckOrphanEffects(FleetInput{}),
			CheckFriendWake(FleetInput{}),
		} {
			if !l.Red || !strings.Contains(l.String(), "unread (MISSING)") {
				t.Errorf("an unread input must be red and say unread: %s", l)
			}
		}
		if !FleetRed(FleetChecks(ctx, FleetInput{})) {
			t.Error("an unread fleet passed preflight")
		}
	})

	// Each collection alone unread, everything else read and clean: the one
	// check that needs it goes red and names it.
	green := FleetInput{
		Loaded:           allLoaded(),
		Benches:          []BenchState{upBench("ctl-a", 2)},
		Dialer:           &fakeSSHD{},
		Launcher:         batchLauncher{},
		TableCheck:       func(context.Context) error { return nil },
		TableFilePresent: true,
		REST:             RESTBudget{Known: true, Remaining: 4000, CallsPerPass: 1, Cadence: 10 * time.Second},
		OrphanGrace:      5 * time.Minute,
		Wake:             WakeInput{DeclPresent: true, Friends: 2},
	}
	cases := []struct {
		unset  func(*Loaded)
		checks []string
		name   string
	}{
		{func(l *Loaded) { l.Benches = false }, []string{"7.4", "7.5", "7.6"}, "benches"},
		{func(l *Loaded) { l.Consumers = false }, []string{"7.8"}, "consumer groups"},
		{func(l *Loaded) { l.Profiles = false }, []string{"7.16"}, "bench profiles"},
		{func(l *Loaded) { l.Workflows = false }, []string{"7.16"}, "workflows"},
		{func(l *Loaded) { l.ReviewReady = false }, []string{"7.16"}, "review-ready heads"},
		{func(l *Loaded) { l.LandReady = false }, []string{"7.16"}, "land-ready receipts"},
		{func(l *Loaded) { l.Orphans = false }, []string{"7.17"}, "orphan-effect cards"},
		{func(l *Loaded) { l.EndedDone = false }, []string{"7.17"}, "ended(DONE) cards"},
		{func(l *Loaded) { l.Wake = false }, []string{"7.18"}, "friend wake paths"},
	}
	for _, tc := range cases {
		t.Run(tc.name+" unread", func(t *testing.T) {
			in := green
			tc.unset(&in.Loaded)
			want := map[string]bool{}
			for _, c := range tc.checks {
				want[c] = true
			}
			for _, l := range FleetChecks(ctx, in) {
				switch {
				case want[l.Check] && (!l.Red || !strings.Contains(l.String(), tc.name+" unread (MISSING)")):
					t.Errorf("%s must be red naming %q unread: %s", l.Check, tc.name, l)
				case !want[l.Check] && l.Red:
					t.Errorf("%s does not read %s and went red: %s", l.Check, tc.name, l)
				}
			}
		})
	}

	t.Run("orphan_grace unset is MISSING even with no orphans", func(t *testing.T) {
		in := green
		in.OrphanGrace = 0
		if l := CheckOrphanEffects(in); !l.Red || !strings.Contains(l.String(), "orphan_grace MISSING") {
			t.Fatalf("unset grace must be red: %s", l)
		}
	})
}

// TestLiveBeatShapeFixtureNamesLauncher (#3191): the fixture of the live beat hash
// as the bench beat writer emits it (with launcher=nova-sprint card launch)
// goes through CheckBatchLauncher GREEN, and the same fixture with the
// launcher field dropped is RED naming the bench.
func TestLiveBeatShapeFixtureNamesLauncher(t *testing.T) {
	t.Parallel()

	// The seven live benches from the issue.
	benches := []string{"studio", "hulk", "vision", "superman", "batman", "spacegame", "hetzner"}

	// Fixture exactly as the beat writer emits it: every bench beat carries
	// launcher=nova-sprint card launch.
	withLauncher := make([]BenchState, len(benches))
	for i, b := range benches {
		withLauncher[i] = BenchState{
			Name:        b,
			BeatPresent: true,
			BeatAge:     500 * time.Millisecond,
			Launcher:    BatchLauncherName,
		}
	}
	in := FleetInput{Loaded: Loaded{Benches: true}, Benches: withLauncher}
	line := CheckBatchLauncher(in)
	if line.Red {
		t.Fatalf("live beat shape with launcher must be GREEN: %s", line)
	}
	if !strings.Contains(line.What, "7 beats name "+BatchLauncherName) {
		t.Fatalf("green line must count the beats: %s", line)
	}

	// Same fixture with the launcher field dropped: each bench is RED.
	withoutLauncher := make([]BenchState, len(benches))
	for i, b := range benches {
		withoutLauncher[i] = BenchState{
			Name:        b,
			BeatPresent: true,
			BeatAge:     500 * time.Millisecond,
		}
	}
	inRed := FleetInput{Loaded: Loaded{Benches: true}, Benches: withoutLauncher}
	lineRed := CheckBatchLauncher(inRed)
	if !lineRed.Red {
		t.Fatalf("live beat shape without launcher must be RED: %s", lineRed)
	}
	for _, b := range benches {
		if !strings.Contains(lineRed.What, b+" launcher MISSING") {
			t.Fatalf("red line must name bench %q: %s", b, lineRed)
		}
	}
}
