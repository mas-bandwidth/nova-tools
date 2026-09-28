// Command sessiontrace captures bounded shell executions and validates them
// against TableSession with an installed TLC jar. It downloads no tools.
package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"maps"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

type action struct {
	Kind, Command string
	Write, Lost   bool
}
type receipt struct {
	ID     string
	Values map[string]any
}
type step struct {
	Action         action
	Code           int
	Stdout, Stderr string
	Receipts       []receipt
}
type trace struct {
	Seed    int64
	Keep    bool
	Actions []action
	Steps   []step
	Exit    int
	EOF     bool
}
type testEvent struct {
	Action, Test, Output string
}

func main() {
	os.Exit(command(os.Args[1:], os.Stdout, os.Stderr))
}

func command(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("sessiontrace", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	jar := flags.String("jar", "", "existing TLC jar")
	out := flags.String("out", "", "directory for capture, generated models and results")
	if err := flags.Parse(args); errors.Is(err, flag.ErrHelp) {
		fmt.Fprintln(stdout, "sessiontrace — capture shell sessions and check TableSession with TLC\n\nusage: sessiontrace --jar /path/to/tla2tools.jar --out /path/to/results\n\nRun from the nova-tools repository on a TLC bench with cached Go dependencies.\n\nexample:\n  go run ./tools/sessiontrace --jar /path/to/tla2tools.jar --out /path/to/results")
		return 0
	} else if err != nil {
		fmt.Fprintf(stderr, "sessiontrace: invalid arguments: %v; state: no capture started; next: run sessiontrace -h for the supported flags\n", err)
		return 2
	}
	if *jar == "" || *out == "" || flags.NArg() != 0 {
		fmt.Fprintln(stderr, "sessiontrace: want --jar /path/to/tla2tools.jar --out /path/to/results; state: no capture started; next: supply both paths without positional arguments")
		return 2
	}
	if err := run(*jar, *out, stdout); err != nil {
		fmt.Fprintln(stderr, "sessiontrace:", err)
		return 1
	}
	return 0
}

func repoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("cannot read working directory; state: repository unresolved; next: change into the nova-tools checkout and retry: %w", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "tla", "TableSession.tla")); err == nil {
			return dir, nil
		}
		next := filepath.Dir(dir)
		if next == dir {
			return "", errors.New("tla/TableSession.tla not found in working-directory ancestry; state: repository unresolved; next: run from the nova-tools checkout")
		}
		dir = next
	}
}

// test2json fragments long log lines. Reassemble Output before reading traces.
func readTraces(r io.Reader) ([]trace, []json.RawMessage, error) {
	var output strings.Builder
	passed := false
	dec := json.NewDecoder(r)
	for {
		var ev testEvent
		err := dec.Decode(&ev)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, nil, fmt.Errorf("invalid test event at byte %d: %w", dec.InputOffset(), err)
		}
		if ev.Action == "fail" || ev.Action == "skip" {
			return nil, nil, errors.New("execution test failed or skipped; state: capture unverified; next: inspect the capture log, restore the test prerequisites and rerun")
		}
		if ev.Action == "pass" && ev.Test == "" {
			passed = true
		}
		output.WriteString(ev.Output)
	}
	if !passed {
		return nil, nil, errors.New("execution test did not pass; state: capture incomplete; next: inspect the capture log and rerun the complete execution test")
	}
	var traces []trace
	var raw []json.RawMessage
	scanner := bufio.NewScanner(strings.NewReader(output.String()))
	scanner.Buffer(make([]byte, 4096), 8<<20)
	for scanner.Scan() {
		_, row, ok := strings.Cut(scanner.Text(), "SESSION_TRACE ")
		if !ok {
			continue
		}
		var tr trace
		if err := json.Unmarshal([]byte(row), &tr); err != nil {
			return nil, nil, fmt.Errorf("invalid SESSION_TRACE record: %w", err)
		}
		traces = append(traces, tr)
		raw = append(raw, json.RawMessage(row))
	}
	if err := scanner.Err(); err != nil {
		return nil, nil, err
	}
	if len(traces) != 16 {
		return nil, nil, fmt.Errorf("wanted all 16 bounded sessions, got %d", len(traces))
	}
	seen := map[string]bool{}
	for _, tr := range traces {
		key := fmt.Sprintf("%d/%t", tr.Seed, tr.Keep)
		if tr.Seed < 1 || tr.Seed > 8 || seen[key] {
			return nil, nil, fmt.Errorf("duplicate or unexpected session %s", key)
		}
		seen[key] = true
		if len(tr.Actions) == 0 || len(tr.Steps) == 0 || len(tr.Steps) > len(tr.Actions) {
			return nil, nil, fmt.Errorf("incomplete session %s", key)
		}
		for i, s := range tr.Steps {
			if s.Action != tr.Actions[i] || s.Code < 0 || s.Code > 2 {
				return nil, nil, fmt.Errorf("invalid observation in session %s step %d", key, i)
			}
		}
		if !tr.Steps[0].Action.Write || len(tr.Steps[0].Receipts) != 1 {
			return nil, nil, fmt.Errorf("session %s lacks its initial write witness", key)
		}
	}
	return traces, raw, nil
}

var errDuplicateReceipt = errors.New("duplicate durable receipt")

func checkReceipts(traces []trace) error {
	for _, tr := range traces {
		revision := 0
		ids := map[string]bool{}
		for _, s := range tr.Steps {
			want := 0
			if s.Action.Write {
				want = 1
			}
			if len(s.Receipts) != want {
				return errors.New("receipt count does not match committed effects")
			}
			var lines []string
			for _, line := range strings.Split(s.Stdout, "\n") {
				if strings.HasPrefix(line, "TABLE RECEIPT ") {
					lines = append(lines, line)
				}
			}
			if s.Action.Lost {
				want = 0
			}
			if len(lines) != want {
				return errors.New("printed receipt count differs")
			}
			for _, r := range s.Receipts {
				if ids[r.ID] {
					return errDuplicateReceipt
				}
				ids[r.ID] = true
				if r.Values["rev_before"] != strconv.Itoa(revision) ||
					r.Values["rev_after"] != strconv.Itoa(revision+1) || r.Values["actor"] != "trace" {
					return errors.New("receipt revision or actor differs")
				}
				revision++
				if !s.Action.Lost && !strings.Contains(lines[0], "event="+r.ID+" ") {
					return errors.New("printed receipt ID differs from durable event")
				}
			}
		}
	}
	return nil
}

func seq(xs []string) string { return "<<" + strings.Join(xs, ", ") + ">>" }

func module(traces []trace) string {
	var inputs, keeps, exits, remains, ends, lengths, actions, observations []string
	for i, tr := range traces {
		var kinds []string
		for _, a := range tr.Actions {
			kinds = append(kinds, strconv.Quote(a.Kind))
		}
		inputs = append(inputs, seq(kinds))
		keeps = append(keeps, strings.ToUpper(strconv.FormatBool(tr.Keep)))
		exits = append(exits, strconv.Itoa(tr.Exit))
		remains = append(remains, strconv.Itoa(len(tr.Actions)-len(tr.Steps)))
		end := "fail"
		if tr.EOF {
			end = "eof"
		} else if len(tr.Steps) > 0 && tr.Steps[len(tr.Steps)-1].Action.Kind == "quit" {
			end = "quit"
		}
		ends = append(ends, strconv.Quote(end))
		tick, high := 0, 0
		add := func(a string) {
			actions = append(actions, fmt.Sprintf("  \\/ /\\ traceCase = %d /\\ tick = %d\n     /\\ %s\n     /\\ tick' = %d", i+1, tick, a, tick+1))
			tick++
		}
		for _, s := range tr.Steps {
			code := 0
			if s.Action.Kind == "usage" || s.Action.Kind == "long" {
				code = 2
			}
			add("ReadLine /\\ lineCode' = " + strconv.Itoa(code))
			if s.Action.Kind == "ok" || s.Action.Kind == "no" {
				conn := "live"
				if s.Code == 2 {
					conn = "dead"
				}
				add("RunVerb /\\ conn' = " + strconv.Quote(conn) +
					" /\\ lineCode' = IF conn' = \"dead\" THEN 2 ELSE Code(cur)")
			}
			high = max(high, s.Code)
			observations = append(observations, fmt.Sprintf(
				" /\\ ((traceCase = %d /\\ tick = %d) => (exit = %d /\\ lineCode = %d))", i+1, tick, high, s.Code))
		}
		if tr.EOF {
			add("EndOfInput /\\ UNCHANGED lineCode")
		}
		lengths = append(lengths, strconv.Itoa(tick))
	}
	return fmt.Sprintf(modelTemplate, seq(inputs), seq(keeps), seq(exits), seq(remains),
		seq(ends), seq(lengths), len(traces), strings.Join(actions, "\n"), strings.Join(observations, "\n"))
}

const modelTemplate = `---------------------- MODULE SessionTrace ----------------------
EXTENDS TableSession
VARIABLES traceCase, tick, lineCode
traceVars == <<vars, traceCase, tick, lineCode>>
ObservedInputs == %s
TraceInputs == UNION {{SubSeq(s, j, Len(s)) : j \in 1..(Len(s)+1)} : s \in {ObservedInputs[i] : i \in DOMAIN ObservedInputs}}
ObservedKeep == %s
ObservedExit == %s
ObservedRemaining == %s
ObservedEnd == %s
TraceLength == %s
TraceInit ==
 /\ traceCase \in 1..%d
 /\ input = ObservedInputs[traceCase]
 /\ keep = ObservedKeep[traceCase]
 /\ store = "up"
 /\ Init
 /\ tick = 0
 /\ lineCode = 0
TraceStep ==
%s
TraceNext ==
 /\ UNCHANGED traceCase
 /\ (TraceStep \/ (tick = TraceLength[traceCase] /\ UNCHANGED <<vars, tick, lineCode>>))
TraceSpec == TraceInit /\ [][TraceNext]_traceVars
TraceObserved ==
%s
TraceEnd ==
 (tick = TraceLength[traceCase]) =>
   /\ pc = "done"
   /\ exit = ObservedExit[traceCase]
   /\ Len(input) = ObservedRemaining[traceCase]
   /\ how = ObservedEnd[traceCase]
=============================================================================
`

func configuration(traces []trace) string {
	bound := 0
	for _, tr := range traces {
		bound = max(bound, len(tr.Actions))
	}
	return "SPECIFICATION TraceSpec\nCONSTANTS\n Inputs <- TraceInputs\n MaxLines = " + strconv.Itoa(bound) +
		"\n Broken = \"none\"\nINVARIANTS TypeOK NothingStartsAfterStop StopOnlyWhenStopped " +
		"NoFalseAlarm ConnectionFailureIsTwo AtMostOnce StopsAtFirstFailure ExitIsHighest " +
		"KeepGoingReadsEveryLine EndOfInputMeansNoFailure EndsForAReason LineInHand " +
		"LiveMeansUp TraceObserved TraceEnd\n"
}

func runTLC(ctx context.Context, root, jar, out string, traces []trace) (int, string, error) {
	if err := os.MkdirAll(out, 0o755); err != nil {
		return 0, "", err
	}
	source, err := os.ReadFile(filepath.Join(root, "tla", "TableSession.tla"))
	if err != nil {
		return 0, "", err
	}
	files := map[string][]byte{"TableSession.tla": source, "SessionTrace.tla": []byte(module(traces)), "SessionTrace.cfg": []byte(configuration(traces))}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(out, name), data, 0o644); err != nil {
			return 0, "", err
		}
	}
	scratch, err := os.MkdirTemp("", "tlc-session-")
	if err != nil {
		return 0, "", err
	}
	defer os.RemoveAll(scratch)
	logPath := filepath.Join(out, "tlc.log")
	log, err := os.Create(logPath)
	if err != nil {
		return 0, "", err
	}
	cmd := exec.CommandContext(ctx, "java", "-XX:+UseParallelGC", "-Xmx512m",
		"-Djava.io.tmpdir="+scratch, "-cp", jar, "tlc2.TLC", "-workers", "2",
		"-metadir", filepath.Join(scratch, "states"), "-config", "SessionTrace.cfg", "SessionTrace.tla")
	cmd.Dir, cmd.Stdout, cmd.Stderr = out, log, log
	runErr := cmd.Run()
	closeErr := log.Close()
	if ctx.Err() != nil {
		return 0, "", ctx.Err()
	}
	code := 0
	var exitErr *exec.ExitError
	if errors.As(runErr, &exitErr) {
		code = exitErr.ExitCode()
	} else if runErr != nil {
		return 0, "", runErr
	}
	if closeErr != nil {
		return 0, "", closeErr
	}
	data, err := os.ReadFile(logPath)
	return code, string(data), err
}

func writeJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

func clone(traces []trace) []trace {
	copied := slices.Clone(traces)
	for i := range copied {
		copied[i].Actions = slices.Clone(traces[i].Actions)
		copied[i].Steps = slices.Clone(traces[i].Steps)
		for j := range copied[i].Steps {
			copied[i].Steps[j].Receipts = slices.Clone(traces[i].Steps[j].Receipts)
			for k := range copied[i].Steps[j].Receipts {
				copied[i].Steps[j].Receipts[k].Values = maps.Clone(traces[i].Steps[j].Receipts[k].Values)
			}
		}
	}
	return copied
}

// Preserve counts, revisions and printed IDs so only cross-step uniqueness fails.
func checkDuplicateControl(traces []trace) error {
	bad := clone(traces)
	for i := range bad {
		first := ""
		for j := range bad[i].Steps {
			s := &bad[i].Steps[j]
			if !s.Action.Write || len(s.Receipts) != 1 {
				continue
			}
			if first == "" {
				first = s.Receipts[0].ID
				continue
			}
			old := s.Receipts[0].ID
			s.Receipts[0].ID = first
			s.Stdout = strings.ReplaceAll(s.Stdout, "event="+old+" ", "event="+first+" ")
			if err := checkReceipts(bad); !errors.Is(err, errDuplicateReceipt) {
				return fmt.Errorf("duplicate-ID control wanted %q, got %v; state: uniqueness check unproven; next: inspect receipt validation before accepting this run", errDuplicateReceipt, err)
			}
			return nil
		}
	}
	return errors.New("no session contains two write receipts; state: duplicate-ID control untested; next: restore a trace with two writes and rerun")
}

func readCapture(path string) ([]trace, []json.RawMessage, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("cannot read capture %s; state: capture unverified; next: check the file and rerun capture: %w", path, err)
	}
	traces, raw, err := readTraces(bytes.NewReader(data))
	if err != nil {
		return nil, nil, fmt.Errorf("capture %s rejected; state: sessions unverified; next: inspect this log, fix the capture failure and rerun: %w", path, err)
	}
	return traces, raw, nil
}

func run(jar, out string, stdout io.Writer) error {
	var err error
	if jar, err = filepath.Abs(jar); err != nil {
		return err
	}
	if info, err := os.Stat(jar); err != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("--jar %q is not an existing regular file; state: no capture started; next: pass the path to an installed TLC jar", jar)
	}
	if out, err = filepath.Abs(out); err != nil {
		return err
	}
	root, err := repoRoot()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	started := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	capture := filepath.Join(out, "capture.json")
	log, err := os.Create(capture)
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, "go", "test", "-tags", "functional", "-p", "2", "-parallel", "2",
		"-count=1", "-timeout", "50s", "-json", "./cmd/nova-table", "-run", "^TestShellRandomSequencesProduceSessionTrace$")
	cmd.Dir, cmd.Stdout, cmd.Stderr = root, log, log
	cmd.Env = append(os.Environ(), "GOMAXPROCS=2", "NOVA_CI=1",
		"GOTOOLCHAIN=local", "GOPROXY=off", "GOSUMDB=off")
	runErr, closeErr := cmd.Run(), log.Close()
	if runErr != nil {
		return fmt.Errorf("capture failed: %s; state: sessions unverified; next: inspect this log, ensure Redis and cached Go dependencies are available, and rerun: %w", capture, runErr)
	}
	if closeErr != nil {
		return closeErr
	}
	traces, raw, err := readCapture(capture)
	if err != nil {
		return err
	}
	if err := checkReceipts(traces); err != nil {
		return fmt.Errorf("receipt check failed in %s; state: durable effects unverified; next: inspect the recorded receipts and repair the mismatch: %w", capture, err)
	}
	if err := writeJSON(filepath.Join(out, "trace.json"), raw); err != nil {
		return err
	}
	hashes := map[string]string{}
	for _, name := range []string{"tla/TableSession.tla", "tools/sessiontrace/main.go",
		"cmd/nova-table/session.go", "cmd/nova-table/session_property_functional_test.go", "cmd/nova-table/connect_functional_test.go"} {
		data, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			return err
		}
		hashes[name] = fmt.Sprintf("%x", sha256.Sum256(data))
	}
	data, err := os.ReadFile(jar)
	if err != nil {
		return err
	}
	hashes["tlc_jar"] = fmt.Sprintf("%x", sha256.Sum256(data))
	if err := writeJSON(filepath.Join(out, "hashes.json"), hashes); err != nil {
		return err
	}
	code, output, err := runTLC(ctx, root, jar, filepath.Join(out, "positive"), traces)
	if err != nil {
		return err
	}
	if code != 0 || !strings.Contains(output, "Model checking completed. No error has been found.") {
		return fmt.Errorf("positive TLC failed: %s", filepath.Join(out, "positive", "tlc.log"))
	}
	badExit := clone(traces)
	badExit[0].Exit = (badExit[0].Exit + 1) % 3
	code, output, err = runTLC(ctx, root, jar, filepath.Join(out, "wrong-exit"), badExit)
	if err != nil {
		return err
	}
	if code != 12 || !strings.Contains(output, "Invariant TraceEnd is violated.") {
		return errors.New("TLC failed to reject wrong final exit")
	}
	badStatus := clone(traces)
	corrupted := false
	for i := range badStatus {
		high := 0
		for j := range badStatus[i].Steps {
			s := &badStatus[i].Steps[j]
			if high == 2 && s.Code == 0 && s.Action.Kind == "ok" {
				s.Code, corrupted = 1, true
				break
			}
			high = max(high, s.Code)
		}
		if corrupted {
			break
		}
	}
	if !corrupted {
		return errors.New("no high-exit trace for line-status negative control")
	}
	code, output, err = runTLC(ctx, root, jar, filepath.Join(out, "wrong-line-status"), badStatus)
	if err != nil {
		return err
	}
	if code != 12 || !strings.Contains(output, "Invariant TraceObserved is violated.") {
		return errors.New("TLC failed to reject wrong per-line status")
	}
	if err := checkDuplicateControl(traces); err != nil {
		return err
	}
	steps := 0
	for _, tr := range traces {
		steps += len(tr.Steps)
	}
	result := map[string]any{"seconds": math.Round(time.Since(started).Seconds()*1000) / 1000,
		"sessions": len(traces), "steps": steps, "positive_tlc": "PASS", "wrong_exit_control": "REJECTED",
		"wrong_line_status_control": "REJECTED", "duplicate_effect_control": "REJECTED"}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := writeJSON(filepath.Join(out, "result.json"), result); err != nil {
		return err
	}
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(result)
}
