package friend_test

import (
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/friend"
)

func TestNormalizeTiers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input   string
		want    string
		wantErr bool
	}{
		{input: "", want: ""},
		{input: "-", want: ""},
		{input: "flash", want: "flash"},
		{input: "pro", want: "pro"},
		{input: "frontier", want: "frontier"},
		{input: "flash,pro", want: "flash,pro"},
		{input: "pro, flash", want: "pro,flash"},
		{input: "frontier,pro,flash", want: "frontier,pro,flash"},
		{input: "flash,flash", want: "flash"},
		{input: "invalid", wantErr: true},
		{input: "pro,unknown", wantErr: true},
	}

	for _, tc := range tests {
		got, err := friend.NormalizeTiers(tc.input)
		if tc.wantErr && err == nil {
			t.Errorf("NormalizeTiers(%q) expected error, got nil", tc.input)
		}
		if !tc.wantErr && err != nil {
			t.Errorf("NormalizeTiers(%q) unexpected error: %v", tc.input, err)
		}
		if got != tc.want {
			t.Errorf("NormalizeTiers(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestCommsLineFormatting(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)

	t.Run("tell", func(t *testing.T) {
		t.Parallel()
		res := &friend.TellResult{
			Friend:  "peer-a",
			Actor:   "rowan",
			EventID: "1727611200000-0",
			Text:    "hello friend",
			At:      now,
		}
		line := res.Line()
		for _, want := range []string{"TELL", "friend=peer-a", "from=rowan", "id=1727611200000-0", "text=\"hello friend\""} {
			if !strings.Contains(line, want) {
				t.Errorf("TellResult.Line() missing %q: %s", want, line)
			}
		}
	})

	t.Run("ask", func(t *testing.T) {
		t.Parallel()
		res := &friend.AskResult{
			Friend:  "peer-a",
			CardID:  "c100",
			CopyID:  "c100~1",
			Actor:   "rowan",
			EventID: "1727611200000-1",
			Brief:   "brief text",
			At:      now,
		}
		line := res.Line()
		for _, want := range []string{"ASK", "friend=peer-a", "card=c100", "copy=c100~1", "from=rowan", "id=1727611200000-1"} {
			if !strings.Contains(line, want) {
				t.Errorf("AskResult.Line() missing %q: %s", want, line)
			}
		}
	})

	t.Run("tiers", func(t *testing.T) {
		t.Parallel()
		res := &friend.TiersResult{
			Friend: "peer-b",
			Tiers:  "flash,pro",
		}
		line := res.Line()
		for _, want := range []string{"FRIEND TIERS", "friend=peer-b", "tiers=flash,pro"} {
			if !strings.Contains(line, want) {
				t.Errorf("TiersResult.Line() missing %q: %s", want, line)
			}
		}

		resEmpty := &friend.TiersResult{
			Friend: "peer-b",
			Tiers:  "",
		}
		if lineEmpty := resEmpty.Line(); !strings.Contains(lineEmpty, "tiers=-") {
			t.Errorf("empty TiersResult.Line() = %q, want tiers=-", lineEmpty)
		}
	})

	t.Run("slots", func(t *testing.T) {
		t.Parallel()
		res := &friend.SlotsResult{
			Friend: "peer-c",
			Slots:  8,
		}
		line := res.Line()
		for _, want := range []string{"FRIEND SLOTS", "friend=peer-c", "slots=8"} {
			if !strings.Contains(line, want) {
				t.Errorf("SlotsResult.Line() missing %q: %s", want, line)
			}
		}
	})

	t.Run("pause and resume", func(t *testing.T) {
		t.Parallel()
		resPaused := &friend.PauseResult{
			Friend:  "peer-d",
			Paused:  true,
			Changed: true,
		}
		if line := resPaused.Line(); line != "PAUSED friend:peer-d" {
			t.Errorf("PauseResult.Line() = %q, want 'PAUSED friend:peer-d'", line)
		}

		resResumed := &friend.PauseResult{
			Friend:  "peer-d",
			Paused:  false,
			Changed: true,
		}
		if line := resResumed.Line(); line != "RESUMED friend:peer-d" {
			t.Errorf("PauseResult.Line() = %q, want 'RESUMED friend:peer-d'", line)
		}
	})
}
