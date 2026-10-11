package buildinfo_test

import (
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/buildinfo"
)

// TestParseRefusesExtraPlatformSeparators holds field three of the grammar to
// what docs/SPEC.md spells once -- <goos>/<goarch>, a pair -- rather than to
// "contains a slash with nonempty halves". The hurt: a line like
// `nova-example v1 linux/amd64/extra go1.27.1` or `nova-example v1 linux//amd64
// go1.27.1` returned ok=true, and consumers that trust ok -- nova-update's
// snapshot records f.Platform, fleet reads the same field through its
// validPlatform, which already demands exactly one separator with nonempty
// components -- disagreed with the shared parser about what a version line is.
// The parser refuses the excess now; the components stay opaque and nonempty,
// with no fixed allowlist, so valid cross-platform lines still parse.
func TestParseRefusesExtraPlatformSeparators(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ name, line string }{
		{"extra separator: a third segment after the pair", "nova-example v1 linux/amd64/extra go1.27.1"},
		{"repeated separator: an empty segment between two slashes", "nova-example v1 linux//amd64 go1.27.1"},
		{"repeated separator mid-token: several excess slashes", "nova-example v1 linux///amd64//x go1.27.1"},
		{"trailing separator: an empty goarch", "nova-example v1 linux/amd64/ go1.27.1"},
		{"leading separator: an empty goos", "nova-example v1 /amd64 go1.27.1"},
		{"separator alone: both components empty", "nova-example v1 / go1.27.1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := buildinfo.Parse(tc.line)
			assert.False(t, ok, "Parse accepted a platform that is not one <goos>/<goarch> pair: %q -> %+v", tc.line, got)
		})
	}

	const stamp = "v0.15.3-0.20260918044559-d576bf6bbabb"
	for _, tc := range []struct {
		name string
		line string
		want buildinfo.Fields
	}{
		{
			name: "one separator: the canonical linux pair",
			line: "nova-example " + stamp + " linux/amd64 go1.27.1",
			want: buildinfo.Fields{Tool: "nova-example", Version: stamp, Platform: "linux/amd64", GoVersion: "go1.27.1"},
		},
		{
			name: "one separator: a valid cross-platform pair (windows binary read on linux)",
			line: "nova-example " + stamp + " windows/arm64 go1.27.1",
			want: buildinfo.Fields{Tool: "nova-example", Version: stamp, Platform: "windows/arm64", GoVersion: "go1.27.1"},
		},
		{
			name: "unknown but nonempty components stay opaque: no fixed allowlist",
			line: "nova-example " + stamp + " plan9/mips64 go1.27.1",
			want: buildinfo.Fields{Tool: "nova-example", Version: stamp, Platform: "plan9/mips64", GoVersion: "go1.27.1"},
		},
		{
			name: "arbitrary nonempty punctuation in the components is still one pair",
			line: "nova-example " + stamp + " custom.os_64/custom-arch go1.27.1",
			want: buildinfo.Fields{Tool: "nova-example", Version: stamp, Platform: "custom.os_64/custom-arch", GoVersion: "go1.27.1"},
		},
		{
			name: "CRLF first line and trailing output beyond it still parse",
			line: "nova-example " + stamp + " darwin/arm64 go1.27.1\r\nnext line of output\n",
			want: buildinfo.Fields{Tool: "nova-example", Version: stamp, Platform: "darwin/arm64", GoVersion: "go1.27.1"},
		},
		{
			name: "arbitrary valid key=value extras after the four tokens",
			line: "nova-example " + stamp + " linux/arm64 go1.27.1 backend=sandbox-exec build=9c1885748f57",
			want: buildinfo.Fields{Tool: "nova-example", Version: stamp, Platform: "linux/arm64", GoVersion: "go1.27.1",
				Extras: []string{"backend=sandbox-exec", "build=9c1885748f57"}},
		},
		{
			name: "source metadata extras survive the stricter platform rule",
			line: "nova-example " + stamp + " linux/amd64 go1.27.1 repo=github.com/o/r revision=" + stamp + " dirty=false build_host=builder",
			want: buildinfo.Fields{Tool: "nova-example", Version: stamp, Platform: "linux/amd64", GoVersion: "go1.27.1",
				Extras: []string{"repo=github.com/o/r", "revision=" + stamp, "dirty=false", "build_host=builder"}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := buildinfo.Parse(tc.line)
			require.True(t, ok, "Parse refused a line whose platform is one valid <goos>/<goarch> pair: %q", tc.line)
			assert.Equal(t, tc.want, got, "Parse changed the fields it returns for %q", tc.line)
		})
	}

	t.Run("the writer's own line still reads back through Parse", func(t *testing.T) {
		line := buildinfo.Line("nova-example", "v9.9.9", "build=9c1885748f57")
		got, ok := buildinfo.Parse(line)
		require.True(t, ok, "Line wrote a line Parse refuses: %q", line)
		assert.Equal(t, buildinfo.Fields{
			Tool:      "nova-example",
			Version:   "v9.9.9",
			Platform:  runtime.GOOS + "/" + runtime.GOARCH,
			GoVersion: runtime.Version(),
			Extras:    []string{"build=9c1885748f57"},
		}, got, "the round trip through the one writer and the one reader changed a field")
	})
}
