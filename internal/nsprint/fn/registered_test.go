package fn

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
// "ERR Function not found". The two names checked first are the table
// primitive's batch and its read, so a library that assembled nothing fails
// loudly before the walk.
func TestEveryGoFunctionNameIsRegistered(t *testing.T) {
	t.Parallel()
	src, err := Source()
	if err != nil {
		require.NoError(t, err, err)
	}
	registered := luaFunctionRegistrations(src)
	if !registered["ns_table_apply"] || !registered["ns_table_read"] {
		require.Failf(t, "assertion failed", "the library registers %d functions, not ns_table_apply and ns_table_read", len(registered))
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
					assert.True(t, registered[m[1]], "%s names %s; no Lua file registers it", path, m[1])
				}
			}
			return nil
		})
		if err != nil {
			require.NoError(t, err, err)
		}
	}
	if named == 0 {
		require.NotEqual(t, 0, named, "no Go source names an ns_* function: the walk read nothing")
	}
}

// TestRegistrationInventoryDetectsAMissingRegistration is the inventory's
// control: with the table-form ns_table_read registration disabled, the
// inventory no longer lists it, and the string-form ns_table_apply stays.
func TestRegistrationInventoryDetectsAMissingRegistration(t *testing.T) {
	t.Parallel()

	source, err := Source()
	if err != nil {
		require.NoError(t, err, err)
	}
	var readDeclaration string
	for _, match := range luaTableRegisterRx.FindAllStringSubmatch(source, -1) {
		if match[1] == "ns_table_read" {
			readDeclaration = match[0]
			break
		}
	}
	if readDeclaration == "" {
		require.NotEqual(t, "", readDeclaration, "the library has no table-form ns_table_read registration to guard")
	}
	withoutRead := strings.Replace(source, readDeclaration,
		strings.Replace(readDeclaration, "redis.register_function", "redis.not_register_function", 1), 1)
	registered := luaFunctionRegistrations(withoutRead)
	if registered["ns_table_read"] {
		require.False(t, registered["ns_table_read"], "registration inventory accepted a library with ns_table_read removed")
	}
	if !registered["ns_table_apply"] {
		require.True(t, registered["ns_table_apply"], "removing ns_table_read also hid the string-form ns_table_apply registration")
	}
}
