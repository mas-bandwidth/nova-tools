//go:build functional

package ci

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// TestTheSourceSeamsAnswerAsTheDiskDoes holds the unit tier's shortcut to the
// disk: every production checker run over this repository through the shared
// tree (tree_test.go's walk, read and parse seams) returns exactly what it
// returns through filepath.WalkDir, os.ReadFile and a fresh parse. It walks
// the repository seven times more, so it is the functional tier's
// (nova-tools#4328).
func TestTheSourceSeamsAnswerAsTheDiskDoes(t *testing.T) {
	root := repoRoot(t)
	checks := map[string]func() (any, error){
		"CheckNet":           func() (any, error) { return CheckNet(root, "") },
		"CheckWaits":         func() (any, error) { return CheckWaits(root, "") },
		"CheckTestbins":      func() (any, error) { return CheckTestbins(root, "") },
		"CheckTemplates":     func() (any, error) { return CheckTemplates(root, "") },
		"CheckGoEnv":         func() (any, error) { return CheckGoEnv(root, "") },
		"FindBenchRunners":   func() (any, error) { return FindBenchRunners(root) },
		"CheckCardTemplates": func() (any, error) { return CheckCardTemplates(root, []string{"docs", "tools", "cmd", "internal"}, "") },
		"HelpBannerExamples": func() (any, error) { return HelpBannerExamples(root) },
	}
	for name, check := range checks {
		viaTree, treeErr := check()
		walk, read, parse := walkSourceDir, readSourceFile, parseSource
		walkSourceDir, readSourceFile, parseSource = filepath.WalkDir, os.ReadFile, parseSourceFile
		viaDisk, diskErr := check()
		walkSourceDir, readSourceFile, parseSource = walk, read, parse
		if (treeErr == nil) != (diskErr == nil) || !reflect.DeepEqual(viaTree, viaDisk) {
			t.Errorf("%s through the shared tree differs from the disk:\n tree %+v (%v)\n disk %+v (%v)", name, viaTree, treeErr, viaDisk, diskErr)
		}
	}
}
