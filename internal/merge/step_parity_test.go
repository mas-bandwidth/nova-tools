package merge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestFullClassParity verifies that FullClassSteps matches .github/workflows/ci.yml's
// bench legs byte for byte (SPEC-MERGE parity).
func TestFullClassParity(t *testing.T) {
	t.Parallel()
	verifyFullClassParity(t)
}

func TestFullClassParityEqualsCIWorkflowsBenchLegs(t *testing.T) {
	t.Parallel()
	verifyFullClassParity(t)
}

func ciStepRun(ciContent, stepName string) string {
	lines := strings.Split(ciContent, "\n")
	inStep := false
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "- name: "+stepName) {
			inStep = true
			continue
		}
		if inStep {
			if strings.HasPrefix(trimmed, "- name:") {
				break
			}
			if strings.HasPrefix(trimmed, "run:") {
				run := strings.TrimSpace(strings.TrimPrefix(trimmed, "run:"))
				if run != "|" {
					return run
				}
				// A block scalar (`run: |`, the unit-tier test step since
				// #4345): the step's command is its indented lines, joined.
				indent := len(line) - len(strings.TrimLeft(line, " "))
				var block []string
				for _, next := range lines[i+1:] {
					if strings.TrimSpace(next) != "" && len(next)-len(strings.TrimLeft(next, " ")) <= indent {
						break
					}
					block = append(block, strings.TrimSpace(next))
				}
				return strings.TrimSpace(strings.Join(block, "\n"))
			}
		}
	}
	return ""
}

func verifyFullClassParity(t *testing.T) {
	t.Helper()

	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}

	ciPath := filepath.Join(root, ".github", "workflows", "ci.yml")
	ciBytes, err := os.ReadFile(ciPath)
	if err != nil {
		t.Fatalf("could not read %s: %v", ciPath, err)
	}
	ciContent := string(ciBytes)

	makePath := filepath.Join(root, "Makefile")
	makeBytes, err := os.ReadFile(makePath)
	if err != nil {
		t.Fatalf("could not read %s: %v", makePath, err)
	}
	makeContent := string(makeBytes)

	// Expected bench legs from ci.yml:
	// 1. build (in lint job, runs `make build`)
	// 2. vet (in lint job, runs `make vet`)
	// 3. vet-windows (in lint job, runs `make vet-windows`)
	// 4. test (in test / test-packages jobs, runs `make test PKGS=...`)
	// 5. lisp (in lisp job, runs `make test-lisp`)

	if run := ciStepRun(ciContent, "build"); run != "make build" {
		t.Errorf("ci.yml step 'build' run = %q, want 'make build'", run)
	}
	if run := ciStepRun(ciContent, "vet"); run != "make vet" {
		t.Errorf("ci.yml step 'vet' run = %q, want 'make vet'", run)
	}
	if run := ciStepRun(ciContent, "vet-windows"); run != "make vet-windows" {
		t.Errorf("ci.yml step 'vet-windows' run = %q, want 'make vet-windows'", run)
	}
	if run := ciStepRun(ciContent, "test"); !strings.Contains(run, "make test") {
		t.Errorf("ci.yml step 'test' run = %q, want it to contain 'make test'", run)
	}
	if run := ciStepRun(ciContent, "nova-work acceptance"); run != "make test-lisp" {
		t.Errorf("ci.yml step 'nova-work acceptance' run = %q, want 'make test-lisp'", run)
	}

	// Verify Makefile recipes correspond to FullClassSteps commands
	if !strings.Contains(makeContent, "build:\n\t$(GO) build ./...") {
		t.Errorf("Makefile build recipe does not run 'go build ./...'")
	}
	if !strings.Contains(makeContent, "vet:\n\t$(GO) vet $(PKGS)") {
		t.Errorf("Makefile vet recipe does not run 'go vet'")
	}
	if !strings.Contains(makeContent, "GOOS=windows GOARCH=amd64 CGO_ENABLED=0 $(GO) vet ./...") {
		t.Errorf("Makefile vet-windows recipe does not match expected cross-vet command")
	}

	expectedSteps := []struct {
		name    string
		command string
		needs   string
		file    string
		stream  bool
		env     []string
	}{
		{
			name:    "build",
			command: "go build ./...",
			needs:   "go",
		},
		{
			name:    "vet",
			command: "go vet ./...",
			needs:   "go",
		},
		{
			name:    "vet-windows",
			command: "go vet ./...",
			needs:   "go",
			env:     []string{"GOOS=windows", "GOARCH=amd64", "CGO_ENABLED=0"},
		},
		{
			name:    "test",
			command: "go test -json -count=1 ./...",
			needs:   "go",
			stream:  true,
		},
		{
			name:    "lisp",
			command: "sh tools/ci/lisp-test.sh",
			needs:   "sbcl",
			file:    "tools/ci/lisp-test.sh",
		},
	}

	if len(FullClassSteps) != len(expectedSteps) {
		t.Fatalf("FullClassSteps has %d steps, want %d", len(FullClassSteps), len(expectedSteps))
	}

	for i, exp := range expectedSteps {
		got := FullClassSteps[i]
		if got.Name != exp.name {
			t.Errorf("step %d name = %q, want %q", i, got.Name, exp.name)
		}
		if got.Command != exp.command {
			t.Errorf("step %d command = %q, want %q", i, got.Command, exp.command)
		}
		if got.Needs != exp.needs {
			t.Errorf("step %d needs = %q, want %q", i, got.Needs, exp.needs)
		}
		if got.File != exp.file {
			t.Errorf("step %d file = %q, want %q", i, got.File, exp.file)
		}
		if got.Stream != exp.stream {
			t.Errorf("step %d stream = %v, want %v", i, got.Stream, exp.stream)
		}
		if len(got.Env) != len(exp.env) {
			t.Errorf("step %d env len = %d, want %d", i, len(got.Env), len(exp.env))
		} else {
			for j := range got.Env {
				if got.Env[j] != exp.env[j] {
					t.Errorf("step %d env[%d] = %q, want %q", i, j, got.Env[j], exp.env[j])
				}
			}
		}
	}
}
