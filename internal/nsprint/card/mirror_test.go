package card_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
)

// privateForge answers every repository 404, as github.com answers an
// anonymous request for a private repo, and counts the requests it sees.
func privateForge(t *testing.T) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

// TestCardPushPrivateRepoWithMirror (#3649): a private repo this host holds a
// bare mirror of passes lint, and the forge is never asked.
func TestCardPushPrivateRepoWithMirror(t *testing.T) {
	ctx := context.Background()
	client := newRedis(t)
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "rowan-tools.git", "objects"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NOVA_MIRROR_ROOT", root)
	srv, hits := privateForge(t)

	f := validCard(srv.URL + "/mas-bandwidth/rowan-tools.git")
	f.label = "private-mirrored"
	res := card.Push(ctx, client, sprint, f.render())
	if res.Code != 0 || res.Stderr != "" || !strings.Contains(res.Stdout, "place=pool") {
		t.Fatalf("mirrored private repo refused: exit %d stdout %q stderr %q", res.Code, res.Stdout, res.Stderr)
	}
	if n := hits.Load(); n != 0 {
		t.Fatalf("forge probed %d times with a mirror present, want 0", n)
	}
	assertPlace(t, ctx, client, f.label, true, false)
}

// TestCardPushPrivateRepoNoMirrorNamesRemedy (#3649): no mirror and a 404
// refuses in one line that names mirror-refresh.
func TestCardPushPrivateRepoNoMirrorNamesRemedy(t *testing.T) {
	ctx := context.Background()
	client := newRedis(t)
	root := t.TempDir()
	// A directory without objects/ is not a mirror.
	if err := os.MkdirAll(filepath.Join(root, "rowan-tools.git"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NOVA_MIRROR_ROOT", root)
	srv, hits := privateForge(t)

	f := validCard(srv.URL + "/mas-bandwidth/rowan-tools.git")
	f.label = "private-unmirrored"
	res := card.Push(ctx, client, sprint, f.render())
	if res.Code != 2 || res.Stdout != "" {
		t.Fatalf("exit %d stdout %q stderr %q, want exit 2 and no write", res.Code, res.Stdout, res.Stderr)
	}
	msg := strings.TrimRight(res.Stderr, "\n")
	if strings.Contains(msg, "\n") {
		t.Fatalf("refusal is not one line: %q", res.Stderr)
	}
	for _, want := range []string{"private repo mas-bandwidth/rowan-tools", "mirror-refresh", filepath.Join(root, "rowan-tools.git")} {
		if !strings.Contains(msg, want) {
			t.Fatalf("stderr %q, want %q", res.Stderr, want)
		}
	}
	if hits.Load() == 0 {
		t.Fatal("no mirror, but the forge was never probed")
	}
	assertAbsent(t, ctx, client, f.label)
}
