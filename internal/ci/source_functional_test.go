//go:build functional

package ci

import (
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
	t.Parallel()
	root := repoRoot(t)
	checks := map[string]func(SourceSeams) (any, error){
		"CheckNet":         func(s SourceSeams) (any, error) { return CheckNetWith(s, root, "") },
		"CheckWaits":       func(s SourceSeams) (any, error) { return CheckWaitsWith(s, root, "") },
		"CheckTestbins":    func(s SourceSeams) (any, error) { return CheckTestbinsWith(s, root, "") },
		"CheckTemplates":   func(s SourceSeams) (any, error) { return CheckTemplatesWith(s, root, "") },
		"CheckGoEnv":       func(s SourceSeams) (any, error) { return CheckGoEnvWith(s, root, "") },
		"FindBenchRunners": func(s SourceSeams) (any, error) { return FindBenchRunnersWith(s, root) },
		"CheckCardTemplates": func(s SourceSeams) (any, error) {
			return CheckCardTemplatesWith(s, root, []string{"docs", "tools", "cmd", "internal"}, "")
		},
		"HelpBannerExamples": func(s SourceSeams) (any, error) { return HelpBannerExamplesWith(s, root) },
	}
	for name, check := range checks {
		viaTree, treeErr := check(defaultSourceSeams())
		viaDisk, diskErr := check(DiskSourceSeams())
		if (treeErr == nil) != (diskErr == nil) || !reflect.DeepEqual(viaTree, viaDisk) {
			t.Errorf("%s through the shared tree differs from the disk:\n tree %+v (%v)\n disk %+v (%v)", name, viaTree, treeErr, viaDisk, diskErr)
		}
	}
}
