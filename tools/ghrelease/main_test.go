package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNoVerbPrintsTheBannerAndRefuses(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.wantRC(h.do(), 2)
	require.Contains(t, h.errb.String(), "usage: go run ./tools/ghrelease <verb>")
}

func TestUnknownVerbIsRefusedByName(t *testing.T) {
	t.Parallel()
	newHarness(t).wantRun(2, `unknown verb "no-such-verb"`, "no-such-verb")
}

func TestEveryVerbHasASummaryAndHelp(t *testing.T) {
	t.Parallel()
	want := []string{"attach", "build", "certified", "controls", "ldflags", "stamp", "sums", "upload"}
	require.Equal(t, want, names())
	for _, n := range names() {
		v := registry[n]
		assert.NotEmpty(t, strings.TrimSpace(v.summary), "verb %s: summary is required", n)
		assert.NotEmpty(t, strings.TrimSpace(v.help), "verb %s: help is required", n)
		assert.NotNil(t, v.do, "verb %s: do is required", n)
		var out bytes.Buffer
		h := newHarness(t)
		h.e.stdout = &out
		assert.Equal(t, 0, h.do("help", n), "help %s", n)
		assert.Equal(t, v.help, out.String(), "help %s", n)
		assert.True(t, strings.HasPrefix(v.help, "usage: go run ./tools/ghrelease "+n), "help %s does not open with its usage line", n)
	}
}

func TestHelpWithNoVerbPrintsTheBannerOnStdout(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.wantRC(h.do("help"), 0)
	for _, n := range names() {
		assert.Contains(t, h.out.String(), n)
	}
	h2 := newHarness(t)
	h2.wantRC(h2.do("help", "nope"), 2)
	h2.mustContain(`unknown verb "nope"`)
}

func TestRegisteringAVerbTwiceIsAProgrammingError(t *testing.T) {
	t.Parallel()
	assert.Panics(t, func() { register(verb{name: "ldflags"}) })
}

func TestARequiredVariableThatIsUnsetIsNamed(t *testing.T) {
	t.Parallel()
	for _, v := range []string{"certified", "upload", "attach"} {
		newHarness(t).wantRun(2, "is not set", v)
	}
}

func TestTheEmbeddedTargetsAreTheShippedPlatforms(t *testing.T) {
	t.Parallel()
	ts, err := parseTargets(releaseTargets)
	require.NoError(t, err)
	require.NotEmpty(t, ts, "release-targets names no platform; a release would ship nothing")
	seen := map[target]bool{}
	for _, x := range ts {
		assert.False(t, seen[x], "%s is listed twice", x)
		seen[x] = true
	}
	for _, x := range []target{{"linux", "amd64"}, {"darwin", "arm64"}, {"windows", "amd64"}} {
		assert.True(t, seen[x], "%s is not shipped", x)
	}
	assert.Equal(t, ".exe", (target{"windows", "amd64"}).ext())
	assert.Empty(t, (target{"linux", "amd64"}).ext())
}

func TestATargetLineOfAnyOtherShapeIsAnErrorNotASkip(t *testing.T) {
	t.Parallel()
	for _, bad := range []string{"linux", "linux amd64 extra", "linux  amd64", " linux amd64"} {
		_, err := parseTargets("# c\n\n" + bad + "\n")
		assert.Error(t, err, "%q was accepted", bad)
	}
	ts, err := parseTargets("# c\n\nlinux amd64\r\n")
	require.NoError(t, err)
	require.Len(t, ts, 1)
}

func TestEnvRootUsesTheWorkingDirectory(t *testing.T) {
	t.Parallel()
	assert.Equal(t, ".", (env{}).root())
	assert.Equal(t, "release", (env{dir: "release"}).root())
}

func TestEnvShippedReturnsSetTargets(t *testing.T) {
	t.Parallel()
	want := []target{{"linux", "amd64"}, {"windows", "amd64"}}
	got, err := (env{targets: want}).shipped()
	require.NoError(t, err)
	assert.Equal(t, want, got)
}

func TestEnvShippedParsesEmbeddedTargets(t *testing.T) {
	t.Parallel()
	got, err := (env{}).shipped()
	require.NoError(t, err)
	assert.NotEmpty(t, got)
}
