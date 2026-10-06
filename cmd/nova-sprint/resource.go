package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// The resources' verbs (docs/SPEC-SPRINT.md section 19; internal/sprint/resource.go;
// the model is tla/Resources.tla): resource add, the coordinator's, and resource
// claim, renew and release, a member's, each one read and one write of the record,
// which the server runs one at a time beside the store; resource list, a read. A
// friend's brief never tells her to wait for another friend: she claims through the
// verb, and the coordinator's tick grants her when there is room.

// resourceWords is nova-sprint help resource, and the banner's paragraph on the
// resources.
func resourceWords() string {
	return strings.TrimSpace(fmt.Sprintf(`
The resources: a shared resource (a bench machine or directory, a branch, a
port, a provider account) is a row of the resources table, managed by these
verbs alone; no member holds one by agreement with another member. Every
hold is a lease with an expiry; the tick releases a lease at its expiry, or
at once when its holder is down (held, out of credit, no session evidence),
and grants the next in line.
  nova-sprint resource add <name> --kind bench|branch|port|account --capacity <n>
    the coordinator's: a row at that capacity (how many hold it at once); on
    a row that is there, --capacity sets it, a wider one granting the line;
    one below the holders is refused, naming them (release first), so the
    holders never outnumber the capacity.
  nova-sprint resource claim <name> --as <member> --for <duration> [--wait <duration>]
    a lease of the length asked: granted (exit 0, until=) while the resource
    has room and nobody waits ahead; otherwise the member joins the back of
    the line (exit 1, its place) and keeps the place without asking again:
    the grant is the coordinator's, made at a release, an expiry or a holder
    going down. --wait asks again every %s until the grant or the wait is
    over; an ask again keeps the place. A holder's claim renews its lease.
  nova-sprint resource renew <name> --as <member> --for <duration>
    the lease runs that long again from now; a member that holds nothing is
    refused (a renewal never joins the line).
  nova-sprint resource release <name> --as <member>
    the lease, or the place in the line, given back; the head is granted.
  nova-sprint resource remove <name>
    the coordinator's: the row off the table, refused while it is held or waited for.
  nova-sprint resource list [--json]
    every resource with its holders, their expiries and its line, in order.
A resource with waiters and no room for %s raises one judgment to the
coordinator (release a holder, add capacity, or ack); a lease is at most %s.`,
		sprint.ResourceWatchEvery, sprint.ResourceStarveBound, sprint.ResourceLeaseMax)) + "\n"
}

func (a *app) cmdResourceAdd(args []string, stdout, stderr io.Writer) int {
	const name = "resource add"
	fs, c := a.verbSetup(name)
	kind := fs.String("kind", "", "what the resource is: bench, branch, port or account (a new row wants one)")
	capacity := fs.Int("capacity", 1, "how many members hold it at once, from 1")
	dry := fs.Bool("dry-run", false, "check the row and the flags and say what would be written; write nothing")
	pos, err := parse(fs, args)
	if err != nil {
		return refuse(stderr, name, err.Error())
	}
	if len(pos) != 1 {
		return refuse(stderr, name, "wants one resource name")
	}
	if why := sprint.ResourceWhy(pos[0], *kind, *capacity, ""); why != "" {
		return refuse(stderr, name, why)
	}
	if *capacity < 1 {
		return refuse(stderr, name, fmt.Sprintf("--capacity wants a count from 1, found %d", *capacity))
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, name, err.Error())
	}
	if *dry {
		rows, err := st.ResourceRows(context.Background())
		if err != nil {
			return a.readFailed(name, err, stderr)
		}
		had := false
		for _, r := range rows {
			if r.Name != pos[0] {
				continue
			}
			had = true
			var holders []string
			for _, h := range r.Holders {
				holders = append(holders, h.Who)
			}
			if why := sprint.CapacityWhy(pos[0], *capacity, holders); why != "" {
				fmt.Fprintf(stderr, "%s %s: %s; run: nova-sprint help resource\n", prog, name, oneline.Escape(why))
				return 1
			}
		}
		if !had && *kind == "" {
			return refuse(stderr, name, "a new row wants --kind bench|branch|port|account")
		}
		facts := map[string]any{"name": pos[0], "kind": *kind, "capacity": *capacity, "exists": had, "dry_run": true}
		sayOK(stdout, c.json, name, fmt.Sprintf("RESOURCE-ADD DRY-RUN %s kind=%s capacity=%d exists=%s; nothing was written", pos[0], dashed(*kind), *capacity, yesNo(had)), facts)
		return 0
	}
	ch, err := st.ResourceAdd(context.Background(), pos[0], *kind, *capacity)
	if err != nil {
		fmt.Fprintf(stderr, "%s %s: %s; run: nova-sprint help resource\n", prog, name, oneline.Escape(err.Error()))
		return 1
	}
	facts := map[string]any{"name": pos[0], "kind": *kind, "capacity": *capacity, "granted": nonNil(ch.Granted)}
	line := fmt.Sprintf("RESOURCE-ADD OK %s kind=%s capacity=%d", pos[0], dashed(*kind), *capacity)
	if len(ch.Granted) > 0 {
		line += " granted=" + strings.Join(ch.Granted, ",")
	}
	sayOK(stdout, c.json, name, line, facts)
	return 0
}

func (a *app) cmdResourceRemove(args []string, stdout, stderr io.Writer) int {
	const name = "resource remove"
	fs, c := a.verbSetup(name)
	dry := fs.Bool("dry-run", false, "say whether the row could be removed; write nothing")
	pos, err := parse(fs, args)
	if err != nil {
		return refuse(stderr, name, err.Error())
	}
	if len(pos) != 1 || !sprint.ValidID(pos[0]) {
		return refuse(stderr, name, "wants one resource name (letters, digits, _ and -)")
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, name, err.Error())
	}
	if *dry {
		rows, err := st.ResourceRows(context.Background())
		if err != nil {
			return a.readFailed(name, err, stderr)
		}
		for _, r := range rows {
			if r.Name == pos[0] {
				busy := len(r.Holders)+len(r.Waiters) > 0
				sayOK(stdout, c.json, name, fmt.Sprintf("RESOURCE-REMOVE DRY-RUN %s would_remove=%s held=%d waiting=%d; nothing was written", pos[0], yesNo(!busy), len(r.Holders), len(r.Waiters)),
					map[string]any{"name": pos[0], "would_remove": !busy, "held": len(r.Holders), "waiting": len(r.Waiters), "dry_run": true})
				return 0
			}
		}
		fmt.Fprintf(stderr, "%s %s: no resource %s on the table; run: nova-sprint resource list\n", prog, name, pos[0])
		return 1
	}
	if err := st.ResourceRemove(context.Background(), pos[0]); err != nil {
		fmt.Fprintf(stderr, "%s %s: %s; run: nova-sprint resource list\n", prog, name, oneline.Escape(err.Error()))
		return 1
	}
	sayOK(stdout, c.json, name, "RESOURCE-REMOVE OK "+pos[0], map[string]any{"name": pos[0]})
	return 0
}

// resourceArgs is a claim's, a renew's or a release's resource and member, or the
// refusal's code.
func resourceArgs(name string, fs flagSet, args []string, stderr io.Writer, as *string) (string, int) {
	pos, err := parse(fs, args)
	if err != nil {
		return "", refuse(stderr, name, err.Error())
	}
	var why []string
	if len(pos) != 1 || !sprint.ValidID(pos[0]) {
		why = append(why, "wants one resource name (letters, digits, _ and -)")
	}
	if !sprint.ValidID(*as) {
		why = append(why, "--as wants the member claiming (letters, digits, _ and -)")
	}
	if len(why) > 0 {
		return "", refuse(stderr, name, strings.Join(why, "; "))
	}
	return pos[0], 0
}

func (a *app) cmdResourceClaim(args []string, stdout, stderr io.Writer) int {
	const name = "resource claim"
	fs, c := a.verbSetup(name)
	as := fs.String("as", "", "the member claiming: the holder or waiter the table records")
	lease := fs.Duration("for", 0, "the lease's length (2h): the hold ends then unless renewed; the tick releases it")
	wait := fs.Duration("wait", 0, fmt.Sprintf("how long to wait for the grant, asking again every %s (30m), each ask keeping the place; 0, the default, claims once and reports the place", sprint.ResourceWatchEvery))
	dry := fs.Bool("dry-run", false, "say whether the claim would be granted or queued; write nothing")
	res, code := resourceArgs(name, fs, args, stderr, as)
	if code != 0 {
		return code
	}
	if why := sprint.LeaseWhy(*lease); why != "" {
		return refuse(stderr, name, why)
	}
	if *wait < 0 {
		return refuse(stderr, name, "--wait wants a duration from 0, found "+wait.String())
	}
	if *wait > 0 && !*dry {
		return a.resourceWait(args, *wait, stdout, stderr)
	}
	c.orActor(*as)
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, name, err.Error())
	}
	if *dry {
		rows, err := st.ResourceRows(context.Background())
		if err != nil {
			return a.readFailed(name, err, stderr)
		}
		for _, r := range rows {
			if r.Name != res {
				continue
			}
			granted, place := false, 0
			switch {
			case holdsLease(r, *as):
				granted = true
			case placeOf(r, *as) > 0:
				place = placeOf(r, *as)
			case len(r.Holders) < r.Capacity && len(r.Waiters) == 0:
				granted = true
			default:
				place = len(r.Waiters) + 1
			}
			facts := map[string]any{"name": res, "as": *as, "would_grant": granted, "held": len(r.Holders), "capacity": r.Capacity, "dry_run": true}
			line := fmt.Sprintf("RESOURCE-CLAIM DRY-RUN %s as=%s would_grant=%s held=%d/%d; nothing was written", res, *as, yesNo(granted), len(r.Holders), r.Capacity)
			if !granted {
				facts["place"] = place
				line = fmt.Sprintf("RESOURCE-CLAIM DRY-RUN %s as=%s would_grant=no place=%d held=%d/%d; nothing was written", res, *as, place, len(r.Holders), r.Capacity)
			}
			sayOK(stdout, c.json, name, line, facts)
			return 0
		}
		fmt.Fprintf(stderr, "%s %s: no resource %s on the table; run: nova-sprint resource list\n", prog, name, res)
		return 1
	}
	ans, err := st.ResourceClaim(context.Background(), res, *as, *lease)
	if err != nil {
		fmt.Fprintf(stderr, "%s %s: %s; run: nova-sprint help resource\n", prog, name, oneline.Escape(err.Error()))
		return 1
	}
	return sayResource(stdout, stderr, c.json, name, "RESOURCE-CLAIM", res, *as, ans)
}

// resourceWait is resource claim --wait: the claim asked again every
// sprint.ResourceWatchEvery, each ask the verb with no --wait run as it would be
// typed (so through the server under NOVA_SPRINT_SERVER), until it is granted, or
// refused for a reason that is not the line, or the wait is over; what the last
// ask printed is printed. A claim again keeps the place: the ask reads the grant
// the tick made, it never moves the line.
func (a *app) resourceWait(args []string, wait time.Duration, stdout, stderr io.Writer) int {
	once := append([]string{"resource", "claim"}, withoutFlag(args, "wait")...)
	end := a.now().Add(wait)
	for {
		var o, e bytes.Buffer
		code := a.run(once, &o, &e)
		if code != 1 || !strings.Contains(e.String()+o.String(), "in the line") || !a.now().Before(end) {
			// ignored: the buffers are what the ask printed; a short write to the caller's stdout or stderr is the caller's
			_, _ = io.Copy(stdout, &o)
			// ignored: as above, the caller's stderr
			_, _ = io.Copy(stderr, &e)
			return code
		}
		a.sleep(min(sprint.ResourceWatchEvery, end.Sub(a.now())))
	}
}

func (a *app) cmdResourceRenew(args []string, stdout, stderr io.Writer) int {
	const name = "resource renew"
	fs, c := a.verbSetup(name)
	as := fs.String("as", "", "the member that holds the lease")
	lease := fs.Duration("for", 0, "the lease's length from now (2h)")
	dry := fs.Bool("dry-run", false, "say whether the member holds a lease to renew; write nothing")
	res, code := resourceArgs(name, fs, args, stderr, as)
	if code != 0 {
		return code
	}
	if why := sprint.LeaseWhy(*lease); why != "" {
		return refuse(stderr, name, why)
	}
	c.orActor(*as)
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, name, err.Error())
	}
	if *dry {
		return a.resourceDry(st, name, "RESOURCE-RENEW", res, *as, c.json, stdout, stderr, func(r sprint.ResourceRow) bool { return holdsLease(r, *as) })
	}
	ans, err := st.ResourceRenew(context.Background(), res, *as, *lease)
	if err != nil {
		fmt.Fprintf(stderr, "%s %s: %s; run: nova-sprint help resource\n", prog, name, oneline.Escape(err.Error()))
		return 1
	}
	return sayResource(stdout, stderr, c.json, name, "RESOURCE-RENEW", res, *as, ans)
}

func (a *app) cmdResourceRelease(args []string, stdout, stderr io.Writer) int {
	const name = "resource release"
	fs, c := a.verbSetup(name)
	as := fs.String("as", "", "the member that holds the lease, or waits")
	dry := fs.Bool("dry-run", false, "say whether the member holds or waits; write nothing")
	res, code := resourceArgs(name, fs, args, stderr, as)
	if code != 0 {
		return code
	}
	c.orActor(*as)
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, name, err.Error())
	}
	if *dry {
		return a.resourceDry(st, name, "RESOURCE-RELEASE", res, *as, c.json, stdout, stderr, func(r sprint.ResourceRow) bool { return holdsLease(r, *as) || placeOf(r, *as) > 0 })
	}
	ans, err := st.ResourceRelease(context.Background(), res, *as)
	if err != nil {
		fmt.Fprintf(stderr, "%s %s: %s; run: nova-sprint help resource\n", prog, name, oneline.Escape(err.Error()))
		return 1
	}
	facts := map[string]any{"name": res, "as": *as, "gave": ans.Gave, "held": ans.Held, "capacity": ans.Capacity, "granted_now": nonNil(ans.GrantedNow)}
	line := fmt.Sprintf("RESOURCE-RELEASE OK %s as=%s gave=%s held=%d/%d", res, *as, yesNo(ans.Gave), ans.Held, ans.Capacity)
	if len(ans.GrantedNow) > 0 {
		line += " granted=" + strings.Join(ans.GrantedNow, ",")
	}
	sayOK(stdout, c.json, name, line, facts)
	return 0
}

func (a *app) cmdResourceList(args []string, stdout, stderr io.Writer) int {
	const name = "resource list"
	fs, c := a.verbSetup(name)
	pos, err := parse(fs, args)
	if err != nil {
		return refuse(stderr, name, err.Error())
	}
	if len(pos) != 0 {
		return refuse(stderr, name, "takes no resource name: it lists them all")
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, name, err.Error())
	}
	rows, err := st.ResourceRows(context.Background())
	if err != nil {
		return a.readFailed(name, err, stderr)
	}
	if c.json {
		if rows == nil {
			rows = []sprint.ResourceRow{}
		}
		sayOK(stdout, true, name, "", map[string]any{"resources": rows})
		return 0
	}
	if len(rows) == 0 {
		fmt.Fprintln(stdout, "RESOURCES none: nova-sprint resource add <name> --kind bench|branch|port|account --capacity <n>")
		return 0
	}
	now := a.now()
	for _, r := range rows {
		fmt.Fprintln(stdout, r.Line(now))
	}
	return 0
}

// resourceDry is a renew's or a release's --dry-run: whether the member could.
func (a *app) resourceDry(st interface {
	ResourceRows(context.Context) ([]sprint.ResourceRow, error)
}, name, word, res, as string, asJSON bool, stdout, stderr io.Writer, could func(sprint.ResourceRow) bool) int {
	rows, err := st.ResourceRows(context.Background())
	if err != nil {
		return a.readFailed(name, err, stderr)
	}
	for _, r := range rows {
		if r.Name == res {
			ok := could(r)
			sayOK(stdout, asJSON, name, fmt.Sprintf("%s DRY-RUN %s as=%s would_apply=%s held=%d/%d; nothing was written", word, res, as, yesNo(ok), len(r.Holders), r.Capacity),
				map[string]any{"name": res, "as": as, "would_apply": ok, "held": len(r.Holders), "capacity": r.Capacity, "dry_run": true})
			return 0
		}
	}
	fmt.Fprintf(stderr, "%s %s: no resource %s on the table; run: nova-sprint resource list\n", prog, name, res)
	return 1
}

// sayResource prints a claim's or a renew's answer: granted (exit 0, the expiry)
// or in the line (exit 1, the place and the command that waits).
func sayResource(stdout, stderr io.Writer, asJSON bool, name, word, res, as string, ans sprint.ResourceAnswer) int {
	facts := map[string]any{"name": res, "as": as, "granted": ans.Granted, "held": ans.Held, "capacity": ans.Capacity, "granted_now": nonNil(ans.GrantedNow)}
	if !ans.Granted {
		facts["place"] = ans.Place
		why := fmt.Sprintf("%s is held (%d/%d); %s is %s in the line and keeps the place without asking again: the tick grants it at a release, an expiry or a holder going down", res, ans.Held, ans.Capacity, as, ordinal(ans.Place))
		return sayLaneRefused(stdout, stderr, asJSON, name, why, "nova-sprint resource claim "+res+" --as "+as+" --for 2h --wait 30m", facts)
	}
	facts["until"] = ans.Until.UTC().Format(time.RFC3339)
	sayOK(stdout, asJSON, name, fmt.Sprintf("%s OK %s as=%s until=%s held=%d/%d: work, then nova-sprint resource release %s --as %s", word, res, as, ans.Until.UTC().Format(time.RFC3339), ans.Held, ans.Capacity, res, as), facts)
	return 0
}

func holdsLease(r sprint.ResourceRow, who string) bool {
	for _, h := range r.Holders {
		if h.Who == who {
			return true
		}
	}
	return false
}

// placeOf is who's place in the line (1 is next), 0 when it waits nowhere.
func placeOf(r sprint.ResourceRow, who string) int {
	for i, w := range r.Waiters {
		if w.Who == who {
			return i + 1
		}
	}
	return 0
}
