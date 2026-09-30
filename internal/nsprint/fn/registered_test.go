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
	goFunctionNameRx    = regexp.MustCompile(`"(ns_[a-z0-9_]+)"`)
	luaStringRegisterRx = regexp.MustCompile(`redis\.register_function\s*\(\s*'(ns_[a-z0-9_]+)'`)
	luaTableRegisterRx  = regexp.MustCompile(`redis\.register_function\s*(?:\(\s*)?\{\s*function_name\s*=\s*'(ns_[a-z0-9_]+)'`)
)

func luaFunctionRegistrations(source string) map[string]bool {
	registered := map[string]bool{}
	for _, rx := range []*regexp.Regexp{luaStringRegisterRx, luaTableRegisterRx} {
		for _, match := range rx.FindAllStringSubmatch(source, -1) {
			registered[match[1]] = true
		}
	}
	return registered
}

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
	registered := luaFunctionRegistrations(src)
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

func TestStandaloneTSetProfileRegistersWriterAndReader(t *testing.T) {
	t.Parallel()

	source, err := TSetSource(TSetStandalone)
	if err != nil {
		t.Fatal(err)
	}
	registered := luaFunctionRegistrations(source)
	for _, name := range []string{"ns_tset_step", "ns_tset_read"} {
		if !registered[name] {
			t.Errorf("standalone tset profile does not declare %s", name)
		}
	}
}

func TestRegistrationInventoryDetectsMissingTSetRead(t *testing.T) {
	t.Parallel()

	source, err := TSetSource(TSetStandalone)
	if err != nil {
		t.Fatal(err)
	}
	var readDeclaration string
	for _, match := range luaTableRegisterRx.FindAllStringSubmatch(source, -1) {
		if match[1] == "ns_tset_read" {
			readDeclaration = match[0]
			break
		}
	}
	if readDeclaration == "" {
		t.Fatal("standalone profile has no table-form ns_tset_read registration to guard")
	}
	withoutRead := strings.Replace(source, readDeclaration,
		strings.Replace(readDeclaration, "redis.register_function", "redis.not_register_function", 1), 1)
	registered := luaFunctionRegistrations(withoutRead)
	if registered["ns_tset_read"] {
		t.Fatal("registration inventory accepted a standalone profile with ns_tset_read removed")
	}
	if !registered["ns_tset_step"] {
		t.Fatal("removing ns_tset_read also hid the string-form ns_tset_step registration")
	}
}
