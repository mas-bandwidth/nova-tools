package decide_test

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/decide"
	"github.com/mas-bandwidth/nova-tools/internal/events"
)

// TestJevVerdictOnceInStream is the DONE-WHEN of card nx-e15-jev-ledger-to-stream:
// every Jev verdict appears once in the stream as kind=jev with PR, head and score,
// and the calibration view reads it from the fold.
func TestJevVerdictOnceInStream(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	stream := events.NewFakeStream()

	verdicts := []struct {
		repo  string
		pr    int
		head  string
		score int
	}{
		{"mas-bandwidth/nova-tools", 2563, "abc123def456", 7},
		{"mas-bandwidth/schema", 1585, "def789abc012", 9},
		{"mas-bandwidth/nova-tools", 2580, "ghi345jkl678", 5},
	}

	var ids []string
	for i, v := range verdicts {
		id, err := decide.WriteJevLedger(ctx, stream, v.repo, v.pr, v.head, v.score)
		if err != nil {
			t.Fatalf("verdict #%d: %v", i, err)
		}
		ids = append(ids, id)
	}

	// Every call produces exactly one entry with a unique id.
	if len(ids) != 3 {
		t.Fatalf("got %d ids, want 3", len(ids))
	}
	for i := 1; i < len(ids); i++ {
		if ids[i] == ids[i-1] {
			t.Fatalf("ids[%d] == ids[%d] == %s; the stream mints a new id per call", i, i-1, ids[i])
		}
	}

	// Fold the stream, then check the calibration view: the fold's jev rows
	// carry PR, head and score (=route).
	db, err := events.OpenDB(ctx, filepath.Join(t.TempDir(), "jev-fold.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	folder := &events.Folder{Reader: stream, DB: db, Group: "fold", Consumer: "test-1", Count: 10}
	if err := folder.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := folder.Once(ctx); err != nil {
		t.Fatal(err)
	}

	count, err := db.CountKind(ctx, events.Jev)
	if err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Fatalf("the fold holds %d jev rows, want 3", count)
	}

	// Dump the fold and verify every verdict is present with its fields.
	var dump bytes.Buffer
	if err := db.Dump(ctx, &dump); err != nil {
		t.Fatal(err)
	}
	body := dump.String()
	for _, v := range verdicts {
		if !strings.Contains(body, v.repo) {
			t.Errorf("the fold does not contain repo %q", v.repo)
		}
		if !strings.Contains(body, strconv.Itoa(v.pr)) {
			t.Errorf("the fold does not contain pr %d", v.pr)
		}
		if !strings.Contains(body, v.head) {
			t.Errorf("the fold does not contain head %q", v.head)
		}
		scoreRoute := fmt.Sprintf("score/%d", v.score)
		if !strings.Contains(body, scoreRoute) {
			t.Errorf("the fold does not contain score %d as route %q", v.score, scoreRoute)
		}
	}

	// A second write of the same verdict produces a new stream entry (different id),
	// and the fold holds both because each event has its own id.
	id, err := decide.WriteJevLedger(ctx, stream, "mas-bandwidth/nova-tools", 2563, "abc123def456", 7)
	if err != nil {
		t.Fatal(err)
	}
	if id == ids[0] {
		t.Fatal("a second write of the same data got the same id; the stream mints a new one every time")
	}
	if _, err := folder.Once(ctx); err != nil {
		t.Fatal(err)
	}
	count, err = db.CountKind(ctx, events.Jev)
	if err != nil {
		t.Fatal(err)
	}
	if count != 4 {
		t.Fatalf("the fold holds %d jev rows after a duplicate write, want 4 (different event id = different row)", count)
	}
}
