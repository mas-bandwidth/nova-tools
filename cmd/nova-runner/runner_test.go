package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/onboarding"
	"github.com/stretchr/testify/require"
)

func TestParseRowReadsTheFieldsTheLoopUses(t *testing.T) {
	t.Parallel()
	out := "FRIEND name=ada slots=64 tiers=frontier,pro roles=builder width=16 mode=one-shot config_dir=- token_cap=6000000 runner_version=1.2.3 created=2023-11-14T22:13:20Z\n"
	row, err := parseRow("ada", out)
	require.NoError(t, err)
	require.Equal(t, Width(16), row.Width)
	require.Equal(t, []string{"frontier", "pro"}, row.Tiers)
	require.Equal(t, "one-shot", row.Mode)
	require.Equal(t, "1.2.3", row.RunnerVersion)

	unset, err := parseRow("ada", "FRIEND name=ada width=8 mode=batch tiers=- runner_version=-\n")
	require.NoError(t, err)
	require.Empty(t, unset.RunnerVersion)
	require.Empty(t, unset.Tiers)
	require.Equal(t, "batch", unset.Mode)

	_, err = parseRow("ada", "FRIEND name=ada mode=one-shot\n")
	require.ErrorContains(t, err, "no width")
	_, err = parseRow("ada", "FRIEND name=bob width=8\n")
	require.ErrorContains(t, err, "showed bob")
}

func TestParseQueueKeepsTheSprintsOrderAndSkipsWhatIsNotReady(t *testing.T) {
	t.Parallel()
	raw := `{
	  "cards": [
	    {"id":"d1","col":"ready","gen":1,"packet":{"kind":"read","epoch":15,"tier":"flash","primary":"p1","attempt":1,"head":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","work_branch":"sprint/p1.w1","brief":"STATUS: nova-sprint card p1, attempt 1;\nREPO: mas-bandwidth/nova-tools\nBASE: sprint/mechanical-2026-10-02@aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\n"}},
	    {"id":"gone","col":"done","packet":{"kind":"work","repo":"a/b","base":"main","branch":"sprint/gone"}},
	    {"id":"w1","col":"working","gen":2,"attempt":3,"packet":{"kind":"work","model":"inception/mercury-2.5","deadline":30,"repo":"a/b","base":"main","branch":"sprint/w1"}}
	  ]
	}`
	cards, err := parseQueue(raw)
	require.NoError(t, err)
	require.Len(t, cards, 1, "only the ready column is launchable; working and done are not")
	require.Equal(t, "d1", cards[0].ID)
	require.Equal(t, KindRead, cards[0].Kind)
	require.Equal(t, "d1~15", cards[0].Job)
	require.Equal(t, "p1", cards[0].Primary)
	require.Equal(t, "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", cards[0].Head)
	require.Equal(t, "sprint/p1.w1", cards[0].WorkBranch)
	require.Equal(t, "mas-bandwidth/nova-tools", cards[0].Repo)
	require.Equal(t, "sprint/mechanical-2026-10-02", cards[0].Base)
	require.Equal(t, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", cards[0].BaseSha)
}

func TestCommandLinesMatchTheLoop(t *testing.T) {
	t.Parallel()
	require.Equal(t, []string{"friend", "show", "ada"}, showArgv("ada"))
	require.Equal(t, []string{"queue", "--as", "friend.ada", "--json"}, queueArgv("ada"))
	require.Equal(t, []string{"friend", "beat", "ada", "--working", "12", "--queue", "3", "--width", "16", "--running", "a,b"},
		beatArgv("ada", Beat{Working: 12, Queue: 3, Width: 16, Running: []string{"a", "b"}}))
	card := Card{ID: "s1-1.w1", Gen: 2, Epoch: 15, Branch: "sprint/s1-1.w1.g2.e15"}
	require.Equal(t,
		[]string{"finish", "--as", "friend.ada", "s1-1.w1@2", "--epoch", "15", "--failed", "--report", "harness fault: boom", "--branch", "sprint/s1-1.w1.g2.e15"},
		finishArgv("ada", card, true, "", "harness fault: boom"))
	require.Equal(t, []string{"take", "--as", "friend.ada", "s1-1.w1@2", "--epoch", "15"}, claimArgv("ada", card))
	require.Equal(t, []string{"read", "--as", "friend.ada", "--broken", "s1-1.w1@2", "--epoch", "15", "--finding", "main.go:3 off by one"}, readArgv("ada", card, "broken", "main.go:3 off by one"))
	require.Equal(t, []string{"read", "--as", "friend.ada", "--ok", "s1-1.w1@2", "--epoch", "15"}, readArgv("ada", card, "ok", ""))
	require.Equal(t, []string{"read", "--as", "friend.ada", "--return", "s1-1.w1@2", "--epoch", "15", "--reason", "harness fault: no report"}, readArgv("ada", card, "return", "harness fault: no report"))
	require.Equal(t, []string{"install", "nova-runner@1.2.3"}, installArgv("1.2.3"))
	bin, args := harnessArgv("opencode", "inception/mercury-2.5", "do the card")
	require.Equal(t, "opencode", bin)
	require.Equal(t, []string{"run", "--model", "inception/mercury-2.5", "do the card"}, args)
	bin, args = harnessArgv("claude", "claude-opus-5-5", "do the card")
	require.Equal(t, "claude", bin)
	require.Equal(t, []string{"-p", "do the card", "--model", "claude-opus-5-5"}, args)
	failed, head := landOf("LAND", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	require.False(t, failed)
	require.Equal(t, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", head)
	failed, head = landOf("HOLD", "none")
	require.True(t, failed)
	require.Empty(t, head)
}

func TestHelpExplainsTheLoop(t *testing.T) {
	t.Parallel()
	var out, errb bytes.Buffer
	require.Equal(t, 0, run([]string{"--help"}, &out, &errb))
	require.Contains(t, out.String(), "every second")
	require.Contains(t, out.String(), "--as <friend>")
	out.Reset()
	errb.Reset()
	require.Equal(t, 0, run([]string{"run", "-h"}, &out, &errb))
	text := out.String()
	for _, want := range []string{
		"nova-config friend show",
		"a work lane is 1",
		"a read lane is one half",
		"nova-sprint queue --as friend.<friend>",
		"JOB.md",
		"BRIEF.md",
		"friend beat",
		"harness fault:",
		"Three alike",
		"nova-update install nova-runner@",
		"session leader",
		"never kills a lane",
		"never keeps a width of its own",
		"--as <friend>",
		"--dir",
		"--harness",
		"--seat",
	} {
		require.Contains(t, text, want)
	}
	out.Reset()
	errb.Reset()
	require.Equal(t, 0, run([]string{"help"}, &out, &errb))
	out.Reset()
	errb.Reset()
	require.Equal(t, 0, run([]string{"run", "-h"}, &out, &errb))
	require.Contains(t, out.String(), "every second")

	out.Reset()
	errb.Reset()
	require.Equal(t, 2, run([]string{"run"}, &out, &errb))
	msg := errb.String()
	for _, flag := range []string{"--as", "--dir", "--harness", "--seat"} {
		require.Contains(t, msg, flag)
	}
	require.Equal(t, 2, run([]string{"nope"}, &out, &errb))
}

func TestRunnerToolMeetsStandard(t *testing.T) {
	t.Parallel()
	require.Empty(t, runnerTool().Problems())
}

func TestRunnerBannerExampleNamesItsSetup(t *testing.T) {
	t.Parallel()
	const example = "nova-runner run --as ada --dir ~/ada-working --harness opencode --seat coordinator"
	var out, errb bytes.Buffer
	require.Zero(t, run([]string{"help"}, &out, &errb))
	lines, err := onboarding.ExampleLines(out.String(), "nova-runner")
	require.NoError(t, err)
	require.Contains(t, lines, example)
	out.Reset()
	errb.Reset()
	code := run(strings.Fields(strings.TrimPrefix(example, "nova-runner ")), &out, &errb)
	require.Equal(t, 2, code)
	require.Empty(t, out.String())
	step := onboarding.Step{Line: "example: " + example, Want: []string{
		"RUN REFUSED: --dir ~/ada-working is not a directory; give an existing working directory; run: nova-runner help",
	}}
	require.Empty(t, onboarding.Compare(step, onboarding.Result{Code: code, Stderr: errb.String()}, nil))
}

type fakeProc struct {
	exited bool
	line   string
	pid    int
}

func (f *fakeProc) Exited() (bool, string) { return f.exited, f.line }
func (f *fakeProc) PID() int               { return f.pid }
func (f *fakeProc) LogPath() string        { return "" }

func TestTickFillsFailsAndFollowsWithoutAClock(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 7, 21, 0, 0, 0, time.UTC)
	ctx := context.Background()

	t.Run("fill", func(t *testing.T) {
		t.Parallel()
		var started []string
		var beats []Beat
		r := &Runner{
			Friend: "ada", Version: "1",
			Edges: Edges{
				Show: func(context.Context, string) (Row, error) {
					return Row{Width: 16, RunnerVersion: "1"}, nil
				},
				Queue: func(context.Context, string) ([]Card, error) {
					return queueIDs("q", 3, KindWork), nil
				},
				Start: func(_ context.Context, c Card) (Proc, error) {
					started = append(started, c.ID)
					return &fakeProc{pid: 1}, nil
				},
				Beat: func(_ context.Context, _ string, b Beat) error {
					beats = append(beats, b)
					return nil
				},
			},
		}
		require.NoError(t, r.Tick(ctx, now))
		require.Equal(t, []string{"q0", "q1", "q2"}, started)
		require.Equal(t, 3, beats[0].Working)
		require.Equal(t, 16, beats[0].Width)
		require.Equal(t, []string{"q0", "q1", "q2"}, beats[0].Running)
	})

	t.Run("no report", func(t *testing.T) {
		t.Parallel()
		var finishes []string
		var judgments int
		procs := map[string]*fakeProc{}
		r := &Runner{Friend: "ada", Version: "1", loaded: true, row: Row{Width: 16, RunnerVersion: "1"}, rowAt: now}
		for _, id := range []string{"a", "b", "c"} {
			procs[id] = &fakeProc{exited: true, line: "opencode: boom", pid: 4}
			r.lanes = r.lanes.Add(Lane{ID: id, Kind: KindWork})
			if r.cards == nil {
				r.cards = map[string]Card{}
			}
			r.cards[id] = Card{ID: id, Kind: KindWork}
			if r.procs == nil {
				r.procs = map[string]procSlot{}
			}
			r.procs[id] = procSlot{proc: procs[id]}
		}
		r.Edges = Edges{
			Queue:  func(context.Context, string) ([]Card, error) { return nil, nil },
			Report: func(Card) (string, bool) { return "", false },
			Finish: func(_ context.Context, c Card, failed bool, head, report string) error {
				require.True(t, failed)
				require.Empty(t, head)
				finishes = append(finishes, c.ID+" "+report)
				return nil
			},
			Judge: func(context.Context, string, string) error {
				judgments++
				return nil
			},
			Beat: func(context.Context, string, Beat) error { return nil },
		}
		require.NoError(t, r.Tick(ctx, now))
		require.Len(t, finishes, 3)
		for _, line := range finishes {
			require.True(t, strings.Contains(line, "harness fault: opencode: boom"), line)
		}
		require.Equal(t, 1, judgments)
		require.Zero(t, r.lanes.Len())
	})

	t.Run("drain then exec", func(t *testing.T) {
		t.Parallel()
		proc := &fakeProc{pid: 7}
		var started, installed, execed int
		r := &Runner{
			Friend: "ada", Version: "1", loaded: true,
			row: Row{Width: 16, RunnerVersion: "2"}, rowAt: now,
			lanes: Lanes{}.Add(Lane{ID: "w0", Kind: KindWork}),
			procs: map[string]procSlot{"w0": {proc: proc}},
			cards: map[string]Card{"w0": {ID: "w0", Kind: KindWork, Gen: 1}},
			Edges: Edges{
				Queue: func(context.Context, string) ([]Card, error) {
					return queueIDs("q", 2, KindWork), nil
				},
				Start: func(context.Context, Card) (Proc, error) {
					started++
					return &fakeProc{pid: 1}, nil
				},
				Report: func(Card) (string, bool) {
					return "Verdict: LAND\nHead: aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\n", true
				},
				Finish: func(context.Context, Card, bool, string, string) error { return nil },
				Beat:   func(context.Context, string, Beat) error { return nil },
				Install: func(_ context.Context, v string) error {
					installed++
					require.Equal(t, "2", v)
					return nil
				},
				Exec: func(v string) error {
					execed++
					require.Equal(t, "2", v)
					return nil
				},
			},
		}
		require.NoError(t, r.Tick(ctx, now))
		require.Zero(t, started, "a version change starts nothing while a lane is up")
		require.Zero(t, installed)
		require.Equal(t, []string{"w0"}, r.lanes.IDs())

		proc.exited = true
		require.NoError(t, r.Tick(ctx, now.Add(time.Second)))
		require.Zero(t, started)
		require.Equal(t, 1, installed)
		require.Equal(t, 1, execed)
		require.Zero(t, r.lanes.Len())
	})
}

func TestTickClaimsBeforeLaunchAndSkipsWhatWasNotClaimed(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 7, 21, 0, 0, 0, time.UTC)
	ctx := context.Background()
	var claims, starts []string
	r := &Runner{
		Friend: "ada", Version: "1",
		Edges: Edges{
			Show: func(context.Context, string) (Row, error) {
				return Row{Width: 16, RunnerVersion: "1"}, nil
			},
			Queue: func(context.Context, string) ([]Card, error) {
				return queueIDs("q", 3, KindWork), nil
			},
			Claim: func(_ context.Context, c Card) error {
				claims = append(claims, c.ID)
				if c.ID == "q1" {
					return errors.New("stale generation")
				}
				return nil
			},
			Start: func(_ context.Context, c Card) (Proc, error) {
				starts = append(starts, c.ID)
				return &fakeProc{pid: 1}, nil
			},
			Beat: func(context.Context, string, Beat) error { return nil },
		},
	}
	require.NoError(t, r.Tick(ctx, now))
	require.Equal(t, []string{"q0", "q1", "q2"}, claims)
	require.Equal(t, []string{"q0", "q2"}, starts, "a card whose take was refused is not launched")
	require.Equal(t, []string{"q0", "q2"}, r.lanes.IDs())
}

func TestTickClosesAClaimWhoseStartFails(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 7, 21, 0, 0, 0, time.UTC)
	for _, kind := range []string{KindWork, KindRead} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			card := Card{ID: "card", Kind: kind, Gen: 2, Epoch: 15}
			var claim, close int
			r := &Runner{Friend: "ada", Version: "1", Edges: Edges{
				Show:  func(context.Context, string) (Row, error) { return Row{Width: 1, RunnerVersion: "1"}, nil },
				Queue: func(context.Context, string) ([]Card, error) { return []Card{card}, nil },
				Claim: func(context.Context, Card) error { claim++; return nil },
				Start: func(context.Context, Card) (Proc, error) { return nil, errors.New("staging failed") },
				Finish: func(_ context.Context, c Card, failed bool, head, report string) error {
					require.Equal(t, KindWork, kind)
					require.Equal(t, card, c)
					require.True(t, failed)
					require.Equal(t, "harness fault: staging failed", report)
					close++
					return nil
				},
				Read: func(_ context.Context, c Card, verdict, reason string) error {
					require.Equal(t, KindRead, kind)
					require.Equal(t, card, c)
					require.Equal(t, "return", verdict)
					require.Equal(t, "harness fault: staging failed", reason)
					close++
					return nil
				},
			}}
			require.NoError(t, r.Tick(context.Background(), now))
			require.Equal(t, 1, claim)
			require.Equal(t, 1, close, "a successfully claimed card must be closed after Start fails")
			require.Zero(t, r.lanes.Len())
			require.Empty(t, r.pending)
			require.Empty(t, r.pendingReads)
		})
	}
}

func TestTickReturnsAReadWithNoReport(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 7, 21, 0, 0, 0, time.UTC)
	card := Card{ID: "read", Kind: KindRead, Gen: 1, Epoch: 15}
	var returned int
	r := &Runner{Friend: "ada", Version: "1", loaded: true,
		row: Row{Width: 1, RunnerVersion: "1"}, rowAt: now,
		lanes: Lanes{}.Add(Lane{ID: card.ID, Kind: KindRead}),
		cards: map[string]Card{card.ID: card},
		procs: map[string]procSlot{card.ID: {proc: &fakeProc{exited: true, line: "no report", pid: 4}}},
		Edges: Edges{
			Queue:  func(context.Context, string) ([]Card, error) { return nil, nil },
			Report: func(Card) (string, bool) { return "", false },
			Finish: func(context.Context, Card, bool, string, string) error {
				t.Fatal("a read cannot use finish")
				return nil
			},
			Read: func(_ context.Context, c Card, verdict, reason string) error {
				require.Equal(t, card, c)
				require.Equal(t, "return", verdict)
				require.Equal(t, "harness fault: no report", reason)
				returned++
				return nil
			},
		}}
	require.NoError(t, r.Tick(context.Background(), now))
	require.Equal(t, 1, returned)
	require.Zero(t, r.lanes.Len())
}

func TestTickClosesAReadViaTheReadVerb(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 7, 21, 0, 0, 0, time.UTC)
	ctx := context.Background()

	newReader := func(report string) (*Runner, *[]string) {
		var closes []string
		r := &Runner{
			Friend: "ada", Version: "1", loaded: true,
			row: Row{Width: 16, RunnerVersion: "1"}, rowAt: now,
			lanes: Lanes{}.Add(Lane{ID: "d1", Kind: KindRead}),
			procs: map[string]procSlot{"d1": {proc: &fakeProc{exited: true, pid: 4}}},
			cards: map[string]Card{"d1": {ID: "d1", Kind: KindRead, Gen: 1, Epoch: 15}},
			Edges: Edges{
				Queue:  func(context.Context, string) ([]Card, error) { return nil, nil },
				Report: func(Card) (string, bool) { return report, true },
				Read: func(_ context.Context, c Card, verdict, finding string) error {
					closes = append(closes, c.ID+" "+verdict+" "+finding)
					return nil
				},
				Beat: func(context.Context, string, Beat) error { return nil },
			},
		}
		return r, &closes
	}

	r, closes := newReader("Verdict: LAND\n")
	require.NoError(t, r.Tick(ctx, now))
	require.Equal(t, []string{"d1 ok "}, *closes)
	require.Zero(t, r.lanes.Len())

	r, closes = newReader("Verdict: HOLD\nmain.go:3 off by one\n")
	require.NoError(t, r.Tick(ctx, now))
	require.Equal(t, []string{"d1 broken main.go:3 off by one"}, *closes)
	require.Zero(t, r.lanes.Len())

	r, closes = newReader("Verdict: HOLD\nnothing wrong here\n")
	require.NoError(t, r.Tick(ctx, now))
	require.Equal(t, []string{"d1 return harness fault: read report has no verdict"}, *closes)
	require.Zero(t, r.lanes.Len())
}

func TestParseReadReportMapsLandAndHold(t *testing.T) {
	t.Parallel()
	v, f, ok := parseReadReport("Verdict: LAND\n")
	require.True(t, ok)
	require.Equal(t, "ok", v)
	require.Empty(t, f)

	v, f, ok = parseReadReport("Verdict: HOLD\nmain.go:3 off by one\n")
	require.True(t, ok)
	require.Equal(t, "broken", v)
	require.Equal(t, "main.go:3 off by one", f)

	_, _, ok = parseReadReport("Verdict: HOLD\nnothing wrong\n")
	require.False(t, ok, "a HOLD that names no defect is no verdict")
}
