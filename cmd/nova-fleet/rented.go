package main

import (
	"cmp"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"strconv"
	"time"
)

const rentedHelp = `nova-fleet rented [--json]

The boxes rented by the hour, from the record (nova-config rented rows), so none bills unseen: one
line per box,
  RENTED <name> provider=<latitude|aws> id=<provider id|-> req=<spot request|-> asked=<time|-> market=<on-demand|spot|->
         age=<h>h<m>m rate=<usd an hour|-> price_from=<provider|recalled|-> cost=<usd so far|unknown> return_by=<time|->
         [PAST] purpose="<text>"
then RENTED TOTAL machines=<n> rate=<usd an hour> cost=<usd> unknown=<n> past=<n>. cost is the hourly
price times the hours since rented_at (a stopped box bills less: its disk only); unknown, when either is
missing, and then not in the total, which counts it in unknown; price_from=recalled is a price not read
from the provider; PAST marks a box kept beyond its return_by; asked is when a rent asked the provider
for the box, until it noted its ids (a box that may bill with no id yet). A nova-config on PATH older than the
rented rows has no rented verb, and its refusal is passed on. --json prints the same as one object.
Reads the store through nova-config, so run it as the plays run:
  nova-secrets exec --only NOVA_PG_PASSWORD -- nova-fleet rented
Exit: 0 listed (also when none is rented); 2 could not read the record.
`

// now is the clock of the rented verbs; unit tests replace it.
var now = time.Now

// rentedRow is a rented row of the record as nova-config rented list --json prints its fields.
type rentedRow struct {
	Name        string `json:"name"`
	Provider    string `json:"provider"`
	ProviderID  string `json:"provider_id"`
	Market      string `json:"market"`
	SpotRequest string `json:"spot_request"`
	Address     string `json:"address"`
	User        string `json:"user"`
	Price       string `json:"price"`
	PriceFrom   string `json:"price_from"`
	Purpose     string `json:"purpose"`
	RentedBy    string `json:"rented_by"`
	RentedAt    string `json:"rented_at"`
	ReturnBy    string `json:"return_by"`
	Creating    string `json:"creating"` // when rent asked the provider for the box, until its ids are noted
}

// cmdFunc runs a program to its end: its stdout, stderr and exit code (outCmd).
type cmdFunc func(bin string, args, env []string) (string, string, int)

// record reads the rented rows through nova-config at cfg, with the caller's environment.
func record(cfg string, run cmdFunc) ([]rentedRow, error) {
	out, why, c := run(cfg, []string{"rented", "list", "--json"}, os.Environ())
	if c != 0 {
		return nil, fmt.Errorf("nova-config rented list --json exit %d: %s", c, cmp.Or(oneLine(why), "(nothing on stderr)"))
	}
	var v struct {
		Items []struct{ Fields rentedRow }
	}
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		return nil, fmt.Errorf("nova-config rented list --json printed no JSON (%v)", err)
	}
	var rows []rentedRow
	for _, it := range v.Items {
		rows = append(rows, it.Fields)
	}
	return rows, nil
}

// bill is what a rented machine has cost by at: its hourly price times the hours since rented_at. Known is false
// when either is missing, and then the cost is not counted.
type bill struct {
	Name, Provider, ProviderID, SpotRequest, Creating, Market, Price, PriceFrom, Purpose, ReturnBy string
	Hours, Rate, Cost                                                                              float64
	Known, Past                                                                                    bool
}

func billOf(r rentedRow, at time.Time) bill {
	b := bill{Name: r.Name, Provider: r.Provider, ProviderID: r.ProviderID, SpotRequest: r.SpotRequest, Creating: r.Creating, Market: r.Market, Price: r.Price, PriceFrom: r.PriceFrom, Purpose: r.Purpose, ReturnBy: r.ReturnBy}
	t, terr := time.Parse(time.RFC3339, r.RentedAt)
	p, perr := strconv.ParseFloat(r.Price, 64)
	if terr == nil {
		b.Hours = max(at.Sub(t).Hours(), 0)
	}
	if perr == nil {
		b.Rate = p
	}
	if b.Known = terr == nil && perr == nil; b.Known {
		b.Cost = p * b.Hours
	}
	if t, err := time.Parse(time.RFC3339, r.ReturnBy); err == nil && at.After(t) {
		b.Past = true
	}
	return b
}

func dash(s string) string { return cmp.Or(s, "-") }

func usd(f float64) string { return strconv.FormatFloat(f, 'f', 2, 64) }

// rate is dollars an hour to the record's four places, without trailing zeros.
func rate(f float64) string { return strconv.FormatFloat(math.Round(f*1e4)/1e4, 'f', -1, 64) }

// listRented prints the rented rows of rows as at, as lines or one JSON object.
func listRented(rows []rentedRow, at time.Time, asJSON bool, w io.Writer) error {
	bills := []bill{}
	var total struct {
		Machines, Unknown, Past int
		Rate, Cost              float64
	}
	for _, r := range rows {
		b := billOf(r, at)
		bills = append(bills, b)
		total.Machines++
		total.Rate += b.Rate
		total.Cost += b.Cost
		if !b.Known {
			total.Unknown++
		}
		if b.Past {
			total.Past++
		}
	}
	if asJSON {
		out, err := json.MarshalIndent(map[string]any{"machines": bills, "total": total}, "", "  ")
		if err == nil {
			_, err = fmt.Fprintf(w, "%s\n", out)
		}
		return err
	}
	for _, b := range bills {
		cost, past := "unknown", ""
		if b.Known {
			cost = usd(b.Cost)
		}
		if b.Past {
			past = " PAST"
		}
		h := int(b.Hours * 60)
		purpose, _ := json.Marshal(b.Purpose) // ignored: a string always encodes
		fmt.Fprintf(w, "RENTED %s provider=%s id=%s req=%s asked=%s market=%s age=%dh%02dm rate=%s price_from=%s cost=%s return_by=%s%s purpose=%s\n",
			b.Name, b.Provider, dash(b.ProviderID), dash(b.SpotRequest), dash(b.Creating), dash(b.Market), h/60, h%60, dash(b.Price), dash(b.PriceFrom), cost, dash(b.ReturnBy), past, purpose)
	}
	_, err := fmt.Fprintf(w, "RENTED TOTAL machines=%d rate=%s cost=%s unknown=%d past=%d\n", total.Machines, rate(total.Rate), usd(total.Cost), total.Unknown, total.Past)
	return err
}

func runRented(args []string, stdout, stderr io.Writer) int {
	asJSON := false
	for _, a := range args {
		switch a {
		case "-h", "--help":
			fmt.Fprint(stdout, rentedHelp)
			return 0
		case "--json":
			asJSON = true
		default:
			fmt.Fprintf(stderr, "nova-fleet rented: unexpected argument %s; run: nova-fleet rented -h\n", a)
			return 2
		}
	}
	cfg, err := lookPath("nova-config")
	if err != nil {
		fmt.Fprintf(stderr, "nova-fleet rented: nova-config is not on PATH (%v); run: nova-fleet check\n", err)
		return 2
	}
	rows, err := record(cfg, outCmd)
	if err != nil {
		fmt.Fprintf(stderr, "nova-fleet rented: the record cannot be read: %v; with the store's password in the environment (nova-secrets exec --only NOVA_PG_PASSWORD -- nova-fleet rented); run: nova-config rented list\n", err)
		return 2
	}
	if err := listRented(rows, now().UTC(), asJSON, stdout); err != nil {
		fmt.Fprintf(stderr, "nova-fleet rented: write the list: %v; run: nova-fleet rented > <a file>\n", err)
		return 2
	}
	return 0
}
