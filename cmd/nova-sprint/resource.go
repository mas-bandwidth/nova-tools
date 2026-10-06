package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// The resource verbs (docs/SPEC-SPRINT.md section 11, Resources; internal/sprint/resource.go;
// the model is tla/Resources.tla). The table and verbs.go register them; init gives each
// its class and effect line.
func init() {
	verbClasses["resource add"] = classCoordinator
	verbClasses["resource claim"] = classWorker
	verbClasses["resource renew"] = classWorker
	verbClasses["resource release"] = classWorker
	verbClasses["resource list"] = classRead
	verbEffect["resource add"] = "local write: adds the resource to the sprint's resources table, or sets its capacity"
	verbEffect["resource claim"] = "local write: grants the member a lease of the resource in the sprint's store, or puts it in the resource's line"
	verbEffect["resource renew"] = "local write: moves the member's live lease of the resource on in the sprint's store"
	verbEffect["resource release"] = "local write: gives the member's lease of the resource back, or takes it out of the line, and grants the room to the line's head"
	verbEffect["resource list"] = "inspection: reads the resources table, writes nothing"
}

// resourceWords is resource in nova-sprint help.
func resourceWords() string {
	return strings.TrimSpace(fmt.Sprintf(`
Resources: a shared thing (a bench machine or directory, a branch, a port, a provider
account) is a row of the coordinator's resources table, held only by a lease these
verbs grant; no member holds one by agreement with another, and a brief never tells a
friend to wait for a friend: it claims through the verb.
  resource add <name> --kind bench|branch|port|account --capacity <n>
    the coordinator's: adds it, or sets its capacity (a larger one grants the line).
  resource claim <name> --as <member> --for <duration>
    granted at once (exit 0, GRANTED) while it has room and nobody waits; else the
    member is put in line (exit 0, WAITING place=<n>) and never polls: the release or
    the tick that frees the room grants the head in the same write and tells it by a
    note ("%s"). A lease is at most %s.
  resource renew <name> --as <member> --for <duration>
    moves a live lease's expiry to now+duration; a lease past its expiry is not renewed.
  resource release <name> --as <member>
    gives the lease back (or leaves the line); the room goes to the line's head at once.
  resource list [--json]
    every resource, its holders and their expiries, and its line in order.
The tick releases, at once, a lease past its expiry and every lease and place in line
of a member down, held or out of credit, and grants the room to the line. A line that
waits with no room for %s is one judgment for the coordinator ("%s").
The model is tla/Resources.tla.`, sprint.NResourceGranted, sprint.ResourceMaxLease, sprint.ResourceStarveAfter, sprint.NResourceStarved)) + "\n"
}

// resourceRefused is a change the resources table said no to, or could not make: the
// REFUSED line with the next command, exit 1 (a usage refusal is refuse's, exit 2).
func resourceRefused(stderr io.Writer, verb string, err error) int {
	refuse(stderr, verb, err.Error())
	return 1
}

// resourceName is the one resource a verb names.
func resourceName(verb string, pos []string, stderr io.Writer) (string, int) {
	if len(pos) != 1 {
		return "", refuse(stderr, verb, argErr("wants one resource name", nil, pos...)+"; run: nova-sprint resource list")
	}
	return pos[0], 0
}

// cmdResourceAdd is resource add <name> --kind <kind> --capacity <n>.
func (a *app) cmdResourceAdd(args []string, stdout, stderr io.Writer) int {
	const name = "resource add"
	fs, c := a.verbSetup(name)
	kind := fs.String("kind", "", "what it is: "+strings.Join(sprint.ResourceKinds, ", ")+" (required)")
	capacity := fs.Int("capacity", 1, "how many members may hold it at once, from 1")
	pos, err := parse(fs, args)
	if err != nil {
		return refuse(stderr, name, err.Error())
	}
	res, code := resourceName(name, pos, stderr)
	if code != 0 {
		return code
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, name, err.Error())
	}
	evs, err := st.ResourceAdd(context.Background(), res, *kind, *capacity)
	if err != nil {
		return resourceRefused(stderr, name, err)
	}
	sayOK(stdout, c.json, name, fmt.Sprintf("RESOURCE %s kind=%s capacity=%d granted=%d", res, *kind, *capacity, len(evs)), map[string]any{"resource": res, "kind": *kind, "capacity": *capacity, "events": evs})
	return 0
}

// cmdResourceClaim is resource claim <name> --as <member> --for <duration>.
func (a *app) cmdResourceClaim(args []string, stdout, stderr io.Writer) int {
	const name = "resource claim"
	fs, c := a.verbSetup(name)
	as := fs.String("as", "", "the member claiming: a friend, a fleet member or the coordinator (required)")
	d := fs.Duration("for", 0, fmt.Sprintf("how long the lease holds from its grant, above 0 and at most %s (required)", sprint.ResourceMaxLease))
	pos, err := parse(fs, args)
	if err != nil {
		return refuse(stderr, name, err.Error())
	}
	res, code := resourceName(name, pos, stderr)
	if code != 0 {
		return code
	}
	if *as == "" {
		return refuse(stderr, name, "wants --as <member>, the member claiming")
	}
	c.orActor(*as)
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, name, err.Error())
	}
	ev, err := st.ResourceClaim(context.Background(), res, *as, *d)
	if err != nil {
		return resourceRefused(stderr, name, err)
	}
	return sayResourceEvent(stdout, c.json, name, ev)
}

// cmdResourceRenew is resource renew <name> --as <member> --for <duration>.
func (a *app) cmdResourceRenew(args []string, stdout, stderr io.Writer) int {
	const name = "resource renew"
	fs, c := a.verbSetup(name)
	as := fs.String("as", "", "the member holding the lease (required)")
	d := fs.Duration("for", 0, fmt.Sprintf("the lease's new length from now, above 0 and at most %s (required)", sprint.ResourceMaxLease))
	pos, err := parse(fs, args)
	if err != nil {
		return refuse(stderr, name, err.Error())
	}
	res, code := resourceName(name, pos, stderr)
	if code != 0 {
		return code
	}
	if *as == "" {
		return refuse(stderr, name, "wants --as <member>, the member holding the lease")
	}
	c.orActor(*as)
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, name, err.Error())
	}
	ev, err := st.ResourceRenew(context.Background(), res, *as, *d)
	if err != nil {
		return resourceRefused(stderr, name, err)
	}
	return sayResourceEvent(stdout, c.json, name, ev)
}

// cmdResourceRelease is resource release <name> --as <member>.
func (a *app) cmdResourceRelease(args []string, stdout, stderr io.Writer) int {
	const name = "resource release"
	fs, c := a.verbSetup(name)
	as := fs.String("as", "", "the member giving its lease back, or leaving the line (required)")
	pos, err := parse(fs, args)
	if err != nil {
		return refuse(stderr, name, err.Error())
	}
	res, code := resourceName(name, pos, stderr)
	if code != 0 {
		return code
	}
	if *as == "" {
		return refuse(stderr, name, "wants --as <member>, the member releasing")
	}
	c.orActor(*as)
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, name, err.Error())
	}
	evs, err := st.ResourceRelease(context.Background(), res, *as)
	if err != nil {
		return resourceRefused(stderr, name, err)
	}
	var granted []string
	for _, e := range evs {
		if e.Kind == sprint.ResourceGranted {
			granted = append(granted, e.Member)
		}
	}
	sayOK(stdout, c.json, name, fmt.Sprintf("RELEASED %s by=%s granted=%s", res, *as, dashed(strings.Join(granted, ","))), map[string]any{"resource": res, "member": *as, "events": evs})
	return 0
}

// sayResourceEvent is a claim's or renew's line: GRANTED until, or WAITING place.
func sayResourceEvent(stdout io.Writer, asJSON bool, verb string, ev sprint.ResourceEvent) int {
	line := fmt.Sprintf("GRANTED %s to=%s until=%s", ev.Resource, ev.Member, ev.Until.UTC().Format(time.RFC3339))
	if ev.Kind == sprint.ResourceWaiting {
		line = fmt.Sprintf("WAITING %s as=%s place=%d (granted by the release or tick that frees the room; a note to %s says so)", ev.Resource, ev.Member, ev.Place, ev.Member)
	}
	sayOK(stdout, asJSON, verb, line, map[string]any{"event": ev})
	return 0
}

// cmdResourceList is resource list [--json].
func (a *app) cmdResourceList(args []string, stdout, stderr io.Writer) int {
	const name = "resource list"
	fs, c := a.verbSetup(name)
	pos, err := parse(fs, args)
	if err != nil || len(pos) > 0 {
		return refuse(stderr, name, argErr("takes no words", err, pos...))
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, name, err.Error())
	}
	rows, err := st.Resources(context.Background())
	if err != nil {
		return a.readFailed(name, err, stderr)
	}
	if c.json {
		if rows == nil {
			rows = []store.ResourceRow{}
		}
		b, err := json.Marshal(map[string]any{"resources": rows})
		if err != nil {
			return a.readFailed(name, err, stderr)
		}
		fmt.Fprintln(stdout, string(b))
		return 0
	}
	if len(rows) == 0 {
		fmt.Fprintln(stdout, "no resources; the coordinator adds one: nova-sprint resource add <name> --kind <kind> --capacity <n>")
		return 0
	}
	for _, r := range rows {
		var holders, line []string
		for _, l := range r.Holders {
			h := l.Member + " until " + l.Until.UTC().Format(time.RFC3339)
			for _, e := range r.Expired {
				if e == l.Member {
					h += " (expired: the next tick releases it)"
				}
			}
			holders = append(holders, h)
		}
		for _, w := range r.Line {
			line = append(line, fmt.Sprintf("%s (for %s, since %s)", w.Member, w.For, w.Since.UTC().Format(time.RFC3339)))
		}
		fmt.Fprintf(stdout, "%s kind=%s capacity=%d held=%d waiting=%d\n", r.Name, r.Kind, r.Capacity, len(r.Holders), len(r.Line))
		fmt.Fprintf(stdout, "  holders: %s\n", dashed(strings.Join(holders, "; ")))
		fmt.Fprintf(stdout, "  line: %s\n", dashed(strings.Join(line, "; ")))
	}
	return 0
}
