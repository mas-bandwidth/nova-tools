package ws_test

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/land/stream"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws"
)

// TestSlugAgreesWithTheStreamSlug: ws.Slug (the sentinel id's rule, TK.slug
// in Lua too) is land/stream.Slug for one stream.
func TestSlugAgreesWithTheStreamSlug(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"swarm: cards", "nova-sprint + merge + bus", "ci", "S1: Work", "  a--b  ", "x_y.z"} {
		want, err := stream.Slug(name)
		if err != nil {
			t.Fatal(err)
		}
		if got := ws.Slug(name); got != want {
			t.Errorf("Slug(%q) = %q, stream.Slug %q", name, got, want)
		}
		if id := ws.SentinelID(name); id != want+":sentinel" || !ws.IsSentinel(id) {
			t.Errorf("SentinelID(%q) = %q", name, id)
		}
	}
	if ws.SentinelID("---") != "" || ws.IsSentinel("a b:sentinel") || ws.IsSentinel("A:sentinel") || ws.IsSentinel("task:a:sentinel") {
		t.Fatal("a name with no slug has no sentinel; a sentinel id is <slug>:sentinel alone")
	}
}
