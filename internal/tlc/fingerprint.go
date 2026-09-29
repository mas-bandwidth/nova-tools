package tlc

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// The runner's files that decide how a result is produced and read: the command
// line of a TLC run, its flags, its workers and its timeouts (run.go, suite.go)
// and the reading of TLC's exit status and output into pass or fail
// (outcome.go). A change to one of them can change what a record means, so they
// are inputs of every case's fingerprint. go:embed cannot take a list, so the
// directive below repeats ResultFiles and InputListFiles;
// TestEmbeddedSourcesAreTheCheckedFiles holds them together.
//
//go:embed outcome.go run.go suite.go inputs.go
var sources embed.FS

// ResultFiles are the runner's files that are inputs of every fingerprint, by
// name in RunnerDir.
var ResultFiles = []string{"outcome.go", "run.go", "suite.go"}

// InputListFiles are the bookkeeping files that decide which files a case's
// fingerprint covers (the parser of module references and the list of TLC's
// standard modules: inputs.go). They are in no fingerprint, because the digest
// is a function of the list they compute; but a binary built from another
// version of them computes another list than the checkout, so CheckRunner holds
// them to the checkout beside ResultFiles. They are also in BookkeepingFiles.
var InputListFiles = []string{"inputs.go"}

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

// CheckedFiles are the files CheckRunner holds to the checkout: ResultFiles and
// InputListFiles.
func CheckedFiles() []string {
	return append(append([]string{}, ResultFiles...), InputListFiles...)
}

// CheckedSources returns the bytes of CheckedFiles as they were when this
// binary was built, by their path under the checkout root.
func CheckedSources() (map[string][]byte, error) {
	out := map[string][]byte{}
	for _, name := range CheckedFiles() {
		raw, err := sources.ReadFile(name)
		if err != nil {
			return nil, err
		}
		out[RunnerDir+"/"+name] = raw
	}
	return out, nil
}

// CheckRunner holds the result files and the input-list files this binary was
// built from to the ones under root: a binary built from another checkout
// computes fingerprints or input lists that the checkout does not, so its
// verdict on what is stale is wrong. It returns an error naming the files that
// differ. A root that holds no internal/tlc has
// no runner to compare (a bench copy of tla/ only), and nothing is checked.
func CheckRunner(root string) error {
	dir := filepath.Join(root, filepath.FromSlash(RunnerDir))
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return nil
	}
	var differ []string
	for _, name := range CheckedFiles() {
		built, err := sources.ReadFile(name)
		if err != nil {
			return err
		}
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil || string(raw) != string(built) {
			differ = append(differ, RunnerDir+"/"+name)
		}
	}
	if len(differ) > 0 {
		return fmt.Errorf("this tlacheck was built from other runner files than the ones under %s (%s differ); build tlacheck from this tree: go build -o /tmp/tlacheck ./tools/tlacheck", root, strings.Join(differ, ", "))
	}
	return nil
}
