package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/route"
)

// TestRoutesPreamblePrintsTheParagraph is #2498 S8: `nova-sprint routes
// --preamble <route>` prints the route's one paragraph for a card front to
// inline, and refuses (exit 1, nothing on stdout) a route that carries none.
func TestRoutesPreamblePrintsTheParagraph(t *testing.T) {
	t.Parallel()

	tab, err := route.Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range tab.Reachable("pro") {
		want, err := tab.Preamble(r)
		if err != nil {
			t.Fatalf("%s: %v", r, err)
		}
		var out, errb bytes.Buffer
		if code := cmdRoutes(context.Background(), []string{"--preamble", r}, &out, &errb); code != 0 {
			t.Fatalf("--preamble %s: exit %d, stderr %q", r, code, errb.String())
		}
		if out.String() != want+"\n" {
			t.Errorf("--preamble %s printed %q, want %q", r, out.String(), want+"\n")
		}
	}
	for _, r := range []string{"ormuse", "no-such-route"} {
		var out, errb bytes.Buffer
		if code := cmdRoutes(context.Background(), []string{"--preamble", r}, &out, &errb); code != 1 || out.Len() != 0 || !strings.Contains(errb.String(), r) {
			t.Errorf("--preamble %s: exit %d stdout %q stderr %q, want exit 1, empty stdout, a refusal naming the route", r, code, out.String(), errb.String())
		}
	}
	var out, errb bytes.Buffer
	if code := cmdRoutes(context.Background(), []string{"--preamble", "ordspro", "--rung", "pro"}, &out, &errb); code != 2 {
		t.Errorf("--preamble with --rung: exit %d, want 2 (it takes no other flag)", code)
	}
}
