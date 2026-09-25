package card_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
	"github.com/mas-bandwidth/nova-tools/internal/typedrec"
)

func withKind(f cardFix, kind string) []byte {
	body := string(f.render())
	if kind == "" {
		return []byte(strings.Replace(body, "KIND: fix\n", "", 1))
	}
	return []byte(strings.Replace(body, "KIND: fix\n", "KIND: "+kind+"\n", 1))
}

// TestCardPushRefusesClassificationKind is nova-tools#3651: a card whose KIND
// is a classification kind was stored, ran, and was refused as malformed only
// at card end. RED WITHOUT THE CHECK: KIND: go-verb pushed with exit 0.
func TestCardPushRefusesClassificationKind(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	client := newRedis(t)
	srv := repoServer(t)
	f := validCard(srv.URL + "/acme/public.git")
	f.label = "kind-go-verb"
	body := withKind(f, "go-verb")

	res := card.Push(ctx, client, sprint, body)
	if res.Code != 2 || res.Stdout != "" || !strings.Contains(res.Stderr, "KIND: go-verb is not a RESULT kind") {
		t.Fatalf("KIND: go-verb without --map-kind: exit %d stdout %q stderr %q, want exit 2 naming the KIND", res.Code, res.Stdout, res.Stderr)
	}
	for _, k := range typedrec.Kinds {
		if !strings.Contains(res.Stderr, k) {
			t.Errorf("the refusal does not name RESULT kind %s: %q", k, res.Stderr)
		}
	}
	if !strings.Contains(res.Stderr, "--map-kind pushes it as fix") {
		t.Errorf("the refusal does not say what --map-kind would push: %q", res.Stderr)
	}
	assertAbsent(t, ctx, client, f.label)

	res = card.PushWith(ctx, client, sprint, body, card.PushOptions{MapKind: true})
	if res.Code != 0 || res.Stderr != "" || !strings.Contains(res.Stdout, "place=pool") {
		t.Fatalf("KIND: go-verb with --map-kind: exit %d stdout %q stderr %q, want pushed", res.Code, res.Stdout, res.Stderr)
	}
	got, err := client.HMGet(ctx, keyCard(f.label), "kind", "payload_sha").Result()
	if err != nil {
		t.Fatal(err)
	}
	mapped := withKind(f, "fix")
	sum := sha256.Sum256(mapped)
	if got[0] != "fix" {
		t.Errorf("stored kind %v, want fix", got[0])
	}
	if got[1] != hex.EncodeToString(sum[:]) {
		t.Errorf("payload_sha %v is not the sha of the card as stored (KIND: fix)", got[1])
	}
}

// TestCardPushKeepsResultAndRunnerKinds: the six RESULT kinds, the runner
// kinds and a card with no KIND line push as before, byte for byte.
func TestCardPushKeepsResultAndRunnerKinds(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	client := newRedis(t)
	srv := repoServer(t)
	kinds := append(append([]string{""}, typedrec.Kinds...), card.RunnerKinds...)
	for _, kind := range kinds {
		f := validCard(srv.URL + "/acme/public.git")
		f.label = "kind-ok-" + kind
		if kind == "" {
			f.label = "kind-ok-absent"
		}
		body := withKind(f, kind)
		if out, from, to := card.MapKind(body); !bytes.Equal(out, body) || from != to {
			t.Errorf("MapKind rewrote KIND %q (%s -> %s)", kind, from, to)
		}
		res := card.Push(ctx, client, sprint, body)
		if res.Code != 0 || res.Stderr != "" {
			t.Fatalf("KIND %q: exit %d stderr %q, want pushed", kind, res.Code, res.Stderr)
		}
		want := kind
		if want == "" {
			want = card.KindModel
		}
		if got, _ := client.HGet(ctx, keyCard(f.label), "kind").Result(); got != want {
			t.Errorf("KIND %q stored as %q, want %q", kind, got, want)
		}
	}
}

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
