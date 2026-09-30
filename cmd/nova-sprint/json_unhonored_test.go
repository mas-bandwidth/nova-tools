package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

func TestWaitJSONSuccessAndFailure(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a --members m1")
	ta.ok("add --stream s1 --count 1")
	ta.deal(1)
	ta.ok("take --as m1 --limit 1")
	ta.ok("finish --as m1 s1-1.w1@1 --failed")

	// Test wait with nonexistent note (exit 1 failure under --json)
	code, out, _ := ta.do("wait nonexistent-note --for 10m --json")
	if code != 1 {
		t.Fatalf("expected exit 1 for nonexistent wait, got %d: %s", code, out)
	}
	var failResp struct {
		Result struct {
			Verb   string `json:"verb"`
			Status string `json:"status"`
			Exit   int    `json:"exit"`
		} `json:"result"`
		Facts struct {
			State string `json:"state"`
			Done  string `json:"done"`
			Error string `json:"error"`
			Note  string `json:"note"`
		} `json:"facts"`
	}
	if err := json.Unmarshal([]byte(out), &failResp); err != nil {
		t.Fatalf("unmarshal wait failure json: %v\n%s", err, out)
	}
	if failResp.Result.Verb != "wait" || failResp.Result.Status != "failed" || failResp.Result.Exit != 1 {
		t.Fatalf("unexpected failure result envelope: %+v", failResp.Result)
	}
	if failResp.Facts.State == "" || failResp.Facts.Done == "" || failResp.Facts.Error == "" {
		t.Fatalf("unexpected failure facts: %+v", failResp.Facts)
	}

	// Get the open judgment note from inbox
	var in struct {
		Groups []sprint.Group `json:"groups"`
	}
	ta.json("inbox", &in)
	var noteID string
	for _, g := range in.Groups {
		if g.Kind == sprint.Judgment && len(g.Notes) > 0 {
			noteID = g.Notes[0]
			break
		}
	}
	if noteID == "" {
		t.Fatalf("expected open judgment in inbox, got: %+v", in.Groups)
	}

	code, out, errs := ta.do("wait " + noteID + " --for 10m --json")
	if code != 0 {
		t.Fatalf("expected exit 0 for wait, got %d: %s %s", code, out, errs)
	}
	var okResp struct {
		Result struct {
			Verb   string `json:"verb"`
			Status string `json:"status"`
			Exit   int    `json:"exit"`
		} `json:"result"`
		Facts struct {
			State string `json:"state"`
			Done  string `json:"done"`
			Note  string `json:"note"`
			Held  bool   `json:"held"`
			Until string `json:"until"`
		} `json:"facts"`
	}
	if err := json.Unmarshal([]byte(out), &okResp); err != nil {
		t.Fatalf("unmarshal wait success json: %v\n%s", err, out)
	}
	if okResp.Result.Verb != "wait" || okResp.Result.Status != "ok" || okResp.Result.Exit != 0 {
		t.Fatalf("unexpected ok result envelope: %+v", okResp.Result)
	}
	if okResp.Facts.State == "" || okResp.Facts.Done == "" || okResp.Facts.Note != noteID {
		t.Fatalf("unexpected ok facts: %+v", okResp.Facts)
	}
}

func TestReaderAddJSON(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a --members m1")

	code, out, errs := ta.do("reader add reader-b --json")
	if code != 0 {
		t.Fatalf("reader add: exit %d\n%s%s", code, out, errs)
	}
	var resp struct {
		Result struct {
			Verb   string `json:"verb"`
			Status string `json:"status"`
			Exit   int    `json:"exit"`
		} `json:"result"`
		Facts struct {
			Reader  string   `json:"reader"`
			Readers []string `json:"readers"`
		} `json:"facts"`
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("unmarshal reader add json: %v\n%s", err, out)
	}
	if resp.Result.Verb != "reader add" || resp.Result.Status != "ok" || resp.Result.Exit != 0 {
		t.Fatalf("unexpected result: %+v", resp.Result)
	}
	if resp.Facts.Reader != "reader-b" || len(resp.Facts.Readers) != 1 || resp.Facts.Readers[0] != "reader-b" {
		t.Fatalf("unexpected facts: %+v", resp.Facts)
	}
}

func TestClearJSON(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a --members m1")
	ta.ok("add --stream s1 --count 2")

	code, out, errs := ta.do("clear --confirm sprint --json")
	if code != 0 {
		t.Fatalf("clear: exit %d\n%s%s", code, out, errs)
	}
	var resp struct {
		Result struct {
			Verb   string `json:"verb"`
			Status string `json:"status"`
			Exit   int    `json:"exit"`
		} `json:"result"`
		Facts struct {
			Sprint    string `json:"sprint"`
			EpochFrom uint64 `json:"epoch_from"`
			EpochTo   uint64 `json:"epoch_to"`
		} `json:"facts"`
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("unmarshal clear json: %v\n%s", err, out)
	}
	if resp.Result.Verb != "clear" || resp.Result.Status != "ok" || resp.Result.Exit != 0 {
		t.Fatalf("unexpected result: %+v", resp.Result)
	}
	if resp.Facts.Sprint != "sprint" {
		t.Fatalf("unexpected sprint: %q", resp.Facts.Sprint)
	}
	if resp.Facts.EpochFrom != 0 || resp.Facts.EpochTo != 1 {
		t.Fatalf("unexpected epochs: from %d to %d", resp.Facts.EpochFrom, resp.Facts.EpochTo)
	}
}

func TestTeardownJSON(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a --members m1")

	code, out, errs := ta.do("teardown --confirm sprint --json")
	if code != 0 {
		t.Fatalf("teardown: exit %d\n%s%s", code, out, errs)
	}
	var resp struct {
		Result struct {
			Verb   string `json:"verb"`
			Status string `json:"status"`
			Exit   int    `json:"exit"`
		} `json:"result"`
		Facts struct {
			Sprint string `json:"sprint"`
			Keys   int    `json:"keys"`
		} `json:"facts"`
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("unmarshal teardown json: %v\n%s", err, out)
	}
	if resp.Result.Verb != "teardown" || resp.Result.Status != "ok" || resp.Result.Exit != 0 {
		t.Fatalf("unexpected result: %+v", resp.Result)
	}
	if resp.Facts.Sprint != "sprint" {
		t.Fatalf("unexpected sprint: %q", resp.Facts.Sprint)
	}
	if resp.Facts.Keys <= 0 {
		t.Fatalf("expected keys > 0, got %d", resp.Facts.Keys)
	}
}

func TestPlayJSON(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a --members m1")
	ta.ok("add --stream s1 --count 2")
	ta.ok("start")

	code, out, errs := ta.do("play --ticks 1 --seed 42 --json")
	if code != 0 {
		t.Fatalf("play: exit %d\n%s%s", code, out, errs)
	}
	var resp struct {
		Result struct {
			Verb   string `json:"verb"`
			Status string `json:"status"`
			Exit   int    `json:"exit"`
		} `json:"result"`
		Facts struct {
			Seed    uint64 `json:"seed"`
			Stopped string `json:"stopped"`
		} `json:"facts"`
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("unmarshal play json: %v\n%s", err, out)
	}
	if resp.Result.Verb != "play" || resp.Result.Status != "ok" || resp.Result.Exit != 0 {
		t.Fatalf("unexpected result: %+v", resp.Result)
	}
	if resp.Facts.Seed != 42 {
		t.Fatalf("unexpected seed: %d", resp.Facts.Seed)
	}
	if resp.Facts.Stopped != "ticks" {
		t.Fatalf("unexpected stopped: %q", resp.Facts.Stopped)
	}
}

func TestRunLoopJSON(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a --members m1")
	ta.ok("add --stream s1 --count 3")
	ta.ok("start")

	st, _, code := ta.a.machineVerb("run", nil, &bytes.Buffer{})
	if st == nil {
		t.Fatalf("run setup: %d", code)
	}

	var out, errb bytes.Buffer
	ta.a.runLoop(context.Background(), st, 20, 2, &out, &errb, true)
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 json lines, got %d: %q", len(lines), out.String())
	}
	for i, l := range lines {
		var rep runReport
		if err := json.Unmarshal([]byte(l), &rep); err != nil {
			t.Fatalf("line %d: invalid json: %v\n%s", i, err, l)
		}
		if rep.Result.Verb != "run" || rep.Result.Status != "ok" || rep.Result.Exit != 0 {
			t.Fatalf("line %d: unexpected result: %+v", i, rep.Result)
		}
		if rep.Facts["state"] != "RUNNING" {
			t.Fatalf("line %d: unexpected state: %v", i, rep.Facts["state"])
		}
		if rep.Tick == nil {
			t.Fatalf("line %d: expected tick in rep", i)
		}
	}
}

func TestRunLoopJSONZeroTicksExitReport(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a --members m1")

	st, _, code := ta.a.machineVerb("run", nil, &bytes.Buffer{})
	if st == nil {
		t.Fatalf("run setup: %d", code)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // canceled before running

	var out, errb bytes.Buffer
	ta.a.runLoop(ctx, st, 20, 0, &out, &errb, true)
	var resp struct {
		Result struct {
			Verb   string `json:"verb"`
			Status string `json:"status"`
			Exit   int    `json:"exit"`
		} `json:"result"`
		Facts struct {
			State string `json:"state"`
			Ticks int    `json:"ticks"`
		} `json:"facts"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out.String())), &resp); err != nil {
		t.Fatalf("unmarshal zero ticks run json: %v\n%s", err, out.String())
	}
	if resp.Result.Verb != "run" || resp.Result.Status != "ok" || resp.Result.Exit != 0 {
		t.Fatalf("unexpected result: %+v", resp.Result)
	}
	if resp.Facts.Ticks != 0 {
		t.Fatalf("expected 0 ticks, got %d", resp.Facts.Ticks)
	}
}
