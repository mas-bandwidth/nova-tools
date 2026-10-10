package swarm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// THE RECEIPT (issue #2916): 264 of the 2026-09-22 failed cards ended on the provider's own
// words, `Error: {"name":"UnknownError",...,"ref":"err_xxxxxxxx"}`, after the launch grace --
// 75 s, 4 min, 11 min in. The grace retry (#900) only sees a death inside 15 s, so every one
// of them was filed NATIVE INCOMPLETE why=no-result, scored as the MODEL's failure, and
// re-dealt to the same route that had just failed it.
const unknownErrorTail = `STEP 3 reading pkg/swarm/finish.go
Error: {"name":"UnknownError","data":{"message":"Unexpected server error. Check server logs for details.","ref":"err_fb35c63e"}}
`

// TestUnknownErrorAtAnyWallHandsBack is the control: a harness exit carrying UnknownError
// after 75 s is PROVIDER-5XX, not INCOMPLETE, and the launcher line names the next route
// with avoid=<failed route>.
func TestUnknownErrorAtAnyWallHandsBack(t *testing.T) {
	t.Parallel()

	job := t.TempDir()
	routes := ParseRouteList("zen/kimi-k3, openrouter/qwen3-coder ,zen/glm-5")

	h, ok := ProviderHandback(ProviderExit{
		Tail: []byte(unknownErrorTail), Job: job, RC: 1, Wall: 75 * time.Second,
		Route: "openrouter/qwen3-coder", Routes: routes,
	})
	require.True(t, ok, "an UnknownError exit at 75 s was not classed as the provider's: it would be filed INCOMPLETE and scored against the model")
	require.Equal(t, ProviderClass5xx, h.Class, "class=%q, want %q", h.Class, ProviderClass5xx)
	require.NotEqual(t, "INCOMPLETE", h.Class, "class=%q, want %q", h.Class, ProviderClass5xx)
	require.Equal(t, "err_fb35c63e", h.Ref, "ref=%q, want the provider's own err_fb35c63e", h.Ref)
	require.Equal(t, "zen/glm-5", h.Next, "next=%q, want the route after the failed one in list order (zen/glm-5)", h.Next)
	line := h.Line("c18")
	for _, want := range []string{
		"NATIVE PROVIDER-5XX ", "label=c18", "ref=err_fb35c63e", "wall=75.00s",
		"route=openrouter/qwen3-coder", "next=zen/glm-5", "avoid=openrouter/qwen3-coder",
	} {
		require.Contains(t, line, want, "the hand-back line is missing %q:\n%s", want, line)
	}
	require.NotContains(t, line, "INCOMPLETE", "the hand-back line is one line and never says INCOMPLETE:\n%q", line)
	require.NotContains(t, line, "\n", "the hand-back line is one line and never says INCOMPLETE:\n%q", line)
	require.NotContains(t, line, "\r", "the hand-back line is one line and never says INCOMPLETE:\n%q", line)

	// AT ANY WALL: the same words at 2 s, 4 min and past an hour are the same class.
	for _, wall := range []time.Duration{2 * time.Second, 4 * time.Minute, 70 * time.Minute} {
		_, ok = ProviderHandback(ProviderExit{Tail: []byte(unknownErrorTail), Job: job, RC: 1, Wall: wall, Route: "zen/glm-5", Routes: routes})
		require.True(t, ok, "UnknownError at wall %s was not PROVIDER-5XX", wall)
	}
	// The bare provider ref is the same signature, and the list wraps: the route after the
	// last is the first.
	h, ok = ProviderHandback(ProviderExit{Tail: []byte("Error: server said no ref=err_0a1b2c3d\n"), Job: job, RC: 1, Wall: 90 * time.Second, Route: "zen/glm-5", Routes: routes})
	require.True(t, ok, "a bare err_xxxxxxxx exit: ok=%v next=%q ref=%q, want ok, zen/kimi-k3, err_0a1b2c3d", ok, h.Next, h.Ref)
	require.Equal(t, "zen/kimi-k3", h.Next, "a bare err_xxxxxxxx exit: ok=%v next=%q ref=%q, want ok, zen/kimi-k3, err_0a1b2c3d", ok, h.Next, h.Ref)
	require.Equal(t, "err_0a1b2c3d", h.Ref, "a bare err_xxxxxxxx exit: ok=%v next=%q ref=%q, want ok, zen/kimi-k3, err_0a1b2c3d", ok, h.Next, h.Ref)
}

// The hand-back never names the failed route as the next one, and says so with a dash when
// the list has nowhere else to go; the avoid= is still there for whoever re-deals.
func TestProviderHandbackNeverNamesTheFailedRouteNext(t *testing.T) {
	t.Parallel()

	job := t.TempDir()
	for _, routes := range [][]string{nil, {"zen/glm-5"}} {
		h, ok := ProviderHandback(ProviderExit{Tail: []byte(unknownErrorTail), Job: job, RC: 1, Wall: 80 * time.Second, Route: "zen/glm-5", Routes: routes})
		require.True(t, ok, "routes=%v: ok=%v next=%q, want ok and no next", routes, ok, h.Next)
		require.Empty(t, h.Next, "routes=%v: ok=%v next=%q, want ok and no next", routes, ok, h.Next)
		line := h.Line("x")
		require.Contains(t, line, "next=- ", "routes=%v: %s", routes, line)
		require.Contains(t, line, " avoid=zen/glm-5 reason=provider: class=", "routes=%v: %s", routes, line)
	}
	// A failed route that is not on the list hands back to the list's first.
	h, _ := ProviderHandback(ProviderExit{Tail: []byte(unknownErrorTail), Job: job, RC: 1, Wall: 80 * time.Second, Route: "other/model", Routes: []string{"zen/glm-5", "other/model"}})
	require.Equal(t, "zen/glm-5", h.Next, "next=%q, want zen/glm-5", h.Next)
	h, _ = ProviderHandback(ProviderExit{Tail: []byte(unknownErrorTail), Job: job, RC: 1, Wall: 80 * time.Second, Route: "gone/model", Routes: []string{"zen/glm-5"}})
	require.Equal(t, "zen/glm-5", h.Next, "a route off the list: next=%q, want the list's first", h.Next)
}

// What is NOT the provider's: a card that published its report, a clean exit, and a card
// whose own work mentioned the words long before its harness's last line.
func TestProviderHandbackLeavesTheCardsOwnEndsAlone(t *testing.T) {
	t.Parallel()

	routes := []string{"zen/glm-5", "zen/kimi-k3"}

	published := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(published, "RESULT.md"), []byte("RESULT: DONE\n"), 0o644))
	_, ok := ProviderHandback(ProviderExit{Tail: []byte(unknownErrorTail), Job: published, RC: 1, Wall: 80 * time.Second, Route: "zen/glm-5", Routes: routes})
	require.False(t, ok, "a card that published its RESULT.md was handed back: its report would be thrown away")
	_, ok = ProviderHandback(ProviderExit{Tail: []byte(unknownErrorTail), Job: t.TempDir(), RC: 0, Wall: 80 * time.Second, Route: "zen/glm-5", Routes: routes})
	require.False(t, ok, "a clean exit was handed back")
	var early strings.Builder
	early.WriteString("$ git grep UnknownError\npulse/replay.go at 39e472aa0: UnknownError err_fb35c63e\n")
	for i := 0; i < 40; i++ {
		early.WriteString("STEP working on the card\n")
	}
	early.WriteString("the model gave up without a report\n")
	_, ok = ProviderHandback(ProviderExit{Tail: []byte(early.String()), Job: t.TempDir(), RC: 1, Wall: 80 * time.Second, Route: "zen/glm-5", Routes: routes})
	require.False(t, ok, "the words in the card's own tool output, forty lines before the harness's end, were read as the provider's")
	_, ok = ProviderHandback(ProviderExit{Tail: []byte("Error: model ran out of steps\n"), Job: t.TempDir(), RC: 1, Wall: 80 * time.Second, Route: "zen/glm-5", Routes: routes})
	require.False(t, ok, "an ordinary failure was classed as the provider's")
}

func TestParseRouteListReadsCommasAndSpaces(t *testing.T) {
	t.Parallel()

	got := ParseRouteList(" zen/a, zen/b\tzen/c ,, zen/a ")
	require.Equal(t, "zen/a|zen/b|zen/c", strings.Join(got, "|"), "got %v, want zen/a zen/b zen/c (first occurrence wins)", got)
}
