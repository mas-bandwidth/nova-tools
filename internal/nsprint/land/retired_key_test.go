package land

import (
	"go/ast"
	"go/parser"
	gotoken "go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestNoRetiredPRRecordRead (nova-tools#3611): the retired PR record
// s:<S>:pr:<repo>:<n> is never written (internal/nsprint/disposition/line.go,
// #3491), so no verb in the lander's path may read it. The unit records
// (s:<S>:u:<unit> through s:<S>:prunit) are the one key contract `why`,
// `land status` and `lander` read. This fails while any non-test Go file
// under cmd/nova-sprint or internal/nsprint/land spells the key: a string
// literal holding "s:%s:pr:", or ":pr:" (the "s:"+S+":pr:"+repo form).
func TestNoRetiredPRRecordRead(t *testing.T) {
	t.Parallel()

	roots := []string{filepath.Join("..", "..", "..", "cmd", "nova-sprint"), "."}
	scanned := 0
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			f, err := parser.ParseFile(gotoken.NewFileSet(), path, nil, parser.SkipObjectResolution)
			if err != nil {
				return err
			}
			scanned++
			ast.Inspect(f, func(n ast.Node) bool {
				lit, ok := n.(*ast.BasicLit)
				if !ok || lit.Kind != gotoken.STRING {
					return true
				}
				v, err := strconv.Unquote(lit.Value)
				if err != nil {
					return true
				}
				if strings.Contains(v, "s:%s:pr:") || strings.Contains(v, ":pr:") {
					t.Errorf("%s reads the retired PR record: string %s (read s:<S>:u:<unit> through land.PRUnitKey)", path, lit.Value)
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if scanned < 20 {
		t.Fatalf("scanned %d Go files under cmd/nova-sprint and internal/nsprint/land, want the whole tree", scanned)
	}
}
