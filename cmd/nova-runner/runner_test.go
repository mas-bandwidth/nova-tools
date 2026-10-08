package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestParseRowReadsTheFieldsTheLoopUses(t *testing.T) {
	t.Parallel()
	out := "FRIEND name=ada slots=64 tiers=frontier,pro roles=builder width=16 mode=one-shot config_dir=- token_cap=6000000 runner_version=1.2.3 created=2023-11-14T22:13:20Z\n"
	row, err := ParseRow("ada", out)
	require.NoError(t, err)
	require.Equal(t, Width(16), row.Width)
	require.Equal(t, []string{"frontier", "pro"}, row.Tiers)
	require.Equal(t, "one-shot", row.Mode)
	require.Equal(t, "1.2.3", row.RunnerVersion)

	unset, err := ParseRow("ada", "FRIEND name=ada width=8 mode=batch tiers=- runner_version=-\n")
	require.NoError(t, err)
	require.Empty(t, unset.RunnerVersion)
	require.Empty(t, unset.Tiers)
	require.Equal(t, "batch", unset.Mode)

	_, err = ParseRow("ada", "FRIEND name=ada mode=one-shot\n")
	require.ErrorContains(t, err, "no width")
	_, err = ParseRow("ada", "FRIEND name=bob width=8\n")
	require.ErrorContains(t, err, "showed bob")
}

func TestParseQueueKeepsTheSprintsOrderAndSkipsWhatIsNotReady(t *testing.T) {
	t.Parallel()
	raw := `{
	  "cards": [
	    {"id":"d1","col":"ready","gen":1,"packet":{"kind":"read","epoch":15,"tier":"flash","brief":"STATUS: nova-sprint card d1, attempt 1; push your work to the branch sprint/d1;\nREPO: mas-bandwidth/nova-tools\nBASE: sprint/mechanical-2026-10-02@aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\n"}},
	    {"id":"gone","col":"done","packet":{"kind":"work","repo":"a/b","base":"main","branch":"sprint/gone"}},
	    {"id":"w1","col":"working","gen":2,"attempt":3,"packet":{"kind":"work","model":"inception/mercury-2.5","deadline":30,"repo":"a/b","base":"main","branch":"sprint/w1"}}
	  ]
	}`
	cards, err := ParseQueue(raw)
	require.NoError(t, err)
	require.Len(t, cards, 2)
	require.Equal(t, "d1", cards[0].ID)
	require.Equal(t, KindRead, cards[0].Kind)
	require.Equal(t, "d1~15", cards[0].Job)
	require.Equal(t, "sprint/d1", cards[0].Branch)
	require.Equal(t, "mas-bandwidth/nova-tools", cards[0].Repo)
	require.Equal(t, "sprint/mechanical-2026-10-02", cards[0].Base)
	require.Equal(t, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", cards[0].BaseSha)
	require.Equal(t, "w1", cards[1].ID)
	require.Equal(t, KindWork, cards[1].Kind)
	require.Equal(t, 2, cards[1].Gen)
	require.Equal(t, "inception/mercury-2.5", cards[1].Model)
	require.Equal(t, 30, cards[1].Deadline)
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
