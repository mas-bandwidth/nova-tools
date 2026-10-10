package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
)

// The lanes' verbs (docs/SPEC-SPRINT.md section 18; internal/sprint/lane.go): lane take
// and lane give, a worker's, which the server runs one at a time beside the store, and
// lane list, a read. A take with --wait asks again every sprint.LaneAskEvery until it is
// granted or the wait is over; each ask is a plain take, run where a take runs (through
// the server under NOVA_SPRINT_SERVER), so the server never waits.

// laneWords is nova-sprint help lane, and the banner's paragraph on the lanes.
func laneWords() string {
	return strings.TrimSpace(fmt.Sprintf(`
The lanes: one Go build or test stream per machine, a lock the machine grants:
  nova-sprint lane take go --machine <m> --as <worker> --wait 30m
    a store write: granted (exit 0) while the machine's holders are under its
    width (set --go-lanes, %d by default) and nobody waits ahead; otherwise
    queued behind the waiters in the order they asked (exit 1, the place in
    the queue). --wait asks again every %s until granted or the wait is over.
  nova-sprint lane give go --machine <m> --as <worker>
    gives the lane (or the place in the queue) back when the run exits; the
    head of the queue is granted. A holder that does not take again within %s
    is released, and a grant or a place not claimed within %s.
  nova-sprint lane list
    every machine's holders and queue; where --json --cards carries them as lanes.`,
		sprint.LaneWidthDefault, sprint.LaneAskEvery, sprint.LaneHoldFor, sprint.LaneWaitFor)) + "\n"
}

// laneArgs is a take's or a give's kind, machine and worker, or the refusal's code.
func laneArgs(name string, fs flagSet, args []string, stderr io.Writer, machine, as *string) (string, int) {
	pos, err := parse(fs, args)
	if err != nil {
		return "", refuse(stderr, name, err.Error())
	}
	var why []string
	if len(pos) != 1 || !slices.Contains(sprint.LaneKinds, pos[0]) {
		why = append(why, "wants one lane kind, one of "+strings.Join(sprint.LaneKinds, ", "))
	}
	if !sprint.ValidID(*machine) {
		why = append(why, "--machine wants the machine the run is on (letters, digits, _ and -)")
	}
	if !sprint.ValidLaneWho(*as) {
		why = append(why, "--as wants the worker asking (letters, digits, _ and -, or two such joined by one /, as the lander's lander/<stream>)")
	}
	if len(why) > 0 {
		return "", refuse(stderr, name, strings.Join(why, "; "))
	}
	return pos[0], 0
}

func (a *app) cmdLaneTake(args []string, stdout, stderr io.Writer) int {
	const name = "lane take"
	fs, c := a.verbSetup(name)
	machine := fs.String("machine", "", "the machine the run is on: its lanes are its own")
	as := fs.String("as", "", "the worker asking, the holder or waiter the lane records")
	wait := fs.Duration("wait", 0, fmt.Sprintf("how long to wait for the grant, asking again every %s (30m); 0, the default, asks once", sprint.LaneAskEvery))
	dry := fs.Bool("dry-run", false, "check the machine's lanes and say whether the take would be granted or queued; write nothing")
	kind, code := laneArgs(name, fs, args, stderr, machine, as)
	if code != 0 {
		return code
	}
	if *wait < 0 {
		return refuse(stderr, name, "--wait wants a duration from 0, found "+wait.String())
	}
	if *wait > 0 && !*dry {
		return a.laneWait(args, *wait, stdout, stderr)
	}
	c.orActor(*as)
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, name, err.Error())
	}
	if *dry {
		rows, err := st.LaneRows(context.Background())
		if err != nil {
			return a.readFailed(name, err, stderr)
		}
		width, err := st.LaneWidth(context.Background())
		if err != nil {
			return a.readFailed(name, err, stderr)
		}
		var held, waiting []string
		for _, r := range rows {
			if r.Kind == kind && r.Machine == *machine {
				held, waiting, width = r.Held, r.Waiting, r.Width
				break
			}
		}
		granted := false
		place := 0
		if slices.Contains(held, *as) {
			granted = true
		} else if i := slices.Index(waiting, *as); i >= 0 {
			place = i + 1
		} else if len(held) < width && len(waiting) == 0 {
			granted = true
		} else {
			place = len(waiting) + 1
		}
		facts := map[string]any{"kind": kind, "machine": *machine, "as": *as, "would_grant": granted, "held": len(held), "width": width, "dry_run": true}
		line := fmt.Sprintf("LANE-TAKE DRY-RUN %s machine=%s as=%s would_grant=%s held=%d/%d; nothing was written", kind, *machine, *as, yesNo(granted), len(held), width)
		if !granted {
			facts["place"] = place
			line = fmt.Sprintf("LANE-TAKE DRY-RUN %s machine=%s as=%s would_grant=%s place=%d held=%d/%d; nothing was written", kind, *machine, *as, yesNo(granted), place, len(held), width)
		}
		sayOK(stdout, c.json, name, line, facts)
		return 0
	}
	ans, err := st.LaneStep(context.Background(), kind, *machine, *as, false)
	if err != nil {
		fmt.Fprintf(stderr, "%s %s: %s; run: nova-sprint help lane\n", prog, name, oneline.Escape(err.Error()))
		return 1
	}
	facts := map[string]any{"kind": kind, "machine": *machine, "as": *as, "granted": ans.Granted, "held": ans.Held, "width": ans.Width, "released": nonNil(ans.Released)}
	if !ans.Granted {
		facts["place"] = ans.Place
		why := fmt.Sprintf("the %s lanes of %s are held (%d/%d); %s is %s in the queue, which keeps the place while it asks again within %s", kind, *machine, ans.Held, ans.Width, *as, ordinal(ans.Place), sprint.LaneWaitFor)
		return sayLaneRefused(stdout, stderr, c.json, name, why, "nova-sprint lane take "+kind+" --machine "+*machine+" --as "+*as+" --wait 30m", facts)
	}
	sayOK(stdout, c.json, name, fmt.Sprintf("LANE-TAKE OK %s machine=%s as=%s held=%d/%d: run, then nova-sprint lane give %s --machine %s --as %s", kind, *machine, *as, ans.Held, ans.Width, kind, *machine, *as), facts)
	return 0
}

// laneWait is lane take --wait: the take asked again every sprint.LaneAskEvery, each
// ask the verb with no --wait run as it would be typed (so through the server under
// NOVA_SPRINT_SERVER), until it is granted, or refused for a reason that is not the
// queue, or the wait is over; what the last ask printed is printed.
func (a *app) laneWait(args []string, wait time.Duration, stdout, stderr io.Writer) int {
	once := append([]string{"lane", "take"}, withoutFlag(args, "wait")...)
	end := a.now().Add(wait)
	for {
		var o, e bytes.Buffer
		code := a.run(once, &o, &e)
		if code != 1 || !strings.Contains(e.String()+o.String(), "in the queue") || !a.now().Before(end) {
			// ignored: the buffers are what the ask printed; a short write to the caller's stdout or stderr is the caller's
			_, _ = io.Copy(stdout, &o)
			// ignored: as above, the caller's stderr
			_, _ = io.Copy(stderr, &e)
			return code
		}
		a.sleep(min(sprint.LaneAskEvery, end.Sub(a.now())))
	}
}

// withoutFlag is the words with the flag name and its value taken out (--name v,
// --name=v, one dash or two).
func withoutFlag(words []string, name string) []string {
	var out []string
	for i := 0; i < len(words); i++ {
		n, _, eq := strings.Cut(strings.TrimLeft(words[i], "-"), "=")
		if strings.HasPrefix(words[i], "-") && n == name {
			if !eq {
				i++
			}
			continue
		}
		out = append(out, words[i])
	}
	return out
}

func (a *app) cmdLaneGive(args []string, stdout, stderr io.Writer) int {
	const name = "lane give"
	fs, c := a.verbSetup(name)
	machine := fs.String("machine", "", "the machine the run was on")
	as := fs.String("as", "", "the worker giving its lane, or its place in the queue, back")
	dry := fs.Bool("dry-run", false, "check whether the worker holds a lane or waits in the queue, and write nothing")
	kind, code := laneArgs(name, fs, args, stderr, machine, as)
	if code != 0 {
		return code
	}
	c.orActor(*as)
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, name, err.Error())
	}
	if *dry {
		rows, err := st.LaneRows(context.Background())
		if err != nil {
			return a.readFailed(name, err, stderr)
		}
		width, err := st.LaneWidth(context.Background())
		if err != nil {
			return a.readFailed(name, err, stderr)
		}
		var held, waiting []string
		for _, r := range rows {
			if r.Kind == kind && r.Machine == *machine {
				held, waiting, width = r.Held, r.Waiting, r.Width
				break
			}
		}
		holds := slices.Contains(held, *as)
		waits := slices.Contains(waiting, *as)
		wouldGive := holds || waits
		line := fmt.Sprintf("LANE-GIVE DRY-RUN %s machine=%s as=%s would_give=%s held=%d/%d; nothing was written", kind, *machine, *as, yesNo(wouldGive), len(held), width)
		if !wouldGive {
			line += ": it holds no lane and waits in no queue there"
		}
		facts := map[string]any{"kind": kind, "machine": *machine, "as": *as, "would_give": wouldGive, "held": len(held), "width": width, "dry_run": true}
		sayOK(stdout, c.json, name, line, facts)
		return 0
	}
	ans, err := st.LaneStep(context.Background(), kind, *machine, *as, true)
	if err != nil {
		fmt.Fprintf(stderr, "%s %s: %s; run: nova-sprint help lane\n", prog, name, oneline.Escape(err.Error()))
		return 1
	}
	line := fmt.Sprintf("LANE-GIVE OK %s machine=%s as=%s gave=%s held=%d/%d", kind, *machine, *as, yesNo(ans.Gave), ans.Held, ans.Width)
	if !ans.Gave {
		line += ": it held no lane and waited in no queue there (a timeout may have released it)"
	}
	sayOK(stdout, c.json, name, line, map[string]any{"kind": kind, "machine": *machine, "as": *as, "gave": ans.Gave, "held": ans.Held, "width": ans.Width, "released": nonNil(ans.Released)})
	return 0
}

func (a *app) cmdLaneList(args []string, stdout, stderr io.Writer) int {
	const name = "lane list"
	fs, c := a.verbSetup(name)
	pos, err := parse(fs, args)
	if err != nil {
		return refuse(stderr, name, err.Error())
	}
	if len(pos) > 0 {
		return refuse(stderr, name, "takes no positional words: it lists every machine's lanes")
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, name, err.Error())
	}
	rows, err := st.LaneRows(context.Background())
	if err != nil {
		fmt.Fprintf(stderr, "%s %s: %s; run: nova-sprint lane list\n", prog, name, oneline.Escape(err.Error()))
		return 2
	}
	if c.json {
		b, err := json.Marshal(map[string]any{"verb": name, "status": "ok", "exit": 0, "lanes": rows})
		if err != nil {
			fmt.Fprintf(stderr, "%s %s: %s; run: nova-sprint lane list\n", prog, name, oneline.Escape(err.Error()))
			return 2
		}
		fmt.Fprintln(stdout, string(b))
		return 0
	}
	for _, r := range rows {
		l := fmt.Sprintf("LANE %s machine=%s held=%s width=%d waiting=%s", r.Kind, r.Machine, orDashStr(strings.Join(r.Held, ","), "-"), r.Width, orDashStr(strings.Join(r.Waiting, ","), "-"))
		if !r.Since.IsZero() {
			l += " since=" + r.Since.Format(time.RFC3339)
		}
		fmt.Fprintln(stdout, l)
	}
	fmt.Fprintf(stdout, "LANE-LIST OK machines=%d: a machine with no holder and no queue is not listed\n", len(rows))
	return 0
}

// sayLaneRefused is a take the lane said no to (exit 1): the REFUSED line on stderr
// with the next command, or under --json the one value on stdout.
func sayLaneRefused(stdout, stderr io.Writer, asJSON bool, verbName, why, next string, facts map[string]any) int {
	if !asJSON {
		fmt.Fprintf(stderr, "%s %s REFUSED: %s; run: %s\n", prog, verbName, oneline.Escape(why), next)
		return 1
	}
	o := map[string]any{"verb": verbName, "status": "refused", "exit": 1, "reason": why, "remedy": next}
	for k, v := range facts {
		o[k] = v
	}
	// ignored: a map of strings, numbers, booleans and lists of strings always encodes
	b, _ := json.Marshal(o)
	fmt.Fprintln(stdout, string(b))
	return 1
}

// ordinal is 1st, 2nd, 3rd, 4th ... for a place in a queue.
func ordinal(n int) string {
	suffix := "th"
	switch {
	case n%100 >= 11 && n%100 <= 13:
	case n%10 == 1:
		suffix = "st"
	case n%10 == 2:
		suffix = "nd"
	case n%10 == 3:
		suffix = "rd"
	}
	return strconv.Itoa(n) + suffix
}

// yesNo is a boolean as the lines say it.
func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}
