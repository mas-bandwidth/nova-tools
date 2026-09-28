package tablemodel

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/tlc"
)

// TraceStep is one step of the retained trace: the call, whether it was
// refused, its receipt, the model action that performs it and the store state
// after it.
type TraceStep struct {
	Verb    string   `json:"verb"`
	Args    []string `json:"args"`
	Actor   string   `json:"actor"`
	Refused *bool    `json:"refused"`
	Receipt Event    `json:"receipt"`
	Model   string   `json:"model"`
	State   State    `json:"state"`
}

func (t TraceStep) action() Action { return Action{t.Verb, t.Args, t.Actor} }

// Trace is trace.json: the source and model hashes it was taken from, the
// initial state and every step.
type Trace struct {
	SourceSHA256 string            `json:"source_sha256"`
	ModelSHA256  map[string]string `json:"model_sha256"`
	Initial      State             `json:"initial"`
	Steps        []TraceStep       `json:"steps"`
}

// ReplayOptions is one execution replay.
type ReplayOptions struct {
	Source string // the table.lua under test
	Jar    string // the TLC jar
	Java   string // the java program
	Models string // the directory of the TLA+ modules
	Dir    string // output directory: trace.json, the harness and the TLC logs
	Budget time.Duration

	Server ServerOptions
	Exec   tlc.Executor // tlc.Execute when nil
	OnStep func(ReplayEvent)
}

// ReplayEvent is one completed part of the replay.
type ReplayEvent struct {
	Step    string   // capture, receipt-replay, mutation-controls, tlc-execution, tlc-mutated-observation
	Verdict string   // pass, or expected-mutation-failure
	Fields  []string // key=value details
}

// modelModules are the modules the harness extends, copied beside it for TLC.
var modelModules = []string{"TableMachine", "MemberTable", "EpochMemberTable", "MCEpochMemberTable"}

// RunReplay runs the whole execution replay under one budget:
//
//  1. Capture: the trace's calls run against the table.lua in a disposable
//     store; every state, refusals included, is snapshotted; every committed
//     receipt is checked against the states on either side of its call.
//  2. Replay: the retained receipts (command, arguments, options) are replayed
//     into a second fresh store, which must pass through the same states and
//     commit the same receipts.
//  3. Controls: a receipt with a revision gap and one with a wrong member
//     delta must be refused by the checks above.
//  4. TLC: the captured states become a linear harness over EpochMemberTable
//     that must pass, and the same harness with one corrupted observation
//     must violate MatchesExecution.
//
// The trace, the harness modules and the TLC logs are kept in Dir.
func RunReplay(o ReplayOptions) error {
	ctx, cancel := context.WithTimeout(context.Background(), o.Budget)
	defer cancel()
	dir, err := filepath.Abs(o.Dir)
	if err != nil {
		return cannotRun(err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return cannotRun(fmt.Errorf("cannot create %s: %v", dir, err))
	}
	source, err := os.ReadFile(o.Source)
	if err != nil {
		return cannotRun(err)
	}
	if _, err := hashModels(o.Models); err != nil {
		return cannotRun(err)
	}
	for _, m := range append(modelModules, "MCEpochMemberTable.cfg") {
		name := m
		if filepath.Ext(m) == "" {
			name = m + ".tla"
		}
		if _, err := os.Stat(filepath.Join(o.Models, name)); err != nil {
			return cannotRun(fmt.Errorf("the model file %s is missing from %s", name, o.Models))
		}
	}
	emit := func(e ReplayEvent) {
		if o.OnStep != nil {
			o.OnStep(e)
		}
	}
	load := func(r *Store) { r.Cmd("FUNCTION", "LOAD", "#!lua name=member_replay\n"+string(source)) }

	var trace Trace
	err = WithStore(ctx, o.Server, func(r *Store) {
		load(r)
		trace = capture(r)
	})
	if err != nil {
		return err
	}
	trace.SourceSHA256 = sum(source)
	if trace.ModelSHA256, err = hashModels(o.Models); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(trace, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "trace.json"), append(raw, '\n'), 0o644); err != nil {
		return err
	}
	emit(ReplayEvent{"capture", VerdictPass, []string{"transitions=" + strconv.Itoa(len(trace.Steps)), "trace=" + filepath.Join(dir, "trace.json")}})

	err = WithStore(ctx, o.Server, func(r *Store) {
		load(r)
		replayReceipts(r, trace)
	})
	if err != nil {
		return err
	}
	emit(ReplayEvent{"receipt-replay", VerdictPass, []string{"transitions=" + strconv.Itoa(len(trace.Steps))}})

	if err := guard(func() { mutationControls(trace) }); err != nil {
		return err
	}
	emit(ReplayEvent{"mutation-controls", VerdictExpectedMutationFailure, []string{"controls=revision-gap,receipt-member-delta"}})

	for _, mutate := range []bool{false, true} {
		label, verdict, step := "execution", VerdictPass, "tlc-execution"
		if mutate {
			label, verdict, step = "mutated-observation", VerdictExpectedMutationFailure, "tlc-mutated-observation"
		}
		log, err := runHarness(ctx, o, dir, label, mutate, trace)
		if err != nil {
			return err
		}
		emit(ReplayEvent{step, verdict, []string{"log=" + log}})
	}
	return nil
}

func sum(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func hashModels(dir string) (map[string]string, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*.tla"))
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		out[filepath.Base(f)] = sum(raw)
	}
	return out, nil
}

// capture runs the trace's calls in the store and returns the trace.
func capture(r *Store) Trace {
	seed(r)
	seen := map[string]int{}
	for _, w := range writers {
		seen[w] = 1
	}
	t := Trace{Initial: snapshot(r, seen)}
	for _, a := range Actions() {
		model, err := ActionTLA(a, integer(r.Cmd("HGET", "replay:epoch", "n")))
		assert(err == nil, "%v", err)
		before := snapshot(r, seen)
		refused, event := execute(r, a, seen, nil)
		after := snapshot(r, seen)
		if event != nil {
			if err := validateDelta(event, before, after); err != nil {
				fail("%v", err)
			}
		}
		args := append([]string{}, a.Args...)
		t.Steps = append(t.Steps, TraceStep{Verb: a.Verb, Args: args, Actor: a.Actor, Refused: refused,
			Receipt: event, Model: model, State: after})
	}
	return t
}

// replayReceipts replays every step of the trace into a fresh store, calling
// the durable receipt's own command for each step that has one.
func replayReceipts(r *Store, t Trace) {
	seed(r)
	seen := map[string]int{}
	for _, w := range writers {
		seen[w] = 1
	}
	assert(reflect.DeepEqual(snapshot(r, seen), t.Initial), "the initial state differs on replay")
	for i, step := range t.Steps {
		refused, event := execute(r, step.action(), seen, step.Receipt)
		assert(reflect.DeepEqual(refused, step.Refused) && reflect.DeepEqual(snapshot(r, seen), step.State),
			"receipt replay diverged at step %d (%s)", i, step.Verb)
		if event != nil {
			want := step.Receipt.clone()
			want["id"] = event["id"]
			assert(reflect.DeepEqual(event, want), "receipt payload changed during replay at step %d", i)
		}
	}
}

// mutationControls proves the receipt checks can fail: a receipt that starts
// one revision late is a gap, and a receipt whose member delta is edited
// disagrees with the store.
func mutationControls(t Trace) {
	first := t.Steps[0].Receipt
	assert(first != nil, "the first step of the trace has no receipt")
	bad := first.clone()
	bad["rev_before"] = strconv.Itoa(atoi(bad["rev_before"]) + 1)
	err := continuity(bad, atoi(first["rev_before"]))
	assert(err != nil && strings.HasPrefix(err.Error(), "GAP:"), "gap mutation went undetected")

	bad = first.clone()
	var change []map[string]string
	assert(json.Unmarshal([]byte(bad["members"]), &change) == nil && len(change) > 0, "the first receipt has no member change to corrupt")
	change[0]["to"] = ""
	edited, err := json.Marshal(change)
	assert(err == nil, "cannot encode the edited members")
	bad["members"] = string(edited)
	err = validateDelta(bad, t.Initial, t.Steps[0].State)
	assert(err != nil && err.Error() == "receipt member delta disagrees with store", "receipt mutation went undetected")
}

// runHarness writes the harness for the trace (or for the trace with one
// observed record link destroyed), runs TLC on it, and holds the result to
// what the label declares: the real trace must pass, and the corrupted one
// must violate MatchesExecution. A validator that only ran the model without
// inspecting execution would stay green on both. It returns the log path.
func runHarness(ctx context.Context, o ReplayOptions, dir, label string, mutate bool, t Trace) (string, error) {
	observed := []State{t.Initial.Clone()}
	steps := make([]string, len(t.Steps))
	for i, s := range t.Steps {
		observed = append(observed, s.State.Clone())
		steps[i] = s.Model
	}
	if mutate {
		// Destroy one observed record link after the real move.
		observed[1].Place[0].Locations[0].Cell = NoPlace
	}
	root, err := os.MkdirTemp(dir, "member-tlc-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(root)
	for _, m := range modelModules {
		raw, err := os.ReadFile(filepath.Join(o.Models, m+".tla"))
		if err != nil {
			return "", fmt.Errorf("cannot read the model module %s: %v", m, err)
		}
		if err := os.WriteFile(filepath.Join(root, m+".tla"), raw, 0o644); err != nil {
			return "", err
		}
	}
	modelCfg, err := os.ReadFile(filepath.Join(o.Models, "MCEpochMemberTable.cfg"))
	if err != nil {
		return "", fmt.Errorf("cannot read the model configuration: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, HarnessName+".tla"), []byte(HarnessModule(observed, steps)), 0o644); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(root, HarnessName+".cfg"), []byte(HarnessConfig(string(modelCfg), len(t.Steps))), 0o644); err != nil {
		return "", err
	}
	artifact := filepath.Join(dir, label)
	if err := os.MkdirAll(artifact, 0o755); err != nil {
		return "", err
	}
	if err := copyFiles(root, artifact, "*.tla", "*.cfg"); err != nil {
		return "", err
	}
	exec := o.Exec
	if exec == nil {
		exec = tlc.Execute
	}
	log := filepath.Join(dir, label+".log")
	code := exec(ctx, tlc.Run{
		Java: o.Java, Jar: o.Jar, Dir: root,
		JVM:     []string{"-XX:+UseParallelGC", "-Xmx1g"},
		Workers: 1,
		MetaDir: filepath.Join(root, "states"),
		Config:  HarnessName + ".cfg",
		Module:  HarnessName + ".tla",
	}, log)
	// An error trace TLC wrote is evidence and stays with the harness.
	_ = copyFiles(root, artifact, "*_TTrace_*")
	raw, err := os.ReadFile(log)
	if err != nil {
		return log, err
	}
	want := tlc.Case{Expected: "pass", Property: "-"}
	if mutate {
		want = tlc.Case{Expected: "invariant", Property: "MatchesExecution"}
	}
	if !tlc.Accepts(want, code, string(raw), "") {
		return log, fmt.Errorf("the %s harness is not the result TLC should give (exit %d); see %s", label, code, log)
	}
	return log, nil
}

func copyFiles(src, dst string, patterns ...string) error {
	for _, p := range patterns {
		matches, err := filepath.Glob(filepath.Join(src, p))
		if err != nil {
			return err
		}
		for _, m := range matches {
			raw, err := os.ReadFile(m)
			if err != nil {
				return err
			}
			if err := os.WriteFile(filepath.Join(dst, filepath.Base(m)), raw, 0o644); err != nil {
				return err
			}
		}
	}
	return nil
}
