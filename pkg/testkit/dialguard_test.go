package testkit

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestUnitTierDials reads what it refuses: the scan names a dialer the unit
// tier holds and passes over the same line behind the functional tag, a
// miniredis inside a trap, and a name that only looks like a dialer.
func TestUnitTierDials(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, src string
		want      []string
	}{
		{"a dial in the unit tier", "package p\nimport \"net\"\nfunc f() { net.Dial(\"tcp\", \"x\") }\n", []string{"a_test.go:3: net.Dial"}},
		{"a listener in the unit tier", "package p\nimport \"net\"\nfunc f() { net.Listen(\"tcp\", \"x\") }\n", []string{"a_test.go:3: net.Listen"}},
		{"an httptest server", "package p\nimport \"net/http/httptest\"\nfunc f() { httptest.NewServer(nil) }\n", []string{"a_test.go:3: httptest.NewServer"}},
		{"a store opener", "package p\nimport \"github.com/mas-bandwidth/nova-tools/pkg/redisconn\"\nfunc f() { redisconn.Open(nil, redisconn.Options{}, nil) }\n", []string{"a_test.go:3: redisconn.Open"}},
		{"a renamed import", "package p\nimport n \"net\"\nvar d n.Dialer\n", []string{"a_test.go:3: n.Dialer"}},
		{"a miniredis outside a trap", "package p\nimport \"github.com/alicebob/miniredis/v2\"\nfunc f() { miniredis.RunT(nil) }\n", []string{"a_test.go:3: miniredis.RunT"}},
		{"a miniredis inside a trap", "package p\nimport \"github.com/alicebob/miniredis/v2\"\nfunc pipeStore() { miniredis.RunT(nil) }\n", nil},
		{"the functional tier", "//go:build functional\n\npackage p\nimport \"net\"\nfunc f() { net.Dial(\"tcp\", \"x\") }\n", nil},
		{"a name that is not a dialer", "package p\nimport \"net\"\nfunc f() { net.SplitHostPort(\"x:1\") }\n", nil},
		{"a selector on a call", "package p\nimport \"net\"\nfunc f() { foo().Dial(\"x\") }\n", nil},
		{"a field on a variable", "package p\nfunc f() { var x T; x.Dial() }\n", nil},
		{"a leading comment", "// suite\npackage p\n", nil},
		{"a malformed build tag", "//go:build (\npackage p\n", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(dir, "a_test.go"), []byte(c.src), 0o600))
			found, err := UnitTierDials(dir, map[string]bool{"pipeStore": true})
			require.NoError(t, err)
			assert.Equal(t, c.want, found)
		})
	}
}

// TestUnitTierDialsErrorPaths reports the scan's failures: a file it cannot
// read and source that does not parse. Both must surface as errors, not
// silence.
func TestUnitTierDialsErrorPaths(t *testing.T) {
	t.Parallel()
	t.Run("an unreadable file", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.Mkdir(filepath.Join(dir, "a_test.go"), 0o700))
		_, err := UnitTierDials(dir, nil)
		require.Error(t, err)
	})
	t.Run("source that does not parse", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "a_test.go"), []byte("package p\nfunc {\n"), 0o600))
		_, err := UnitTierDials(dir, nil)
		require.Error(t, err)
	})
	t.Run("a file with no package clause", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "a_test.go"), []byte("// nothing here\n"), 0o600))
		_, err := UnitTierDials(dir, nil)
		require.Error(t, err)
	})
}
