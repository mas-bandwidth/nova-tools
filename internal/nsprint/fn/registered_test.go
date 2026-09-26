package fn

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var (
	goFunctionNameRx = regexp.MustCompile(`"(ns_[a-z0-9_]+)"`)
	luaRegisterRx    = regexp.MustCompile(`register_function\(\s*'(ns_[a-z0-9_]+)'|function_name = '(ns_[a-z0-9_]+)'`)
)

// TestEveryGoFunctionNameIsRegistered is the #4405 fix round's item 5
// (#4322): every ns_* function name a Go source outside the tests names
// (an FCALL's constant) is registered by the library, so no call reaches
// "ERR Function not found". The cold read named ns_task_push
// (internal/nsprint/task/push.go) as unregistered; task_claim.lua
// registers it, and this test holds it so for every name.
func TestEveryGoFunctionNameIsRegistered(t *testing.T) {
	t.Parallel()
	src, err := Source()
	if err != nil {
		t.Fatal(err)
	}
	registered := map[string]bool{}
	for _, m := range luaRegisterRx.FindAllStringSubmatch(src, -1) {
		registered[m[1]+m[2]] = true
	}
	if !registered["ns_task_push"] || !registered["ns_ws_paths_repair"] {
		t.Fatalf("the library registers %d functions, not ns_task_push and ns_ws_paths_repair", len(registered))
	}
	root := filepath.Join("..", "..", "..")
	named := 0
	for _, dir := range []string{"cmd", "internal"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return err
			}
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for _, m := range goFunctionNameRx.FindAllStringSubmatch(string(b), -1) {
				named++
				if !registered[m[1]] {
					t.Errorf("%s names %s; no Lua file registers it", path, m[1])
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if named == 0 {
		t.Fatal("no Go source names an ns_* function: the walk read nothing")
	}
}
