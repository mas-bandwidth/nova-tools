package merge

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/goenv"
)

// Step is one step of the gate: the word its progress line carries, the command it
// runs in the merged tree, the program that must be on PATH for it to mean anything,
// the file of the checkout it runs, and whether its output is a `go test -json` stream.
type Step struct {
	Name    string
	Command string
	Needs   string
	File    string
	Stream  bool
	Env     []string
}

const (
	ClassGo     = "go"
	ClassVetWin = "+vetwin"
	ClassLisp   = "lisp"
	ClassFull   = "full"
)

// Selection specifies the gate class and the selected packages.
type Selection struct {
	Class    string   // "go", "+vetwin", "lisp", "full"
	Packages []string // selected packages for class go / +vetwin
}

// FullClassSteps is the suite run for class "full": every step of the base's required
// set, byte for byte with ci.yml's bench-runnable legs (SPEC-MERGE parity).
var FullClassSteps = []Step{
	{Name: "build", Command: "go build ./...", Needs: "go"},
	{Name: "vet", Command: "go vet ./...", Needs: "go"},
	{Name: "vet-windows", Command: "go vet ./...", Needs: "go", Env: []string{"GOOS=windows", "GOARCH=amd64", "CGO_ENABLED=0"}},
	{Name: "test", Command: "go test -json -count=1 ./...", Needs: "go", Stream: true},
	{Name: "lisp", Command: "sh tools/ci/lisp-test.sh", Needs: "sbcl", File: "tools/ci/lisp-test.sh"},
}

// StepsFor returns the planned steps for a Selection.
func StepsFor(sel Selection) []Step {
	class := strings.ToLower(strings.TrimSpace(sel.Class))
	if class == "" || class == ClassFull {
		out := make([]Step, len(FullClassSteps))
		copy(out, FullClassSteps)
		return out
	}

	pkgs := "./..."
	if len(sel.Packages) > 0 {
		pkgs = strings.Join(sel.Packages, " ")
	}

	steps := []Step{
		{Name: "build", Command: "go build ./...", Needs: "go"},
		{Name: "vet", Command: "go vet " + pkgs, Needs: "go"},
	}

	if class == ClassVetWin || strings.Contains(class, "vetwin") {
		steps = append(steps, Step{
			Name:    "vet-windows",
			Command: "go vet " + pkgs,
			Needs:   "go",
			Env:     []string{"GOOS=windows", "GOARCH=amd64", "CGO_ENABLED=0"},
		})
	}

	steps = append(steps, Step{
		Name:    "test",
		Command: "go test -json -count=1 " + pkgs,
		Needs:   "go",
		Stream:  true,
	})

	if class == ClassLisp || strings.Contains(class, "lisp") {
		steps = append(steps, Step{
			Name:    "lisp",
			Command: "sh tools/ci/lisp-test.sh",
			Needs:   "sbcl",
			File:    "tools/ci/lisp-test.sh",
		})
	}

	return steps
}

// Rusage is resource accounting for a completed step's process group.
type Rusage struct {
	UserCPU   time.Duration
	SystemCPU time.Duration
	MaxRSS    int64 // memory high-water mark in bytes
}

// StepResult is the outcome of running one gate step.
type StepResult struct {
	Step     Step
	Duration time.Duration
	Rusage   Rusage
	Output   string
	Err      error
	Skipped  bool
	SkipWhy  string
}

// SdkDir is where this fleet's hand-installed toolchains live under $HOME.
const SdkDir = "sdk"

// StepUnavailable reports why a step cannot run, or empty string if it can.
// The second return value is the bin directory under ~/sdk if the program was found there.
func StepUnavailable(step Step, clone string) (why, binDir string) {
	if step.File != "" && clone != "" {
		if _, err := os.Stat(filepath.Join(clone, filepath.FromSlash(step.File))); err != nil {
			return "this checkout holds no " + step.File, ""
		}
	}
	if step.Needs != "" {
		if _, err := exec.LookPath(step.Needs); err == nil {
			return "", ""
		}
		if dir := LookInSDK(step.Needs); dir != "" {
			return "", dir
		}
		return step.Needs + " is not on this machine and is not under ~/" + SdkDir, ""
	}
	return "", ""
}

// LookInSDK checks ~/sdk/<anything>/bin/<program>.
func LookInSDK(program string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	entries, err := os.ReadDir(filepath.Join(home, SdkDir))
	if err != nil {
		return ""
	}
	found := ""
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		bin := filepath.Join(home, SdkDir, e.Name(), "bin")
		for _, name := range []string{program, program + ".exe"} {
			if info, err := os.Stat(filepath.Join(bin, name)); err == nil && !info.IsDir() {
				found = bin
				break
			}
		}
	}
	return found
}

// StepRunner executes gate steps with concurrency and rusage capture.
type StepRunner struct {
	Dir         string
	Timeout     time.Duration
	Env         []string
	OnStepStart func(step Step)
	OnStepDone  func(res StepResult)
}

// Run executes the given steps. If a "build" step is present, it runs first;
// once build succeeds (or if no build step is present), all remaining available
// steps run concurrently.
func (r *StepRunner) Run(steps []Step) []StepResult {
	results := make([]StepResult, len(steps))
	binDirs := make([]string, len(steps))

	var mu sync.Mutex
	safeStart := func(s Step) {
		if r.OnStepStart != nil {
			mu.Lock()
			defer mu.Unlock()
			r.OnStepStart(s)
		}
	}
	safeDone := func(res StepResult) {
		if r.OnStepDone != nil {
			mu.Lock()
			defer mu.Unlock()
			r.OnStepDone(res)
		}
	}

	for i, step := range steps {
		why, bin := StepUnavailable(step, r.Dir)
		if why != "" {
			results[i] = StepResult{
				Step:    step,
				Skipped: true,
				SkipWhy: why,
			}
			safeDone(results[i])
			continue
		}
		binDirs[i] = bin
	}

	buildIdx := -1
	for i, step := range steps {
		if step.Name == "build" && !results[i].Skipped {
			buildIdx = i
			break
		}
	}

	if buildIdx >= 0 {
		safeStart(steps[buildIdx])
		res := r.runStep(steps[buildIdx], binDirs[buildIdx])
		results[buildIdx] = res
		safeDone(res)
		if res.Err != nil {
			for i := range steps {
				if i != buildIdx && !results[i].Skipped {
					results[i] = StepResult{
						Step:    steps[i],
						Skipped: true,
						SkipWhy: "build step failed",
					}
					safeDone(results[i])
				}
			}
			return results
		}
	}

	var wg sync.WaitGroup
	for i, step := range steps {
		if results[i].Skipped || i == buildIdx {
			continue
		}
		wg.Add(1)
		go func(idx int, st Step, bin string) {
			defer wg.Done()
			safeStart(st)
			res := r.runStep(st, bin)
			results[idx] = res
			safeDone(res)
		}(i, step, binDirs[i])
	}
	wg.Wait()

	return results
}

// RunSelection plans steps for sel and executes them.
func (r *StepRunner) RunSelection(sel Selection) []StepResult {
	return r.Run(StepsFor(sel))
}

func (r *StepRunner) runStep(step Step, bin string) StepResult {
	start := time.Now()
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Minute
	}

	cmd := exec.Command("sh", "-c", step.Command)
	if r.Dir != "" {
		cmd.Dir = r.Dir
	}

	env := r.Env
	if env == nil {
		env = os.Environ()
	}
	if bin != "" {
		env = withStepBin(env, bin)
	}
	if len(step.Env) > 0 {
		env = append(append([]string(nil), env...), step.Env...)
	}
	cmd.Env = checkStepEnv(env)

	var buf strings.Builder
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	configureStepProcess(cmd)

	if err := cmd.Start(); err != nil {
		return StepResult{
			Step:     step,
			Duration: time.Since(start),
			Err:      err,
			Output:   err.Error(),
		}
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	var waitErr error
	select {
	case err := <-done:
		waitErr = err
	case <-timer.C:
		killStepProcess(cmd)
		<-done
		waitErr = fmt.Errorf("no answer within the %s --timeout", timeout)
	}

	dur := time.Since(start)
	ru := extractRusage(cmd.ProcessState)

	return StepResult{
		Step:     step,
		Duration: dur,
		Rusage:   ru,
		Output:   buf.String(),
		Err:      waitErr,
	}
}

func withStepBin(env []string, bin string) []string {
	if bin == "" {
		return append([]string(nil), env...)
	}
	out := make([]string, 0, len(env)+1)
	path := bin
	for _, kv := range env {
		name, value, _ := strings.Cut(kv, "=")
		if strings.EqualFold(name, "PATH") {
			if value != "" {
				path = bin + string(os.PathListSeparator) + value
			}
			continue
		}
		out = append(out, kv)
	}
	return append(out, "PATH="+path)
}

func checkStepEnv(env []string) []string {
	cleaned := goenv.Clean(env)
	out := make([]string, 0, len(cleaned)+1)
	for _, entry := range cleaned {
		name, _, ok := strings.Cut(entry, "=")
		if ok && extraStepSecret(name) {
			continue
		}
		out = append(out, entry)
	}
	return withStepSHLVL(out)
}

func withStepSHLVL(env []string) []string {
	out := make([]string, 0, len(env)+1)
	for _, entry := range env {
		name, _, _ := strings.Cut(entry, "=")
		if strings.EqualFold(name, "SHLVL") {
			continue
		}
		out = append(out, entry)
	}
	return append(out, "SHLVL=1")
}

func extraStepSecret(name string) bool {
	up := strings.ToUpper(strings.TrimSpace(name))
	return strings.Contains(up, "PASSWORD") || strings.Contains(up, "WEBHOOK")
}
