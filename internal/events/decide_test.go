package events

import (
	"bytes"
	"context"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
)

// decideEvent is one Jev routing decision with every decide_log field filled, the shape
// nova-decide route writes for a decision that made a call.
func decideEvent() Event {
	return Event{
		Label: "card-41", Kind: Decide, TokensIn: Int64(812), TokensOut: Int64(64), At: at,
		Decision: &Decision{
			UnitID: "card-41", Kind: "rebase", Files: 2, Packages: 1, Lanes: 1, Lane: "decide",
			RungTried: "flash", Height: 1, Confidence: Float64(0.82), Floor: Float64(0.65),
			SteppedUp: false, Escalated: true, Designated: false, Source: "provider",
			RowanPick: "flash", Reason: "jev chose flash among flash, pro over the evidence", Wait: "-",
			AwaitingTermination: false, Refusal: "no-accounting", Outcome: "green", RungSucceeded: "flash",
			Calls: 1, UsageFailed: false,
		},
	}
}

// decideLogColumns is decide_log's column list from the retired table's migration, less the
// three that do not travel as themselves: id (the event id), ts (the entry's `at`) and
// evidence (a JSON document the stream refuses; its measured columns are here).
var decideLogColumns = []string{
	"unit_id", "kind", "files", "packages", "lanes", "lane",
	"rung_tried", "height", "confidence", "floor",
	"stepped_up", "escalated", "designated", "source", "rowan_pick", "reason",
	"wait", "awaiting_termination", "refusal",
	"outcome", "rung_succeeded",
	"calls", "tokens_in", "tokens_out", "usage_failed",
}

// The names are decide_log's names: a full decision puts every one of them on the entry, and
// the fold's decisions table has a column for every one of them.
func TestDecideEventCarriesDecideLogsNames(t *testing.T) {
	t.Parallel()

	fields := decideEvent().Fields()
	if fields["event"] != "decide" {
		t.Fatalf("event = %q, want decide", fields["event"])
	}
	for _, name := range decideLogColumns {
		if _, ok := fields[name]; !ok {
			t.Errorf("a full decision carries no %q field; the wire names are decide_log's", name)
		}
	}
	cols := map[string]bool{}
	for _, c := range decisionColumns {
		cols[c] = true
	}
	for _, name := range decideLogColumns {
		if !cols[name] {
			t.Errorf("the decisions table has no %q column", name)
		}
	}
	back, err := FromFields(fields)
	if err != nil {
		t.Fatalf("reading the entry back: %v", err)
	}
	if !reflect.DeepEqual(back, decideEvent()) {
		t.Fatalf("the round trip changed the decision:\n got %+v\nwant %+v", *back.Decision, *decideEvent().Decision)
	}
}

// A decide event without its decision, and a decision on any other kind, are refused at the
// door; so is a decision field that is a payload rather than an id.
func TestValidateRefusesAMisshapenDecision(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		event Event
		want  string
	}{
		{"decide with no decision", Event{Label: "card-41", Kind: Decide}, "carries the decision"},
		{"a decision on an ok", func() Event { e := okEvent(); e.Decision = decideEvent().Decision; return e }(), "only a decide event"},
		{"no unit", func() Event { e := decideEvent(); e.Decision.UnitID = ""; return e }(), "unit_id"},
		{"a reason that is a transcript", func() Event { e := decideEvent(); e.Decision.Reason = strings.Repeat("r", maxFieldBytes+1); return e }(), "ids and counts only"},
		{"a negative call count", func() Event { e := decideEvent(); e.Decision.Calls = -1; return e }(), "calls is a count"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.event.Validate()
			if err == nil {
				t.Fatalf("%s was accepted", tc.name)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("refusal = %q, want it to name %q", err.Error(), tc.want)
			}
		})
	}
}

// Text caps a long reason at the field ceiling and says so with the byte mark, and keeps it
// on one line, so a long reason is cut rather than costing the whole decision.
func TestTextCapsALongReasonAndSaysSo(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("the evidence says flash; ", 20) + "\nsecond line"
	got := Text(long)
	if len(got) > maxFieldBytes {
		t.Fatalf("Text is %d bytes, want at most %d", len(got), maxFieldBytes)
	}
	if !strings.Contains(got, "...+") {
		t.Fatalf("Text cut the reason without the byte mark: %q", got)
	}
	if err := idShaped("reason", got); err != nil {
		t.Fatalf("Text's output is refused by the door: %v", err)
	}
	if short := "jev chose flash"; Text(short) != short {
		t.Fatalf("Text changed a reason that fits: %q", Text(short))
	}
}

// The round trip #2623 asks for, on the real writer: a decision written by RedisStore.Emit
// (the Emitter every card transition goes through) onto cards:done, read by the fold under
// its group into the decisions table. The optional fields the decision did not carry -- no
// token counts, no confidence, no refusal, no outcome -- stay ABSENT all the way: not on the
// entry, NULL in the table, a dash in the dump. Never 0 and never "".
func TestADecideEventRoundTripsFromTheWriterToTheFold(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	mr := miniredis.RunT(t)
	store, err := Open(ctx, Dial{Addr: mr.Addr()})
	if err != nil {
		t.Fatalf("dialing the store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if store.StreamName() != "cards:done" {
		t.Fatalf("the writer writes %q, want cards:done: one writer, one stream", store.StreamName())
	}

	sparse := decideEvent()
	sparse.TokensIn, sparse.TokensOut = nil, nil
	sparse.Decision.Confidence = nil
	sparse.Decision.Refusal, sparse.Decision.Outcome, sparse.Decision.RungSucceeded = "", "", ""
	sparse.Decision.Calls = 0
	id, err := store.Emit(ctx, sparse)
	if err != nil {
		t.Fatalf("writing the decision: %v", err)
	}

	raw, err := store.Range(ctx, "-", 10)
	if err != nil || len(raw) != 1 || raw[0].ID != id {
		t.Fatalf("the stream holds %v (err %v), want the one entry %s", raw, err, id)
	}
	for _, absent := range []string{"tokens_in", "tokens_out", "confidence", "refusal", "outcome", "rung_succeeded"} {
		if v, ok := raw[0].Fields[absent]; ok {
			t.Errorf("the entry carries %s=%q; a field the decision did not carry must be absent", absent, v)
		}
	}
	back, err := FromFields(raw[0].Fields)
	if err != nil {
		t.Fatalf("reading the entry back: %v", err)
	}
	if back.TokensIn != nil || back.TokensOut != nil || back.Decision.Confidence != nil {
		t.Fatalf("an absent counter came back present: tokens_in=%v tokens_out=%v confidence=%v",
			back.TokensIn, back.TokensOut, back.Decision.Confidence)
	}

	db := openFold(t, "ev.sqlite")
	folder := &Folder{Reader: store, DB: db, Group: Group, Consumer: "fold-1"}
	if err := folder.Start(ctx); err != nil {
		t.Fatal(err)
	}
	stats, err := folder.Once(ctx)
	if err != nil {
		t.Fatalf("folding: %v", err)
	}
	if stats.Inserted != 1 || stats.Acked != 1 || stats.Skipped != 0 {
		t.Fatalf("fold stats %+v, want one new row, acked, none skipped", stats)
	}
	if n, err := db.CountKind(ctx, Decide); err != nil || n != 1 {
		t.Fatalf("decisions holds %d (err %v), want 1", n, err)
	}
	if n, err := db.CountKind(ctx, OK); err != nil || n != 0 {
		t.Fatalf("a decision leaked into attempts: %d rows (err %v)", n, err)
	}

	var (
		unit, kind, rung, source string
		files, height, calls     int
		floor                    float64
		escalated                bool
		nulls                    int
	)
	if err := db.db.QueryRowContext(ctx, `SELECT unit_id, kind, rung_tried, source, files, height, calls, floor, escalated,
		(tokens_in IS NULL) + (tokens_out IS NULL) + (confidence IS NULL) + (refusal IS NULL) + (outcome IS NULL) + (rung_succeeded IS NULL)
		FROM decisions WHERE event_id = ?`, id).Scan(&unit, &kind, &rung, &source, &files, &height, &calls, &floor, &escalated, &nulls); err != nil {
		t.Fatalf("reading the decision row: %v", err)
	}
	if unit != "card-41" || kind != "rebase" || rung != "flash" || source != "provider" ||
		files != 2 || height != 1 || calls != 0 || floor != 0.65 || !escalated {
		t.Fatalf("the row is unit=%s kind=%s rung=%s source=%s files=%d height=%d calls=%d floor=%v escalated=%v; want what was written",
			unit, kind, rung, source, files, height, calls, floor, escalated)
	}
	if nulls != 6 {
		t.Fatalf("%d of the six absent fields are NULL; an absent field is NULL, never 0 or ''", nulls)
	}

	var dump bytes.Buffer
	if err := db.Dump(ctx, &dump); err != nil {
		t.Fatal(err)
	}
	var row []string
	for _, line := range strings.Split(dump.String(), "\n") {
		if strings.HasPrefix(line, "decisions\t") {
			row = strings.Split(line, "\t")[1:]
		}
	}
	if len(row) != len(decisionColumns) {
		t.Fatalf("the dump's decision row has %d cells, want %d:\n%s", len(row), len(decisionColumns), dump.String())
	}
	for i, col := range decisionColumns {
		switch col {
		case "tokens_in", "tokens_out", "usd", "confidence", "refusal", "outcome", "rung_succeeded":
			if row[i] != "-" {
				t.Errorf("the dump prints %s=%q; an absent field is a dash", col, row[i])
			}
		case "calls":
			if row[i] != "0" {
				t.Errorf("the dump prints calls=%q; a reported zero is a zero", row[i])
			}
		}
	}

	var report bytes.Buffer
	if err := db.Report(ctx, &report, 0); err != nil {
		t.Fatal(err)
	}
	// kind decisions units stepped_up escalated refused calls tokens_in tokens_out
	if !strings.Contains(report.String(), "# decisions_by_kind\n") || !strings.Contains(report.String(), "rebase\t1\t1\t0\t1\t0\t0\t-\t-\n") {
		t.Fatalf("the report does not answer the decision record per kind:\n%s", report.String())
	}
}

// The decide fields and the event fields never share a name except where they are the same
// fact: tokens_in and tokens_out are the event's counters, carried once.
func TestDecideFieldNamesDoNotShadowTheEventsOwn(t *testing.T) {
	t.Parallel()

	own := map[string]bool{}
	for _, n := range fieldNames {
		own[n] = true
	}
	var clash []string
	for _, n := range decisionFieldNames {
		if own[n] {
			clash = append(clash, n)
		}
	}
	sort.Strings(clash)
	if len(clash) != 0 {
		t.Fatalf("decision fields %v shadow the event's own fields", clash)
	}
}
