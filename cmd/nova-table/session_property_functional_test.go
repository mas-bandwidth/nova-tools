//go:build functional

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/redis/go-redis/v9"
)

// This execution trace observes a bounded subset of TableSession: answered and
// lost verbs, usage/length refusals, keep-going, quit and EOF. Durable receipts
// witness effects separately: the session model does not encode the ledger.
// Signals and store-down events retain their existing functional controls.
type sessionTraceAction struct {
	Kind    string
	Command string
	Write   bool
	Lost    bool
}

type sessionTraceStep struct {
	Action   sessionTraceAction
	Code     int
	Stdout   string
	Stderr   string
	Receipts []redis.XMessage
}

type sessionExecutionTrace struct {
	Seed    int64
	Keep    bool
	Actions []sessionTraceAction
	Steps   []sessionTraceStep
	Exit    int
	EOF     bool
}

// Supply one complete command at a time. bufio cannot prefetch the next line,
// so the next Read is an event boundary after the previous command returned.
// Chunking an overlong line never creates an extra event boundary.
type sessionTraceInput struct {
	t       *testing.T
	trace   *sessionExecutionTrace
	admin   *redis.Client
	table   string
	out     *bytes.Buffer
	errs    *bytes.Buffer
	drop    *atomic.Bool
	next    int
	active  int
	pending *strings.Reader
	seen    int
}

var sessionLineExit = regexp.MustCompile("nova-table shell: line [0-9]+ failed \\(exit ([12])\\)")

func (in *sessionTraceInput) finish() {
	if in.active < 0 {
		return
	}
	a := in.trace.Actions[in.active]
	step := sessionTraceStep{Action: a, Stdout: in.out.String(), Stderr: in.errs.String()}
	if step.Stderr != "" {
		if m := sessionLineExit.FindStringSubmatch(step.Stderr); m != nil {
			step.Code, _ = strconv.Atoi(m[1])
		} else if strings.Contains(step.Stderr, "maximum 1048576 bytes") {
			step.Code = 2
		} else {
			in.t.Fatalf("unclassified shell output at action %d: %q", in.active, step.Stderr)
		}
	}
	all, err := in.admin.XRange(context.Background(), ntable.ChangesKey(in.table), "-", "+").Result()
	if err != nil {
		in.t.Fatal(err)
	}
	if len(all) < in.seen {
		in.t.Fatal("receipt ledger shrank")
	}
	step.Receipts = append([]redis.XMessage{}, all[in.seen:]...)
	in.seen = len(all)
	in.trace.Steps = append(in.trace.Steps, step)
	in.out.Reset()
	in.errs.Reset()
	in.active = -1
}

func (in *sessionTraceInput) Read(p []byte) (int, error) {
	if in.pending == nil || in.pending.Len() == 0 {
		in.finish()
		if in.next == len(in.trace.Actions) {
			in.trace.EOF = true
			return 0, io.EOF
		}
		a := in.trace.Actions[in.next]
		in.active = in.next
		in.next++
		// The relay's false -> true transition drops exactly the next FCALL
		// response, after the owned store has answered it.
		in.drop.Store(!a.Lost)
		line := a.Command
		if a.Kind == "long" {
			line = "#" + strings.Repeat("x", maxShellLine)
		}
		in.pending = strings.NewReader(line + "\n")
	}
	return in.pending.Read(p)
}

func sessionActions(seed int64, table string) []sessionTraceAction {
	rng := rand.New(rand.NewSource(seed))
	actions := []sessionTraceAction{
		{Kind: "ok", Command: "create " + table + " --columns ready,note:text", Write: true},
		{Kind: "ok", Command: "row add " + table + " base", Write: true},
	}
	for i := 0; i < 12; i++ {
		var a sessionTraceAction
		switch rng.Intn(8) {
		case 0:
			a = sessionTraceAction{Kind: "ok", Command: fmt.Sprintf("row add %s r%d", table, i), Write: true}
		case 1:
			a = sessionTraceAction{Kind: "ok", Command: fmt.Sprintf("cell add %s base ready m%d --score %d", table, i, i), Write: true}
		case 2:
			a = sessionTraceAction{Kind: "ok", Command: fmt.Sprintf("row set %s base note=value%d", table, i), Write: true}
		case 3:
			a = sessionTraceAction{Kind: "ok", Command: "show " + table}
		case 4:
			a = sessionTraceAction{Kind: "no", Command: "row add missing-table r"}
		case 5:
			a = sessionTraceAction{Kind: "usage", Command: "unknown-command"}
		case 6:
			a = sessionTraceAction{Kind: "long", Command: "<overlong line>"}
		case 7:
			a = sessionTraceAction{Kind: "ok", Command: fmt.Sprintf("row add %s lost%d", table, i), Write: true, Lost: true}
		}
		a.Command += fmt.Sprintf(" --idem step%d", len(actions))
		// Usage and overlong commands do not have write flags. show is a
		// read-only command and also must retain its actual argument grammar.
		if !a.Write {
			a.Command = strings.Split(a.Command, " --idem ")[0]
		}
		actions = append(actions, a)
	}
	if seed%2 == 0 {
		at := 3 + rng.Intn(len(actions)-3)
		actions = append(actions[:at], append([]sessionTraceAction{{Kind: "quit", Command: "quit"}}, actions[at:]...)...)
	}
	// Every stopped session has a real write available to catch continuation.
	actions = append(actions, sessionTraceAction{Kind: "ok", Command: "row add " + table + " tail", Write: true})
	return actions
}

func checkSessionExecution(tr sessionExecutionTrace) error {
	expectedExit, executed := 0, 0
	for _, a := range tr.Actions {
		executed++
		if a.Kind == "quit" {
			break
		}
		code := 0
		if a.Kind == "no" {
			code = 1
		}
		if a.Kind == "usage" || a.Kind == "long" || a.Lost {
			code = 2
		}
		if code > expectedExit {
			expectedExit = code
		}
		if code != 0 && !tr.Keep {
			break
		}
	}
	if len(tr.Steps) != executed || tr.Exit != expectedExit {
		return fmt.Errorf("executed=%d want=%d exit=%d want=%d", len(tr.Steps), executed, tr.Exit, expectedExit)
	}
	revision := 0
	for i, step := range tr.Steps {
		a := tr.Actions[i]
		code := 0
		if a.Kind == "no" {
			code = 1
		}
		if a.Kind == "usage" || a.Kind == "long" || a.Lost {
			code = 2
		}
		if step.Action != a || step.Code != code {
			return fmt.Errorf("step %d: action or status differs (code=%d want=%d)", i, step.Code, code)
		}
		want := 0
		if a.Write {
			want = 1 // lost response is committed, too; replay would make two
		}
		if len(step.Receipts) != want {
			return fmt.Errorf("step %d: durable receipts=%d want=%d", i, len(step.Receipts), want)
		}
		printed := strings.Count(step.Stdout, "TABLE RECEIPT ")
		if a.Lost {
			want = 0
		}
		if printed != want {
			return fmt.Errorf("step %d: printed receipts=%d want=%d", i, printed, want)
		}
		for _, receipt := range step.Receipts {
			words := strings.Fields(a.Command)
			verb := words[0]
			if verb == "row" || verb == "cell" {
				verb += "_" + words[1]
			}
			if receipt.Values["verb"] != verb {
				return fmt.Errorf("step %d: receipt verb=%v want=%s", i, receipt.Values["verb"], verb)
			}
			if receipt.Values["rev_before"] != strconv.Itoa(revision) || receipt.Values["rev_after"] != strconv.Itoa(revision+1) || receipt.Values["actor"] != "trace" {
				return fmt.Errorf("step %d: receipt revision or actor mismatch: %v", i, receipt.Values)
			}
			revision++
			if !a.Lost && !strings.Contains(step.Stdout, "event="+receipt.ID+" ") {
				return fmt.Errorf("step %d: printed receipt is not the committed event", i)
			}
			// IDs are generated by Redis; the model never predicts wall time.
			if receipt.ID == "" {
				return fmt.Errorf("step %d: missing durable event ID", i)
			}
		}
	}
	return nil
}

func TestShellRandomSequencesProduceSessionTrace(t *testing.T) {
	t.Parallel()
	addr := firstRunStore(t)
	admin := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = admin.Close() })
	var drop atomic.Bool
	drop.Store(true)
	r := startRelay(t, addr, &drop)
	coverage := map[string]int{}
	for seed := int64(1); seed <= 8; seed++ {
		for _, keep := range []bool{false, true} {
			t.Run(fmt.Sprintf("seed%d/keep%v", seed, keep), func(t *testing.T) {
				table := fmt.Sprintf("trace-%d-%v", seed, keep)
				tr := sessionExecutionTrace{Seed: seed, Keep: keep, Actions: sessionActions(seed, table)}
				var out, errs bytes.Buffer
				input := &sessionTraceInput{t: t, trace: &tr, admin: admin, table: table, out: &out, errs: &errs, drop: &drop, active: -1}
				tr.Exit = (&application{in: input, getenv: noEnv}).run([]string{
					"shell", "--redis", r.addr(), "--keep-going=" + strconv.FormatBool(keep), "--actor", "trace",
				}, &out, &errs)
				input.finish()
				raw, err := json.Marshal(tr)
				if err != nil {
					t.Fatal(err)
				}
				t.Logf("SESSION_TRACE %s", raw)
				if err := checkSessionExecution(tr); err != nil {
					t.Fatal(err)
				}
				for _, step := range tr.Steps {
					kind := step.Action.Kind
					if step.Action.Lost {
						kind = "lost"
					}
					coverage[kind]++
				}
			})
		}
	}
	t.Logf("SESSION_COVERAGE %v", coverage)
	for _, kind := range []string{"ok", "no", "usage", "long", "lost", "quit"} {
		if coverage[kind] == 0 {
			t.Errorf("generator did not execute %s", kind)
		}
	}
}
