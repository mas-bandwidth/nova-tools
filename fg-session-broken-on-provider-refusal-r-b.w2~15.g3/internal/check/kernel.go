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
// followed — the same posture as attest. See SPEC.md.
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

// Kernel enforces a BYTE budget on the kernel file. maxBytes must be
// positive; there is no default budget and zero does not mean unlimited.
// A missing kernel and an empty kernel are both failures, not errors:
// the check ran, and the answer is NO.
//
// Bytes are a proxy. What a context window actually spends is tokens, and the
// bytes-per-token ratio is a property of the writing, not of the format — see
// KernelTokens for the honest denomination.
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

// KernelTokens enforces a TOKEN budget, derived from the measured bytes and a
// divisor the caller states.
//
// The divisor is not a constant this tool can know: bytes per token varies by
// tokenizer, by language, and by how a given writer writes. It is a
// MEASUREMENT the caller makes on their own text — count the tokens of a
// representative sample with the tokenizer that will actually read the
// kernel, divide by its bytes, and pass that number. There is deliberately no
// default: a guessed divisor would make the whole answer a guess while
// looking like an instrument.
//
// The derivation rounds UP (ceiling). A size check must never report fewer
// tokens than its own estimate, and rounding down would let a kernel sit one
// token over budget and read as exactly at it.
//
// AND IT MUST NOT REPORT A NUMBER THE CONVERSION INVENTED. The estimate is a
// float64 and the count is an int64, and Go leaves an out-of-range conversion
// between them to the hardware: arm64 saturates to MaxInt64, amd64 yields the
// integer-indefinite value MinInt64 -- which is less than every budget, so a
// divisor of 1e-20 printed `KERNEL OK tokens=-9223372036854775808 budget=400`
// and exited 0 on three of the five shipped targets while the other two failed
// on the same file. The divisor is a hand-typed measurement and `1e-20` is a
// scientific-notation typo away from `1e20`, so the range is checked BEFORE the
// conversion and an estimate that cannot be counted is over budget, said in
// those words. The gate now gives the same verdict on every GOARCH.
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
