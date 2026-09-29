package tlc

import "embed"

// The runner's files that decide how a result is produced and read: the command
// line of a TLC run, its flags, its workers and its timeouts (run.go, suite.go)
// and the reading of TLC's exit status and output into pass or fail
// (outcome.go). A change to one of them can change what a record means, so they
// are inputs of every case's fingerprint. go:embed cannot take a list, so the
// directive below repeats ResultFiles; TestEveryRunnerFileIsClassified holds the
// two together.
//
//go:embed outcome.go run.go suite.go
var sources embed.FS

// ResultFiles are the runner's files that are inputs of every fingerprint, by
// name in RunnerDir.
var ResultFiles = []string{"outcome.go", "run.go", "suite.go"}

// BookkeepingFiles are the runner's other non-test files: the description
// (doc.go), the reading of the case plan (cases.go), the records (records.go),
// the jar and helper lookup (jar.go), the listing of a case's inputs and the
// list of TLC's standard modules (inputs.go) and this file. They decide no
// result, so a change to one of them stales no record. Every non-test file of
// the package is in exactly one of ResultFiles and BookkeepingFiles, so a new
// file cannot be left unclassified: TestEveryRunnerFileIsClassified.
var BookkeepingFiles = []string{"cases.go", "doc.go", "fingerprint.go", "inputs.go", "jar.go", "records.go"}

// RunnerDir is where the runner's files live in a checkout, and the prefix
// their paths carry in the fingerprint.
const RunnerDir = "internal/tlc"

// RunnerFiles returns the runner's result files as they were when this binary
// was built, by their path under the checkout root.
func RunnerFiles() (map[string][]byte, error) {
	out := map[string][]byte{}
	for _, name := range ResultFiles {
		raw, err := sources.ReadFile(name)
		if err != nil {
			return nil, err
		}
		out[RunnerDir+"/"+name] = raw
	}
	return out, nil
}
