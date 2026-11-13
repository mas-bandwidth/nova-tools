package main

import (
	"cmp"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"
)

// The rent and return verbs, bound to tla/Rent.tla: every step that changes the record, a box or a spot request
// is one of the model's actions, named to rentDeps.step (the trace check in rent_trace_test.go).

const rentHelp = `nova-fleet rent <name> --provider latitude|aws --plan P --site S --image I --purpose TEXT --return-by TIME
               --actor A [--spot] [--price USD] [--nic VENDOR:DEVICE] [--cores N] [--first-login L] [--first-key FILE]
               [--wait DURATION] [latitude: --project ID --ssh-key ID[,ID]] [aws: --group SG --disk GB --user-data FILE]
               [-- <up flags>]

Rent one box by the hour, recorded before it exists, so none bills unseen (nova-fleet rented lists them).
  1 record   nova-config rented add: provider, market, purpose, rented_by (--actor), rented_at, return_by,
             and creating, the time of the ask: the store keeps a record that holds it. A record that exists
             is resumed, never asked for again; while its ask is under 15m old, rent and return refuse
  2 find     the boxes and live spot requests named <name> at the provider: one is taken as this rent's (a run
             that stopped after creating resumes it), never a second made beside it
  3 create   when none, and this run added the record (else: return it, then rent again): Latitude POST
             /servers (hourly); AWS run-instances tagged Name=<name>. --spot is a
             one-time spot request whose interruption terminates the box: a refused spot request is the
             refusal RENT REFUSED, never an on-demand box instead
  4 note     the box id (and spot request) into the record, its ask cleared in the same write, and the price:
             Latitude's from the box, a spot price from AWS's price history; else --price, marked recalled
  5 wait     for the box to run (--wait, default 30m). On Latitude, with --nic, the ETH0 MAC is read while
             it deploys: a prefix nic-prefixes.tsv names for another PCI id deletes the box at once
  6 hardware with --nic or --cores, over ssh as --first-login: nproc and lspci -nn; the PCI id decides.
             A mismatch deletes the box and its record, and refuses with what was found
  7 join     Latitude only: nova-fleet up with the flags after --, for <name>=<address>. An AWS box gets
             no tailnet, seat or secrets: ssh from this node only
Every step reads before it writes, so the same command resumes. Exit: 0 rented (RENT OK); 1 refused or failed
(RENT REFUSED|FAILED, with why); 2 could not run. The record is written through nova-config, so run it as
  nova-secrets exec --only NOVA_PG_PASSWORD -- nova-fleet rent ...
`

const returnHelp = `nova-fleet return <name> [--force] [--runners URL]...

Give a rented box back, every piece of it, the record last; refused while a rent's ask is under 15m old (a box
may land after this looks):
  0 work     over ssh as the record's user, as root: every git clone under /home and /root (a runner's job
             checkouts aside) with commits on no remote branch, a stash or uncommitted changes is printed
             (CLONE <dir> and its lines), and refuses, unless --force: the disk goes with the box
  1 runners  GitHub runners named <name>-* in the record's runner scopes and each --runners URL, removed from
             GitHub; refused while one runs a job, unless --force
  2 seat     the machine's seat removed from the secrets store, which is pushed (Latitude)
  3 tailnet  its tailnet device deleted (TAILSCALE_API_KEY)
  4 row      its machine row removed (nova-config machine remove), and its host keys from known_hosts
  5 request  every live spot request named <name> cancelled, and read back dead
  6 box      every box named <name> deleted (Latitude) or terminated (AWS), and read back gone
  7 record   the box and request ids and the ask it read cleared in one write, then the record removed
Each step reads first: run it again to resume. Exit: 0 returned (RETURN OK); 1 refused or failed; 2 could not run.
`

const stopHelp = `nova-fleet stop <name>

Stop a rented AWS box (stop-instances): it keeps its disk, which still bills, and its record. A Latitude box
bills until deleted, so it is refused: run nova-fleet return <name>.
Exit: 0 stopped (STOP OK); 1 refused or failed; 2 could not run.
`

// rentRecord is the record as rent and return use it; nova-config in production, a map in unit tests.
type rentRecord interface {
	get(name string) (*rentedRow, error)
	add(r rentedRow, actor string) error
	set(r rentedRow, actor string, fields ...string) error // name, value ...; refused unless r is the record read (rented_at)
	remove(name, actor string) error
}

// fleetSide is the part of return that is not the box: runners, seat, tailnet, row, host keys.
type fleetSide interface {
	teardown(r rentedRow, force bool, scopes []string) error
}

// rentDeps is what the verbs act through.
type rentDeps struct {
	prov  provider
	rec   rentRecord
	fleet fleetSide
	// hardware reads cores and PCI ids (vendor:device) over ssh; join runs up for a Latitude box.
	hardware func(address, login string) (int, []string, error)
	join     func(name, address string) error
	step     func(action string) // the model's action just taken
	sleep    func(time.Duration)
	out      io.Writer
}

// rentArgs is rent's request.
type rentArgs struct {
	spec                                  rentSpec
	provider, purpose, returnBy, actor    string
	price, nic, firstLogin, firstKey, why string
	cores                                 int
	wait                                  time.Duration
}

var errHardware = errors.New("wrong hardware")

// createWindow is how long a provider's create call may run (a guess, unmeasured: each returns in seconds tonight).
// While a record's ask is younger, another rent or a return of the name waits: the box may land after it looks.
const createWindow = 15 * time.Minute

func askEnd(r rentedRow) string {
	t, err := time.Parse(time.RFC3339, r.Creating)
	if err != nil {
		return "(unreadable)"
	}
	return t.Add(createWindow).Format(time.RFC3339)
}

// asking is whether a rent may still be in its provider's create call: an ask younger than createWindow.
func asking(r rentedRow) bool {
	t, err := time.Parse(time.RFC3339, r.Creating)
	return r.Creating != "" && (err != nil || now().Before(t.Add(createWindow)))
}

func (d rentDeps) say(format string, a ...any) { fmt.Fprintf(d.out, format+"\n", a...) }

// waitFor polls until done says so, or the deadline: the provider's state is read, never assumed.
func (d rentDeps) waitFor(what string, limit time.Duration, done func() (bool, error)) error {
	end := now().Add(limit)
	for {
		ok, err := done()
		if err != nil || ok {
			return err
		}
		if now().After(end) {
			return fmt.Errorf("%s: not after %s", what, limit)
		}
		d.sleep(10 * time.Second)
	}
}

// rent runs the steps; a refusal or failure is the error.
func (d rentDeps) rent(a rentArgs) error {
	name := a.spec.Name
	market := map[bool]string{true: "spot", false: "on-demand"}[a.spec.Spot]
	if a.provider != "aws" {
		market = ""
	}
	r, err := d.rec.get(name)
	if err != nil {
		return err
	}
	// The ask is recorded with the record, before it is made: until note writes the ids, the box may bill and only
	// creating names it, so the store keeps the record and another rent or a return waits it out (createWindow).
	added := r == nil
	if added {
		at := now().UTC().Format(time.RFC3339)
		r = &rentedRow{Name: name, Provider: a.provider, Market: market, Purpose: a.purpose, RentedBy: a.actor,
			RentedAt: at, ReturnBy: a.returnBy, Creating: at}
		if err := d.rec.add(*r, a.actor); err != nil {
			return err
		}
		d.step("RAdd")
	} else if r.Provider != a.provider || r.Market != market {
		return fmt.Errorf("%s is already rented at %s %s, not %s %s; run: nova-fleet return %s first", name, r.Provider, dash(r.Market), a.provider, dash(market), name)
	} else if asking(*r) {
		d.step("RBusy")
		return fmt.Errorf("a rent of %s asked %s for its box at %s and may still be asking; run it again after %s", name, r.Provider, r.Creating, askEnd(*r))
	} else {
		d.step("RHave")
	}
	boxes, reqs, err := d.prov.find(name)
	if err != nil {
		return err
	}
	d.step("RFind")
	var live []request
	for _, q := range reqs {
		if q.live() {
			live = append(live, q)
		}
	}
	switch {
	case len(boxes) > 1 || len(live) > 1:
		d.step("RMany")
		return fmt.Errorf("%d boxes and %d live spot requests are named %s at %s, where one is the most there may be; run: nova-fleet return %s", len(boxes), len(live), name, a.provider, name)
	case len(boxes) == 1 && r.ProviderID != boxes[0].ID:
		if r.ProviderID != "" {
			d.step("RTwo")
			return fmt.Errorf("the record holds box %s and %s has %s named %s; run: nova-fleet return %s", r.ProviderID, a.provider, boxes[0].ID, name, name)
		}
		if err := d.note(r, a, boxes[0].ID, boxes[0].Request, boxes[0]); err != nil {
			return err
		}
		d.step("RAdoptBox")
	case len(boxes) == 0 && len(live) == 1 && r.SpotRequest != live[0].ID:
		if r.SpotRequest != "" {
			d.step("RTwo")
			return fmt.Errorf("the record holds spot request %s and AWS has %s named %s; run: nova-fleet return %s", r.SpotRequest, live[0].ID, name, name)
		}
		if err := d.note(r, a, "", live[0].ID, box{}); err != nil {
			return err
		}
		d.step("RAdoptReq")
	case len(boxes) == 0 && len(live) == 0 && r.ProviderID != "":
		d.step("RGone")
		return fmt.Errorf("box %s of %s is gone (a spot interruption, or deleted by hand) and its record stays; run: nova-fleet return %s, then rent it again", r.ProviderID, name, name)
	case len(boxes) == 0 && len(live) == 0 && !added: // only the run whose add won the name asks: never two asks
		d.step("RNoBox")
		return fmt.Errorf("the record of %s holds no box at %s and none is named for it (a refused or failed create, or a spot request that ended); run: nova-fleet return %s, then rent it again", name, a.provider, name)
	case len(boxes) == 0 && len(live) == 0:
		b, err := d.prov.create(a.spec)
		if errors.Is(err, errRefused) {
			if cerr := d.rec.set(*r, a.actor, "creating", ""); cerr != nil {
				return fmt.Errorf("%s refused the box (%v) and the record kept its ask: %v; run: nova-fleet return %s", a.provider, err, cerr, name)
			}
			d.step("RRefused")
			return fmt.Errorf("%s would not rent %s %s in %s: %v; the record stays with no box: run nova-fleet return %s, or rent again later", a.provider, market, a.spec.Plan, a.spec.Site, err, name)
		}
		if err != nil {
			return err
		}
		d.step("RCreate")
		if err := d.note(r, a, b.ID, b.Request, b); err != nil {
			return err
		}
		d.step("RNote")
	default:
		d.step("RFound")
	}
	// The box runs, or a live request has none yet: wait, adopting the box when it comes.
	var b box
	err = d.waitFor("the box of "+name, cmp.Or(a.wait, 30*time.Minute), func() (bool, error) {
		if r.ProviderID == "" {
			if r.SpotRequest == "" { // note wrote a box or a request; neither is a box gone before it was read
				return false, fmt.Errorf("the record of %s holds neither box nor spot request; run: nova-fleet return %s", name, name)
			}
			st, err := d.prov.requestState(r.SpotRequest)
			if err != nil {
				return false, err
			}
			boxes, _, err := d.prov.find(name)
			if err != nil || len(boxes) == 0 {
				if err == nil && st != "open" && st != "active" {
					d.step("RWaitDead")
					err = fmt.Errorf("spot request %s ended %s with no box; run: nova-fleet return %s", r.SpotRequest, st, name)
				}
				return false, err
			}
			if err := d.note(r, a, boxes[0].ID, "", boxes[0]); err != nil {
				return false, err
			}
			d.step("RWaitBox")
		}
		var err error
		if b, err = d.prov.get(r.ProviderID); err != nil {
			return false, err
		}
		if b.State == "gone" {
			d.step("RCheckGone")
			return false, fmt.Errorf("box %s of %s went while it started (a spot interruption); run: nova-fleet return %s", r.ProviderID, name, name)
		}
		if pci, what := nicHint(b.MAC); a.nic != "" && pci != "" && pci != a.nic {
			return false, fmt.Errorf("%w: its ETH0 MAC %s is %s (%s), not %s (nic-prefixes.tsv)", errHardware, b.MAC, what, pci, a.nic)
		}
		return b.State == "running" && b.Address != "", nil
	})
	if err == nil && (a.nic != "" || a.cores > 0) {
		err = d.check(a, b)
	}
	if errors.Is(err, errHardware) {
		d.step("RCheckBad")
		d.say("RENT WRONG %s %v: returning it", name, err)
		if rerr := d.give(r, a.actor); rerr != nil {
			return fmt.Errorf("%v, and the return stopped: %v; run: nova-fleet return %s", err, rerr, name)
		}
		d.step("RRefusedHW")
		return fmt.Errorf("%v; the box is deleted and its record removed", err)
	}
	if err != nil {
		return err
	}
	if r.Address != b.Address {
		if err := d.rec.set(*r, a.actor, "address", b.Address, "user", a.firstLogin); err != nil {
			return err
		}
	}
	if a.provider == "latitude" {
		if err := d.join(name, b.Address); err != nil {
			return err
		}
	}
	d.step("RCheckOK")
	d.say("RENT OK %s provider=%s id=%s market=%s address=%s rate=%s price_from=%s", name, a.provider, r.ProviderID, dash(market), b.Address, dash(r.Price), dash(r.PriceFrom))
	return nil
}

// note records the box and request ids, with the price read from the provider where it gives one.
func (d rentDeps) note(r *rentedRow, a rentArgs, id, req string, b box) error {
	set := []string{}
	if id != "" && r.ProviderID == "" {
		set, r.ProviderID = append(set, "provider_id", id), id
	}
	if req != "" && r.SpotRequest == "" {
		set, r.SpotRequest = append(set, "spot_request", req), req
	}
	if r.Creating != "" && (r.ProviderID != "" || r.SpotRequest != "") { // the ids and the end of the ask in one write
		set, r.Creating = append(set, "creating", ""), ""
	}
	if r.Price == "" {
		p, from := b.Price, "provider"
		if p == 0 {
			var err error
			if p, err = d.prov.price(a.spec); err != nil {
				d.say("RENT NOTE %s price not read: %v", r.Name, err)
			}
		}
		if p == 0 && a.price != "" {
			p, _ = strconv.ParseFloat(a.price, 64) // checked by the flag parser
			from = "recalled"
		}
		if p > 0 {
			r.Price, r.PriceFrom = strconv.FormatFloat(p, 'f', -1, 64), from
			set = append(set, "price", r.Price, "price_from", from)
		}
	}
	if len(set) == 0 {
		return nil
	}
	return d.rec.set(*r, a.actor, set...)
}

// check reads the hardware over ssh; its PCI ids decide, never the MAC.
func (d rentDeps) check(a rentArgs, b box) error {
	var cores int
	var pci []string
	err := d.waitFor("ssh to "+b.Address, 15*time.Minute, func() (bool, error) {
		var err error
		cores, pci, err = d.hardware(b.Address, a.firstLogin)
		return err == nil, nil
	})
	if err != nil {
		return err
	}
	if a.nic != "" && !slices.Contains(pci, a.nic) {
		return fmt.Errorf("%w: no %s among its PCI ids (lspci -nn read %s)", errHardware, a.nic, cmp.Or(strings.Join(pci, " "), "none"))
	}
	if a.cores > 0 && cores != a.cores {
		return fmt.Errorf("%w: nproc read %d, not %d", errHardware, cores, a.cores)
	}
	return nil
}

// give takes back the box and request of a record and then the record: cancel and read back dead, clear; delete
// and read back gone, clear; remove. A request is cancelled before its box goes, so none launches another.
func (d rentDeps) give(r *rentedRow, actor string) error {
	boxes, reqs, err := d.prov.find(r.Name)
	if err != nil {
		return err
	}
	d.step("XFind")
	ids := []string{}
	if r.SpotRequest != "" {
		ids = append(ids, r.SpotRequest)
	}
	for _, q := range reqs {
		if q.live() && !slices.Contains(ids, q.ID) {
			ids = append(ids, q.ID)
		}
	}
	for _, id := range ids {
		st, err := d.prov.requestState(id)
		if err != nil {
			return err
		}
		if st == "open" || st == "active" {
			if err := d.prov.cancel(id); err != nil {
				return err
			}
			if err := d.waitFor("spot request "+id+" to end", 5*time.Minute, func() (bool, error) {
				st, err := d.prov.requestState(id)
				return st != "open" && st != "active", err
			}); err != nil {
				return err
			}
			d.step("XCancel")
		}
	}
	gone := []string{}
	if r.ProviderID != "" {
		gone = append(gone, r.ProviderID)
	}
	for _, b := range boxes {
		if !slices.Contains(gone, b.ID) {
			gone = append(gone, b.ID)
		}
	}
	for _, id := range gone {
		b, err := d.prov.get(id)
		if err != nil {
			return err
		}
		if b.State == "gone" {
			continue
		}
		if err := d.prov.remove(id); err != nil {
			return err
		}
		if err := d.waitFor("box "+id+" to go", 20*time.Minute, func() (bool, error) {
			b, err := d.prov.get(id)
			return b.State == "gone", err
		}); err != nil {
			return err
		}
		d.step("XRemove")
	}
	// Every request named for it is dead and every box gone, so the handles and the ask read at the start are over,
	// cleared in one write: until then the record names each. Only those are cleared: one written since (a rent's
	// note) names a box this return did not see, and keeps the record.
	clear := []string{}
	if r.ProviderID != "" {
		clear = append(clear, "provider_id", "")
	}
	if r.SpotRequest != "" {
		clear = append(clear, "spot_request", "")
	}
	if r.Creating != "" {
		clear = append(clear, "creating", "")
	}
	if len(clear) > 0 {
		if err := d.rec.set(*r, actor, clear...); err != nil {
			return err
		}
		r.ProviderID, r.SpotRequest, r.Creating = "", "", ""
		d.step("XClearBox")
	}
	if err := d.rec.remove(r.Name, actor); err != nil {
		return err
	}
	d.step("XDrop")
	return nil
}

// giveBack is return: the fleet's pieces first (a busy runner refuses it all), then the box, the record last.
func (d rentDeps) giveBack(name, actor string, force bool, scopes []string) error {
	r, err := d.rec.get(name)
	if err != nil {
		return err
	}
	if r == nil {
		d.step("XNone")
		d.say("RETURN OK %s: no record, nothing rented by that name", name)
		return nil
	}
	if asking(*r) {
		d.step("XBusy")
		return fmt.Errorf("a rent of %s asked %s for its box at %s and may still be asking, so a box may land after this looks; run it again after %s", name, r.Provider, r.Creating, askEnd(*r))
	}
	d.step("XRead")
	if err := d.fleet.teardown(*r, force, scopes); err != nil {
		return err
	}
	if err := d.give(r, actor); err != nil {
		return err
	}
	d.say("RETURN OK %s provider=%s: box, spot request and record gone", name, r.Provider)
	return nil
}

// stopBox stops an AWS box, record kept.
func (d rentDeps) stopBox(name string) error {
	r, err := d.rec.get(name)
	switch {
	case err != nil:
		return err
	case r == nil:
		return fmt.Errorf("%s is not rented (nova-fleet rented lists what is)", name)
	case r.Provider != "aws":
		return d.prov.stop(r.ProviderID)
	case r.ProviderID == "":
		return fmt.Errorf("the record of %s holds no box; run: nova-fleet return %s", name, name)
	}
	if err := d.prov.stop(r.ProviderID); err != nil {
		return err
	}
	if err := d.waitFor("box "+r.ProviderID+" to stop", 10*time.Minute, func() (bool, error) {
		b, err := d.prov.get(r.ProviderID)
		return b.State == "stopped" || b.State == "gone", err
	}); err != nil {
		return err
	}
	d.say("STOP OK %s id=%s: its disk still bills; nova-fleet return %s ends it", name, r.ProviderID, name)
	return nil
}
