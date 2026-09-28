package merge

import (
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestThreeStepsOf10SRunInUnder15S verifies that concurrent steps execute in
// parallel. It asserts the event, not the clock: each step marks itself
// started and then waits for all three marks, so under a serial runner the
// first step never sees the other two and exits 3 (bounded, about 30 s).
func TestThreeStepsOf10SRunInUnder15S(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	rendezvous := func(name string) string {
		return "touch " + dir + "/" + name + "; n=0; " +
			"while [ $(ls " + dir + " | wc -l) -lt 3 ]; do " +
			"n=$((n+1)); [ $n -gt 600 ] && exit 3; sleep 0.05; done"
	}
	runner := &StepRunner{}
	steps := []Step{
		{Name: "step1", Command: rendezvous("step1")},
		{Name: "step2", Command: rendezvous("step2")},
		{Name: "step3", Command: rendezvous("step3")},
	}

	results := runner.Run(steps)

	if len(results) != 3 {
		t.Fatalf("got %d results, want 3", len(results))
	}

	for i, res := range results {
		if res.Err != nil {
			t.Errorf("step %d (%s) failed: %v (a serial runner never meets the other two steps)", i, res.Step.Name, res.Err)
		}
		if res.Skipped {
			t.Errorf("step %d (%s) was skipped: %s", i, res.Step.Name, res.SkipWhy)
		}
	}
}

// TestBuildStepRunsFirstAndGatesConcurrentSteps asserts that a "build" step
// executes before any concurrent step, and if "build" fails, remaining steps
// are skipped without executing.
func TestBuildStepRunsFirstAndGatesConcurrentSteps(t *testing.T) {
	t.Parallel()

	t.Run("build success executes remaining steps concurrently", func(t *testing.T) {
		var buildFinished atomic.Bool
		var step1RanAfterBuild atomic.Bool
		var step2RanAfterBuild atomic.Bool

		runner := &StepRunner{
			OnStepDone: func(res StepResult) {
				if res.Step.Name == "build" {
					buildFinished.Store(true)
				} else if res.Step.Name == "step1" {
					if buildFinished.Load() {
						step1RanAfterBuild.Store(true)
					}
				} else if res.Step.Name == "step2" {
					if buildFinished.Load() {
						step2RanAfterBuild.Store(true)
					}
				}
			},
		}

		steps := []Step{
			{Name: "build", Command: "sleep 0.1"},
			{Name: "step1", Command: "sleep 0.1"},
			{Name: "step2", Command: "sleep 0.1"},
		}

		results := runner.Run(steps)
		if len(results) != 3 {
			t.Fatalf("expected 3 results, got %d", len(results))
		}
		for _, res := range results {
			if res.Err != nil {
				t.Fatalf("step %s failed: %v", res.Step.Name, res.Err)
			}
			if res.Skipped {
				t.Fatalf("step %s was skipped: %s", res.Step.Name, res.SkipWhy)
			}
		}

		if !step1RanAfterBuild.Load() || !step2RanAfterBuild.Load() {
			t.Error("steps did not run after build finished")
		}
	})

	t.Run("build failure skips remaining steps", func(t *testing.T) {
		runner := &StepRunner{}
		steps := []Step{
			{Name: "build", Command: "exit 1"},
			{Name: "step1", Command: "sleep 10"},
			{Name: "step2", Command: "sleep 10"},
		}

		results := runner.Run(steps)

		if len(results) != 3 {
			t.Fatalf("expected 3 results, got %d", len(results))
		}

		if results[0].Err == nil {
			t.Fatalf("build step should have failed")
		}

		for i := 1; i < 3; i++ {
			if !results[i].Skipped {
				t.Errorf("step %d was not skipped", i)
			}
			if !strings.Contains(results[i].SkipWhy, "build") {
				t.Errorf("step %d SkipWhy = %q, want mention of build", i, results[i].SkipWhy)
			}
		}
	})
}

// TestStepRunnerRusage verifies that rusage is captured for executed steps.
func TestStepRunnerRusage(t *testing.T) {
	t.Parallel()

	runner := &StepRunner{}
	steps := []Step{
		{Name: "test-rusage", Command: "echo hello"},
	}

	results := runner.Run(steps)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	res := results[0]
	if res.Err != nil {
		t.Fatalf("step failed: %v", res.Err)
	}
	if strings.TrimSpace(res.Output) != "hello" {
		t.Errorf("output = %q, want 'hello'", res.Output)
	}
	// MaxRSS should be >= 0
	if res.Rusage.MaxRSS < 0 {
		t.Errorf("MaxRSS is negative: %d", res.Rusage.MaxRSS)
	}
}

// TestStepsForSelection tests the step planning for different classes and package sets.
func TestStepsForSelection(t *testing.T) {
	t.Parallel()

	t.Run("class full", func(t *testing.T) {
		steps := StepsFor(Selection{Class: ClassFull})
		if len(steps) != len(FullClassSteps) {
			t.Fatalf("got %d steps, want %d", len(steps), len(FullClassSteps))
		}
		for i := range steps {
			if steps[i].Name != FullClassSteps[i].Name {
				t.Errorf("step %d name = %q, want %q", i, steps[i].Name, FullClassSteps[i].Name)
			}
		}
	})

	t.Run("class go with packages", func(t *testing.T) {
		steps := StepsFor(Selection{Class: ClassGo, Packages: []string{"./cmd/a", "./internal/b"}})
		names := make([]string, len(steps))
		for i, s := range steps {
			names[i] = s.Name
		}
		wantNames := []string{"build", "vet", "test"}
		if strings.Join(names, ",") != strings.Join(wantNames, ",") {
			t.Errorf("names = %v, want %v", names, wantNames)
		}
		if steps[1].Command != "go vet ./cmd/a ./internal/b" {
			t.Errorf("vet command = %q", steps[1].Command)
		}
		if steps[2].Command != "go test -json -count=1 ./cmd/a ./internal/b" {
			t.Errorf("test command = %q", steps[2].Command)
		}
	})

	t.Run("class vetwin", func(t *testing.T) {
		steps := StepsFor(Selection{Class: ClassVetWin, Packages: []string{"./cmd/a"}})
		names := make([]string, len(steps))
		for i, s := range steps {
			names[i] = s.Name
		}
		wantNames := []string{"build", "vet", "vet-windows", "test"}
		if strings.Join(names, ",") != strings.Join(wantNames, ",") {
			t.Errorf("names = %v, want %v", names, wantNames)
		}
		if len(steps[2].Env) != 3 {
			t.Errorf("vet-windows env = %v", steps[2].Env)
		}
	})

	t.Run("class lisp", func(t *testing.T) {
		steps := StepsFor(Selection{Class: ClassLisp})
		names := make([]string, len(steps))
		for i, s := range steps {
			names[i] = s.Name
		}
		wantNames := []string{"build", "vet", "test", "lisp"}
		if strings.Join(names, ",") != strings.Join(wantNames, ",") {
			t.Errorf("names = %v, want %v", names, wantNames)
		}
	})
}

// TestStepRunnerTimeout verifies that a step times out and kills the process group.
func TestStepRunnerTimeout(t *testing.T) {
	t.Parallel()

	runner := &StepRunner{
		Timeout: 200 * time.Millisecond,
	}
	steps := []Step{
		{Name: "hang", Command: "sleep 10"},
	}

	results := runner.Run(steps)

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if !strings.Contains(results[0].Err.Error(), "timeout") {
		t.Errorf("err = %v, want timeout mention", results[0].Err)
	}
}

// TestStepUnavailable verifies reporting for missing binaries or files.
func TestStepUnavailable(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	why, _ := StepUnavailable(Step{Name: "missing-bin", Needs: "non_existent_program_xyz_123"}, dir)
	if why == "" {
		t.Errorf("expected reason for missing program, got empty string")
	}

	whyFile, _ := StepUnavailable(Step{Name: "missing-file", File: "missing/file.sh"}, dir)
	if whyFile == "" {
		t.Errorf("expected reason for missing file, got empty string")
	}

	// Existing command like sh should be available
	whySh, _ := StepUnavailable(Step{Name: "sh-step", Needs: "sh"}, dir)
	if whySh != "" {
		t.Errorf("expected sh to be available, got %q", whySh)
	}
}
