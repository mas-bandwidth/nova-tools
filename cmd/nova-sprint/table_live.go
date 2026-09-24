// table_live.go: `nova-sprint table --layout live` and `--compare` (#2674),
// the Go port of rowan-tools bin/sprint-table-redis. It reads only the keys
// that script reads (internal/nsprint/table/live.go) and prints its layout
// byte for byte, so the two can run side by side until the switch.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/table"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

func cmdTableLive(opts tableOpts, stdout, stderr io.Writer) int {
	var problems []string
	if opts.layout != "live" && opts.compare == "" {
		problems = append(problems, "--compare renders the live layout; drop --layout "+opts.layout)
	}
	if opts.redis == "" {
		problems = append(problems, "--layout live needs --redis <addr>")
	}
	if opts.sprint == "" {
		problems = append(problems, "--layout live needs --sprint <name> (sprint:<name>:xy and :landed)")
	}
	if opts.friends == "" {
		problems = append(problems, "--layout live needs --friends <a,b,...>, the roster in display order")
	}
	if opts.check {
		problems = append(problems, "--layout live takes no --check")
	}
	if opts.loop && (opts.once || opts.compare != "") {
		problems = append(problems, "--loop takes neither --once nor --compare")
	}
	if len(problems) > 0 {
		return tableRefuse(stderr, strings.Join(problems, "; "))
	}
	cfg := table.LiveConfig{Sprint: opts.sprint, XYFile: opts.xyFile, RowStale: 10 * time.Second}
	for _, name := range strings.FieldsFunc(opts.friends, func(r rune) bool { return r == ',' || r == ' ' }) {
		cfg.Friends = append(cfg.Friends, name)
	}
	ctx := context.Background()
	st, err := store.Open(ctx, opts.redis)
	if err != nil && !opts.loop {
		return tableRefuse(stderr, err.Error())
	}
	if err != nil {
		// The loop never refuses on a down Redis: it publishes the stale
		// shape and keeps trying, as the bash does.
		fmt.Fprintf(stderr, "nova-sprint table: %s; publishing the stale table and retrying\n", oneline.Escape(err.Error()))
	}
	defer st.Close()

	if opts.compare != "" {
		return compareLive(ctx, st, cfg, opts.compare, stdout, stderr)
	}
	if !opts.loop {
		snap, err := table.ReadLive(ctx, st.Client(), cfg)
		if err != nil {
			return tableRefuse(stderr, err.Error())
		}
		_, _ = io.WriteString(stdout, snap.RenderLive(time.Now()))
		return 0
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	var last *table.LiveSnapshot
	for {
		now := time.Now()
		var snap *table.LiveSnapshot
		if st == nil {
			if st, err = store.Open(ctx, opts.redis); err != nil {
				st = nil
			}
		}
		if st != nil {
			snap, err = table.ReadLive(ctx, st.Client(), cfg)
		}
		if snap != nil {
			snap.LastGood = now
			last = snap
		} else {
			snap = table.FailedLive(cfg, last)
			if err != nil {
				fmt.Fprintf(stderr, "nova-sprint table: %s; stale table published\n", oneline.Escape(err.Error()))
			}
		}
		// Written nowhere (#3326): each tick is printed, never published to a file.
		_, _ = io.WriteString(stdout, snap.RenderLive(now))
		<-ticker.C
	}
}

// compareLive renders from the live Redis and diffs against path's content:
// MATCH and exit 0, or a unified diff and exit 1. The table under comparison
// is rewritten once a second from counts that move every second, so compare
// first waits (at most comparePublishWait) for the writer's next publish of
// path and reads Redis the moment it lands: both renders then come from the
// same second. A file nobody rewrites is compared as it stands.
func compareLive(ctx context.Context, st *store.Store, cfg table.LiveConfig, path string, stdout, stderr io.Writer) int {
	waitForPublish(path, comparePublishWait)
	snap, err := table.ReadLive(ctx, st.Client(), cfg)
	if err != nil {
		return tableRefuse(stderr, err.Error())
	}
	want, err := os.ReadFile(path)
	if err != nil {
		return tableRefuse(stderr, "--compare: "+err.Error())
	}
	if err != nil {
		return tableRefuse(stderr, err.Error())
	}
	got := snap.RenderLive(time.Now())
	if got == string(want) {
		fmt.Fprintln(stdout, "MATCH")
		return 0
	}
	_, _ = io.WriteString(stdout, unifiedDiff(path, "nova-sprint table --layout live", string(want), got))
	return 1
}

const comparePublishWait = 3 * time.Second

// waitForPublish returns once path is replaced (a new inode or mtime: the
// writers publish by rename) or after limit.
func waitForPublish(path string, limit time.Duration) {
	before, err := os.Stat(path)
	if err != nil {
		return
	}
	stop := time.Now().Add(limit)
	for time.Now().Before(stop) {
		time.Sleep(5 * time.Millisecond)
		now, err := os.Stat(path)
		if err == nil && (!os.SameFile(before, now) || !now.ModTime().Equal(before.ModTime())) {
			return
		}
	}
}

// unifiedDiff is a whole-file unified diff (one hunk, 3 lines of context
// folded to the changed span), enough to read a table of ~30 lines.
func unifiedDiff(aName, bName, a, b string) string {
	al, bl := splitLines(a), splitLines(b)
	// longest common subsequence over lines
	n, m := len(al), len(bl)
	lcs := make([][]int, n+1)
	for i := range lcs {
		lcs[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if al[i] == bl[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else if lcs[i+1][j] >= lcs[i][j+1] {
				lcs[i][j] = lcs[i+1][j]
			} else {
				lcs[i][j] = lcs[i][j+1]
			}
		}
	}
	var body strings.Builder
	i, j := 0, 0
	for i < n || j < m {
		switch {
		case i < n && j < m && al[i] == bl[j]:
			body.WriteString(" " + al[i] + "\n")
			i, j = i+1, j+1
		case i < n && (j == m || lcs[i+1][j] >= lcs[i][j+1]):
			body.WriteString("-" + al[i] + "\n")
			i++
		default:
			body.WriteString("+" + bl[j] + "\n")
			j++
		}
	}
	return fmt.Sprintf("--- %s\n+++ %s\n@@ -1,%d +1,%d @@\n%s", aName, bName, n, m, body.String())
}

func splitLines(s string) []string {
	s = strings.TrimSuffix(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}
