package ci

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAllowlistedScanRulesSurfaceAWalkError pins the folded frame's walk-error
// return: runAllowlistedScan shares the one `if err != nil { return out, err }`
// with every rule, and each rule's own walk propagates the seam's error before
// the frame sees it. A walk that stumbles is surfaced by all five rules --
// waits, goenv, net, templates and testbins -- never swallowed. The five `_with`
// seams are the test door, so the branch is reached without a fixture that
// cannot exist on the disk.
func TestAllowlistedScanRulesSurfaceAWalkError(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, "internal"), 0o755), "the fixture needs a tree for the walk to enter")
	boom := errors.New("walk refused")
	seams := SourceSeams{
		WalkDir: func(string, fs.WalkDirFunc) error { return boom },
	}

	rules := map[string]func() error{
		"waits":     func() error { _, err := checkWaitsWith(root, "", seams); return err },
		"goenv":     func() error { _, err := checkGoEnvWith(root, "", seams); return err },
		"net":       func() error { _, err := checkNetWith(root, "", seams); return err },
		"templates": func() error { _, err := checkTemplatesWith(root, "", seams); return err },
		"testbins":  func() error { _, err := checkTestbinsWith(root, "", seams); return err },
	}
	for name, run := range rules {
		t.Run(name, func(t *testing.T) {
			err := run()
			require.Error(t, err, "%s swallowed the walk error", name)
			assert.ErrorIs(t, err, boom, "%s surfaced %v, want the seam's walk error", name, err)
		})
	}
}
