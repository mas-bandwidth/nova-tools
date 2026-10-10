package update

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestPinReadsTheVersionTokenTheInstalledReaderTakes pins the pin read at the
// help's own `local:go version` sample: the second word of `go version ...` is
// the word `version`, so a pin that takes it answers a green that is not true.
// A pin keeps the second token whole when it is a version (`v0.12.0`, a
// pseudo-version, `devel` or a bare commit), and otherwise takes the version
// token the installed reader's ladder takes; a line with no version token is
// refused, never counted current.
func TestPinReadsTheVersionTokenTheInstalledReaderTakes(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ line, want string }{
		{"go version go1.27.1 darwin/arm64", "1.27.1"},
		{"nova-wake v0.12.0 darwin/arm64", "v0.12.0"},
		{"nova-wake v0.12.1-0.20260912135226-0459069 darwin/arm64", "v0.12.1-0.20260912135226-0459069"},
		{"nova-wake devel darwin/arm64 go1.27.1", "devel"},
		{"nova-merge 0459069", "0459069"},
	} {
		r := identity(Entry{Kind: "pin"}, tc.line, false)
		require.Truef(t, r.Known(), "%q: %+v", tc.line, r)
		require.Equalf(t, tc.want, r.Version, "%q: %+v", tc.line, r)
	}
	r := identity(Entry{Kind: "pin"}, "no version on this line", false)
	require.False(t, r.Known(), r)
	require.Equal(t, "no_version", r.Reason, r)
}

// TestPinThroughTheHelpsLocalSampleReportsTheVersion runs the help's own
// `local:go version` pattern as a pin's latest, through the two real reads, and
// requires the STATUS line to carry the version both sides agree on, never the
// second word `version`.
func TestPinThroughTheHelpsLocalSampleReportsTheVersion(t *testing.T) {
	t.Parallel()

	same := printer(t, "go version go1.27.1 darwin/arm64")
	p := manifest(t, row("go", "pin", same, "local:"+same, "none"))
	c, out, errs := run(t, Environment{}, "status", "--file", p)
	require.EqualValuesf(t, 0, c, "%s\n%s", out, errs)
	need(t, out, "installed=1.27.1 latest=1.27.1")
	require.NotContainsf(t, out+errs, "installed=version", "the pin took the line's second word:\n%s\n%s", out, errs)
}
