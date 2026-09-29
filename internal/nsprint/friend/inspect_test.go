package friend_test

import (
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/friend"
)

func TestFriendStatusCalculation(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name       string
		summary    friend.FriendSummary
		wantStatus string
	}{
		{
			name: "fresh beat, no down flag -> up",
			summary: friend.FriendSummary{
				Name:       "f1",
				HasBeat:    true,
				LastBeatAt: now.Add(-10 * time.Second),
				BeatAge:    10 * time.Second,
				Status:     friend.StateUp,
			},
			wantStatus: friend.StateUp,
		},
		{
			name: "stale beat (>60s) -> down",
			summary: friend.FriendSummary{
				Name:       "f2",
				HasBeat:    true,
				LastBeatAt: now.Add(-75 * time.Second),
				BeatAge:    75 * time.Second,
				Status:     friend.StateDown,
			},
			wantStatus: friend.StateDown,
		},
		{
			name: "no beat at all -> down",
			summary: friend.FriendSummary{
				Name:    "f3",
				HasBeat: false,
				Status:  friend.StateDown,
			},
			wantStatus: friend.StateDown,
		},
		{
			name: "out of credits with reset time",
			summary: friend.FriendSummary{
				Name:       "f4",
				HasBeat:    true,
				LastBeatAt: now.Add(-5 * time.Second),
				BeatAge:    5 * time.Second,
				Status:     friend.StateOutOfCredits,
				ResetUntil: now.Add(2 * time.Hour),
			},
			wantStatus: friend.StateOutOfCredits,
		},
		{
			name: "away with until",
			summary: friend.FriendSummary{
				Name:       "f5",
				HasBeat:    true,
				LastBeatAt: now.Add(-5 * time.Second),
				BeatAge:    5 * time.Second,
				Status:     friend.StateAway,
				ResetUntil: now.Add(30 * time.Minute),
			},
			wantStatus: friend.StateAway,
		},
		{
			name: "paused while up",
			summary: friend.FriendSummary{
				Name:       "f6",
				HasBeat:    true,
				LastBeatAt: now.Add(-5 * time.Second),
				BeatAge:    5 * time.Second,
				Status:     "paused",
			},
			wantStatus: "paused",
		},
		{
			name: "offline model",
			summary: friend.FriendSummary{
				Name:       "f7",
				HasBeat:    true,
				LastBeatAt: now.Add(-5 * time.Second),
				BeatAge:    5 * time.Second,
				Status:     friend.StateOfflineModel,
			},
			wantStatus: friend.StateOfflineModel,
		},
		{
			name: "wake missed",
			summary: friend.FriendSummary{
				Name:       "f8",
				HasBeat:    true,
				LastBeatAt: now.Add(-5 * time.Second),
				BeatAge:    5 * time.Second,
				Status:     friend.StateWakeMissed,
			},
			wantStatus: friend.StateWakeMissed,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.summary.Status != tc.wantStatus {
				t.Errorf("status = %q, want %q", tc.summary.Status, tc.wantStatus)
			}
		})
	}
}

func TestFriendSlots(t *testing.T) {
	t.Parallel()
	s := friend.FriendSummary{
		SlotsTotal: 4,
		SlotsFree:  3,
		SlotsUsed:  1,
	}
	if got := s.SlotsString(); got != "3/1" {
		t.Errorf("SlotsString() = %q, want %q", got, "3/1")
	}

	full := friend.FriendSummary{
		SlotsTotal: 4,
		SlotsFree:  0,
		SlotsUsed:  4,
	}
	if got := full.SlotsString(); got != "0/4" {
		t.Errorf("SlotsString() = %q, want %q", got, "0/4")
	}
}

func TestWorkingCopiesString(t *testing.T) {
	t.Parallel()
	empty := friend.FriendSummary{}
	if got := empty.WorkingCopiesString(); got != "-" {
		t.Errorf("empty WorkingCopiesString() = %q, want %q", got, "-")
	}

	one := friend.FriendSummary{
		Working: []friend.WorkingCopySummary{
			{ID: "card-1~1", Age: 135 * time.Second},
		},
	}
	if got := one.WorkingCopiesString(); got != "card-1~1 (2m15s)" {
		t.Errorf("single WorkingCopiesString() = %q, want %q", got, "card-1~1 (2m15s)")
	}

	multi := friend.FriendSummary{
		Working: []friend.WorkingCopySummary{
			{ID: "c1~1", Age: 2 * time.Minute},
			{ID: "c2~1", Age: 45 * time.Second},
		},
	}
	if got := multi.WorkingCopiesString(); got != "c1~1 (2m00s), c2~1 (45s)" {
		t.Errorf("multi WorkingCopiesString() = %q, want %q", got, "c1~1 (2m00s), c2~1 (45s)")
	}

	many := friend.FriendSummary{
		Working: []friend.WorkingCopySummary{
			{ID: "c1~1", Age: 2 * time.Minute},
			{ID: "c2~1", Age: 45 * time.Second},
			{ID: "c3~1", Age: 10 * time.Second},
			{ID: "c4~1", Age: 5 * time.Second},
		},
	}
	if got := many.WorkingCopiesString(); !strings.Contains(got, "+2 more") {
		t.Errorf("many WorkingCopiesString() = %q, want to contain '+2 more'", got)
	}
}

func TestCreditsQuotaString(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)

	empty := friend.FriendSummary{}
	if got := empty.CreditsQuotaString(); got != "-" {
		t.Errorf("empty CreditsQuotaString() = %q, want %q", got, "-")
	}

	withCredits := friend.FriendSummary{Credits: "$15.50"}
	if got := withCredits.CreditsQuotaString(); got != "credits=$15.50" {
		t.Errorf("withCredits CreditsQuotaString() = %q, want %q", got, "credits=$15.50")
	}

	withQuota := friend.FriendSummary{Quota: "80%"}
	if got := withQuota.CreditsQuotaString(); got != "quota=80%" {
		t.Errorf("withQuota CreditsQuotaString() = %q, want %q", got, "quota=80%")
	}

	reset := friend.FriendSummary{
		Status:     friend.StateOutOfCredits,
		ResetUntil: now.Add(2 * time.Hour),
	}
	cq := reset.CreditsQuotaString()
	if !strings.Contains(cq, "resets in") && !strings.Contains(cq, "out-of-credits") {
		t.Errorf("reset CreditsQuotaString() = %q, want mention of reset or out-of-credits", cq)
	}
}

func TestFormatList(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)

	t.Run("empty list", func(t *testing.T) {
		out := friend.FormatList(nil, now)
		if out != "FRIENDS n=0 up=0 down=0 working=0\n" {
			t.Errorf("FormatList(nil) = %q", out)
		}
	})

	t.Run("formatted table", func(t *testing.T) {
		summaries := []friend.FriendSummary{
			{
				Name:       "peer-a",
				Status:     friend.StateUp,
				Harness:    "nova-friend",
				Models:     "model-pro",
				SlotsTotal: 4,
				SlotsFree:  3,
				SlotsUsed:  1,
				HasBeat:    true,
				BeatAge:    4 * time.Second,
				Working: []friend.WorkingCopySummary{
					{ID: "c1~1", Age: 45 * time.Second},
				},
				Credits: "$20",
			},
			{
				Name:       "peer-b",
				Status:     friend.StateDown,
				Harness:    "-",
				Models:     "-",
				SlotsTotal: 2,
				SlotsFree:  2,
				SlotsUsed:  0,
				HasBeat:    false,
			},
		}

		out := friend.FormatList(summaries, now)

		// Verify header
		for _, hdr := range []string{"FRIEND", "STATUS", "HARNESS", "MODELS", "SLOTS (F/U)", "WORKING COPIES", "LAST BEAT", "CREDITS/QUOTA"} {
			if !strings.Contains(out, hdr) {
				t.Errorf("FormatList missing header %q:\n%s", hdr, out)
			}
		}

		// Verify data rows
		if !strings.Contains(out, "peer-a") || !strings.Contains(out, "up") || !strings.Contains(out, "3/1") || !strings.Contains(out, "c1~1 (45s)") {
			t.Errorf("FormatList missing peer-a row facts:\n%s", out)
		}
		if !strings.Contains(out, "peer-b") || !strings.Contains(out, "down") || !strings.Contains(out, "2/0") {
			t.Errorf("FormatList missing peer-b row facts:\n%s", out)
		}

		// Verify summary line
		wantSummary := "FRIENDS n=2 up=1 down=1 working=1\n"
		if !strings.HasSuffix(out, wantSummary) {
			t.Errorf("FormatList suffix = %q, want %q", out, wantSummary)
		}
	})
}

func TestFormatShow(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)

	t.Run("nil detail", func(t *testing.T) {
		if got := friend.FormatShow(nil, now); got != "" {
			t.Errorf("FormatShow(nil) = %q, want empty", got)
		}
	})

	t.Run("full detail", func(t *testing.T) {
		detail := &friend.FriendDetail{
			FriendSummary: friend.FriendSummary{
				Name:        "peer-a",
				Status:      friend.StateUp,
				Harness:     "nova-friend",
				Host:        "host-studio",
				Session:     "sess-01",
				Models:      "model-flash,model-pro",
				SlotsTotal:  4,
				SlotsFree:   3,
				SlotsUsed:   1,
				HasBeat:     true,
				LastBeatAt:  now.Add(-4 * time.Second),
				BeatAge:     4 * time.Second,
				State:       friend.StateUp,
				StateReason: "ready for work",
				Credits:     "$50",
				Load:        "1.2",
				Working: []friend.WorkingCopySummary{
					{
						ID:       "task-123~2",
						Primary:  "task-123",
						Stream:   "ci",
						Leg:      "work",
						Attempt:  2,
						Title:    "Add friend ls",
						Model:    "model-pro",
						LeasedAt: now.Add(-3 * time.Minute),
						Age:      3 * time.Minute,
					},
				},
			},
			Machine: "host-studio",
			Paused:  false,
			Tiers:   "pro",
			Receipts: []friend.ReceiptSummary{
				{
					ID:      "task-100~1",
					Primary: "task-100",
					Stream:  "ci",
					Leg:     "task",
					Outcome: "OK",
					Why:     "tests pass",
					PR:      "mas-bandwidth/nova-tools#4356",
					EndedAt: now.Add(-15 * time.Minute),
					Age:     15 * time.Minute,
				},
				{
					ID:      "read-200~1",
					Primary: "task-200",
					Stream:  "table",
					Leg:     "review",
					Outcome: "OK",
					Why:     "SCORE 10/10",
					EndedAt: now.Add(-45 * time.Minute),
					Age:     45 * time.Minute,
				},
			},
		}

		out := friend.FormatShow(detail, now)

		// Verify core fields
		for _, want := range []string{
			"FRIEND peer-a",
			"Status:       up",
			"Harness:      nova-friend",
			"Host:         host-studio",
			"Machine:      host-studio",
			"Session:      sess-01",
			"Models:       model-flash,model-pro",
			"Slots:        4 desired (3 free, 1 used)",
			"Paused:       false",
			"Tiers:        pro",
			"Last beat:    4s ago",
			"Load:         1.2",
			"WORKING COPIES (1):",
			"task-123~2",
			"stream=ci",
			"attempt=2",
			"age=3m00s",
			"leg=work",
			"title=\"Add friend ls\"",
			"RECENT RECEIPTS (2):",
			"task-100~1",
			"stream=ci",
			"leg=task",
			"outcome=OK",
			"why=\"tests pass\"",
			"pr=mas-bandwidth/nova-tools#4356",
			"read-200~1",
			"leg=review",
			"why=\"SCORE 10/10\"",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("FormatShow missing %q:\n%s", want, out)
			}
		}
	})

	t.Run("empty working and receipts", func(t *testing.T) {
		detail := &friend.FriendDetail{
			FriendSummary: friend.FriendSummary{
				Name:    "peer-b",
				Status:  friend.StateDown,
				HasBeat: false,
			},
		}

		out := friend.FormatShow(detail, now)
		if !strings.Contains(out, "WORKING COPIES: none") {
			t.Errorf("FormatShow missing 'WORKING COPIES: none':\n%s", out)
		}
		if !strings.Contains(out, "RECENT RECEIPTS: none") {
			t.Errorf("FormatShow missing 'RECENT RECEIPTS: none':\n%s", out)
		}
	})
}
