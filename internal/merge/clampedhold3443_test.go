package merge

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// nova-tools #3443: since #2512 a comment or review body was cut at 64 KiB and the
// prefix parsed as if it were the whole body, so a HOLD past the cut read clear and an
// APPROVE before the cut released a hold after it. Verdict lines are now read over the
// whole body (up to MaxParseBodyBytes, 1 MiB, far above GitHub's 65,536-character
// limit), and a body beyond that is refused as a hold that names the clamp, never
// read as clear. Cases B, D and G are the receipt's (rowan-new
// reports/nova-merge-adopt-2026-09-24.md), built with the issue's generator.

const head3443 = "ecaf321f83c5e10dd9fa2690c97afa50443b9a75"

const reviewers3443 = "who\tlogins\tmay-hold\n" +
	"emma\tgafferongames\tyes\n" +
	"stella\tgafferongames\tyes\n" +
	"rowan\trowan-claude\tyes\n"

func pad3443(n int) string {
	line := "padding prose line that is not a verdict, just a long review narrative.\n"
	return strings.Repeat(line, n/len(line)+1)[:n]
}

func comments3443(t *testing.T, bodies ...string) string {
	t.Helper()
	type c struct {
		ID        int64             `json:"id"`
		Body      string            `json:"body"`
		CreatedAt string            `json:"created_at"`
		User      map[string]string `json:"user"`
	}
	var cs []c
	for i, b := range bodies {
		cs = append(cs, c{ID: int64(5805100000 + i), Body: b,
			CreatedAt: fmt.Sprintf("2026-09-24T01:%02d:00Z", 10+i),
			User:      map[string]string{"login": "gafferongames"}})
	}
	out, err := json.Marshal(cs)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func TestAClampedBodyNeverClearsAHold(t *testing.T) {
	t.Parallel()
	rs, err := ParseReviewers(strings.NewReader(reviewers3443))
	if err != nil {
		t.Fatal(err)
	}
	hold := "DISPOSITION who=emma head=" + head3443 + " verdict=HOLD score=3"
	appr := "DISPOSITION who=emma head=" + head3443 + " verdict=APPROVE score=10"
	B := pad3443(70*1024) + "\n" + hold + "\n"
	D := appr + "\n\n" + pad3443(70*1024) + "\n" + hold + "\n"
	G := pad3443(64*1024-20) + "\n" + hold
	// Over the parse cap (1 MiB): only an APPROVE is visible, and it must not clear.
	H := appr + "\n\n" + pad3443(1<<20+1)

	cases := []struct {
		name     string
		comments []string
		reviews  string
	}{
		{"B hold after 70 KiB", []string{B}, "[]"},
		{"D approve, 70 KiB, hold", []string{D}, "[]"},
		{"G hold cut mid-line at 64 KiB", []string{G}, "[]"},
		// D's APPROVE must not release emma's own earlier HOLD either.
		{"D after emma's earlier hold", []string{hold + "\n", D}, "[]"},
		{"B as a COMMENTED review", nil, fmt.Sprintf(`[{"id":77,"user":{"login":"gafferongames"},"body":%q,"state":"COMMENTED","submitted_at":"2026-09-24T01:30:00Z","commit_id":%q}]`, B, head3443)},
		{"H approve then over 1 MiB", []string{H}, "[]"},
		{"H as an APPROVED review", nil, fmt.Sprintf(`[{"id":78,"user":{"login":"gafferongames"},"body":%q,"state":"APPROVED","submitted_at":"2026-09-24T01:30:00Z","commit_id":%q}]`, H, head3443)},
	}
	for _, tc := range cases {
		for _, ignore := range []bool{false, true} {
			cj := "[]"
			if tc.comments != nil {
				cj = comments3443(t, tc.comments...)
			}
			vs, err := ParseForgeVerdicts(cj, tc.reviews, 3406, rs, "rowan", head3443, ignore)
			if err != nil {
				t.Fatalf("%s ignore=%v: %v", tc.name, ignore, err)
			}
			st := EvaluateVerdicts(vs, head3443, "rowan", rs)
			if !st.Held {
				t.Errorf("%s ignore=%v: a body this tool did not read as a whole reads clear (held=false); verdicts=%+v", tc.name, ignore, vs)
			}
		}
	}
}

// The over-cap refusal names the clamp: who=unknown, source=comment-truncated, so the
// LAND REFUSED / BATCH DROP line says why.
func TestAnOverCapBodyIsRefusedByName(t *testing.T) {
	t.Parallel()
	rs, _ := ParseReviewers(strings.NewReader(reviewers3443))
	body := pad3443(MaxParseBodyBytes + 1)
	v, ok := ParseComment(9, "gafferongames", body, "2026-09-24T01:00:00Z", rs, "rowan", head3443, true)
	if !ok || v.Word != "hold" || v.Who != "unknown" || v.Source != "comment-truncated" || v.ID != "comment:9" {
		t.Fatalf("an over-cap body must be a named refusal, got ok=%v %+v", ok, v)
	}
	// Exactly at the cap is read whole: a HOLD at the very end still holds by name.
	hold := "DISPOSITION who=emma head=" + head3443 + " verdict=HOLD score=3"
	atCap := pad3443(MaxParseBodyBytes-len(hold)-1) + "\n" + hold
	if len(atCap) != MaxParseBodyBytes {
		t.Fatalf("fixture is %d bytes", len(atCap))
	}
	v, ok = ParseComment(10, "gafferongames", atCap, "2026-09-24T01:00:00Z", rs, "rowan", head3443, true)
	if !ok || v.Word != "hold" || v.Who != "emma" || v.Source != "comment-rule" {
		t.Fatalf("a HOLD ending at the cap must be read, got ok=%v %+v", ok, v)
	}
}
