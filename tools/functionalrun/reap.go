package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
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

// runIDRE is the shape of every run id this tool makes (newRunID, and the
// module step's id with -mod): a container whose run label has another shape
// is not this tool's.
var runIDRE = regexp.MustCompile(`^[0-9]{8}t[0-9]{6}-[0-9a-f]{8}(-mod)?$`)

// unixRE is a unix-seconds label as this tool writes it: digits only.
var unixRE = regexp.MustCompile(`^[1-9][0-9]{0,11}$`)

func unixLabel(v string) (int64, bool) {
	if !unixRE.MatchString(v) {
		return 0, false
	}
	n, err := strconv.ParseInt(v, 10, 64)
	return n, err == nil
}

// judge decides, for each listed container, whether the reaper removes it.
// Only this tool's containers of this user are judged: the run label present
// (the listing's filter, checked again here) with a run id of the tool's own
// shape, and the owner label equal to this user's uid. Any other container
// is counted as foreign and never touched. One of ours with a start or
// deadline label that is missing, not digits, or not a bound this tool could
// have written (a deadline before the start, or more than maxDeadline after
// it) is left and reported as unreadable. One whose deadline plus the grace
// is still ahead is left alone.
func judge(cs []listed, now time.Time, grace time.Duration, ownerID string) (out []verdict, foreign int) {
	for _, c := range cs {
		run, ok := c.Labels[labelRun]
		if !ok {
			continue
		}
		if !runIDRE.MatchString(run) || c.Labels[labelOwner] != ownerID {
			foreign++
			continue
		}
		v := verdict{id: c.ID, run: run, state: c.State}
		start, sok := unixLabel(c.Labels[labelStart])
		deadline, dok := unixLabel(c.Labels[labelDeadline])
		switch {
		case !dok:
			v.reason = "unreadable-deadline"
		case !sok:
			v.reason = "unreadable-start"
		case deadline < start || time.Duration(deadline-start)*time.Second > maxDeadline:
			v.reason = "unreadable-bound"
		case now.After(time.Unix(deadline, 0).Add(grace)):
			v.remove = true
		default:
			v.reason = "within-deadline"
		}
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].id < out[j].id })
	return out, foreign
}

// reap removes every container of this tool and this user past its deadline
// plus the grace, in any state, and returns how many it removed (or would, on
// a dry run) and how many of ours it left with an unreadable label. It never
// touches another container, never removes a volume by itself (a removed
// container's anonymous volumes go with it), and never kills a process.
func reap(ctx context.Context, eng engine, now time.Time, grace time.Duration, ownerID string, dryRun bool, stderr io.Writer) (reaped, unreadable int, err error) {
	lctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := eng.Output(lctx, reapListArgs()...)
	if err != nil {
		return 0, 0, err
	}
	cs, err := parseListed(out)
	if err != nil {
		return 0, 0, err
	}
	verdicts, foreign := judge(cs, now, grace, ownerID)
	left := 0
	for _, v := range verdicts {
		if !v.remove {
			left++
			if v.reason != "within-deadline" {
				unreadable++
				fmt.Fprintf(stderr, "REAP-UNREADABLE id=%s run=%s state=%s reason=%s\n", short(v.id), v.run, v.state, v.reason)
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
	fmt.Fprintf(stderr, "REAP containers=%d reaped=%d left=%d unreadable=%d foreign=%d dry_run=%t\n", len(cs), reaped, left, unreadable, foreign, dryRun)
	return reaped, unreadable, nil
}

func short(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}
