package onboarding

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// rig is the package's test harness (docs/STANDARD.md section 8: a rig is a
// helper struct owning the plumbing): it stands a help banner the way the tool
// prints one, under the tool name every opening reader is checked with, and
// carries the checks the readers' tests repeat. The banners are pure strings,
// so pkg/testkit's file mechanics do not apply here and the rig stays
// package-specific, as pkg/testkit/HARNESS.md asks of domain fixtures.

type rig struct {
	t    *testing.T
	tool string
}

// newRig builds a rig reading banners for the tool the tests call nova-foo.
func newRig(t *testing.T) *rig {
	t.Helper()
	return &rig{t: t, tool: "nova-foo"}
}

// banner joins lines the way a help screen prints them: one under the next,
// the screen ending in a newline.
func (r *rig) banner(lines ...string) string {
	return strings.Join(lines, "\n") + "\n"
}

// refuses checks that OpeningSentence refuses a banner's line 1 and says in
// the refusal what the line lacks; name is the row's, want a phrase of the
// sentence a cold reader is told.
func (r *rig) refuses(name, banner, want string) {
	r.t.Helper()
	_, err := OpeningSentence(banner, r.tool)
	assert.ErrorContains(r.t, err, want, "%s: err = %v, want it to say %q", name, err, want)
}

// foundAt checks the line HowItWorksLine says a banner's how-it-works
// paragraph opens on.
func (r *rig) foundAt(banner string, want int) {
	r.t.Helper()
	got := HowItWorksLine(banner)
	assert.Equal(r.t, want, got, "HowItWorksLine = %d, want %d", got, want)
}

// counted checks how many lines HowItWorksLength says a banner's
// how-it-works paragraph takes.
func (r *rig) counted(banner string, want int) {
	r.t.Helper()
	got := HowItWorksLength(banner)
	assert.Equal(r.t, want, got, "HowItWorksLength = %d, want %d", got, want)
}

// commands checks the tool lines ExampleCommands takes from a banner's
// `example:` block, one line per want; with no wants it pins that the reader
// took none.
func (r *rig) commands(banner string, wants ...string) {
	r.t.Helper()
	got := ExampleCommands(banner, r.tool)
	if len(wants) == 0 {
		assert.Empty(r.t, got, "ExampleCommands of %q gave %q", banner, strings.Join(got, "\n"))
		return
	}
	require.Equal(r.t, strings.Join(wants, "\n"), strings.Join(got, "\n"), "ExampleCommands = %q, want %q", strings.Join(got, "\n"), strings.Join(wants, "\n"))
}
