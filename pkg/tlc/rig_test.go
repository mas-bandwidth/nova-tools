package tlc

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// The rig of pkg/tlc (STANDARD.md section 8): one constructor and the
// mechanics the package's test shapes share, over testify's require so setup
// stays one line. The checkout tree, the source view and the suite options stay
// beside their units (tree in cases_test.go, testSource in inputs_test.go,
// suiteOptions in suite_test.go) until their shrink cards move them here.

// parseDuration is time.ParseDuration, named so a caller's table rows name the
// field they carry.
func parseDuration(s string) (time.Duration, error) { return time.ParseDuration(s) }

// rig is one test's rig: a temporary root to write fixtures under.
type rig struct {
	t    *testing.T
	root string
}

// newRig is a rig over a fresh temporary directory.
func newRig(t *testing.T) *rig {
	t.Helper()
	return &rig{t: t, root: t.TempDir()}
}

// jar writes a jar named name whose bytes are its name, the stand-in for the
// real jar a test never opens, and returns the file's path and its sha256, the
// digest a found Jar records.
func (r *rig) jar(name string) (string, string) {
	r.t.Helper()
	path := filepath.Join(r.root, name)
	require.NoError(r.t, os.WriteFile(path, []byte(name), 0o644))
	sum := sha256.Sum256([]byte(name))
	return path, hex.EncodeToString(sum[:])
}
