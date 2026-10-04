package bus

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func opsCoverNoteText(from, date, id, subject string) string {
	var b strings.Builder
	b.WriteString("From: " + from + "\nTo: Ada\n")
	if id != "" {
		b.WriteString(KeyID + ": " + id + "\n")
	}
	b.WriteString("Date: " + date + "\n")
	b.WriteString("Subject: " + subject + "\n\nWhat about this?\n")
	return b.String()
}

func TestOpsCoverReceiptPlanMessageRendersTheRecord(t *testing.T) {
	t.Parallel()
	ada := Participant{Name: "Ada", Lane: "from-ada"}
	for _, tt := range []struct {
		name   string
		record []string
		want   string
	}{
		{
			name:   "joins the record in the order given",
			record: []string{"bo-1234567890ab", "from-bo/2026-09-01T0800Z-old-question.md"},
			want:   "ada: receipt for bo-1234567890ab, from-bo/2026-09-01T0800Z-old-question.md",
		},
		{
			name: "an empty record still names the sender",
			want: "ada: receipt for ",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			plan := ReceiptPlan{Record: tt.record}
			assert.Equal(t, tt.want, plan.Message(ada))
		})
	}
}

func TestOpsCoverClosePlanMessageCountsTheNotesClosed(t *testing.T) {
	t.Parallel()
	ada := Participant{Name: "Ada", Lane: "from-ada"}
	for _, tt := range []struct {
		name   string
		closed int
		want   string
	}{
		{
			name:   "counts notes closed, not receipts",
			closed: 3,
			want:   "ada: close 3 before 2026-09-12T14:00:00Z",
		},
		{
			name: "nothing closed reads as zero",
			want: "ada: close 0 before 2026-09-12T14:00:00Z",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			plan := ClosePlan{Closed: tt.closed, Stamp: "2026-09-12T14:00:00Z"}
			assert.Equal(t, tt.want, plan.Message(ada))
		})
	}
}

func TestOpsCoverPlanCloseBuildsOneReceiptPerSenderLane(t *testing.T) {
	t.Parallel()
	const (
		stampDate = "Sat Sep 12 14:00:00 UTC 2026"
		stamp     = "2026-09-12T14:00:00Z"
		boID      = "bo-1234567890ab"
	)
	before := at(stamp)
	now := at("2026-09-12T15:00:00Z")
	for _, tt := range []struct {
		name    string
		files   map[string]string
		closed  int
		kept    int
		wantRe  []string
	}{
		{
			name: "an open note with an id before the stamp closes by id",
			files: map[string]string{
				"from-bo/2026-09-01T0800Z-old-question.md": opsCoverNoteText("Bo", "Tue Sep  1 08:00:00 UTC 2026", boID, "Old question"),
			},
			closed: 1,
			wantRe: []string{boID},
		},
		{
			name: "a legacy note without an id closes by its path",
			files: map[string]string{
				"from-bo/2026-09-01T0800Z-legacy-question.md": opsCoverNoteText("Bo", "Tue Sep  1 08:00:00 UTC 2026", "", "Legacy question"),
			},
			closed: 1,
			wantRe: []string{"from-bo/2026-09-01T0800Z-legacy-question.md"},
		},
		{
			name: "a note dated at the stamp is kept open",
			files: map[string]string{
				"from-bo/2026-09-12T1400Z-fresh-question.md": opsCoverNoteText("Bo", stampDate, boID, "Fresh question"),
			},
			kept: 1,
		},
		{
			name: "two notes sharing a hand-made id close once",
			files: map[string]string{
				"from-bo/2026-09-01T0800Z-first-question.md":  opsCoverNoteText("Bo", "Tue Sep  1 08:00:00 UTC 2026", boID, "First question"),
				"from-bo/2026-09-01T0900Z-second-question.md": opsCoverNoteText("Bo", "Tue Sep  1 09:00:00 UTC 2026", boID, "Second question"),
			},
			closed: 2,
			wantRe: []string{boID},
		},
		{
			name: "an empty inbox plans nothing",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			root := writeBus(t, tt.files)
			tab := loadBus(t, root)
			ada := mustParticipant(t, tab.Config, "Ada")
			plan, err := PlanClose(tab, ada, before, now)
			require.NoError(t, err, "PlanClose: %v", err)
			assert.Equal(t, stamp, plan.Stamp)
			assert.Equal(t, tt.closed, plan.Closed)
			assert.Equal(t, tt.kept, plan.Kept)
			if len(tt.wantRe) == 0 {
				assert.Empty(t, plan.Prepared)
				return
			}
			require.Len(t, plan.Prepared, 1, "one receipt per sender lane")
			p := plan.Prepared[0]
			assert.Equal(t, "Bo", p.Note.Header.To)
			assert.Equal(t, KindReceipt, p.Note.Header.Kind)
			assert.Equal(t, "closed: unanswered before "+stamp, p.Note.Header.Subject)
			assert.ElementsMatch(t, tt.wantRe, p.Note.Header.Re)
			assert.True(t, strings.HasPrefix(p.Note.Header.ID, "ada-"), "ID = %q, want an id minted in the reader's lane", p.Note.Header.ID)
			assert.True(t, strings.HasPrefix(p.Path, "from-ada/"), "Path = %q, want the receipt in the reader's lane", p.Path)
			assert.Equal(t, "ada: closed: unanswered before "+stamp, p.Message)
		})
	}
}

func TestOpsCoverPlanCloseRefuses(t *testing.T) {
	t.Parallel()
	before := at("2026-09-12T14:00:00Z")
	now := at("2026-09-12T15:00:00Z")
	for _, tt := range []struct {
		name   string
		me     string
		before time.Time
		want   string
	}{
		{
			name:   "a reader with no lane",
			me:     "Dana",
			before: before,
			want:   "has no lane on this bus, so has nowhere to record a receipt",
		},
		{
			name: "no stamp",
			me:   "Ada",
			want: "no stamp given",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			root := writeBus(t, nil)
			tab := loadBus(t, root)
			me := mustParticipant(t, tab.Config, tt.me)
			_, err := PlanClose(tab, me, tt.before, now)
			require.Error(t, err, "PlanClose: %v", err)
			assert.Contains(t, err.Error(), tt.want)
		})
	}
}

func TestOpsCoverCloseReceiptBuildsTheReceiptNote(t *testing.T) {
	t.Parallel()
	root := writeBus(t, nil)
	tab := loadBus(t, root)
	ada := mustParticipant(t, tab.Config, "Ada")
	stamp := "2026-09-12T14:00:00Z"
	now := at("2026-09-12T15:00:00Z")
	p, err := closeReceipt(tab, ada, "Bo", []string{"bo-1234567890ab", "from-bo/2026-09-01T0800Z-old-question.md"}, stamp, now)
	require.NoError(t, err, "closeReceipt: %v", err)
	assert.Equal(t, ada.Name, p.Note.Header.From)
	assert.Equal(t, "Bo", p.Note.Header.To)
	assert.Equal(t, KindReceipt, p.Note.Header.Kind)
	assert.Equal(t, "closed: unanswered before "+stamp, p.Note.Header.Subject)
	assert.Equal(t, now.UTC().Format(DateLayout), p.Note.Header.Date)
	assert.Equal(t, []string{"bo-1234567890ab", "from-bo/2026-09-01T0800Z-old-question.md"}, p.Note.Header.Re)
	assert.True(t, strings.HasPrefix(p.Note.Header.ID, "ada-"), "ID = %q, want an id minted in the reader's lane", p.Note.Header.ID)
	assert.True(t, strings.HasPrefix(p.Path, "from-ada/"), "Path = %q, want the receipt in the reader's lane", p.Path)
	assert.Equal(t, ada, p.Sender)
	assert.Equal(t, p.Note.Lane, ada.Lane)
	assert.Contains(t, p.Note.Body, "bo-1234567890ab")
	assert.Contains(t, p.Note.Body, "from-bo/2026-09-01T0800Z-old-question.md")
	assert.Equal(t, "ada: closed: unanswered before "+stamp, p.Message)
}

func TestOpsCoverCloseReceiptRefusesASenderWithNoLane(t *testing.T) {
	t.Parallel()
	root := writeBus(t, nil)
	tab := loadBus(t, root)
	dana := mustParticipant(t, tab.Config, "Dana")
	_, err := closeReceipt(tab, dana, "Bo", []string{"bo-1234567890ab"}, "2026-09-12T14:00:00Z", at("2026-09-12T15:00:00Z"))
	require.Error(t, err, "closeReceipt: %v", err)
	assert.Contains(t, err.Error(), "has no lane, so cannot send")
}
