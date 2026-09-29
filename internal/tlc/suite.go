package tlc

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
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
	Out     string        // output directory: logs, RUNS.tsv and the private copy of the models
	Budget  time.Duration // the whole suite's limit
	Workers int           // TLC workers for a case expected to pass; counterexample cases use one
	Manual  bool          // mode=manual in the records
	Host    string        // the host name recorded

	Clock  func() time.Time // time.Now when nil
	Exec   Executor         // Execute when nil
	OnCase func(Record)     // called with each record as it is made

	beforeCopy func() // a test's seam: runs between the digest and the copy
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
// done the fingerprint is taken again and the records are written to
// Out/RUNS.tsv only if it is unchanged; a suite whose inputs moved under it
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
	out, err := filepath.Abs(o.Out)
	if err != nil {
		return res, err
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		return res, fmt.Errorf("cannot create %s: %v", out, err)
	}
	res.Work = filepath.Join(out, workDir)
	// The digest is taken first and the copy is checked against it: TLC checks
	// the copy, the records name the digest, and a module edited between the two
	// would make them different bytes. Then the suite refuses to run.
	digest, plan, err := fingerprint(o.Root, filepath.Join(o.Root, "tla"))
	if err != nil {
		return res, err
	}
	// The cases to run were parsed before this digest. They are run only if the
	// plan the digest names still holds each of them: an edit to CASES.tsv
	// between the parse and the digest would otherwise run stale cases under a
	// fresh digest.
	current, err := ParseCases(bytes.NewReader(plan))
	if err != nil {
		return res, fmt.Errorf("the case plan is refused: %v", err)
	}
	for _, c := range o.Cases {
		if !slices.Contains(current, c) {
			res.Failed = true
			res.Refused = "CASES.tsv changed after the cases were read (" + c.Config + " is not as it was)"
			return res, nil
		}
	}
	if o.beforeCopy != nil {
		o.beforeCopy()
	}
	if err := CopyModels(filepath.Join(o.Root, "tla"), res.Work); err != nil {
		return res, err
	}
	copied, _, err := fingerprint(o.Root, res.Work)
	if err != nil {
		return res, err
	}
	if copied != digest {
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
	for _, c := range o.Cases {
		started := clock()
		log := filepath.Join(out, c.Config+".log")
		code := ExitTimeout
		if remaining := deadline.Sub(started); remaining <= 0 {
			if err := os.WriteFile(log, []byte("TLC suite budget exhausted before starting this case\n"), 0o644); err != nil {
				return res, err
			}
		} else {
			scratch, err := os.MkdirTemp(out, "tlc-")
			if err != nil {
				return res, err
			}
			workers := 1
			if c.Expected == "pass" {
				workers = o.Workers
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
			Config: c.Config, Module: c.Module, InputSHA256: digest, JarSHA256: o.Jar.SHA256,
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
	after, err := Fingerprint(o.Root)
	if err != nil {
		return res, err
	}
	if after != digest {
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
	if len(res.Records) != len(o.Cases) {
		res.Failed = true
	}
	return res, nil
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
