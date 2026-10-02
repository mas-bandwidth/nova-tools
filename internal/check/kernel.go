package check

import (
	"fmt"
	"math"
	"os"
)

// measureKernel is the half both budgets share: the file must exist, be a
// regular file, and be non-empty. These findings are about the record, not
// about the unit the budget is denominated in, so they read the same either
// way.
//
// Lstat, not Stat: a symlinked kernel is refused as not a regular file, never
// followed, as attest refuses one (docs/SPEC.md, "kernel").
func measureKernel(file string) (size int64, failures []Failure, err error) {
	fi, statErr := os.Lstat(file)
	if statErr != nil {
		if os.IsNotExist(statErr) {
			return 0, []Failure{{file, "does not exist; a missing kernel is the worst over-budget"}}, nil
		}
		return 0, nil, statErr
	}
	if !fi.Mode().IsRegular() {
		return 0, []Failure{{file, fmt.Sprintf("not a regular file (%s); symlinks are never followed", fi.Mode().Type())}}, nil
	}
	size = fi.Size()
	if size == 0 {
		failures = append(failures, Failure{file, "empty (0 bytes); under every budget and still not a kernel"})
	}
	return size, failures, nil
}

// Kernel enforces a byte budget on one file. maxBytes must be positive; zero
// does not mean unlimited. Missing or empty files are findings, not errors.
// KernelTokens offers an estimate in tokens when the caller has measured
// bytes per token for the intended tokenizer.
func Kernel(file string, maxBytes int64) (measured int64, failures []Failure, err error) {
	if maxBytes <= 0 {
		return 0, nil, fmt.Errorf("max-bytes must be positive, got %d", maxBytes)
	}
	measured, failures, err = measureKernel(file)
	if err != nil || measured == 0 {
		return measured, failures, err
	}
	if measured > maxBytes {
		failures = append(failures, Failure{file, fmt.Sprintf(
			"over budget: %d bytes, budget %d, over by %d", measured, maxBytes, measured-maxBytes)})
	}
	return measured, failures, nil
}

// KernelTokens checks a token estimate against maxTokens. The caller measures
// bytesPerToken on representative text: divide the sample's byte count by its
// token count under the intended tokenizer. There is no default because this
// ratio varies by tokenizer, language and writing style.
//
// The estimate is ceil(bytes / bytesPerToken). Rounding up prevents the check
// from understating its own estimate. The float64-to-int64 conversion is range
// checked first: an out-of-range value can become a negative integer on some
// architectures and would incorrectly pass any positive budget. An estimate
// too large to count is reported as over budget on every architecture.
func KernelTokens(file string, maxTokens int64, bytesPerToken float64) (measured, tokens int64, failures []Failure, err error) {
	if maxTokens <= 0 {
		return 0, 0, nil, fmt.Errorf("max-tokens must be positive, got %d", maxTokens)
	}
	if bytesPerToken <= 0 || math.IsInf(bytesPerToken, 0) || math.IsNaN(bytesPerToken) {
		return 0, 0, nil, fmt.Errorf("bytes-per-token must be a positive finite ratio, got %v", bytesPerToken)
	}
	measured, failures, err = measureKernel(file)
	if err != nil || measured == 0 {
		return measured, 0, failures, err
	}
	// float64(math.MaxInt64) rounds UP to 2^63, one past the largest int64, so the
	// boundary is >= rather than >: an estimate of exactly 2^63 is already out of range.
	// +Inf (a denormal divisor) and every larger finite estimate take the same branch.
	estimate := math.Ceil(float64(measured) / bytesPerToken)
	if estimate >= float64(math.MaxInt64) {
		return measured, 0, append(failures, Failure{file, fmt.Sprintf(
			"over budget: the divisor derives more tokens than can be counted (measured %d bytes at %g bytes/token, budget %d)",
			measured, bytesPerToken, maxTokens)}), nil
	}
	tokens = int64(estimate)
	if tokens > maxTokens {
		failures = append(failures, Failure{file, fmt.Sprintf(
			"over budget: %d tokens, budget %d, over by %d (measured %d bytes at %g bytes/token)",
			tokens, maxTokens, tokens-maxTokens, measured, bytesPerToken)})
	}
	return measured, tokens, failures, nil
}
