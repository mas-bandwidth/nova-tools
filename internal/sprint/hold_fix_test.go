package sprint

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseHoldFix(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		note string
		want HoldFix
	}{
		{
			name: "PATHS-PROPOSED only",
			note: "PATHS-PROPOSED: internal/sprint/steps_work.go,internal/sprint/brief_defect.go",
			want: HoldFix{PATHSProposed: []string{"internal/sprint/steps_work.go", "internal/sprint/brief_defect.go"}},
		},
		{
			name: "NEEDS only",
			note: "NEEDS: card-123",
			want: HoldFix{NEEDS: "card-123"},
		},
		{
			name: "TIER only",
			note: "TIER: heavy",
			want: HoldFix{TIER: "heavy"},
		},
		{
			name: "GATE-HOST only",
			note: "GATE-HOST: linux",
			want: HoldFix{GATEHOST: "linux"},
		},
		{
			name: "multiple fix lines",
			note: "PATHS-PROPOSED: a,b\nNEEDS: card-456\nTIER: pro\nGATE-HOST: linux",
			want: HoldFix{
				PATHSProposed: []string{"a", "b"},
				NEEDS:         "card-456",
				TIER:          "pro",
				GATEHOST:      "linux",
			},
		},
		{
			name: "ignores unknown keys",
			note: "UNKNOWN: value\nTIER: flash",
			want: HoldFix{TIER: "flash"},
		},
		{
			name: "empty note",
			note: "",
			want: HoldFix{},
		},
		{
			name: "only whitespace",
			note: "   \n  \n",
			want: HoldFix{},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := ParseHoldFix(tc.note)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestApplyHoldFix_PATHSProposed(t *testing.T) {
	t.Parallel()
	s, cleanup := NewSnapshot()
	defer cleanup()

	stream := "test-stream"
	s.StreamCtl(stream).SetField(FieldPATHS, "internal/sprint/steps_work.go")
	s.protected[stream] = []string{"internal/sprint/steps_work.go", "internal/sprint/brief_defect.go"}

	c := &Card{ID: "card-123", fields: map[string]string{"stream": stream}}
	a := &Attempt{ID: "card-123.w1", fields: map[string]string{"stream": stream}}

	fix := HoldFix{PATHSProposed: []string{"internal/sprint/brief_defect.go"}}
	p, refused := ApplyHoldFix(s, c, a, fix)

	require.Len(t, refused, 0)
	require.Len(t, p.Units, 1)
	require.Contains(t, p.Units[0].Moved, "PATHS widened")
}

func TestApplyHoldFix_PATHSProposedOutsideProtected(t *testing.T) {
	t.Parallel()
	s, cleanup := NewSnapshot()
	defer cleanup()

	stream := "test-stream"
	s.StreamCtl(stream).SetField(FieldPATHS, "internal/sprint/steps_work.go")

	c := &Card{ID: "card-123", fields: map[string]string{"stream": stream}}
	a := &Attempt{ID: "card-123.w1", fields: map[string]string{"stream": stream}}

	fix := HoldFix{PATHSProposed: []string{"internal/sprint/brief_defect.go"}}
	_, refused := ApplyHoldFix(s, c, a, fix)

	require.Len(t, refused, 1)
	require.Contains(t, refused[0].Reason, "outside land-protected set")
}

func TestApplyHoldFix_NEEDS(t *testing.T) {
	t.Parallel()
	s, cleanup := NewSnapshot()
	defer cleanup()

	stream := "test-stream"
	c := &Card{ID: "card-123", fields: map[string]string{"stream": stream}}
	a := &Attempt{ID: "card-123.w1", fields: map[string]string{"stream": stream}, Dependencies: ""}

	fix := HoldFix{NEEDS: "card-456"}
	p, refused := ApplyHoldFix(s, c, a, fix)

	require.Len(t, refused, 0)
	require.Len(t, p.Units, 1)
	require.Contains(t, p.Units[0].Moved, "added dependency")
}

func TestApplyHoldFix_NEEDSNotFound(t *testing.T) {
	t.Parallel()
	s, cleanup := NewSnapshot()
	defer cleanup()

	stream := "test-stream"
	c := &Card{ID: "card-123", fields: map[string]string{"stream": stream}}
	a := &Attempt{ID: "card-123.w1", fields: map[string]string{"stream": stream}}

	fix := HoldFix{NEEDS: "card-nonexistent"}
	_, refused := ApplyHoldFix(s, c, a, fix)

	require.Len(t, refused, 1)
	require.Contains(t, refused[0].Reason, "card card-nonexistent not found")
}

func TestApplyHoldFix_TIER(t *testing.T) {
	t.Parallel()
	s, cleanup := NewSnapshot()
	defer cleanup()

	stream := "test-stream"
	c := &Card{ID: "card-123", fields: map[string]string{"stream": stream}, fields: map[string]string{"tier_now": "flash"}}

	a := &Attempt{ID: "card-123.w1", fields: map[string]string{"stream": stream, "tier_now": "flash"}, Owner: "friend-test"}
	s.fleet["friend-test"] = &FriendRow{Tiers: []string{"flash", "pro"}}

	fix := HoldFix{TIER: "pro"}
	p, refused := ApplyHoldFix(s, c, a, fix)

	require.Len(t, refused, 0)
	require.Len(t, p.Units, 1)
	require.Contains(t, p.Units[0].Moved, "tier recut to pro")
}

func TestApplyHoldFix_TIERNotFound(t *testing.T) {
	t.Parallel()
	s, cleanup := NewSnapshot()
	defer cleanup()

	stream := "test-stream"
	c := &Card{ID: "card-123", fields: map[string]string{"stream": stream}}
	a := &Attempt{ID: "card-123.w1", fields: map[string]string{"stream": stream}, Owner: "friend-test"}
	s.fleet["friend-test"] = &FriendRow{Tiers: []string{"flash"}}

	fix := HoldFix{TIER: "pro"}
	_, refused := ApplyHoldFix(s, c, a, fix)

	require.Len(t, refused, 1)
	require.Contains(t, refused[0].Reason, "tier pro not served by")
}

func TestApplyHoldFix_TIERUnknown(t *testing.T) {
	t.Parallel()
	s, cleanup := NewSnapshot()
	defer cleanup()

	stream := "test-stream"
	c := &Card{ID: "card-123", fields: map[string]string{"stream": stream}}
	a := &Attempt{ID: "card-123.w1", fields: map[string]string{"stream": stream}}

	fix := HoldFix{TIER: "unknown"}
	_, refused := ApplyHoldFix(s, c, a, fix)

	require.Len(t, refused, 1)
	require.Contains(t, refused[0].Reason, "unknown tier")
}

func TestApplyHoldFix_GATEHOST(t *testing.T) {
	t.Parallel()
	s, cleanup := NewSnapshot()
	defer cleanup()

	stream := "test-stream"
	c := &Card{ID: "card-123", fields: map[string]string{"stream": stream}}
	a := &Attempt{ID: "card-123.w1", fields: map[string]string{"stream": stream, "gate_host": "macos"}}

	fix := HoldFix{GATEHOST: "linux"}
	p, refused := ApplyHoldFix(s, c, a, fix)

	require.Len(t, refused, 0)
	require.Len(t, p.Units, 1)
	require.Contains(t, p.Units[0].Moved, "gates marked to run on linux")
}

func TestApplyHoldFix_GATEHOSTUnknown(t *testing.T) {
	t.Parallel()
	s, cleanup := NewSnapshot()
	defer cleanup()

	stream := "test-stream"
	c := &Card{ID: "card-123", fields: map[string]string{"stream": stream}}
	a := &Attempt{ID: "card-123.w1", fields: map[string]string{"stream": stream}}

	fix := HoldFix{GATEHOST: "windows"}
	_, refused := ApplyHoldFix(s, c, a, fix)

	require.Len(t, refused, 1)
	require.Contains(t, refused[0].Reason, "unknown host")
}

func TestApplyHoldFix_NoteWithNoFixLine(t *testing.T) {
	t.Parallel()
	s, cleanup := NewSnapshot()
	defer cleanup()

	stream := "test-stream"
	c := &Card{ID: "card-123", fields: map[string]string{"stream": stream}}
	a := &Attempt{ID: "card-123.w1", fields: map[string]string{"stream": stream}}

	// Note without fix lines
	note := "This note has no fix line"
	fix := ParseHoldFix(note)
	p, refused := ApplyHoldFix(s, c, a, fix)

	require.Len(t, refused, 0)
	require.Len(t, p.Units, 0)
}

func TestApplyHoldFix_MultipleFixLines(t *testing.T) {
	t.Parallel()
	s, cleanup := NewSnapshot()
	defer cleanup()

	stream := "test-stream"
	s.StreamCtl(stream).SetField(FieldPATHS, "internal/sprint/steps_work.go")
	s.protected[stream] = []string{"internal/sprint/steps_work.go", "internal/sprint/brief_defect.go"}

	c := &Card{ID: "card-123", fields: map[string]string{"stream": stream}, fields: map[string]string{"tier_now": "flash"}}
	a := &Attempt{ID: "card-123.w1", fields: map[string]string{"stream": stream, "tier_now": "flash", "gate_host": "macos"}, Owner: "friend-test"}
	s.fleet["friend-test"] = &FriendRow{Tiers: []string{"flash", "pro"}}

	note := "PATHS-PROPOSED: internal/sprint/brief_defect.go\nTIER: pro\nGATE-HOST: linux"
	fix := ParseHoldFix(note)
	p, refused := ApplyHoldFix(s, c, a, fix)

	require.Len(t, refused, 0)
	require.Len(t, p.Units, 3)
}

func TestApplyHoldFix_RequiresAttempt(t *testing.T) {
	t.Parallel()
	s, cleanup := NewSnapshot()
	defer cleanup()

	stream := "test-stream"
	c := &Card{ID: "card-123", fields: map[string]string{"stream": stream}}

	// Test with nil attempt
	fix := HoldFix{TIER: "pro", GATEHOST: "linux", NEEDS: "card-456"}
	_, refused := ApplyHoldFix(s, c, nil, fix)

	require.Len(t, refused, 3)
}
