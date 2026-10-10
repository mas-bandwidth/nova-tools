package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
)

// The shadow tick, and the canary before a server swap (docs/SPEC-SPRINT.md
// section 14, install-canary-shadow-tick-r.w1). `tick --shadow` plans the tick
// on the live store and applies nothing: its store is opened read-only
// (store.ReadOnly), so every write it could reach is a refusal, and it prints
// the plan. `server switch <binary>` runs `<binary> tick --shadow --json` first,
// under the tick deadline, and refuses the swap when the shadow exits non-zero,
// panics, misses the deadline or prints no plan; the old server keeps running.

// shadowFlag is tick's --shadow.
func shadowFlag(on *bool) func(flagSet) {
	return func(fs flagSet) {
		fs.BoolVar(on, "shadow", false, "plan the tick on the store and apply nothing: the store is opened read-only (every write a refusal), no beat, heartbeat, repair or restore is written, and the plan is printed, part by part, with its size and time; server switch runs it as the canary of a new binary")
	}
}

// shadowOut is tick --shadow's --json line.
type shadowOut struct {
	Shadow *store.ShadowPlan `json:"shadow,omitempty"`
	Error  string            `json:"error,omitempty"`
}

// shadowStore is the store a shadow tick reads: the backend as every verb
// opens it, wrapped read-only, and none of storeCtx's writes (a twin's beats,
// an owed restore).
func (a *app) shadowStore(ctx context.Context, c common) (*store.Store, error) {
	if a.getenv("NOVA_SPRINT_PREFIX") != "" {
		return nil, errors.New("NOVA_SPRINT_PREFIX is set: " + noPrefix + "; unset it")
	}
	if strings.TrimSpace(c.redis) == "" {
		return nil, errors.New("--redis <addr> is required (or NOVA_SPRINT_REDIS, NOVA_REDIS_ADDR): a shadow tick plans on a store")
	}
	names := sprint.Names{}
	b, err := a.backend(ctx, c.redis, names)
	if err != nil {
		return nil, err
	}
	return &store.Store{B: store.ReadOnly(b), Names: names, Actor: c.actor, Now: a.now, NewID: store.NewID, Sleep: a.sleep, ByHand: a.twinOpen(c.redis)}, nil
}

// shadowTick is tick --shadow: the plan, printed; exit 0 when it was made, 2 when
// it was not.
func (a *app) shadowTick(c common, rules, idle bool, stdout, stderr io.Writer) int {
	ctx := context.Background()
	st, err := a.shadowStore(ctx, c)
	if err != nil {
		return refuse(stderr, "tick", err.Error())
	}
	st.AnswerRules, st.IdleAlarm = rules, idle
	plan, err := st.ShadowTick(ctx)
	err = noSprintYet(err)
	if c.json {
		o := shadowOut{}
		if err != nil {
			o.Error = err.Error()
		} else {
			o.Shadow = &plan
		}
		b, _ := json.Marshal(o)
		fmt.Fprintln(stdout, string(b))
	} else {
		for _, p := range plan.Parts {
			part := p.Name
			if p.Table != "" {
				part = p.Table + "/" + p.Name
			}
			fmt.Fprintf(stdout, "SHADOW PLAN %s size=%d due=%d\n", part, p.Size, p.Due)
			if part == sprint.Readers+"/ask" {
				for _, r := range plan.Reads {
					fmt.Fprintf(stdout, "SHADOW READS %s\n", oneline.Escape(r))
				}
				plan.Reads = nil
			}
		}
		for _, r := range plan.Reads { // the ask planned nothing
			fmt.Fprintf(stdout, "SHADOW READS %s\n", oneline.Escape(r))
		}
		status := "OK"
		if err != nil {
			status = "FAILED"
		}
		fmt.Fprintf(stdout, "SHADOW TICK %s epoch=%d state=%s parts=%d size=%d took=%s wrote=nothing\n", status, plan.Epoch, plan.State, len(plan.Parts), plan.Size, plan.Took.Round(time.Millisecond))
	}
	if err != nil {
		fmt.Fprintf(stderr, "%s tick --shadow: %s; run: nova-sprint tick -h\n", prog, oneline.Escape(err.Error()))
		return 2
	}
	return 0
}

// shadowRecord is what server switch keeps beside the switch record, at
// <target>.shadow.json: the candidate's shadow tick, its plan's size and its
// time.
type shadowRecord struct {
	Binary string           `json:"binary"`
	At     time.Time        `json:"at"`
	Wall   time.Duration    `json:"wall_ns"` // the shadow's process, start to exit
	Plan   store.ShadowPlan `json:"plan"`
}

// shadowOutCap bounds what a candidate's shadow may print that the switch keeps.
const shadowOutCap = 1 << 20

// capped is a writer that keeps the first n bytes and takes the rest unkept.
type capped struct {
	b bytes.Buffer
	n int
}

func (w *capped) Write(p []byte) (int, error) {
	if room := w.n - w.b.Len(); room > 0 {
		w.b.Write(p[:min(len(p), room)])
	}
	return len(p), nil
}

// runShadow runs `<binary> tick --shadow --json` against the store, under the
// deadline (0: none): the plan it printed, and how long its process took; an
// error names why the candidate is refused (it exited non-zero, panicked,
// missed the deadline, or printed no plan).
func runShadow(ctx context.Context, binary, redis string, deadline time.Duration) (store.ShadowPlan, time.Duration, error) {
	if deadline > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, deadline)
		defer cancel()
	}
	args := []string{"tick", "--shadow", "--json"}
	if redis != "" {
		args = append(args, "--redis", redis)
	}
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Env = os.Environ()
	cmd.WaitDelay = time.Second
	var out, errs = &capped{n: shadowOutCap}, &capped{n: shadowOutCap}
	cmd.Stdout, cmd.Stderr = out, errs
	began := time.Now()
	err := cmd.Run()
	wall := time.Since(began)
	if deadline > 0 && errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return store.ShadowPlan{}, wall, fmt.Errorf("missed the tick deadline %s: killed after %s", deadline, wall.Round(time.Millisecond))
	}
	if p := panicLine(errs.b.String()); p != "" {
		return store.ShadowPlan{}, wall, fmt.Errorf("panicked: %s", p)
	}
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return store.ShadowPlan{}, wall, fmt.Errorf("exited %d: %s", ee.ExitCode(), shadowLast(errs.b.String(), out.b.String()))
		}
		return store.ShadowPlan{}, wall, fmt.Errorf("did not run: %v", err)
	}
	plan, line, err := shadowLine(out.b.String())
	if err != nil {
		return store.ShadowPlan{}, wall, fmt.Errorf("exited 0 and printed no plan: %q", line)
	}
	return plan, wall, nil
}

// shadowLine is the plan tick --shadow --json printed on its last line, and the line.
func shadowLine(stdout string) (store.ShadowPlan, string, error) {
	line := shadowLast(stdout)
	var o shadowOut
	if err := json.Unmarshal([]byte(line), &o); err != nil {
		return store.ShadowPlan{}, line, err
	}
	if o.Shadow == nil {
		return store.ShadowPlan{}, line, fmt.Errorf("no plan: %s", o.Error)
	}
	return *o.Shadow, line, nil
}

// panicLine is the panic a Go program's stderr shows ("" for none).
func panicLine(s string) string {
	for _, l := range strings.Split(s, "\n") {
		if strings.HasPrefix(l, "panic: ") || strings.HasPrefix(l, "fatal error: ") {
			return strings.TrimSpace(l)
		}
	}
	return ""
}

// shadowLast is the last line that is not blank of the first text that has one.
func shadowLast(texts ...string) string {
	for _, s := range texts {
		lines := strings.Split(strings.TrimSpace(s), "\n")
		if l := strings.TrimSpace(lines[len(lines)-1]); l != "" {
			return l
		}
	}
	return "(nothing printed)"
}

// writeShadowRecord keeps the shadow beside the switch record, atomically.
func writeShadowRecord(target string, r shadowRecord) error {
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	tmp := target + ".shadow.json.tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, target+".shadow.json")
}
