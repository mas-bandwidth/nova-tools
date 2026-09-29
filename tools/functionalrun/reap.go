package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"
)

// listed is one container as `podman ps --format json` prints it.
type listed struct {
	ID     string            `json:"Id"`
	Names  []string          `json:"Names"`
	Labels map[string]string `json:"Labels"`
	State  string            `json:"State"`
}

// verdict is what the reaper decides for one listed container.
type verdict struct {
	id, run, state string
	// remove: the deadline plus the grace has passed.
	remove bool
	// reason, when the container is left: young, or unreadable.
	reason string
}

// parseListed reads `podman ps --format json`. An empty listing is `[]` or
// nothing at all.
func parseListed(out string) ([]listed, error) {
	out = strings.TrimSpace(out)
	if out == "" || out == "null" {
		return nil, nil
	}
	var cs []listed
	if err := json.Unmarshal([]byte(out), &cs); err != nil {
		return nil, fmt.Errorf("podman ps printed what is not a JSON list of containers: %v", err)
	}
	return cs, nil
}

// judge decides, for each listed container, whether the reaper removes it. A
// container without this tool's run label is never judged at all, whatever
// the listing says (the listing is filtered by that label; this is the second
// guard). One with the label and no readable deadline is left and reported.
// One whose deadline plus the grace is still ahead is left alone.
func judge(cs []listed, now time.Time, grace time.Duration) []verdict {
	var out []verdict
	for _, c := range cs {
		run, ok := c.Labels[labelRun]
		if !ok {
			continue
		}
		v := verdict{id: c.ID, run: run, state: c.State}
		raw, ok := c.Labels[labelDeadline]
		secs, err := strconv.ParseInt(raw, 10, 64)
		switch {
		case !ok:
			v.reason = "no-deadline-label"
		case err != nil || secs <= 0:
			v.reason = "unreadable-deadline"
		case now.After(time.Unix(secs, 0).Add(grace)):
			v.remove = true
		default:
			v.reason = "within-deadline"
		}
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].id < out[j].id })
	return out
}

// reap removes every container of this tool past its deadline plus the grace,
// in any state, and returns how many it removed (or would, on a dry run). It
// never touches a container without the run label, never removes a volume by
// itself (a removed container's anonymous volumes go with it), and never
// kills a process.
func reap(ctx context.Context, eng engine, now time.Time, grace time.Duration, dryRun bool, stderr io.Writer) (int, error) {
	lctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := eng.Output(lctx, reapListArgs()...)
	if err != nil {
		return 0, err
	}
	cs, err := parseListed(out)
	if err != nil {
		return 0, err
	}
	reaped, left := 0, 0
	for _, v := range judge(cs, now, grace) {
		if !v.remove {
			left++
			if v.reason != "within-deadline" {
				fmt.Fprintf(stderr, "REAP-LEFT id=%s run=%s state=%s reason=%s\n", short(v.id), v.run, v.state, v.reason)
			}
			continue
		}
		if dryRun {
			fmt.Fprintf(stderr, "REAP-WOULD id=%s run=%s state=%s\n", short(v.id), v.run, v.state)
			reaped++
			continue
		}
		rctx, rcancel := context.WithTimeout(ctx, 60*time.Second)
		_, err := eng.Output(rctx, removeArgs(v.id)...)
		rcancel()
		if err != nil {
			fmt.Fprintf(stderr, "REAP-FAILED id=%s run=%s state=%s err=%q\n", short(v.id), v.run, v.state, err.Error())
			left++
			continue
		}
		fmt.Fprintf(stderr, "REAPED id=%s run=%s state=%s\n", short(v.id), v.run, v.state)
		reaped++
	}
	fmt.Fprintf(stderr, "REAP containers=%d reaped=%d left=%d dry_run=%t\n", len(cs), reaped, left, dryRun)
	return reaped, nil
}

func short(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}
