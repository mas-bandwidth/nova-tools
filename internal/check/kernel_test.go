package check

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestKernel(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		create   bool
		max      int64
		wantFail []string // substrings; empty = must pass
	}{
		{name: "under budget", content: "small kernel", create: true, max: 100},
		{name: "exactly at budget passes", content: "12345", create: true, max: 5},
		{name: "over budget fails with the measured number", content: "123456789", create: true, max: 5,
			wantFail: []string{"over budget: 9 bytes, budget 5, over by 4"}},
		{name: "missing kernel fails", create: false, max: 100,
			wantFail: []string{"does not exist"}},
		{name: "empty kernel fails despite being under budget", content: "", create: true, max: 100,
			wantFail: []string{"empty"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			file := filepath.Join(dir, "KERNEL.md")
			if tt.create {
				writeMode(t, dir, "KERNEL.md", tt.content, 0o644)
			}
			measured, failures, err := Kernel(file, tt.max)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			wantFailures(t, failures, tt.wantFail)
			if tt.create && measured != int64(len(tt.content)) {
				t.Errorf("measured = %d, want %d", measured, len(tt.content))
			}
		})
	}
}

// Reviewer suggestion: kernel used Stat while attest used Lstat. Unified on
// Lstat-and-refuse: a symlinked kernel is not a regular file, never followed.
func TestKernelRefusesSymlink(t *testing.T) {
	dir := t.TempDir()
	writeMode(t, dir, "real.md", "the kernel", 0o644)
	link := filepath.Join(dir, "KERNEL.md")
	if err := os.Symlink(filepath.Join(dir, "real.md"), link); err != nil {
		t.Skipf("cannot create symlink: %v", err)
	}
	measured, failures, err := Kernel(link, 100)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	wantFailures(t, failures, []string{"not a regular file", "symlink"})
	if measured != 0 {
		t.Errorf("measured = %d, want 0: a symlink must not be measured", measured)
	}
}

func TestKernelRefusesNonPositiveBudget(t *testing.T) {
	dir := t.TempDir()
	writeMode(t, dir, "KERNEL.md", "k", 0o644)
	for _, budget := range []int64{0, -1} {
		if _, _, err := Kernel(filepath.Join(dir, "KERNEL.md"), budget); err == nil {
			t.Errorf("budget %d should be refused, not treated as unlimited", budget)
		}
	}
}

// The token budget: what a context window actually spends. The divisor is the
// caller's own measurement, and the derivation rounds up so a kernel one token
// over can never read as exactly at budget.
func TestKernelTokens(t *testing.T) {
	tests := []struct {
		name       string
		content    string
		create     bool
		max        int64
		divisor    float64
		wantTokens int64
		wantFail   []string // substrings; empty = must pass
	}{
		{name: "under budget", content: "0123456789", create: true, max: 100, divisor: 2.4, wantTokens: 5},
		{name: "exactly at budget passes", content: strings.Repeat("a", 240), create: true, max: 100, divisor: 2.4, wantTokens: 100},
		{name: "the derivation rounds up", content: strings.Repeat("a", 241), create: true, max: 200, divisor: 2.4, wantTokens: 101},
		{name: "over budget fails in tokens, with the bytes and the divisor", content: strings.Repeat("a", 241), create: true, max: 100, divisor: 2.4, wantTokens: 101,
			wantFail: []string{"over budget: 101 tokens, budget 100, over by 1", "measured 241 bytes at 2.4 bytes/token"}},
		// F5-4: a divisor small enough to derive more tokens than an int64 can
		// hold. Go leaves the out-of-range float-to-int conversion to the
		// hardware -- arm64 saturates to MaxInt64 and amd64 wraps to MinInt64,
		// which compared as UNDER budget and exited 0 on three of the five
		// shipped targets. The row asserts the FAILURE and a countable token
		// number, never the saturated one, so it is the same red on every GOARCH.
		{name: "a divisor too small to count fails rather than wrapping", content: strings.Repeat("a", 241), create: true, max: 100, divisor: 1e-20, wantTokens: 0,
			wantFail: []string{"over budget", "more tokens than can be counted", "measured 241 bytes at 1e-20 bytes/token"}},
		{name: "missing kernel fails", create: false, max: 100, divisor: 2.4,
			wantFail: []string{"does not exist"}},
		{name: "empty kernel fails despite being under budget", content: "", create: true, max: 100, divisor: 2.4,
			wantFail: []string{"empty"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			file := filepath.Join(dir, "KERNEL.md")
			if tt.create {
				writeMode(t, dir, "KERNEL.md", tt.content, 0o644)
			}
			measured, tokens, failures, err := KernelTokens(file, tt.max, tt.divisor)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			wantFailures(t, failures, tt.wantFail)
			if tokens != tt.wantTokens {
				t.Errorf("tokens = %d, want %d", tokens, tt.wantTokens)
			}
			if tt.create && measured != int64(len(tt.content)) {
				t.Errorf("measured = %d bytes, want %d", measured, len(tt.content))
			}
		})
	}
}

// A guessed divisor would make the whole answer a guess while still looking
// like an instrument, so every unusable ratio is refused rather than
// substituted.
func TestKernelTokensRefusesUnusableInputs(t *testing.T) {
	dir := t.TempDir()
	writeMode(t, dir, "KERNEL.md", "kernel", 0o644)
	file := filepath.Join(dir, "KERNEL.md")
	for _, budget := range []int64{0, -1} {
		if _, _, _, err := KernelTokens(file, budget, 2.4); err == nil {
			t.Errorf("token budget %d should be refused, not treated as unlimited", budget)
		}
	}
	for _, d := range []float64{0, -2.4, math.Inf(1), math.NaN()} {
		if _, _, _, err := KernelTokens(file, 100, d); err == nil {
			t.Errorf("divisor %v should be refused; a ratio that is not a positive finite number is not a measurement", d)
		}
	}
}

func TestKernelTokensRefusesSymlink(t *testing.T) {
	dir := t.TempDir()
	writeMode(t, dir, "real.md", "the kernel", 0o644)
	link := filepath.Join(dir, "KERNEL.md")
	if err := os.Symlink(filepath.Join(dir, "real.md"), link); err != nil {
		t.Skipf("cannot create symlink: %v", err)
	}
	measured, tokens, failures, err := KernelTokens(link, 100, 2.4)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	wantFailures(t, failures, []string{"not a regular file", "symlink"})
	if measured != 0 || tokens != 0 {
		t.Errorf("measured = %d bytes / %d tokens, want 0/0: a symlink must not be measured", measured, tokens)
	}
}

// TestKernelTokensNeverReportsFewerTokensThanItsEstimate is the invariant the
// derivation's own comment states -- "A size check must never report fewer
// tokens than its own estimate" -- held at the edge where the estimate stops
// being representable. The conversion at the heart of it is the one Go leaves
// implementation-defined, so this asserts the helper's ANSWER (a failure, and a
// token count that is not negative) rather than either platform's rounding, and
// is therefore the same test on amd64, arm64 and any future GOARCH.
func TestKernelTokensNeverReportsFewerTokensThanItsEstimate(t *testing.T) {
	dir := t.TempDir()
	writeMode(t, dir, "KERNEL.md", strings.Repeat("a", 241), 0o644)
	file := filepath.Join(dir, "KERNEL.md")
	// Each divisor derives an estimate above math.MaxInt64 from 241 bytes:
	// 2.41e21, 2.41e23, and a denormal one whose quotient is +Inf.
	for _, divisor := range []float64{1e-20, 1e-22, math.SmallestNonzeroFloat64} {
		measured, tokens, failures, err := KernelTokens(file, 400, divisor)
		if err != nil {
			t.Fatalf("divisor %g: unexpected error: %v", divisor, err)
		}
		if len(failures) == 0 {
			t.Errorf("divisor %g: an uncountable estimate passed the budget; tokens = %d", divisor, tokens)
		}
		if tokens < 0 {
			t.Errorf("divisor %g: tokens = %d, a negative count is not an estimate", divisor, tokens)
		}
		if measured != 241 {
			t.Errorf("divisor %g: measured = %d bytes, want 241", divisor, measured)
		}
	}
}

// TestTheTokenEstimateBoundaryIsExact witnesses the exact 2^63 boundary the
// conversion gate enforces, and the largest representable float64 below it that
// is still a countable int64. The repair lives in KernelTokens: it refuses when
// estimate >= float64(math.MaxInt64), because that constant rounds UP to 2^63
// -- one past the largest int64 -- and it treats an uncountable estimate as
// over budget. Each row states the estimate as a float64 and the expected
// outcome. For a countable estimate (and for 2^63 itself) the divisor is chosen
// so the measured bytes derive exactly that estimate -- divisor = measured /
// estimate -- which drives KernelTokens rather than any one platform's
// float-to-int conversion. The assertions are exact int64 values, so the test
// is the same on amd64, arm64 and any future GOARCH.
func TestTheTokenEstimateBoundaryIsExact(t *testing.T) {
	two63 := math.Ldexp(1, 63)            // == float64(math.MaxInt64): rounds UP to one past MaxInt64
	justBelow := math.Nextafter(two63, 0) // 2^63 - 1024 == 9223372036854774784, the largest float64 below 2^63

	tests := []struct {
		name       string
		estimate   float64 // the estimate this row witnesses
		measured   int     // bytes written to the file, so the estimate is derived, not injected
		divisor    float64 // bytes-per-token; used directly unless derived is set
		derived    bool    // divisor = measured/estimate, to realise the estimate exactly
		maxTokens  int64
		wantTokens int64
		wantErr    bool
		wantFail   []string
	}{
		{
			name:      "exactly 2^63 is refused as uncountable",
			estimate:  two63,
			measured:  1,
			derived:   true,
			maxTokens: 400,
			wantFail:  []string{"more tokens than can be counted"},
		},
		{
			name:       "the largest float64 below 2^63 (2^63-1024) converts exactly and passes at that exact budget",
			estimate:   justBelow,
			measured:   3,
			derived:    true,
			maxTokens:  9223372036854774784,
			wantTokens: 9223372036854774784,
		},
		{
			name:       "2^62 converts exactly and passes at that exact budget",
			estimate:   math.Ldexp(1, 62),
			measured:   1,
			derived:    true,
			maxTokens:  4611686018427387904,
			wantTokens: 4611686018427387904,
		},
		{
			name:       "a small ordinary estimate converts exactly and passes at that exact budget",
			estimate:   400.0,
			measured:   400,
			derived:    true,
			maxTokens:  400,
			wantTokens: 400,
		},
		{
			name:      "+Inf estimate is refused as uncountable (a denormal divisor derives it)",
			estimate:  math.Inf(1),
			measured:  1,
			divisor:   math.SmallestNonzeroFloat64,
			maxTokens: 400,
			wantFail:  []string{"more tokens than can be counted"},
		},
		{
			// The helper cannot receive a NaN estimate: the caller validates the
			// divisor before any division, and a finite positive divisor over a
			// non-negative byte count can never yield NaN. The caller's path is
			// the one to assert: a NaN divisor is refused before the estimate
			// exists.
			name:      "NaN estimate is unreachable; the caller refuses a NaN divisor first",
			estimate:  math.NaN(),
			measured:  1,
			divisor:   math.NaN(),
			maxTokens: 400,
			wantErr:   true,
		},
		{
			// Current contract: a negative estimate cannot arise. The divisor is
			// validated positive and the measured byte count is never negative,
			// so ceil(measured/divisor) is never negative. The only input that
			// could lead there -- a negative divisor -- is refused by the caller
			// before the estimate is formed, so there is nothing for the gate to
			// refuse and no kernel.go change is warranted.
			name:      "negative estimate is unreachable; the caller refuses a negative divisor first",
			estimate:  -1.0,
			measured:  1,
			divisor:   -1.0,
			maxTokens: 400,
			wantErr:   true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			writeMode(t, dir, "KERNEL.md", strings.Repeat("x", tt.measured), 0o644)
			file := filepath.Join(dir, "KERNEL.md")

			divisor := tt.divisor
			if tt.derived {
				divisor = float64(tt.measured) / tt.estimate
			}

			measured, tokens, failures, err := KernelTokens(file, tt.maxTokens, divisor)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("estimate %g (divisor %g): expected an error, got tokens=%d", tt.estimate, divisor, tokens)
				}
				return
			}
			if err != nil {
				t.Fatalf("estimate %g (divisor %g): unexpected error: %v", tt.estimate, divisor, err)
			}
			wantFailures(t, failures, tt.wantFail)
			if tokens != tt.wantTokens {
				t.Errorf("estimate %g (divisor %g): tokens = %d, want %d", tt.estimate, divisor, tokens, tt.wantTokens)
			}
			if measured != int64(tt.measured) {
				t.Errorf("estimate %g: measured = %d bytes, want %d", tt.estimate, measured, tt.measured)
			}
		})
	}
}
