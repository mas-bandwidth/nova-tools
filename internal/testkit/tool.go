package testkit

import (
	"runtime"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/buildinfo"
	"github.com/stretchr/testify/require"
)

// Version is the check every tool's tests repeat whatever the tool does. It
// runs `version` and fails the test unless it exits 0 with stderr
// empty and stdout one line that internal/buildinfo parses, naming tool, a
// build, this platform and this toolchain. It returns the fields, for a
// tool's own extras.
func (m Main) Version(t testing.TB, tool string) buildinfo.Fields {
	t.Helper()
	r := m.Do(t, "version").Exit(0)
	require.Empty(t, r.Stderr, r)
	require.Regexp(t, `^[^\n]+\n$`, r.Stdout, "version is one terminated line: %s", r)
	f, ok := buildinfo.Parse(r.Stdout)
	require.True(t, ok, "version printed a line internal/buildinfo.Parse refuses: %s", r)
	require.Equal(t, tool, f.Tool, r)
	require.NotEmpty(t, f.Version, r)
	require.Equal(t, runtime.GOOS+"/"+runtime.GOARCH, f.Platform, r)
	require.Equal(t, runtime.Version(), f.GoVersion, r)
	return f
}
