//go:build functional

package ci

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"testing"

	"github.com/stretchr/testify/assert"
)

type seamCalls struct {
	walks  int
	reads  int
	parses int
}

func countedDiskSeams(calls *seamCalls) SourceSeams {
	base := diskSourceSeams()
	return SourceSeams{
		WalkDir: func(root string, fn fs.WalkDirFunc) error {
			calls.walks++
			return base.WalkDir(root, fn)
		},
		ReadFile: func(name string) ([]byte, error) {
			calls.reads++
			return base.ReadFile(name)
		},
		ParseFile: func(name string, src []byte, mode parser.Mode) (*token.FileSet, *ast.File, error) {
			calls.parses++
			return base.ParseFile(name, src, mode)
		},
	}
}

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
		"CheckNet":         func(s SourceSeams) (any, error) { return checkNetWith(root, "", s) },
		"CheckWaits":       func(s SourceSeams) (any, error) { return checkWaitsWith(root, "", s) },
		"CheckTestbins":    func(s SourceSeams) (any, error) { return checkTestbinsWith(root, "", s) },
		"CheckTemplates":   func(s SourceSeams) (any, error) { return checkTemplatesWith(root, "", s) },
		"CheckGoEnv":       func(s SourceSeams) (any, error) { return checkGoEnvWith(root, "", s) },
		"FindBenchRunners": func(s SourceSeams) (any, error) { return findBenchRunnersWith(root, s) },
		"CheckCardTemplates": func(s SourceSeams) (any, error) {
			return checkCardTemplatesWith(root, []string{"docs", "tools", "cmd", "internal"}, "", s)
		},
		"HelpBannerExamples": func(s SourceSeams) (any, error) { return helpBannerExamplesWith(root, s) },
	}
	for name, check := range checks {
		viaTree, treeErr := check(defaultSourceSeams())
		var calls seamCalls
		viaDisk, diskErr := check(countedDiskSeams(&calls))
		assert.Equal(t, diskErr == nil, treeErr == nil, "%s through the shared tree differs from the disk:\n tree %+v (%v)\n disk %+v (%v)", name, viaTree, treeErr, viaDisk, diskErr)
		assert.Equal(t, viaDisk, viaTree, "%s through the shared tree differs from the disk:\n tree %+v (%v)\n disk %+v (%v)", name, viaTree, treeErr, viaDisk, diskErr)
		assert.NotEqual(t, 0, calls.reads, "%s made no disk reads through its seam (%+v); injected seam bypass would not be caught", name, calls)
	}
}
