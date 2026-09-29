package tlc

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// The runner's own files. The list is explicit because go:embed cannot leave
// out the tests; TestEmbeddedSourcesAreTheNonTestFiles holds it to the
// directory.
//
//go:embed cases.go constants.go doc.go fingerprint.go jar.go outcome.go records.go run.go suite.go
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

// Fingerprint identifies the inputs a run record was measured on: every TLA+
// module, every MC configuration and the case plan under root/tla, and the
// runner that reads TLC's results. A record whose fingerprint is not the
// current one is stale. The digest is over each input's path under root, a
// NUL, its bytes and a NUL, in path order; internal/ci computes the same from
// a checkout.
func Fingerprint(root string) (string, error) {
	digest, _, err := fingerprint(root, filepath.Join(root, "tla"))
	return digest, err
}

// fingerprint is Fingerprint with the modules and configurations read from
// tlaDir, which is root/tla or a copy of it. The case plan is always read from
// root/tla, and every path is digested as tla/<name>, so a faithful copy has
// the fingerprint of its source.
//
// It also returns the bytes of the case plan that the digest covers, so a
// caller can parse the plan from exactly the bytes the digest names.
func fingerprint(root, tlaDir string) (string, []byte, error) {
	files, err := RunnerFiles()
	if err != nil {
		return "", nil, err
	}
	paths := []string{filepath.Join(root, "tla", CasesFile)}
	for _, pattern := range []string{"*.tla", "MC*.cfg"} {
		matches, err := filepath.Glob(filepath.Join(tlaDir, pattern))
		if err != nil {
			return "", nil, err
		}
		paths = append(paths, matches...)
	}
	for _, p := range paths {
		raw, err := os.ReadFile(p)
		if err != nil {
			return "", nil, fmt.Errorf("cannot read %s: %v", p, err)
		}
		files["tla/"+filepath.Base(p)] = raw
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	h := sha256.New()
	for _, name := range names {
		h.Write([]byte(name))
		h.Write([]byte{0})
		h.Write(files[name])
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil)), files["tla/"+CasesFile], nil
}
