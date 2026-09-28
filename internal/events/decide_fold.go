package events

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"strings"
)

// decide_fold.go is the fold's half of the decision record (#2623): the one INSERT into
// `decisions` and the dump of it.

// decisionColumns is the decisions table's column order, which the INSERT, the dump and the
// tests share so the three can never drift apart.
var decisionColumns = []string{
	"event_id", "label", "bench", "model", "route", "pr", "head", "at", "day",
	"unit_id", "kind", "files", "packages", "lanes", "lane",
	"rung_tried", "height", "confidence", "floor",
	"stepped_up", "escalated", "designated", "source", "rowan_pick", "reason",
	"wait", "awaiting_termination", "refusal",
	"outcome", "rung_succeeded",
	"calls", "tokens_in", "tokens_out", "usd", "usage_failed",
}

// insertDecision is the one INSERT into decisions, idempotent on the event id like every
// other table of the fold.
var insertDecision = func() string {
	marks := strings.TrimSuffix(strings.Repeat("?,", len(decisionColumns)), ",")
	return fmt.Sprintf("INSERT INTO decisions (%s) VALUES (%s) ON CONFLICT(event_id) DO NOTHING",
		strings.Join(decisionColumns, ", "), marks)
}()

// decisionArgs is one decide entry as the INSERT's arguments, in decisionColumns order. An
// absent string, confidence, floor or token count is a nil argument, which is SQL NULL.
func decisionArgs(id string, e Event, at string) []any {
	d := e.Decision
	return []any{
		id, e.Label, e.Bench, e.Model, e.Route, e.PR, e.Head, at, e.Day(),
		d.UnitID, nullText(d.Kind), d.Files, d.Packages, d.Lanes, nullText(d.Lane),
		nullText(d.RungTried), d.Height, nullable(d.Confidence), nullable(d.Floor),
		d.SteppedUp, d.Escalated, d.Designated, nullText(d.Source), nullText(d.RowanPick), nullText(d.Reason),
		nullText(d.Wait), d.AwaitingTermination, nullText(d.Refusal),
		nullText(d.Outcome), nullText(d.RungSucceeded),
		d.Calls, nullable(e.TokensIn), nullable(e.TokensOut), nullable(e.USD), d.UsageFailed,
	}
}

// nullText is an absent string as SQL NULL: the entry did not carry it, which is not "".
func nullText(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// dumpDecisions writes the decisions table under its own header, ordered by event id, with a
// NULL as a dash so a dump tells "the decision did not carry it" from "zero" and from "".
func dumpDecisions(ctx context.Context, db *sql.DB, w io.Writer) error {
	if _, err := fmt.Fprintf(w, "table\t%s\n", strings.Join(decisionColumns, "\t")); err != nil {
		return err
	}
	rows, err := db.QueryContext(ctx, fmt.Sprintf(`SELECT %s FROM decisions ORDER BY event_id`, strings.Join(decisionColumns, ", ")))
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		cells := make([]any, len(decisionColumns))
		for i := range cells {
			cells[i] = new(any)
		}
		if err := rows.Scan(cells...); err != nil {
			return err
		}
		out := make([]string, 0, len(cells)+1)
		out = append(out, "decisions")
		for _, c := range cells {
			out = append(out, cell(*(c.(*any))))
		}
		if _, err := fmt.Fprintln(w, strings.Join(out, "\t")); err != nil {
			return err
		}
	}
	return rows.Err()
}
