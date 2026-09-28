package card_test

import (
	"sort"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
	"github.com/mas-bandwidth/nova-tools/internal/typedrec"
)

// TestKindMapCoversTheWaitingSet: every classification kind in the waiting set
// (build/sprint-next/kinds.tsv, 846 rows on 2026-09-24) maps to a RESULT kind,
// the table maps nothing else, and MapKind rewrites only the KIND line.
func TestKindMapCoversTheWaitingSet(t *testing.T) {
	t.Parallel()

	want := map[string]string{
		"go-verb": "fix", "go-fix": "fix", "lua-fn": "fix", "bats": "fix",
		"security": "fix", "fleet": "fix", "retire": "fix",
		"docs":  "docs-guard",
		"spec":  "report",
		"probe": "report",
	}
	for kind, to := range want {
		got, ok := card.KindMap[kind]
		if !ok || got != to {
			t.Errorf("KindMap[%s] = %q (declared %v), want %s", kind, got, ok, to)
			continue
		}
		if !typedrec.IsKind(got) {
			t.Errorf("%s maps to %s, not a RESULT kind", kind, got)
		}
		body := []byte("RESULT: c1 sha=0123456789ab\r\nKIND: " + kind + "\r\nBASE: dev\r\n\r\nKIND: " + kind + " in the text stays\r\n")
		out, from, mapped := card.MapKind(body)
		wantOut := strings.Replace(string(body), "KIND: "+kind+"\r\n", "KIND: "+to+"\r\n", 1)
		if string(out) != wantOut || from != kind || mapped != to {
			t.Errorf("MapKind(%s) = %q (%s -> %s), want %q", kind, out, from, mapped, wantOut)
		}
	}
	var extra []string
	for _, kind := range card.MappedKinds() {
		if _, ok := want[kind]; !ok {
			extra = append(extra, kind)
		}
	}
	sort.Strings(extra)
	if len(extra) > 0 {
		t.Errorf("KindMap maps kinds the waiting set does not carry: %v", extra)
	}
	if out, from, to := card.MapKind([]byte("RESULT: c1\nKIND: not-a-kind\n")); string(out) != "RESULT: c1\nKIND: not-a-kind\n" || from != "not-a-kind" || to != "not-a-kind" {
		t.Errorf("MapKind rewrote an unmapped kind: %q", out)
	}
}
