package tlc

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/safepath"
)

// Bounds of a run. A bounded run is the only kind a required case can be
// measured by; a manual run is an explicit bench experiment and CI refuses it.
const (
	BoundedCap = 110 * time.Second
	ManualCap  = 3600 * time.Second
)

// CheckLimits refuses a budget or worker count outside what a run may use: at
// most two TLC workers, and a budget that is positive and within the cap of
// its mode.
func CheckLimits(budget time.Duration, workers int, manual bool) error {
	cap := BoundedCap
	if manual {
		cap = ManualCap
	}
	if budget <= 0 || budget > cap {
		return fmt.Errorf("the timeout must be positive and at most %s", cap)
	}
	if workers != 1 && workers != 2 {
		return errors.New("workers must be 1 or 2")
	}
	return nil
}

// Options is one suite: the selected cases under one budget.
type Options struct {
	Root    string        // checkout root; the models are root/tla
	Cases   []Case        // the selected cases, in order
	Jar     Jar           // the TLC jar
	Java    string        // the java program
	JavaVer string        // the version java reported (JavaVersion); recorded
	Out     string        // output directory: logs, RUNS.tsv and the private copy of the models
	Budget  time.Duration // the whole suite's limit
	Workers int           // TLC workers for a case expected to pass; counterexample cases use one
	Manual  bool          // mode=manual in the records
	Host    string        // the host name recorded

	Clock  func() time.Time // time.Now when nil
	Exec   Executor         // Execute when nil
	OnCase func(Record)     // called with each record as it is made

	// Selection is how Cases was chosen from the plan. RunSuite chooses again
	// from the plan its digest names, runs that, and refuses when it is not the
	// caller's Cases. The zero value selects every case.
	Selection Selection

	beforeCopy func() // a test's seam: runs between the digest and the copy
}

// Selection is a choice of cases from the plan, as Select takes it.
type Selection struct {
	Group         string
	Shards, Shard int
}

// selectionDiffers says how two selections differ, or "" when they are the
// same cases in the same order with the same fields.
func selectionDiffers(read, digested []Case) string {
	for i := 0; i < len(read) && i < len(digested); i++ {
		if read[i] != digested[i] {
			if read[i].Config != digested[i].Config {
				return "case " + strconv.Itoa(i+1) + " was " + read[i].Config + " and is " + digested[i].Config
			}
			return read[i].Config + " is not as it was"
		}
	}
	if len(read) != len(digested) {
		return "the selection was " + strconv.Itoa(len(read)) + " cases and is " + strconv.Itoa(len(digested))
	}
	return ""
}

// Result is what a suite did.
type Result struct {
	Records []Record
	Failed  bool   // a case was not the declared result, or the budget ran out first
	Refused string // set when the records were not written, and why
	Work    string // the private copy of the models the cases ran in
}

const workDir = "work"

// RunSuite runs the selected cases one after the other. Each runs in a private
// copy of the models, with its own temporary directory for TLC's standard
// modules and state files, and its log at Out/<config>.log. When the cases are
// done the fingerprint of each is taken again and the records are written to
// Out/RUNS.tsv only if none changed; a suite whose inputs moved under it
// writes nothing and says so. A budget that ends before the last case is a
// failure, and the records of the cases that ran are still written.
func RunSuite(o Options) (Result, error) {
	clock, exec := o.Clock, o.Exec
	if clock == nil {
		clock = time.Now
	}
	if exec == nil {
		exec = Execute
	}
	var res Result
	if o.JavaVer == "" {
		return res, errors.New("no java version to record")
	}
	out, err := filepath.Abs(o.Out)
	if err != nil {
		return res, err
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		return res, fmt.Errorf("cannot create %s: %v", out, err)
	}
	res.Work = filepath.Join(out, workDir)
	// The fingerprints are taken first and the copy is checked against them:
	// TLC checks the copy, the records name the fingerprints, and a module
	// edited between the two would make them different bytes. Then the suite
	// refuses to run.
	src, err := SourceAt(o.Root)
	if err != nil {
		return res, err
	}
	// The cases to run are the plan's own rows, parsed from the same bytes the
	// fingerprints are taken from: an edit to CASES.tsv between the caller's
	// parse and now would otherwise run stale cases under a fresh fingerprint.
	current, err := ParseCases(bytes.NewReader(src.Plan))
	if err != nil {
		return res, fmt.Errorf("the case plan is refused: %v", err)
	}
	sel := o.Selection
	if sel.Shards == 0 {
		sel.Shards = 1
	}
	chosen, err := Select(current, sel.Group, sel.Shards, sel.Shard)
	if err != nil {
		return res, fmt.Errorf("the selection no longer fits the case plan: %v", err)
	}
	if why := selectionDiffers(o.Cases, chosen); why != "" {
		res.Failed = true
		res.Refused = "CASES.tsv changed after the cases were read (" + why + ")"
		return res, nil
	}
	digests, err := fingerprints(src, chosen)
	if err != nil {
		return res, err
	}
	if o.beforeCopy != nil {
		o.beforeCopy()
	}
	if err := CopyModels(filepath.Join(o.Root, "tla"), res.Work); err != nil {
		return res, err
	}
	copied := src
	copied.TLADir = res.Work
	if got, err := fingerprints(copied, chosen); err != nil {
		return res, err
	} else if !sameFingerprints(got, digests) {
		res.Failed = true
		res.Refused = "model inputs changed while the models were copied"
		return res, nil
	}
	begin := clock()
	deadline := begin.Add(o.Budget)
	budget := strconv.FormatFloat(o.Budget.Seconds(), 'g', -1, 64)
	mode := "bounded"
	if o.Manual {
		mode = "manual"
	}
	// What runs is the selection of the digested plan, never the caller's slice.
	for _, c := range chosen {
		started := clock()
		log := filepath.Join(out, c.Config+".log")
		code := ExitTimeout
		workers := 1
		if c.Expected == "pass" {
			workers = o.Workers
		}
		if remaining := deadline.Sub(started); remaining <= 0 {
			if err := os.WriteFile(log, []byte("TLC suite budget exhausted before starting this case\n"), 0o644); err != nil {
				return res, err
			}
		} else {
			scratch, err := os.MkdirTemp(out, "tlc-")
			if err != nil {
				return res, err
			}
			ctx, cancel := context.WithTimeout(context.Background(), remaining)
			code = exec(ctx, Run{
				Java: o.Java, Jar: o.Jar.Path, Dir: res.Work,
				JVM:          []string{"-XX:+UseParallelGC", "-XX:ActiveProcessorCount=2", "-Xmx2g"},
				TmpDir:       scratch,
				Workers:      workers,
				LnCheckFinal: true,
				NoDeadlock:   c.Deadlock == "ignore-terminal",
				MetaDir:      filepath.Join(scratch, "states"),
				Config:       c.Config,
				Module:       c.Module,
			}, log)
			cancel()
			_ = safepath.RemoveUnder(out, scratch)
		}
		raw, err := os.ReadFile(log)
		if err != nil {
			return res, fmt.Errorf("cannot read %s: %v", log, err)
		}
		cfg, err := os.ReadFile(filepath.Join(res.Work, c.Config))
		if err != nil {
			return res, fmt.Errorf("cannot read the configuration %s: %v", c.Config, err)
		}
		outcome := Parse(string(raw))
		ok := Accepts(c, code, string(raw), string(cfg)) && outcome.HasStats()
		rec := Record{
			Config: c.Config, Module: c.Module, InputSHA256: digests[c.Config].fingerprint, InputFiles: digests[c.Config].files, JarSHA256: o.Jar.SHA256, JavaVersion: o.JavaVer, Workers: workers,
			Host: o.Host, StartedUTC: started.UTC().Format("2006-01-02T15:04:05.000000-07:00"),
			Generated: outcome.Generated, Distinct: outcome.Distinct,
			Seconds: fmt.Sprintf("%.3f", clock().Sub(started).Seconds()),
			Exit:    code, Result: "PASS", Expected: c.Expected, Property: c.Property,
			Budget: budget, Mode: mode,
		}
		if !ok {
			rec.Result = "FAIL"
			res.Failed = true
		}
		res.Records = append(res.Records, rec)
		if o.OnCase != nil {
			o.OnCase(rec)
		}
		if !clock().Before(deadline) {
			break
		}
	}
	after, err := SourceAt(o.Root)
	if err != nil {
		return res, err
	}
	if got, err := fingerprints(after, chosen); err != nil {
		return res, err
	} else if !sameFingerprints(got, digests) {
		res.Failed = true
		res.Refused = "model inputs changed during execution"
		return res, nil
	}
	f, err := os.Create(filepath.Join(out, RunsFile))
	if err != nil {
		return res, err
	}
	if err := WriteRecords(f, res.Records); err != nil {
		f.Close()
		return res, err
	}
	if err := f.Close(); err != nil {
		return res, err
	}
	if len(res.Records) != len(chosen) {
		res.Failed = true
	}
	return res, nil
}

// fingerprint is the fingerprint of one case and the number of inputs under it.
type fingerprint struct {
	fingerprint string
	files       int
}

// fingerprints takes the fingerprint of each case, by config.
func fingerprints(src Source, cases []Case) (map[string]fingerprint, error) {
	out := map[string]fingerprint{}
	for _, c := range cases {
		fp, n, err := src.Fingerprint(c.Config)
		if err != nil {
			return nil, err
		}
		out[c.Config] = fingerprint{fp, n}
	}
	return out, nil
}

func sameFingerprints(a, b map[string]fingerprint) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// CopyModels copies the TLA+ modules and configurations of src into dst,
// replacing dst. TLC writes files beside the spec it is given (an error trace
// as a module and a binary), so a run never gets the checkout's own directory.
func CopyModels(src, dst string) error {
	if err := safepath.RemoveUnder(filepath.Dir(dst), dst); err != nil {
		return fmt.Errorf("cannot clear %s: %v", dst, err)
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return fmt.Errorf("cannot create %s: %v", dst, err)
	}
	for _, pattern := range []string{"*.tla", "*.cfg"} {
		matches, err := filepath.Glob(filepath.Join(src, pattern))
		if err != nil {
			return err
		}
		for _, m := range matches {
			raw, err := os.ReadFile(m)
			if err != nil {
				return fmt.Errorf("cannot read %s: %v", m, err)
			}
			if err := os.WriteFile(filepath.Join(dst, filepath.Base(m)), raw, 0o644); err != nil {
				return err
			}
		}
	}
	return nil
}
