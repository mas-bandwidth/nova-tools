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

const historyHint = "history takes [<kind> [<name>]] --since <duration|RFC3339> --max <n> --pg <dsn>; kinds: machine, loop"

// historyKinds are the kinds the history table holds; fleet is retired and still reads.
var historyKinds = []string{"machine", "loop", "fleet"}

// parseSince is a duration back from now (90m, 24h, 7d) or an RFC 3339 time.
func parseSince(s string, now time.Time) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	days, isDays := strings.CutSuffix(s, "d")
	d, err := time.ParseDuration(map[bool]string{true: days + "h", false: s}[isDays])
	if isDays {
		d *= 24
	}
	if err != nil || d <= 0 {
		return time.Time{}, fmt.Errorf("--since %q: want a duration such as 90m, 24h or 7d, or an RFC 3339 time", s)
	}
	return now.Add(-d), nil
}

func runHistory(ctx context.Context, args []string, d deps, stdout io.Writer) error {
	fs, words, err := flags("history", historyHint, args, func(fs *flag.FlagSet) {
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
	max, _ := strconv.Atoi(str(fs, "max")) // ignored: the flag package has parsed it as an int
	if max < 0 {
		return usagef("history", historyHint, "--max %d: want a number, 0 for all", max)
	}
	if max > 0 {
		q.Limit = max + 1
	}
	if s := str(fs, "since"); s != "" {
		if q.Since, err = parseSince(s, d.now()); err != nil {
			return usagef("history", historyHint, "%s", err.Error())
		}
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
	for _, e := range entries {
		fmt.Fprintf(stdout, "HISTORY at=%s actor=%s kind=%s name=%s op=%s rev=%d\n", value(e.At), value(e.Actor), value(e.Kind), value(e.Name), value(e.Op), e.ID)
		for _, l := range changeLines(kindNamed(e.Kind), e.Op, e.Before, e.After) {
			fmt.Fprintln(stdout, l)
		}
	}
	_, err = fmt.Fprintf(stdout, "CONFIG HISTORY rows=%d%s\n", len(entries), map[bool]string{true: " more=true"}[more])
	return err
}
