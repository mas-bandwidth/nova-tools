package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"
)

const historyHint = "history takes [<kind> [<name>]] --since <duration|RFC3339> --max <n> --json --pg <dsn>; kinds: machine, loop"

// historyKinds are the kinds the history table holds; fleet is retired and still reads.
var historyKinds = []string{"machine", "loop", "fleet"}

// parseSince is a duration back from now (90m, 24h, 7d) or an RFC 3339 time.
func parseSince(s string, now time.Time) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	d, err := time.ParseDuration(s)
	if days, ok := strings.CutSuffix(s, "d"); ok && err != nil {
		if n, nerr := strconv.Atoi(days); nerr == nil {
			d, err = time.Duration(n)*24*time.Hour, nil
		}
	}
	if err != nil || d <= 0 {
		return time.Time{}, fmt.Errorf("--since %q: want a duration such as 90m, 24h or 7d, or an RFC 3339 time", s)
	}
	return now.Add(-d), nil
}

func runHistory(ctx context.Context, args []string, d deps, stdout io.Writer) error {
	fs, words, _, err := flags("history", historyHint, args, func(fs *flag.FlagSet) {
		fs.Bool("json", false, "print one JSON object instead of the lines")
		fs.String("since", "", "only writes since this duration back or RFC 3339 time")
		fs.Int("max", 20, "the most writes shown; 0 for all")
	})
	if err != nil {
		return err
	}
	q := HistoryQuery{}
	switch {
	case len(words) > 2:
		return usagef("history", historyHint, "unexpected words %q", words[2:])
	case len(words) == 2:
		q.Name = words[1]
		fallthrough
	case len(words) == 1:
		if q.Kind = words[0]; !slices.Contains(historyKinds, q.Kind) {
			return usagef("history", historyHint, "unknown kind %q", q.Kind)
		}
	}
	max, err := strconv.Atoi(str(fs, "max"))
	if err != nil || max < 0 {
		return usagef("history", historyHint, "--max %q: want a number, 0 for all", str(fs, "max"))
	}
	if s := str(fs, "since"); s != "" {
		if q.Since, err = parseSince(s, d.now()); err != nil {
			return usagef("history", historyHint, "%s", err.Error())
		}
	}
	if max > 0 {
		q.Limit = max + 1
	}
	st, err := open(ctx, fs, d)
	if err != nil {
		return err
	}
	defer st.Close()
	entries, err := st.History(ctx, q)
	if err != nil {
		return err
	}
	more := max > 0 && len(entries) > max
	if more {
		entries = entries[:max]
	}
	return printHistory(stdout, entries, more, str(fs, "json") == "true")
}

// side is the fields a history entry's write put in the row: the written side,
// or the removed row.
func (e HistoryEntry) side() map[string]string {
	if e.Op == "remove" {
		return e.Before
	}
	return e.After
}

func printHistory(w io.Writer, entries []HistoryEntry, more, asJSON bool) error {
	if asJSON {
		return printHistoryJSON(w, entries, more)
	}
	for _, e := range entries {
		fmt.Fprintf(w, "HISTORY at=%s actor=%s kind=%s name=%s op=%s rev=%d\n", value(e.At), value(e.Actor), value(e.Kind), value(e.Name), value(e.Op), e.ID)
		for _, l := range changeLines(kindNamed(e.Kind), e.Op, e.Before, e.After) {
			fmt.Fprintln(w, l)
		}
	}
	tail := ""
	if more {
		tail = " more=true"
	}
	fmt.Fprintf(w, "CONFIG HISTORY rows=%d%s\n", len(entries), tail)
	return nil
}

type historyChange struct {
	Before *string `json:"before"`
	After  *string `json:"after"`
}

type historyItem struct {
	Rev     int64                    `json:"rev"`
	At      string                   `json:"at"`
	Actor   string                   `json:"actor"`
	Kind    string                   `json:"kind"`
	Name    string                   `json:"name"`
	Op      string                   `json:"op"`
	Reason  string                   `json:"reason"`
	Changes map[string]historyChange `json:"changes"`
}

func printHistoryJSON(w io.Writer, entries []HistoryEntry, more bool) error {
	items := []map[string]any{}
	for _, e := range entries {
		it := historyItem{e.ID, e.At, e.Actor, e.Kind, e.Name, e.Op, e.side()["reason"], map[string]historyChange{}}
		for _, n := range changedNames(kindNamed(e.Kind), e.Op, e.Before, e.After) {
			c := historyChange{}
			if v, ok := e.Before[n]; ok {
				c.Before = &v
			}
			if v, ok := e.After[n]; ok {
				c.After = &v
			}
			it.Changes[n] = c
		}
		items = append(items, map[string]any{"kind": "history", "fields": it})
	}
	out := marshal(map[string]any{
		"result": map[string]any{"verb": "history", "status": "ok", "exit": 0},
		"facts":  map[string]any{"rows": len(entries), "more": more},
		"items":  items,
	})
	_, err := fmt.Fprintln(w, oddEscapes.Replace(out))
	return err
}
