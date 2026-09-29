package tlc

import "embed"

// The runner's own files. The list is explicit because go:embed cannot leave
// out the tests; TestEmbeddedSourcesAreTheNonTestFiles holds it to the
// directory.
//
//go:embed cases.go doc.go fingerprint.go inputs.go jar.go outcome.go records.go run.go suite.go
var sources embed.FS

// RunnerDir is where the runner's files live in a checkout, and the prefix
// their paths carry in the fingerprint.
const RunnerDir = "internal/tlc"

// RunnerFiles returns the runner's non-test files as they were when this
// binary was built, by their path under the checkout root.
func RunnerFiles() (map[string][]byte, error) {
	entries, err := sources.ReadDir(".")
	if err != nil {
		return nil, err
	}
	out := map[string][]byte{}
	for _, e := range entries {
		raw, err := sources.ReadFile(e.Name())
		if err != nil {
			return nil, err
		}
		out[RunnerDir+"/"+e.Name()] = raw
	}
	return out, nil
}
